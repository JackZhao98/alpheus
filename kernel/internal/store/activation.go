package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"alpheus/kernel/internal/units"
)

const m3aActivationLockKey int64 = 0x414c50484d334100 // "ALPHM3A\0"

type ActivationPosition struct {
	Symbol     string
	Kind       string
	Multiplier int64
	Qty        units.Qty
}

type M3AActivationSnapshot struct {
	Equity      units.Micros
	BuyingPower units.Micros
	Positions   []ActivationPosition
}

func (s *Store) FeatureActive(name string) (bool, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var active bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM feature_activation WHERE name=$1)`, name).Scan(&active)
	return active, normalizeDBError(err)
}

func (s *Store) ActivateM3A(snapshot M3AActivationSnapshot) error {
	if snapshot.Equity <= 0 || snapshot.BuyingPower < 0 {
		return fmt.Errorf("M3A activation requires known positive equity and buying power")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return normalizeDBError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, m3aActivationLockKey); err != nil {
		return normalizeDBError(err)
	}
	var alreadyActive bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM feature_activation WHERE name='m3a')`).Scan(&alreadyActive); err != nil {
		return normalizeDBError(err)
	}
	if alreadyActive {
		return normalizeDBError(tx.Commit())
	}
	var cutoff time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&cutoff); err != nil {
		return normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO shadow_account
		(singleton,cash_micros,buying_power_micros,activated_at,updated_at)
		VALUES (true,$1,$2,$3,$3)`, int64(snapshot.Equity), int64(snapshot.BuyingPower), cutoff); err != nil {
		return normalizeDBError(err)
	}

	orders, err := loadActivationOrders(ctx, tx)
	if err != nil {
		return normalizeDBError(err)
	}
	orderByID := make(map[string]Order, len(orders))
	operationByOrderID := make(map[string]activationOperation, len(orders))
	openFacts := make(map[string]activationOperation)
	for _, order := range orders {
		orderByID[order.ID] = order
		operation, err := loadActivationOperation(ctx, tx, order.OperationID)
		if err != nil {
			return normalizeDBError(err)
		}
		operationByOrderID[order.ID] = operation
		if order.Ledger != "live" {
			return fmt.Errorf("M3A activation blocker: unexpected pre-activation %s order %s", order.Ledger, order.ID)
		}
		if err := validateActivationOrder(order, operation); err != nil {
			return err
		}
		if operation.Action != "open" {
			continue
		}
		if operation.DerivedMaxRisk <= 0 || operation.RequiredCash <= 0 {
			return fmt.Errorf("M3A activation blocker: open %s lacks exact risk facts", order.OperationID)
		}
		var marketDay time.Time
		var grantLedger, riskSource string
		var authorizedRisk int64
		if err := tx.QueryRowContext(ctx, `SELECT market_day,ledger,risk_source,authorized_risk_micros
			FROM trade_grant WHERE operation_id=$1`, order.OperationID).Scan(
			&marketDay, &grantLedger, &riskSource, &authorizedRisk); err != nil {
			return fmt.Errorf("M3A activation blocker: open %s lacks trade grant", order.OperationID)
		}
		if grantLedger != order.Ledger || riskSource != "computed" ||
			authorizedRisk != int64(operation.DerivedMaxRisk) {
			return fmt.Errorf("M3A activation blocker: open %s trade grant does not match durable risk facts", order.OperationID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO open_reservation
			(id,operation_id,ledger,market_day,symbol,kind,original_qty,remaining_qty,
			 original_risk_micros,remaining_risk_micros,original_cash_micros,
			 remaining_cash_micros,resource_state,created_at)
			VALUES ($1,$1,$2,$3,$4,$5,$6,$6,$7,$7,$8,$8,'held',$9)`,
			order.OperationID, order.Ledger, marketDay, order.Symbol, order.Kind,
			int64(order.Qty), int64(operation.DerivedMaxRisk), int64(operation.RequiredCash),
			order.CreatedAt); err != nil {
			return normalizeDBError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE execution_attempt SET open_reservation_id=$1
			WHERE id=$2`, order.OperationID, order.ExecutionAttemptID); err != nil {
			return normalizeDBError(err)
		}
		openFacts[order.ID] = operation
	}

	fills, err := loadActivationFills(ctx, tx)
	if err != nil {
		return normalizeDBError(err)
	}
	for _, persisted := range fills {
		order, ok := orderByID[persisted.OrderID]
		if !ok {
			return fmt.Errorf("M3A activation blocker: fill %s has no durable order", persisted.ID)
		}
		if persisted.Ledger != order.Ledger {
			return fmt.Errorf("M3A activation blocker: fill %s ledger does not match order", persisted.ID)
		}
		if order.Ledger != "live" {
			continue
		}
		operation := operationByOrderID[order.ID]
		switch operation.Action {
		case "open":
			if err := applyOpenExposureFill(ctx, tx, &order, persisted.ID, persisted.Fill, order.OperationID); err != nil {
				return fmt.Errorf("M3A activation blocker: %w", err)
			}
		case "close":
			if err := applyCloseExposureFill(ctx, tx, &order, persisted.ID, persisted.Fill); err != nil {
				return fmt.Errorf("M3A activation blocker: %w", err)
			}
		}
	}

	for orderID := range openFacts {
		order := orderByID[orderID]
		var attemptState string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM execution_attempt WHERE id=$1`, order.ExecutionAttemptID).Scan(&attemptState); err != nil {
			return normalizeDBError(err)
		}
		switch order.State {
		case "cancelled", "rejected", "expired":
			if err := releaseActivationReservation(ctx, tx, order.OperationID); err != nil {
				return err
			}
		case "new":
			if attemptState == "failed" || attemptState == "settled" {
				if err := releaseActivationReservation(ctx, tx, order.OperationID); err != nil {
					return err
				}
			}
		case "filled":
			var state string
			if err := tx.QueryRowContext(ctx, `SELECT resource_state FROM open_reservation WHERE id=$1`, order.OperationID).Scan(&state); err != nil {
				return normalizeDBError(err)
			}
			if state != "converted" {
				return fmt.Errorf("M3A activation blocker: filled open %s is not fully reconstructed", order.OperationID)
			}
		}
	}
	if err := validateActivationPositions(ctx, tx, snapshot.Positions); err != nil {
		return err
	}
	if err := insertEvent(ctx, tx, "m3a_activated", map[string]any{"cutoff": cutoff}); err != nil {
		return normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO feature_activation (name,activated_at,cutoff)
		VALUES ('m3a',$1,$1)`, cutoff); err != nil {
		return normalizeDBError(err)
	}
	return normalizeDBError(tx.Commit())
}

type activationOperation struct {
	Action         string       `json:"action"`
	Symbol         string       `json:"symbol"`
	Underlying     string       `json:"underlying"`
	Side           string       `json:"side"`
	Kind           string       `json:"kind"`
	Qty            units.Qty    `json:"qty"`
	Multiplier     int64        `json:"multiplier"`
	DerivedMaxRisk units.Micros `json:"derived_max_risk"`
	RequiredCash   units.Micros `json:"required_cash"`
	ApprovedCap    units.Micros `json:"approved_price_cap"`
}

func validateActivationOrder(order Order, operation activationOperation) error {
	symbol := operation.Symbol
	if symbol == "" {
		symbol = operation.Underlying
	}
	if symbol == "" || symbol != order.Symbol || operation.Kind != order.Kind ||
		operation.Multiplier != order.Multiplier || operation.Qty != order.Qty {
		return fmt.Errorf("M3A activation blocker: order %s differs from its operation", order.ID)
	}
	switch operation.Action {
	case "open":
		if operation.Side != "buy" || order.Side != "buy" || operation.ApprovedCap <= 0 ||
			order.Limit > operation.ApprovedCap {
			return fmt.Errorf("M3A activation blocker: open order %s has invalid execution facts", order.ID)
		}
	case "close":
		if operation.Side != "sell" || order.Side != "sell" {
			return fmt.Errorf("M3A activation blocker: close order %s is not a verified reduction", order.ID)
		}
	default:
		return fmt.Errorf("M3A activation blocker: order %s belongs to action %q", order.ID, operation.Action)
	}
	return nil
}

func loadActivationOperation(ctx context.Context, tx *sql.Tx, operationID string) (activationOperation, error) {
	var raw []byte
	var operation activationOperation
	if err := tx.QueryRowContext(ctx, `SELECT payload FROM operations WHERE id=$1`, operationID).Scan(&raw); err != nil {
		return operation, err
	}
	if err := json.Unmarshal(raw, &operation); err != nil {
		return operation, err
	}
	return operation, nil
}

func loadActivationOrders(ctx context.Context, tx *sql.Tx) ([]Order, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+orderColumns+` FROM orders ORDER BY created_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, *order)
	}
	return orders, rows.Err()
}

