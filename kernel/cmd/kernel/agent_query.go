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
	Workflow              string `json:"workflow"`
	Symbol                string `json:"symbol"`
	Query                 string `json:"query"`
	ConversationID        string `json:"conversation_id,omitempty"`
	ConversationCreatedAt string `json:"conversation_created_at,omitempty"`
}

type cortexSubmission struct {
	RunID                 string `json:"run_id"`
	ConversationID        string `json:"conversation_id"`
	ConversationCreatedAt string `json:"conversation_created_at"`
}

func legacyAgentQueryGone(w http.ResponseWriter, _ *http.Request) {
	writeAgentQueryError(w, http.StatusGone, "agent_query_retired", "use /agent/cortex-requests")
}

func (s *server) postCortexRequest(w http.ResponseWriter, r *http.Request) {
	var input agentQueryRequest
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
	input.Workflow = strings.TrimSpace(input.Workflow)
	input.Query = strings.TrimSpace(input.Query)
	// Workflow is retained only for compatibility with existing callers. Cortex
	// owns routing; no client-selected route enters the immutable UserRequest.
	input.Workflow = "auto"
	if !validAgentQuerySymbol(input.Symbol) || input.Query == "" || len(input.Query) > 4000 {
		writeAgentQueryError(w, http.StatusBadRequest, "cortex_input_invalid", "symbol and query are required")
		return
	}
	accepted, code := s.submitCortexRequest(r.Context(), input)
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code, "Cortex request was not accepted")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"id": accepted.RunID, "status": "running", "workflow": input.Workflow, "symbol": input.Symbol,
		"conversation_id": accepted.ConversationID, "conversation_created_at": accepted.ConversationCreatedAt, "trace": []any{}})
}

func (s *server) getCortexRun(w http.ResponseWriter, r *http.Request) {
	state, text, trace, code := s.fetchCortexRun(r.Context(), r.PathValue("id"))
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code, "Cortex result is unavailable")
		return
	}
	response := map[string]any{"id": r.PathValue("id"), "status": "running", "trace": trace}
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

func (s *server) getCortexConversation(w http.ResponseWriter, r *http.Request) {
	conversationID := strings.TrimSpace(r.PathValue("id"))
	if !validCortexConversationID(conversationID) {
		writeAgentQueryError(w, http.StatusBadRequest, "cortex_conversation_invalid", "Conversation is invalid")
		return
	}
	entries, code := s.fetchCortexConversation(r.Context(), conversationID)
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code, "Cortex conversation is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversation_id": conversationID, "entries": entries})
}

