package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"alpheus/agentruntime/internal/contracts"
	"alpheus/agentruntime/internal/roles"
)

func TestWakeRequiresKernelToken(t *testing.T) {
	runs := 0
	handler := newWakeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(roles.Role, string, string) { runs++ })

	for _, token := range []string{"", "runtime-secret", "wrong"} {
		req := httptest.NewRequest(http.MethodPost, "/wake", bytes.NewBufferString(`{"role":"scout"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("token=%q status=%d body=%s", token, w.Code, w.Body.String())
		}
	}
	if runs != 0 {
		t.Fatalf("unauthorized wake ran %d sessions", runs)
	}
}

func TestWakeAcceptsAuthenticatedKnownRole(t *testing.T) {
	var gotRole, gotTrigger, gotOccurrence string
	handler := newWakeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(role roles.Role, trigger, occurrenceID string) {
		gotRole, gotTrigger, gotOccurrence = role.Role, trigger, occurrenceID
	})
	req := authenticatedWakeRequest(`{"role":"scout","trigger":"spine","occurrence_id":"scout:20260717T164500Z:abc123"}`)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || gotRole != "scout" || gotTrigger != "spine" ||
		gotOccurrence != "scout:20260717T164500Z:abc123" {
		t.Fatalf("status=%d role=%q trigger=%q occurrence=%q body=%s", w.Code, gotRole, gotTrigger, gotOccurrence, w.Body.String())
	}
}

func TestWakeDeduplicatesConcurrentOccurrence(t *testing.T) {
	var runs atomic.Int64
	handler := newWakeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(roles.Role, string, string) { runs.Add(1) })
	const requests = 20
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(requests)
	done.Add(requests)
	statuses := make(chan int, requests)
	for range requests {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			req := authenticatedWakeRequest(`{"role":"scout","trigger":"spine","occurrence_id":"same-slot"}`)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			statuses <- w.Code
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusAccepted {
			t.Fatalf("duplicate wake status=%d", status)
		}
	}
	if runs.Load() != 1 {
		t.Fatalf("same occurrence ran %d sessions, want 1", runs.Load())
	}
}

func TestWakeRejectsUnknownRoleAndInvalidOccurrence(t *testing.T) {
	handler := newWakeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(roles.Role, string, string) { t.Fatal("invalid wake ran a session") })

	unknown := authenticatedWakeRequest(`{"role":"missing","trigger":"spine","occurrence_id":"slot-1"}`)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, unknown)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown role status=%d body=%s", w.Code, w.Body.String())
	}

	for _, body := range []string{
		`{"role":"scout","trigger":"spine"}`,
		`{"role":"scout","trigger":"manual","occurrence_id":"slot-1"}`,
		`{"role":"scout","trigger":"spine","occurrence_id":"bad/id"}`,
	} {
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, authenticatedWakeRequest(body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, w.Code, w.Body.String())
		}
	}
}

func TestWakeResponseMarksDuplicate(t *testing.T) {
	handler := newWakeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(roles.Role, string, string) {})
	body := `{"role":"scout","trigger":"spine","occurrence_id":"slot-1"}`
	for i, wantDuplicate := range []bool{false, true} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, authenticatedWakeRequest(body))
		var response map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response["deduplicated"] != wantDuplicate {
			t.Fatalf("request %d response=%v, want deduplicated=%v", i+1, response, wantDuplicate)
		}
	}
}

func TestQueryRunsScoutWithoutSubmittingOperations(t *testing.T) {
	var gotSymbol, gotQuery, gotAPIKey string
	handler := newRuntimeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(roles.Role, string, string) {
		t.Fatal("query must not run the operation session path")
	}, func(workflow, symbol, query, apiKey string) (queryResult, error) {
		gotSymbol, gotQuery, gotAPIKey = symbol, query, apiKey
		return queryResult{
			Role: "scout", Workflow: workflow,
			Output:    contracts.OpportunityBrief{Action: "PASS"},
			Cognition: "llm", Provider: "openai", Model: "gpt-5.6-sol",
		}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBufferString(`{"symbol":"SOFI","query":"值得研究吗？","openai_api_key":"sk-test-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer kernel-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK || gotSymbol != "SOFI" || gotQuery != "值得研究吗？" || gotAPIKey != "sk-test-secret" {
		t.Fatalf("status=%d symbol=%q query=%q api_key_set=%t body=%s", w.Code, gotSymbol, gotQuery, gotAPIKey != "", w.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["role"] != "scout" {
		t.Fatalf("response=%v", response)
	}
	if response["workflow"] != "scout" {
		t.Fatalf("response=%v", response)
	}
	if response["cognition"] != "llm" || response["provider"] != "openai" || response["model"] != "gpt-5.6-sol" {
		t.Fatalf("response=%v", response)
	}
	if strings.Contains(w.Body.String(), "sk-test-secret") {
		t.Fatal("API key leaked into query response")
	}
}

func authenticatedWakeRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/wake", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer kernel-secret")
	return req
}
