package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type candidateExecution struct {
	mu         sync.Mutex
	candidates []broker.OrderResult
	placeCalls int
	findCalls  int
	getCalls   int
	findErr    error
	getErr     error
}

type equityOnlyExecution struct{ broker.ExecutionProvider }

func (equityOnlyExecution) SupportsOrderKind(kind string) bool { return kind == "equity" }

func (c *candidateExecution) PlaceLimitOrder(context.Context, broker.PlaceRequest) (broker.OrderResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.placeCalls++
	return broker.OrderResult{}, &rhmcp.MutationError{Kind: rhmcp.ErrMutationOutcomeUnknown, Code: "call_failed"}
}

func (c *candidateExecution) CancelOrder(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{}, broker.ErrNotFound
}

func (c *candidateExecution) GetOrder(_ context.Context, brokerOrderID string) (broker.OrderResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	if c.getErr != nil {
		return broker.OrderResult{}, c.getErr
	}
	for _, candidate := range c.candidates {
		if candidate.BrokerOrderID == brokerOrderID {
			return candidate, nil
		}
	}
	return broker.OrderResult{}, broker.ErrNotFound
}

func (c *candidateExecution) FindExactPlaceCandidates(context.Context, broker.ExactPlaceCandidateQuery) ([]broker.OrderResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.findCalls++
	if c.findErr != nil {
		return nil, c.findErr
	}
	return append([]broker.OrderResult(nil), c.candidates...), nil
}

func (c *candidateExecution) setCandidates(candidates ...broker.OrderResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.candidates = append([]broker.OrderResult(nil), candidates...)
}

func (c *candidateExecution) setFindError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.findErr = err
}

func (c *candidateExecution) setGetError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getErr = err
}

func (c *candidateExecution) callCounts() (place, find, get int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.placeCalls, c.findCalls, c.getCalls
}

const m11Plan = `{"stop":"-30%","invalidation":"x","time_stop":"15:45 ET","target":"+50%"}`

func m11OpenPayload(symbol, risk string) string {
	return fmt.Sprintf(`{"proposer":"m11","action":"open","kind":"option","underlying":"%s","symbol":"%s","side":"buy","qty":1,"max_risk_usd":%s,"plan":%s}`,
		symbol, symbol, risk, m11Plan)
}

func m11Server(cap string) (*server, *memoryStore, *broker.Fake) {
	venue := newFake("300")
	setQuote(venue, "SPY", "0.34", "0.35", 45_000)
	limits := dualLedgerLimits()
	limits.HardLimits.MaxNewTradesPerDay = 100
	st := newMemoryStore()
	setMemoryKernelPolicy(st, limits)
	st.liveCanary.DailyAuthorizedRiskCapUSD = units.MustMicros(cap)
	st.liveCanary.CleanDaysBeforeRaise = 3
	return &server{
		mode: protectedMode(config.ModeLive), limits: limits,
		broker: venue, store: st,
	}, st, venue
}

func TestLiveCanaryBarrierGrantsExactlyOneRemainingAllowance(t *testing.T) {
	s, st, venue := m11Server("35")
	handler := s.routes()
	const requests = 20
	responses := make(chan string, requests)
	var start sync.WaitGroup
	start.Add(1)
	var workers sync.WaitGroup
	for i := 0; i < requests; i++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			start.Wait()
			response := routeRequestWithKey(handler, http.MethodPost, "/operations", m11OpenPayload("SPY", "35"),
				"runtime-secret", fmt.Sprintf("m11-canary-%d", index))
			responses <- response.Body.String()
		}(i)
	}
	start.Done()
	workers.Wait()
	close(responses)

	granted, refused := 0, 0
	for body := range responses {
		switch {
		case strings.Contains(body, `"class":"B"`):
			granted++
		case strings.Contains(body, `"live_canary_daily_risk_cap"`):
			refused++
		default:
			t.Fatalf("unexpected response: %s", body)
		}
	}
	if granted != 1 || refused != requests-1 {
		t.Fatalf("granted=%d refused=%d", granted, refused)
	}
	st.mu.Lock()
	grantCount := len(st.grants)
	var boundRevisionID int64
	for _, grant := range st.grants {
		boundRevisionID = grant.LiveCanaryRevisionID
	}
	st.mu.Unlock()
	if grantCount != 1 {
		t.Fatalf("grants=%d", grantCount)
	}
	if boundRevisionID != st.liveCanary.ID {
		t.Fatalf("grant revision=%d, want %d", boundRevisionID, st.liveCanary.ID)
	}
	if _, err := venue.GetOrder(context.Background(), "fake-1"); err != nil {
		t.Fatalf("one order missing: %v", err)
	}
	if _, err := venue.GetOrder(context.Background(), "fake-2"); err == nil {
		t.Fatal("canary overflow reached a second broker order")
	}
}

