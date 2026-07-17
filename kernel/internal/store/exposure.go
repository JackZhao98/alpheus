package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"alpheus/kernel/internal/units"
)

type OpenReservation struct {
	ID            string
	OperationID   string
	Ledger        string
	MarketDay     time.Time
	Symbol        string
	Kind          string
	OriginalQty   units.Qty
	RemainingQty  units.Qty
	OriginalRisk  units.Micros
	RemainingRisk units.Micros
	OriginalCash  units.Micros
	RemainingCash units.Micros
	ResourceState string
	CreatedAt     time.Time
	SettledAt     time.Time
}

type LedgerResources struct {
	OpenRisk units.Micros
	HeldCash units.Micros
}

type ShadowAccount struct {
	Cash        units.Micros
	BuyingPower units.Micros
}

type ShadowPosition struct {
	Symbol     string
	Kind       string
	Multiplier int64
	Qty        units.Qty
}

func (t *ledgerTx) InsertOpenReservation(reservation OpenReservation) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO open_reservation
		(id,operation_id,ledger,market_day,symbol,kind,original_qty,remaining_qty,
		 original_risk_micros,remaining_risk_micros,original_cash_micros,
		 remaining_cash_micros,resource_state)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
		reservation.ID, reservation.OperationID, reservation.Ledger, reservation.MarketDay,
		reservation.Symbol, reservation.Kind, int64(reservation.OriginalQty),
		int64(reservation.RemainingQty), int64(reservation.OriginalRisk),
		int64(reservation.RemainingRisk), int64(reservation.OriginalCash),
		int64(reservation.RemainingCash), reservation.ResourceState)
	return normalizeDBError(err)
}

func (t *ledgerTx) LedgerResources(ledger, excludeOperationID string) (LedgerResources, error) {
	return ledgerResources(t.ctx, t.tx, ledger, excludeOperationID)
}

func (s *Store) LedgerResources(ledger, excludeOperationID string) (LedgerResources, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	return ledgerResources(ctx, s.DB, ledger, excludeOperationID)
}

func ledgerResources(ctx context.Context, db queryRower, ledger, excludeOperationID string) (LedgerResources, error) {
	var resources LedgerResources
	var risk, cash int64
	err := db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT sum(remaining_risk_micros) FROM exposure_lot
		          WHERE ledger=$1 AND closed_qty < opened_qty),0)
		+ COALESCE((SELECT sum(remaining_risk_micros) FROM open_reservation
		            WHERE ledger=$1 AND resource_state='held'
		              AND (NULLIF($2,'') IS NULL OR operation_id <> NULLIF($2,'')::uuid)),0),
		COALESCE((SELECT sum(remaining_cash_micros) FROM open_reservation
		          WHERE ledger=$1 AND resource_state='held'
		            AND (NULLIF($2,'') IS NULL OR operation_id <> NULLIF($2,'')::uuid)),0)`,
		ledger, excludeOperationID).Scan(&risk, &cash)
	if err != nil {
		return resources, normalizeDBError(err)
	}
	resources.OpenRisk = units.Micros(risk)
	resources.HeldCash = units.Micros(cash)
	return resources, nil
}

func (t *ledgerTx) InsertDayOpen(marketDay time.Time, ledger string, equity units.Micros) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO day_open (market_day,ledger,equity_micros)
		VALUES ($1,$2,$3) ON CONFLICT (market_day,ledger) DO NOTHING`, marketDay, ledger, int64(equity))
	return normalizeDBError(err)
}

func (s *Store) InsertDayOpen(marketDay time.Time, ledger string, equity units.Micros) error {
	ctx, cancel := s.deadline()
	defer cancel()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO day_open (market_day,ledger,equity_micros)
		VALUES ($1,$2,$3) ON CONFLICT (market_day,ledger) DO NOTHING`, marketDay, ledger, int64(equity))
	return normalizeDBError(err)
}

func (s *Store) GetOpenReservation(id string) (*OpenReservation, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var reservation OpenReservation
	var originalQty, remainingQty, originalRisk, remainingRisk, originalCash, remainingCash int64
	var settledAt sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT id,operation_id,ledger,market_day,symbol,kind,
		original_qty,remaining_qty,original_risk_micros,remaining_risk_micros,
		original_cash_micros,remaining_cash_micros,resource_state,created_at,settled_at
		FROM open_reservation WHERE id=$1`, id).Scan(
		&reservation.ID, &reservation.OperationID, &reservation.Ledger, &reservation.MarketDay,
		&reservation.Symbol, &reservation.Kind, &originalQty, &remainingQty, &originalRisk,
		&remainingRisk, &originalCash, &remainingCash, &reservation.ResourceState,
		&reservation.CreatedAt, &settledAt)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	reservation.OriginalQty, reservation.RemainingQty = units.Qty(originalQty), units.Qty(remainingQty)
	reservation.OriginalRisk, reservation.RemainingRisk = units.Micros(originalRisk), units.Micros(remainingRisk)
	reservation.OriginalCash, reservation.RemainingCash = units.Micros(originalCash), units.Micros(remainingCash)
	if settledAt.Valid {
		reservation.SettledAt = settledAt.Time
	}
	return &reservation, nil
}

