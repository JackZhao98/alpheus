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
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

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
