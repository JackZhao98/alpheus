package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/runtimecontract"
	"alpheus/agentplatform/security"
	_ "github.com/lib/pq"
)

const workerRole = "alpheus_agent_worker"

type worker struct {
	db                                                 *sql.DB
	store                                              *blob.LocalStore
	principal, apiKey, controlURL, controlToken, model string
	http                                               *http.Client
}
type workItem struct {
	TaskID         string       `json:"task_id"`
	TaskGeneration int64        `json:"task_state_generation"`
	OutputDigest   string       `json:"output_contract_digest"`
	Deadline       time.Time    `json:"deadline"`
	Context        blob.BlobRef `json:"context_manifest"`
	Raw            blob.BlobRef `json:"raw_input"`
	RawBinding     string       `json:"raw_input_binding_id"`
}
type claimResult struct {
	Status            string `json:"status"`
	AttemptID         string `json:"attempt_id"`
	LeaseToken        string `json:"lease_token"`
	AttemptGeneration int64  `json:"attempt_state_generation"`
	LeaseGeneration   int64  `json:"lease_generation"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	dbURL, err := secret("CORTEX_WORKER_DATABASE_URL_FILE")
	if err != nil {
		return err
	}
	apiKey, err := secret("OPENAI_API_KEY_FILE")
	if err != nil {
		return err
	}
	controlToken, err := secret("CORTEX_WORKER_CONTROL_TOKEN_FILE")
	if err != nil {
		return err
	}
	store, err := blob.NewLocalStore(env("CORTEX_BLOB_ROOT", "/var/lib/alpheus/cortex-blobs"))
	if err != nil {
		return err
	}
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	w := &worker{db: db, store: store, principal: env("CORTEX_WORKER_PRINCIPAL_ID", "cortex-worker-1"), apiKey: apiKey,
		controlURL: strings.TrimRight(env("CORTEX_CONTROL_URL", "http://cortex-input:8400"), "/"), controlToken: controlToken,
		model: env("CORTEX_MODEL", "gpt-5.6-sol"), http: &http.Client{Timeout: 85 * time.Second}}
	interval := 2 * time.Second
	log.Printf("Cortex Worker listening for canonical Tasks as %s with %s", w.principal, w.model)
	for {
		item, err := w.next(context.Background())
		if err != nil {
			log.Printf("discover Task: %v", err)
			time.Sleep(interval)
			continue
		}
		if item == nil {
			time.Sleep(interval)
			continue
		}
		if err := w.execute(context.Background(), *item); err != nil {
			log.Printf("Task %s failed: %v", item.TaskID, err)
		}
	}
}

func (w *worker) execute(ctx context.Context, item workItem) error {
	prompt, err := w.readBlob(ctx, item.Raw, item.RawBinding)
	if err != nil {
		return fmt.Errorf("read UserRequest: %w", err)
	}
	claimCmd := runtimecontract.ClaimTaskCommand{SchemaRevision: 1, Envelope: w.envelope("claim_task", item.TaskID, item.Deadline), TaskID: item.TaskID, ExpectedTaskStateGeneration: item.TaskGeneration, RequestedLeaseSeconds: 120}
	var claim claimResult
	if err := w.command(ctx, "claim_task", claimCmd, &claim); err != nil {
		return err
	}
	if claim.Status != "committed" {
		return fmt.Errorf("claim denied")
	}
	start := runtimecontract.StartAttemptCommand{SchemaRevision: 1, Envelope: w.envelope("start_attempt", claim.AttemptID, item.Deadline), AttemptID: claim.AttemptID, ExpectedAttemptStateGeneration: claim.AttemptGeneration, LeaseGeneration: claim.LeaseGeneration, LeaseToken: claim.LeaseToken}
	var started struct {
		Status            string `json:"status"`
		AttemptGeneration int64  `json:"attempt_state_generation"`
	}
	if err := w.command(ctx, "start_attempt", start, &started); err != nil {
		return err
	}
	if started.Status != "committed" {
		return fmt.Errorf("start denied")
	}
	callID := uuid()
	turnID := uuid()
	idem := uuid()
	requestBody := openAIRequest(w.model, string(prompt))
	requestRaw, _ := json.Marshal(requestBody)
	requestDigest := digest(requestRaw)
	promptDigest := digest(prompt)
	dispatch := runtimecontract.DispatchModelCallCommand{SchemaRevision: 1, Envelope: w.envelope("dispatch_model_call", callID, item.Deadline), AttemptID: claim.AttemptID, ExpectedAttemptStateGeneration: started.AttemptGeneration, LeaseGeneration: claim.LeaseGeneration, LeaseToken: claim.LeaseToken, TurnID: turnID, Manifest: runtimecontract.ModelCallManifestCandidate{CallID: callID, IdempotencyKey: idem, Provider: "openai", Model: w.model, PromptDigest: promptDigest, ContextManifest: item.Context, OutputContractDigest: item.OutputDigest, RequestDigest: requestDigest, MaxOutputTokens: 2000, ReservedInputTokens: reservedInputTokens(requestRaw), ReservedExternalCostMicroUSD: 0, TimeoutMS: 75000, TemperatureMicros: 0}}
	var dispatched struct {
		Status         string `json:"status"`
		ManifestDigest string `json:"manifest_digest"`
		TurnGeneration int64  `json:"turn_state_generation"`
	}
	if err := w.command(ctx, "dispatch_model_call", dispatch, &dispatched); err != nil {
		return err
	}
	if dispatched.Status != "committed" {
		return fmt.Errorf("dispatch denied")
	}
	providerCtx, cancelProvider := context.WithTimeout(ctx, 75*time.Second)
	heartbeatDone := make(chan error, 1)
	go func() {
		heartbeatDone <- w.heartbeatLoop(providerCtx, item, claim, started.AttemptGeneration, 20*time.Second)
	}()
	startedAt := time.Now()
	response, uncertain, err := w.callOpenAI(providerCtx, requestBody, idem)
	cancelProvider()
	if heartbeatErr := <-heartbeatDone; heartbeatErr != nil {
		log.Printf("Attempt %s heartbeat stopped: %v", claim.AttemptID, heartbeatErr)
	}
	wall := time.Since(startedAt).Milliseconds()
	if err != nil {
		failure := contracts.Failure{Code: "openai_request_failed", Message: bounded(err.Error()), Retryable: true}
		if uncertain {
			_ = w.markUnknown(ctx, item, claim, started.AttemptGeneration, turnID, dispatched.TurnGeneration, failure)
		} else {
			_ = w.resolveFailure(ctx, item, claim, started.AttemptGeneration, turnID, dispatched.TurnGeneration, runtimecontract.RetryInfrastructure, failure)
		}
		return err
	}
	outputRaw, err := extractOutput(response)
	if err != nil {
		failure := contracts.Failure{Code: "openai_output_invalid", Message: bounded(err.Error()), Retryable: true}
		_ = w.resolveFailure(ctx, item, claim, started.AttemptGeneration, turnID, dispatched.TurnGeneration, runtimecontract.RetryInvalidOutput, failure)
		return err
	}
	outputRef, err := w.publishWithRetry(ctx, callID, dispatched.ManifestDigest, outputRaw)
	if err != nil {
		failure := contracts.Failure{Code: "model_output_commit_failed", Message: bounded(err.Error()), Retryable: true}
		_ = w.resolveFailure(ctx, item, claim, started.AttemptGeneration, turnID, dispatched.TurnGeneration, runtimecontract.RetryInfrastructure, failure)
		return err
	}
	resultCandidate := runtimecontract.ModelCallResultCandidate{CallID: callID, RequestDigest: requestDigest, ProviderRequestID: response.ID, Output: outputRef, InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens, ExternalCostMicroUSD: 0, WallTimeMS: wall, FinishReason: runtimecontract.FinishStop}
	resolve := runtimecontract.ResolveModelCallCommand{SchemaRevision: 1, Envelope: w.envelope("resolve_model_call", callID+"-resolve", item.Deadline), AttemptID: claim.AttemptID, ExpectedAttemptStateGeneration: started.AttemptGeneration, LeaseGeneration: claim.LeaseGeneration, LeaseToken: claim.LeaseToken, TurnID: turnID, ExpectedTurnStateGeneration: dispatched.TurnGeneration, Outcome: runtimecontract.TurnResultCommitted, Result: &resultCandidate}
	var resolved struct {
		Status       string `json:"status"`
		ResultID     string `json:"result_id"`
		ResultDigest string `json:"result_digest"`
	}
	if err := w.command(ctx, "resolve_model_call", resolve, &resolved); err != nil {
		return err
	}
	if resolved.Status != "committed" {
		return fmt.Errorf("resolve denied")
	}
	resultRef := contracts.RecordRef{Owner: contracts.OwnerAgentControl, RecordType: "model_call_result", RecordID: resolved.ResultID, SchemaRevision: 1, RecordDigest: resolved.ResultDigest}
	commit := runtimecontract.CommitAttemptCommand{SchemaRevision: 1, Envelope: w.envelope("commit_attempt", callID+"-commit", item.Deadline), AttemptID: claim.AttemptID, ExpectedAttemptStateGeneration: started.AttemptGeneration, LeaseGeneration: claim.LeaseGeneration, LeaseToken: claim.LeaseToken, Result: resultRef, Artifact: runtimecontract.ArtifactCandidate{ArtifactType: "assistant_response", OutputContractDigest: item.OutputDigest, EffectClass: contracts.EffectNone, Sections: []runtimecontract.ArtifactSection{{Name: "response", Required: true, Content: outputRef}}}}
	var committed struct {
		Status     string `json:"status"`
		ArtifactID string `json:"artifact_id"`
		RunState   string `json:"run_state"`
	}
	if err := w.command(ctx, "commit_attempt", commit, &committed); err != nil {
		return err
	}
	if committed.Status != "committed" {
		return fmt.Errorf("commit denied")
	}
	log.Printf("Cortex Task %s succeeded with Artifact %s", item.TaskID, committed.ArtifactID)
	return nil
}

type openAIResponse struct {
	ID, Status string
	Output     []struct {
		Type, Role string
		Content    []struct{ Type, Text, Refusal string }
	}
	Usage struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	}
}

func openAIRequest(model, prompt string) map[string]any {
	return map[string]any{"model": model, "instructions": "Answer the user's request directly and accurately. Return only the required JSON object.", "input": prompt, "store": false, "max_output_tokens": 2000, "reasoning": map[string]any{"effort": "low"}, "text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "assistant_response", "strict": true, "schema": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"text"}, "properties": map[string]any{"text": map[string]any{"type": "string"}}}}}}
}
func (w *worker) callOpenAI(ctx context.Context, body any, idem string) (openAIResponse, bool, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(raw))
	if err != nil {
		return openAIResponse{}, false, err
	}
	req.Header.Set("Authorization", "Bearer "+w.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", idem)
	resp, err := w.http.Do(req)
	if err != nil {
		return openAIResponse{}, true, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return openAIResponse{}, true, err
	}
	if resp.StatusCode/100 != 2 {
		return openAIResponse{}, false, fmt.Errorf("OpenAI status %d: %s", resp.StatusCode, bounded(string(data)))
	}
	var out openAIResponse
	if json.Unmarshal(data, &out) != nil || out.ID == "" {
		return out, false, fmt.Errorf("invalid OpenAI response")
	}
	return out, false, nil
}
func extractOutput(r openAIResponse) ([]byte, error) {
	for _, o := range r.Output {
		if o.Type == "message" {
			for _, c := range o.Content {
				if c.Type == "output_text" && c.Text != "" {
					var v struct {
						Text string `json:"text"`
					}
					if json.Unmarshal([]byte(c.Text), &v) == nil && strings.TrimSpace(v.Text) != "" {
						return []byte(c.Text), nil
					}
				}
			}
		}
	}
	return nil, fmt.Errorf("OpenAI response contained no valid assistant output")
}

func (w *worker) next(ctx context.Context) (*workItem, error) {
	var raw sql.NullString
	err := w.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT agent_control.next_cortex_task()::TEXT").Scan(&raw)
	})
	if err != nil {
		return nil, err
	}
	if !raw.Valid || raw.String == "null" {
		return nil, nil
	}
	var item workItem
	if err := json.Unmarshal([]byte(raw.String), &item); err != nil {
		return nil, err
	}
	return &item, nil
}
func (w *worker) command(ctx context.Context, name string, command, out any) error {
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	var response []byte
	err = w.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT agent_control."+name+"($1)::TEXT", string(raw)).Scan(&response)
	})
	if err != nil {
		return err
	}
	if err := json.Unmarshal(response, out); err != nil {
		return err
	}
	return nil
}
func (w *worker) withRole(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL ROLE "+workerRole); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (w *worker) envelope(kind, key string, deadline time.Time) contracts.CommandEnvelope {
	return contracts.CommandEnvelope{SchemaRevision: 1, CommandID: uuid(), Actor: contracts.AuditActor{PrincipalID: w.principal, Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceWorker}, Audience: contracts.AudienceControlAPI, CommandType: kind, IdempotencyKey: key, RequestDigest: digest([]byte(kind + "\n" + key)), CausationID: key, CorrelationID: key, Deadline: deadline.UTC()}
}
func (w *worker) AuthorizeBlobRead(ctx context.Context, r blob.ReadRequest) (blob.ReadAuthorization, error) {
	var a blob.ReadAuthorization
	a.PrincipalID = w.principal
	a.BindingID = r.BindingID
	a.OwningReference = r.OwningReference
	a.Blob.Origin = r.OwningReference
	err := w.withRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT schema_revision,blob_id::TEXT,content_digest,media_type,size_bytes,origin_owner,origin_record_type,origin_record_id,origin_record_digest,committed_at,authorized_at,valid_until FROM blob.authorize_read($1,$2,$3,$4,$5,$6,$7)`, w.principal, r.BindingID, r.BlobID, r.OwningReference.Owner, r.OwningReference.RecordType, r.OwningReference.RecordID, r.OwningReference.RecordDigest).Scan(&a.Blob.SchemaRevision, &a.Blob.BlobID, &a.Blob.ContentDigest, &a.Blob.MediaType, &a.Blob.SizeBytes, &a.Blob.Origin.Owner, &a.Blob.Origin.RecordType, &a.Blob.Origin.RecordID, &a.Blob.Origin.RecordDigest, &a.Blob.CommittedAt, &a.AuthorizedAt, &a.ValidUntil)
	})
	a.Blob.Origin.SchemaRevision = 1
	a.Blob.CommittedAt = a.Blob.CommittedAt.UTC()
	a.AuthorizedAt = a.AuthorizedAt.UTC()
	a.ValidUntil = a.ValidUntil.UTC()
	return a, err
}
func (w *worker) readBlob(ctx context.Context, ref blob.BlobRef, binding string) ([]byte, error) {
	read, err := w.store.OpenVerified(ctx, blob.ReadRequest{PrincipalID: w.principal, BindingID: binding, BlobID: ref.BlobID, OwningReference: ref.Origin}, w)
	if err != nil {
		return nil, err
	}
	defer read.Close()
	return io.ReadAll(io.LimitReader(read, ref.SizeBytes+1))
}
func (w *worker) publish(ctx context.Context, call, digestValue string, output []byte) (blob.BlobRef, error) {
	body, _ := json.Marshal(map[string]any{"call_id": call, "manifest_digest": digestValue, "output": json.RawMessage(output)})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, w.controlURL+"/internal/v1/model-outputs", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+w.controlToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.http.Do(req)
	if err != nil {
		return blob.BlobRef{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return blob.BlobRef{}, fmt.Errorf("Control output commit status %d", resp.StatusCode)
	}
	var ref blob.BlobRef
	err = json.NewDecoder(resp.Body).Decode(&ref)
	return ref, err
}

func (w *worker) publishWithRetry(ctx context.Context, call, digestValue string, output []byte) (blob.BlobRef, error) {
	delays := []time.Duration{0, 150 * time.Millisecond, 500 * time.Millisecond}
	var last error
	for _, delay := range delays {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return blob.BlobRef{}, ctx.Err()
			case <-time.After(delay):
			}
		}
		ref, err := w.publish(ctx, call, digestValue, output)
		if err == nil {
			return ref, nil
		}
		last = err
	}
	return blob.BlobRef{}, last
}

