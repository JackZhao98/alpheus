package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	orderstate "alpheus/kernel/internal/state"
	"alpheus/kernel/internal/units"
)

var (
	ErrIllegalOrderTransition = errors.New("illegal order transition")
	ErrFillIntegrity          = errors.New("fill integrity failure")
	errRepriceManagedOrder    = errors.New("order update is owned by reprice cancellation")
)

type Order struct {
	ID                 string
	OperationID        string
	ExecutionAttemptID string
	BrokerOrderID      string
	ClientOrderID      string
	Ledger             string
	Symbol             string
	Side               string
	Kind               string
	Multiplier         int64
	Qty                units.Qty
	Limit              units.Micros
	State              string
	Reprices           int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type FillInput struct {
	BrokerFillID string
	Qty          units.Qty
	Price        units.Micros
	Fees         units.Micros
	TS           time.Time
}

type OrderUpdate struct {
	ExecutionAttemptID string
	BrokerOrderID      string
	State              string
	FilledQty          units.Qty
	Fills              []FillInput
}

func (t *ledgerTx) InsertOrder(order Order) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO orders
		(id,operation_id,execution_attempt_id,broker_order_id,client_order_id,
		 ledger,symbol,side,kind,multiplier,qty,limit_micros,state,reprices)
		VALUES ($1,$2,$3,NULL,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		order.ID, order.OperationID, order.ExecutionAttemptID, order.ClientOrderID,
		order.Ledger, order.Symbol, order.Side, order.Kind, order.Multiplier,
		int64(order.Qty), int64(order.Limit), order.State, order.Reprices)
	return normalizeDBError(err)
}

const orderColumns = `id,operation_id,execution_attempt_id,broker_order_id,
	client_order_id,ledger,symbol,side,kind,multiplier,qty,limit_micros,state,
	reprices,created_at,updated_at`

func scanOrder(row scanner) (*Order, error) {
	var order Order
	var brokerOrderID sql.NullString
	var quantity, limit int64
	err := row.Scan(&order.ID, &order.OperationID, &order.ExecutionAttemptID,
		&brokerOrderID, &order.ClientOrderID, &order.Ledger, &order.Symbol,
		&order.Side, &order.Kind, &order.Multiplier, &quantity, &limit,
		&order.State, &order.Reprices, &order.CreatedAt, &order.UpdatedAt)
	if err != nil {
		return nil, err
	}
	order.BrokerOrderID = brokerOrderID.String
	order.Qty = units.Qty(quantity)
	order.Limit = units.Micros(limit)
	return &order, nil
}

func (s *Store) GetOrderByAttempt(attemptID string) (*Order, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	order, err := scanOrder(s.DB.QueryRowContext(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE execution_attempt_id=$1`, attemptID))
	return order, normalizeDBError(err)
}

func (s *Store) GetOrderByBrokerID(brokerOrderID string) (*Order, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	order, err := scanOrder(s.DB.QueryRowContext(ctx,
		`SELECT `+orderColumns+` FROM orders WHERE broker_order_id=$1`, brokerOrderID))
	return order, normalizeDBError(err)
}

func (s *Store) ListWorkingOrders(limit int) ([]Order, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT `+orderColumns+` FROM orders
		WHERE state IN ('submitted','partially_filled') AND broker_order_id IS NOT NULL
		  AND NOT EXISTS (
			SELECT 1 FROM execution_attempt cancel_attempt
			WHERE cancel_attempt.operation_id=orders.operation_id
			  AND cancel_attempt.intent='cancel'
			  AND cancel_attempt.target_broker_order_id=orders.broker_order_id
			  AND cancel_attempt.state IN ('pending','claimed','unknown'))
		ORDER BY updated_at,id LIMIT $1`, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	orders := make([]Order, 0, limit)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, normalizeDBError(err)
		}
		orders = append(orders, *order)
	}
	return orders, normalizeDBError(rows.Err())
}

