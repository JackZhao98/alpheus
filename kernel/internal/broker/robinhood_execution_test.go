package broker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

const (
	executionRefID   = "11111111-1111-4111-8111-111111111111"
	executionOrderID = "22222222-2222-4222-8222-222222222222"
	executionOption  = "33333333-3333-4333-8333-333333333333"
	executionFillID  = "44444444-4444-4444-8444-444444444444"
	executionEquity  = "55555555-5555-4555-8555-555555555555"
)

type mutationCall struct {
	tool string
	args map[string]any
}

type recordingMutation struct {
	mu        sync.Mutex
	responses map[string]json.RawMessage
	calls     []mutationCall
}

func (m *recordingMutation) MutationBoundary() {}

func (m *recordingMutation) Call(_ context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mutationCall{tool: tool, args: args})
	raw, ok := m.responses[tool]
	if !ok {
		return nil, fmt.Errorf("missing mutation fixture")
	}
	return raw, nil
}

type executionInstrument struct {
	instrument Instrument
	err        error
}

func (f executionInstrument) Instrument(context.Context, string) (Instrument, error) {
	return f.instrument, f.err
}

type executionLookup struct {
	result OrderResult
	err    error
}

func (f executionLookup) FindByClientOrderID(context.Context, string, string) (OrderResult, error) {
	return f.result, f.err
}

func optionOrderFixture(state, side, effect, price, processed string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"data":{"order":{"id":"%s","chain_symbol":"SPY","state":"%s","quantity":"1","processed_quantity":"%s","price":"%s","reject_reason":"","created_at":"2026-07-17T20:00:00Z","updated_at":"2026-07-17T20:00:01Z","legs":[{"id":"leg","option_id":"%s","side":"%s","position_effect":"%s","ratio_quantity":1,"executions":[%s]}]}},"guide":"fixture"}`,
		executionOrderID, state, processed, price, executionOption, side, effect, optionExecutions(processed, price)))
}

func optionOrdersFixture(state string) json.RawMessage {
	processed := "0"
	if state == "filled" || state == "cancelled" || state == "partially_filled_rest_cancelled" {
		processed = "1"
	}
	order := optionOrderFixture(state, "buy", "open", "0.35", processed)
	var envelope struct {
		Data struct {
			Order json.RawMessage `json:"order"`
		} `json:"data"`
	}
	if err := json.Unmarshal(order, &envelope); err != nil {
		panic(err)
	}
	return json.RawMessage(`{"data":{"orders":[` + string(envelope.Data.Order) + `],"next":""},"guide":"fixture"}`)
}

func optionExecutions(processed, price string) string {
	if processed == "0" {
		return ""
	}
	return fmt.Sprintf(`{"id":"%s","quantity":"%s","price":"%s","timestamp":"2026-07-17T20:00:01Z"}`,
		executionFillID, processed, price)
}

func emptyOrdersFixture() json.RawMessage {
	return json.RawMessage(`{"data":{"orders":[],"next":""},"guide":"fixture"}`)
}

func equityOrderFixture() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"data":{"order":{"id":"%s","instrument_id":"%s","symbol":"SPY","side":"buy","state":"filled","quantity":"1","cumulative_quantity":"1","price":"100.1","average_price":"100.1","reject_reason":"","created_at":"2026-07-17T20:00:00Z","last_transaction_at":"2026-07-17T20:00:01Z","executions":[{"id":"%s","quantity":"1","price":"100.1","fees":"0.01","timestamp":"2026-07-17T20:00:01Z"}]}},"guide":"fixture"}`,
		executionOrderID, executionEquity, executionFillID))
}

