package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type repriceExecution struct {
	mu            sync.Mutex
	orders        map[string]broker.OrderResult
	requests      []broker.PlaceRequest
	cancelCalls   int
	cancelErr     error
	cancelResults map[string]broker.OrderResult
	nextID        int
}

func newRepriceExecution(initialID string) *repriceExecution {
	return &repriceExecution{
		orders: map[string]broker.OrderResult{
			initialID: {BrokerOrderID: initialID, ClientOrderID: "client-1", State: "submitted"},
		},
		cancelResults: map[string]broker.OrderResult{},
	}
}

func (e *repriceExecution) PlaceOrder(_ context.Context, request broker.PlaceRequest) (broker.OrderResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, result := range e.orders {
		if result.ClientOrderID == request.ClientOrderID {
			return result, nil
		}
	}
	e.nextID++
	id := fmt.Sprintf("replacement-%d", e.nextID)
	result := broker.OrderResult{
		BrokerOrderID: id, ClientOrderID: request.ClientOrderID, State: "submitted",
	}
	e.orders[id] = result
	e.requests = append(e.requests, request)
	return result, nil
}

func (e *repriceExecution) CancelOrder(_ context.Context, brokerOrderID string) (broker.OrderResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cancelCalls++
	if e.cancelErr != nil {
		return broker.OrderResult{}, e.cancelErr
	}
	if result, ok := e.cancelResults[brokerOrderID]; ok {
		e.orders[brokerOrderID] = result
		delete(e.cancelResults, brokerOrderID)
		return result, nil
	}
	result, ok := e.orders[brokerOrderID]
	if !ok {
		return broker.OrderResult{}, broker.ErrNotFound
	}
	result.State = "cancelled"
	e.orders[brokerOrderID] = result
	return result, nil
}

func (e *repriceExecution) GetOrder(_ context.Context, brokerOrderID string) (broker.OrderResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	result, ok := e.orders[brokerOrderID]
	if !ok {
		return broker.OrderResult{}, broker.ErrNotFound
	}
	return result, nil
}

func (e *repriceExecution) FindOrderByClientID(_ context.Context, clientOrderID string) (broker.OrderResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, result := range e.orders {
		if result.ClientOrderID == clientOrderID {
			return result, nil
		}
	}
	return broker.OrderResult{}, broker.ErrNotFound
}