func (t *ledgerTx) ShadowAccount() (ShadowAccount, error) {
	return shadowAccount(t.ctx, t.tx)
}

func (s *Store) ShadowAccount() (ShadowAccount, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	return shadowAccount(ctx, s.DB)
}

func shadowAccount(ctx context.Context, db queryRower) (ShadowAccount, error) {
	var account ShadowAccount
	var cash, buyingPower int64
	err := db.QueryRowContext(ctx, `SELECT cash_micros,buying_power_micros
		FROM shadow_account WHERE singleton=true`).Scan(&cash, &buyingPower)
	account.Cash, account.BuyingPower = units.Micros(cash), units.Micros(buyingPower)
	return account, normalizeDBError(err)
}

func (t *ledgerTx) ShadowPositions() ([]ShadowPosition, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT symbol,kind,multiplier,qty FROM shadow_positions ORDER BY symbol`)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	var positions []ShadowPosition
	for rows.Next() {
		var position ShadowPosition
		var quantity int64
		if err := rows.Scan(&position.Symbol, &position.Kind, &position.Multiplier, &quantity); err != nil {
			return nil, normalizeDBError(err)
		}
		position.Qty = units.Qty(quantity)
		positions = append(positions, position)
	}
	return positions, normalizeDBError(rows.Err())
}

func (t *ledgerTx) OpenExposureQuantity(ledger, symbol, kind string) (units.Qty, error) {
	var quantity int64
	err := t.tx.QueryRowContext(t.ctx, `SELECT COALESCE(sum(opened_qty-closed_qty),0)
		FROM exposure_lot WHERE ledger=$1 AND symbol=$2 AND kind=$3 AND closed_qty < opened_qty`,
		ledger, symbol, kind).Scan(&quantity)
	return units.Qty(quantity), normalizeDBError(err)
}

func (t *ledgerTx) FirstOpenExposureOperation(ledger, symbol, kind string) (string, error) {
	var operationID string
	err := t.tx.QueryRowContext(t.ctx, `SELECT operation_id FROM exposure_lot
		WHERE ledger=$1 AND symbol=$2 AND kind=$3 AND closed_qty < opened_qty
		ORDER BY opened_at,open_fill_id LIMIT 1`, ledger, symbol, kind).Scan(&operationID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return operationID, normalizeDBError(err)
}

func applyExposureFill(ctx context.Context, tx *sql.Tx, order *Order, fillID string, fill FillInput, openReservationID sql.NullString) error {
	var action string
	if err := tx.QueryRowContext(ctx, `SELECT payload->>'action' FROM operations WHERE id=$1`, order.OperationID).Scan(&action); err != nil {
		return err
	}
	switch action {
	case "open":
		if !openReservationID.Valid {
			return fmt.Errorf("%w: open fill has no reservation", ErrFillIntegrity)
		}
		return applyOpenExposureFill(ctx, tx, order, fillID, fill, openReservationID.String)
	case "close":
		return applyCloseExposureFill(ctx, tx, order, fillID, fill)
	default:
		return fmt.Errorf("%w: fill belongs to unsupported operation %s", ErrFillIntegrity, action)
	}
}

func applyOpenExposureFill(ctx context.Context, tx *sql.Tx, order *Order, fillID string, fill FillInput, reservationID string) error {
	if order.Side != "buy" || fill.Price > order.Limit {
		return fmt.Errorf("%w: open fill exceeds approved limit", ErrFillIntegrity)
	}
	cost, err := units.MulQtyPrice(fill.Qty, fill.Price, order.Multiplier, true)
	if err != nil {
		return err
	}
	cost, err = units.Add(cost, fill.Fees)
	if err != nil || cost <= 0 {
		return fmt.Errorf("%w: invalid open fill cost", ErrFillIntegrity)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO exposure_lot
		(open_fill_id,operation_id,ledger,symbol,kind,multiplier,opened_qty,closed_qty,
		 entry_cost_micros,remaining_cost_basis_micros,remaining_risk_micros,opened_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$8,$8,$9)`, fillID, order.OperationID,
		order.Ledger, order.Symbol, order.Kind, order.Multiplier, int64(fill.Qty),
		int64(cost), fill.TS); err != nil {
		return err
	}

	var originalQty, remainingQty, originalRisk, originalCash int64
	var ledger, symbol, kind, resourceState string
	if err := tx.QueryRowContext(ctx, `SELECT ledger,symbol,kind,original_qty,remaining_qty,
		original_risk_micros,original_cash_micros,resource_state
		FROM open_reservation WHERE id=$1 FOR UPDATE`, reservationID).Scan(
		&ledger, &symbol, &kind, &originalQty, &remainingQty, &originalRisk, &originalCash,
		&resourceState); err != nil {
		return err
	}
	if ledger != order.Ledger || symbol != order.Symbol || kind != order.Kind ||
		resourceState != "held" || int64(fill.Qty) > remainingQty {
		return fmt.Errorf("%w: open fill does not match held reservation", ErrFillIntegrity)
	}
	nextQty := remainingQty - int64(fill.Qty)
	nextRisk, err := proportionalInt64(originalRisk, nextQty, originalQty, true)
	if err != nil {
		return err
	}
	nextCash, err := proportionalInt64(originalCash, nextQty, originalQty, true)
	if err != nil {
		return err
	}
	nextState := "held"
	var settledAt any
	if nextQty == 0 {
		nextState = "converted"
		nextRisk, nextCash, settledAt = 0, 0, time.Now().UTC()
	}
	if _, err := tx.ExecContext(ctx, `UPDATE open_reservation SET remaining_qty=$2,
		remaining_risk_micros=$3,remaining_cash_micros=$4,resource_state=$5,settled_at=$6
		WHERE id=$1`, reservationID, nextQty, nextRisk, nextCash, nextState, settledAt); err != nil {
		return err
	}
	if order.Ledger == "shadow" {
		if err := applyShadowOpenFill(ctx, tx, order, fill, cost); err != nil {
			return err
		}
	}
	return nil
}

