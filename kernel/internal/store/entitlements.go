package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

type TradeGrant struct {
	OperationID    string
	Ledger         string
	MarketDay      time.Time
	AuthorizedRisk units.Micros
	RiskSource     string
}

type TradeGrantUsage struct {
	AuthorizedRisk   units.Micros
	HasLegacyUnknown bool
	GrantCount       int
}

type CloseReservation struct {
	ID           string
	OperationID  string
	Ledger       string
	Symbol       string
	OriginalQty  units.Qty
	RemainingQty units.Qty
	State        string
	CreatedAt    time.Time
	ReleasedAt   time.Time
}

type ExecutionAttempt struct {
	ID                     string
	OperationID            string
	Seq                    int
	CloseReservationID     string
	OpenReservationID      string
	Intent                 string
	ClientOrderID          string
	TargetBrokerOrderID    string
	State                  string
	BrokerOrderID          string
	Qty                    units.Qty
	Limit                  units.Micros
	Attempt                int
	ClaimedBy              string
	CreatedAt              time.Time
	ClaimedAt              time.Time
	ResolvedAt             time.Time
	LastError              string
	IntentFingerprint      []byte
	ProviderAccountID      string
	ProviderIntent         json.RawMessage
	SentAt                 time.Time
	SendWindowStart        time.Time
	SendWindowEnd          time.Time
	ReplayCount            int
	ProviderErrorCode      string
	CandidateBrokerOrderID string
	CandidateObservedAt    time.Time
}

type AttemptResolution struct {
	State                  string
	BrokerOrderID          string
	LastError              string
	ProviderErrorCode      string
	CandidateBrokerOrderID string
	OperatorSubject        string
	OperationStatus        string
	ReleaseReservation     bool
	OrderUpdate            *OrderUpdate
	OrderEvent             any
}

func ledgerName(shadow bool) string {
	if shadow {
		return "shadow"
	}
	return "live"
}

func (t *ledgerTx) LockLedgerSymbol(ledger, symbol string) error {
	_, err := t.tx.ExecContext(t.ctx, `SELECT pg_advisory_xact_lock($1)`, symbolLockKey(ledger, symbol))
	return normalizeDBError(err)
}

func (t *ledgerTx) HeldCloseQuantity(ledger, symbol string) (units.Qty, error) {
	var quantity int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(sum(remaining_qty),0)
		FROM close_reservation WHERE ledger=$1 AND symbol=$2 AND state='held'`, ledger, symbol).Scan(&quantity)
	return units.Qty(quantity), normalizeDBError(err)
}

func (t *ledgerTx) InsertTradeGrant(grant TradeGrant) error {
	var authorizedRisk any
	if grant.RiskSource == "computed" {
		authorizedRisk = int64(grant.AuthorizedRisk)
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO trade_grant
		(operation_id,ledger,market_day,authorized_risk_micros,risk_source)
		VALUES ($1,$2,$3,$4,$5)`,
		grant.OperationID, grant.Ledger, grant.MarketDay, authorizedRisk, grant.RiskSource)
	return normalizeDBError(err)
}