func (s *server) getCortexOperations(w http.ResponseWriter, r *http.Request) {
	raw, code := s.fetchCortexOperations(r.Context())
	if code != "" {
		writeAgentQueryError(w, http.StatusServiceUnavailable, code,
			"Cortex operations overview is unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
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

func (s *server) fetchCortexOperations(ctx context.Context) (json.RawMessage, string) {
	token, err := s.cortexToken()
	if err != nil {
		return nil, "cortex_credential_unavailable"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(s.cortexURL, "/")+"/v1/operations/overview", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "cortex_unavailable"
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(
		resp.Body, maxAgentQueryResponseBytes+1))
	if err != nil || int64(len(raw)) > maxAgentQueryResponseBytes {
		return nil, "cortex_response_invalid"
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "cortex_operations_unavailable"
	}
	var overview struct {
		GeneratedAt string `json:"generated_at"`
		Status      string `json:"status"`
		Cortex      struct {
			Status string `json:"status"`
		} `json:"cortex"`
		Research struct {
			Status string `json:"status"`
		} `json:"research"`
	}
	if json.Unmarshal(raw, &overview) != nil ||
		(overview.Status != "healthy" && overview.Status != "degraded") ||
		(overview.Cortex.Status != "healthy" &&
			overview.Cortex.Status != "degraded") ||
		(overview.Research.Status != "healthy" &&
			overview.Research.Status != "degraded" &&
			overview.Research.Status != "unavailable") {
		return nil, "cortex_response_invalid"
	}
	generatedAt, err := time.Parse(time.RFC3339Nano, overview.GeneratedAt)
	if err != nil || generatedAt.Location() != time.UTC {
		return nil, "cortex_response_invalid"
	}
	return json.RawMessage(raw), ""
}

func (s *server) submitCortexRequest(ctx context.Context, input agentQueryRequest) (cortexSubmission, string) {
	token, err := s.cortexToken()
	if err != nil {
		return cortexSubmission{}, "cortex_credential_unavailable"
	}
	id := store.NewID()
	now := time.Now().UTC()
	conversationID := strings.TrimSpace(input.ConversationID)
	conversationCreatedAt := now
	kind := "new_request"
	if conversationID != "" || strings.TrimSpace(input.ConversationCreatedAt) != "" {
		if !validCortexConversationID(conversationID) {
			return cortexSubmission{}, "cortex_conversation_invalid"
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(input.ConversationCreatedAt))
		if parseErr != nil || parsed.Location() != time.UTC || parsed.After(now) {
			return cortexSubmission{}, "cortex_conversation_invalid"
		}
		conversationCreatedAt = parsed
		kind = "continuation"
	} else {
		conversationID = "agent-lab-" + id
	}
	// A bounded Scout retry must survive a 120-second Worker lease expiry and
	// still leave time for the Desk continuation. Keep this below the Agent
	// Lab's nine-minute polling window.
	body := map[string]any{"conversation_id": conversationID, "conversation_created_at": conversationCreatedAt, "request_id": id, "kind": kind,
		"text": fmt.Sprintf("Symbol: %s\n\n%s", input.Symbol, input.Query), "idempotency_key": "agent-lab-" + id,
		"causation_id": id, "correlation_id": id, "deadline": now.Add(8 * time.Minute)}
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
		return cortexSubmission{}, "cortex_unavailable"
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes))
	if resp.StatusCode != http.StatusAccepted {
		return cortexSubmission{}, "cortex_rejected"
	}
	var accepted struct {
		RunID                 string    `json:"run_id"`
		ConversationID        string    `json:"conversation_id"`
		ConversationCreatedAt time.Time `json:"conversation_created_at"`
	}
	if json.Unmarshal(data, &accepted) != nil || accepted.RunID == "" || accepted.ConversationID != conversationID || accepted.ConversationCreatedAt.IsZero() ||
		!accepted.ConversationCreatedAt.Equal(conversationCreatedAt) {
		return cortexSubmission{}, "cortex_response_invalid"
	}
	return cortexSubmission{RunID: accepted.RunID, ConversationID: accepted.ConversationID,
		ConversationCreatedAt: accepted.ConversationCreatedAt.UTC().Format(time.RFC3339Nano)}, ""
}
func (s *server) fetchCortexRun(ctx context.Context, runID string) (string, string, []any, string) {
	token, err := s.cortexToken()
	if err != nil {
		return "", "", nil, "cortex_credential_unavailable"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cortexURL, "/")+"/v1/runs/"+runID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", nil, "cortex_unavailable"
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes))
	if resp.StatusCode != http.StatusOK {
		return "", "", nil, "cortex_result_unavailable"
	}
	var result struct {
		State string `json:"state"`
		Text  string `json:"text"`
		Trace []any  `json:"trace"`
	}
	if json.Unmarshal(data, &result) != nil || result.State == "" {
		return "", "", nil, "cortex_response_invalid"
	}
	return result.State, result.Text, result.Trace, ""
}

func (s *server) fetchCortexConversation(ctx context.Context, conversationID string) ([]any, string) {
	token, err := s.cortexToken()
	if err != nil {
		return nil, "cortex_credential_unavailable"
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(s.cortexURL, "/")+"/v1/conversations/"+conversationID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	client := s.runtimeHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "cortex_unavailable"
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxAgentQueryResponseBytes))
	if resp.StatusCode != http.StatusOK {
		return nil, "cortex_conversation_unavailable"
	}
	var result struct {
		ConversationID string `json:"conversation_id"`
		Entries        []any  `json:"entries"`
	}
	if json.Unmarshal(data, &result) != nil || result.ConversationID != conversationID || len(result.Entries) > 6 {
		return nil, "cortex_response_invalid"
	}
	return result.Entries, ""
}

func validCortexConversationID(value string) bool {
	if value == "" || len(value) > 200 || value != strings.TrimSpace(value) {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_') {
			return false
		}
	}
	return true
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
	body := map[string]any{"conversation_id": "agent-lab-" + job.ID, "conversation_created_at": now, "request_id": requestID, "kind": "new_request", "text": fmt.Sprintf("Symbol: %s\nWorkflow: %s\n\n%s", input.Symbol, input.Workflow, input.Query), "idempotency_key": "agent-lab-" + job.ID, "causation_id": job.ID, "correlation_id": job.ID, "deadline": now.Add(8 * time.Minute)}
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