func newRepriceTestServer(t *testing.T, quantity string, maxReprices int) (*server, *memoryStore, *repriceExecution, store.Order, risk.Operation) {
	t.Helper()
	accountVenue := newFake("100000")
	setQuote(accountVenue, "XYZ", "100", "110", 10_000)
	accountVenue.SetInstrument(broker.Instrument{
		Symbol: "XYZ", Kind: "equity", Multiplier: 1,
		PriceTick: units.MustMicros("1"), QtyIncrement: units.MustQty("1"),
	})
	execution := newRepriceExecution("broker-1")
	st := newMemoryStore()
	st.m3aActive = true
	limits := dualLedgerLimits()
	limits.ExecutionPolicy.RepriceIntervalSec = 20
	limits.ExecutionPolicy.MaxReprices = maxReprices
	limits.QuoteMaxAgeSec = 60
	qty := units.MustQty(quantity)
	op := risk.Operation{
		Proposer: "m5b", Action: "open", Kind: "equity", Underlying: "XYZ", Symbol: "XYZ",
		Side: "buy", Qty: qty, ApprovedPriceCap: units.MustMicros("110"),
		WorkingPrice: units.MustMicros("105"), Multiplier: 1,
		DerivedMaxRisk: units.MustMicros("440"), RequiredCash: units.MustMicros("440"),
	}
	operationID := store.NewID()
	payload, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	st.operations[operationID] = op
	st.statuses[operationID] = "auto_approved"
	st.classes[operationID] = "B"
	st.operationRows[operationID] = store.OperationRow{
		ID: operationID, Class: "B", Status: "auto_approved", Payload: payload, TS: now,
	}
	reservationID := store.NewID()
	st.openReservations[reservationID] = store.OpenReservation{
		ID: reservationID, OperationID: operationID, Ledger: "live",
		Symbol: "XYZ", Kind: "equity", OriginalQty: qty, RemainingQty: qty,
		OriginalRisk: units.MustMicros("440"), RemainingRisk: units.MustMicros("440"),
		OriginalCash: units.MustMicros("440"), RemainingCash: units.MustMicros("440"),
		ResourceState: "held", CreatedAt: now,
	}
	st.grants[operationID] = store.TradeGrant{OperationID: operationID, Ledger: "live"}
	attemptID := store.NewID()
	st.attempts[attemptID] = store.ExecutionAttempt{
		ID: attemptID, OperationID: operationID, Seq: 1, OpenReservationID: reservationID,
		Intent: "place", ClientOrderID: "client-1", State: "placed", Qty: qty,
		Limit: units.MustMicros("105"), BrokerOrderID: "broker-1", CreatedAt: now,
	}
	order := store.Order{
		ID: store.NewID(), OperationID: operationID, ExecutionAttemptID: attemptID,
		BrokerOrderID: "broker-1", ClientOrderID: "client-1", Ledger: "live",
		Symbol: "XYZ", Side: "buy", Kind: "equity", Multiplier: 1,
		Qty: qty, Limit: units.MustMicros("105"), State: "submitted",
		CreatedAt: now, UpdatedAt: now,
	}
	st.orders[attemptID] = order
	s := &server{
		limits: limits, account: accountVenue, execution: execution,
		market: marketdata.NewFakeProvider(accountVenue), store: st,
		instanceID: "m5b-test", brokerTimeout: time.Second,
		claimTimeout: time.Second, attemptStale: time.Millisecond,
	}
	return s, st, execution, order, op
}

func TestK1B2EffectiveOrderPolicyNeverWidensOldEnvelope(t *testing.T) {
	st := newMemoryStore()
	base := st.kernelPolicy.Policy
	s := &server{limits: base, store: st}
	order := &store.Order{
		KernelPolicyRevisionID: 1, KernelPolicyGeneration: 1,
		KernelPolicyDigest: st.kernelPolicy.Digest,
		MaxReprices:        2, RepriceIntervalSec: 30, QuoteMaxAgeSec: 15,
	}

	wide := *st.kernelPolicy
	wide.Generation = 2
	wide.Policy.ExecutionPolicy.MaxReprices = 10
	wide.Policy.ExecutionPolicy.RepriceIntervalSec = 1
	wide.Policy.QuoteMaxAgeSec = 60
	st.kernelPolicy = &wide
	effective, err := s.effectiveOrderPolicy(order)
	if err != nil {
		t.Fatal(err)
	}
	if effective.maxReprices != 2 || effective.repriceIntervalSec != 30 || effective.quoteMaxAgeSec != 15 {
		t.Fatalf("later widening expanded old envelope: %+v", effective)
	}

	tight := wide
	tight.Generation = 3
	tight.Policy.ExecutionPolicy.MaxReprices = 1
	tight.Policy.ExecutionPolicy.RepriceIntervalSec = 60
	tight.Policy.QuoteMaxAgeSec = 5
	st.kernelPolicy = &tight
	effective, err = s.effectiveOrderPolicy(order)
	if err != nil {
		t.Fatal(err)
	}
	if effective.maxReprices != 1 || effective.repriceIntervalSec != 60 || effective.quoteMaxAgeSec != 5 {
		t.Fatalf("current tightening was not applied: %+v", effective)
	}
}