func TestHumanCandidateAdoptionRepullsAndClearsLiveLatch(t *testing.T) {
	s, st, venue := m11Server("35")
	setQuote(venue, "EQ", "9.99", "10", 0)
	venue.SetInstrument(broker.Instrument{
		Symbol: "EQ", InstrumentID: "55555555-5555-4555-8555-555555555555",
		Kind: "equity", Multiplier: 1, PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	execution := &candidateExecution{}
	s.account = venue
	s.market = marketdata.NewFakeProvider(venue)
	s.execution = execution
	handler := s.routes()
	payload := `{"proposer":"m11","action":"open","kind":"equity","underlying":"EQ","symbol":"EQ","side":"buy","qty":1,"max_risk_usd":10,"plan":` + m11Plan + `}`
	response := routeRequestWithKey(handler, http.MethodPost, "/operations", payload, "runtime-secret", "m11-candidate")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("propose status=%d body=%s", response.Code, response.Body.String())
	}
	st.mu.Lock()
	var attempt store.ExecutionAttempt
	for _, candidate := range st.attempts {
		attempt = candidate
	}
	st.mu.Unlock()
	if attempt.ID == "" || attempt.State != "unknown" || attempt.SentAt.IsZero() {
		t.Fatalf("attempt=%+v", attempt)
	}
	brokerOrderID := "77777777-7777-4777-8777-777777777777"
	execution.setCandidates(broker.OrderResult{
		BrokerOrderID: brokerOrderID, ClientOrderID: attempt.ClientOrderID, State: "submitted",
	})
	claimed, err := st.ClaimRecoverableAttemptLive(attempt.ID, "candidate-discovery", "unknown", attempt.Attempt, 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("candidate claim=%+v err=%v", claimed, err)
	}
	if err := s.reconcileLivePlaceAttempt(context.Background(), execution, claimed); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	if err != nil || current.State != "unknown" || current.CandidateBrokerOrderID != brokerOrderID {
		t.Fatalf("candidate attempt=%+v err=%v", current, err)
	}
	body := fmt.Sprintf(`{"confirm_attempt_id":%q,"confirm_broker_order_id":%q}`, attempt.ID, brokerOrderID)
	execution.setCandidates(
		broker.OrderResult{BrokerOrderID: brokerOrderID, ClientOrderID: attempt.ClientOrderID, State: "submitted"},
		broker.OrderResult{
			BrokerOrderID: "88888888-8888-4888-8888-888888888888", ClientOrderID: attempt.ClientOrderID, State: "submitted",
		},
	)
	ambiguous := routeRequest(handler, http.MethodPost, "/execution-attempts/"+attempt.ID+"/adopt-candidate", body, "admin-secret")
	if ambiguous.Code != http.StatusConflict {
		t.Fatalf("ambiguous status=%d body=%s", ambiguous.Code, ambiguous.Body.String())
	}
	current, _ = st.GetExecutionAttempt(attempt.ID)
	gate, _ := st.GetLiveExecutionGate()
	if current.State != "unknown" || gate.UnknownAttemptID != attempt.ID {
		t.Fatalf("ambiguous current=%+v gate=%+v", current, gate)
	}
	execution.setCandidates(broker.OrderResult{
		BrokerOrderID: brokerOrderID, ClientOrderID: attempt.ClientOrderID, State: "submitted",
	})
	adopted := routeRequest(handler, http.MethodPost, "/execution-attempts/"+attempt.ID+"/adopt-candidate", body, "admin-secret")
	if adopted.Code != http.StatusOK || !strings.Contains(adopted.Body.String(), `"attempt_state":"placed"`) {
		t.Fatalf("adopt status=%d body=%s", adopted.Code, adopted.Body.String())
	}
	current, _ = st.GetExecutionAttempt(attempt.ID)
	gate, _ = st.GetLiveExecutionGate()
	if current.State != "placed" || current.BrokerOrderID != brokerOrderID || gate.ActiveAttemptID != "" || gate.UnknownAttemptID != "" {
		t.Fatalf("current=%+v gate=%+v", current, gate)
	}
}

func m11CandidateAwaitingApproval(t *testing.T) (*server, *memoryStore, *candidateExecution, store.ExecutionAttempt, string) {
	t.Helper()
	s, st, venue := m11Server("35")
	setQuote(venue, "EQ", "9.99", "10", 0)
	venue.SetInstrument(broker.Instrument{
		Symbol: "EQ", InstrumentID: "55555555-5555-4555-8555-555555555555",
		Kind: "equity", Multiplier: 1, PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	execution := &candidateExecution{}
	s.account = venue
	s.market = marketdata.NewFakeProvider(venue)
	s.execution = execution
	payload := `{"proposer":"m11","action":"open","kind":"equity","underlying":"EQ","symbol":"EQ","side":"buy","qty":1,"max_risk_usd":10,"plan":` + m11Plan + `}`
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-fault-candidate")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("propose status=%d body=%s", response.Code, response.Body.String())
	}
	st.mu.Lock()
	var attempt store.ExecutionAttempt
	for _, candidate := range st.attempts {
		attempt = candidate
	}
	st.mu.Unlock()
	if attempt.ID == "" || attempt.State != "unknown" {
		t.Fatalf("attempt=%+v", attempt)
	}
	brokerOrderID := "77777777-7777-4777-8777-777777777777"
	execution.setCandidates(broker.OrderResult{
		BrokerOrderID: brokerOrderID, ClientOrderID: attempt.ClientOrderID, State: "submitted",
	})
	claimed, err := st.ClaimRecoverableAttemptLive(attempt.ID, "candidate-discovery", "unknown", attempt.Attempt, 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("candidate claim=%+v err=%v", claimed, err)
	}
	if err := s.reconcileLivePlaceAttempt(context.Background(), execution, claimed); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	if err != nil || current.State != "unknown" || current.CandidateBrokerOrderID != brokerOrderID {
		t.Fatalf("candidate attempt=%+v err=%v", current, err)
	}
	body := fmt.Sprintf(`{"confirm_attempt_id":%q,"confirm_broker_order_id":%q}`, current.ID, brokerOrderID)
	return s, st, execution, *current, body
}

func TestHumanCandidateAdoptionFaultsKeepLiveLatch(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*candidateExecution, store.ExecutionAttempt)
		wantStatus int
		wantCode   string
	}{
		{
			name: "candidate query fails",
			configure: func(execution *candidateExecution, _ store.ExecutionAttempt) {
				execution.setFindError(fmt.Errorf("injected query failure"))
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   "candidate_query_failed",
		},
		{
			name: "candidate disappears",
			configure: func(execution *candidateExecution, _ store.ExecutionAttempt) {
				execution.setCandidates()
			},
			wantStatus: http.StatusConflict,
			wantCode:   "candidate_zero",
		},
		{
			name: "candidate becomes ambiguous",
			configure: func(execution *candidateExecution, attempt store.ExecutionAttempt) {
				execution.setCandidates(
					broker.OrderResult{BrokerOrderID: attempt.CandidateBrokerOrderID, State: "submitted"},
					broker.OrderResult{BrokerOrderID: "88888888-8888-4888-8888-888888888888", State: "submitted"},
				)
			},
			wantStatus: http.StatusConflict,
			wantCode:   "candidate_ambiguous",
		},
		{
			name: "unique candidate identity changes",
			configure: func(execution *candidateExecution, _ store.ExecutionAttempt) {
				execution.setCandidates(broker.OrderResult{
					BrokerOrderID: "88888888-8888-4888-8888-888888888888", State: "submitted",
				})
			},
			wantStatus: http.StatusConflict,
			wantCode:   "candidate_mismatch",
		},
		{
			name: "canonical order query fails",
			configure: func(execution *candidateExecution, _ store.ExecutionAttempt) {
				execution.setGetError(fmt.Errorf("injected canonical query failure"))
			},
			wantStatus: http.StatusBadGateway,
			wantCode:   "candidate_order_unavailable",
		},
		{
			name: "canonical order state is unknown",
			configure: func(execution *candidateExecution, attempt store.ExecutionAttempt) {
				execution.setCandidates(broker.OrderResult{
					BrokerOrderID: attempt.CandidateBrokerOrderID, State: "provider_added_state",
				})
			},
			wantStatus: http.StatusConflict,
			wantCode:   "candidate_state_unknown",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s, st, execution, attempt, body := m11CandidateAwaitingApproval(t)
			test.configure(execution, attempt)
			response := routeRequest(s.routes(), http.MethodPost,
				"/execution-attempts/"+attempt.ID+"/adopt-candidate", body, "admin-secret")
			if response.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			current, err := st.GetExecutionAttempt(attempt.ID)
			gate, gateErr := st.GetLiveExecutionGate()
			if err != nil || gateErr != nil || current.State != "unknown" ||
				current.ProviderErrorCode != test.wantCode || gate.UnknownAttemptID != attempt.ID ||
				gate.ActiveAttemptID != "" {
				t.Fatalf("attempt=%+v gate=%+v err=%v gate_err=%v", current, gate, err, gateErr)
			}
			st.mu.Lock()
			reservation := st.openReservations[attempt.OpenReservationID]
			grantCount := len(st.grants)
			st.mu.Unlock()
			if reservation.ResourceState != "held" || grantCount != 1 {
				t.Fatalf("reservation=%+v grants=%d", reservation, grantCount)
			}
		})
	}
}

