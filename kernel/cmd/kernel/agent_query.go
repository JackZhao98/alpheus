package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxAgentQueryResponseBytes int64 = 1 << 20

type agentQueryInput struct {
	Workflow     string `json:"workflow"`
	Symbol       string `json:"symbol"`
	Query        string `json:"query"`
	OpenAIAPIKey string `json:"openai_api_key"`
}

// postAgentQuery creates a durable, non-trading MVP job. The model credential
// is handed directly to the short-lived dispatcher and is never persisted.
func (s *server) postAgentQuery(w http.ResponseWriter, r *http.Request) {
	var input agentQueryInput
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Workflow = strings.TrimSpace(input.Workflow)
	if input.Workflow == "" {
		input.Workflow = "scout"
	}
	input.Query = strings.TrimSpace(input.Query)
	input.OpenAIAPIKey = strings.TrimSpace(input.OpenAIAPIKey)
	if (input.Workflow != "scout" && input.Workflow != "team") || !validAgentQuerySymbol(input.Symbol) || input.Query == "" || len(input.Query) > 4000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "symbol and query are required"})
		return
	}
	if input.OpenAIAPIKey == "" || !validAgentAPIKey(input.OpenAIAPIKey) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "OpenAI API token is required"})
		return
	}

	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent query store unavailable"})
		return
	}
	job, err := s.store.CreateAgentQueryJob(authenticatedSubject(r), input.Workflow, input.Symbol, input.Query)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent query store unavailable"})
		return
	}
	go s.executeAgentQuery(job.ID, input)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) getAgentQueryJob(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent query store unavailable"})
		return
	}
	job, err := s.store.GetAgentQueryJob(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent query store unavailable"})
		return
	}
	if job == nil || job.Subject != authenticatedSubject(r) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent query job not found"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *server) executeAgentQuery(jobID string, input agentQueryInput) {
	started, err := s.store.StartAgentQueryJob(jobID)
	if err != nil || !started {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 270*time.Second)
	defer cancel()
	result, errorCode := s.callAgentRuntime(ctx, input)
	if errorCode != "" {
		_, _ = s.store.FailAgentQueryJob(jobID, errorCode)
		return
	}
	completed, err := s.store.CompleteAgentQueryJob(jobID, result)
	if err != nil || !completed {
		return
	}
	s.store.Event("agent_query", map[string]string{"workflow": input.Workflow, "symbol": input.Symbol})
}

func (s *server) callAgentRuntime(ctx context.Context, input agentQueryInput) (json.RawMessage, string) {
	body, err := json.Marshal(input)
	if err != nil {
		return nil, "request_encode_failed"
	}
	runtimeURL := strings.TrimRight(s.runtimeURL, "/")
	if runtimeURL == "" {
		runtimeURL = "http://agent-runtime:8200"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, runtimeURL+"/query", bytes.NewReader(body))
	if err != nil {
		return nil, "request_encode_failed"
	}
	req.Header.Set("Content-Type", "application/json")
	if s.mode.KernelToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.mode.KernelToken)
	}
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 265 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "runtime_unavailable"
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes+1))
	if err != nil || int64(len(raw)) > maxAgentQueryResponseBytes {
		return nil, "runtime_response_invalid"
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "runtime_rejected"
	}
	var output any
	if err := json.Unmarshal(raw, &output); err != nil || output == nil {
		return nil, "runtime_response_invalid"
	}
	return json.RawMessage(raw), ""
}

func validAgentAPIKey(value string) bool {
	if len(value) > 512 {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func validAgentQuerySymbol(symbol string) bool {
	if len(symbol) == 0 || len(symbol) > 16 {
		return false
	}
	for _, char := range symbol {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' {
			continue
		}
		return false
	}
	return true
}
