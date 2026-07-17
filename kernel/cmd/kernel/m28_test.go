package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type rejectingExecution struct{}

func (rejectingExecution) PlaceLimitOrder(_ context.Context, request broker.PlaceRequest) (broker.OrderResult, error) {
	return broker.OrderResult{
		ClientOrderID: request.ClientOrderID, State: "rejected", Reason: "injected rejection",
	}, nil
}
func (rejectingExecution) CancelOrder(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{State: "rejected", Reason: "injected rejection"}, nil
}
func (rejectingExecution) GetOrder(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{}, broker.ErrNotFound
}
func (rejectingExecution) FindOrderByClientID(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{}, broker.ErrNotFound
}

type timeoutExecution struct{ base *broker.Fake }

func (e timeoutExecution) PlaceLimitOrder(ctx context.Context, _ broker.PlaceRequest) (broker.OrderResult, error) {
	<-ctx.Done()
	return broker.OrderResult{}, ctx.Err()
}
func (e timeoutExecution) CancelOrder(ctx context.Context, _ string) (broker.OrderResult, error) {
	<-ctx.Done()
	return broker.OrderResult{}, ctx.Err()
}
func (e timeoutExecution) GetOrder(ctx context.Context, id string) (broker.OrderResult, error) {
	return e.base.GetOrder(ctx, id)
}
func (e timeoutExecution) FindOrderByClientID(ctx context.Context, id string) (broker.OrderResult, error) {
	return e.base.FindOrderByClientID(ctx, id)
}