func TestConcurrentHumanCandidateAdoptionHasOneWinner(t *testing.T) {
	s, st, execution, attempt, body := m11CandidateAwaitingApproval(t)
	handler := s.routes()
	const workers = 20
	responses := make(chan int, workers)
	var start sync.WaitGroup
	start.Add(1)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			start.Wait()
			response := routeRequest(handler, http.MethodPost,
				"/execution-attempts/"+attempt.ID+"/adopt-candidate", body, "admin-secret")
			responses <- response.Code
		}()
	}
	start.Done()
	wait.Wait()
	close(responses)
	winners, conflicts := 0, 0
	for status := range responses {
		switch status {
		case http.StatusOK:
			winners++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status=%d", status)
		}
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	gate, gateErr := st.GetLiveExecutionGate()
	_, findCalls, getCalls := execution.callCounts()
	if winners != 1 || conflicts != workers-1 || err != nil || gateErr != nil ||
		current.State != "placed" || gate.ActiveAttemptID != "" || gate.UnknownAttemptID != "" ||
		findCalls != 2 || getCalls != 1 {
		t.Fatalf("winners=%d conflicts=%d attempt=%+v gate=%+v find=%d get=%d err=%v gate_err=%v",
			winners, conflicts, current, gate, findCalls, getCalls, err, gateErr)
	}
}

func TestUnknownRecoveryClaimSurvivesWorkerCrash(t *testing.T) {
	_, st, _, attempt, _ := m11CandidateAwaitingApproval(t)
	claimed, err := st.ClaimRecoverableAttemptLive(
		attempt.ID, "first-recovery-worker", "unknown", attempt.Attempt, time.Nanosecond,
	)
	if err != nil || claimed == nil {
		t.Fatalf("initial recovery claim=%+v err=%v", claimed, err)
	}
	time.Sleep(time.Millisecond)
	reclaimed, err := st.ClaimRecoverableAttemptLive(
		claimed.ID, "replacement-recovery-worker", "claimed", claimed.Attempt, 30*time.Second,
	)
	gate, gateErr := st.GetLiveExecutionGate()
	if err != nil || reclaimed == nil || reclaimed.Attempt != claimed.Attempt+1 || gateErr != nil ||
		gate.UnknownAttemptID != attempt.ID || gate.ActiveAttemptID != "" {
		t.Fatalf("reclaimed=%+v gate=%+v err=%v gate_err=%v", reclaimed, gate, err, gateErr)
	}
}

