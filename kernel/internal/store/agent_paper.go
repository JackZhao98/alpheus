package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

var (
	ErrAgentPaperIdempotencyConflict = errors.New("agent paper idempotency conflict")
	ErrAgentPaperBuyingPower         = errors.New("agent paper buying power is insufficient")
	ErrAgentPaperPosition            = errors.New("agent paper position is unavailable")
	ErrAgentPaperOrder               = errors.New("agent paper order is invalid")
)

type AgentPaperAccount struct {
	AccountID    string
	AccountType  string
	StartingCash units.Micros
	Cash         units.Micros
	BuyingPower  units.Micros
	Generation   int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AgentPaperPosition struct {
	AccountID  string
	Symbol     string
	Kind       string
	Multiplier int64
	Qty        units.Qty
	AvgPrice   units.Micros
	Generation int64
	OpenedAt   time.Time
	UpdatedAt  time.Time
}

type AgentPaperOrderInput struct {
	OrderID        string
	AccountID      string
	IdempotencyKey string
	RequestHash    [sha256.Size]byte
	ActorKind      string
	ActorID        string
	Symbol         string
	Kind           string
	Side           string
	Multiplier     int64
	Qty            units.Qty
	FillPrice      units.Micros
	QuoteBid       units.Micros
	QuoteAsk       units.Micros
	QuoteSource    string
	QuoteAsOf      time.Time
}

type AgentPaperOrder struct {
	OrderID        string
	AccountID      string
	IdempotencyKey string
	RequestHash    [sha256.Size]byte
	ActorKind      string
	ActorID        string
	Symbol         string
	Kind           string
	Side           string
	Multiplier     int64
	Qty            units.Qty
	FillPrice      units.Micros
	Notional       units.Micros
	QuoteBid       units.Micros
	QuoteAsk       units.Micros
	QuoteSource    string
	QuoteAsOf      time.Time
	State          string
	Generation     int64
	CreatedAt      time.Time
	FilledAt       time.Time
}

type AgentPaperOrderResult struct {
	Order  AgentPaperOrder
	Replay bool
}

func (s *Store) AgentPaperPortfolio(
	accountID string,
) (AgentPaperAccount, []AgentPaperPosition, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var account AgentPaperAccount
	var startingCash, cash, buyingPower int64
	err := s.DB.QueryRowContext(ctx, `SELECT
		account_id,account_type,starting_cash_micros,cash_micros,
		buying_power_micros,generation,created_at,updated_at
		FROM agent_paper_account WHERE account_id=$1`,
		accountID,
	).Scan(
		&account.AccountID, &account.AccountType, &startingCash, &cash,
		&buyingPower, &account.Generation, &account.CreatedAt,
		&account.UpdatedAt,
	)
	if err != nil {
		return AgentPaperAccount{}, nil, normalizeDBError(err)
	}
	account.StartingCash = units.Micros(startingCash)
	account.Cash = units.Micros(cash)
	account.BuyingPower = units.Micros(buyingPower)
	rows, err := s.DB.QueryContext(ctx, `SELECT
		account_id,symbol,kind,multiplier,qty,avg_price_micros,
		generation,opened_at,updated_at
		FROM agent_paper_position WHERE account_id=$1 ORDER BY symbol`,
		accountID,
	)
	if err != nil {
		return AgentPaperAccount{}, nil, normalizeDBError(err)
	}
	defer rows.Close()
	positions := make([]AgentPaperPosition, 0)
	for rows.Next() {
		var position AgentPaperPosition
		var qty, avgPrice int64
		if err := rows.Scan(
			&position.AccountID, &position.Symbol, &position.Kind,
			&position.Multiplier, &qty, &avgPrice, &position.Generation,
			&position.OpenedAt, &position.UpdatedAt,
		); err != nil {
			return AgentPaperAccount{}, nil, normalizeDBError(err)
		}
		position.Qty = units.Qty(qty)
		position.AvgPrice = units.Micros(avgPrice)
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return AgentPaperAccount{}, nil, normalizeDBError(err)
	}
	return account, positions, nil
}

func (s *Store) ListAgentPaperOrders(
	accountID string,
	limit int,
) ([]AgentPaperOrder, error) {
	if strings.TrimSpace(accountID) == "" || limit < 1 || limit > 100 {
		return nil, ErrAgentPaperOrder
	}
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, `SELECT
		order_id,account_id,idempotency_key,request_hash,actor_kind,actor_id,
		symbol,kind,side,multiplier,qty,fill_price_micros,notional_micros,
		quote_bid_micros,quote_ask_micros,quote_source,quote_observed_at,
		state,generation,created_at,filled_at
		FROM agent_paper_order
		WHERE account_id=$1
		ORDER BY filled_at DESC,order_id DESC LIMIT $2`,
		accountID, limit,
	)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	orders := make([]AgentPaperOrder, 0)
	for rows.Next() {
		order, scanErr := scanAgentPaperOrder(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		orders = append(orders, *order)
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeDBError(err)
	}
	return orders, nil
}

func (s *Store) ExecuteAgentPaperOrder(
	input AgentPaperOrderInput,
) (AgentPaperOrderResult, error) {
	if err := validateAgentPaperOrder(input); err != nil {
		return AgentPaperOrderResult{}, err
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	result, err := executeAgentPaperOrderTx(ctx, tx, input)
	if err != nil {
		return AgentPaperOrderResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}
	committed = true
	return result, nil
}

func executeAgentPaperOrderTx(
	ctx context.Context,
	tx *sql.Tx,
	input AgentPaperOrderInput,
) (AgentPaperOrderResult, error) {
	var account AgentPaperAccount
	var startingCash, cash, buyingPower int64
	err := tx.QueryRowContext(ctx, `SELECT
		account_id,account_type,starting_cash_micros,cash_micros,
		buying_power_micros,generation,created_at,updated_at
		FROM agent_paper_account WHERE account_id=$1 FOR UPDATE`,
		input.AccountID,
	).Scan(
		&account.AccountID, &account.AccountType, &startingCash, &cash,
		&buyingPower, &account.Generation, &account.CreatedAt,
		&account.UpdatedAt,
	)
	if err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}
	account.StartingCash = units.Micros(startingCash)
	account.Cash = units.Micros(cash)
	account.BuyingPower = units.Micros(buyingPower)

	existing, err := agentPaperOrderByIdempotency(
		ctx, tx, input.AccountID, input.IdempotencyKey,
	)
	if err != nil {
		return AgentPaperOrderResult{}, err
	}
	if existing != nil {
		if !bytes.Equal(existing.RequestHash[:], input.RequestHash[:]) {
			return AgentPaperOrderResult{}, ErrAgentPaperIdempotencyConflict
		}
		return AgentPaperOrderResult{Order: *existing, Replay: true}, nil
	}

	notional, err := units.MulQtyPrice(
		input.Qty, input.FillPrice, input.Multiplier,
		input.Side == "buy",
	)
	if err != nil || notional <= 0 {
		return AgentPaperOrderResult{}, fmt.Errorf(
			"%w: invalid notional", ErrAgentPaperOrder,
		)
	}
	var now time.Time
	if err := tx.QueryRowContext(
		ctx, `SELECT clock_timestamp()`,
	).Scan(&now); err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}
	generation := account.Generation + 1

	position, err := lockedAgentPaperPosition(
		ctx, tx, input.AccountID, input.Symbol,
	)
	if err != nil {
		return AgentPaperOrderResult{}, err
	}
	eventType := "order_filled"
	if input.Side == "buy" {
		if account.Cash < notional {
			return AgentPaperOrderResult{}, ErrAgentPaperBuyingPower
		}
		account.Cash -= notional
		account.BuyingPower = account.Cash
		if position == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO agent_paper_position (
				account_id,symbol,kind,multiplier,qty,avg_price_micros,
				generation,opened_at,updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)`,
				input.AccountID, input.Symbol, input.Kind, input.Multiplier,
				int64(input.Qty), int64(input.FillPrice), generation, now,
			)
		} else {
			if position.Kind != input.Kind ||
				position.Multiplier != input.Multiplier {
				return AgentPaperOrderResult{}, fmt.Errorf(
					"%w: position contract mismatch", ErrAgentPaperOrder,
				)
			}
			newQty, addErr := units.AddQty(position.Qty, input.Qty)
			if addErr != nil {
				return AgentPaperOrderResult{}, fmt.Errorf(
					"%w: %v", ErrAgentPaperOrder, addErr,
				)
			}
			avgPrice, avgErr := agentPaperWeightedAverage(
				position.Qty, position.AvgPrice,
				input.Qty, input.FillPrice,
			)
			if avgErr != nil {
				return AgentPaperOrderResult{}, avgErr
			}
			_, err = tx.ExecContext(ctx, `UPDATE agent_paper_position SET
				qty=$3,avg_price_micros=$4,generation=$5,updated_at=$6
				WHERE account_id=$1 AND symbol=$2`,
				input.AccountID, input.Symbol, int64(newQty),
				int64(avgPrice), generation, now,
			)
		}
	} else {
		if position == nil || position.Qty < input.Qty ||
			position.Kind != input.Kind ||
			position.Multiplier != input.Multiplier {
			return AgentPaperOrderResult{}, ErrAgentPaperPosition
		}
		account.Cash, err = units.Add(account.Cash, notional)
		if err != nil {
			return AgentPaperOrderResult{}, fmt.Errorf(
				"%w: %v", ErrAgentPaperOrder, err,
			)
		}
		account.BuyingPower = account.Cash
		remaining := position.Qty - input.Qty
		if remaining == 0 {
			eventType = "position_closed"
			_, err = tx.ExecContext(ctx, `DELETE FROM agent_paper_position
				WHERE account_id=$1 AND symbol=$2`,
				input.AccountID, input.Symbol,
			)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE agent_paper_position SET
				qty=$3,generation=$4,updated_at=$5
				WHERE account_id=$1 AND symbol=$2`,
				input.AccountID, input.Symbol, int64(remaining),
				generation, now,
			)
		}
	}
	if err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_paper_account SET
		cash_micros=$2,buying_power_micros=$3,generation=$4,updated_at=$5
		WHERE account_id=$1`,
		input.AccountID, int64(account.Cash), int64(account.BuyingPower),
		generation, now,
	); err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}

	order := AgentPaperOrder{
		OrderID: input.OrderID, AccountID: input.AccountID,
		IdempotencyKey: input.IdempotencyKey,
		RequestHash:    input.RequestHash, ActorKind: input.ActorKind,
		ActorID: input.ActorID, Symbol: input.Symbol, Kind: input.Kind,
		Side: input.Side, Multiplier: input.Multiplier, Qty: input.Qty,
		FillPrice: input.FillPrice, Notional: notional,
		QuoteBid: input.QuoteBid, QuoteAsk: input.QuoteAsk,
		QuoteSource: input.QuoteSource, QuoteAsOf: input.QuoteAsOf.UTC(),
		State: "filled", Generation: generation,
		CreatedAt: now.UTC(), FilledAt: now.UTC(),
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_paper_order (
		order_id,account_id,idempotency_key,request_hash,actor_kind,actor_id,
		symbol,kind,side,multiplier,qty,fill_price_micros,notional_micros,
		quote_bid_micros,quote_ask_micros,quote_source,quote_observed_at,
		state,generation,created_at,filled_at
	) VALUES (
		$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,
		$18,$19,$20,$21
	)`,
		order.OrderID, order.AccountID, order.IdempotencyKey,
		order.RequestHash[:], order.ActorKind, order.ActorID, order.Symbol,
		order.Kind, order.Side, order.Multiplier, int64(order.Qty),
		int64(order.FillPrice), int64(order.Notional),
		int64(order.QuoteBid), int64(order.QuoteAsk), order.QuoteSource,
		order.QuoteAsOf, order.State, order.Generation, order.CreatedAt,
		order.FilledAt,
	); err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}
	payload, err := json.Marshal(map[string]any{
		"schema_revision": 1,
		"order_id":        order.OrderID,
		"actor_kind":      order.ActorKind,
		"actor_id":        order.ActorID,
		"symbol":          order.Symbol,
		"kind":            order.Kind,
		"side":            order.Side,
		"multiplier":      order.Multiplier,
		"qty":             order.Qty,
		"fill_price":      order.FillPrice,
		"notional":        order.Notional,
		"quote_source":    order.QuoteSource,
		"quote_as_of":     order.QuoteAsOf,
	})
	if err != nil {
		return AgentPaperOrderResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_paper_event (
		event_id,account_id,generation,event_type,payload,occurred_at
	) VALUES ($1,$2,$3,$4,$5,$6)`,
		NewID(), input.AccountID, generation, eventType, payload, now,
	); err != nil {
		return AgentPaperOrderResult{}, normalizeDBError(err)
	}
	return AgentPaperOrderResult{Order: order}, nil
}

func validateAgentPaperOrder(input AgentPaperOrderInput) error {
	if input.OrderID == "" || input.AccountID == "" ||
		input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 ||
		input.ActorID == "" || len(input.ActorID) > 200 ||
		strings.TrimSpace(input.Symbol) == "" ||
		input.Symbol != strings.ToUpper(input.Symbol) ||
		input.Qty <= 0 || input.Multiplier <= 0 ||
		input.FillPrice <= 0 || input.QuoteBid <= 0 ||
		input.QuoteAsk <= input.QuoteBid ||
		input.QuoteSource == "" || input.QuoteAsOf.IsZero() {
		return ErrAgentPaperOrder
	}
	if input.ActorKind != "user" && input.ActorKind != "agent" &&
		input.ActorKind != "trigger" {
		return ErrAgentPaperOrder
	}
	if input.Kind != "equity" && input.Kind != "option" {
		return ErrAgentPaperOrder
	}
	if input.Side != "buy" && input.Side != "sell" {
		return ErrAgentPaperOrder
	}
	if input.Side == "buy" && input.FillPrice != input.QuoteAsk ||
		input.Side == "sell" && input.FillPrice != input.QuoteBid {
		return ErrAgentPaperOrder
	}
	for _, char := range input.IdempotencyKey {
		if char <= ' ' {
			return ErrAgentPaperOrder
		}
	}
	return nil
}

func agentPaperWeightedAverage(
	oldQty units.Qty,
	oldPrice units.Micros,
	addQty units.Qty,
	addPrice units.Micros,
) (units.Micros, error) {
	if oldQty <= 0 || oldPrice <= 0 || addQty <= 0 || addPrice <= 0 {
		return 0, ErrAgentPaperOrder
	}
	totalQty, err := units.AddQty(oldQty, addQty)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrAgentPaperOrder, err)
	}
	numerator := new(big.Int).Mul(
		big.NewInt(int64(oldQty)), big.NewInt(int64(oldPrice)),
	)
	numerator.Add(numerator, new(big.Int).Mul(
		big.NewInt(int64(addQty)), big.NewInt(int64(addPrice)),
	))
	average, remainder := new(big.Int), new(big.Int)
	average.QuoRem(numerator, big.NewInt(int64(totalQty)), remainder)
	if remainder.Sign() != 0 {
		average.Add(average, big.NewInt(1))
	}
	if !average.IsInt64() {
		return 0, ErrAgentPaperOrder
	}
	return units.Micros(average.Int64()), nil
}

func lockedAgentPaperPosition(
	ctx context.Context,
	tx *sql.Tx,
	accountID string,
	symbol string,
) (*AgentPaperPosition, error) {
	var position AgentPaperPosition
	var qty, avgPrice int64
	err := tx.QueryRowContext(ctx, `SELECT
		account_id,symbol,kind,multiplier,qty,avg_price_micros,
		generation,opened_at,updated_at
		FROM agent_paper_position
		WHERE account_id=$1 AND symbol=$2 FOR UPDATE`,
		accountID, symbol,
	).Scan(
		&position.AccountID, &position.Symbol, &position.Kind,
		&position.Multiplier, &qty, &avgPrice, &position.Generation,
		&position.OpenedAt, &position.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	position.Qty = units.Qty(qty)
	position.AvgPrice = units.Micros(avgPrice)
	return &position, nil
}

func agentPaperOrderByIdempotency(
	ctx context.Context,
	tx *sql.Tx,
	accountID string,
	idempotencyKey string,
) (*AgentPaperOrder, error) {
	return scanAgentPaperOrder(tx.QueryRowContext(ctx, `SELECT
		order_id,account_id,idempotency_key,request_hash,actor_kind,actor_id,
		symbol,kind,side,multiplier,qty,fill_price_micros,notional_micros,
		quote_bid_micros,quote_ask_micros,quote_source,quote_observed_at,
		state,generation,created_at,filled_at
		FROM agent_paper_order
		WHERE account_id=$1 AND idempotency_key=$2`,
		accountID, idempotencyKey,
	))
}

func scanAgentPaperOrder(row rowScanner) (*AgentPaperOrder, error) {
	var order AgentPaperOrder
	var requestHash []byte
	var qty, fillPrice, notional, quoteBid, quoteAsk int64
	err := row.Scan(
		&order.OrderID, &order.AccountID, &order.IdempotencyKey,
		&requestHash, &order.ActorKind, &order.ActorID, &order.Symbol,
		&order.Kind, &order.Side, &order.Multiplier, &qty, &fillPrice,
		&notional, &quoteBid, &quoteAsk, &order.QuoteSource,
		&order.QuoteAsOf, &order.State, &order.Generation,
		&order.CreatedAt, &order.FilledAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if len(requestHash) != sha256.Size {
		return nil, fmt.Errorf("%w: invalid stored request hash", ErrAgentPaperOrder)
	}
	copy(order.RequestHash[:], requestHash)
	order.Qty = units.Qty(qty)
	order.FillPrice = units.Micros(fillPrice)
	order.Notional = units.Micros(notional)
	order.QuoteBid = units.Micros(quoteBid)
	order.QuoteAsk = units.Micros(quoteAsk)
	return &order, nil
}
