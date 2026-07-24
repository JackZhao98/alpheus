package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestAgentPaperWeightedAverageRoundsCostBasisUp(t *testing.T) {
	average, err := agentPaperWeightedAverage(
		units.MustQty("2"), units.MustMicros("10"),
		units.MustQty("1"), units.MustMicros("10.000001"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if average != units.MustMicros("10.000001") {
		t.Fatalf("average=%s", average)
	}
}

func TestValidateAgentPaperOrderRequiresExecutableQuoteSide(t *testing.T) {
	base := AgentPaperOrderInput{
		OrderID:   "27a0d56a-3e25-487d-b8ed-7b90376b9236",
		AccountID: "agent-default", IdempotencyKey: "paper-order-1",
		RequestHash: sha256.Sum256([]byte("paper-order-1")),
		ActorKind:   "agent", ActorID: "cortex-worker",
		Symbol: "SPY", Kind: "equity", Side: "buy", Multiplier: 1,
		Qty: units.MustQty("1"), FillPrice: units.MustMicros("700.02"),
		QuoteBid:    units.MustMicros("700"),
		QuoteAsk:    units.MustMicros("700.02"),
		QuoteSource: "kernel_quote", QuoteAsOf: time.Now().UTC(),
	}
	if err := validateAgentPaperOrder(base); err != nil {
		t.Fatal(err)
	}
	base.FillPrice = base.QuoteBid
	if err := validateAgentPaperOrder(base); !errors.Is(
		err, ErrAgentPaperOrder,
	) {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateAgentPaperOrderRejectsUnsafeIdempotencyKey(t *testing.T) {
	input := AgentPaperOrderInput{
		OrderID:   "9cb0046e-ee7c-4fb5-be98-af5f494d50b2",
		AccountID: "agent-default", IdempotencyKey: "paper order",
		RequestHash: sha256.Sum256([]byte("paper order")),
		ActorKind:   "user", ActorID: "agent-console",
		Symbol: "SPY", Kind: "equity", Side: "sell", Multiplier: 1,
		Qty: units.MustQty("1"), FillPrice: units.MustMicros("700"),
		QuoteBid:    units.MustMicros("700"),
		QuoteAsk:    units.MustMicros("700.02"),
		QuoteSource: "kernel_quote", QuoteAsOf: time.Now().UTC(),
	}
	if err := validateAgentPaperOrder(input); !errors.Is(
		err, ErrAgentPaperOrder,
	) {
		t.Fatalf("err=%v", err)
	}
}

func TestAgentPaperOrderSettlesAndReplaysAtomicallyPostgres(t *testing.T) {
	databaseURL := os.Getenv("ALPHEUS_TEST_AGENT_PAPER_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_AGENT_PAPER_DATABASE_URL is not set")
	}
	migrationsDir := os.Getenv("ALPHEUS_TEST_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "../../../db/migrations"
	}
	s, err := Open(Config{
		URL: databaseURL, MigrationsDir: migrationsDir,
		Timeout: 5 * time.Second, MarketTZ: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	accountID := "paper-test-" + NewID()[:8]
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_paper_account (
		account_id,account_type,starting_cash_micros,cash_micros,
		buying_power_micros,generation,created_at,updated_at
	) VALUES ($1,'paper',$2,$2,$2,1,$3,$3)`,
		accountID, int64(units.MustMicros("1000")), now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_paper_event (
		event_id,account_id,generation,event_type,payload,occurred_at
	) VALUES ($1,$2,1,'account_created','{}'::jsonb,$3)`,
		NewID(), accountID, now,
	); err != nil {
		t.Fatal(err)
	}
	buy := AgentPaperOrderInput{
		OrderID: NewID(), AccountID: accountID,
		IdempotencyKey: "paper-integration-buy",
		RequestHash:    sha256.Sum256([]byte("paper-integration-buy")),
		ActorKind:      "agent", ActorID: "integration-test",
		Symbol: "SPY", Kind: "equity", Side: "buy", Multiplier: 1,
		Qty: units.MustQty("2"), FillPrice: units.MustMicros("10.02"),
		QuoteBid:    units.MustMicros("10"),
		QuoteAsk:    units.MustMicros("10.02"),
		QuoteSource: "integration-quote", QuoteAsOf: now,
	}
	first, err := executeAgentPaperOrderTx(ctx, tx, buy)
	if err != nil || first.Replay ||
		first.Order.Notional != units.MustMicros("20.04") {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := executeAgentPaperOrderTx(ctx, tx, buy)
	if err != nil || !replay.Replay ||
		replay.Order.OrderID != first.Order.OrderID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	conflict := buy
	conflict.RequestHash = sha256.Sum256([]byte("changed"))
	if _, err := executeAgentPaperOrderTx(
		ctx, tx, conflict,
	); !errors.Is(err, ErrAgentPaperIdempotencyConflict) {
		t.Fatalf("conflict err=%v", err)
	}
	sell := AgentPaperOrderInput{
		OrderID: NewID(), AccountID: accountID,
		IdempotencyKey: "paper-integration-sell",
		RequestHash:    sha256.Sum256([]byte("paper-integration-sell")),
		ActorKind:      "agent", ActorID: "integration-test",
		Symbol: "SPY", Kind: "equity", Side: "sell", Multiplier: 1,
		Qty: units.MustQty("1"), FillPrice: units.MustMicros("10"),
		QuoteBid:    units.MustMicros("10"),
		QuoteAsk:    units.MustMicros("10.02"),
		QuoteSource: "integration-quote", QuoteAsOf: now,
	}
	if _, err := executeAgentPaperOrderTx(ctx, tx, sell); err != nil {
		t.Fatal(err)
	}
	var cash, qty, avg int64
	if err := tx.QueryRowContext(ctx, `SELECT
		a.cash_micros,p.qty,p.avg_price_micros
		FROM agent_paper_account a
		JOIN agent_paper_position p ON p.account_id=a.account_id
		WHERE a.account_id=$1 AND p.symbol='SPY'`,
		accountID,
	).Scan(&cash, &qty, &avg); err != nil {
		t.Fatal(err)
	}
	if units.Micros(cash) != units.MustMicros("989.96") ||
		units.Qty(qty) != units.MustQty("1") ||
		units.Micros(avg) != units.MustMicros("10.02") {
		t.Fatalf("cash=%s qty=%s avg=%s",
			units.Micros(cash), units.Qty(qty), units.Micros(avg))
	}
}
