package store

import (
	"database/sql"
	"fmt"

	"alpheus/kernel/internal/units"
)

type TerminalReservationCandidate struct {
	Kind              string
	ReservationID     string
	OperationID       string
	AttemptID         string
	Ledger            string
	Symbol            string
	BrokerOrderID     string
	OrderState        string
	DurableFilledQty  units.Qty
	SafeWithoutBroker bool
}

func (s *Store) ListTerminalReservationCandidates(limit int) ([]TerminalReservationCandidate, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `WITH candidates AS (
		SELECT 'open'::text AS kind,
		       r.id AS reservation_id,
		       r.operation_id AS operation_id,
		       a.id AS attempt_id,
		       r.ledger AS ledger,
		       r.symbol AS symbol,
		       COALESCE(o.broker_order_id,a.broker_order_id,'') AS broker_order_id,
		       COALESCE(o.state,'new') AS order_state,
		       COALESCE((SELECT sum(f.qty) FROM fills f WHERE f.order_id=o.id),0) AS durable_filled_qty,
		       (COALESCE(o.broker_order_id,a.broker_order_id,'')='' AND
		        a.state='failed' AND a.attempt=0 AND
		        COALESCE((SELECT sum(f.qty) FROM fills f WHERE f.order_id=o.id),0)=0) AS safe_without_broker
		FROM open_reservation r
		JOIN operations op ON op.id=r.operation_id
		JOIN LATERAL (
			SELECT ea.* FROM execution_attempt ea
			WHERE ea.open_reservation_id=r.id AND ea.intent IN ('place','paper_place')
			ORDER BY ea.seq DESC LIMIT 1
		) a ON true
		JOIN orders o ON o.execution_attempt_id=a.id
		WHERE r.resource_state='held' AND op.status IN ('executed','failed','rejected')
		  AND NOT EXISTS (SELECT 1 FROM execution_attempt unresolved
		                  WHERE unresolved.operation_id=r.operation_id
		                    AND unresolved.state IN ('pending','claimed','placed','unknown'))
		UNION ALL
		SELECT 'close'::text AS kind,r.id,r.operation_id,a.id,r.ledger,r.symbol,
		       COALESCE(o.broker_order_id,a.broker_order_id,''),COALESCE(o.state,'new'),
		       COALESCE((SELECT sum(f.qty) FROM fills f WHERE f.order_id=o.id),0),
		       (COALESCE(o.broker_order_id,a.broker_order_id,'')='' AND
		        a.state='failed' AND a.attempt=0 AND
		        COALESCE((SELECT sum(f.qty) FROM fills f WHERE f.order_id=o.id),0)=0)
		FROM close_reservation r
		JOIN operations op ON op.id=r.operation_id
		JOIN LATERAL (
			SELECT ea.* FROM execution_attempt ea
			WHERE ea.close_reservation_id=r.id AND ea.intent IN ('place','paper_place')
			ORDER BY ea.seq DESC LIMIT 1
		) a ON true
		JOIN orders o ON o.execution_attempt_id=a.id
		WHERE r.state='held' AND op.status IN ('executed','failed','rejected')
		  AND NOT EXISTS (SELECT 1 FROM execution_attempt unresolved
		                  WHERE unresolved.operation_id=r.operation_id
		                    AND unresolved.state IN ('pending','claimed','placed','unknown'))
	)
	SELECT * FROM candidates ORDER BY operation_id,reservation_id LIMIT $1`, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	candidates := make([]TerminalReservationCandidate, 0, limit)
	for rows.Next() {
		var candidate TerminalReservationCandidate
		var durableFilled int64
		if err := rows.Scan(&candidate.Kind, &candidate.ReservationID, &candidate.OperationID,
			&candidate.AttemptID, &candidate.Ledger, &candidate.Symbol,
			&candidate.BrokerOrderID, &candidate.OrderState, &durableFilled,
			&candidate.SafeWithoutBroker); err != nil {
			return nil, normalizeDBError(err)
		}
		candidate.DurableFilledQty = units.Qty(durableFilled)
		candidates = append(candidates, candidate)
	}
	return candidates, normalizeDBError(rows.Err())
}

