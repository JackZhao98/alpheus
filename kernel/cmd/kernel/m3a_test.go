package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

func TestPaperAttemptFailsClosedWhenQuoteMovesThroughLimit(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "PAPER", "10", "11", 1_000)
	st := newMemoryStore()
	st.m3aActive = true

	operationID, reservationID, attemptID := store.NewID(), store.NewID(), store.NewID()
	op := risk.Operation{
		Proposer: "m3a-test", Action: "open", Shadow: true,
		Symbol: "PAPER", Underlying: "PAPER", Kind: "equity", Side: "buy",
		Qty: units.MustQty("1"), Multiplier: 1,
		DerivedMaxRisk: units.MustMicros("10"), RequiredCash: units.MustMicros("10"),
		ApprovedPriceCap: units.MustMicros("10"), WorkingPrice: units.MustMicros("10"),
	}
	if err := st.InsertOperation(operationID, op.Proposer, "B", "auto_approved", op, risk.Verdict{Class: "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertOpenReservation(store.OpenReservation{
		ID: reservationID, OperationID: operationID, Ledger: "shadow",
		MarketDay: time.Now(), Symbol: op.Symbol, Kind: op.Kind,
		OriginalQty: op.Qty, RemainingQty: op.Qty,
		OriginalRisk: op.DerivedMaxRisk, RemainingRisk: op.DerivedMaxRisk,
		OriginalCash: op.RequiredCash, RemainingCash: op.RequiredCash,
		ResourceState: "held",
	}); err != nil {
		t.Fatal(err)
	}
	attempt := store.ExecutionAttempt{
		ID: attemptID, OperationID: operationID, Seq: 1,
		OpenReservationID: reservationID, Intent: "paper_place",
		ClientOrderID: "shadow:" + attemptID, State: "pending",
		Qty: op.Qty, Limit: units.MustMicros("10"),
	}
	if err := st.InsertExecutionAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertOrder(store.Order{
		ID: store.NewID(), OperationID: operationID, ExecutionAttemptID: attemptID,
		ClientOrderID: attempt.ClientOrderID, Ledger: "shadow", Symbol: op.Symbol,
		Side: op.Side, Kind: op.Kind, Multiplier: op.Multiplier,
		Qty: op.Qty, Limit: attempt.Limit, State: "new",
	}); err != nil {
		t.Fatal(err)
	}

	s := &server{limits: config.Limits{}, broker: venue, store: st}
	if _, err := s.executePendingAttempt(context.Background(), attemptID); !errors.Is(err, errPaperExecutionFailed) {
		t.Fatalf("paper attempt error=%v, want deterministic paper failure", err)
	}
	resolved, err := st.GetExecutionAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := st.GetOpenReservation(reservationID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != "failed" || reservation.ResourceState != "released" ||
		reservation.RemainingRisk != 0 || len(st.fills) != 0 || len(st.shadowPositions) != 0 {
		t.Fatalf("attempt=%+v reservation=%+v fills=%d positions=%v",
			resolved, reservation, len(st.fills), st.shadowPositions)
	}
}

