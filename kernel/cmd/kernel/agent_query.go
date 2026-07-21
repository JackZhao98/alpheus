package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"alpheus/kernel/internal/store"
)

const maxAgentQueryResponseBytes int64 = 1 << 20
const agentQueryLease = 7 * time.Minute
const agentQueryRecoveryBatch = 32

type agentQueryInput struct {
	Workflow     string `json:"workflow"`
	Symbol       string `json:"symbol"`
	Query        string `json:"query"`
	OpenAIAPIKey string `json:"openai_api_key"`
}

type agentQueryRequest struct {
	Workflow string `json:"workflow"`
	Symbol   string `json:"symbol"`
	Query    string `json:"query"`
}

// postAgentQuery creates a durable, non-trading MVP job. The encrypted model
// credential is loaded only after authentication and exists as plaintext only
// for the lifetime of the dispatcher call; it is never part of the job.
func (s *server) postAgentQuery(w http.ResponseWriter, r *http.Request) {
	var request agentQueryRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	input := agentQueryInput{Workflow: request.Workflow, Symbol: request.Symbol, Query: request.Query}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Workflow = strings.TrimSpace(input.Workflow)
	if input.Workflow == "" {
		input.Workflow = "scout"
	}
	input.Query = strings.TrimSpace(input.Query)
	if (input.Workflow != "auto" && input.Workflow != "scout" && input.Workflow != "team") || !validAgentQuerySymbol(input.Symbol) || input.Query == "" || len(input.Query) > 4000 {
		writeAgentQueryError(w, http.StatusBadRequest, "agent_query_input_invalid", "symbol and query are required")
		return
	}
	openAIAPIKey, err := s.loadAgentSecret("openai")
	if err != nil || !validAgentAPIKey(openAIAPIKey) {
		writeAgentQueryError(w, http.StatusBadRequest, "agent_query_openai_credential_unavailable", "OpenAI API token is not configured")
		return
	}
	input.OpenAIAPIKey = openAIAPIKey

	if s.store == nil {
		writeAgentQueryError(w, http.StatusServiceUnavailable, "agent_query_store_unavailable", "agent query store unavailable")
		return
	}
	job, err := s.store.CreateAgentQueryJob(authenticatedSubject(r), input.Workflow, input.Symbol, input.Query)
	if err != nil {
		writeAgentQueryError(w, http.StatusServiceUnavailable, "agent_query_store_unavailable", "agent query store unavailable")
		return
	}
	go s.executeAgentQuery(job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

func (s *server) getAgentQueryJob(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeAgentQueryError(w, http.StatusServiceUnavailable, "agent_query_store_unavailable", "agent query store unavailable")
		return
	}
	job, err := s.store.GetAgentQueryJob(r.PathValue("id"))
	if err != nil {
		writeAgentQueryError(w, http.StatusServiceUnavailable, "agent_query_store_unavailable", "agent query store unavailable")
		return
	}
	if job == nil || job.Subject != authenticatedSubject(r) {
		writeAgentQueryError(w, http.StatusNotFound, "agent_query_job_not_found", "agent query job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *server) executeAgentQuery(jobID string) {
	job, err := s.store.ClaimAgentQueryJob(jobID, agentQueryLease)
	if err != nil || job == nil {
		return
	}
	input := agentQueryInput{Workflow: job.Workflow, Symbol: job.Symbol, Query: job.Query}
	openAIAPIKey, err := s.loadAgentSecret("openai")
	if err != nil || !validAgentAPIKey(openAIAPIKey) {
		_, _ = s.store.FailClaimedAgentQueryJob(job.ID, job.ClaimToken, "credential_unavailable")
		return
	}
	input.OpenAIAPIKey = openAIAPIKey
	if !s.traceAgentQuery(job, "credential_loaded", "") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 390*time.Second)
	defer cancel()
	if !s.traceAgentQuery(job, "runtime_request_started", "") {
		return
	}
	result, errorCode := s.callAgentRuntime(ctx, input)
	if errorCode != "" {
		if !s.traceAgentQuery(job, "runtime_request_failed", errorCode) {
			return
		}
		_, _ = s.store.FailClaimedAgentQueryJob(job.ID, job.ClaimToken, errorCode)
		return
	}
	if !s.traceAgentQuery(job, "runtime_response_received", "") {
		return
	}
	completed, err := s.store.CompleteClaimedAgentQueryJob(job.ID, job.ClaimToken, result)
	if err != nil || !completed {
		return
	}
	s.store.Event("agent_query", map[string]any{"workflow": input.Workflow, "symbol": input.Symbol, "attempt": job.Attempt})
}

func (s *server) traceAgentQuery(job *store.AgentQueryJob, stage, errorCode string) bool {
	recorded, err := s.store.RecordAgentQueryJobTrace(job.ID, job.ClaimToken, stage, errorCode)
	if err == nil && recorded {
		return true
	}
	_, _ = s.store.FailClaimedAgentQueryJob(job.ID, job.ClaimToken, "agent_query_trace_unavailable")
	return false
}

func startAgentQueryRecovery(s *server) error {
	if err := s.recoverAgentQueryJobs(); err != nil {
		return err
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := s.recoverAgentQueryJobs(); err != nil {
				log.Printf("agent query recovery: %v", err)
			}
		}
	}()
	return nil
}

func (s *server) recoverAgentQueryJobs() error {
	jobs, err := s.store.ListRecoverableAgentQueryJobs(agentQueryRecoveryBatch)
	if err != nil {
		return err
	}
	for i := range jobs {
		go s.executeAgentQuery(jobs[i].ID)
	}
	return nil
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
		client = &http.Client{Timeout: 385 * time.Second}
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

func writeAgentQueryError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"error_code": code, "error": message})
}