func newExecutionFixture(t *testing.T, mutation *recordingMutation, orderState string) (*RobinhoodExecution, *Robinhood) {
	t.Helper()
	read, err := NewRobinhood(fixtureCaller{
		"get_accounts":      accountFixture(`[` + validAccount("wanted") + `]`),
		"get_equity_orders": emptyOrdersFixture(),
		"get_option_orders": optionOrdersFixture(orderState),
	}, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	instruments := executionInstrument{instrument: Instrument{
		Symbol: "SPY", InstrumentID: executionOption, Kind: "option", Multiplier: 100,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
		Source: robinhoodSource, AsOf: time.Now().UTC(),
	}}
	adapter, err := NewRobinhoodExecution(read, mutation, instruments, executionLookup{
		result: OrderResult{BrokerOrderID: executionOrderID, ClientOrderID: executionRefID},
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, read
}

func TestRobinhoodExecutionCannotConstructWithoutClientIDLookup(t *testing.T) {
	read, err := NewRobinhood(fixtureCaller{}, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRobinhoodExecution(read, &recordingMutation{}, executionInstrument{}, nil); err == nil {
		t.Fatal("execution adapter constructed without provider client-id lookup")
	}
}

func TestRobinhoodOptionPlaceMatchesFakeReplay(t *testing.T) {
	mutation := &recordingMutation{responses: map[string]json.RawMessage{
		"place_option_order": optionOrderFixture("filled", "buy", "open", "0.35", "1"),
	}}
	adapter, _ := newExecutionFixture(t, mutation, "filled")
	req := PlaceRequest{
		ClientOrderID: executionRefID, Symbol: executionOption, Side: "buy", PositionEffect: "open",
		Qty: units.MustQty("1"), Limit: units.MustMicros("0.35"), Kind: "option",
	}
	live, err := adapter.PlaceLimitOrder(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	fake := NewFake(units.MustMicros("300"))
	fake.SetInstrument(Instrument{
		Symbol: executionOption, InstrumentID: executionOption, Kind: "option", Multiplier: 100,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	fake.SetQuote(Quote{Symbol: executionOption, Bid: units.MustMicros("0.34"), Ask: units.MustMicros("0.35"), OpenInterest: 1000})
	paper, err := fake.PlaceLimitOrder(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if live.State != paper.State || live.FilledQty != paper.FilledQty || live.FilledPrice != paper.FilledPrice ||
		live.ClientOrderID != req.ClientOrderID || len(live.Fills) != 1 {
		t.Fatalf("live=%+v fake=%+v", live, paper)
	}
	mutation.mu.Lock()
	defer mutation.mu.Unlock()
	if len(mutation.calls) != 1 || mutation.calls[0].tool != "place_option_order" {
		t.Fatalf("calls=%+v", mutation.calls)
	}
	args := mutation.calls[0].args
	if args["account_number"] != "wanted" || args["ref_id"] != executionRefID ||
		args["price"] != "0.35" || args["quantity"] != "1" {
		t.Fatalf("args=%v", args)
	}
	legs, ok := args["legs"].([]map[string]any)
	if !ok || len(legs) != 1 || legs[0]["position_effect"] != "open" || legs[0]["option_id"] != executionOption {
		t.Fatalf("legs=%v", args["legs"])
	}
}

func TestRobinhoodEquityPlaceUsesExactProductionShape(t *testing.T) {
	mutation := &recordingMutation{responses: map[string]json.RawMessage{
		"place_equity_order": equityOrderFixture(),
	}}
	adapter, _ := newExecutionFixture(t, mutation, "filled")
	adapter.instruments = executionInstrument{instrument: Instrument{
		Symbol: "SPY", InstrumentID: executionEquity, Kind: "equity", Multiplier: 1,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
		Source: robinhoodSource, AsOf: time.Now().UTC(),
	}}
	result, err := adapter.PlaceLimitOrder(context.Background(), PlaceRequest{
		ClientOrderID: executionRefID, Symbol: "SPY", Side: "buy", PositionEffect: "open",
		Qty: units.MustQty("1"), Limit: units.MustMicros("100.1"), Kind: "equity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "filled" || result.FilledQty != units.MustQty("1") ||
		result.FilledPrice != units.MustMicros("100.1") || len(result.Fills) != 1 ||
		result.Fills[0].Fees != units.MustMicros("0.01") {
		t.Fatalf("result=%+v", result)
	}
	mutation.mu.Lock()
	defer mutation.mu.Unlock()
	if len(mutation.calls) != 1 || mutation.calls[0].tool != "place_equity_order" ||
		mutation.calls[0].args["limit_price"] != "100.1" || mutation.calls[0].args["symbol"] != "SPY" {
		t.Fatalf("calls=%+v", mutation.calls)
	}
}

func TestRobinhoodExecutionFailsBeforeMutationOnMetadataOrEchoDrift(t *testing.T) {
	for name, test := range map[string]struct {
		limit    string
		response json.RawMessage
	}{
		"off tick":  {"0.355", optionOrderFixture("filled", "buy", "open", "0.35", "1")},
		"echo side": {"0.35", optionOrderFixture("filled", "sell", "open", "0.35", "1")},
	} {
		t.Run(name, func(t *testing.T) {
			mutation := &recordingMutation{responses: map[string]json.RawMessage{"place_option_order": test.response}}
			adapter, _ := newExecutionFixture(t, mutation, "filled")
			_, err := adapter.PlaceLimitOrder(context.Background(), PlaceRequest{
				ClientOrderID: executionRefID, Symbol: executionOption, Side: "buy", PositionEffect: "open",
				Qty: units.MustQty("1"), Limit: units.MustMicros(test.limit), Kind: "option",
			})
			if err == nil {
				t.Fatal("unsafe order was accepted")
			}
			mutation.mu.Lock()
			calls := len(mutation.calls)
			mutation.mu.Unlock()
			if name == "off tick" && calls != 0 {
				t.Fatalf("off-tick order reached mutation: calls=%d", calls)
			}
		})
	}
}

func TestRobinhoodExecutionGetCancelAndClientLookup(t *testing.T) {
	mutation := &recordingMutation{responses: map[string]json.RawMessage{
		"cancel_option_order": json.RawMessage(`{"data":{"accepted":true},"guide":"fixture"}`),
	}}
	adapter, _ := newExecutionFixture(t, mutation, "confirmed")
	order, err := adapter.GetOrder(context.Background(), executionOrderID)
	if err != nil || order.State != "submitted" || order.FilledQty != 0 || len(order.Fills) != 0 {
		t.Fatalf("order=%+v err=%v", order, err)
	}
	found, err := adapter.FindOrderByClientID(context.Background(), executionRefID)
	if err != nil || found.BrokerOrderID != executionOrderID || found.ClientOrderID != executionRefID {
		t.Fatalf("found=%+v err=%v", found, err)
	}
	cancelled, err := adapter.CancelOrder(context.Background(), executionOrderID)
	if err != nil || cancelled.State != "submitted" {
		t.Fatalf("cancelled=%+v err=%v", cancelled, err)
	}
	mutation.mu.Lock()
	defer mutation.mu.Unlock()
	if len(mutation.calls) != 1 || mutation.calls[0].tool != "cancel_option_order" ||
		mutation.calls[0].args["account_number"] != "wanted" || mutation.calls[0].args["order_id"] != executionOrderID {
		t.Fatalf("calls=%+v", mutation.calls)
	}
}

func TestRobinhoodExecutionRejectsClientLookupMismatch(t *testing.T) {
	mutation := &recordingMutation{}
	adapter, _ := newExecutionFixture(t, mutation, "filled")
	adapter.lookup = executionLookup{result: OrderResult{
		BrokerOrderID: executionOrderID, ClientOrderID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
	}}
	if _, err := adapter.FindOrderByClientID(context.Background(), executionRefID); err == nil {
		t.Fatal("mismatched provider client id was accepted")
	}
}
