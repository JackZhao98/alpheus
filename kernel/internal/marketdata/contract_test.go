package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/units"
)

func runReadContract(t *testing.T, account broker.AccountProvider, market Provider) {
	t.Helper()
	ctx := context.Background()
	accountID, err := account.AccountID(ctx)
	if err != nil || accountID == "" {
		t.Fatalf("account id=%q err=%v", accountID, err)
	}
	state, err := account.Account(ctx)
	if err != nil || state.Source == "" || state.AsOf.IsZero() || state.ExternalID != accountID {
		t.Fatalf("account=%+v err=%v", state, err)
	}
	positions, err := account.Positions(ctx)
	if err != nil || positions == nil {
		t.Fatalf("positions=%+v err=%v", positions, err)
	}
	orders, err := account.OpenOrders(ctx)
	if err != nil || orders == nil {
		t.Fatalf("orders=%+v err=%v", orders, err)
	}
	fills, err := account.RecentFills(ctx, time.Now().Add(-24*time.Hour))
	if err != nil || fills == nil {
		t.Fatalf("fills=%+v err=%v", fills, err)
	}
	quote, err := market.Quote(ctx, "SPY")
	if err != nil || !quote.Sane() || quote.Source == "" || quote.AsOf.IsZero() {
		t.Fatalf("quote=%+v err=%v", quote, err)
	}
}

func runSemanticContract(t *testing.T, account broker.AccountProvider, market Provider, instrumentKey string) {
	t.Helper()
	runReadContract(t, account, market)
	ctx := context.Background()
	instrument, err := market.Instrument(ctx, instrumentKey)
	validMultiplier := (instrument.Kind == "equity" && instrument.Multiplier == 1) ||
		(instrument.Kind == "option" && instrument.Multiplier == 100)
	if err != nil || instrument.InstrumentID == "" || !validMultiplier ||
		instrument.PriceTick <= 0 || instrument.QtyIncrement <= 0 || instrument.Source == "" || instrument.AsOf.IsZero() {
		t.Fatalf("instrument=%+v err=%v", instrument, err)
	}
}

func TestFakeProviderSemanticContract(t *testing.T) {
	venue := broker.NewFake(units.MustMicros("300"))
	runSemanticContract(t, venue, NewFakeProvider(venue), "SPY")
}

func TestRobinhoodToolRegistrationIsReadOnlyAndDocumented(t *testing.T) {
	for _, tool := range RobinhoodReadTools {
		for _, mutation := range []string{"place_", "cancel_", "review_", "create_", "update_", "remove_", "add_"} {
			if strings.HasPrefix(tool, mutation) {
				t.Fatalf("mutation tool registered: %s", tool)
			}
		}
		if tool == "get_market_hours" || tool == "get_movers" || tool == "get_option_expirations" || tool == "get_option_chain" {
			t.Fatalf("undocumented/guessed tool registered: %s", tool)
		}
	}
}

type contractCaller map[string]json.RawMessage

func (c contractCaller) Call(_ context.Context, tool string, _ map[string]any) (json.RawMessage, error) {
	raw, ok := c[tool]
	if !ok {
		return nil, fmt.Errorf("unexpected tool %s", tool)
	}
	return raw, nil
}

type contractStatus struct{}

func (contractStatus) Status() rhmcp.Status {
	return rhmcp.Status{Connected: true, LastSuccessfulRead: time.Now().UTC()}
}
func (contractStatus) MarkSchemaDrift() {}
func (contractStatus) MarkDataError()   {}

func TestRobinhoodFixtureSemanticContract(t *testing.T) {
	optionID := "11111111-1111-1111-1111-111111111111"
	raw, err := os.ReadFile("testdata/robinhood_read_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	caller := contractCaller{}
	if err := json.Unmarshal(raw, &caller); err != nil {
		t.Fatal(err)
	}
	account, err := broker.NewRobinhood(caller, "fixture-agentic")
	if err != nil {
		t.Fatal(err)
	}
	market, err := NewRobinhoodProvider(caller, contractStatus{}, "fixture-v1")
	if err != nil {
		t.Fatal(err)
	}
	runSemanticContract(t, account, market, optionID)
	fills, err := account.RecentFills(context.Background(), time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC))
	if err != nil || len(fills) != 2 {
		t.Fatalf("fixture fills=%d err=%v", len(fills), err)
	}
}

func TestRobinhoodInstrumentFailsClosedUntilDiscovery(t *testing.T) {
	status := &trackingStatus{}
	provider, err := NewRobinhoodProvider(contractCaller{}, status, "fixture-v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Instrument(context.Background(), "SPY"); err == nil {
		t.Fatal("unverified instrument metadata was synthesized")
	}
	if !status.data {
		t.Fatal("unverified instrument metadata was not reported")
	}
}

type trackingStatus struct {
	drift bool
	data  bool
}

func (*trackingStatus) Status() rhmcp.Status { return rhmcp.Status{Connected: true} }
func (s *trackingStatus) MarkSchemaDrift()   { s.drift = true }
func (s *trackingStatus) MarkDataError()     { s.data = true }

func TestRobinhoodQuoteFailuresAreVisibleInProviderStatus(t *testing.T) {
	asOf := time.Now().UTC().Format(time.RFC3339Nano)
	for name, quote := range map[string]string{
		"renamed_required_field": `{"symbol":"SPY","best_bid":"1","ask_price":"2","venue_bid_time":"` + asOf + `","venue_ask_time":"` + asOf + `","has_traded":true,"state":"active"}`,
		"crossed_market":         `{"symbol":"SPY","bid_price":"2","ask_price":"1","venue_bid_time":"` + asOf + `","venue_ask_time":"` + asOf + `","has_traded":true,"state":"active"}`,
	} {
		t.Run(name, func(t *testing.T) {
			status := &trackingStatus{}
			caller := contractCaller{
				"get_equity_quotes": json.RawMessage(`{"data":{"results":[{"quote":` + quote + `}]},"guide":"fixture"}`),
			}
			provider, err := NewRobinhoodProvider(caller, status, "fixture-v1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Quote(context.Background(), "SPY"); err == nil {
				t.Fatal("invalid quote was accepted")
			}
			if name == "renamed_required_field" && !status.drift {
				t.Fatal("schema drift did not reach provider status")
			}
			if name == "crossed_market" && !status.data {
				t.Fatal("invalid market data did not reach provider status")
			}
		})
	}
}