func (s *Store) ReleaseProvenTerminalReservation(candidate TerminalReservationCandidate, provenFilledQty units.Qty, terminalProof bool) (bool, error) {
	if !terminalProof || provenFilledQty != candidate.DurableFilledQty {
		return false, nil
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(candidate.Ledger == "shadow")); err != nil {
		return false, normalizeDBError(err)
	}
	if candidate.Kind == "close" {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, symbolLockKey(candidate.Ledger, candidate.Symbol)); err != nil {
			return false, normalizeDBError(err)
		}
	}

	var operationStatus, attemptState string
	var attemptNumber int
	var brokerOrderID, orderState string
	var durableFilled int64
	err = tx.QueryRowContext(ctx, `SELECT op.status,a.state,a.attempt,
		COALESCE(o.broker_order_id,a.broker_order_id,''),COALESCE(o.state,'new'),
		COALESCE((SELECT sum(f.qty) FROM fills f WHERE f.order_id=o.id),0)
		FROM operations op
		JOIN execution_attempt a ON a.id=$2 AND a.operation_id=op.id
		JOIN orders o ON o.execution_attempt_id=a.id
		WHERE op.id=$1`, candidate.OperationID, candidate.AttemptID).Scan(
		&operationStatus, &attemptState, &attemptNumber, &brokerOrderID, &orderState, &durableFilled)
	if err != nil {
		return false, normalizeDBError(err)
	}
	if operationStatus != "executed" && operationStatus != "failed" && operationStatus != "rejected" {
		return false, nil
	}
	var unresolved bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM execution_attempt WHERE operation_id=$1
		AND state IN ('pending','claimed','placed','unknown'))`, candidate.OperationID).Scan(&unresolved); err != nil {
		return false, normalizeDBError(err)
	}
	if unresolved || units.Qty(durableFilled) != candidate.DurableFilledQty ||
		brokerOrderID != candidate.BrokerOrderID || orderState != candidate.OrderState {
		return false, nil
	}
	if candidate.BrokerOrderID == "" &&
		!(candidate.SafeWithoutBroker && attemptState == "failed" && attemptNumber == 0 && durableFilled == 0) {
		return false, nil
	}

	var result sql.Result
	switch candidate.Kind {
	case "open":
		result, err = tx.ExecContext(ctx, `UPDATE open_reservation SET resource_state='released',
			remaining_risk_micros=0,remaining_cash_micros=0,settled_at=COALESCE(settled_at,now())
			WHERE id=$1 AND operation_id=$2 AND ledger=$3 AND symbol=$4 AND resource_state='held'`,
			candidate.ReservationID, candidate.OperationID, candidate.Ledger, candidate.Symbol)
	case "close":
		result, err = tx.ExecContext(ctx, `UPDATE close_reservation SET state='released',
			remaining_qty=0,released_at=COALESCE(released_at,now())
			WHERE id=$1 AND operation_id=$2 AND ledger=$3 AND symbol=$4 AND state='held'`,
			candidate.ReservationID, candidate.OperationID, candidate.Ledger, candidate.Symbol)
	default:
		return false, fmt.Errorf("unknown terminal reservation kind %q", candidate.Kind)
	}
	if err != nil {
		return false, normalizeDBError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, normalizeDBError(err)
	}
	if err := insertEvent(ctx, tx, "orphan_reservation_released", map[string]any{
		"operation_id": candidate.OperationID, "reservation_id": candidate.ReservationID,
		"kind": candidate.Kind, "ledger": candidate.Ledger,
		"durable_filled_qty": candidate.DurableFilledQty,
	}); err != nil {
		return false, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return false, normalizeDBError(err)
	}
	return true, nil
}
