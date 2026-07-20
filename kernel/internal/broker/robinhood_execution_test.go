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

type recordingReadCaller struct {
	responses map[string]json.RawMessage
	mu        sync.Mutex
	calls     []mutationCall
}

func (c *recordingReadCaller) Call(_ context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, mutationCall{tool: tool, args: args})
	raw, ok := c.responses[tool]
	if !ok {
		return nil, fmt.Errorf("missing read fixture")
	}
	return raw, nil
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
	return json.RawMessage(fmt.Sprintf(`{"data":{"order":{"id":"%s"}},"guide":"fixture"}`, executionOrderID))
}

func equityOrdersFixture(quantity, price, state string) json.RawMessage {
	filled := "0"
	executions := ""
	average := "null"
	if state == "filled" {
		filled = quantity
		average = `"` + price + `"`
		executions = fmt.Sprintf(`{"id":"%s","quantity":%q,"price":%q,"fees":"0.01","timestamp":"2026-07-17T20:00:01Z"}`,
			executionFillID, quantity, price)
	}
	return json.RawMessage(fmt.Sprintf(`{"data":{"orders":[{"id":"%s","instrument_id":"%s","symbol":"SPY","side":"buy","state":%q,"quantity":%q,"dollar_based_amount":null,"cumulative_quantity":%q,"price":%q,"stop_price":null,"average_price":%s,"reject_reason":"","created_at":"2026-07-17T20:00:00Z","last_transaction_at":"2026-07-17T20:00:01Z","type":"limit","time_in_force":"gfd","market_hours":"regular_hours","trigger":"immediate","placed_agent":"agentic","executions":[%s]}],"next":""},"guide":"fixture"}`,
		executionOrderID, executionEquity, state, quantity, filled, price, average, executions))
}

func equityMarketOrdersFixture() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"data":{"orders":[{"id":"%s","instrument_id":"%s","symbol":"SPY","side":"buy","state":"filled","quantity":"1","dollar_based_amount":null,"cumulative_quantity":"1","price":"100.1","stop_price":null,"average_price":"100.1","reject_reason":"","created_at":"2026-07-17T20:00:00Z","last_transaction_at":"2026-07-17T20:00:01Z","type":"market","time_in_force":"gfd","market_hours":"regular_hours","trigger":"immediate","placed_agent":"agentic","executions":[{"id":"%s","quantity":"1","price":"100.1","fees":"0.01","timestamp":"2026-07-17T20:00:01Z"}]}],"next":""},"guide":"fixture"}`,
		executionOrderID, executionEquity, executionFillID))
}

func equityWorkingSellOrdersFixture() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"data":{"orders":[{"id":"%s","instrument_id":"%s","symbol":"SOFI","side":"sell","state":"confirmed","quantity":"1","dollar_based_amount":null,"cumulative_quantity":"0","price":"18.00","stop_price":null,"average_price":null,"reject_reason":"","created_at":"2026-07-20T19:48:09.301Z","last_transaction_at":"2026-07-20T19:48:09.301Z","type":"limit","time_in_force":"gfd","market_hours":"regular_hours","trigger":"immediate","placed_agent":"agentic","executions":[]}],"next":""},"guide":"fixture"}`,
		executionOrderID, executionEquity))
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
	adapter, err := newRobinhoodExecution(read, mutation, instruments, true)
	if err != nil {
		t.Fatal(err)
	}
	return adapter, read
}

