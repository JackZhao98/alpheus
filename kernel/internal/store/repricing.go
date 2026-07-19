package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"alpheus/kernel/internal/units"
)

type RepriceReplacement struct {
	AttemptID     string
	OrderID       string
	ClientOrderID string
	Limit         units.Micros
}

// StageRepriceCancel creates exactly one durable cancel effect for a working
// order. A second worker sees the unresolved attempt and returns nil.
func (s *Store) StageRepriceCancel(orderID string) (*ExecutionAttempt, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	var ledger string
	if err := tx.QueryRowContext(ctx, `SELECT ledger FROM orders WHERE id=$1`, orderID).Scan(&ledger); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(ledger == "shadow")); err != nil {
		return nil, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, kernelPolicyLockKey); err != nil {
		return nil, normalizeDBError(err)
	}
	order, err := scanOrder(tx.QueryRowContext(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE id=$1 FOR UPDATE`, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if (order.State != "submitted" && order.State != "partially_filled") || order.BrokerOrderID == "" {
		return nil, nil
	}
	var unresolved bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM execution_attempt
		WHERE operation_id=$1 AND intent='cancel' AND target_broker_order_id=$2
		  AND state IN ('pending','claimed','unknown'))`, order.OperationID, order.BrokerOrderID).Scan(&unresolved); err != nil {
		return nil, normalizeDBError(err)
	}
	if unresolved {
		return nil, nil
	}
	var seq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(seq),0)+1 FROM execution_attempt
		WHERE operation_id=$1`, order.OperationID).Scan(&seq); err != nil {
		return nil, normalizeDBError(err)
	}
	attempt := ExecutionAttempt{
		ID: NewID(), OperationID: order.OperationID, Seq: seq,
		Intent: "cancel", TargetBrokerOrderID: order.BrokerOrderID, State: "pending",
	}
	gate := &ledgerTx{tx: tx, ctx: ctx, marketTZ: s.marketTZ}
	if err := gate.InsertExecutionAttempt(attempt); err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, "reprice_cancel_staged", map[string]any{
		"operation_id": order.OperationID, "order_id": order.ID,
		"attempt_id": attempt.ID, "broker_order_id": order.BrokerOrderID,
	}); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return &attempt, nil
}

// FinalizeRepriceCancel atomically ingests all fills raced with cancellation,
// makes the old order terminal, and either releases the remaining reservation
// or transfers it to one replacement attempt/order.
func (s *Store) FinalizeRepriceCancel(cancelAttemptID string, fencingToken int, update OrderUpdate, replacement *RepriceReplacement, policyReason string) (*ExecutionAttempt, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	var targetBrokerOrderID, ledger string
	if err := tx.QueryRowContext(ctx, `SELECT a.target_broker_order_id,o.ledger
		FROM execution_attempt a JOIN orders o ON o.broker_order_id=a.target_broker_order_id
		WHERE a.id=$1`, cancelAttemptID).Scan(&targetBrokerOrderID, &ledger); err != nil {
		return nil, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(ledger == "shadow")); err != nil {
		return nil, normalizeDBError(err)
	}
	var activeAttemptID, unknownAttemptID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT active_attempt_id,unknown_attempt_id
		FROM live_execution_gate WHERE singleton=true FOR UPDATE`).Scan(&activeAttemptID, &unknownAttemptID); err != nil {
		return nil, normalizeDBError(err)
	}
	cancelAttempt, err := scanAttempt(tx.QueryRowContext(ctx, `SELECT `+attemptColumns+`
		FROM execution_attempt WHERE id=$1 AND attempt=$2 AND state='claimed' FOR UPDATE`,
		cancelAttemptID, fencingToken))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if cancelAttempt.Intent != "cancel" || cancelAttempt.TargetBrokerOrderID != targetBrokerOrderID {
		return nil, fmt.Errorf("reprice cancel attempt identity changed")
	}
	source, err := scanOrder(tx.QueryRowContext(ctx, `SELECT `+orderColumns+`
		FROM orders WHERE broker_order_id=$1`, targetBrokerOrderID))
	if err != nil {
		return nil, normalizeDBError(err)
	}
	update.ExecutionAttemptID = source.ExecutionAttemptID
	update.BrokerOrderID = targetBrokerOrderID
	if update.State != "cancelled" && update.State != "filled" &&
		update.State != "rejected" && update.State != "expired" {
		return nil, fmt.Errorf("reprice cancel lacks terminal order proof")
	}
	updated, err := applyOrderUpdate(ctx, tx, update, true)
	if err != nil {
		// The failed mutation must be rolled back before recording an integrity
		// event and its database-fenced Global Halt in a separate transaction.
		// Calling the recorder while this transaction is alive would self-block
		// on the ledger/global advisory lock order.
		_ = tx.Rollback()
		return nil, s.recordOrderUpdateFailure(update, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE execution_attempt SET state='settled',resolved_at=now()
		WHERE id=$1 AND state='placed'`, source.ExecutionAttemptID); err != nil {
		return nil, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE execution_attempt SET state='settled',
		broker_order_id=$3,last_error=NULL,resolved_at=now()
		WHERE id=$1 AND attempt=$2 AND state='claimed'`, cancelAttemptID, fencingToken, targetBrokerOrderID); err != nil {
		return nil, normalizeDBError(err)
	}
	if activeAttemptID.String == cancelAttemptID || unknownAttemptID.String == cancelAttemptID {
		if _, err := tx.ExecContext(ctx, `UPDATE live_execution_gate SET
			active_attempt_id=CASE WHEN active_attempt_id=$1 THEN NULL ELSE active_attempt_id END,
			active_since=CASE WHEN active_attempt_id=$1 THEN NULL ELSE active_since END,
			unknown_attempt_id=CASE WHEN unknown_attempt_id=$1 THEN NULL ELSE unknown_attempt_id END,
			unknown_since=CASE WHEN unknown_attempt_id=$1 THEN NULL ELSE unknown_since END,
			updated_at=now() WHERE singleton=true`, cancelAttemptID); err != nil {
			return nil, normalizeDBError(err)
		}
	}
	if err := insertEvent(ctx, tx, "execution_attempt_resolved", map[string]any{
		"attempt_id": cancelAttemptID, "operation_id": cancelAttempt.OperationID,
		"state": "settled", "broker_order_id": targetBrokerOrderID,
		"fencing_token": fencingToken,
	}); err != nil {
		return nil, normalizeDBError(err)
	}

	var durableFilled int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(qty),0) FROM fills
		WHERE order_id=$1`, source.ID).Scan(&durableFilled); err != nil {
		return nil, normalizeDBError(err)
	}
	remaining := units.Qty(int64(source.Qty) - durableFilled)
	if remaining < 0 {
		return nil, fmt.Errorf("reprice fill exceeds source order quantity")
	}
	failureStatus, err := operationStatusAfterFailedAttempt(ctx, tx, source.OperationID)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if failureStatus == "executed" {
		// Once any fill is durable, every later replacement is continuation of
		// an executed broker fact. A pending or claimed remainder may fail, but
		// the operation itself must never move back to failed.
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='executed' WHERE id=$1`, source.OperationID); err != nil {
			return nil, normalizeDBError(err)
		}
	}
	var closeReservationID, openReservationID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT close_reservation_id,open_reservation_id
		FROM execution_attempt WHERE id=$1`, source.ExecutionAttemptID).Scan(
		&closeReservationID, &openReservationID); err != nil {
		return nil, normalizeDBError(err)
	}
	if update.State == "filled" || remaining == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='executed' WHERE id=$1`, source.OperationID); err != nil {
			return nil, normalizeDBError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, normalizeDBError(err)
		}
		return nil, nil
	}
	if replacement != nil && source.KernelPolicyRevisionID > 0 {
		current, err := activeKernelPolicyRevision(ctx, tx)
		if err != nil {
			return nil, normalizeDBError(err)
		}
		current.ObservedAt, err = databaseClock(ctx, tx)
		if err != nil {
			return nil, normalizeDBError(err)
		}
		if err := validateKernelPolicyAuthority(current); err != nil {
			return nil, err
		}
		effectiveMax := source.MaxReprices
		if current.Policy.ExecutionPolicy.MaxReprices < effectiveMax {
			effectiveMax = current.Policy.ExecutionPolicy.MaxReprices
		}
		nextReprice := updated.Reprices + 1
		if nextReprice > effectiveMax {
			replacement, policyReason = nil, "max_reprices"
		}
		if replacement != nil && current.Policy.ExecutionPolicy.RepriceIntervalSec > source.RepriceIntervalSec {
			now, clockErr := databaseClock(ctx, tx)
			if clockErr != nil {
				return nil, normalizeDBError(clockErr)
			}
			eligibleAt := source.WorkingSince.Add(time.Duration(current.Policy.ExecutionPolicy.RepriceIntervalSec) * time.Second)
			if !source.WorkingSince.IsZero() && now.Before(eligibleAt) {
				replacement, policyReason = nil, "reprice_interval_tightened"
			}
		}
		if replacement != nil && current.Policy.QuoteMaxAgeSec < source.QuoteMaxAgeSec {
			// The quote timestamp is not part of this transaction. After a quote-age
			// tightening races the broker cancel, fail closed instead of granting the
			// pre-change observation a wider lifetime.
			replacement, policyReason = nil, "quote_age_tightened"
		}
	}
	if replacement == nil {
		if policyReason == "" {
			return nil, fmt.Errorf("terminal reprice cancel has no replacement or policy reason")
		}
		if err := releaseRepriceReservation(ctx, tx, closeReservationID, openReservationID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET status=$2 WHERE id=$1`, source.OperationID, failureStatus); err != nil {
			return nil, normalizeDBError(err)
		}
		if err := insertEvent(ctx, tx, "order_expired_policy", map[string]any{
			"operation_id": source.OperationID, "order_id": source.ID,
			"broker_order_id": targetBrokerOrderID, "reason": policyReason,
			"remaining_qty": remaining,
		}); err != nil {
			return nil, normalizeDBError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, normalizeDBError(err)
		}
		return nil, nil
	}
	if replacement.AttemptID == "" || replacement.OrderID == "" || replacement.ClientOrderID == "" || replacement.Limit <= 0 {
		return nil, fmt.Errorf("replacement identity or limit is incomplete")
	}
	if err := validateRepriceReplacementBounds(ctx, tx, source, replacement.Limit); err != nil {
		return nil, err
	}
	reservedQty, err := heldRepriceQuantity(ctx, tx, closeReservationID, openReservationID)
	if err != nil {
		return nil, err
	}
	if reservedQty != remaining {
		return nil, fmt.Errorf("replacement quantity differs from held reservation")
	}
	reprices := updated.Reprices + 1
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET reprices=$2 WHERE id=$1`, source.ID, reprices); err != nil {
		return nil, normalizeDBError(err)
	}
	next := ExecutionAttempt{
		ID: replacement.AttemptID, OperationID: source.OperationID, Seq: cancelAttempt.Seq + 1,
		CloseReservationID: closeReservationID.String, OpenReservationID: openReservationID.String,
		Intent: "place", ClientOrderID: replacement.ClientOrderID, State: "pending",
		Qty: remaining, Limit: replacement.Limit,
	}
	gate := &ledgerTx{tx: tx, ctx: ctx, marketTZ: s.marketTZ}
	if err := gate.InsertExecutionAttempt(next); err != nil {
		return nil, err
	}
	if err := gate.InsertOrder(Order{
		ID: replacement.OrderID, OperationID: source.OperationID, ExecutionAttemptID: next.ID,
		ClientOrderID: next.ClientOrderID, Ledger: source.Ledger, Symbol: source.Symbol,
		Side: source.Side, Kind: source.Kind, Multiplier: source.Multiplier,
		Qty: remaining, Limit: replacement.Limit, ApprovedPriceBound: source.ApprovedPriceBound,
		State: "new", Reprices: reprices,
	}); err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, "order_replacement_staged", map[string]any{
		"operation_id": source.OperationID, "source_order_id": source.ID,
		"replacement_order_id": replacement.OrderID, "attempt_id": next.ID,
		"reprices": reprices, "qty": remaining, "limit_micros": replacement.Limit,
	}); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return &next, nil
}

