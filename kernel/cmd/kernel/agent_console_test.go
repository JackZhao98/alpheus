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
		!strings.Contains(response.Body.String(), "Agent Channel") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	script := routeRequest(s.routes(), http.MethodGet,
		"/assets/agent-console.js", "", "")
	if script.Code != http.StatusOK ||
		!strings.Contains(script.Body.String(),
			`request("/agent/console/triggers")`) ||
		!strings.Contains(script.Body.String(), "trigger.last_value") ||
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
	if body.Autonomy.Selected != "observe" || len(body.Autonomy.Available) != 1 {
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
