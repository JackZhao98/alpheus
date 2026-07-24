package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestAgentConsoleServesDedicatedCommandSurface(t *testing.T) {
	s := &server{}
	response := routeRequest(s.routes(), http.MethodGet, "/agent/console", "", "")
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), "交易决策控制台") ||
		!strings.Contains(response.Body.String(), "AI Trigger Points") ||
		!strings.Contains(response.Body.String(), "MOODY BLUES · DATA STREAM") ||
		!strings.Contains(response.Body.String(), "Agent Channel") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	script := routeRequest(s.routes(), http.MethodGet,
		"/assets/agent-console.js", "", "")
	if script.Code != http.StatusOK ||
		!strings.Contains(script.Body.String(),
			`request("/agent/console/triggers")`) ||
		!strings.Contains(script.Body.String(),
			"`/agent/console/candidates?environment=${encodeURIComponent(environment)}`") ||
		!strings.Contains(script.Body.String(), "trigger.last_value") ||
		!strings.Contains(script.Body.String(), "advanceReplay") ||
		!strings.Contains(script.Body.String(), "loadTriggers(),loadHealth(),loadMarket()") {
		t.Fatalf("script status=%d body=%s",
			script.Code, script.Body.String())
	}
}

func TestAgentRoomLinksConsoleAndKeepsStrategiesBelowInteractionMode(t *testing.T) {
	s := &server{}
	response := routeRequest(s.routes(), http.MethodGet, "/agent", "", "")
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `href="/agent/console"`) ||
		!strings.Contains(response.Body.String(), "Monitor Session") ||
		strings.Contains(response.Body.String(), `data-mode="spx_gamma"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentConsoleSnapshotProjectsRealKernelState(t *testing.T) {
	venue := newFake("300")
	st := newMemoryStore()
	s := &server{
		mode: config.ModeConfig{
			TradingMode:      config.ModeSim,
			AgentWebAuthMode: config.AgentWebAuthLocal,
		},
		broker: venue,
		store:  st,
	}

	response := routeRequest(s.routes(), http.MethodGet, "/agent/console/snapshot", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Environment agentConsoleEnvironment `json:"environment"`
		Autonomy    agentConsoleAutonomy    `json:"autonomy"`
		Portfolio   struct {
			Available bool `json:"available"`
			Account   struct {
				BuyingPower float64 `json:"buying_power"`
			} `json:"account"`
		} `json:"portfolio"`
		Activity agentConsoleActivity `json:"activity"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Environment.Selected != "paper" || !body.Environment.PaperAvailable ||
		body.Environment.LiveAvailable || !body.Environment.ExecutionEnabled {
		t.Fatalf("environment=%+v", body.Environment)
	}
	if body.Autonomy.Selected != "observe" ||
		len(body.Autonomy.Available) != 3 ||
		body.Autonomy.Generation != 1 {
		t.Fatalf("autonomy=%+v", body.Autonomy)
	}
	if !body.Portfolio.Available ||
		body.Portfolio.Account.BuyingPower != 100000 {
		t.Fatalf("portfolio=%+v", body.Portfolio)
	}
	if !body.Activity.Available || body.Activity.Operations == nil {
		t.Fatalf("activity=%+v", body.Activity)
	}
}

func TestAgentConsolePaperAutonomyIsDurableAndGenerationGuarded(
	t *testing.T,
) {
	s := &server{
		mode: config.ModeConfig{
			TradingMode:      config.ModeReadOnly,
			AgentWebAuthMode: config.AgentWebAuthLocal,
		},
		store:  newMemoryStore(),
		limits: dualLedgerLimits(),
	}
	first := routeRequest(
		s.routes(), http.MethodPut, "/agent/console/autonomy/paper",
		`{"expected_generation":1,"mode":"copilot"}`, "",
	)
	if first.Code != http.StatusOK ||
		!strings.Contains(first.Body.String(), `"selected":"copilot"`) ||
		!strings.Contains(first.Body.String(), `"generation":2`) {
		t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
	}
	snapshot := routeRequest(
		s.routes(), http.MethodGet,
		"/agent/console/snapshot?environment=paper", "", "",
	)
	if snapshot.Code != http.StatusOK ||
		!strings.Contains(snapshot.Body.String(), `"selected":"copilot"`) ||
		!strings.Contains(snapshot.Body.String(),
			`"execution_enabled":true`) {
		t.Fatalf("status=%d body=%s", snapshot.Code, snapshot.Body.String())
	}
	stale := routeRequest(
		s.routes(), http.MethodPut, "/agent/console/autonomy/paper",
		`{"expected_generation":1,"mode":"agentic"}`, "",
	)
	if stale.Code != http.StatusConflict ||
		!strings.Contains(stale.Body.String(),
			`"error_code":"autonomy_generation_conflict"`) {
		t.Fatalf("status=%d body=%s", stale.Code, stale.Body.String())
	}
}

