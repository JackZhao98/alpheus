package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestAgentQueryProxiesThroughKernelWithoutOperationEffect(t *testing.T) {
	client := &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/query" {
			t.Fatalf("runtime request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer kernel-secret" {
			t.Fatal("missing kernel service authorization")
		}
		var input agentQueryInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.Symbol != "SOFI" || input.Query != "值得研究吗？" || input.OpenAIAPIKey != "sk-test-secret" {
			t.Fatalf("input=%+v", input)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"role":"scout","cognition":"llm","provider":"openai","model":"gpt-5.6-sol","output":{"action":"PASS","candidates":[],"structural_notes":[]}}`)),
			Request:    r,
		}, nil
	})}
	st := newMemoryStore()
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeSim, RuntimeToken: "runtime-secret", KernelToken: "kernel-secret"},
		store: st, runtimeURL: "http://runtime.test", runtimeHTTP: client,
	}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"symbol":"sofi","query":"值得研究吗？","openai_api_key":"sk-test-secret"}`, "runtime-secret")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"role":"scout"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !containsEvent(st.events, "agent_query") {
		t.Fatalf("events=%v", st.events)
	}
	if strings.Contains(response.Body.String(), "sk-test-secret") {
		t.Fatal("API key leaked into query response")
	}
}

func TestAgentQueryRequiresOpenAIAPIKey(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim, RuntimeToken: "runtime-secret"}}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"symbol":"SOFI","query":"test"}`, "runtime-secret")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "OpenAI API token is required") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