func TestOpenBrokerTimeoutKeepsGrantAndReservationHeld(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "TIMEOUT-OPEN", "9.90", "10", 1_000)
	st := newMemoryStore()
	st.m3aActive = true
	s := &server{
		limits: dualLedgerLimits(), broker: venue, execution: timeoutExecution{base: venue},
		store: st, brokerTimeout: 20 * time.Millisecond, attemptStale: time.Millisecond,
	}
	response, body := postOperation(t, s,
		`{"action":"open","kind":"equity","underlying":"TIMEOUT-OPEN","symbol":"TIMEOUT-OPEN","side":"buy","qty":1,"plan":{"stop":"9","invalidation":"x","time_stop":"15:45","target":"12"}}`)
	if response.Code != http.StatusBadGateway || body["status"] != "unknown" {
		t.Fatalf("status=%d body=%v", response.Code, body)
	}
	st.mu.Lock()
	if len(st.grants) != 1 || len(st.openReservations) != 1 {
		st.mu.Unlock()
		t.Fatalf("grants=%d reservations=%d", len(st.grants), len(st.openReservations))
	}
	for id, attempt := range st.attempts {
		if attempt.State != "unknown" {
			st.mu.Unlock()
			t.Fatalf("attempt=%+v", attempt)
		}
		attempt.ClaimedAt = time.Now().Add(-time.Second)
		st.attempts[id] = attempt
	}
	for _, reservation := range st.openReservations {
		if reservation.ResourceState != "held" || reservation.RemainingRisk != units.MustMicros("10") {
			st.mu.Unlock()
			t.Fatalf("reservation=%+v", reservation)
		}
	}
	st.mu.Unlock()
	if err := s.reconcileAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, attempt := range st.attempts {
		if attempt.State != "unknown" || attempt.Attempt != 2 {
			t.Fatalf("reconciled attempt=%+v", attempt)
		}
	}
	for _, reservation := range st.openReservations {
		if reservation.ResourceState != "held" || reservation.RemainingRisk != units.MustMicros("10") {
			t.Fatalf("reconciled reservation=%+v", reservation)
		}
	}
}

type positionsForbiddenProvider struct {
	broker.AccountProvider
	calls int
}

func (p *positionsForbiddenProvider) Positions(context.Context) ([]broker.Position, error) {
	p.calls++
	return nil, errors.New("live positions must not be read for shadow recovery")
}

