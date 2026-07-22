package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
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
	for _, stage := range []string{"submitted", "claimed", "credential_loaded", "runtime_request_started", "runtime_response_received", "completed"} {
		if !agentQueryTraceHasStage(completed.Trace, stage) {
			t.Fatalf("trace missing %q: %+v", stage, completed.Trace)
		}
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

func agentQueryTraceHasStage(trace []store.AgentQueryTraceEvent, stage string) bool {
	for _, event := range trace {
		if event.Stage == stage {
			return true
		}
	}
	return false
}

func TestAgentQueryTraceExposesRuntimeFailureCode(t *testing.T) {
	client := &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error_code":"runtime_overloaded","error":"busy"}`)),
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
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"workflow":"scout","symbol":"SOFI","query":"trace failure"}`, "runtime-secret")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var submitted store.AgentQueryJob
	if err := json.Unmarshal(response.Body.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	var completed *store.AgentQueryJob
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		job, err := st.GetAgentQueryJob(submitted.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job != nil && job.Status == "failed" {
			completed = job
			break
		}
		time.Sleep(time.Millisecond)
	}
	if completed == nil || completed.ErrorCode != "runtime_rejected" {
		t.Fatalf("failed job=%+v", completed)
	}
	if !agentQueryTraceHasStage(completed.Trace, "runtime_request_failed") || !agentQueryTraceHasStage(completed.Trace, "failed") {
		t.Fatalf("failure trace=%+v", completed.Trace)
	}
	for _, event := range completed.Trace {
		if event.Stage == "runtime_request_failed" && event.ErrorCode != "runtime_rejected" {
			t.Fatalf("runtime failure event=%+v", event)
		}
	}
}

func TestAgentQueryRecoveryReclaimsExpiredLeaseExactlyOnce(t *testing.T) {
	var runtimeCalls atomic.Int32
	client := &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		runtimeCalls.Add(1)
		var input agentQueryInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.Workflow != "scout" || input.Symbol != "SOFI" || input.Query != "recover me" ||
			input.OpenAIAPIKey != "sk-recovery-secret" {
			t.Fatalf("recovered input=%+v", input)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"role":"scout","workflow":"scout","cognition":"llm","provider":"openai","model":"gpt-5.6-sol","output":{"action":"PASS","candidates":[],"structural_notes":[]}}`,
			)),
			Request: r,
		}, nil
	})}
	st := newMemoryStore()
	s := &server{
		mode: config.ModeConfig{
			TradingMode: config.ModeSim, RuntimeToken: "runtime-secret", KernelToken: "kernel-secret",
			AgentWebSessionKey: strings.Repeat("k", 32),
		},
		store: st, runtimeURL: "http://runtime.test", runtimeHTTP: client,
	}
	ciphertext, err := sealAgentSecret(s.mode.AgentWebSessionKey, "openai", "sk-recovery-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAgentSecret("openai", ciphertext); err != nil {
		t.Fatal(err)
	}
	job, err := st.CreateAgentQueryJob("owner", "scout", "SOFI", "recover me")
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.ClaimAgentQueryJob(job.ID, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	st.mu.Lock()
	expired := st.agentQueryJobs[job.ID]
	expired.LeaseExpiresAt = time.Now().UTC().Add(-time.Second)
	st.agentQueryJobs[job.ID] = expired
	st.mu.Unlock()

	if err := s.recoverAgentQueryJobs(); err != nil {
		t.Fatal(err)
	}
	if err := s.recoverAgentQueryJobs(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	var recovered *store.AgentQueryJob
	for time.Now().Before(deadline) {
		recovered, err = st.GetAgentQueryJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if recovered.Status == "succeeded" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if recovered == nil || recovered.Status != "succeeded" || recovered.Attempt != 2 || runtimeCalls.Load() != 1 {
		t.Fatalf("recovered=%+v runtime_calls=%d", recovered, runtimeCalls.Load())
	}
	if updated, err := st.CompleteClaimedAgentQueryJob(job.ID, first.ClaimToken, json.RawMessage(`{"stale":true}`)); err != nil || updated {
		t.Fatalf("stale completion updated=%v err=%v", updated, err)
	}
	encoded, err := json.Marshal(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), first.ClaimToken) || strings.Contains(string(encoded), "sk-recovery-secret") {
		t.Fatalf("recovered job leaked claim or secret: %s", encoded)
	}
}

func TestAgentQueryRequiresConfiguredOpenAIAPIKey(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim, RuntimeToken: "runtime-secret", AgentWebSessionKey: strings.Repeat("k", 32)}, store: newMemoryStore()}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"symbol":"SOFI","query":"test"}`, "runtime-secret")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "OpenAI API token is not configured") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"error_code":"agent_query_openai_credential_unavailable"`) {
		t.Fatalf("missing stable error code: %s", response.Body.String())
	}
}

func TestAgentQueryRejectsBrowserSuppliedCredential(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim, RuntimeToken: "runtime-secret", AgentWebSessionKey: strings.Repeat("k", 32)}, store: newMemoryStore()}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"symbol":"SOFI","query":"test","openai_api_key":"must-not-cross-browser-boundary"}`, "runtime-secret")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid JSON body") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"error_code":"request_json_invalid"`) {
		t.Fatalf("missing stable error code: %s", response.Body.String())
	}
}

func TestCortexConversationIDRejectsURLSyntax(t *testing.T) {
	for _, value := range []string{"agent-lab-123", "Conversation_01"} {
		if !validCortexConversationID(value) {
			t.Fatalf("valid conversation id rejected: %q", value)
		}
	}
	for _, value := range []string{"", "agent-lab/other", "agent-lab?x=1", "agent lab"} {
		if validCortexConversationID(value) {
			t.Fatalf("invalid conversation id accepted: %q", value)
		}
	}
}