func (s *Store) ApplyOrderUpdate(update OrderUpdate) error {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return normalizeDBError(err)
	}
	defer tx.Rollback()
	order, err := applyOrderUpdate(ctx, tx, update, false)
	if err != nil {
		if errors.Is(err, errRepriceManagedOrder) {
			return nil
		}
		_ = tx.Rollback()
		return s.recordOrderUpdateFailure(update, err)
	}
	if terminalAttemptState, operationStatus := terminalPlacementState(update.State); terminalAttemptState != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE execution_attempt
			SET state=$1,resolved_at=now(),last_error=NULL
			WHERE id=$2 AND state='placed'`, terminalAttemptState, order.ExecutionAttemptID); err != nil {
			return normalizeDBError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET status=$1 WHERE id=$2`, operationStatus, order.OperationID); err != nil {
			return normalizeDBError(err)
		}
	}
	return normalizeDBError(tx.Commit())
}

func terminalPlacementState(orderState string) (string, string) {
	switch orderState {
	case "filled":
		return "settled", "executed"
	case "rejected":
		return "failed", "failed"
	case "cancelled", "expired":
		return "settled", "failed"
	default:
		return "", ""
	}
}

func applyOrderUpdate(ctx context.Context, tx *sql.Tx, update OrderUpdate, preserveReservation bool) (*Order, error) {
	if update.ExecutionAttemptID == "" && update.BrokerOrderID == "" {
		return nil, fmt.Errorf("order update has no durable identity")
	}
	query := `SELECT ` + orderColumns + ` FROM orders WHERE execution_attempt_id=$1`
	identity := update.ExecutionAttemptID
	if identity == "" {
		query = `SELECT ` + orderColumns + ` FROM orders WHERE broker_order_id=$1`
		identity = update.BrokerOrderID
	}
	order, err := scanOrder(tx.QueryRowContext(ctx, query, identity))
	if err != nil {
		return nil, err
	}

	// Fill/resource mutations take the stable ledger lock before the optional
	// symbol and row locks, matching the proposal and recovery lock order.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(order.Ledger == "shadow")); err != nil {
		return nil, err
	}
	order, err = scanOrder(tx.QueryRowContext(ctx, query+` FOR UPDATE`, identity))
	if err != nil {
		return nil, err
	}
	if order.BrokerOrderID != "" && update.BrokerOrderID != "" && order.BrokerOrderID != update.BrokerOrderID {
		return nil, fmt.Errorf("%w: order broker id changed", ErrFillIntegrity)
	}
	if !preserveReservation && order.BrokerOrderID != "" {
		var repriceManaged bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
			SELECT 1 FROM execution_attempt
			WHERE operation_id=$1 AND intent='cancel' AND target_broker_order_id=$2
			  AND state IN ('pending','claimed','unknown','settled'))`,
			order.OperationID, order.BrokerOrderID).Scan(&repriceManaged); err != nil {
			return nil, err
		}
		if repriceManaged {
			// FinalizeRepriceCancel is the sole owner of this source order after a
			// durable reprice cancel exists. A generic reconciler may have listed
			// the order before staging and must not release the reservation from a
			// stale terminal snapshot after it has transferred to a replacement.
			return nil, errRepriceManagedOrder
		}
	}
	if err := validateOrderTransition(order.State, update.State); err != nil {
		return nil, err
	}
	var reservationID, openReservationID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT close_reservation_id,open_reservation_id
		FROM execution_attempt WHERE id=$1`, order.ExecutionAttemptID).Scan(&reservationID, &openReservationID); err != nil {
		return nil, err
	}
	if reservationID.Valid {
		var ledger, symbol string
		if err := tx.QueryRowContext(ctx, `SELECT ledger,symbol FROM close_reservation WHERE id=$1`, reservationID.String).Scan(&ledger, &symbol); err != nil {
			return nil, err
		}
		if ledger != order.Ledger || symbol != order.Symbol {
			return nil, fmt.Errorf("%w: close reservation does not match order", ErrFillIntegrity)
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, symbolLockKey(ledger, symbol)); err != nil {
			return nil, err
		}
	}
	attempt, err := scanAttempt(tx.QueryRowContext(ctx, `SELECT `+attemptColumns+`
		FROM execution_attempt WHERE id=$1`, order.ExecutionAttemptID))
	if err != nil {
		return nil, err
	}
	if err := validateM3AExecutionEntitlement(ctx, tx, attempt); err != nil {
		return nil, err
	}

	for _, fill := range update.Fills {
		if fill.BrokerFillID == "" || fill.Qty <= 0 || fill.Price <= 0 || fill.Fees < 0 || fill.TS.IsZero() {
			return nil, fmt.Errorf("%w: invalid fill fields", ErrFillIntegrity)
		}
		fillID := NewID()
		result, err := tx.ExecContext(ctx, `INSERT INTO fills
			(id,order_id,broker_fill_id,ledger,qty,price_micros,fees_micros,ts)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (broker_fill_id) DO NOTHING`, fillID, order.ID,
			fill.BrokerFillID, order.Ledger, int64(fill.Qty), int64(fill.Price),
			int64(fill.Fees), fill.TS)
		if err != nil {
			return nil, err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if inserted == 0 {
			var existingOrder, existingLedger string
			var existingQty, existingPrice, existingFees int64
			if err := tx.QueryRowContext(ctx, `SELECT order_id,ledger,qty,price_micros,fees_micros
				FROM fills WHERE broker_fill_id=$1`, fill.BrokerFillID).Scan(
				&existingOrder, &existingLedger, &existingQty, &existingPrice, &existingFees); err != nil {
				return nil, err
			}
			if existingOrder != order.ID || existingLedger != order.Ledger ||
				existingQty != int64(fill.Qty) || existingPrice != int64(fill.Price) ||
				existingFees != int64(fill.Fees) {
				return nil, fmt.Errorf("%w: broker fill id %s changed economics", ErrFillIntegrity, fill.BrokerFillID)
			}
			continue
		}
		if reservationID.Valid {
			result, err := tx.ExecContext(ctx, `UPDATE close_reservation SET
				remaining_qty=remaining_qty-$2,
				state=CASE WHEN remaining_qty=$2 THEN 'released' ELSE state END,
				released_at=CASE WHEN remaining_qty=$2 THEN now() ELSE released_at END
				WHERE id=$1 AND state='held' AND remaining_qty >= $2`,
				reservationID.String, int64(fill.Qty))
			if err != nil {
				return nil, err
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				return nil, fmt.Errorf("%w: close fill exceeds held reservation", ErrFillIntegrity)
			}
		}
		if err := insertEvent(ctx, tx, "fill", map[string]any{
			"order_id": order.ID, "broker_fill_id": fill.BrokerFillID,
			"qty": fill.Qty, "price_micros": fill.Price, "ledger": order.Ledger,
		}); err != nil {
			return nil, err
		}
		if err := applyExposureFill(ctx, tx, order, fillID, fill, openReservationID); err != nil {
			return nil, err
		}
	}
	var durableFilled int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(qty),0) FROM fills WHERE order_id=$1`, order.ID).Scan(&durableFilled); err != nil {
		return nil, err
	}
	if durableFilled != int64(update.FilledQty) {
		return nil, fmt.Errorf("%w: broker filled quantity does not match durable fills", ErrFillIntegrity)
	}
	if update.State == "filled" && update.FilledQty != order.Qty {
		return nil, fmt.Errorf("%w: terminal fill quantity does not match order", ErrFillIntegrity)
	}

	if reservationID.Valid {
		switch update.State {
		case "cancelled", "rejected", "expired":
			if !preserveReservation {
				if _, err := tx.ExecContext(ctx, `UPDATE close_reservation SET
				state='released',remaining_qty=0,released_at=COALESCE(released_at,now())
				WHERE id=$1 AND state='held'`, reservationID.String); err != nil {
					return nil, err
				}
			}
		case "filled":
			var remaining int64
			var reservationState string
			if err := tx.QueryRowContext(ctx, `SELECT remaining_qty,state FROM close_reservation WHERE id=$1`, reservationID.String).Scan(&remaining, &reservationState); err != nil {
				return nil, err
			}
			if remaining != 0 || reservationState != "released" {
				return nil, fmt.Errorf("%w: filled close is missing durable fills", ErrFillIntegrity)
			}
		}
	}
	if openReservationID.Valid {
		switch update.State {
		case "cancelled", "rejected", "expired":
			if !preserveReservation {
				if _, err := tx.ExecContext(ctx, `UPDATE open_reservation SET
				resource_state='released',remaining_risk_micros=0,remaining_cash_micros=0,
				settled_at=COALESCE(settled_at,now())
				WHERE id=$1 AND resource_state='held'`, openReservationID.String); err != nil {
					return nil, err
				}
			}
		case "filled":
			var resourceState string
			if err := tx.QueryRowContext(ctx, `SELECT resource_state FROM open_reservation WHERE id=$1`, openReservationID.String).Scan(&resourceState); err != nil {
				return nil, err
			}
			if resourceState != "converted" {
				return nil, fmt.Errorf("%w: filled open is missing durable exposure", ErrFillIntegrity)
			}
		}
	}

	brokerOrderID := order.BrokerOrderID
	if update.BrokerOrderID != "" {
		brokerOrderID = update.BrokerOrderID
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET broker_order_id=NULLIF($2,''),
		state=$3,updated_at=now() WHERE id=$1`, order.ID, brokerOrderID, update.State); err != nil {
		return nil, err
	}
	if err := insertEvent(ctx, tx, "order_update", map[string]any{
		"order_id": order.ID, "operation_id": order.OperationID,
		"broker_order_id": brokerOrderID, "state": update.State,
	}); err != nil {
		return nil, err
	}
	order.BrokerOrderID = brokerOrderID
	order.State = update.State
	return order, nil
}

