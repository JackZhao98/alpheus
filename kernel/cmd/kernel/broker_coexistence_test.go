package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

func TestManualPositionCanBlockNextAlpheusOpenAtPreEffectBarrier(t *testing.T) {
	st := newMemoryStore()
	st.m3aActive = true
	st.liveCanary.DailyAuthorizedRiskCapUSD = units.MustMicros("1000")
	venue := newFake("300")
	setQuote(venue, "EXT", "99.99", "100", 0)
	if result, err := placeOrder(venue, "EXT", "buy", "2", "100", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed external position: result=%+v err=%v", result, err)
	}
	setQuote(venue, "NEW", "49.99", "50", 0)
	venue.SetInstrument(broker.Instrument{
		Symbol: "NEW", InstrumentID: "fake-instrument-NEW", Kind: "equity", Multiplier: 1,
	})
	s := &server{
		mode: protectedMode("live"), limits: dualLedgerLimits(), broker: venue, store: st,
	}
	payload := `{"action":"open","kind":"equity","underlying":"NEW","symbol":"NEW","side":"buy","qty":1,"max_risk_usd":50,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "external-risk-block")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "proposal_stale") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if order, _ := venue.GetOrder(context.Background(), "fake-2"); order.Reason != "unknown order" {
		t.Fatalf("manual position did not stop second broker effect: %+v", order)
	}
}

func TestExternalPositionsAndWorkingBuysConsumeAggregateOpenRisk(t *testing.T) {
	st := newMemoryStore()
	gate := &memoryGate{memoryStore: st}
	st.exposureQty[memoryExposureKey("live", "EXT", "equity")] = units.MustQty("1")
	now := time.Now().UTC()
	snapshot := &providerSnapshot{
		Positions: []broker.Position{{
			PositionID: "equity:EXT", InstrumentID: "instrument-ext", Symbol: "EXT",
			Qty: units.MustQty("2"), AvgPrice: units.MustMicros("10"), AvgPriceKnown: true,
			Kind: "equity", Multiplier: 1, Source: "fixture", AsOf: now,
		}},
		Orders: []broker.ReadOrder{{
			BrokerOrderID: "external-buy", InstrumentID: "instrument-ext", Symbol: "EXT",
			Side: "buy", Kind: "equity", PositionEffect: "open", State: "queued",
			Qty: units.MustQty("1"), LimitPrice: units.MustMicros("5"), LimitPriceKnown: true,
			Source: "fixture", AsOf: now,
		}},
		View: &store.BrokerAccountView{Objects: []store.BrokerObservedObject{{
			Family: store.BrokerFamilyOrders, ObjectKey: "external-buy", Origin: "external", Evidence: "unmatched",
		}}},
	}
	positionRisk, workingRisk, err := aggregateExternalOpenRisk(snapshot, gate)
	if err != nil {
		t.Fatal(err)
	}
	if positionRisk != units.MustMicros("10") || workingRisk != units.MustMicros("5") {
		t.Fatalf("position=%s working=%s", positionRisk, workingRisk)
	}
}

func TestExactAlpheusWorkingOrderIsNotDoubleCountedAsExternalRisk(t *testing.T) {
	st := newMemoryStore()
	gate := &memoryGate{memoryStore: st}
	now := time.Now().UTC()
	snapshot := &providerSnapshot{
		Orders: []broker.ReadOrder{{
			BrokerOrderID: "owned", InstrumentID: "instrument-owned", Symbol: "OWNED",
			Side: "buy", Kind: "equity", PositionEffect: "open", State: "queued",
			Qty: units.MustQty("1"), LimitPrice: units.MustMicros("10"), LimitPriceKnown: true,
			Source: "fixture", AsOf: now,
		}},
		View: &store.BrokerAccountView{Objects: []store.BrokerObservedObject{{
			Family: store.BrokerFamilyOrders, ObjectKey: "owned", Origin: "alpheus", Evidence: "exact_broker_order_id",
		}}},
	}
	_, workingRisk, err := aggregateExternalOpenRisk(snapshot, gate)
	if err != nil || workingRisk != 0 {
		t.Fatalf("working=%s err=%v", workingRisk, err)
	}
}

func TestAmbiguousOrUnboundedExternalRiskFailsClosed(t *testing.T) {
	st := newMemoryStore()
	gate := &memoryGate{memoryStore: st}
	now := time.Now().UTC()
	tests := []providerSnapshot{
		{
			Positions: []broker.Position{{
				PositionID: "short", Symbol: "SHORT", Qty: units.MustQty("-1"),
				AvgPrice: units.MustMicros("10"), AvgPriceKnown: true, Kind: "equity", Multiplier: 1,
				Source: "fixture", AsOf: now,
			}}, View: &store.BrokerAccountView{},
		},
		{
			Orders: []broker.ReadOrder{{
				BrokerOrderID: "sell-open", Symbol: "SELL", Side: "sell", Kind: "option",
				PositionEffect: "open", State: "queued", Qty: units.MustQty("1"),
				LimitPrice: units.MustMicros("0.10"), LimitPriceKnown: true, Source: "fixture", AsOf: now,
			}}, View: &store.BrokerAccountView{},
		},
		{
			Orders: []broker.ReadOrder{{
				BrokerOrderID: "unknown-price", Symbol: "BUY", Side: "buy", Kind: "equity",
				PositionEffect: "open", State: "queued", Qty: units.MustQty("1"),
				LimitPriceKnown: false, Source: "fixture", AsOf: now,
			}}, View: &store.BrokerAccountView{},
		},
	}
	for i := range tests {
		if _, _, err := aggregateExternalOpenRisk(&tests[i], gate); err == nil {
			t.Fatalf("case %d did not fail closed", i)
		}
	}
}

func TestExternalWorkingCloseConsumesClosableQuantityWithoutReversal(t *testing.T) {
	now := time.Now().UTC()
	snapshot := &providerSnapshot{
		Positions: []broker.Position{{
			PositionID: "equity:CLOSE", InstrumentID: "instrument-close", Symbol: "CLOSE",
			Qty: units.MustQty("2"), AvgPrice: units.MustMicros("10"), AvgPriceKnown: true,
			Kind: "equity", Multiplier: 1, Source: "fixture", AsOf: now,
		}},
		Orders: []broker.ReadOrder{{
			BrokerOrderID: "manual-sell", InstrumentID: "instrument-close", Symbol: "CLOSE",
			Side: "sell", Kind: "equity", PositionEffect: "unknown", State: "queued",
			Qty: units.MustQty("1"), LimitPrice: units.MustMicros("11"), LimitPriceKnown: true,
			Source: "fixture", AsOf: now,
		}},
		View: &store.BrokerAccountView{Objects: []store.BrokerObservedObject{{
			Family: store.BrokerFamilyOrders, ObjectKey: "manual-sell", Origin: "external", Evidence: "unmatched",
		}}},
	}
	op := risk.Operation{
		Action: "close", Symbol: "CLOSE", Side: "sell", Kind: "equity",
		InstrumentID: "instrument-close", Multiplier: 1, Qty: units.MustQty("1"),
		BrokerPositionID: "equity:CLOSE", DecisionPositionQty: units.MustQty("2"),
	}
	external, err := validateFreshCloseCapacity(snapshot, op, 0)
	if err != nil || external != units.MustQty("1") {
		t.Fatalf("external=%s err=%v", external, err)
	}
	op.Qty = units.MustQty("2")
	if _, err := validateFreshCloseCapacity(snapshot, op, 0); err == nil {
		t.Fatal("aggregate close reservations allowed a position reversal")
	}
}

func TestExternalOpeningOrderCancelCreatesNewAuditedLifecycle(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "EXTCANCEL", "9.90", "10", 1_000)
	target, err := venue.PlaceOrder(context.Background(), broker.PlaceRequest{
		ClientOrderID: "manual-ref", Symbol: "EXTCANCEL", Side: "buy",
		PositionEffect: "open", Qty: units.MustQty("1"), Limit: units.MustMicros("9"), Kind: "equity",
	})
	if err != nil || target.State != "submitted" {
		t.Fatalf("seed external opening order: target=%+v err=%v", target, err)
	}
	st := newMemoryStore()
	s := &server{limits: dualLedgerLimits(), broker: venue, store: st}
	response, body := postOperation(t, s, `{"action":"cancel","broker_order_id":"`+target.BrokerOrderID+`"}`)
	if response.Code != http.StatusOK || body["class"] != "A" || body["status"] != "executed" {
		t.Fatalf("status=%d body=%v", response.Code, body)
	}
	current, err := venue.GetOrder(context.Background(), target.BrokerOrderID)
	if err != nil || current.State != "cancelled" {
		t.Fatalf("external order not cancelled: order=%+v err=%v", current, err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.externalControls) != 1 {
		t.Fatalf("external control episodes=%d want=1", len(st.externalControls))
	}
	for operationID, episode := range st.externalControls {
		if operationID == "" || episode.ControlAction != "cancel_order" || episode.Origin != "external" ||
			episode.ObjectKey != target.BrokerOrderID || episode.BrokerObservationID == "" {
			t.Fatalf("episode=%+v", episode)
		}
	}
}

func TestProtectiveExternalOrderCancelIsRejectedWithoutProviderEffect(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "PROTECT", "9.90", "10", 1_000)
	if opened, err := placeOrder(venue, "PROTECT", "buy", "1", "10", "equity"); err != nil || opened.State != "filled" {
		t.Fatalf("seed position: order=%+v err=%v", opened, err)
	}
	target, err := venue.PlaceOrder(context.Background(), broker.PlaceRequest{
		ClientOrderID: "protective-ref", Symbol: "PROTECT", Side: "sell",
		PositionEffect: "close", Qty: units.MustQty("1"), Limit: units.MustMicros("11"), Kind: "equity",
	})
	if err != nil || target.State != "submitted" {
		t.Fatalf("seed protective order: target=%+v err=%v", target, err)
	}
	st := newMemoryStore()
	s := &server{limits: dualLedgerLimits(), broker: venue, store: st}
	response, body := postOperation(t, s, `{"action":"cancel","broker_order_id":"`+target.BrokerOrderID+`"}`)
	if response.Code != http.StatusOK || body["class"] != "REJECT" || body["status"] != "rejected" {
		t.Fatalf("status=%d body=%v", response.Code, body)
	}
	current, err := venue.GetOrder(context.Background(), target.BrokerOrderID)
	if err != nil || current.State != "submitted" {
		t.Fatalf("protective order changed: order=%+v err=%v", current, err)
	}
	if len(st.externalControls) != 0 {
		t.Fatalf("rejected cancel created external control episode: %+v", st.externalControls)
	}
}

func TestUnknownWorkingOrderEffectDetectsPossibleReversal(t *testing.T) {
	position := []broker.Position{{
		PositionID: "equity:X", InstrumentID: "instrument-x", Symbol: "X",
		Qty: units.MustQty("1"), Kind: "equity", Multiplier: 1,
	}}
	base := broker.ReadOrder{
		BrokerOrderID: "sell", InstrumentID: "instrument-x", Symbol: "X",
		Side: "sell", Kind: "equity", PositionEffect: "unknown", State: "queued",
	}
	base.Qty = units.MustQty("1")
	mayOpen, mayClose, err := workingOrderEffects(base, position)
	if err != nil || mayOpen || !mayClose {
		t.Fatalf("exact close open=%v close=%v err=%v", mayOpen, mayClose, err)
	}
	base.Qty = units.MustQty("2")
	mayOpen, mayClose, err = workingOrderEffects(base, position)
	if err != nil || !mayOpen || !mayClose {
		t.Fatalf("reversal open=%v close=%v err=%v", mayOpen, mayClose, err)
	}
	base.PositionEffect = "close"
	mayOpen, mayClose, err = workingOrderEffects(base, position)
	if err != nil || !mayOpen || !mayClose {
		t.Fatalf("oversized declared close open=%v close=%v err=%v", mayOpen, mayClose, err)
	}
}

func TestSameReferenceCandidateVisibleAtPreEffectStopsReplay(t *testing.T) {
	orders := []broker.ReadOrder{{BrokerOrderID: "candidate", ClientOrderID: "stable-ref"}}
	if !sameReferenceOrderVisible(orders, "stable-ref") {
		t.Fatal("exact Provider client reference was not detected")
	}
	if sameReferenceOrderVisible(orders, "different-ref") || sameReferenceOrderVisible(orders, "") {
		t.Fatal("unrelated Provider order was treated as the replay candidate")
	}
}

func TestFreshEquityCloseAcceptsProviderPositionWithoutInstrumentID(t *testing.T) {
	now := time.Now().UTC()
	snapshot := &providerSnapshot{
		Positions: []broker.Position{{
			PositionID: "equity:CLOSE", Symbol: "CLOSE", Qty: units.MustQty("1"),
			AvgPrice: units.MustMicros("10"), AvgPriceKnown: true,
			Kind: "equity", Multiplier: 1, Source: "fixture", AsOf: now,
		}},
		View: &store.BrokerAccountView{},
	}
	op := risk.Operation{
		Action: "close", Symbol: "CLOSE", Side: "sell", Kind: "equity",
		InstrumentID: "exact-market-instrument", Multiplier: 1, Qty: units.MustQty("1"),
		BrokerPositionID: "equity:CLOSE", DecisionPositionQty: units.MustQty("1"),
	}
	if _, err := validateFreshCloseCapacity(snapshot, op, 0); err != nil {
		t.Fatalf("equity position identity rejected: %v", err)
	}
	snapshot.Positions[0].InstrumentID = "conflicting-instrument"
	if _, err := validateFreshCloseCapacity(snapshot, op, 0); err == nil {
		t.Fatal("conflicting equity instrument identity was accepted")
	}
}

func TestClosePositionIDDisambiguatesCanonicalBrokerPosition(t *testing.T) {
	positions := []broker.Position{
		{PositionID: "position-a", Symbol: "SAME", Qty: units.MustQty("1"), Kind: "equity", Multiplier: 1},
		{PositionID: "position-b", Symbol: "SAME", Qty: units.MustQty("2"), Kind: "equity", Multiplier: 1},
	}
	op := risk.Operation{Action: "close", Symbol: "SAME", Qty: units.MustQty("1")}
	if _, err := normalizeClose(op, positions); err == nil {
		t.Fatal("ambiguous symbol-only close selected a broker position")
	}
	op.PositionID = "position-b"
	normalized, err := normalizeClose(op, positions)
	if err != nil || normalized.Side != "sell" || normalized.Kind != "equity" {
		t.Fatalf("canonical position close: op=%+v err=%v", normalized, err)
	}
}

func TestExternalOptionCoexistenceFailsClosedForNewRisk(t *testing.T) {
	snapshot := &providerSnapshot{
		Positions: []broker.Position{{
			PositionID: "option-id", InstrumentID: "option-id", Symbol: "OPT",
			Qty: units.MustQty("1"), AvgPrice: units.MustMicros("0.50"), AvgPriceKnown: true,
			Kind: "option", Multiplier: 100,
		}},
		View: &store.BrokerAccountView{},
	}
	if _, _, err := aggregateExternalOpenRisk(snapshot, &memoryGate{memoryStore: newMemoryStore()}); err == nil {
		t.Fatal("uncertified option-position coexistence authorized new risk")
	}
}

func TestPreEffectProposalTTLAppliesExceptToDurableRepriceRecovery(t *testing.T) {
	tests := []struct {
		name    string
		attempt store.ExecutionAttempt
		op      risk.Operation
		want    bool
	}{
		{name: "initial open", attempt: store.ExecutionAttempt{Intent: "place", Seq: 1}, op: risk.Operation{Action: "open"}, want: true},
		{name: "initial close", attempt: store.ExecutionAttempt{Intent: "place", Seq: 1}, op: risk.Operation{Action: "close"}, want: true},
		{name: "generic cancel", attempt: store.ExecutionAttempt{Intent: "cancel", Seq: 1}, op: risk.Operation{Action: "cancel"}, want: true},
		{name: "reprice cancel", attempt: store.ExecutionAttempt{Intent: "cancel", Seq: 2}, op: risk.Operation{Action: "open"}, want: false},
		{name: "replacement place", attempt: store.ExecutionAttempt{Intent: "place", Seq: 3}, op: risk.Operation{Action: "open"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := preEffectRequiresProposalTTL(&test.attempt, test.op); got != test.want {
				t.Fatalf("requires TTL=%v, want %v", got, test.want)
			}
		})
	}
}
