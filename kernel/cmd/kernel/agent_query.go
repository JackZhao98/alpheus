package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
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

func (s *server) postCortexRequest(w http.ResponseWriter, r *http.Request) {
	var input agentQueryRequest
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Workflow = strings.TrimSpace(input.Workflow)
	input.Query = strings.TrimSpace(input.Query)
	if input.Workflow == "" {
		input.Workflow = "scout"
	}
	if (input.Workflow != "auto" && input.Workflow != "scout" && input.Workflow != "team") || !validAgentQuerySymbol(input.Symbol) || input.Query == "" || len(input.Query) > 4000 {
		writeAgentQueryError(w, http.StatusBadRequest, "cortex_input_invalid", "symbol and query are required")
		return
	}
	runID, code := s.submitCortexRequest(r.Context(), input)
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code, "Cortex request was not accepted")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": runID, "status": "running", "workflow": input.Workflow, "symbol": input.Symbol, "trace": []any{}})
}

func (s *server) getCortexRun(w http.ResponseWriter, r *http.Request) {
	state, text, code := s.fetchCortexRun(r.Context(), r.PathValue("id"))
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code, "Cortex result is unavailable")
		return
	}
	response := map[string]any{"id": r.PathValue("id"), "status": "running", "trace": []any{}}
	if state == "succeeded" {
		response["status"] = "succeeded"
		response["result"] = map[string]any{"workflow": "answer", "cognition": "llm", "model": "gpt-5.6-sol", "answer": text, "run_id": r.PathValue("id")}
	}
	if state == "failed" || state == "canceled" || state == "dead_lettered" {
		response["status"] = "failed"
		response["error_code"] = "cortex_run_failed"
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) cortexToken() (string, error) {
	raw, err := os.ReadFile(s.cortexTokenFile)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("empty Cortex token")
	}
	return token, nil
}
func (s *server) submitCortexRequest(ctx context.Context, input agentQueryRequest) (string, string) {
	token, err := s.cortexToken()
	if err != nil {
		return "", "cortex_credential_unavailable"
	}
	id := store.NewID()
	now := time.Now().UTC()
	body := map[string]any{"conversation_id": "agent-lab-" + id, "conversation_created_at": now, "request_id": id, "kind": "new_request", "text": fmt.Sprintf("Symbol: %s\nWorkflow: %s\n\n%s", input.Symbol, input.Workflow, input.Query), "idempotency_key": "agent-lab-" + id, "causation_id": id, "correlation_id": id, "deadline": now.Add(5 * time.Minute)}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cortexURL, "/")+"/v1/user-requests", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "cortex_unavailable"
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes))
	if resp.StatusCode != http.StatusAccepted {
		return "", "cortex_rejected"
	}
	var accepted struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(data, &accepted) != nil || accepted.RunID == "" {
		return "", "cortex_response_invalid"
	}
	return accepted.RunID, ""
}
func (s *server) fetchCortexRun(ctx context.Context, runID string) (string, string, string) {
	token, err := s.cortexToken()
	if err != nil {
		return "", "", "cortex_credential_unavailable"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cortexURL, "/")+"/v1/runs/"+runID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "cortex_unavailable"
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes))
	if resp.StatusCode != http.StatusOK {
		return "", "", "cortex_result_unavailable"
	}
	var result struct{ State, Text string }
	if json.Unmarshal(data, &result) != nil || result.State == "" {
		return "", "", "cortex_response_invalid"
	}
	return result.State, result.Text, ""
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
	if strings.TrimSpace(s.cortexURL) == "" {
		openAIAPIKey, err := s.loadAgentSecret("openai")
		if err != nil || !validAgentAPIKey(openAIAPIKey) {
			writeAgentQueryError(w, http.StatusBadRequest, "agent_query_openai_credential_unavailable", "OpenAI API token is not configured")
			return
		}
		input.OpenAIAPIKey = openAIAPIKey
	}

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
	if strings.TrimSpace(s.cortexURL) != "" {
		if !s.traceAgentQuery(job, "runtime_request_started", "") {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 390*time.Second)
		defer cancel()
		result, errorCode := s.callCortex(ctx, job, input)
		if errorCode != "" {
			_, _ = s.store.FailClaimedAgentQueryJob(job.ID, job.ClaimToken, errorCode)
			return
		}
		if !s.traceAgentQuery(job, "runtime_response_received", "") {
			return
		}
		completed, err := s.store.CompleteClaimedAgentQueryJob(job.ID, job.ClaimToken, result)
		if err == nil && completed {
			s.store.Event("cortex_agent_query", map[string]any{"workflow": input.Workflow, "symbol": input.Symbol})
		}
		return
	}
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

func (s *server) callCortex(ctx context.Context, job *store.AgentQueryJob, input agentQueryInput) (json.RawMessage, string) {
	tokenRaw, err := os.ReadFile(s.cortexTokenFile)
	if err != nil {
		return nil, "cortex_credential_unavailable"
	}
	token := strings.TrimSpace(string(tokenRaw))
	if token == "" {
		return nil, "cortex_credential_unavailable"
	}
	now := time.Now().UTC()
	requestID := job.ID
	body := map[string]any{"conversation_id": "agent-lab-" + job.ID, "conversation_created_at": now, "request_id": requestID, "kind": "new_request", "text": fmt.Sprintf("Symbol: %s\nWorkflow: %s\n\n%s", input.Symbol, input.Workflow, input.Query), "idempotency_key": "agent-lab-" + job.ID, "causation_id": job.ID, "correlation_id": job.ID, "deadline": now.Add(5 * time.Minute)}
	raw, _ := json.Marshal(body)
	base := strings.TrimRight(s.cortexURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/user-requests", bytes.NewReader(raw))
	if err != nil {
		return nil, "cortex_request_invalid"
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "cortex_unavailable"
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes))
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return nil, "cortex_rejected"
	}
	var accepted struct {
		RunID string `json:"run_id"`
	}
	if json.Unmarshal(data, &accepted) != nil || accepted.RunID == "" {
		return nil, "cortex_response_invalid"
	}
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, "cortex_timeout"
		case <-ticker.C:
			statusReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/runs/"+accepted.RunID, nil)
			statusReq.Header.Set("Authorization", "Bearer "+token)
			statusResp, err := client.Do(statusReq)
			if err != nil {
				continue
			}
			statusRaw, _ := io.ReadAll(io.LimitReader(statusResp.Body, maxAgentQueryResponseBytes))
			statusResp.Body.Close()
			if statusResp.StatusCode != http.StatusOK {
				continue
			}
			var status struct{ State, Text string }
			if json.Unmarshal(statusRaw, &status) != nil {
				continue
			}
			if status.State == "succeeded" {
				result, _ := json.Marshal(map[string]any{"workflow": "answer", "cognition": "llm", "model": "gpt-5.6-sol", "answer": status.Text, "run_id": accepted.RunID})
				return result, ""
			}
			if status.State == "failed" || status.State == "canceled" || status.State == "dead_lettered" {
				return nil, "cortex_run_failed"
			}
		}
	}
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