func validateOrderTransition(current, next string) error {
	if current == next {
		return nil
	}
	if current == "new" && next != "submitted" && next != "rejected" {
		if _, err := orderstate.Advance(current, "submitted"); err != nil {
			return fmt.Errorf("%w: %v", ErrIllegalOrderTransition, err)
		}
		current = "submitted"
	}
	if _, err := orderstate.Advance(current, next); err != nil {
		return fmt.Errorf("%w: %v", ErrIllegalOrderTransition, err)
	}
	return nil
}

func (s *Store) recordOrderUpdateFailure(update OrderUpdate, cause error) error {
	ctx, cancel := s.deadline()
	defer cancel()
	if errors.Is(cause, ErrIllegalOrderTransition) {
		err := insertEvent(ctx, s.DB, "order_transition_rejected", map[string]any{
			"execution_attempt_id": update.ExecutionAttemptID,
			"broker_order_id":      update.BrokerOrderID, "next_state": update.State,
			"error": cause.Error(),
		})
		return errors.Join(cause, normalizeDBError(err))
	}
	if errors.Is(cause, ErrFillIntegrity) {
		return s.recordIntegrityFailure("fill_integrity_error", map[string]any{
			"execution_attempt_id": update.ExecutionAttemptID,
			"broker_order_id":      update.BrokerOrderID, "error": cause.Error(),
		}, cause)
	}
	return cause
}

func (s *Store) recordIntegrityFailure(kind string, payload map[string]any, cause error) error {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return errors.Join(cause, normalizeDBError(err))
	}
	defer tx.Rollback()
	if err := insertEvent(ctx, tx, kind, payload); err != nil {
		return errors.Join(cause, normalizeDBError(err))
	}
	if err := insertEvent(ctx, tx, "global_halt_transition", map[string]any{
		"halted": true, "reason": cause.Error(),
	}); err != nil {
		return errors.Join(cause, normalizeDBError(err))
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(cause, normalizeDBError(err))
	}
	return cause
}