func (t *ledgerTx) TradeGrantUsage(ledger string, marketDay time.Time, excludeOperationID string) (TradeGrantUsage, error) {
	var usage TradeGrantUsage
	var authorizedRisk int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT
		COALESCE(sum(authorized_risk_micros) FILTER (WHERE risk_source='computed'),0),
		COALESCE(bool_or(risk_source='legacy_unknown'),false),
		count(*)
		FROM trade_grant
		WHERE ledger=$1 AND market_day=$2::date
		  AND (NULLIF($3,'') IS NULL OR operation_id <> NULLIF($3,'')::uuid)`,
		ledger, marketDay, excludeOperationID).Scan(&authorizedRisk, &usage.HasLegacyUnknown, &usage.GrantCount)
	usage.AuthorizedRisk = units.Micros(authorizedRisk)
	return usage, normalizeDBError(err)
}

func (t *ledgerTx) InsertCloseReservation(reservation CloseReservation) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO close_reservation
		(id,operation_id,ledger,symbol,original_qty,remaining_qty,state)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, reservation.ID, reservation.OperationID,
		reservation.Ledger, reservation.Symbol, int64(reservation.OriginalQty),
		int64(reservation.RemainingQty), reservation.State)
	return normalizeDBError(err)
}

func (t *ledgerTx) InsertExecutionAttempt(attempt ExecutionAttempt) error {
	var clientOrderID, targetBrokerOrderID, closeReservationID, openReservationID, quantity, limit any
	if attempt.ClientOrderID != "" {
		clientOrderID = attempt.ClientOrderID
	}
	if attempt.TargetBrokerOrderID != "" {
		targetBrokerOrderID = attempt.TargetBrokerOrderID
	}
	if attempt.CloseReservationID != "" {
		closeReservationID = attempt.CloseReservationID
	}
	if attempt.OpenReservationID != "" {
		openReservationID = attempt.OpenReservationID
	}
	if attempt.Intent == "place" || attempt.Intent == "paper_place" {
		quantity, limit = int64(attempt.Qty), int64(attempt.Limit)
	}
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO execution_attempt
		(id,operation_id,seq,close_reservation_id,open_reservation_id,intent,client_order_id,
		 target_broker_order_id,state,qty,limit_micros,intent_fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, attempt.ID, attempt.OperationID,
		attempt.Seq, closeReservationID, openReservationID, attempt.Intent, clientOrderID,
		targetBrokerOrderID, attempt.State, quantity, limit, nullableBytes(attempt.IntentFingerprint))
	return normalizeDBError(err)
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func symbolLockKey(ledger, symbol string) int64 {
	digest := sha256Bytes("symbol\x00" + ledger + "\x00" + symbol)
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func sha256Bytes(value string) [32]byte {
	return sha256.Sum256([]byte(value))
}

const attemptColumns = `id,operation_id,seq,close_reservation_id,open_reservation_id,intent,client_order_id,
	target_broker_order_id,state,broker_order_id,qty,limit_micros,attempt,claimed_by,
	created_at,claimed_at,resolved_at,last_error,intent_fingerprint,provider_account_id,provider_intent,
	sent_at,send_window_start,send_window_end,replay_count,provider_error_code,
	candidate_broker_order_id,candidate_observed_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanAttempt(row scanner) (*ExecutionAttempt, error) {
	var attempt ExecutionAttempt
	var reservationID, openReservationID, clientOrderID, targetOrderID, brokerOrderID, claimedBy, lastError sql.NullString
	var quantity, limit sql.NullInt64
	var claimedAt, resolvedAt, sentAt, sendWindowStart, sendWindowEnd, candidateObservedAt sql.NullTime
	var fingerprint []byte
	var providerAccountID, providerErrorCode, candidateBrokerOrderID sql.NullString
	var providerIntent []byte
	err := row.Scan(&attempt.ID, &attempt.OperationID, &attempt.Seq, &reservationID, &openReservationID,
		&attempt.Intent, &clientOrderID, &targetOrderID, &attempt.State, &brokerOrderID,
		&quantity, &limit, &attempt.Attempt, &claimedBy, &attempt.CreatedAt,
		&claimedAt, &resolvedAt, &lastError, &fingerprint, &providerAccountID, &providerIntent,
		&sentAt, &sendWindowStart, &sendWindowEnd, &attempt.ReplayCount, &providerErrorCode,
		&candidateBrokerOrderID, &candidateObservedAt)
	if err != nil {
		return nil, err
	}
	attempt.CloseReservationID = reservationID.String
	attempt.OpenReservationID = openReservationID.String
	attempt.ClientOrderID = clientOrderID.String
	attempt.TargetBrokerOrderID = targetOrderID.String
	attempt.BrokerOrderID = brokerOrderID.String
	attempt.ClaimedBy = claimedBy.String
	attempt.LastError = lastError.String
	attempt.IntentFingerprint = append([]byte(nil), fingerprint...)
	attempt.ProviderAccountID = providerAccountID.String
	attempt.ProviderIntent = append(json.RawMessage(nil), providerIntent...)
	attempt.ProviderErrorCode = providerErrorCode.String
	attempt.CandidateBrokerOrderID = candidateBrokerOrderID.String
	attempt.Qty = units.Qty(quantity.Int64)
	attempt.Limit = units.Micros(limit.Int64)
	if claimedAt.Valid {
		attempt.ClaimedAt = claimedAt.Time
	}
	if resolvedAt.Valid {
		attempt.ResolvedAt = resolvedAt.Time
	}
	if sentAt.Valid {
		attempt.SentAt = sentAt.Time
	}
	if sendWindowStart.Valid {
		attempt.SendWindowStart = sendWindowStart.Time
	}
	if sendWindowEnd.Valid {
		attempt.SendWindowEnd = sendWindowEnd.Time
	}
	if candidateObservedAt.Valid {
		attempt.CandidateObservedAt = candidateObservedAt.Time
	}
	return &attempt, nil
}

func (s *Store) GetExecutionAttempt(id string) (*ExecutionAttempt, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	attempt, err := scanAttempt(s.DB.QueryRowContext(ctx,
		`SELECT `+attemptColumns+` FROM execution_attempt WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	return attempt, normalizeDBError(err)
}

func (s *Store) UpdatePendingAttemptLimit(id string, limit units.Micros) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE execution_attempt
		SET limit_micros=LEAST(limit_micros,$2)
		WHERE id=$1 AND state='pending' AND intent='place'`, id, int64(limit))
	if err != nil {
		return false, normalizeDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET limit_micros=LEAST(limit_micros,$2),updated_at=now()
		WHERE execution_attempt_id=$1 AND state='new'`, id, int64(limit)); err != nil {
		return false, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
}

func (s *Store) ClaimPendingAttempt(id, instance string) (*ExecutionAttempt, error) {
	return s.claimAttempt(id, instance, "pending", 0, time.Time{}, false)
}

func (s *Store) ClaimRecoverableAttempt(id, instance, expectedState string, expectedToken int, claimBefore time.Time) (*ExecutionAttempt, error) {
	return s.claimAttempt(id, instance, expectedState, expectedToken, claimBefore, false)
}

func (s *Store) ClaimPendingAttemptLive(id, instance string) (*ExecutionAttempt, error) {
	return s.claimAttempt(id, instance, "pending", 0, time.Time{}, true)
}

func (s *Store) ClaimRecoverableAttemptLive(id, instance, expectedState string, expectedToken int, claimBefore time.Time) (*ExecutionAttempt, error) {
	return s.claimAttempt(id, instance, expectedState, expectedToken, claimBefore, true)
}

func (s *Store) claimAttempt(id, instance, expectedState string, expectedToken int, claimBefore time.Time, liveGate bool) (*ExecutionAttempt, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	var intent string
	if err := tx.QueryRowContext(ctx, `SELECT intent FROM execution_attempt WHERE id=$1`, id).Scan(&intent); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, normalizeDBError(err)
	}
	useLiveGate := liveGate && intent != "paper_place"
	var activeAttemptID, unknownAttemptID sql.NullString
	recoveringUnknown := false
	if useLiveGate {
		if err := tx.QueryRowContext(ctx, `SELECT active_attempt_id,unknown_attempt_id
			FROM live_execution_gate WHERE singleton=true FOR UPDATE`).Scan(&activeAttemptID, &unknownAttemptID); err != nil {
			return nil, normalizeDBError(err)
		}
		var unresolvedOther bool
		if !activeAttemptID.Valid && !unknownAttemptID.Valid {
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
				SELECT 1 FROM execution_attempt
				WHERE id<>$1 AND intent<>'paper_place' AND state IN ('claimed','unknown')
			)`, id).Scan(&unresolvedOther); err != nil {
				return nil, normalizeDBError(err)
			}
		}
		allowed := false
		recoveringUnknown = expectedState == "claimed" &&
			unknownAttemptID.String == id && !activeAttemptID.Valid
		switch expectedState {
		case "pending":
			allowed = !activeAttemptID.Valid && !unknownAttemptID.Valid && !unresolvedOther
		case "claimed":
			allowed = (activeAttemptID.String == id && !unknownAttemptID.Valid) ||
				recoveringUnknown ||
				(!activeAttemptID.Valid && !unknownAttemptID.Valid && !unresolvedOther)
		case "unknown":
			allowed = (unknownAttemptID.String == id && !activeAttemptID.Valid) ||
				(!activeAttemptID.Valid && !unknownAttemptID.Valid && !unresolvedOther)
		}
		if !allowed {
			return nil, nil
		}
	}
	query := `UPDATE execution_attempt SET state='claimed', attempt=attempt+1,
		claimed_by=$2, claimed_at=now(), resolved_at=NULL
		WHERE id=$1 AND state=$3`
	args := []any{id, instance, expectedState}
	if expectedState != "pending" {
		query += ` AND attempt=$4`
		args = append(args, expectedToken)
	}
	if expectedState == "claimed" {
		query += ` AND claimed_at < $5`
		args = append(args, claimBefore)
	}
	query += ` RETURNING ` + attemptColumns
	attempt, err := scanAttempt(tx.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if err := validateM3AExecutionEntitlement(ctx, tx, attempt); err != nil {
		_ = tx.Rollback()
		return nil, s.recordExecutionEntitlementFailure(attempt.ID, err)
	}
	if useLiveGate {
		var query string
		if expectedState == "unknown" || recoveringUnknown {
			query = `UPDATE live_execution_gate
				SET unknown_attempt_id=$1,unknown_since=COALESCE(unknown_since,now()),updated_at=now()
				WHERE singleton=true`
		} else {
			query = `UPDATE live_execution_gate
				SET active_attempt_id=$1,active_since=COALESCE(active_since,now()),updated_at=now()
				WHERE singleton=true`
		}
		if _, err := tx.ExecContext(ctx, query, attempt.ID); err != nil {
			return nil, normalizeDBError(err)
		}
	}
	if err := insertEvent(ctx, tx, "execution_attempt_claimed", map[string]any{
		"attempt_id": attempt.ID, "operation_id": attempt.OperationID,
		"fencing_token": attempt.Attempt,
	}); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return attempt, nil
}

type LiveExecutionGate struct {
	ActiveAttemptID  string     `json:"active_attempt_id,omitempty"`
	UnknownAttemptID string     `json:"unknown_attempt_id,omitempty"`
	ActiveSince      *time.Time `json:"active_since,omitempty"`
	UnknownSince     *time.Time `json:"unknown_since,omitempty"`
}

func (s *Store) GetLiveExecutionGate() (LiveExecutionGate, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var gate LiveExecutionGate
	var activeID, unknownID sql.NullString
	var activeSince, unknownSince sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT active_attempt_id,unknown_attempt_id,active_since,unknown_since
		FROM live_execution_gate WHERE singleton=true`).Scan(&activeID, &unknownID, &activeSince, &unknownSince)
	if err != nil {
		return LiveExecutionGate{}, normalizeDBError(err)
	}
	gate.ActiveAttemptID, gate.UnknownAttemptID = activeID.String, unknownID.String
	if activeSince.Valid {
		value := activeSince.Time
		gate.ActiveSince = &value
	}
	if unknownSince.Valid {
		value := unknownSince.Time
		gate.UnknownSince = &value
	}
	return gate, nil
}

func (s *Store) MarkAttemptSent(id string, fencingToken int, replay bool) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer tx.Rollback()
	var activeID, unknownID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT active_attempt_id,unknown_attempt_id
		FROM live_execution_gate WHERE singleton=true FOR UPDATE`).Scan(&activeID, &unknownID); err != nil {
		return false, normalizeDBError(err)
	}
	allowed := activeID.String == id && !unknownID.Valid
	if replay {
		allowed = unknownID.String == id && !activeID.Valid
	}
	if !allowed {
		return false, nil
	}
	query := `UPDATE execution_attempt SET sent_at=now(),
		send_window_start=now()-interval '30 seconds',send_window_end=now()+interval '2 minutes'
		WHERE id=$1 AND attempt=$2 AND state='claimed' AND sent_at IS NULL
		  AND (intent='cancel' OR
		       (intent_fingerprint IS NOT NULL AND provider_intent IS NOT NULL AND provider_account_id IS NOT NULL))`
	if replay {
		query = `UPDATE execution_attempt SET replay_count=replay_count+1
			WHERE id=$1 AND attempt=$2 AND state='claimed' AND sent_at IS NOT NULL AND replay_count=0`
	}
	result, err := tx.ExecContext(ctx, query, id, fencingToken)
	if err != nil {
		return false, normalizeDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, normalizeDBError(err)
	}
	if err := insertEvent(ctx, tx, "execution_attempt_sent", map[string]any{
		"attempt_id": id, "fencing_token": fencingToken, "replay": replay,
	}); err != nil {
		return false, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
}

func (s *Store) PrepareAttemptProviderIntent(id string, fencingToken int, accountID string, canonical json.RawMessage, fingerprint []byte) (bool, error) {
	if strings.TrimSpace(accountID) == "" || len(canonical) == 0 || !json.Valid(canonical) || len(fingerprint) != sha256.Size {
		return false, fmt.Errorf("invalid provider intent evidence")
	}
	var decoded any
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		return false, fmt.Errorf("invalid provider intent evidence")
	}
	if _, ok := decoded.(map[string]any); !ok {
		return false, fmt.Errorf("provider intent must be an object")
	}
	if digest := sha256.Sum256(canonical); !strings.EqualFold(fmt.Sprintf("%x", digest[:]), fmt.Sprintf("%x", fingerprint)) {
		return false, fmt.Errorf("provider intent fingerprint mismatch")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	result, err := s.DB.ExecContext(ctx, `UPDATE execution_attempt SET
		provider_account_id=$3,provider_intent=$4::jsonb,intent_fingerprint=$5
		WHERE id=$1 AND attempt=$2 AND state='claimed' AND intent='place' AND sent_at IS NULL
		  AND (intent_fingerprint IS NULL OR
		       (provider_account_id=$3 AND provider_intent=$4::jsonb AND intent_fingerprint=$5))`,
		id, fencingToken, accountID, []byte(canonical), fingerprint)
	if err != nil {
		return false, normalizeDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, normalizeDBError(err)
	}
	return affected == 1, nil
}

func validateM3AExecutionEntitlement(ctx context.Context, tx *sql.Tx, attempt *ExecutionAttempt) error {
	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM feature_activation WHERE name='m3a')`).Scan(&active); err != nil {
		return err
	}
	if !active {
		return nil
	}
	var action, operationSide string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(payload->>'action',''),
		COALESCE(payload->>'side','') FROM operations WHERE id=$1`, attempt.OperationID).Scan(
		&action, &operationSide); err != nil {
		return err
	}
	if attempt.Intent == "cancel" {
		if attempt.OpenReservationID != "" || attempt.CloseReservationID != "" {
			return fmt.Errorf("%w: cancel attempt has invalid execution entitlement", ErrFillIntegrity)
		}
		if action == "cancel" {
			return nil
		}
		if action != "open" && action != "close" {
			return fmt.Errorf("%w: reprice cancel belongs to action %q", ErrFillIntegrity, action)
		}
		var orderOperationID string
		if err := tx.QueryRowContext(ctx, `SELECT operation_id FROM orders
			WHERE broker_order_id=$1`, attempt.TargetBrokerOrderID).Scan(&orderOperationID); err != nil {
			return fmt.Errorf("%w: reprice cancel target is not durable", ErrFillIntegrity)
		}
		if orderOperationID != attempt.OperationID {
			return fmt.Errorf("%w: reprice cancel target belongs to another operation", ErrFillIntegrity)
		}
		return nil
	}
	if attempt.Intent != "place" && attempt.Intent != "paper_place" {
		return fmt.Errorf("%w: unsupported placement intent %q", ErrFillIntegrity, attempt.Intent)
	}

	var orderOperationID, orderLedger, orderState, orderSymbol, orderKind, orderSide string
	var orderMultiplier, orderQty, orderLimit int64
	if err := tx.QueryRowContext(ctx, `SELECT operation_id,ledger,state,symbol,kind,side,
		multiplier,qty,limit_micros FROM orders WHERE execution_attempt_id=$1`, attempt.ID).Scan(
		&orderOperationID, &orderLedger, &orderState, &orderSymbol, &orderKind, &orderSide,
		&orderMultiplier, &orderQty, &orderLimit); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: placement attempt has no durable order", ErrFillIntegrity)
		}
		return err
	}
	if orderOperationID != attempt.OperationID {
		return fmt.Errorf("%w: placement order belongs to another operation", ErrFillIntegrity)
	}
	if orderQty != int64(attempt.Qty) || orderLimit != int64(attempt.Limit) {
		return fmt.Errorf("%w: placement order differs from its attempt", ErrFillIntegrity)
	}
	if (attempt.Intent == "paper_place") != (orderLedger == "shadow") {
		return fmt.Errorf("%w: placement intent does not match its ledger", ErrFillIntegrity)
	}

	switch action {
	case "open":
		if attempt.OpenReservationID == "" || attempt.CloseReservationID != "" {
			return fmt.Errorf("%w: open attempt lacks its exclusive reservation", ErrFillIntegrity)
		}
		var reservationOperationID, reservationLedger, reservationState, reservationSymbol, reservationKind string
		var grantLedger, riskSource string
		var reservationMarketDay, grantMarketDay time.Time
		var originalQty, remainingQty, originalRisk, originalCash, authorizedRisk int64
		err := tx.QueryRowContext(ctx, `SELECT r.operation_id,r.ledger,r.resource_state,
			r.symbol,r.kind,r.market_day,r.original_qty,r.remaining_qty,
			r.original_risk_micros,r.original_cash_micros,
			g.ledger,g.market_day,g.authorized_risk_micros,g.risk_source
			FROM open_reservation r
			JOIN trade_grant g ON g.operation_id=r.operation_id
			WHERE r.id=$1`, attempt.OpenReservationID).Scan(
			&reservationOperationID, &reservationLedger, &reservationState,
			&reservationSymbol, &reservationKind, &reservationMarketDay,
			&originalQty, &remainingQty, &originalRisk, &originalCash,
			&grantLedger, &grantMarketDay, &authorizedRisk, &riskSource)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: open attempt lacks its reservation or grant", ErrFillIntegrity)
		}
		if err != nil {
			return err
		}
		expectedState := "held"
		if orderState == "filled" {
			expectedState = "converted"
		} else if isTerminalUnfilledOrderState(orderState) {
			expectedState = "released"
		}
		expectedRemaining, err := replacementOrderRemaining(ctx, tx, attempt, orderQty, expectedState)
		if err != nil {
			return err
		}
		quantityMismatch := (attempt.Seq == 1 && originalQty != orderQty) ||
			(attempt.Seq > 1 && expectedState == "held" && remainingQty != expectedRemaining)
		if reservationOperationID != attempt.OperationID || reservationLedger != orderLedger ||
			grantLedger != orderLedger || reservationState != expectedState ||
			reservationSymbol != orderSymbol || reservationKind != orderKind ||
			orderSide != "buy" || orderMultiplier <= 0 || quantityMismatch ||
			originalRisk <= 0 || originalCash <= 0 || authorizedRisk != originalRisk ||
			riskSource != "computed" || !reservationMarketDay.Equal(grantMarketDay) ||
			(expectedState == "held" && remainingQty <= 0) {
			return fmt.Errorf("%w: open attempt entitlement does not match its durable order", ErrFillIntegrity)
		}
	case "close":
		if attempt.CloseReservationID == "" || attempt.OpenReservationID != "" {
			return fmt.Errorf("%w: close attempt lacks its exclusive reservation", ErrFillIntegrity)
		}
		var reservationOperationID, reservationLedger, reservationState, reservationSymbol string
		var originalQty, remainingQty int64
		err := tx.QueryRowContext(ctx, `SELECT operation_id,ledger,state,symbol,original_qty,remaining_qty
			FROM close_reservation
			WHERE id=$1`, attempt.CloseReservationID).Scan(
			&reservationOperationID, &reservationLedger, &reservationState,
			&reservationSymbol, &originalQty, &remainingQty)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: close attempt lacks its reservation", ErrFillIntegrity)
		}
		if err != nil {
			return err
		}
		expectedState := "held"
		if isTerminalOrderStateForEntitlement(orderState) {
			expectedState = "released"
		}
		expectedRemaining, err := replacementOrderRemaining(ctx, tx, attempt, orderQty, expectedState)
		if err != nil {
			return err
		}
		quantityMismatch := (attempt.Seq == 1 && originalQty != orderQty) ||
			(attempt.Seq > 1 && expectedState == "held" && remainingQty != expectedRemaining)
		if reservationOperationID != attempt.OperationID || reservationLedger != orderLedger ||
			reservationState != expectedState || reservationSymbol != orderSymbol ||
			(orderSide != "sell" && orderSide != "buy") || orderSide != operationSide ||
			orderKind == "" || orderMultiplier <= 0 ||
			quantityMismatch || (expectedState == "held" && remainingQty <= 0) {
			return fmt.Errorf("%w: close attempt entitlement does not match its durable order", ErrFillIntegrity)
		}
	default:
		return fmt.Errorf("%w: placement attempt belongs to action %q", ErrFillIntegrity, action)
	}
	return nil
}