func TestM5BRepriceWalksWithinCapThenExpiresAtMax(t *testing.T) {
	s, st, execution, _, _ := newRepriceTestServer(t, "1", 1)
	if err := s.repriceOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 1 {
		t.Fatalf("replacement requests=%d, want 1", len(execution.requests))
	}
	request := execution.requests[0]
	if request.Limit != units.MustMicros("108") || request.Limit > units.MustMicros("110") {
		t.Fatalf("first replacement limit=%s, want 108 within cap", request.Limit)
	}
	if request.Qty != units.MustQty("1") {
		t.Fatalf("first replacement qty=%s, want 1", request.Qty)
	}
	if request.PositionEffect != "open" {
		t.Fatalf("first replacement position_effect=%q, want open", request.PositionEffect)
	}
	if len(st.grants) != 1 {
		t.Fatalf("trade grants=%d, replacement allocated a new slot", len(st.grants))
	}
	if err := s.repriceOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 1 || execution.cancelCalls != 2 {
		t.Fatalf("requests=%d cancels=%d, want one replacement then policy cancel", len(execution.requests), execution.cancelCalls)
	}
	for _, reservation := range st.openReservations {
		if reservation.ResourceState != "released" || reservation.RemainingCash != 0 || reservation.RemainingRisk != 0 {
			t.Fatalf("reservation after max=%+v, want released", reservation)
		}
	}
	if st.statuses[requestOperationID(st)] != "failed" {
		t.Fatalf("zero-fill operation status=%q, want failed", st.statuses[requestOperationID(st)])
	}
	if !containsEvent(st.events, "order_expired_policy") {
		t.Fatalf("events=%v, want order_expired_policy", st.events)
	}
}

func TestM5BPartialFillTransfersOnlyConfirmedRemainder(t *testing.T) {
	s, st, execution, source, _ := newRepriceTestServer(t, "4", 3)
	now := time.Now().UTC()
	execution.cancelResults[source.BrokerOrderID] = broker.OrderResult{
		BrokerOrderID: source.BrokerOrderID, ClientOrderID: source.ClientOrderID,
		State: "cancelled", FilledQty: units.MustQty("2"),
		Fills: []broker.ReadFill{{
			FillID: "partial-1", BrokerOrderID: source.BrokerOrderID,
			Symbol: source.Symbol, Side: source.Side, Qty: units.MustQty("2"),
			Price: units.MustMicros("106"), AsOf: now,
		}},
	}
	if err := s.repriceOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 1 || execution.requests[0].Qty != units.MustQty("2") {
		t.Fatalf("replacement requests=%+v, want qty 2", execution.requests)
	}
	for _, reservation := range st.openReservations {
		if reservation.ResourceState != "held" || reservation.RemainingQty != units.MustQty("2") ||
			reservation.RemainingCash != units.MustMicros("220") || reservation.RemainingRisk != units.MustMicros("220") {
			t.Fatalf("partial-fill reservation=%+v", reservation)
		}
	}
	if len(st.fills) != 1 || st.exposureQty[memoryExposureKey("live", "XYZ", "equity")] != units.MustQty("2") {
		t.Fatalf("fills=%d exposure=%s, want one durable fill and qty 2", len(st.fills), st.exposureQty[memoryExposureKey("live", "XYZ", "equity")])
	}
}

