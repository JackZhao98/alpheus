package main

import (
	"encoding/json"
	"net/http"
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
	if !body.Portfolio.Available || body.Portfolio.Account.BuyingPower != 300 {
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
	environment := s.agentConsoleEnvironment()
	if environment.Selected != "live" || environment.DataScope != "live" ||
		!environment.LiveAvailable || environment.PaperAvailable ||
		environment.ExecutionEnabled {
		t.Fatalf("environment=%+v", environment)
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