func replacementOrderRemaining(ctx context.Context, tx *sql.Tx, attempt *ExecutionAttempt, orderQty int64, reservationState string) (int64, error) {
	if attempt.Seq <= 1 || reservationState != "held" {
		return orderQty, nil
	}
	var durableFilled int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(f.qty),0)
		FROM orders o LEFT JOIN fills f ON f.order_id=o.id
		WHERE o.execution_attempt_id=$1`, attempt.ID).Scan(&durableFilled); err != nil {
		return 0, err
	}
	if durableFilled < 0 || durableFilled > orderQty {
		return 0, fmt.Errorf("%w: replacement fills exceed its order quantity", ErrFillIntegrity)
	}
	return orderQty - durableFilled, nil
}

func isTerminalUnfilledOrderState(state string) bool {
	return state == "cancelled" || state == "rejected" || state == "expired"
}

func isTerminalOrderStateForEntitlement(state string) bool {
	return state == "filled" || isTerminalUnfilledOrderState(state)
}

func (s *Store) recordExecutionEntitlementFailure(attemptID string, cause error) error {
	return s.recordIntegrityFailure("execution_entitlement_invalid", map[string]any{
		"execution_attempt_id": attemptID, "error": cause.Error(),
	}, cause)
}

func (s *Store) ListRecoverableAttempts(pendingBefore, claimBefore time.Time, limit int) ([]ExecutionAttempt, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT `+attemptColumns+` FROM execution_attempt
		WHERE (state='pending' AND created_at < $1)
		   OR (state='claimed' AND claimed_at < $2)
		   OR (state='unknown' AND COALESCE(claimed_at,created_at) < $1)
		ORDER BY created_at,id LIMIT $3`, pendingBefore, claimBefore, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	attempts := make([]ExecutionAttempt, 0, limit)
	for rows.Next() {
		attempt, err := scanAttempt(rows)
		if err != nil {
			return nil, normalizeDBError(err)
		}
		attempts = append(attempts, *attempt)
	}
	return attempts, normalizeDBError(rows.Err())
}