func TestShadowCloseRecoveryUsesOnlyPaperBook(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "PAPER", "9.90", "10", 1_000)
	account := &positionsForbiddenProvider{AccountProvider: venue}
	st := newMemoryStore()
	st.m3aActive = true
	st.shadowPositions["PAPER"] = store.ShadowPosition{
		Symbol: "PAPER", Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
	}
	st.exposureQty[memoryExposureKey("shadow", "PAPER", "equity")] = units.MustQty("1")

	operationID, reservationID, attemptID := store.NewID(), store.NewID(), store.NewID()
	op := risk.Operation{
		Proposer: "m3a-test", Action: "close", Shadow: true,
		Symbol: "PAPER", Kind: "equity", Side: "sell",
		Qty: units.MustQty("1"), Multiplier: 1, VerifiedReduction: true,
	}
	if err := st.InsertOperation(operationID, op.Proposer, "A", "auto_approved", op, risk.Verdict{Class: "A"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertCloseReservation(store.CloseReservation{
		ID: reservationID, OperationID: operationID, Ledger: "shadow", Symbol: op.Symbol,
		OriginalQty: op.Qty, RemainingQty: op.Qty, State: "held",
	}); err != nil {
		t.Fatal(err)
	}
	attempt := store.ExecutionAttempt{
		ID: attemptID, OperationID: operationID, Seq: 1,
		CloseReservationID: reservationID, Intent: "paper_place",
		ClientOrderID: "shadow:" + attemptID, State: "pending",
		Qty: op.Qty, Limit: units.MustMicros("9.90"), CreatedAt: time.Now().UTC(),
	}
	if err := st.InsertExecutionAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertOrder(store.Order{
		ID: store.NewID(), OperationID: operationID, ExecutionAttemptID: attemptID,
		ClientOrderID: attempt.ClientOrderID, Ledger: "shadow", Symbol: op.Symbol,
		Side: op.Side, Kind: op.Kind, Multiplier: op.Multiplier,
		Qty: op.Qty, Limit: attempt.Limit, State: "new",
	}); err != nil {
		t.Fatal(err)
	}

	s := &server{
		limits: config.Limits{}, account: account, broker: venue, store: st,
	}
	if err := s.reconcilePendingAttempt(context.Background(), &attempt); err != nil {
		t.Fatal(err)
	}
	if account.calls != 0 {
		t.Fatalf("shadow recovery read live positions %d times", account.calls)
	}
	resolved, err := st.GetExecutionAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != "settled" || len(st.shadowPositions) != 0 ||
		st.exposureQty[memoryExposureKey("shadow", "PAPER", "equity")] != 0 {
		t.Fatalf("attempt=%+v positions=%v exposure=%s", resolved, st.shadowPositions,
			st.exposureQty[memoryExposureKey("shadow", "PAPER", "equity")])
	}
}

type recentSinceRecorder struct {
	broker.AccountProvider
	since        time.Time
	accountCalls int
	onRecent     func()
}

func (r *recentSinceRecorder) Account(ctx context.Context) (broker.AccountState, error) {
	r.accountCalls++
	return r.AccountProvider.Account(ctx)
}

func (r *recentSinceRecorder) RecentFills(_ context.Context, since time.Time) ([]broker.ReadFill, error) {
	r.since = since
	if r.onRecent != nil {
		r.onRecent()
	}
	return nil, nil
}

func TestStateQueriesFillsFromDatabaseMarketDay(t *testing.T) {
	t.Setenv("TZ_MARKET", "America/New_York")
	venue := newFake("300")
	account := &recentSinceRecorder{AccountProvider: venue}
	st := newMemoryStore()
	databaseTime := time.Date(2026, 1, 16, 0, 30, 0, 0, time.UTC)
	firstClockInsideLiveLock := false
	clockCalls := 0
	st.databaseNow = func() time.Time {
		clockCalls++
		if clockCalls == 1 {
			if st.ledgerLocks[0].TryLock() {
				st.ledgerLocks[0].Unlock()
			} else {
				firstClockInsideLiveLock = true
			}
		}
		return databaseTime
	}
	s := &server{limits: config.Limits{}, account: account, broker: venue, store: st}
	w := httptest.NewRecorder()
	s.getState(w, httptest.NewRequest(http.MethodGet, "/state", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	window, err := marketDayWindow(databaseTime, "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if account.since.IsZero() || !account.since.Equal(window.start) {
		t.Fatalf("recent fills since=%s want=%s", account.since, window.start)
	}
	if account.accountCalls != 1 || !firstClockInsideLiveLock {
		t.Fatalf("account_calls=%d first_clock_inside_live_lock=%v", account.accountCalls, firstClockInsideLiveLock)
	}
}

func TestStateFailsClosedWhenDatabaseClockUnavailable(t *testing.T) {
	venue := newFake("300")
	account := &recentSinceRecorder{AccountProvider: venue}
	st := newMemoryStore()
	st.databaseNowErr = store.ErrUnavailable
	s := &server{limits: config.Limits{}, account: account, broker: venue, store: st}
	w := httptest.NewRecorder()
	s.getState(w, httptest.NewRequest(http.MethodGet, "/state", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "database unavailable") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !account.since.IsZero() {
		t.Fatalf("recent fills queried with fallback market window: %s", account.since)
	}
}

func TestStateRejectsRecentFillsThatCrossMarketDay(t *testing.T) {
	t.Setenv("TZ_MARKET", "America/New_York")
	venue := newFake("300")
	st := newMemoryStore()
	databaseTime := time.Date(2026, 1, 16, 4, 59, 59, 0, time.UTC)
	st.databaseNow = func() time.Time { return databaseTime }
	account := &recentSinceRecorder{AccountProvider: venue}
	account.onRecent = func() {
		databaseTime = time.Date(2026, 1, 16, 5, 0, 0, 0, time.UTC)
	}
	s := &server{limits: config.Limits{}, account: account, broker: venue, store: st}
	w := httptest.NewRecorder()
	s.getState(w, httptest.NewRequest(http.MethodGet, "/state", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "market day advanced") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestStateRejectsLiveShadowMarketDaySplit(t *testing.T) {
	t.Setenv("TZ_MARKET", "America/New_York")
	venue := newFake("300")
	account := &recentSinceRecorder{AccountProvider: venue}
	st := newMemoryStore()
	liveTime := time.Date(2026, 1, 16, 4, 59, 59, 0, time.UTC)
	shadowTime := time.Date(2026, 1, 16, 5, 0, 0, 0, time.UTC)
	st.databaseNow = func() time.Time {
		if st.ledgerLocks[1].TryLock() {
			st.ledgerLocks[1].Unlock()
			return liveTime
		}
		return shadowTime
	}
	s := &server{limits: config.Limits{}, account: account, broker: venue, store: st}
	w := httptest.NewRecorder()
	s.getState(w, httptest.NewRequest(http.MethodGet, "/state", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "market day advanced") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !account.since.IsZero() {
		t.Fatalf("recent fills queried for mixed-day state: %s", account.since)
	}
}

func TestOpenProposalDoesNotExecuteWhenStagingCrossesMarketDay(t *testing.T) {
	t.Setenv("TZ_MARKET", "America/New_York")
	venue := newFake("300")
	setQuote(venue, "MIDNIGHT", "0.99", "1", 1_000)
	st := newMemoryStore()
	beforeMidnight := time.Date(2026, 1, 16, 4, 59, 59, 0, time.UTC)
	afterMidnight := time.Date(2026, 1, 16, 5, 0, 0, 0, time.UTC)
	clockCalls := 0
	st.databaseNow = func() time.Time {
		clockCalls++
		if clockCalls >= 4 {
			return afterMidnight
		}
		return beforeMidnight
	}
	s := &server{limits: dualLedgerLimits(), account: venue, broker: venue, store: st}
	response, body := postOperation(t, s, `{
		"action":"open","kind":"equity","underlying":"MIDNIGHT","symbol":"MIDNIGHT",
		"side":"buy","qty":1,
		"plan":{"stop":"0.90","invalidation":"x","time_stop":"15:45","target":"1.20"}
	}`)
	if response.Code != http.StatusServiceUnavailable || body["error"] != "market day advanced; retry" {
		t.Fatalf("status=%d body=%v clock_calls=%d", response.Code, body, clockCalls)
	}
	fills, err := venue.RecentFills(context.Background(), time.Time{})
	if err != nil || len(fills) != 0 {
		t.Fatalf("cross-day proposal reached broker fills=%v err=%v", fills, err)
	}
}

type accountReadRecorder struct {
	broker.AccountProvider
	accountCalls int
}

func (r *accountReadRecorder) Account(ctx context.Context) (broker.AccountState, error) {
	r.accountCalls++
	return r.AccountProvider.Account(ctx)
}

func TestOpenUsesDatabaseMarketDayAfterAccountRead(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "CLOCK", "9.90", "10", 1_000)
	account := &accountReadRecorder{AccountProvider: venue}
	st := newMemoryStore()
	databaseTime := time.Date(2026, 1, 16, 5, 1, 0, 0, time.UTC)
	clockObservedAccount := false
	st.databaseNow = func() time.Time {
		clockObservedAccount = account.accountCalls > 0
		return databaseTime
	}
	s := &server{limits: dualLedgerLimits(), account: account, broker: venue, store: st}
	w, body := postOperation(t, s,
		`{"proposer":"clock","action":"open","kind":"equity","underlying":"CLOCK","symbol":"CLOCK","side":"buy","qty":1,"plan":{"stop":"9","invalidation":"x","time_stop":"15:45","target":"12"}}`)
	if w.Code != http.StatusOK || body["class"] != "B" {
		t.Fatalf("status=%d body=%v", w.Code, body)
	}
	operationID, _ := body["operation_id"].(string)
	grant := st.grants[operationID]
	market, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if !clockObservedAccount || grant.MarketDay.In(market).Format(time.DateOnly) != "2026-01-16" {
		t.Fatalf("clock_after_account=%v market_day=%s", clockObservedAccount,
			grant.MarketDay.In(market).Format(time.DateOnly))
	}
}

type timeoutAccountProvider struct {
	broker.AccountProvider
	entered chan struct{}
	once    sync.Once
}

func (p *timeoutAccountProvider) Account(ctx context.Context) (broker.AccountState, error) {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return broker.AccountState{}, ctx.Err()
}

func TestTimedOutOpenCommitsNoAttemptWhileVerifiedCloseProceeds(t *testing.T) {
	venue := newFake("300")
	setQuote(venue, "BLOCK", "9.90", "10", 1_000)
	setQuote(venue, "EXIT", "9.90", "10", 1_000)
	if result, err := placeOrder(venue, "EXIT", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed exit: result=%+v err=%v", result, err)
	}
	account := &timeoutAccountProvider{AccountProvider: venue, entered: make(chan struct{})}
	st := newMemoryStore()
	s := &server{
		limits: dualLedgerLimits(), account: account, broker: venue, store: st,
		brokerTimeout: 50 * time.Millisecond,
	}

	type response struct {
		code int
		body map[string]any
	}
	openResult := make(chan response, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/operations", bytes.NewBufferString(
			`{"proposer":"timeout","action":"open","kind":"equity","underlying":"BLOCK","symbol":"BLOCK","side":"buy","qty":1,"plan":{"stop":"9","invalidation":"x","time_stop":"15:45","target":"12"}}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.propose(w, req)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		openResult <- response{code: w.Code, body: body}
	}()
	select {
	case <-account.entered:
	case <-time.After(time.Second):
		t.Fatal("open never reached the locked account read")
	}

	closeResponse, closeBody := postOperation(t, s,
		`{"proposer":"exit","action":"close","symbol":"EXIT","qty":1}`)
	if closeResponse.Code != http.StatusOK || closeBody["class"] != "A" || closeBody["status"] != "executed" {
		t.Fatalf("close status=%d body=%v", closeResponse.Code, closeBody)
	}
	open := <-openResult
	if open.code != http.StatusBadGateway {
		t.Fatalf("open status=%d body=%v", open.code, open.body)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, operation := range st.operations {
		if operation.Action == "open" {
			t.Fatalf("timed-out open committed: %+v", operation)
		}
	}
}

func TestRestingOpenReservationBlocksRiskAndBuyingPowerReuse(t *testing.T) {
	plan := `"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}`
	t.Run("total open risk", func(t *testing.T) {
		venue := newFake("1000")
		setQuote(venue, "RISK1", "99", "100", 1_000)
		setQuote(venue, "RISK2", "99", "100", 1_000)
		limits := dualLedgerLimits()
		limits.HardLimits.MaxRiskPerTradePct = units.MustPercent("100")
		limits.HardLimits.MaxTotalOpenRiskPct = units.MustPercent("15")
		st := newMemoryStore()
		setMemoryKernelPolicy(st, limits)
		s := &server{limits: limits, broker: venue, store: st}

		first, firstBody := postOperation(t, s,
			`{"action":"open","kind":"equity","underlying":"RISK1","symbol":"RISK1","side":"buy","qty":1,`+plan+`}`)
		if first.Code != http.StatusOK || firstBody["class"] != "B" {
			t.Fatalf("first status=%d body=%v", first.Code, firstBody)
		}
		second, secondBody := postOperation(t, s,
			`{"action":"open","kind":"equity","underlying":"RISK2","symbol":"RISK2","side":"buy","qty":1,`+plan+`}`)
		if second.Code != http.StatusOK || secondBody["class"] != "C" || secondBody["status"] != "pending_review" {
			t.Fatalf("second status=%d body=%v", second.Code, secondBody)
		}
		checks, _ := secondBody["checks"].(map[string]any)
		if checks["total_open_risk"] != false {
			t.Fatalf("checks=%v, want total_open_risk=false", checks)
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		held := 0
		for _, reservation := range st.openReservations {
			if reservation.ResourceState == "held" {
				held++
			}
		}
		if held != 1 {
			t.Fatalf("held reservations=%d, want 1", held)
		}
	})

	t.Run("buying power", func(t *testing.T) {
		venue := newFake("150")
		setQuote(venue, "CASH1", "99", "100", 1_000)
		setQuote(venue, "CASH2", "59", "60", 1_000)
		limits := dualLedgerLimits()
		limits.HardLimits.MaxRiskPerTradePct = units.MustPercent("100")
		limits.HardLimits.MaxTotalOpenRiskPct = units.MustPercent("100")
		st := newMemoryStore()
		setMemoryKernelPolicy(st, limits)
		s := &server{limits: limits, broker: venue, store: st}

		first, firstBody := postOperation(t, s,
			`{"action":"open","kind":"equity","underlying":"CASH1","symbol":"CASH1","side":"buy","qty":1,`+plan+`}`)
		if first.Code != http.StatusOK || firstBody["class"] != "B" {
			t.Fatalf("first status=%d body=%v", first.Code, firstBody)
		}
		second, secondBody := postOperation(t, s,
			`{"action":"open","kind":"equity","underlying":"CASH2","symbol":"CASH2","side":"buy","qty":1,`+plan+`}`)
		if second.Code != http.StatusOK || secondBody["class"] != "REJECT" || secondBody["status"] != "rejected" {
			t.Fatalf("second status=%d body=%v", second.Code, secondBody)
		}
		reasons, _ := secondBody["reasons"].([]any)
		if len(reasons) == 0 || reasons[0] != "insufficient_buying_power" {
			t.Fatalf("reasons=%v", reasons)
		}
	})
}

func TestCloseUsesSmallerOfPositionAndExposureAndEmitsMismatch(t *testing.T) {
	tests := []struct {
		name        string
		positionQty string
		exposureQty string
		closeQty    string
	}{
		{name: "broker exceeds exposure", positionQty: "2", exposureQty: "1", closeQty: "2"},
		{name: "exposure exceeds broker", positionQty: "1", exposureQty: "2", closeQty: "2"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			venue := newFake("1000")
			setQuote(venue, "MISMATCH", "9.90", "10", 1_000)
			if result, err := placeOrder(venue, "MISMATCH", "buy", testCase.positionQty, "10", "equity"); err != nil || result.State != "filled" {
				t.Fatalf("seed: result=%+v err=%v", result, err)
			}
			st := newMemoryStore()
			st.m3aActive = true
			st.exposureQty[memoryExposureKey("live", "MISMATCH", "equity")] = units.MustQty(testCase.exposureQty)
			s := &server{limits: dualLedgerLimits(), broker: venue, store: st}
			response, body := postOperation(t, s,
				`{"action":"close","symbol":"MISMATCH","qty":`+testCase.closeQty+`}`)
			if response.Code != http.StatusBadRequest || body["error"] != "insufficient closable quantity" {
				t.Fatalf("status=%d body=%v", response.Code, body)
			}
			st.mu.Lock()
			mismatchEvent := false
			for _, event := range st.events {
				if event == "position_exposure_mismatch" {
					mismatchEvent = true
				}
			}
			operationCount := len(st.operations)
			st.mu.Unlock()
			if !mismatchEvent {
				t.Fatalf("events=%v, want position_exposure_mismatch", st.events)
			}
			if operationCount != 0 {
				t.Fatalf("rejected mismatch persisted %d operations", operationCount)
			}
			positions, err := venue.Positions(context.Background())
			if err != nil || len(positions) != 1 || positions[0].Qty != units.MustQty(testCase.positionQty) {
				t.Fatalf("rejected close changed broker: positions=%v err=%v", positions, err)
			}
		})
	}
}

func TestCloseOperationHintMustMatchFirstFIFOLot(t *testing.T) {
	venue := newFake("1000")
	setQuote(venue, "FIFO", "9.90", "10", 1_000)
	if result, err := placeOrder(venue, "FIFO", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed: result=%+v err=%v", result, err)
	}
	st := newMemoryStore()
	st.m3aActive = true
	key := memoryExposureKey("live", "FIFO", "equity")
	st.exposureQty[key] = units.MustQty("1")
	st.firstExposureOp[key] = store.NewID()
	s := &server{limits: dualLedgerLimits(), broker: venue, store: st}
	response, body := postOperation(t, s,
		`{"action":"close","symbol":"FIFO","qty":1,"closes_operation_id":"`+store.NewID()+`"}`)
	if response.Code != http.StatusBadRequest || body["error"] != "closes_operation_id does not match the first FIFO lot" {
		t.Fatalf("status=%d body=%v", response.Code, body)
	}
	positions, err := venue.Positions(context.Background())
	if err != nil || len(positions) != 1 || positions[0].Qty != units.MustQty("1") {
		t.Fatalf("bad FIFO hint changed broker: positions=%v err=%v", positions, err)
	}
}
