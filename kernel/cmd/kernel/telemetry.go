package main

import (
	"net/http"
	"strings"
)

type llmTelemetry struct {
	Role         string `json:"role"`
	Model        string `json:"model"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	LatencyMS    int64  `json:"latency_ms"`
	Attempt      int    `json:"attempt"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
}

func (s *server) postTelemetry(w http.ResponseWriter, r *http.Request) {
	var event llmTelemetry
	if !decodeJSONBody(w, r, &event) {
		return
	}
	event.Role = strings.TrimSpace(event.Role)
	event.Model = strings.TrimSpace(event.Model)
	if event.Role == "" || len(event.Role) > 128 || event.Model == "" || len(event.Model) > 128 ||
		event.InputTokens < 0 || event.OutputTokens < 0 ||
		event.LatencyMS < 0 || event.Attempt < 0 || !validTelemetryStatus(event.Status) || len(event.Error) > 128 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid telemetry"})
		return
	}
	payload := struct {
		llmTelemetry
		Subject string `json:"subject"`
	}{llmTelemetry: event, Subject: authenticatedSubject(r)}
	eventID, err := s.store.InsertEventWithID("llm_telemetry", payload)
	if err != nil {
		writeStoreError(w, "insert llm telemetry", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "event_id": eventID})
}

func validTelemetryStatus(status string) bool {
	switch status {
	case "success", "error", "invalid_output", "budget_refused":
		return true
	default:
		return false
	}
}
