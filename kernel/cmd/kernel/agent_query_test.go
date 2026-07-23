package main

import (
	"net/http"
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