func (w *worker) heartbeatLoop(ctx context.Context, item workItem, c claimResult, attemptGeneration int64, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			key := c.AttemptID + "-heartbeat-" + uuid()
			command := runtimecontract.HeartbeatAttemptCommand{
				SchemaRevision: 1,
				Envelope:       w.envelope("heartbeat_attempt", key, item.Deadline),
				AttemptID:      c.AttemptID, ExpectedAttemptStateGeneration: attemptGeneration,
				LeaseGeneration: c.LeaseGeneration, LeaseToken: c.LeaseToken,
				RequestedExtensionSeconds: 120,
			}
			var result struct {
				Status string `json:"status"`
			}
			if err := w.command(ctx, "heartbeat_attempt", command, &result); err != nil {
				return err
			}
			if result.Status != "committed" {
				return fmt.Errorf("heartbeat denied")
			}
		}
	}
}
func (w *worker) resolveFailure(ctx context.Context, item workItem, c claimResult, gen int64, turn string, turnGen int64, retryClass runtimecontract.RetryClass, f contracts.Failure) error {
	resolve := runtimecontract.ResolveModelCallCommand{SchemaRevision: 1, Envelope: w.envelope("resolve_model_call", turn+"-failed", item.Deadline), AttemptID: c.AttemptID, ExpectedAttemptStateGeneration: gen, LeaseGeneration: c.LeaseGeneration, LeaseToken: c.LeaseToken, TurnID: turn, ExpectedTurnStateGeneration: turnGen, Outcome: runtimecontract.TurnFailed, Failure: &f}
	var resolved struct {
		Status string `json:"status"`
	}
	if err := w.command(ctx, "resolve_model_call", resolve, &resolved); err != nil {
		return err
	}
	if resolved.Status != "committed" {
		return fmt.Errorf("failure resolution denied")
	}
	fail := runtimecontract.FailAttemptCommand{SchemaRevision: 1, Envelope: w.envelope("fail_attempt", turn+"-attempt-failed", item.Deadline), AttemptID: c.AttemptID, ExpectedAttemptStateGeneration: gen, LeaseGeneration: c.LeaseGeneration, LeaseToken: c.LeaseToken, RetryClass: retryClass, Failure: f}
	var failed struct {
		Status string `json:"status"`
	}
	if err := w.command(ctx, "fail_attempt", fail, &failed); err != nil {
		return err
	}
	if failed.Status != "committed" {
		return fmt.Errorf("attempt failure denied")
	}
	return nil
}
func (w *worker) markUnknown(ctx context.Context, item workItem, c claimResult, gen int64, turn string, turnGen int64, f contracts.Failure) error {
	command := runtimecontract.MarkModelCallUnknownCommand{SchemaRevision: 1, Envelope: w.envelope("mark_model_call_unknown", turn+"-unknown", item.Deadline), AttemptID: c.AttemptID, ExpectedAttemptStateGeneration: gen, LeaseGeneration: c.LeaseGeneration, LeaseToken: c.LeaseToken, TurnID: turn, ExpectedTurnStateGeneration: turnGen, Failure: f}
	var result struct {
		Status string `json:"status"`
	}
	return w.command(ctx, "mark_model_call_unknown", command, &result)
}
func secret(name string) (string, error) {
	path := strings.TrimSpace(os.Getenv(name))
	if path == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	raw, err := security.LoadSecret(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}
func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
func digest(v []byte) string { s := sha256.Sum256(v); return hex.EncodeToString(s[:]) }
func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func bounded(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 900 {
		return s[:900]
	}
	if s == "" {
		return "request failed"
	}
	return s
}

func reservedInputTokens(request []byte) int64 {
	// UTF-8 byte length is a conservative token ceiling for ordinary BPE input;
	// double it and add framing headroom for provider-side message encoding.
	reserved := int64(len(request))*2 + 2048
	if reserved > 1000000 {
		return 1000000
	}
	return reserved
}
