package main

import (
	"net/http"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestTelemetryRequiresRuntimePermissionAndPersistsEvent(t *testing.T) {
	store := newMemoryStore()
	server := &server{mode: protectedMode(config.ModeLive), store: store}
	handler := server.routes()
	body := `{"role":"scout","model":"claude-test","input_tokens":101,"output_tokens":22,"latency_ms":9,"attempt":1,"status":"success"}`

	for _, token := range []string{"", "kernel-secret"} {
		response := routeRequest(handler, http.MethodPost, "/telemetry", body, token)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("token=%q status=%d body=%s", token, response.Code, response.Body.String())
		}
	}
	response := routeRequest(handler, http.MethodPost, "/telemetry", body, "runtime-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.events) != 1 || store.events[0] != "llm_telemetry" {
		t.Fatalf("events=%v", store.events)
	}
}

func TestTelemetryRejectsMalformedOrUnboundedFields(t *testing.T) {
	server := &server{mode: protectedMode(config.ModeLive), store: newMemoryStore()}
	for _, body := range []string{
		`{"role":"","model":"test","input_tokens":0,"output_tokens":0,"latency_ms":0,"attempt":0,"status":"budget_refused"}`,
		`{"role":"scout","model":"test","input_tokens":-1,"output_tokens":0,"latency_ms":0,"attempt":1,"status":"success"}`,
		`{"role":"scout","model":"test","input_tokens":0,"output_tokens":0,"latency_ms":0,"attempt":1,"status":"banana"}`,
		`{"role":"scout","model":"test","input_tokens":0,"output_tokens":0,"latency_ms":0,"attempt":1,"status":"success","prompt":"secret"}`,
	} {
		response := routeRequest(server.routes(), http.MethodPost, "/telemetry", body, "runtime-secret")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, response.Code, response.Body.String())
		}
	}
}