type firstBlockingExecution struct {
	base    *broker.Fake
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func newFirstBlockingExecution(base *broker.Fake) *firstBlockingExecution {
	return &firstBlockingExecution{base: base, started: make(chan struct{}), release: make(chan struct{})}
}

func (e *firstBlockingExecution) PlaceLimitOrder(ctx context.Context, request broker.PlaceRequest) (broker.OrderResult, error) {
	e.mu.Lock()
	e.calls++
	call := e.calls
	if call == 1 {
		close(e.started)
	}
	e.mu.Unlock()
	if call == 1 {
		select {
		case <-e.release:
		case <-ctx.Done():
			return broker.OrderResult{}, ctx.Err()
		}
	}
	return e.base.PlaceLimitOrder(ctx, request)
}
func (e *firstBlockingExecution) CancelOrder(ctx context.Context, id string) (broker.OrderResult, error) {
	return e.base.CancelOrder(ctx, id)
}
func (e *firstBlockingExecution) GetOrder(ctx context.Context, id string) (broker.OrderResult, error) {
	return e.base.GetOrder(ctx, id)
}
func (e *firstBlockingExecution) FindOrderByClientID(ctx context.Context, id string) (broker.OrderResult, error) {
	return e.base.FindOrderByClientID(ctx, id)
}

func TestFailedAuthorizedOpensKeepTradeGrants(t *testing.T) {
	st := newMemoryStore()
	b := newFake("300")
	s := &server{limits: dualLedgerLimits(), broker: b, execution: rejectingExecution{}, store: st}
	payload := `{"action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	for i := 0; i < 6; i++ {
		response, body := postOperation(t, s, payload)
		if response.Code != http.StatusOK || body["status"] != "failed" {
			t.Fatalf("failed open %d: status=%d body=%v", i+1, response.Code, body)
		}
	}
	response, body := postOperation(t, s, payload)
	if response.Code != http.StatusOK || body["class"] != "C" || body["status"] != "pending_review" {
		t.Fatalf("seventh open: status=%d body=%v", response.Code, body)
	}
	st.mu.Lock()
	grantCount, attemptCount := len(st.grants), len(st.attempts)
	st.mu.Unlock()
	if grantCount != 6 || attemptCount != 6 {
		t.Fatalf("grants=%d attempts=%d, want 6/6", grantCount, attemptCount)
	}

	reject := `{"action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"sell","qty":1,"max_risk_usd":35,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	response, body = postOperation(t, s, reject)
	if response.Code != http.StatusOK || body["class"] != "REJECT" {
		t.Fatalf("reject status=%d body=%v", response.Code, body)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.grants) != 6 || len(st.attempts) != 6 {
		t.Fatalf("REJECT created entitlement: grants=%d attempts=%d", len(st.grants), len(st.attempts))
	}
}

func TestDelayedCloseReservationBlocksNineteenFollowers(t *testing.T) {
	st := newMemoryStore()
	b := newFake("10000")
	setQuote(b, "DELAY", "9.90", "10", 1_000)
	if result, err := placeOrder(b, "DELAY", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed: result=%+v err=%v", result, err)
	}
	blocked := newFirstBlockingExecution(b)
	s := &server{
		limits: dualLedgerLimits(), broker: b, execution: blocked, store: st,
		brokerTimeout: time.Second,
	}
	payload := `{"action":"close","symbol":"DELAY","qty":1}`
	firstResult := make(chan struct {
		code int
		body map[string]any
	}, 1)
	go func() {
		response, body := postOperation(t, s, payload)
		firstResult <- struct {
			code int
			body map[string]any
		}{response.Code, body}
	}()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("first broker call did not block")
	}

	const followers = 19
	start := make(chan struct{})
	results := make(chan int, followers)
	var ready sync.WaitGroup
	ready.Add(followers)
	for i := 0; i < followers; i++ {
		go func() {
			ready.Done()
			<-start
			response, _ := postOperation(t, s, payload)
			results <- response.Code
		}()
	}
	ready.Wait()
	close(start)
	for i := 0; i < followers; i++ {
		if code := <-results; code != http.StatusBadRequest {
			t.Fatalf("follower status=%d, want 400", code)
		}
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 1 || positions[0].Qty != units.MustQty("1") {
		t.Fatalf("position moved before broker release: positions=%v err=%v", positions, err)
	}
	st.mu.Lock()
	held := 0
	for _, reservation := range st.reservations {
		if reservation.State == "held" {
			held++
		}
	}
	st.mu.Unlock()
	if held != 1 {
		t.Fatalf("held reservations=%d, want 1", held)
	}
	close(blocked.release)
	first := <-firstResult
	if first.code != http.StatusOK || first.body["status"] != "executed" {
		t.Fatalf("first result status=%d body=%v", first.code, first.body)
	}
	positions, err = b.Positions(context.Background())
	if err != nil || len(positions) != 0 {
		t.Fatalf("final positions=%v err=%v", positions, err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, reservation := range st.reservations {
		if reservation.State != "held" || reservation.RemainingQty != units.MustQty("1") {
			t.Fatalf("filled close reservation=%+v, want held until M2.9 persists the fill", reservation)
		}
	}
}

func TestBrokerTimeoutLeavesAttemptUnknownAndReservationHeld(t *testing.T) {
	st := newMemoryStore()
	b := newFake("10000")
	setQuote(b, "TIMEOUT", "9.90", "10", 1_000)
	if result, err := placeOrder(b, "TIMEOUT", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed: result=%+v err=%v", result, err)
	}
	s := &server{
		limits: dualLedgerLimits(), broker: b, execution: timeoutExecution{base: b}, store: st,
		brokerTimeout: 20 * time.Millisecond, attemptStale: time.Millisecond,
	}
	response, body := postOperation(t, s, `{"action":"close","symbol":"TIMEOUT","qty":1}`)
	if response.Code != http.StatusBadGateway || body["status"] != "unknown" {
		t.Fatalf("status=%d body=%v", response.Code, body)
	}
	st.mu.Lock()
	for _, attempt := range st.attempts {
		if attempt.State != "unknown" {
			t.Fatalf("attempt=%+v, want unknown", attempt)
		}
		reservation := st.reservations[attempt.CloseReservationID]
		if reservation.State != "held" || reservation.RemainingQty != units.MustQty("1") {
			t.Fatalf("reservation=%+v, want held 1", reservation)
		}
		attempt.ClaimedAt = time.Now().Add(-time.Second)
		st.attempts[attempt.ID] = attempt
	}
	st.mu.Unlock()
	if err := s.reconcileAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, attempt := range st.attempts {
		if attempt.State != "unknown" || attempt.Attempt != 2 {
			t.Fatalf("reconciled attempt=%+v, want queried unknown with token 2", attempt)
		}
		reservation := st.reservations[attempt.CloseReservationID]
		if reservation.State != "held" {
			t.Fatalf("ambiguous reservation=%+v, want held", reservation)
		}
	}
}

func TestPendingOpenRecoveryRegatesAndKeepsGrantOnFailure(t *testing.T) {
	st := newMemoryStore()
	b := newFake("300")
	s := &server{
		limits: dualLedgerLimits(), broker: b, store: st,
		brokerTimeout: time.Second, attemptStale: time.Millisecond,
	}
	quote, err := b.GetQuote("SPY")
	if err != nil {
		t.Fatal(err)
	}
	maxRisk := units.MustMicros("35")
	op := risk.Operation{
		Proposer: "recovery", Action: "open", Kind: "option", Underlying: "SPY",
		Symbol: "SPY", Side: "buy", Qty: units.MustQty("1"), MaxRiskUSD: &maxRisk,
		Plan: map[string]string{"stop": "x", "invalidation": "x", "time_stop": "x", "target": "x"},
	}
	op = s.deriveOpenOperation(context.Background(), op, &quote)
	opID, attemptID := store.NewID(), store.NewID()
	if err := st.InsertOperation(opID, op.Proposer, "B", "auto_approved", op, risk.Verdict{Class: "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertTradeGrant(store.TradeGrant{
		OperationID: opID, Ledger: "live", MarketDay: time.Now(),
		AuthorizedRisk: op.DerivedMaxRisk, RiskSource: "computed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExecutionAttempt(store.ExecutionAttempt{
		ID: attemptID, OperationID: opID, Seq: 1, Intent: "place",
		ClientOrderID: store.NewID(), State: "pending", Qty: op.Qty,
		Limit: op.WorkingPrice, CreatedAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.halted, st.haltReason = true, "recovery test halt"
	st.mu.Unlock()
	if err := s.reconcileAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetExecutionAttempt(attemptID)
	if err != nil || current.State != "failed" {
		t.Fatalf("attempt=%+v err=%v, want failed after re-gate", current, err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.grants) != 1 {
		t.Fatalf("grants=%d, want immutable grant retained", len(st.grants))
	}
}

func TestExpiredPendingAttemptsNeverReachBroker(t *testing.T) {
	t.Run("open keeps immutable grant", func(t *testing.T) {
		st := newMemoryStore()
		b := newFake("300")
		s := &server{
			limits: dualLedgerLimits(), broker: b, store: st,
			proposalTTL: 30 * time.Minute, attemptStale: time.Millisecond,
		}
		quote, err := b.GetQuote("SPY")
		if err != nil {
			t.Fatal(err)
		}
		maxRisk := units.MustMicros("35")
		op := risk.Operation{
			Proposer: "expired", Action: "open", Kind: "option", Underlying: "SPY",
			Symbol: "SPY", Side: "buy", Qty: units.MustQty("1"), MaxRiskUSD: &maxRisk,
			Plan: map[string]string{"stop": "x", "invalidation": "x", "time_stop": "x", "target": "x"},
		}
		op = s.deriveOpenOperation(context.Background(), op, &quote)
		opID, attemptID := store.NewID(), store.NewID()
		if err := st.InsertOperation(opID, op.Proposer, "B", "auto_approved", op, risk.Verdict{Class: "B"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertTradeGrant(store.TradeGrant{
			OperationID: opID, Ledger: "live", MarketDay: time.Now(),
			AuthorizedRisk: op.DerivedMaxRisk, RiskSource: "computed",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertExecutionAttempt(store.ExecutionAttempt{
			ID: attemptID, OperationID: opID, Seq: 1, Intent: "place",
			ClientOrderID: store.NewID(), State: "pending", Qty: op.Qty,
			Limit: op.WorkingPrice, CreatedAt: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		st.mu.Lock()
		row := st.operationRows[opID]
		row.TS = time.Now().Add(-time.Hour)
		st.operationRows[opID] = row
		st.mu.Unlock()

		if err := s.reconcileAttempts(context.Background()); err != nil {
			t.Fatal(err)
		}
		current, err := st.GetExecutionAttempt(attemptID)
		if err != nil || current.State != "failed" || current.LastError != "proposal expired before recovery" {
			t.Fatalf("attempt=%+v err=%v, want expired failure", current, err)
		}
		st.mu.Lock()
		grantCount := len(st.grants)
		st.mu.Unlock()
		if grantCount != 1 {
			t.Fatalf("grants=%d, want immutable grant retained", grantCount)
		}
		orders, err := b.OpenOrders(context.Background())
		if err != nil || len(orders) != 0 {
			t.Fatalf("expired open reached broker: orders=%v err=%v", orders, err)
		}
	})

	t.Run("close releases untouched quantity", func(t *testing.T) {
		st := newMemoryStore()
		b := newFake("10000")
		setQuote(b, "EXPIRED", "9.90", "10", 1_000)
		if result, err := placeOrder(b, "EXPIRED", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
			t.Fatalf("seed: result=%+v err=%v", result, err)
		}
		s := &server{
			limits: dualLedgerLimits(), broker: b, store: st,
			proposalTTL: 30 * time.Minute, attemptStale: time.Millisecond,
		}
		opID, reservationID, attemptID := store.NewID(), store.NewID(), store.NewID()
		op := risk.Operation{
			Proposer: "expired", Action: "close", Kind: "equity", Symbol: "EXPIRED",
			Side: "sell", Qty: units.MustQty("1"), Multiplier: 1, VerifiedReduction: true,
		}
		if err := st.InsertOperation(opID, op.Proposer, "A", "auto_approved", op, risk.Verdict{Class: "A"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertCloseReservation(store.CloseReservation{
			ID: reservationID, OperationID: opID, Ledger: "live", Symbol: op.Symbol,
			OriginalQty: op.Qty, RemainingQty: op.Qty, State: "held",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertExecutionAttempt(store.ExecutionAttempt{
			ID: attemptID, OperationID: opID, Seq: 1, CloseReservationID: reservationID,
			Intent: "place", ClientOrderID: store.NewID(), State: "pending", Qty: op.Qty,
			Limit: units.MustMicros("9.90"), CreatedAt: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		st.mu.Lock()
		row := st.operationRows[opID]
		row.TS = time.Now().Add(-time.Hour)
		st.operationRows[opID] = row
		st.mu.Unlock()

		if err := s.reconcileAttempts(context.Background()); err != nil {
			t.Fatal(err)
		}
		reservation, err := st.GetCloseReservation(reservationID)
		if err != nil || reservation.State != "released" || reservation.RemainingQty != 0 {
			t.Fatalf("reservation=%+v err=%v, want released", reservation, err)
		}
		positions, err := b.Positions(context.Background())
		if err != nil || len(positions) != 1 || positions[0].Qty != units.MustQty("1") {
			t.Fatalf("expired close changed broker position: positions=%v err=%v", positions, err)
		}
	})
}

func TestYoungClaimLeaseIsNotSwept(t *testing.T) {
	st := newMemoryStore()
	b := newFake("300")
	s := &server{
		limits: dualLedgerLimits(), broker: b, store: st,
		claimTimeout: time.Second, attemptStale: time.Millisecond,
	}
	opID, attemptID := store.NewID(), store.NewID()
	op := risk.Operation{Proposer: "lease", Action: "cancel", BrokerOrderID: "target"}
	if err := st.InsertOperation(opID, op.Proposer, "A", "auto_approved", op, risk.Verdict{Class: "A"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExecutionAttempt(store.ExecutionAttempt{
		ID: attemptID, OperationID: opID, Seq: 1, Intent: "cancel",
		TargetBrokerOrderID: "target", State: "claimed", Attempt: 1,
		ClaimedAt: time.Now(), CreatedAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetExecutionAttempt(attemptID)
	if err != nil || current.State != "claimed" || current.Attempt != 1 {
		t.Fatalf("young lease changed: attempt=%+v err=%v", current, err)
	}
}

func TestRecoveredCloseRechecksDirectionAndOtherReservations(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		seedQty   string
		closeQty  string
		otherHeld string
		flipShort bool
	}{
		{name: "position direction changed", seedQty: "1", closeQty: "1", flipShort: true},
		{name: "other reservations consume quantity", seedQty: "5", closeQty: "3", otherHeld: "3"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			st := newMemoryStore()
			b := newFake("10000")
			setQuote(b, "RECLOSE", "9.90", "10", 1_000)
			if result, err := placeOrder(b, "RECLOSE", "buy", testCase.seedQty, "10", "equity"); err != nil || result.State != "filled" {
				t.Fatalf("seed: result=%+v err=%v", result, err)
			}
			if testCase.flipShort {
				if result, err := placeOrder(b, "RECLOSE", "sell", "2", "9.90", "equity"); err != nil || result.State != "filled" {
					t.Fatalf("flip: result=%+v err=%v", result, err)
				}
			}
			s := &server{
				limits: dualLedgerLimits(), broker: b, store: st,
				brokerTimeout: time.Second, attemptStale: time.Millisecond,
			}
			opID, reservationID, attemptID := store.NewID(), store.NewID(), store.NewID()
			op := risk.Operation{
				Proposer: "recovery", Action: "close", Kind: "equity", Symbol: "RECLOSE",
				Side: "sell", Qty: units.MustQty(testCase.closeQty), Multiplier: 1,
				VerifiedReduction: true,
			}
			if err := st.InsertOperation(opID, op.Proposer, "A", "auto_approved", op, risk.Verdict{Class: "A"}, nil); err != nil {
				t.Fatal(err)
			}
			if err := st.InsertCloseReservation(store.CloseReservation{
				ID: reservationID, OperationID: opID, Ledger: "live", Symbol: op.Symbol,
				OriginalQty: op.Qty, RemainingQty: op.Qty, State: "held",
			}); err != nil {
				t.Fatal(err)
			}
			if testCase.otherHeld != "" {
				otherOpID := store.NewID()
				if err := st.InsertOperation(otherOpID, "other", "A", "auto_approved", op, risk.Verdict{Class: "A"}, nil); err != nil {
					t.Fatal(err)
				}
				if err := st.InsertCloseReservation(store.CloseReservation{
					ID: store.NewID(), OperationID: otherOpID, Ledger: "live", Symbol: op.Symbol,
					OriginalQty: units.MustQty(testCase.otherHeld), RemainingQty: units.MustQty(testCase.otherHeld), State: "held",
				}); err != nil {
					t.Fatal(err)
				}
			}
			attempt := store.ExecutionAttempt{
				ID: attemptID, OperationID: opID, Seq: 1, CloseReservationID: reservationID,
				Intent: "place", ClientOrderID: store.NewID(), State: "pending", Qty: op.Qty,
				Limit: units.MustMicros("9.90"), CreatedAt: time.Now().Add(-time.Second),
			}
			if err := st.InsertExecutionAttempt(attempt); err != nil {
				t.Fatal(err)
			}
			before, err := b.OpenOrders(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := s.reconcileAttempts(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, err := st.GetExecutionAttempt(attemptID)
			if err != nil || current.State != "failed" {
				t.Fatalf("attempt=%+v err=%v, want failed", current, err)
			}
			after, err := b.OpenOrders(context.Background())
			if err != nil || len(after) != len(before) {
				t.Fatalf("broker effect during failed recovery: before=%d after=%d err=%v", len(before), len(after), err)
			}
		})
	}
}

func TestRecoveredCancelStillWorkingRemainsUnknown(t *testing.T) {
	st := newMemoryStore()
	b := newFake("10000")
	setQuote(b, "CANCELREC", "9.90", "10", 1_000)
	target, err := placeOrder(b, "CANCELREC", "buy", "1", "9.95", "equity")
	if err != nil || target.State != "submitted" {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	s := &server{
		limits: dualLedgerLimits(), broker: b, store: st,
		claimTimeout: time.Millisecond, attemptStale: time.Millisecond,
	}
	opID, attemptID := store.NewID(), store.NewID()
	op := risk.Operation{Proposer: "recovery", Action: "cancel", BrokerOrderID: target.BrokerOrderID}
	if err := st.InsertOperation(opID, op.Proposer, "A", "auto_approved", op, risk.Verdict{Class: "A"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExecutionAttempt(store.ExecutionAttempt{
		ID: attemptID, OperationID: opID, Seq: 1, Intent: "cancel",
		TargetBrokerOrderID: target.BrokerOrderID, State: "claimed", Attempt: 1,
		ClaimedAt: time.Now().Add(-time.Second), CreatedAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetExecutionAttempt(attemptID)
	if err != nil || current.State != "unknown" {
		t.Fatalf("attempt=%+v err=%v, want unknown rather than stranded placed", current, err)
	}
}

func TestCloseReservationReleaseRequiresZeroFill(t *testing.T) {
	attempt := &store.ExecutionAttempt{Intent: "place", CloseReservationID: store.NewID()}
	for _, testCase := range []struct {
		state       string
		filledQty   string
		wantRelease bool
	}{
		{state: "filled", filledQty: "1", wantRelease: false},
		{state: "rejected", filledQty: "0", wantRelease: true},
		{state: "rejected", filledQty: "0.5", wantRelease: false},
		{state: "cancelled", filledQty: "0", wantRelease: true},
		{state: "cancelled", filledQty: "0.5", wantRelease: false},
	} {
		resolution := resolutionForOrder(attempt, broker.OrderResult{
			State: testCase.state, FilledQty: units.MustQty(testCase.filledQty),
		})
		if resolution.ReleaseReservation != testCase.wantRelease {
			t.Fatalf("state=%s filled=%s release=%v, want %v", testCase.state, testCase.filledQty, resolution.ReleaseReservation, testCase.wantRelease)
		}
	}
}

func TestClaimStealFencesLateWorkerAndBrokerDedupes(t *testing.T) {
	st := newMemoryStore()
	b := newFake("10000")
	setQuote(b, "FENCE", "9.90", "10", 1_000)
	if result, err := placeOrder(b, "FENCE", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed: result=%+v err=%v", result, err)
	}
	blocked := newFirstBlockingExecution(b)
	s := &server{
		limits: dualLedgerLimits(), broker: b, execution: blocked, store: st,
		brokerTimeout: time.Second, claimTimeout: 20 * time.Millisecond,
		attemptStale: time.Millisecond, providerDedupeVerified: true,
	}
	resultCh := make(chan map[string]any, 1)
	go func() {
		_, body := postOperation(t, s, `{"action":"close","symbol":"FENCE","qty":1}`)
		resultCh <- body
	}()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not reach broker")
	}
	st.mu.Lock()
	for id, attempt := range st.attempts {
		attempt.ClaimedAt = time.Now().Add(-time.Second)
		st.attempts[id] = attempt
	}
	st.mu.Unlock()
	if err := s.reconcileAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(blocked.release)
	body := <-resultCh
	if body["status"] != "executed" {
		t.Fatalf("late worker response=%v", body)
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 0 {
		t.Fatalf("positions=%v err=%v", positions, err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, attempt := range st.attempts {
		if attempt.Attempt != 2 || attempt.State != "settled" {
			t.Fatalf("attempt=%+v, want token=2 settled", attempt)
		}
	}
}

func TestReconcilerHandlesThreeCrashWindows(t *testing.T) {
	for _, crashPoint := range []string{"pending", "claimed", "accepted"} {
		t.Run(crashPoint, func(t *testing.T) {
			st := newMemoryStore()
			b := newFake("300")
			s := &server{
				limits: dualLedgerLimits(), broker: b, store: st,
				brokerTimeout: time.Second, claimTimeout: time.Millisecond,
				attemptStale: time.Millisecond, providerDedupeVerified: true,
			}
			quote, err := b.GetQuote("SPY")
			if err != nil {
				t.Fatal(err)
			}
			maxRisk := units.MustMicros("35")
			op := risk.Operation{
				Proposer: "crash-test", Action: "open", Kind: "option", Underlying: "SPY",
				Symbol: "SPY", Side: "buy", Qty: units.MustQty("1"), MaxRiskUSD: &maxRisk,
				Plan: map[string]string{"stop": "x", "invalidation": "x", "time_stop": "x", "target": "x"},
			}
			op = s.deriveOpenOperation(context.Background(), op, &quote)
			opID, attemptID, clientID := store.NewID(), store.NewID(), store.NewID()
			if err := st.InsertOperation(opID, op.Proposer, "B", "auto_approved", op, risk.Verdict{Class: "B"}, nil); err != nil {
				t.Fatal(err)
			}
			if err := st.InsertTradeGrant(store.TradeGrant{
				OperationID: opID, Ledger: "live", MarketDay: time.Now(),
				AuthorizedRisk: op.DerivedMaxRisk, RiskSource: "computed",
			}); err != nil {
				t.Fatal(err)
			}
			attempt := store.ExecutionAttempt{
				ID: attemptID, OperationID: opID, Seq: 1, Intent: "place", ClientOrderID: clientID,
				State: "pending", Qty: op.Qty, Limit: op.WorkingPrice, CreatedAt: time.Now().Add(-time.Second),
			}
			if crashPoint != "pending" {
				attempt.State = "claimed"
				attempt.Attempt = 1
				attempt.ClaimedAt = time.Now().Add(-time.Second)
			}
			if crashPoint == "accepted" {
				if _, err := b.PlaceLimitOrder(context.Background(), broker.PlaceRequest{
					ClientOrderID: clientID, Symbol: op.Symbol, Side: op.Side,
					Qty: op.Qty, Limit: op.WorkingPrice, Kind: op.Kind,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.InsertExecutionAttempt(attempt); err != nil {
				t.Fatal(err)
			}
			if err := s.reconcileAttempts(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, err := st.GetExecutionAttempt(attemptID)
			if err != nil || current.State != "placed" {
				t.Fatalf("attempt=%+v err=%v, want placed", current, err)
			}
			orders, err := b.OpenOrders(context.Background())
			if err != nil || len(orders) != 1 || orders[0].ClientOrderID != clientID {
				t.Fatalf("orders=%v err=%v", orders, err)
			}
		})
	}
}

func TestAttemptTimingConfigRejectsUnsafeLeases(t *testing.T) {
	t.Setenv("BROKER_TIMEOUT_MS", "100")
	t.Setenv("CLAIM_TIMEOUT_MS", "101")
	t.Setenv("ATTEMPT_STALE_MS", "10")
	config, err := loadAttemptConfig()
	if err != nil || config.brokerTimeout != 100*time.Millisecond || config.claimTimeout != 101*time.Millisecond {
		t.Fatalf("config=%+v err=%v", config, err)
	}
	for _, tc := range []struct{ broker, claim, stale string }{
		{"0", "101", "10"}, {"100", "100", "10"}, {"100", "99", "10"}, {"100", "101", "0"},
		{"9223372036854775807", "9223372036854775807", "10"},
	} {
		t.Setenv("BROKER_TIMEOUT_MS", tc.broker)
		t.Setenv("CLAIM_TIMEOUT_MS", tc.claim)
		t.Setenv("ATTEMPT_STALE_MS", tc.stale)
		if _, err := loadAttemptConfig(); err == nil {
			t.Fatalf("unsafe config broker=%s claim=%s stale=%s accepted", tc.broker, tc.claim, tc.stale)
		}
	}
	if lifetime, err := proposalLifetime(1800); err != nil || lifetime != 30*time.Minute {
		t.Fatalf("proposal lifetime=%s err=%v", lifetime, err)
	}
	for _, seconds := range []int{0, -1} {
		if _, err := proposalLifetime(seconds); err == nil {
			t.Fatalf("unsafe proposal ttl %d accepted", seconds)
		}
	}
}