func TestLiveUnknownPullsThenReplaysSameRefOnlyOnce(t *testing.T) {
	s, st, venue := m11Server("35")
	setQuote(venue, "EQ", "9.99", "10", 0)
	venue.SetInstrument(broker.Instrument{
		Symbol: "EQ", InstrumentID: "55555555-5555-4555-8555-555555555555",
		Kind: "equity", Multiplier: 1, PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	execution := &candidateExecution{}
	s.account = venue
	s.market = marketdata.NewFakeProvider(venue)
	s.execution = execution
	s.providerDedupeVerified = true
	s.providerReplayWindowBoundVerified = true
	payload := `{"proposer":"m11","action":"open","kind":"equity","underlying":"EQ","symbol":"EQ","side":"buy","qty":1,"max_risk_usd":10,"plan":` + m11Plan + `}`
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-one-replay")
	placeCalls, _, _ := execution.callCounts()
	if response.Code != http.StatusBadGateway || placeCalls != 1 {
		t.Fatalf("initial status=%d calls=%d body=%s", response.Code, placeCalls, response.Body.String())
	}
	st.mu.Lock()
	var attempt store.ExecutionAttempt
	for _, candidate := range st.attempts {
		attempt = candidate
	}
	st.mu.Unlock()
	for recovery := 0; recovery < 2; recovery++ {
		current, err := st.GetExecutionAttempt(attempt.ID)
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := st.ClaimRecoverableAttemptLive(current.ID, "recovery", "unknown", current.Attempt, 30*time.Second)
		if err != nil || claimed == nil {
			t.Fatalf("recovery %d claim=%+v err=%v", recovery, claimed, err)
		}
		_ = s.reconcileLivePlaceAttempt(context.Background(), execution, claimed)
	}
	current, _ := st.GetExecutionAttempt(attempt.ID)
	placeCalls, _, _ = execution.callCounts()
	if placeCalls != 2 || current.ReplayCount != 1 || current.State != "unknown" {
		t.Fatalf("calls=%d attempt=%+v", placeCalls, current)
	}
}

func m11UnknownReplayFixture(t *testing.T) (*server, *memoryStore, *candidateExecution, store.ExecutionAttempt) {
	t.Helper()
	s, st, venue := m11Server("35")
	setQuote(venue, "EQ", "9.99", "10", 0)
	venue.SetInstrument(broker.Instrument{
		Symbol: "EQ", InstrumentID: "55555555-5555-4555-8555-555555555555",
		Kind: "equity", Multiplier: 1, PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	execution := &candidateExecution{}
	s.account = venue
	s.market = marketdata.NewFakeProvider(venue)
	s.execution = execution
	s.providerDedupeVerified = true
	s.providerReplayWindowBoundVerified = true
	payload := `{"proposer":"m11","action":"open","kind":"equity","underlying":"EQ","symbol":"EQ","side":"buy","qty":1,"max_risk_usd":10,"plan":` + m11Plan + `}`
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-v17-unknown")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("initial status=%d body=%s", response.Code, response.Body.String())
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, attempt := range st.attempts {
		if attempt.State == "unknown" {
			return s, st, execution, attempt
		}
	}
	t.Fatal("unknown attempt missing")
	return nil, nil, nil, store.ExecutionAttempt{}
}

func TestSameRefReplayStopsWhenCandidateAppearsInPreEffectSnapshot(t *testing.T) {
	s, st, execution, attempt := m11UnknownReplayFixture(t)
	venue, ok := s.accountProvider().(*broker.Fake)
	if !ok {
		t.Fatal("fixture account provider is not FakeBroker")
	}
	result, err := venue.PlaceLimitOrder(context.Background(), broker.PlaceRequest{
		ClientOrderID: attempt.ClientOrderID, Symbol: "EQ", Side: "buy", PositionEffect: "open",
		Qty: units.MustQty("1"), Limit: units.MustMicros("1"), Kind: "equity",
	})
	if err != nil || result.State != "submitted" {
		t.Fatalf("seed late Provider candidate: result=%+v err=%v", result, err)
	}
	claimed, err := st.ClaimRecoverableAttemptLive(
		attempt.ID, "candidate-race-recovery", "unknown", attempt.Attempt, 30*time.Second,
	)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := s.reconcileLivePlaceAttempt(context.Background(), execution, claimed); !errors.Is(err, errPreEffectRejected) {
		t.Fatalf("reconcile error=%v", err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	gate, gateErr := st.GetLiveExecutionGate()
	placeCalls, findCalls, _ := execution.callCounts()
	if err != nil || gateErr != nil || placeCalls != 1 || findCalls != 1 || current.State != "unknown" ||
		current.ReplayCount != 0 || current.ProviderErrorCode != "pre_effect_rejected" ||
		gate.UnknownAttemptID != attempt.ID {
		t.Fatalf("attempt=%+v gate=%+v calls=%d/%d err=%v gate_err=%v",
			current, gate, placeCalls, findCalls, err, gateErr)
	}
}

func TestSameRefReplayExpiresWithoutProviderEffect(t *testing.T) {
	s, st, execution, attempt := m11UnknownReplayFixture(t)
	st.mu.Lock()
	attempt.SendWindowEnd = time.Now().UTC().Add(-time.Second)
	st.attempts[attempt.ID] = attempt
	st.mu.Unlock()
	claimed, err := st.ClaimRecoverableAttemptLive(attempt.ID, "expired-recovery", "unknown", attempt.Attempt, 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := s.reconcileLivePlaceAttempt(context.Background(), execution, claimed); !errors.Is(err, store.ErrReplayWindowExpired) {
		t.Fatalf("reconcile error=%v", err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	gate, gateErr := st.GetLiveExecutionGate()
	placeCalls, findCalls, _ := execution.callCounts()
	if err != nil || gateErr != nil || placeCalls != 1 || findCalls != 1 || current.State != "unknown" ||
		current.ReplayCount != 0 || current.ProviderErrorCode != "replay_window_expired" ||
		gate.UnknownAttemptID != attempt.ID {
		t.Fatalf("attempt=%+v gate=%+v calls=%d/%d err=%v gate_err=%v",
			current, gate, placeCalls, findCalls, err, gateErr)
	}
}

func TestSameRefReplayStopsAtGlobalHaltWithoutProviderEffect(t *testing.T) {
	s, st, execution, attempt := m11UnknownReplayFixture(t)
	if _, err := st.ActivateGlobalHalt("operator stop", "admin:test", config.ModeLive); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimRecoverableAttemptLive(attempt.ID, "halted-recovery", "unknown", attempt.Attempt, 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := s.reconcileLivePlaceAttempt(context.Background(), execution, claimed); !errors.Is(err, store.ErrLiveSendHalted) {
		t.Fatalf("reconcile error=%v", err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	gate, gateErr := st.GetLiveExecutionGate()
	placeCalls, findCalls, _ := execution.callCounts()
	if err != nil || gateErr != nil || placeCalls != 1 || findCalls != 1 || current.State != "unknown" ||
		current.ReplayCount != 0 || current.ProviderErrorCode != "replay_suppressed_halt" ||
		gate.UnknownAttemptID != attempt.ID {
		t.Fatalf("attempt=%+v gate=%+v calls=%d/%d err=%v gate_err=%v",
			current, gate, placeCalls, findCalls, err, gateErr)
	}
}

func TestProviderWithoutCreatedAtBoundDoesNotAutoReplay(t *testing.T) {
	s, st, execution, attempt := m11UnknownReplayFixture(t)
	s.providerReplayWindowBoundVerified = false
	claimed, err := st.ClaimRecoverableAttemptLive(attempt.ID, "unbounded-provider-recovery", "unknown", attempt.Attempt, 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	if err := s.reconcileLivePlaceAttempt(context.Background(), execution, claimed); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	gate, gateErr := st.GetLiveExecutionGate()
	placeCalls, findCalls, _ := execution.callCounts()
	if err != nil || gateErr != nil || placeCalls != 1 || findCalls != 1 || current.State != "unknown" ||
		current.ReplayCount != 0 || current.ProviderErrorCode != "candidate_zero" || gate.UnknownAttemptID != attempt.ID {
		t.Fatalf("attempt=%+v gate=%+v calls=%d/%d err=%v gate_err=%v",
			current, gate, placeCalls, findCalls, err, gateErr)
	}
}

type m11EntityCounts struct {
	operations, grants, closeReservations, openReservations, attempts, orders int
}

func m11Counts(st *memoryStore) m11EntityCounts {
	st.mu.Lock()
	defer st.mu.Unlock()
	return m11EntityCounts{
		operations: len(st.operationRows), grants: len(st.grants), closeReservations: len(st.reservations),
		openReservations: len(st.openReservations), attempts: len(st.attempts), orders: len(st.orders),
	}
}

func TestOccupiedLiveGateRefusesNewEffectsBeforeEntitlements(t *testing.T) {
	for _, test := range []struct {
		name, activeID, unknownID, want string
	}{
		{name: "active", activeID: store.NewID(), want: "live_execution_busy"},
		{name: "unknown", unknownID: store.NewID(), want: "live_execution_suspended"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, st, _ := m11Server("1000")
			st.liveActiveAttemptID, st.liveUnknownAttemptID = test.activeID, test.unknownID
			before := m11Counts(st)
			const workers = 20
			responses := make(chan *httptest.ResponseRecorder, workers)
			var start sync.WaitGroup
			start.Add(1)
			var wait sync.WaitGroup
			for index := range workers {
				wait.Add(1)
				go func() {
					defer wait.Done()
					start.Wait()
					responses <- routeRequestWithKey(s.routes(), http.MethodPost, "/operations", m11OpenPayload("SPY", "35"),
						"runtime-secret", fmt.Sprintf("m11-%s-%d", test.name, index))
				}()
			}
			start.Done()
			wait.Wait()
			close(responses)
			for response := range responses {
				if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), test.want) {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
			}
			if after := m11Counts(st); after != before {
				t.Fatalf("before=%+v after=%+v", before, after)
			}
			st.mu.Lock()
			st.liveActiveAttemptID, st.liveUnknownAttemptID = "", ""
			st.mu.Unlock()
			retry := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", m11OpenPayload("SPY", "35"),
				"runtime-secret", fmt.Sprintf("m11-%s-%d", test.name, 0))
			if retry.Code != http.StatusOK || !strings.Contains(retry.Body.String(), `"class":"B"`) {
				t.Fatalf("retry status=%d body=%s", retry.Code, retry.Body.String())
			}
		})
	}
}

func TestOccupiedLiveGateKeepsClassCPendingAndIdempotencyFirst(t *testing.T) {
	s, st, venue := m11Server("1000")
	setQuote(venue, "SPY", "1.99", "2.00", 45_000)
	handler := s.routes()
	payload := m11OpenPayload("SPY", "200")
	proposal := routeRequestWithKey(handler, http.MethodPost, "/operations", payload, "runtime-secret", "m11-busy-class-c")
	if proposal.Code != http.StatusOK || !strings.Contains(proposal.Body.String(), `"status":"pending_review"`) {
		t.Fatalf("proposal status=%d body=%s", proposal.Code, proposal.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(proposal.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	operationID, _ := body["operation_id"].(string)
	st.mu.Lock()
	st.liveActiveAttemptID = store.NewID()
	st.mu.Unlock()
	replay := routeRequestWithKey(handler, http.MethodPost, "/operations", payload, "runtime-secret", "m11-busy-class-c")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"idempotent_replay":true`) ||
		!strings.Contains(replay.Body.String(), operationID) {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	before := m11Counts(st)
	review := routeRequest(handler, http.MethodPost, "/operations/"+operationID+"/review",
		`{"verdict":"approved","rationale":"busy gate test"}`, "admin-secret")
	if review.Code != http.StatusConflict || !strings.Contains(review.Body.String(), "live_execution_busy") {
		t.Fatalf("review status=%d body=%s", review.Code, review.Body.String())
	}
	row, err := st.GetOperation(operationID)
	if err != nil || row.Status != "pending_review" {
		t.Fatalf("operation=%+v err=%v", row, err)
	}
	if after := m11Counts(st); after != before {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}

func TestOccupiedLiveGateDoesNotBlockShadow(t *testing.T) {
	s, st, _ := m11Server("1000")
	unknownID := store.NewID()
	st.liveUnknownAttemptID = unknownID
	payload := strings.Replace(m11OpenPayload("SPY", "35"), `"plan":`, `"shadow":true,"plan":`, 1)
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-busy-shadow")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"class":"B"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	gate, err := st.GetLiveExecutionGate()
	if err != nil || gate.UnknownAttemptID != unknownID || gate.ActiveAttemptID != "" {
		t.Fatalf("gate=%+v err=%v", gate, err)
	}
	counts := m11Counts(st)
	if counts.grants != 1 || counts.attempts != 1 || counts.orders != 1 {
		t.Fatalf("shadow entities=%+v", counts)
	}
}

func m11StagePendingEquityOpen(t *testing.T) (*server, *memoryStore, *candidateExecution, risk.Operation, *store.ExecutionAttempt) {
	t.Helper()
	s, st, venue := m11Server("1000")
	setQuote(venue, "EQ", "9.99", "10", 0)
	venue.SetInstrument(broker.Instrument{
		Symbol: "EQ", InstrumentID: "55555555-5555-4555-8555-555555555555",
		Kind: "equity", Multiplier: 1, PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	execution := &candidateExecution{}
	s.account = venue
	s.market = marketdata.NewFakeProvider(venue)
	s.execution = execution
	op := risk.Operation{
		Proposer: "m11-v17", Action: "open", Kind: "equity", Underlying: "EQ", Symbol: "EQ",
		InstrumentID: "55555555-5555-4555-8555-555555555555", Side: "buy",
		Qty: units.MustQty("1"), QtyIncrement: units.MustQty("1"), Multiplier: 1,
		WorkingPrice: units.MustMicros("10"), ApprovedPriceCap: units.MustMicros("10"),
		DerivedMaxRisk: units.MustMicros("10"), RequiredCash: units.MustMicros("10"),
		Plan: map[string]string{"stop": "x", "invalidation": "x", "time_stop": "x", "target": "x"},
	}
	operationID := store.NewID()
	if err := st.InsertOperation(operationID, op.Proposer, "B", "auto_approved", op, risk.Verdict{Class: "B"}, nil); err != nil {
		t.Fatal(err)
	}
	var attempt *store.ExecutionAttempt
	window, err := marketDayWindow(time.Now().UTC(), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WithLedgerLock(false, func(gate store.OperationGate) error {
		var stageErr error
		attempt, stageErr = s.stageApprovedOpen(gate, operationID, op, window.day, st.liveCanary)
		return stageErr
	}); err != nil {
		t.Fatal(err)
	}
	return s, st, execution, op, attempt
}

func TestHaltBeforeSentMarkerTerminatesUnsentOpen(t *testing.T) {
	s, st, execution, _, attempt := m11StagePendingEquityOpen(t)
	if _, err := st.ActivateGlobalHalt("halt before mark", "admin:test", config.ModeLive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.executePendingAttempt(context.Background(), attempt.ID); !errors.Is(err, store.ErrLiveSendHalted) {
		t.Fatalf("execute err=%v", err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	gate, gateErr := st.GetLiveExecutionGate()
	order, orderErr := st.GetOrderByAttempt(attempt.ID)
	reservation, reservationErr := st.GetOpenReservation(attempt.OpenReservationID)
	placeCalls, _, _ := execution.callCounts()
	row, rowErr := st.GetOperation(attempt.OperationID)
	if err != nil || gateErr != nil || orderErr != nil || reservationErr != nil || rowErr != nil ||
		placeCalls != 0 || current.State != "failed" || current.ProviderErrorCode != "send_suppressed_halt" ||
		order.State != "rejected" || reservation.ResourceState != "released" || row.Status != "failed" ||
		gate.ActiveAttemptID != "" || gate.UnknownAttemptID != "" {
		t.Fatalf("attempt=%+v order=%+v reservation=%+v operation=%+v gate=%+v calls=%d errors=%v/%v/%v/%v/%v",
			current, order, reservation, row, gate, placeCalls, err, orderErr, reservationErr, rowErr, gateErr)
	}
	if counts := m11Counts(st); counts.grants != 1 {
		t.Fatalf("halt incorrectly restored the consumed grant: %+v", counts)
	}
}

func TestHaltBeforeReplacementSendPreservesExecutedOperation(t *testing.T) {
	s, st, execution, _, attempt := m11StagePendingEquityOpen(t)
	st.mu.Lock()
	replacement := st.attempts[attempt.ID]
	replacement.Seq = 2
	st.attempts[attempt.ID] = replacement
	priorOrder := store.Order{
		ID: store.NewID(), OperationID: attempt.OperationID,
		ExecutionAttemptID: store.NewID(), State: "partially_filled",
	}
	st.orders[priorOrder.ExecutionAttemptID] = priorOrder
	st.fills["prior-fill"] = memoryFill{
		orderID: priorOrder.ID, ledger: "live",
		fill: store.FillInput{
			BrokerFillID: "prior-fill", Qty: units.MustQty("1"),
			Price: units.MustMicros("10"), TS: time.Now().UTC(),
		},
	}
	st.mu.Unlock()
	if _, err := st.ActivateGlobalHalt("halt before replacement", "admin:test", config.ModeLive); err != nil {
		t.Fatal(err)
	}
	if _, err := s.executePendingAttempt(context.Background(), attempt.ID); !errors.Is(err, store.ErrLiveSendHalted) {
		t.Fatalf("execute err=%v", err)
	}
	row, err := st.GetOperation(attempt.OperationID)
	current, attemptErr := st.GetExecutionAttempt(attempt.ID)
	order, orderErr := st.GetOrderByAttempt(attempt.ID)
	placeCalls, _, _ := execution.callCounts()
	if err != nil || attemptErr != nil || orderErr != nil || row.Status != "executed" ||
		current.State != "failed" || order.State != "rejected" || placeCalls != 0 {
		t.Fatalf("operation=%+v attempt=%+v order=%+v calls=%d errors=%v/%v/%v",
			row, current, order, placeCalls, err, attemptErr, orderErr)
	}
}

type staleHaltCleanupStore struct{ storeAPI }

func (s *staleHaltCleanupStore) ResolveAttempt(string, int, store.AttemptResolution) (bool, error) {
	return false, nil
}

func TestHaltCleanupLostFencingDoesNotClaimDurableFailure(t *testing.T) {
	s, st, execution, _, attempt := m11StagePendingEquityOpen(t)
	s.store = &staleHaltCleanupStore{storeAPI: st}
	if _, err := st.ActivateGlobalHalt("halt before stale cleanup", "admin:test", config.ModeLive); err != nil {
		t.Fatal(err)
	}
	_, err := s.executePendingAttempt(context.Background(), attempt.ID)
	if err == nil || !strings.Contains(err.Error(), "cleanup lost fencing") || errors.Is(err, errBrokerMutationFailed) {
		t.Fatalf("err=%v, want explicit lost-fencing error without durable-failure claim", err)
	}
	current, attemptErr := st.GetExecutionAttempt(attempt.ID)
	order, orderErr := st.GetOrderByAttempt(attempt.ID)
	reservation, reservationErr := st.GetOpenReservation(attempt.OpenReservationID)
	gate, gateErr := st.GetLiveExecutionGate()
	placeCalls, _, _ := execution.callCounts()
	if attemptErr != nil || orderErr != nil || reservationErr != nil || gateErr != nil ||
		current.State != "claimed" || order.State != "new" || reservation.ResourceState != "held" ||
		gate.ActiveAttemptID != attempt.ID || placeCalls != 0 {
		t.Fatalf("attempt=%+v order=%+v reservation=%+v gate=%+v calls=%d errors=%v/%v/%v/%v",
			current, order, reservation, gate, placeCalls, attemptErr, orderErr, reservationErr, gateErr)
	}
}

func TestHaltReportsPreCutSentAttempt(t *testing.T) {
	s, st, _, _, attempt := m11StagePendingEquityOpen(t)
	claimed, err := st.ClaimPendingAttemptLive(attempt.ID, "pre-cut-worker", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	st.mu.Lock()
	prepared := st.attempts[claimed.ID]
	prepared.ProviderAccountID = s.mode.LiveAccountID
	prepared.ProviderIntent = json.RawMessage(`{"kind":"equity"}`)
	prepared.IntentFingerprint = make([]byte, sha256.Size)
	st.attempts[claimed.ID] = prepared
	st.mu.Unlock()
	marked, err := st.MarkAttemptSent(claimed.ID, claimed.Attempt, false, 0, nil)
	if err != nil || !marked {
		t.Fatalf("marked=%v err=%v", marked, err)
	}
	response := routeRequest(s.routes(), http.MethodPost, "/halt", `{"reason":"pre-cut report"}`, "admin-secret")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), claimed.ID) ||
		!strings.Contains(response.Body.String(), `"in_flight_attempt_state":"active"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHaltCommitsWhilePreCutProviderCallIsBlocked(t *testing.T) {
	s, _, venue := m11Server("1000")
	blocked := newFirstBlockingExecution(venue)
	s.account = venue
	s.market = marketdata.NewFakeProvider(venue)
	s.execution = blocked
	handler := s.routes()
	proposalDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		proposalDone <- routeRequestWithKey(handler, http.MethodPost, "/operations", m11OpenPayload("SPY", "35"),
			"runtime-secret", "m11-pre-cut-blocked-provider")
	}()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("provider call did not start")
	}
	haltDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		haltDone <- routeRequest(handler, http.MethodPost, "/halt", `{"reason":"blocked provider cut"}`, "admin-secret")
	}()
	select {
	case response := <-haltDone:
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"in_flight_attempt_state":"active"`) {
			t.Fatalf("halt status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("halt waited for the Provider network call")
	}
	close(blocked.release)
	select {
	case response := <-proposalDone:
		if response.Code != http.StatusOK {
			t.Fatalf("proposal status=%d body=%s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("proposal did not finish after Provider release")
	}
}

func TestRepeatedHaltReturnsOriginalCut(t *testing.T) {
	s, _, _ := m11Server("1000")
	handler := s.routes()
	first := routeRequest(handler, http.MethodPost, "/halt", `{"reason":"first cut"}`, "admin-secret")
	second := routeRequest(handler, http.MethodPost, "/halt", `{"reason":"replacement reason"}`, "admin-secret")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	var firstBody, secondBody map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatal(err)
	}
	if firstBody["event_id"] != secondBody["event_id"] || firstBody["cut_at"] != secondBody["cut_at"] ||
		secondBody["reason"] != "first cut" {
		t.Fatalf("first=%v second=%v", firstBody, secondBody)
	}
}

type failingExecution struct{}

func (failingExecution) PlaceLimitOrder(context.Context, broker.PlaceRequest) (broker.OrderResult, error) {
	return broker.OrderResult{}, fmt.Errorf("injected placement failure")
}
func (failingExecution) CancelOrder(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{}, broker.ErrNotFound
}
func (failingExecution) GetOrder(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{}, broker.ErrNotFound
}
func (failingExecution) FindOrderByClientID(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{}, broker.ErrNotFound
}

func TestFailedAttemptDoesNotRestoreCanaryAllowance(t *testing.T) {
	s, st, venue := m11Server("35")
	s.account = venue
	s.market = marketdata.NewFakeProvider(venue)
	s.execution = failingExecution{}
	handler := s.routes()

	first := routeRequestWithKey(handler, http.MethodPost, "/operations", m11OpenPayload("SPY", "35"), "runtime-secret", "m11-failed-first")
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := routeRequestWithKey(handler, http.MethodPost, "/operations", m11OpenPayload("SPY", "35"), "runtime-secret", "m11-failed-second")
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), canaryCapReason) {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.grants) != 1 {
		t.Fatalf("failed attempt restored canary allowance: grants=%d", len(st.grants))
	}
}

func TestLegacyUnknownGrantFailsCanaryClosed(t *testing.T) {
	s, st, _ := m11Server("35")
	window, err := marketDayWindow(time.Now().UTC(), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	st.grants["legacy"] = store.TradeGrant{
		OperationID: "legacy", Ledger: "live", MarketDay: window.day, RiskSource: "legacy_unknown",
	}
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", m11OpenPayload("SPY", "35"), "runtime-secret", "m11-legacy")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), canaryLegacyReason) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUnboundComputedGrantFailsCanaryClosed(t *testing.T) {
	s, st, _ := m11Server("1000")
	window, err := marketDayWindow(time.Now().UTC(), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	st.grants["unbound"] = store.TradeGrant{
		OperationID: "unbound", Ledger: "live", MarketDay: window.day,
		AuthorizedRisk: units.MustMicros("1"), RiskSource: "computed",
	}
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", m11OpenPayload("SPY", "35"), "runtime-secret", "m11-unbound")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), canaryUnboundReason) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUnavailableCanaryAuthorityFailsBeforeAnyLiveEntitlement(t *testing.T) {
	for _, authorityErr := range []error{
		store.ErrLiveCanaryAuthorityMissing,
		store.ErrLiveCanaryAuthorityInvalid,
	} {
		s, st, venue := m11Server("1000")
		st.liveCanaryErr = authorityErr
		response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations",
			m11OpenPayload("SPY", "35"), "runtime-secret", "m11-authority-unavailable")
		if response.Code != http.StatusServiceUnavailable ||
			!strings.Contains(response.Body.String(), "live canary authority unavailable") {
			t.Fatalf("err=%v status=%d body=%s", authorityErr, response.Code, response.Body.String())
		}
		if counts := m11Counts(st); counts.grants != 0 || counts.openReservations != 0 ||
			counts.attempts != 0 || counts.orders != 0 {
			t.Fatalf("err=%v created entitlements: %+v", authorityErr, counts)
		}
		if _, err := venue.GetOrder(context.Background(), "fake-1"); err == nil {
			t.Fatalf("err=%v reached broker", authorityErr)
		}
	}
}

func TestLiveCanaryDoesNotConsumeOrBlockShadowLedger(t *testing.T) {
	s, st, _ := m11Server("35")
	window, err := marketDayWindow(time.Now().UTC(), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	st.grants["live-full"] = store.TradeGrant{
		OperationID: "live-full", Ledger: "live", MarketDay: window.day,
		AuthorizedRisk: units.MustMicros("35"), RiskSource: "computed",
		LiveCanaryRevisionID: st.liveCanary.ID,
	}
	payload := strings.Replace(m11OpenPayload("SPY", "35"), `"plan":`, `"shadow":true,"plan":`, 1)
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-shadow")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"class":"B"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	shadowGrants := 0
	for _, grant := range st.grants {
		if grant.Ledger == "shadow" {
			shadowGrants++
		}
	}
	if shadowGrants != 1 {
		t.Fatalf("shadow grants=%d, want 1", shadowGrants)
	}
}

func TestFirstLiveCanaryGrantMustUseOneProviderQuantityIncrement(t *testing.T) {
	s, st, venue := m11Server("1000")
	payload := strings.Replace(m11OpenPayload("SPY", "70"), `"qty":1`, `"qty":2`, 1)
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-first-size")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), canaryFirstSizeReason) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	st.mu.Lock()
	grantCount := len(st.grants)
	st.mu.Unlock()
	if grantCount != 0 {
		t.Fatalf("oversized first canary created %d grants", grantCount)
	}
	if _, err := venue.GetOrder(context.Background(), "fake-1"); err == nil {
		t.Fatal("oversized first canary reached broker")
	}
}

func TestLiveEquityFailsBeforeGrantWithoutExactProviderIncrement(t *testing.T) {
	s, st, venue := m11Server("1000")
	setQuote(venue, "EQ", "9.99", "10", 0)
	payload := `{"proposer":"m11","action":"open","kind":"equity","underlying":"EQ","symbol":"EQ","side":"buy","qty":1,"max_risk_usd":10,"plan":` + m11Plan + `}`
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-equity-increment")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"class":"REJECT"`) ||
		!strings.Contains(response.Body.String(), "unsupported_contract") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.grants) != 0 {
		t.Fatalf("unsupported equity created grants=%d", len(st.grants))
	}
}

func TestClassCApprovalRechecksCanaryAndStaysPending(t *testing.T) {
	s, st, venue := m11Server("35")
	setQuote(venue, "SPY", "1.99", "2.00", 45_000)
	handler := s.routes()
	proposal := routeRequestWithKey(handler, http.MethodPost, "/operations", m11OpenPayload("SPY", "200"), "runtime-secret", "m11-class-c")
	if proposal.Code != http.StatusOK || !strings.Contains(proposal.Body.String(), `"status":"pending_review"`) {
		t.Fatalf("proposal status=%d body=%s", proposal.Code, proposal.Body.String())
	}
	var proposalBody map[string]any
	if err := json.Unmarshal(proposal.Body.Bytes(), &proposalBody); err != nil {
		t.Fatal(err)
	}
	operationID, _ := proposalBody["operation_id"].(string)
	if operationID == "" {
		t.Fatalf("operation id missing: %v", proposalBody)
	}
	window, err := marketDayWindow(time.Now().UTC(), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	st.grants["existing"] = store.TradeGrant{
		OperationID: "existing", Ledger: "live", MarketDay: window.day,
		AuthorizedRisk: units.MustMicros("35"), RiskSource: "computed",
		LiveCanaryRevisionID: st.liveCanary.ID,
	}
	st.mu.Unlock()

	review := routeRequest(handler, http.MethodPost, "/operations/"+operationID+"/review",
		`{"verdict":"approved","rationale":"canary test"}`, "admin-secret")
	if review.Code != http.StatusConflict || !strings.Contains(review.Body.String(), canaryCapReason) {
		t.Fatalf("review status=%d body=%s", review.Code, review.Body.String())
	}
	row, err := st.GetOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "pending_review" {
		t.Fatalf("status=%s, want pending_review", row.Status)
	}
	if !containsString(st.events, "live_canary_refused") {
		t.Fatalf("canary refusal event missing: %v", st.events)
	}
	st.mu.Lock()
	payloads := append([]any(nil), st.eventPayloads["live_canary_refused"]...)
	st.mu.Unlock()
	if len(payloads) == 0 {
		t.Fatal("canary refusal payload missing")
	}
	payload, ok := payloads[len(payloads)-1].(map[string]any)
	if !ok || payload["revision_id"] != st.liveCanary.ID || payload["generation"] != st.liveCanary.Generation ||
		payload["risk_cap"] != st.liveCanary.DailyAuthorizedRiskCapUSD {
		t.Fatalf("canary refusal payload=%v", payloads[len(payloads)-1])
	}
}

func TestClassCApprovalBindsActiveCanaryRevision(t *testing.T) {
	s, st, venue := m11Server("1000")
	setQuote(venue, "SPY", "1.99", "2.00", 45_000)
	handler := s.routes()
	proposal := routeRequestWithKey(handler, http.MethodPost, "/operations", m11OpenPayload("SPY", "200"), "runtime-secret", "m11-class-c-bound")
	if proposal.Code != http.StatusOK || !strings.Contains(proposal.Body.String(), `"status":"pending_review"`) {
		t.Fatalf("proposal status=%d body=%s", proposal.Code, proposal.Body.String())
	}
	var proposalBody map[string]any
	if err := json.Unmarshal(proposal.Body.Bytes(), &proposalBody); err != nil {
		t.Fatal(err)
	}
	operationID, _ := proposalBody["operation_id"].(string)
	review := routeRequest(handler, http.MethodPost, "/operations/"+operationID+"/review",
		`{"verdict":"approved","rationale":"bounded canary review"}`, "admin-secret")
	if review.Code != http.StatusOK {
		t.Fatalf("review status=%d body=%s", review.Code, review.Body.String())
	}
	st.mu.Lock()
	grant, exists := st.grants[operationID]
	st.mu.Unlock()
	if !exists || grant.LiveCanaryRevisionID != st.liveCanary.ID {
		t.Fatalf("grant=%+v exists=%v, want revision %d", grant, exists, st.liveCanary.ID)
	}
}

func TestLiveOpenRejectsProviderOrderKindBeforeGrant(t *testing.T) {
	venue := broker.NewFake(units.MustMicros("300"))
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(), broker: venue,
		execution: equityOnlyExecution{ExecutionProvider: venue}, store: newMemoryStore(),
	}
	quote := broker.Quote{
		Symbol: "option-id", Bid: units.MustMicros("0.34"), Ask: units.MustMicros("0.35"),
		AsOf: time.Now().UTC(), OpenInterest: 1000,
	}
	op := s.deriveOpenOperation(context.Background(), risk.Operation{
		Action: "open", Kind: "option", Symbol: "option-id", Underlying: "SPY",
		Side: "buy", Qty: units.MustQty("1"),
	}, &quote)
	if op.RejectReason != "unsupported_contract" {
		t.Fatalf("reject_reason=%q", op.RejectReason)
	}
}
