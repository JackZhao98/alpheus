package broker

import (
	"testing"

	"alpheus/kernel/internal/units"
)

func TestShortSaleProceedsDoNotInflateEquity(t *testing.T) {
	broker := NewFake(units.MustMicros("300"))
	if err := broker.SetQuote(Quote{
		Symbol: "X", Bid: units.MustMicros("99.90"),
		Ask: units.MustMicros("100"), OpenInterest: 1_000,
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := broker.PlaceLimitOrder(
		"X", "sell", units.MustQty("1"), units.MustMicros("99.90"), "equity",
	); err != nil || result.State != "filled" {
		t.Fatalf("seed short: result=%+v err=%v", result, err)
	}
	account, err := broker.GetAccount()
	if err != nil {
		t.Fatal(err)
	}
	if !account.EquityKnown || account.Equity != units.MustMicros("299.90") {
		t.Fatalf("account=%+v, want liquidation equity 299.90", account)
	}
	if account.BuyingPower != units.MustMicros("399.90") {
		t.Fatalf("buying power=%s, want cash-based 399.90", account.BuyingPower)
	}
}

func TestMissingPositionMarkDegradesEquity(t *testing.T) {
	broker := NewFake(units.MustMicros("300"))
	if err := broker.SetQuote(Quote{
		Symbol: "A", Bid: units.MustMicros("9.90"),
		Ask: units.MustMicros("10"), OpenInterest: 1_000,
	}); err != nil {
		t.Fatal(err)
	}
	if result, err := broker.PlaceLimitOrder(
		"A", "buy", units.MustQty("1"), units.MustMicros("10"), "equity",
	); err != nil || result.State != "filled" {
		t.Fatalf("seed long: result=%+v err=%v", result, err)
	}
	broker.DeleteQuote("A")
	account, err := broker.GetAccount()
	if err != nil {
		t.Fatal(err)
	}
	if account.EquityKnown {
		t.Fatalf("account=%+v, want unknown equity", account)
	}
}

func TestRestingLimitFillsWhenQuoteTradesThrough(t *testing.T) {
	b := NewFake(units.MustMicros("300"))
	if err := b.SetQuote(Quote{
		Symbol: "X", Bid: units.MustMicros("100"), Ask: units.MustMicros("100.10"),
	}); err != nil {
		t.Fatal(err)
	}
	order, err := b.PlaceLimitOrder(
		"X", "buy", units.MustQty("1"), units.MustMicros("100.05"), "equity",
	)
	if err != nil || order.State != "submitted" {
		t.Fatalf("order=%+v err=%v, want submitted", order, err)
	}
	if err := b.SetQuote(Quote{
		Symbol: "X", Bid: units.MustMicros("100.04"), Ask: units.MustMicros("100.05"),
	}); err != nil {
		t.Fatal(err)
	}
	order, err = b.GetOrder(order.BrokerOrderID)
	if err != nil || order.State != "filled" || order.FilledPrice != units.MustMicros("100.05") {
		t.Fatalf("order=%+v err=%v, want filled at 100.05", order, err)
	}
	positions, err := b.GetPositions()
	if err != nil || len(positions) != 1 || positions[0].Qty != units.MustQty("1") {
		t.Fatalf("positions=%+v err=%v", positions, err)
	}
}