func TestM5BAmbiguousCancelHoldsReservationAndCreatesNoReplacement(t *testing.T) {
	s, st, execution, source, _ := newRepriceTestServer(t, "1", 3)
	execution.cancelErr = errors.New("transport timeout")
	if err := s.repriceOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 0 {
		t.Fatalf("ambiguous cancel created %d replacements", len(execution.requests))
	}
	working, err := st.ListWorkingOrders(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(working) != 0 {
		t.Fatalf("working orders=%v, unresolved cancel must fence another cancel", working)
	}
	unknown := 0
	for _, attempt := range st.attempts {
		if attempt.Intent == "cancel" && attempt.TargetBrokerOrderID == source.BrokerOrderID && attempt.State == "unknown" {
			unknown++
		}
	}
	if unknown != 1 {
		t.Fatalf("unknown reprice cancels=%d, want 1", unknown)
	}
	for _, reservation := range st.openReservations {
		if reservation.ResourceState != "held" || reservation.RemainingQty != units.MustQty("1") {
			t.Fatalf("ambiguous cancel changed reservation: %+v", reservation)
		}
	}
}

func TestM5BOffTickQuoteFailsBeforeCancel(t *testing.T) {
	s, st, execution, _, _ := newRepriceTestServer(t, "1", 3)
	venue, ok := s.account.(*broker.Fake)
	if !ok {
		t.Fatal("test account is not the fake venue")
	}
	setQuote(venue, "XYZ", "100", "110.5", 10_000)
	if err := s.repriceOnce(context.Background()); err == nil {
		t.Fatal("off-tick quote unexpectedly passed repricing")
	}
	if execution.cancelCalls != 0 || len(execution.requests) != 0 {
		t.Fatalf("off-tick quote reached broker: cancels=%d replacements=%d", execution.cancelCalls, len(execution.requests))
	}
	for _, reservation := range st.openReservations {
		if reservation.ResourceState != "held" {
			t.Fatalf("off-tick quote changed reservation: %+v", reservation)
		}
	}
}

func TestM5BRecoveryFinishesConfirmedCancelWithoutDuplicateSlot(t *testing.T) {
	s, st, execution, source, _ := newRepriceTestServer(t, "1", 3)
	cancelAttempt, err := st.StageRepriceCancel(source.ID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel: attempt=%+v err=%v", cancelAttempt, err)
	}
	claimed, err := st.ClaimPendingAttempt(cancelAttempt.ID, "crashed-worker", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim cancel: attempt=%+v err=%v", claimed, err)
	}
	if _, err := execution.CancelOrder(context.Background(), source.BrokerOrderID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
		State: "unknown", LastError: "process died after broker effect",
	}); err != nil {
		t.Fatal(err)
	}
	seen, err := st.GetExecutionAttempt(claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileUncertainAttempt(context.Background(), seen, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 1 || len(st.grants) != 1 {
		t.Fatalf("recovery requests=%d grants=%d, want one replacement and original grant", len(execution.requests), len(st.grants))
	}
	cancels := 0
	for _, attempt := range st.attempts {
		if attempt.Intent == "cancel" {
			cancels++
		}
	}
	if cancels != 1 {
		t.Fatalf("cancel attempts=%d, recovery duplicated the broker effect", cancels)
	}
}

func TestM5BPendingCancelRecoveryBypassesOriginalProposalTTL(t *testing.T) {
	s, st, execution, source, _ := newRepriceTestServer(t, "1", 3)
	row := st.operationRows[source.OperationID]
	row.TS = time.Now().UTC().Add(-time.Hour)
	st.operationRows[source.OperationID] = row
	cancelAttempt, err := st.StageRepriceCancel(source.ID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel: attempt=%+v err=%v", cancelAttempt, err)
	}
	if err := s.reconcilePendingAttempt(context.Background(), cancelAttempt); err != nil {
		t.Fatal(err)
	}
	if execution.cancelCalls != 1 || len(execution.requests) != 1 {
		t.Fatalf("recovered pending cancel: cancels=%d replacements=%d", execution.cancelCalls, len(execution.requests))
	}
	if st.statuses[source.OperationID] == "failed" {
		t.Fatal("reprice cancel was incorrectly expired using the original proposal TTL")
	}
}

func TestM5BPendingReplacementRecoveryUsesDurableIntent(t *testing.T) {
	s, st, execution, source, _ := newRepriceTestServer(t, "1", 3)
	cancelAttempt, err := st.StageRepriceCancel(source.ID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel: attempt=%+v err=%v", cancelAttempt, err)
	}
	claimed, err := st.ClaimPendingAttempt(cancelAttempt.ID, "crashed-worker", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim cancel: attempt=%+v err=%v", claimed, err)
	}
	result, err := execution.CancelOrder(context.Background(), source.BrokerOrderID)
	if err != nil {
		t.Fatal(err)
	}
	update := resolutionForOrder(&store.ExecutionAttempt{
		ID: source.ExecutionAttemptID, Intent: "place",
	}, result).OrderUpdate
	if update == nil {
		t.Fatal("terminal cancel did not produce an order update")
	}
	next, err := st.FinalizeRepriceCancel(claimed.ID, claimed.Attempt, *update, &store.RepriceReplacement{
		AttemptID: store.NewID(), OrderID: store.NewID(), ClientOrderID: store.NewID(),
		Limit: units.MustMicros("108"),
	}, "")
	if err != nil || next == nil {
		t.Fatalf("stage replacement: next=%+v err=%v", next, err)
	}
	row := st.operationRows[source.OperationID]
	row.TS = time.Now().UTC().Add(-time.Hour)
	st.operationRows[source.OperationID] = row
	if err := s.reconcilePendingAttempt(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 1 || execution.requests[0].Limit != units.MustMicros("108") {
		t.Fatalf("recovered replacement requests=%+v, want durable limit 108", execution.requests)
	}
	if st.statuses[source.OperationID] == "failed" {
		t.Fatal("durable replacement was incorrectly expired or re-gated as a new proposal")
	}
}

func TestM5BHaltBeforePendingReplacementReleasesWithoutPlacement(t *testing.T) {
	s, st, execution, source, _ := newRepriceTestServer(t, "3", 3)
	now := time.Now().UTC()
	execution.cancelResults[source.BrokerOrderID] = broker.OrderResult{
		BrokerOrderID: source.BrokerOrderID, ClientOrderID: source.ClientOrderID,
		State: "cancelled", FilledQty: units.MustQty("1"),
		Fills: []broker.ReadFill{{
			FillID: "halted-replacement-prior-fill", BrokerOrderID: source.BrokerOrderID,
			Symbol: source.Symbol, Side: source.Side, Qty: units.MustQty("1"),
			Price: units.MustMicros("105"), AsOf: now,
		}},
	}
	cancelAttempt, err := st.StageRepriceCancel(source.ID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel: attempt=%+v err=%v", cancelAttempt, err)
	}
	claimed, err := st.ClaimPendingAttempt(cancelAttempt.ID, "crashed-worker", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim cancel: attempt=%+v err=%v", claimed, err)
	}
	result, err := execution.CancelOrder(context.Background(), source.BrokerOrderID)
	if err != nil {
		t.Fatal(err)
	}
	update := resolutionForOrder(&store.ExecutionAttempt{ID: source.ExecutionAttemptID, Intent: "place"}, result).OrderUpdate
	next, err := st.FinalizeRepriceCancel(claimed.ID, claimed.Attempt, *update, &store.RepriceReplacement{
		AttemptID: store.NewID(), OrderID: store.NewID(), ClientOrderID: store.NewID(),
		Limit: units.MustMicros("108"),
	}, "")
	if err != nil || next == nil {
		t.Fatalf("stage replacement: next=%+v err=%v", next, err)
	}
	st.halted, st.haltReason = true, "operator stop"
	if err := s.reconcilePendingAttempt(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 0 {
		t.Fatalf("halted recovery placed %d replacement orders", len(execution.requests))
	}
	if !containsEvent(st.events, "order_expired_policy") {
		t.Fatalf("events=%v, want order_expired_policy", st.events)
	}
	for _, reservation := range st.openReservations {
		if reservation.ResourceState != "released" {
			t.Fatalf("halted pending replacement kept reservation: %+v", reservation)
		}
	}
	row, err := st.GetOperation(source.OperationID)
	if err != nil || row.Status != "executed" {
		t.Fatalf("partial-fill operation=%+v err=%v, want executed", row, err)
	}
	replacementOrder, err := st.GetOrderByAttempt(next.ID)
	if err != nil || replacementOrder.State != "rejected" || replacementOrder.BrokerOrderID != "" {
		t.Fatalf("unsent replacement=%+v err=%v, want rejected without broker id", replacementOrder, err)
	}
	if len(st.fills) != 1 || len(st.grants) != 1 {
		t.Fatalf("fills=%d grants=%d, want prior fill and burned grant preserved", len(st.fills), len(st.grants))
	}
}

func TestM5BHaltCancelsOpenButDoesNotSuppressClose(t *testing.T) {
	s, st, execution, _, _ := newRepriceTestServer(t, "1", 3)
	st.halted, st.haltReason = true, "operator stop"
	if err := s.repriceOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(execution.requests) != 0 || !containsEvent(st.events, "order_expired_policy") {
		t.Fatalf("halted open requests=%d events=%v", len(execution.requests), st.events)
	}

	closeServer, closeStore, closeExecution, closeOrder, closeOp := newRepriceTestServer(t, "1", 3)
	closeStore.halted, closeStore.haltReason = true, "operator stop"
	closeOp.Action, closeOp.Side = "close", "sell"
	minimum := units.MustMicros("90")
	closeOp.Limit, closeOp.ApprovedPriceCap = &minimum, 0
	payload, err := json.Marshal(closeOp)
	if err != nil {
		t.Fatal(err)
	}
	closeStore.operationRows[closeOrder.OperationID] = store.OperationRow{
		ID: closeOrder.OperationID, Class: "A", Status: "auto_approved", Payload: payload, TS: time.Now().UTC(),
	}
	closeStore.operations[closeOrder.OperationID] = closeOp
	closeStore.classes[closeOrder.OperationID] = "A"
	closeAttempt := closeStore.attempts[closeOrder.ExecutionAttemptID]
	delete(closeStore.openReservations, closeAttempt.OpenReservationID)
	closeAttempt.OpenReservationID = ""
	closeAttempt.CloseReservationID = store.NewID()
	closeStore.attempts[closeAttempt.ID] = closeAttempt
	closeStore.reservations[closeAttempt.CloseReservationID] = store.CloseReservation{
		ID: closeAttempt.CloseReservationID, OperationID: closeOrder.OperationID,
		Ledger: "live", Symbol: "XYZ", OriginalQty: units.MustQty("1"),
		RemainingQty: units.MustQty("1"), State: "held",
	}
	closeOrder.Side = "sell"
	closeStore.orders[closeOrder.ExecutionAttemptID] = closeOrder
	if err := closeServer.repriceOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(closeExecution.requests) != 1 || closeExecution.requests[0].Side != "sell" ||
		closeExecution.requests[0].PositionEffect != "close" {
		t.Fatalf("halted close requests=%+v, want one eligible replacement", closeExecution.requests)
	}
}

func TestM5BTickRoundingNeverCrossesHardBounds(t *testing.T) {
	buy, err := nextBoundedBuyLimit(
		units.MustMicros("2.995"), units.MustMicros("3.01"),
		units.MustMicros("3.005"), units.MustMicros("0.01"),
	)
	if err != nil || buy != units.MustMicros("3") {
		t.Fatalf("buy=%s err=%v, want tick-valid 3.00 at or below cap", buy, err)
	}
	sell, err := nextBoundedSellLimit(
		units.MustMicros("3.03"), units.MustMicros("3"),
		units.MustMicros("3.005"), units.MustMicros("0.01"),
	)
	if err != nil || sell != units.MustMicros("3.02") {
		t.Fatalf("sell=%s err=%v, want half-step 3.02 at or above minimum", sell, err)
	}
}

func TestM11EquityTickScheduleRoundsOnBothSidesOfDollar(t *testing.T) {
	instrument := broker.Instrument{
		PriceTick: units.MustMicros("0.01"), BelowPriceTick: units.MustMicros("0.0001"),
		TickCutoff: units.MustMicros("1"), QtyIncrement: units.MustQty("1"),
	}
	for _, test := range []struct {
		value string
		ceil  string
		floor string
	}{
		{value: "13.501", ceil: "13.51", floor: "13.50"},
		{value: "1.000001", ceil: "1.01", floor: "1"},
		{value: "0.50001", ceil: "0.5001", floor: "0.5"},
	} {
		value := units.MustMicros(test.value)
		ceil, err := ceilPriceForInstrument(value, instrument)
		floor := floorPriceForInstrument(value, instrument)
		if err != nil || ceil != units.MustMicros(test.ceil) || floor != units.MustMicros(test.floor) {
			t.Fatalf("value=%s ceil=%s floor=%s err=%v", test.value, ceil, floor, err)
		}
	}
}

func requestOperationID(st *memoryStore) string {
	for operationID := range st.operationRows {
		return operationID
	}
	return ""
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