func (s *Store) ResolveAttempt(id string, fencingToken int, resolution AttemptResolution) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer tx.Rollback()

	var operationID string
	var reservationID, openReservationID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT operation_id,close_reservation_id,open_reservation_id
		FROM execution_attempt WHERE id=$1 AND attempt=$2 AND state='claimed'`,
		id, fencingToken).Scan(&operationID, &reservationID, &openReservationID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, normalizeDBError(err)
	}
	if reservationID.Valid && openReservationID.Valid {
		return false, errors.New("execution attempt references both open and close reservations")
	}
	var closeLedger, closeSymbol, reservationLedger string
	if reservationID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT ledger,symbol FROM close_reservation WHERE id=$1`, reservationID.String).Scan(&closeLedger, &closeSymbol); err != nil {
			return false, normalizeDBError(err)
		}
		reservationLedger = closeLedger
	}
	if openReservationID.Valid {
		if err := tx.QueryRowContext(ctx, `SELECT ledger FROM open_reservation WHERE id=$1`, openReservationID.String).Scan(&reservationLedger); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if reservationLedger != "" {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(reservationLedger == "shadow")); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if reservationID.Valid {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, symbolLockKey(closeLedger, closeSymbol)); err != nil {
			return false, normalizeDBError(err)
		}
	}
	var activeAttemptID, unknownAttemptID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT active_attempt_id,unknown_attempt_id
		FROM live_execution_gate WHERE singleton=true FOR UPDATE`).Scan(&activeAttemptID, &unknownAttemptID); err != nil {
		return false, normalizeDBError(err)
	}
	var lockedOperationID string
	var lockedReservationID, lockedOpenReservationID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT operation_id,close_reservation_id,open_reservation_id
		FROM execution_attempt WHERE id=$1 AND attempt=$2 AND state='claimed' FOR UPDATE`,
		id, fencingToken).Scan(&lockedOperationID, &lockedReservationID, &lockedOpenReservationID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, normalizeDBError(err)
	}
	if lockedOperationID != operationID || lockedReservationID != reservationID || lockedOpenReservationID != openReservationID {
		return false, errors.New("execution attempt identity changed while acquiring resource locks")
	}

	if resolution.OrderUpdate != nil {
		if resolution.OrderUpdate.ExecutionAttemptID == "" {
			resolution.OrderUpdate.ExecutionAttemptID = id
		}
		if _, err := applyOrderUpdate(ctx, tx, *resolution.OrderUpdate, false); err != nil {
			_ = tx.Rollback()
			return false, s.recordOrderUpdateFailure(*resolution.OrderUpdate, err)
		}
	}

	result, err := tx.ExecContext(ctx, `UPDATE execution_attempt SET state=$3,
		broker_order_id=NULLIF($4,''), last_error=NULLIF($5,''),
		provider_error_code=COALESCE(NULLIF($6,''),provider_error_code),
		candidate_broker_order_id=COALESCE(candidate_broker_order_id,NULLIF($7,'')::uuid),
		candidate_observed_at=CASE WHEN candidate_broker_order_id IS NOT NULL OR NULLIF($7,'') IS NOT NULL
			THEN COALESCE(candidate_observed_at,now()) ELSE NULL END,
		resolved_at=CASE WHEN $3 IN ('settled','failed') THEN now() ELSE NULL END
		WHERE id=$1 AND attempt=$2 AND state='claimed'
		  AND (NULLIF($7,'') IS NULL OR candidate_broker_order_id IS NULL OR candidate_broker_order_id=NULLIF($7,'')::uuid)`,
		id, fencingToken, resolution.State, resolution.BrokerOrderID, resolution.LastError,
		resolution.ProviderErrorCode, resolution.CandidateBrokerOrderID)
	if err != nil {
		return false, normalizeDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, normalizeDBError(err)
	}
	if resolution.State == "unknown" {
		if activeAttemptID.String == id {
			if _, err := tx.ExecContext(ctx, `UPDATE live_execution_gate SET
				active_attempt_id=NULL,active_since=NULL,
				unknown_attempt_id=$1,unknown_since=COALESCE(unknown_since,now()),updated_at=now()
				WHERE singleton=true`, id); err != nil {
				return false, normalizeDBError(err)
			}
		}
	} else if activeAttemptID.String == id || unknownAttemptID.String == id {
		if _, err := tx.ExecContext(ctx, `UPDATE live_execution_gate SET
			active_attempt_id=CASE WHEN active_attempt_id=$1 THEN NULL ELSE active_attempt_id END,
			active_since=CASE WHEN active_attempt_id=$1 THEN NULL ELSE active_since END,
			unknown_attempt_id=CASE WHEN unknown_attempt_id=$1 THEN NULL ELSE unknown_attempt_id END,
			unknown_since=CASE WHEN unknown_attempt_id=$1 THEN NULL ELSE unknown_since END,
			updated_at=now() WHERE singleton=true`, id); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if resolution.OperationStatus != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET status=$1 WHERE id=$2`, resolution.OperationStatus, operationID); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if resolution.ReleaseReservation && reservationID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE close_reservation
			SET state='released', remaining_qty=0, released_at=COALESCE(released_at,now())
			WHERE id=$1 AND state='held'`, reservationID.String); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if resolution.ReleaseReservation && openReservationID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE open_reservation SET resource_state='released',
			remaining_risk_micros=0,remaining_cash_micros=0,settled_at=COALESCE(settled_at,now())
			WHERE id=$1 AND resource_state='held'`, openReservationID.String); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if err := insertEvent(ctx, tx, "execution_attempt_resolved", map[string]any{
		"attempt_id": id, "operation_id": operationID, "state": resolution.State,
		"broker_order_id": resolution.BrokerOrderID, "fencing_token": fencingToken,
		"last_error": resolution.LastError, "provider_error_code": resolution.ProviderErrorCode,
		"candidate_broker_order_id": resolution.CandidateBrokerOrderID,
		"operator_subject":          resolution.OperatorSubject,
	}); err != nil {
		return false, normalizeDBError(err)
	}
	if resolution.OrderEvent != nil {
		if err := insertEvent(ctx, tx, "order_update", resolution.OrderEvent); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
}

func (s *Store) FailPendingAttempt(id, reason string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer tx.Rollback()
	var operationID string
	var reservationID, openReservationID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT operation_id,close_reservation_id,open_reservation_id
		FROM execution_attempt WHERE id=$1 AND state='pending'`, id).Scan(&operationID, &reservationID, &openReservationID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, normalizeDBError(err)
	}
	if openReservationID.Valid {
		var shadow bool
		if err := tx.QueryRowContext(ctx, `SELECT ledger='shadow' FROM open_reservation WHERE id=$1`, openReservationID.String).Scan(&shadow); err != nil {
			return false, normalizeDBError(err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(shadow)); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if reservationID.Valid {
		var ledger, symbol string
		if err := tx.QueryRowContext(ctx, `SELECT ledger,symbol FROM close_reservation WHERE id=$1`, reservationID.String).Scan(&ledger, &symbol); err != nil {
			return false, normalizeDBError(err)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, symbolLockKey(ledger, symbol)); err != nil {
			return false, normalizeDBError(err)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE execution_attempt SET state='failed',last_error=$2,resolved_at=now()
		WHERE id=$1 AND state='pending'`, id, reason)
	if err != nil {
		return false, normalizeDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE operations SET status='failed' WHERE id=$1`, operationID); err != nil {
		return false, normalizeDBError(err)
	}
	if reservationID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE close_reservation SET state='released',remaining_qty=0,released_at=COALESCE(released_at,now())
			WHERE id=$1 AND state='held'`, reservationID.String); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if openReservationID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE open_reservation SET resource_state='released',
			remaining_risk_micros=0,remaining_cash_micros=0,settled_at=COALESCE(settled_at,now())
			WHERE id=$1 AND resource_state='held'`, openReservationID.String); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if err := insertEvent(ctx, tx, "execution_attempt_failed_gate", map[string]any{
		"attempt_id": id, "operation_id": operationID, "reason": reason,
	}); err != nil {
		return false, normalizeDBError(err)
	}
	if strings.HasPrefix(reason, "order_expired_policy:") {
		if err := insertEvent(ctx, tx, "order_expired_policy", map[string]any{
			"operation_id": operationID, "attempt_id": id,
			"reason": strings.TrimPrefix(reason, "order_expired_policy:"),
		}); err != nil {
			return false, normalizeDBError(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
}

func (s *Store) GetCloseReservation(id string) (*CloseReservation, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var reservation CloseReservation
	var original, remaining int64
	var releasedAt sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT id,operation_id,ledger,symbol,original_qty,
		remaining_qty,state,created_at,released_at FROM close_reservation WHERE id=$1`, id).
		Scan(&reservation.ID, &reservation.OperationID, &reservation.Ledger,
			&reservation.Symbol, &original, &remaining, &reservation.State,
			&reservation.CreatedAt, &releasedAt)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	reservation.OriginalQty = units.Qty(original)
	reservation.RemainingQty = units.Qty(remaining)
	if releasedAt.Valid {
		reservation.ReleasedAt = releasedAt.Time
	}
	return &reservation, nil
}

func (s *Store) HasTradeGrant(operationID string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var exists bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM trade_grant WHERE operation_id=$1)`, operationID).Scan(&exists)
	return exists, normalizeDBError(err)
}