type activationFill struct {
	ID      string
	OrderID string
	Ledger  string
	Fill    FillInput
}

func loadActivationFills(ctx context.Context, tx *sql.Tx) ([]activationFill, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,order_id,ledger,broker_fill_id,qty,price_micros,fees_micros,ts
		FROM fills ORDER BY ts,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var fills []activationFill
	for rows.Next() {
		var fill activationFill
		var qty, price, fees int64
		if err := rows.Scan(&fill.ID, &fill.OrderID, &fill.Ledger, &fill.Fill.BrokerFillID,
			&qty, &price, &fees, &fill.Fill.TS); err != nil {
			return nil, err
		}
		fill.Fill.Qty, fill.Fill.Price, fill.Fill.Fees = units.Qty(qty), units.Micros(price), units.Micros(fees)
		fills = append(fills, fill)
	}
	return fills, rows.Err()
}

func releaseActivationReservation(ctx context.Context, tx *sql.Tx, reservationID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE open_reservation SET resource_state='released',
		remaining_risk_micros=0,remaining_cash_micros=0,settled_at=now()
		WHERE id=$1 AND resource_state='held'`, reservationID)
	return normalizeDBError(err)
}

func validateActivationPositions(ctx context.Context, tx *sql.Tx, positions []ActivationPosition) error {
	type positionKey struct {
		symbol     string
		kind       string
		multiplier int64
	}
	expected := make(map[positionKey]units.Qty)
	rows, err := tx.QueryContext(ctx, `SELECT symbol,kind,multiplier,
		COALESCE(sum(opened_qty-closed_qty),0)
		FROM exposure_lot WHERE ledger='live' GROUP BY symbol,kind,multiplier`)
	if err != nil {
		return normalizeDBError(err)
	}
	for rows.Next() {
		var key positionKey
		var quantity int64
		if err := rows.Scan(&key.symbol, &key.kind, &key.multiplier, &quantity); err != nil {
			rows.Close()
			return normalizeDBError(err)
		}
		expected[key] = units.Qty(quantity)
	}
	if err := rows.Close(); err != nil {
		return normalizeDBError(err)
	}
	actual := make(map[positionKey]units.Qty)
	for _, position := range positions {
		if position.Qty != 0 {
			if position.Symbol == "" || position.Kind == "" || position.Multiplier <= 0 {
				return fmt.Errorf("M3A activation blocker: broker position metadata is incomplete")
			}
			key := positionKey{
				symbol: position.Symbol, kind: position.Kind, multiplier: position.Multiplier,
			}
			actual[key] += position.Qty
		}
	}
	keys := make([]positionKey, 0, len(expected)+len(actual))
	seen := map[positionKey]bool{}
	for key := range expected {
		seen[key] = true
		keys = append(keys, key)
	}
	for key := range actual {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].symbol != keys[j].symbol {
			return keys[i].symbol < keys[j].symbol
		}
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].multiplier < keys[j].multiplier
	})
	for _, key := range keys {
		if expected[key] != actual[key] {
			return fmt.Errorf("M3A activation blocker: position %s/%s x%d broker=%s durable=%s",
				key.symbol, key.kind, key.multiplier, actual[key], expected[key])
		}
	}
	return nil
}
