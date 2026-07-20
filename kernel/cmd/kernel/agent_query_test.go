package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
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
		if input.Workflow != "team" || input.Symbol != "SOFI" || input.Query != "值得研究吗？" || input.OpenAIAPIKey != "sk-test-secret" {
			t.Fatalf("input=%+v", input)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"role":"desk_master","workflow":"team","cognition":"llm","provider":"openai","model":"gpt-5.6-sol","scout_output":{"action":"PASS","candidates":[],"structural_notes":[]},"output":{"action":"PASS","reasoning":"insufficient evidence","proposals":[],"watch_triggers":[],"blackboard_patch":{}}}`)),
			Request:    r,
		}, nil
	})}
	st := newMemoryStore()
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeSim, RuntimeToken: "runtime-secret", KernelToken: "kernel-secret", AgentWebSessionKey: strings.Repeat("k", 32)},
		store: st, runtimeURL: "http://runtime.test", runtimeHTTP: client,
	}
	ciphertext, err := sealAgentSecret(s.mode.AgentWebSessionKey, "openai", "sk-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAgentSecret("openai", ciphertext); err != nil {
		t.Fatal(err)
	}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"workflow":"team","symbol":"sofi","query":"值得研究吗？"}`, "runtime-secret")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var submitted store.AgentQueryJob
	if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil || submitted.ID == "" || submitted.Workflow != "team" || submitted.Status != "queued" {
		t.Fatalf("submitted=%+v err=%v", submitted, err)
	}
	var completed store.AgentQueryJob
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response = routeRequest(s.routes(), http.MethodGet, "/agent/query-jobs/"+submitted.ID, "", "runtime-secret")
		if response.Code != http.StatusOK {
			t.Fatalf("poll status=%d body=%s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &completed); err != nil {
			t.Fatal(err)
		}
		if completed.Status == "succeeded" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if completed.Status != "succeeded" || !strings.Contains(string(completed.Result), `"role":"desk_master"`) {
		t.Fatalf("completed=%+v", completed)
	}
	hasEvent := false
	eventDeadline := time.Now().Add(time.Second)
	for time.Now().Before(eventDeadline) {
		st.mu.Lock()
		hasEvent = containsEvent(st.events, "agent_query")
		st.mu.Unlock()
		if hasEvent {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !hasEvent {
		t.Fatal("agent_query event missing")
	}
	if strings.Contains(response.Body.String(), "sk-test-secret") || strings.Contains(string(completed.Result), "sk-test-secret") {
		t.Fatal("API key leaked into query response")
	}
}

func TestAgentQueryRequiresConfiguredOpenAIAPIKey(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim, RuntimeToken: "runtime-secret", AgentWebSessionKey: strings.Repeat("k", 32)}, store: newMemoryStore()}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"symbol":"SOFI","query":"test"}`, "runtime-secret")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "OpenAI API token is not configured") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentQueryRejectsBrowserSuppliedCredential(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim, RuntimeToken: "runtime-secret", AgentWebSessionKey: strings.Repeat("k", 32)}, store: newMemoryStore()}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"symbol":"SOFI","query":"test","openai_api_key":"must-not-cross-browser-boundary"}`, "runtime-secret")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid JSON body") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
