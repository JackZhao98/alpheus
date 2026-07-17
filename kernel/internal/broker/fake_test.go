package broker

import (
	"context"
	"testing"
	"time"

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
	if result, err := broker.PlaceLimitOrder(context.Background(), PlaceRequest{
		Symbol: "X", Side: "sell", Qty: units.MustQty("1"), Limit: units.MustMicros("99.90"), Kind: "equity",
	}); err != nil || result.State != "filled" {
		t.Fatalf("seed short: result=%+v err=%v", result, err)
	}
	account, err := broker.Account(context.Background())
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

func TestRecentFillsUseDurableFillTimestamp(t *testing.T) {
	b := NewFake(units.MustMicros("300"))
	if err := b.SetQuote(Quote{Symbol: "FILL", Bid: units.MustMicros("9.9"), Ask: units.MustMicros("10")}); err != nil {
		t.Fatal(err)
	}
	result, err := b.PlaceLimitOrder(context.Background(), PlaceRequest{
		Symbol: "FILL", Side: "buy", Qty: units.MustQty("1"), Limit: units.MustMicros("10"), Kind: "equity",
	})
	if err != nil || result.State != "filled" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(result.Fills) != 1 || result.Fills[0].FillID != "fake-fill-"+result.BrokerOrderID || result.Fills[0].AsOf.IsZero() {
		t.Fatalf("result does not carry a stable fill: %+v", result)
	}
	fills, err := b.RecentFills(context.Background(), time.Time{})
	if err != nil || len(fills) != 1 || fills[0].AsOf.IsZero() {
		t.Fatalf("fills=%+v err=%v", fills, err)
	}
	fills, err = b.RecentFills(context.Background(), fills[0].AsOf.Add(time.Nanosecond))
	if err != nil || len(fills) != 0 {
		t.Fatalf("post-fill window returned old fills=%+v err=%v", fills, err)
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
	if result, err := broker.PlaceLimitOrder(context.Background(), PlaceRequest{
		Symbol: "A", Side: "buy", Qty: units.MustQty("1"), Limit: units.MustMicros("10"), Kind: "equity",
	}); err != nil || result.State != "filled" {
		t.Fatalf("seed long: result=%+v err=%v", result, err)
	}
	broker.DeleteQuote("A")
	account, err := broker.Account(context.Background())
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
	order, err := b.PlaceLimitOrder(context.Background(), PlaceRequest{
		Symbol: "X", Side: "buy", Qty: units.MustQty("1"), Limit: units.MustMicros("100.05"), Kind: "equity",
	})
	if err != nil || order.State != "submitted" {
		t.Fatalf("order=%+v err=%v, want submitted", order, err)
	}
	if err := b.SetQuote(Quote{
		Symbol: "X", Bid: units.MustMicros("100.04"), Ask: units.MustMicros("100.05"),
	}); err != nil {
		t.Fatal(err)
	}
	order, err = b.GetOrder(context.Background(), order.BrokerOrderID)
	if err != nil || order.State != "filled" || order.FilledPrice != units.MustMicros("100.05") {
		t.Fatalf("order=%+v err=%v, want filled at 100.05", order, err)
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 1 || positions[0].Qty != units.MustQty("1") {
		t.Fatalf("positions=%+v err=%v", positions, err)
	}
}

func TestPlaceLimitOrderDeduplicatesClientOrderID(t *testing.T) {
	b := NewFake(units.MustMicros("300"))
	request := PlaceRequest{
		ClientOrderID: "stable-client-id", Symbol: "SPY", Side: "buy",
		Qty: units.MustQty("1"), Limit: units.MustMicros("0.35"), Kind: "option",
	}
	first, err := b.PlaceLimitOrder(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.PlaceLimitOrder(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.BrokerOrderID != second.BrokerOrderID {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	conflict := request
	conflict.Qty = units.MustQty("2")
	if _, err := b.PlaceLimitOrder(context.Background(), conflict); err == nil {
		t.Fatal("conflicting order intent reused the client order id")
	}
	fills, err := b.RecentFills(context.Background(), time.Time{})
	if err != nil || len(fills) != 1 {
		t.Fatalf("fills=%v err=%v, want one", fills, err)
	}
}
