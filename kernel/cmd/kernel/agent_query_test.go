package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestLegacyAgentQueryWritePathIsGone(t *testing.T) {
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeReadOnly, RuntimeToken: "runtime-secret"},
		store: newMemoryStore(),
	}
	response := routeRequest(s.routes(), http.MethodPost, "/agent/query", `{"symbol":"SOFI","query":"test"}`, "runtime-secret")
	if response.Code != http.StatusGone {
		t.Fatalf("status=%d, want 410", response.Code)
	}
	if len(s.store.(*memoryStore).agentQueryJobs) != 0 {
		t.Fatal("retired route created a legacy agent_query_job")
	}
}

func TestCortexConversationIDRejectsURLSyntax(t *testing.T) {
	for _, value := range []string{"https://example.com", "../escape", "contains space", ""} {
		if validCortexConversationID(value) {
			t.Fatalf("invalid Conversation ID accepted: %q", value)
		}
	}
	if !validCortexConversationID("agent-lab-7deed53d-d45f-4b2d-a12b-b1e4bf3306e8") {
		t.Fatal("valid Conversation ID rejected")
	}
}

func TestFetchCortexOperationsAcceptsBoundedOverview(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/operations/overview" ||
			r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("unexpected upstream request: %s auth=%q",
				r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"generated_at":"2026-07-23T23:00:00Z",
			"status":"degraded",
			"cortex":{"status":"healthy"},
			"research":{"status":"degraded"}
		}`))
	}))
	defer upstream.Close()
	tokenPath := filepath.Join(t.TempDir(), "cortex-token")
	if err := os.WriteFile(tokenPath, []byte("test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &server{
		cortexURL:       upstream.URL,
		cortexTokenFile: tokenPath,
		runtimeHTTP:     upstream.Client(),
	}
	raw, code := s.fetchCortexOperations(context.Background())
	if code != "" || len(raw) == 0 {
		t.Fatalf("raw=%s code=%s", raw, code)
	}
}