func applyCloseExposureFill(ctx context.Context, tx *sql.Tx, order *Order, closeFillID string, fill FillInput) error {
	if order.Side != "sell" {
		return fmt.Errorf("%w: long-only close fill is not a sell", ErrFillIntegrity)
	}
	if fill.Price < order.Limit {
		return fmt.Errorf("%w: close fill violates approved limit", ErrFillIntegrity)
	}
	rows, err := tx.QueryContext(ctx, `SELECT open_fill_id,opened_qty,closed_qty,
		entry_cost_micros,remaining_cost_basis_micros,remaining_risk_micros
		FROM exposure_lot WHERE ledger=$1 AND symbol=$2 AND kind=$3 AND closed_qty < opened_qty
		ORDER BY opened_at,open_fill_id FOR UPDATE`, order.Ledger, order.Symbol, order.Kind)
	if err != nil {
		return err
	}
	type lotRow struct {
		id                                         string
		opened, closed, entry, remainingCost, risk int64
	}
	var lots []lotRow
	for rows.Next() {
		var lot lotRow
		if err := rows.Scan(&lot.id, &lot.opened, &lot.closed, &lot.entry, &lot.remainingCost, &lot.risk); err != nil {
			rows.Close()
			return err
		}
		lots = append(lots, lot)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	remainingFill := int64(fill.Qty)
	for _, lot := range lots {
		if remainingFill == 0 {
			break
		}
		available := lot.opened - lot.closed
		matched := available
		if matched > remainingFill {
			matched = remainingFill
		}
		nextClosed := lot.closed + matched
		nextRemaining := lot.opened - nextClosed
		// Risk remainder rounds up so the release rounds down, against the
		// account. Cost-basis remainder rounds down so interim realized PnL
		// cannot be flattered.
		nextRisk, err := proportionalInt64(lot.entry, nextRemaining, lot.opened, true)
		if err != nil {
			return err
		}
		nextCost, err := proportionalInt64(lot.entry, nextRemaining, lot.opened, false)
		if err != nil {
			return err
		}
		matchedCost := lot.remainingCost - nextCost
		releasedRisk := lot.risk - nextRisk
		if matchedCost < 0 || releasedRisk < 0 {
			return fmt.Errorf("%w: exposure remainder increased", ErrFillIntegrity)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO exposure_close_allocation
			(close_fill_id,open_fill_id,qty,matched_cost_micros,released_risk_micros)
			VALUES ($1,$2,$3,$4,$5)`, closeFillID, lot.id, matched, matchedCost, releasedRisk); err != nil {
			return err
		}
		var closedAt any
		if nextRemaining == 0 {
			closedAt = fill.TS
		}
		if _, err := tx.ExecContext(ctx, `UPDATE exposure_lot SET closed_qty=$2,
			remaining_cost_basis_micros=$3,remaining_risk_micros=$4,closed_at=$5
			WHERE open_fill_id=$1`, lot.id, nextClosed, nextCost, nextRisk, closedAt); err != nil {
			return err
		}
		remainingFill -= matched
	}
	if remainingFill != 0 {
		return fmt.Errorf("%w: close fill exceeds FIFO exposure", ErrFillIntegrity)
	}
	if order.Ledger == "shadow" {
		if err := applyShadowCloseFill(ctx, tx, order, fill); err != nil {
			return err
		}
	}
	return nil
}

func proportionalInt64(original, remaining, total int64, ceil bool) (int64, error) {
	if original < 0 || remaining < 0 || total <= 0 || remaining > total {
		return 0, fmt.Errorf("invalid proportional inputs")
	}
	numerator := new(big.Int).Mul(big.NewInt(original), big.NewInt(remaining))
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, big.NewInt(total), remainder)
	if ceil && remainder.Sign() != 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, fmt.Errorf("proportional result overflows int64")
	}
	return quotient.Int64(), nil
}

func applyShadowOpenFill(ctx context.Context, tx *sql.Tx, order *Order, fill FillInput, cost units.Micros) error {
	result, err := tx.ExecContext(ctx, `UPDATE shadow_account SET
		cash_micros=cash_micros-$1,buying_power_micros=buying_power_micros-$1,updated_at=now()
		WHERE singleton=true AND cash_micros >= $1 AND buying_power_micros >= $1`, int64(cost))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("%w: shadow buying power is insufficient", ErrFillIntegrity)
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO shadow_positions (symbol,kind,multiplier,qty,updated_at)
		VALUES ($1,$2,$3,$4,now())
		ON CONFLICT (symbol) DO UPDATE SET qty=shadow_positions.qty+EXCLUDED.qty,
		updated_at=now()
		WHERE shadow_positions.kind=EXCLUDED.kind
		  AND shadow_positions.multiplier=EXCLUDED.multiplier`, order.Symbol, order.Kind,
		order.Multiplier, int64(fill.Qty))
	if err != nil {
		return err
	}
	affected, err = result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("%w: shadow position metadata changed", ErrFillIntegrity)
	}
	return nil
}

func applyShadowCloseFill(ctx context.Context, tx *sql.Tx, order *Order, fill FillInput) error {
	result, err := tx.ExecContext(ctx, `UPDATE shadow_positions SET qty=qty-$2,updated_at=now()
		WHERE symbol=$1 AND kind=$3 AND multiplier=$4 AND qty >= $2`, order.Symbol,
		int64(fill.Qty), order.Kind, order.Multiplier)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("%w: shadow close exceeds paper position", ErrFillIntegrity)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM shadow_positions WHERE symbol=$1 AND qty=0`, order.Symbol); err != nil {
		return err
	}
	proceeds, err := units.MulQtyPrice(fill.Qty, fill.Price, order.Multiplier, false)
	if err != nil {
		return err
	}
	proceeds, err = units.Add(proceeds, -fill.Fees)
	if err != nil || proceeds < 0 {
		return fmt.Errorf("%w: invalid shadow close proceeds", ErrFillIntegrity)
	}
	_, err = tx.ExecContext(ctx, `UPDATE shadow_account SET cash_micros=cash_micros+$1,
		buying_power_micros=buying_power_micros+$1,updated_at=now() WHERE singleton=true`, int64(proceeds))
	return err
}
