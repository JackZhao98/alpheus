package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/marketdata"
	"alpheus/kernel/internal/rhmcp"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type candidateExecution struct {
	candidates []broker.OrderResult
	placeCalls int
}

func (c *candidateExecution) PlaceLimitOrder(context.Context, broker.PlaceRequest) (broker.OrderResult, error) {
	c.placeCalls++
	return broker.OrderResult{}, &rhmcp.MutationError{Kind: rhmcp.ErrMutationOutcomeUnknown, Code: "call_failed"}
}

func (c *candidateExecution) CancelOrder(context.Context, string) (broker.OrderResult, error) {
	return broker.OrderResult{}, broker.ErrNotFound
}

func (c *candidateExecution) GetOrder(_ context.Context, brokerOrderID string) (broker.OrderResult, error) {
	for _, candidate := range c.candidates {
		if candidate.BrokerOrderID == brokerOrderID {
			return candidate, nil
		}
	}
	return broker.OrderResult{}, broker.ErrNotFound
}

func (c *candidateExecution) FindExactPlaceCandidates(context.Context, broker.ExactPlaceCandidateQuery) ([]broker.OrderResult, error) {
	return append([]broker.OrderResult(nil), c.candidates...), nil
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
	limits.LiveCanary.DailyAuthorizedRiskCapUSD = units.MustMicros(cap)
	limits.LiveCanary.CleanDaysBeforeRaise = 3
	proposalTTL, err := proposalLifetime(limits.ProposalTTLSec)
	if err != nil {
		panic(err)
	}
	st := newMemoryStore()
	return &server{
		mode: protectedMode(config.ModeLive), limits: limits,
		broker: venue, store: st, proposalTTL: proposalTTL,
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
	st.mu.Unlock()
	if grantCount != 1 {
		t.Fatalf("grants=%d", grantCount)
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
	execution.candidates = []broker.OrderResult{{
		BrokerOrderID: brokerOrderID, ClientOrderID: attempt.ClientOrderID, State: "submitted",
	}}
	claimed, err := st.ClaimRecoverableAttemptLive(attempt.ID, "candidate-discovery", "unknown", attempt.Attempt, time.Now())
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
	execution.candidates = append(execution.candidates, broker.OrderResult{
		BrokerOrderID: "88888888-8888-4888-8888-888888888888", ClientOrderID: attempt.ClientOrderID, State: "submitted",
	})
	ambiguous := routeRequest(handler, http.MethodPost, "/execution-attempts/"+attempt.ID+"/adopt-candidate", body, "admin-secret")
	if ambiguous.Code != http.StatusConflict {
		t.Fatalf("ambiguous status=%d body=%s", ambiguous.Code, ambiguous.Body.String())
	}
	current, _ = st.GetExecutionAttempt(attempt.ID)
	gate, _ := st.GetLiveExecutionGate()
	if current.State != "unknown" || gate.UnknownAttemptID != attempt.ID {
		t.Fatalf("ambiguous current=%+v gate=%+v", current, gate)
	}
	execution.candidates = execution.candidates[:1]
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
	payload := `{"proposer":"m11","action":"open","kind":"equity","underlying":"EQ","symbol":"EQ","side":"buy","qty":1,"max_risk_usd":10,"plan":` + m11Plan + `}`
	response := routeRequestWithKey(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret", "m11-one-replay")
	if response.Code != http.StatusBadGateway || execution.placeCalls != 1 {
		t.Fatalf("initial status=%d calls=%d body=%s", response.Code, execution.placeCalls, response.Body.String())
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
		claimed, err := st.ClaimRecoverableAttemptLive(current.ID, "recovery", "unknown", current.Attempt, time.Now())
		if err != nil || claimed == nil {
			t.Fatalf("recovery %d claim=%+v err=%v", recovery, claimed, err)
		}
		_ = s.reconcileLivePlaceAttempt(context.Background(), execution, claimed)
	}
	current, _ := st.GetExecutionAttempt(attempt.ID)
	if execution.placeCalls != 2 || current.ReplayCount != 1 || current.State != "unknown" {
		t.Fatalf("calls=%d attempt=%+v", execution.placeCalls, current)
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

func TestLiveCanaryDoesNotConsumeOrBlockShadowLedger(t *testing.T) {
	s, st, _ := m11Server("35")
	window, err := marketDayWindow(time.Now().UTC(), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	st.grants["live-full"] = store.TradeGrant{
		OperationID: "live-full", Ledger: "live", MarketDay: window.day,
		AuthorizedRisk: units.MustMicros("35"), RiskSource: "computed",
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
}