func validateRepriceReplacementBounds(ctx context.Context, tx *sql.Tx, source *Order, limit units.Micros) error {
	var payload []byte
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM operations WHERE id=$1`, source.OperationID).Scan(&payload); err != nil {
		return normalizeDBError(err)
	}
	var bounds struct {
		Action           string        `json:"action"`
		Side             string        `json:"side"`
		Limit            *units.Micros `json:"limit"`
		ApprovedPriceCap units.Micros  `json:"approved_price_cap"`
	}
	if err := json.Unmarshal(payload, &bounds); err != nil {
		return fmt.Errorf("decode reprice bounds: %w", err)
	}
	if bounds.Side != source.Side {
		return fmt.Errorf("replacement side differs from persisted operation")
	}
	switch bounds.Action {
	case "open":
		if bounds.Side != "buy" || bounds.ApprovedPriceCap <= 0 || limit > bounds.ApprovedPriceCap {
			return fmt.Errorf("replacement exceeds approved open price cap")
		}
	case "close":
		if (bounds.Side != "buy" && bounds.Side != "sell") || bounds.Limit == nil || *bounds.Limit <= 0 {
			return fmt.Errorf("close without an explicit limit is not replaceable")
		}
		if (bounds.Side == "buy" && limit > *bounds.Limit) ||
			(bounds.Side == "sell" && limit < *bounds.Limit) {
			return fmt.Errorf("replacement exceeds explicit close price bound")
		}
	default:
		return fmt.Errorf("operation action %q is not replaceable", bounds.Action)
	}
	return nil
}

func releaseRepriceReservation(ctx context.Context, tx *sql.Tx, closeID, openID sql.NullString) error {
	if closeID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE close_reservation SET state='released',
			remaining_qty=0,released_at=COALESCE(released_at,now())
			WHERE id=$1 AND state='held'`, closeID.String); err != nil {
			return normalizeDBError(err)
		}
	}
	if openID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE open_reservation SET resource_state='released',
			remaining_risk_micros=0,remaining_cash_micros=0,settled_at=COALESCE(settled_at,now())
			WHERE id=$1 AND resource_state='held'`, openID.String); err != nil {
			return normalizeDBError(err)
		}
	}
	return nil
}

func heldRepriceQuantity(ctx context.Context, tx *sql.Tx, closeID, openID sql.NullString) (units.Qty, error) {
	var quantity int64
	switch {
	case closeID.Valid && !openID.Valid:
		err := tx.QueryRowContext(ctx, `SELECT remaining_qty FROM close_reservation
			WHERE id=$1 AND state='held'`, closeID.String).Scan(&quantity)
		return units.Qty(quantity), normalizeDBError(err)
	case openID.Valid && !closeID.Valid:
		err := tx.QueryRowContext(ctx, `SELECT remaining_qty FROM open_reservation
			WHERE id=$1 AND resource_state='held'`, openID.String).Scan(&quantity)
		return units.Qty(quantity), normalizeDBError(err)
	default:
		return 0, fmt.Errorf("source placement does not have exactly one reservation")
	}
}