func TestRobinhoodExecutionRequiresEveryMutationCapability(t *testing.T) {
	read, err := NewRobinhood(fixtureCaller{}, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRobinhoodExecution(read, &recordingMutation{}, nil); err == nil {
		t.Fatal("execution adapter constructed without instrument metadata")
	}
}

func TestRobinhoodProductionExecutionAllowsOnlyCertifiedEquityMutations(t *testing.T) {
	mutation := &recordingMutation{}
	offline, _ := newExecutionFixture(t, mutation, "filled")
	production, err := NewRobinhoodExecution(offline.read, mutation, offline.instruments)
	if err != nil {
		t.Fatal(err)
	}
	if !production.SupportsOrderKind("equity") || production.SupportsOrderKind("option") {
		t.Fatal("production mutation capability is not equity-only")
	}
	_, err = production.PlaceOrder(context.Background(), PlaceRequest{
		ClientOrderID: executionRefID, Symbol: executionOption, Side: "buy", PositionEffect: "open",
		Qty: units.MustQty("1"), Limit: units.MustMicros("0.35"), Kind: "option",
	})
	if err == nil {
		t.Fatal("uncertified option mutation was accepted")
	}
	mutation.mu.Lock()
	defer mutation.mu.Unlock()
	if len(mutation.calls) != 0 {
		t.Fatalf("uncertified option reached mutation transport: calls=%d", len(mutation.calls))
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
	live, err := adapter.PlaceOrder(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	fake := NewFake(units.MustMicros("300"))
	fake.SetInstrument(Instrument{
		Symbol: executionOption, InstrumentID: executionOption, Kind: "option", Multiplier: 100,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	fake.SetQuote(Quote{Symbol: executionOption, Bid: units.MustMicros("0.34"), Ask: units.MustMicros("0.35"), OpenInterest: 1000})
	paper, err := fake.PlaceOrder(context.Background(), req)
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
	adapter, read := newExecutionFixture(t, mutation, "filled")
	read.caller = fixtureCaller{
		"get_accounts":      accountFixture(`[` + validAccount("wanted") + `]`),
		"get_equity_orders": equityOrdersFixture("1", "100.1", "filled"),
	}
	adapter.instruments = executionInstrument{instrument: Instrument{
		Symbol: "SPY", InstrumentID: executionEquity, Kind: "equity", Multiplier: 1,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
		Source: robinhoodSource, AsOf: time.Now().UTC(),
	}}
	result, err := adapter.PlaceOrder(context.Background(), PlaceRequest{
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

func TestRobinhoodEquityMarketPlaceOmitsProviderPrice(t *testing.T) {
	mutation := &recordingMutation{responses: map[string]json.RawMessage{
		"place_equity_order": equityOrderFixture(),
	}}
	adapter, read := newExecutionFixture(t, mutation, "filled")
	read.caller = fixtureCaller{
		"get_accounts":      accountFixture(`[` + validAccount("wanted") + `]`),
		"get_equity_orders": equityMarketOrdersFixture(),
	}
	adapter.instruments = executionInstrument{instrument: Instrument{
		Symbol: "SPY", InstrumentID: executionEquity, Kind: "equity", Multiplier: 1,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
		Source: robinhoodSource, AsOf: time.Now().UTC(),
	}}
	result, err := adapter.PlaceOrder(context.Background(), PlaceRequest{
		ClientOrderID: executionRefID, Symbol: "SPY", Side: "buy", PositionEffect: "open",
		OrderType: "market", Qty: units.MustQty("1"), Limit: units.MustMicros("150"), Kind: "equity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "filled" || result.FilledQty != units.MustQty("1") ||
		result.FilledPrice != units.MustMicros("100.1") || len(result.Fills) != 1 {
		t.Fatalf("result=%+v", result)
	}
	mutation.mu.Lock()
	defer mutation.mu.Unlock()
	if len(mutation.calls) != 1 || mutation.calls[0].tool != "place_equity_order" ||
		mutation.calls[0].args["type"] != "market" || mutation.calls[0].args["quantity"] != "1" {
		t.Fatalf("calls=%+v", mutation.calls)
	}
	if _, exists := mutation.calls[0].args["limit_price"]; exists {
		t.Fatalf("market order leaked limit_price: calls=%+v", mutation.calls)
	}
}

func TestRobinhoodEquityWorkingSellUsesRecordedCloseShapeOnce(t *testing.T) {
	mutation := &recordingMutation{responses: map[string]json.RawMessage{
		"place_equity_order": equityOrderFixture(),
	}}
	adapter, read := newExecutionFixture(t, mutation, "confirmed")
	read.caller = fixtureCaller{
		"get_accounts":      accountFixture(`[` + validAccount("wanted") + `]`),
		"get_equity_orders": equityWorkingSellOrdersFixture(),
	}
	adapter.instruments = executionInstrument{instrument: Instrument{
		Symbol: "SOFI", InstrumentID: executionEquity, Kind: "equity", Multiplier: 1,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
		Source: robinhoodSource, AsOf: time.Now().UTC(),
	}}
	result, err := adapter.PlaceOrder(context.Background(), PlaceRequest{
		ClientOrderID: executionRefID, Symbol: "SOFI", Side: "sell", PositionEffect: "close",
		OrderType: "limit", Qty: units.MustQty("1"), Limit: units.MustMicros("18"), Kind: "equity",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BrokerOrderID != executionOrderID || result.State != "submitted" ||
		result.FilledQty != 0 || len(result.Fills) != 0 {
		t.Fatalf("working sell result=%+v", result)
	}
	mutation.mu.Lock()
	defer mutation.mu.Unlock()
	if len(mutation.calls) != 1 || mutation.calls[0].tool != "place_equity_order" ||
		mutation.calls[0].args["symbol"] != "SOFI" || mutation.calls[0].args["side"] != "sell" ||
		mutation.calls[0].args["limit_price"] != "18" || mutation.calls[0].args["quantity"] != "1" {
		t.Fatalf("working sell calls=%+v", mutation.calls)
	}
}

func TestRobinhoodEquityLimitPrecisionFailsBeforeMutation(t *testing.T) {
	for name, request := range map[string]PlaceRequest{
		"sub-micro tick": {
			ClientOrderID: executionRefID, Symbol: "SPY", Side: "buy", PositionEffect: "open",
			Qty: units.MustQty("1"), Limit: units.MustMicros("0.50001"), Kind: "equity",
		},
		"fractional limit quantity": {
			ClientOrderID: executionRefID, Symbol: "SPY", Side: "buy", PositionEffect: "open",
			Qty: units.MustQty("0.5"), Limit: units.MustMicros("13.50"), Kind: "equity",
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutation := &recordingMutation{}
			adapter, _ := newExecutionFixture(t, mutation, "filled")
			adapter.instruments = executionInstrument{instrument: Instrument{
				Symbol: "SPY", InstrumentID: executionEquity, Kind: "equity", Multiplier: 1,
				PriceTick: units.MustMicros("0.01"), BelowPriceTick: units.MustMicros("0.0001"),
				TickCutoff: units.MustMicros("1"), QtyIncrement: units.MustQty("1"),
			}}
			if _, err := adapter.PlaceOrder(context.Background(), request); err == nil {
				t.Fatal("unsupported equity precision reached the provider")
			}
			mutation.mu.Lock()
			calls := len(mutation.calls)
			mutation.mu.Unlock()
			if calls != 0 {
				t.Fatalf("mutation calls=%d", calls)
			}
		})
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
			_, err := adapter.PlaceOrder(context.Background(), PlaceRequest{
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

func TestRobinhoodExecutionGetAndCancel(t *testing.T) {
	mutation := &recordingMutation{responses: map[string]json.RawMessage{
		"cancel_option_order": json.RawMessage(`{"data":{"accepted":true},"guide":"fixture"}`),
	}}
	adapter, _ := newExecutionFixture(t, mutation, "confirmed")
	order, err := adapter.GetOrder(context.Background(), executionOrderID)
	if err != nil || order.State != "submitted" || order.FilledQty != 0 || len(order.Fills) != 0 {
		t.Fatalf("order=%+v err=%v", order, err)
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

func TestRobinhoodExactEquityCandidatesRequireEveryVisibleField(t *testing.T) {
	created := "2026-07-17T20:00:00Z"
	order := func(id, instrument, side, price, placed string) string {
		return fmt.Sprintf(`{"id":%q,"instrument_id":%q,"symbol":"SPY","side":%q,"type":"limit","state":"queued","quantity":"1","cumulative_quantity":"0","price":%q,"stop_price":null,"average_price":null,"reject_reason":"","time_in_force":"gfd","market_hours":"regular_hours","trigger":"immediate","placed_agent":%q,"created_at":%q,"last_transaction_at":null,"executions":[]}`,
			id, instrument, side, price, placed, created)
	}
	lookalikeID := "66666666-6666-4666-8666-666666666666"
	caller := &recordingReadCaller{responses: map[string]json.RawMessage{
		"get_accounts": accountFixture(`[` + validAccount("wanted") + `]`),
		"get_equity_orders": json.RawMessage(`{"data":{"orders":[` +
			order(executionOrderID, executionEquity, "buy", "100.1", "agentic") + `,` +
			order(lookalikeID, executionEquity, "buy", "100.11", "agentic") +
			`],"next":""},"guide":"fixture"}`),
	}}
	read, err := NewRobinhood(caller, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewRobinhoodExecution(read, &recordingMutation{}, executionInstrument{})
	if err != nil {
		t.Fatal(err)
	}
	start, _ := time.Parse(time.RFC3339, "2026-07-17T19:59:30Z")
	end, _ := time.Parse(time.RFC3339, "2026-07-17T20:02:00Z")
	candidates, err := adapter.FindExactPlaceCandidates(context.Background(), ExactPlaceCandidateQuery{
		AccountID: "wanted", ClientOrderID: executionRefID, WindowStart: start, WindowEnd: end,
		Intent: ProviderPlaceIntent{
			Kind: "equity", InstrumentID: executionEquity, Symbol: "SPY", Side: "buy",
			PositionEffect: "open", Qty: units.MustQty("1"), Limit: units.MustMicros("100.1"),
			OrderType: "limit", Trigger: "immediate", TimeInForce: "gfd", MarketHours: "regular_hours",
		},
	})
	if err != nil || len(candidates) != 1 || candidates[0].BrokerOrderID != executionOrderID ||
		candidates[0].ClientOrderID != executionRefID {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	caller.mu.Lock()
	defer caller.mu.Unlock()
	var orderCall *mutationCall
	for i := range caller.calls {
		if caller.calls[i].tool == "get_equity_orders" {
			orderCall = &caller.calls[i]
		}
	}
	if orderCall == nil || orderCall.args["account_number"] != "wanted" ||
		orderCall.args["placed_agent"] != "agentic" || orderCall.args["symbol"] != "SPY" ||
		orderCall.args["created_at_gte"] != start.Format(time.RFC3339Nano) {
		t.Fatalf("order query=%+v", orderCall)
	}
}

func TestRobinhoodExactMarketCandidateMatchesWithoutPrice(t *testing.T) {
	caller := &recordingReadCaller{responses: map[string]json.RawMessage{
		"get_accounts":      accountFixture(`[` + validAccount("wanted") + `]`),
		"get_equity_orders": equityMarketOrdersFixture(),
	}}
	read, err := NewRobinhood(caller, "wanted")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewRobinhoodExecution(read, &recordingMutation{}, executionInstrument{})
	if err != nil {
		t.Fatal(err)
	}
	start, _ := time.Parse(time.RFC3339, "2026-07-17T19:59:30Z")
	end, _ := time.Parse(time.RFC3339, "2026-07-17T20:02:00Z")
	candidates, err := adapter.FindExactPlaceCandidates(context.Background(), ExactPlaceCandidateQuery{
		AccountID: "wanted", ClientOrderID: executionRefID, WindowStart: start, WindowEnd: end,
		Intent: ProviderPlaceIntent{
			Kind: "equity", InstrumentID: executionEquity, Symbol: "SPY", Side: "buy",
			PositionEffect: "open", Qty: units.MustQty("1"), OrderType: "market",
			Trigger: "immediate", TimeInForce: "gfd", MarketHours: "regular_hours",
		},
	})
	if err != nil || len(candidates) != 1 || candidates[0].BrokerOrderID != executionOrderID {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
}