func TestAgentConsoleLiveAutonomyFailsClosed(t *testing.T) {
	s := &server{
		mode: config.ModeConfig{
			TradingMode:      config.ModeReadOnly,
			AgentWebAuthMode: config.AgentWebAuthLocal,
		},
		store: newMemoryStore(),
	}
	response := routeRequest(
		s.routes(), http.MethodPut, "/agent/console/autonomy/live",
		`{"expected_generation":1,"mode":"agentic"}`, "",
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(),
			`"error_code":"live_autonomy_locked"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentConsoleSnapshotShowsLiveDataWithoutClaimingExecution(t *testing.T) {
	s := &server{
		mode:             config.ModeConfig{TradingMode: config.ModeReadOnly},
		robinhoodEnabled: true,
	}
	environment := s.agentConsoleEnvironment("")
	if environment.Selected != "live" || environment.DataScope != "live" ||
		!environment.LiveAvailable || !environment.PaperAvailable ||
		environment.ExecutionEnabled {
		t.Fatalf("environment=%+v", environment)
	}
	paper := s.agentConsoleEnvironment("paper")
	if paper.Selected != "paper" || paper.DataScope != "paper" ||
		!paper.PaperAvailable || !paper.LiveAvailable ||
		paper.ExecutionEnabled {
		t.Fatalf("paper environment=%+v", paper)
	}
}

func TestAgentConsolePaperPortfolioIsIndependentAndDurable(t *testing.T) {
	s := &server{
		mode: config.ModeConfig{
			TradingMode:      config.ModeReadOnly,
			AgentWebAuthMode: config.AgentWebAuthLocal,
		},
		robinhoodEnabled: true,
		store:            newMemoryStore(),
		limits:           dualLedgerLimits(),
	}
	response := routeRequest(
		s.routes(), http.MethodGet,
		"/agent/console/snapshot?environment=paper", "", "",
	)
	body := response.Body.String()
	if response.Code != http.StatusOK ||
		!strings.Contains(body, `"selected":"paper"`) ||
		!strings.Contains(body, `"source":"agent-paper-ledger"`) ||
		!strings.Contains(body, `"equity":100000`) ||
		!strings.Contains(body, `"paper_orders":[]`) ||
		strings.Contains(body, "robinhood-mcp") {
		t.Fatalf("status=%d body=%s", response.Code, body)
	}
}

func TestAgentConsoleMarketRoutesUseAgentWebAuthorization(t *testing.T) {
	s := &server{mode: config.ModeConfig{
		TradingMode:      config.ModeReadOnly,
		AgentWebAuthMode: config.AgentWebAuthPassword,
	}}
	response := routeRequest(s.routes(), http.MethodGet,
		"/agent/console/market/bars/SPY?days=5", "", "")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentConsoleTriggerRegistryUsesCortexAuthority(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
			}
			switch {
			case r.Method == http.MethodGet &&
				r.URL.Path == "/v1/decision-triggers":
				writeJSON(w, http.StatusOK, map[string]any{
					"available": true,
					"items": []any{map[string]any{
						"trigger_id": "trigger-1",
						"title":      "SPY downside review",
					}},
				})
			case r.Method == http.MethodPut &&
				r.URL.Path == "/v1/decision-triggers/trigger-1":
				var command agentConsoleTriggerCommand
				if json.NewDecoder(r.Body).Decode(&command) != nil ||
					command.Symbol != "SPY" ||
					command.Threshold.String() != "730" {
					t.Fatalf("command=%+v", command)
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"status": "registered",
					"trigger": map[string]any{
						"trigger_id": "trigger-1",
						"title":      command.Title,
					},
				})
			default:
				t.Fatalf("unexpected upstream request: %s %s",
					r.Method, r.URL.Path)
			}
		}))
	defer upstream.Close()
	tokenPath := filepath.Join(t.TempDir(), "cortex-token")
	if err := os.WriteFile(tokenPath, []byte("test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cortexURL:       upstream.URL,
		cortexTokenFile: tokenPath,
		runtimeHTTP:     upstream.Client(),
	}
	getResponse := httptest.NewRecorder()
	s.getAgentConsoleTriggers(getResponse,
		httptest.NewRequest(http.MethodGet,
			"/agent/console/triggers", nil))
	if getResponse.Code != http.StatusOK ||
		!strings.Contains(getResponse.Body.String(), "SPY downside review") {
		t.Fatalf("GET status=%d body=%s",
			getResponse.Code, getResponse.Body.String())
	}
	putResponse := httptest.NewRecorder()
	putRequest := httptest.NewRequest(http.MethodPut,
		"/agent/console/triggers/trigger-1",
		strings.NewReader(`{
				"expected_generation":0,
				"title":"SPY downside review",
				"strategy_id":"price_monitor",
				"data_source":"kernel_quote",
				"symbol":"spy",
				"metric":"mid_price",
				"comparator":"crosses_below",
				"threshold":730,
				"cooldown_seconds":900,
				"objective":"Reassess SPY.",
				"enabled":true
			}`))
	putRequest.SetPathValue("id", "trigger-1")
	putRequest.Header.Set("Content-Type", "application/json")
	s.putAgentConsoleTrigger(putResponse, putRequest)
	if putResponse.Code != http.StatusOK ||
		!strings.Contains(putResponse.Body.String(), `"status":"registered"`) {
		t.Fatalf("PUT status=%d body=%s",
			putResponse.Code, putResponse.Body.String())
	}
}

func TestAgentConsoleCandidatesUseCortexAuthorityAndHideInLive(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.Method != http.MethodGet ||
				r.URL.Path != "/v1/paper-candidates" ||
				r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("unexpected upstream request: %s %s auth=%q",
					r.Method, r.URL.Path, r.Header.Get("Authorization"))
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"available": true,
				"items": []any{map[string]any{
					"candidate_id": "candidate-1",
					"status":       "proposed",
					"eligible":     true,
					"proposal": map[string]any{
						"symbol": "SPY",
						"side":   "buy",
						"qty":    0.25,
					},
				}},
			})
		}))
	defer upstream.Close()
	tokenPath := filepath.Join(t.TempDir(), "cortex-token")
	if err := os.WriteFile(
		tokenPath, []byte("test-token"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cortexURL:       upstream.URL,
		cortexTokenFile: tokenPath,
		runtimeHTTP:     upstream.Client(),
	}
	paper := httptest.NewRecorder()
	s.getAgentConsoleCandidates(paper, httptest.NewRequest(
		http.MethodGet, "/agent/console/candidates?environment=paper", nil,
	))
	if paper.Code != http.StatusOK ||
		!strings.Contains(paper.Body.String(), `"candidate_id":"candidate-1"`) {
		t.Fatalf("paper status=%d body=%s", paper.Code, paper.Body.String())
	}
	live := httptest.NewRecorder()
	s.getAgentConsoleCandidates(live, httptest.NewRequest(
		http.MethodGet, "/agent/console/candidates?environment=live", nil,
	))
	if live.Code != http.StatusOK ||
		!strings.Contains(live.Body.String(),
			`"reason":"paper_candidates_hidden_in_live"`) ||
		requests != 1 {
		t.Fatalf("live status=%d body=%s requests=%d",
			live.Code, live.Body.String(), requests)
	}
}

func TestAgentConsoleCandidateReviewRequiresCopilotAndProxiesDecision(
	t *testing.T,
) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			upstreamCalls++
			if r.Method != http.MethodPost ||
				r.URL.Path != "/v1/paper-candidates/candidate-1/review" ||
				r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("unexpected upstream request: %s %s auth=%q",
					r.Method, r.URL.Path, r.Header.Get("Authorization"))
			}
			var command struct {
				ExpectedGeneration int64  `json:"expected_generation"`
				Decision           string `json:"decision"`
			}
			if json.NewDecoder(r.Body).Decode(&command) != nil ||
				command.ExpectedGeneration != 1 ||
				command.Decision != "approve" {
				t.Fatalf("command=%+v", command)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"status":       "reviewed",
				"candidate_id": "candidate-1",
				"generation":   2,
				"state":        "approved",
			})
		}))
	defer upstream.Close()
	tokenPath := filepath.Join(t.TempDir(), "cortex-token")
	if err := os.WriteFile(
		tokenPath, []byte("test-token"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	st := newMemoryStore()
	s := &server{
		store:           st,
		cortexURL:       upstream.URL,
		cortexTokenFile: tokenPath,
		runtimeHTTP:     upstream.Client(),
	}
	body := `{"environment":"paper","expected_generation":1,"decision":"approve"}`
	observe := httptest.NewRecorder()
	observeRequest := httptest.NewRequest(
		http.MethodPost,
		"/agent/console/candidates/candidate-1/review",
		strings.NewReader(body),
	)
	observeRequest.SetPathValue("id", "candidate-1")
	observeRequest.Header.Set("Content-Type", "application/json")
	s.postAgentConsoleCandidateReview(observe, observeRequest)
	if observe.Code != http.StatusConflict ||
		!strings.Contains(observe.Body.String(),
			`"error_code":"paper_candidate_review_requires_copilot"`) ||
		upstreamCalls != 0 {
		t.Fatalf("observe status=%d body=%s calls=%d",
			observe.Code, observe.Body.String(), upstreamCalls)
	}
	if _, err := st.SetAgentAutonomy(
		"paper", "copilot", 1, "owner-1",
	); err != nil {
		t.Fatal(err)
	}
	copilot := httptest.NewRecorder()
	copilotRequest := httptest.NewRequest(
		http.MethodPost,
		"/agent/console/candidates/candidate-1/review",
		strings.NewReader(body),
	)
	copilotRequest.SetPathValue("id", "candidate-1")
	copilotRequest.Header.Set("Content-Type", "application/json")
	s.postAgentConsoleCandidateReview(copilot, copilotRequest)
	if copilot.Code != http.StatusOK ||
		!strings.Contains(copilot.Body.String(), `"state":"approved"`) ||
		upstreamCalls != 1 {
		t.Fatalf("copilot status=%d body=%s calls=%d",
			copilot.Code, copilot.Body.String(), upstreamCalls)
	}
}

func TestAgentConsoleMoodyBluesReplayUsesCortexBoundary(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			requests++
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
			}
			switch r.URL.Path {
			case "/v1/data-streams/gexbot/replays":
				var command agentConsoleReplayCreateCommand
				if r.Method != http.MethodPost ||
					json.NewDecoder(r.Body).Decode(&command) != nil ||
					command.Symbol != "SPX" ||
					command.Category != "gex_full" {
					t.Fatalf("create command=%+v", command)
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"replay_id":  "11111111-1111-4111-8111-111111111111",
					"state":      "active",
					"generation": 1,
				})
			case "/v1/data-streams/gexbot/replays/11111111-1111-4111-8111-111111111111/next":
				var command agentConsoleReplayStepCommand
				if r.Method != http.MethodPost ||
					json.NewDecoder(r.Body).Decode(&command) != nil ||
					command.Generation != 1 {
					t.Fatalf("step command=%+v", command)
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"replay_id":  "11111111-1111-4111-8111-111111111111",
					"state":      "active",
					"generation": 2,
					"observation": map[string]any{
						"metrics": map[string]any{"spot": 6400},
					},
				})
			default:
				t.Fatalf("unexpected upstream path=%s", r.URL.Path)
			}
		},
	))
	defer upstream.Close()
	tokenPath := filepath.Join(t.TempDir(), "cortex-token")
	if err := os.WriteFile(
		tokenPath, []byte("test-token"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cortexURL:       upstream.URL,
		cortexTokenFile: tokenPath,
		runtimeHTTP:     upstream.Client(),
	}
	create := httptest.NewRecorder()
	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/agent/console/data-streams/gexbot/replays",
		strings.NewReader(`{
		  "request_id":"console-replay-1",
		  "symbol":"spx",
		  "category":"gex_full",
		  "start_available_at":"2026-07-23T13:00:00Z",
		  "end_available_at":"2026-07-23T20:00:00Z",
		  "as_of":"2026-07-24T00:00:00Z"
		}`),
	)
	createRequest.Header.Set("Content-Type", "application/json")
	s.postAgentConsoleReplay(create, createRequest)
	if create.Code != http.StatusOK ||
		!strings.Contains(create.Body.String(), `"generation":1`) {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	step := httptest.NewRecorder()
	stepRequest := httptest.NewRequest(
		http.MethodPost,
		"/agent/console/data-streams/gexbot/replays/11111111-1111-4111-8111-111111111111/next",
		strings.NewReader(`{"generation":1}`),
	)
	stepRequest.SetPathValue(
		"id", "11111111-1111-4111-8111-111111111111",
	)
	stepRequest.Header.Set("Content-Type", "application/json")
	s.postAgentConsoleReplayNext(step, stepRequest)
	if step.Code != http.StatusOK ||
		!strings.Contains(step.Body.String(), `"spot":6400`) ||
		requests != 2 {
		t.Fatalf(
			"step status=%d body=%s requests=%d",
			step.Code, step.Body.String(), requests,
		)
	}
}
