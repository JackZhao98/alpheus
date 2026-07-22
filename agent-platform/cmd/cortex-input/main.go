package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"database/sql"
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
	"alpheus/agentplatform/inputgateway"
	"alpheus/agentplatform/outputcontract"
	"alpheus/agentplatform/security"
	_ "github.com/lib/pq"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	principal := env("CORTEX_PRINCIPAL_ID", "cortex-control-1")
	subjectID := env("CORTEX_SUBJECT_PRINCIPAL_ID", "owner-1")
	databaseURL, err := loadSecretEnv("CORTEX_DATABASE_URL_FILE")
	if err != nil {
		return err
	}
	serviceToken, err := loadSecretEnv("CORTEX_INPUT_TOKEN_FILE")
	if err != nil {
		return err
	}
	workerToken, err := loadSecretEnv("CORTEX_WORKER_CONTROL_TOKEN_FILE")
	if err != nil {
		return err
	}
	researchToken, err := loadSecretEnv("CORTEX_RESEARCH_TOKEN_FILE")
	if err != nil {
		return err
	}
	researchURL := strings.TrimRight(env("CORTEX_RESEARCH_URL", "http://research-gateway:8300"), "/")
	researchHTTP := &http.Client{Timeout: 25 * time.Second}
	store, err := blob.NewLocalStore(env("CORTEX_BLOB_ROOT", "/var/lib/alpheus/cortex-blobs"))
	if err != nil {
		return fmt.Errorf("open Cortex BlobStore: %w", err)
	}
	database, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("open Cortex database: %w", err)
	}
	defer database.Close()
	database.SetMaxOpenConns(8)
	database.SetMaxIdleConns(4)
	database.SetConnMaxLifetime(30 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("ping Cortex database: %w", err)
	}
	adapter, err := inputgateway.NewPostgresAdapter(database, store, principal)
	if err != nil {
		return err
	}
	recoveryContext, stopRecovery := context.WithCancel(context.Background())
	defer stopRecovery()
	startCortexWebFetchRecovery(recoveryContext, adapter, researchHTTP, researchURL, researchToken)
	startCortexScoutContinuationRecovery(recoveryContext, adapter)
	answerSchema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object", "additionalProperties": false,
		"required":   []string{"text"},
		"properties": map[string]any{"text": map[string]any{"type": "string"}},
	}
	workflowSchema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object", "additionalProperties": false,
		// OpenAI strict schemas require one closed shape at the root.  The
		// Worker applies the kind/target semantic rules after validation.
		"required": []string{"kind", "target", "objective", "rationale", "text"},
		"properties": map[string]any{
			"kind":      map[string]any{"type": "string", "enum": []string{"answer", "handoff"}},
			"target":    map[string]any{"type": "string", "enum": []string{"desk", "user"}},
			"objective": map[string]any{"type": "string", "maxLength": 4000},
			"rationale": map[string]any{"type": "string", "maxLength": 4000},
			"text":      map[string]any{"type": "string", "maxLength": 16000},
		},
	}
	scoutWorkflowSchema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"kind", "target", "objective", "rationale", "text"},
		"properties": map[string]any{
			"kind":      map[string]any{"type": "string", "enum": []string{"answer", "handoff"}},
			"target":    map[string]any{"type": "string", "enum": []string{"desk", "scout", "user"}},
			"objective": map[string]any{"type": "string", "maxLength": 4000},
			"rationale": map[string]any{"type": "string", "maxLength": 4000},
			"text":      map[string]any{"type": "string", "maxLength": 16000},
		},
	}
	scoutMemoSchema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"summary", "evidence", "limitations"},
		"properties": map[string]any{
			"summary":     map[string]any{"type": "string", "maxLength": 12000},
			"evidence":    map[string]any{"type": "array", "maxItems": 12, "items": map[string]any{"type": "string", "maxLength": 4000}},
			"limitations": map[string]any{"type": "string", "maxLength": 4000},
		},
	}
	answerSchemaRaw, err := json.Marshal(answerSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex answer schema: %w", err)
	}
	workflowSchemaRaw, err := json.Marshal(workflowSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex workflow schema: %w", err)
	}
	scoutWorkflowSchemaRaw, err := json.Marshal(scoutWorkflowSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex Scout workflow schema: %w", err)
	}
	scoutMemoSchemaRaw, err := json.Marshal(scoutMemoSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex Scout memo schema: %w", err)
	}
	answerSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-text-output-schema-v1",
		"agent-platform.contract.output_contract_schema.v1", answerSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex answer schema: %w", err)
	}
	workflowSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-workflow-output-schema-v2",
		"agent-platform.contract.output_contract_schema.v1", workflowSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex workflow schema: %w", err)
	}
	scoutWorkflowSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-workflow-output-schema-v3",
		"agent-platform.contract.output_contract_schema.v1", scoutWorkflowSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex Scout workflow schema: %w", err)
	}
	scoutMemoSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-scout-research-memo-schema-v1",
		"agent-platform.contract.output_contract_schema.v1", scoutMemoSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex Scout memo schema: %w", err)
	}
	runtimeDefinitions, err := adapter.EnsureRuntimeDefinitions(ctx, answerSchemaRef, workflowSchemaRef, scoutWorkflowSchemaRef, scoutMemoSchemaRef)
	if err != nil {
		return fmt.Errorf("select Cortex runtime definitions: %w", err)
	}
	outputSchemas := map[string][]byte{
		runtimeDefinitions.AnswerOutputContractDigest:        answerSchemaRaw,
		runtimeDefinitions.WorkflowOutputContractDigest:      workflowSchemaRaw,
		runtimeDefinitions.ScoutWorkflowOutputContractDigest: scoutWorkflowSchemaRaw,
		runtimeDefinitions.ScoutMemoOutputContractDigest:     scoutMemoSchemaRaw,
	}
	gateway, err := inputgateway.New(adapter, adapter)
	if err != nil {
		return err
	}
	actor := contracts.AuditActor{PrincipalID: principal, Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI}
	subject := contracts.AuditActor{PrincipalID: subjectID, Kind: contracts.PrincipalUser, Audience: contracts.AudienceControlAPI}
	if actor.Validate() != nil || subject.Validate() != nil {
		return fmt.Errorf("invalid Cortex actor configuration")
	}
	publicHandler := inputgateway.NewRuntimeHandler(gateway, adapter, actor, bearerSubject(serviceToken, subject))
	mux := http.NewServeMux()
	mux.Handle("/", publicHandler)
	mux.HandleFunc("GET /v1/runs/{id}", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, serviceToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		result, err := adapter.GetRunResult(request.Context(), strings.TrimSpace(request.PathValue("id")))
		if err != nil {
			http.Error(w, "run result unavailable", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /v1/conversations/{id}", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, serviceToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		entries, err := adapter.ConversationHistory(request.Context(), strings.TrimSpace(request.PathValue("id")), subject.PrincipalID, "")
		if err != nil {
			http.Error(w, "conversation unavailable", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"conversation_id": strings.TrimSpace(request.PathValue("id")), "entries": entries})
	})
	mux.HandleFunc("POST /internal/v1/model-outputs", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			CallID               string          `json:"call_id"`
			ManifestDigest       string          `json:"manifest_digest"`
			OutputContractDigest string          `json:"output_contract_digest"`
			Output               json.RawMessage `json:"output"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || len(body.Output) == 0 {
			http.Error(w, "invalid model output", http.StatusBadRequest)
			return
		}
		validation, err := validateModelOutput(outputSchemas, strings.TrimSpace(body.OutputContractDigest), body.Output)
		if err != nil {
			log.Printf("Cortex model output validation failed: %v", err)
			http.Error(w, "model output failed its contract", http.StatusUnprocessableEntity)
			return
		}
		ref, err := adapter.CommitModelOutput(request.Context(), strings.TrimSpace(body.CallID), strings.TrimSpace(body.ManifestDigest), body.Output, validation)
		if err != nil {
			log.Printf("Cortex model output commit failed: %v", err)
			http.Error(w, "model output commit failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(ref)
	})
	mux.HandleFunc("POST /internal/v1/handoffs", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			CallID    string `json:"call_id"`
			Target    string `json:"target"`
			Objective string `json:"objective"`
			Rationale string `json:"rationale"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 12<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil {
			http.Error(w, "invalid handoff", http.StatusBadRequest)
			return
		}
		if err := adapter.RecordHandoff(request.Context(), strings.TrimSpace(body.CallID), strings.TrimSpace(body.Target), strings.TrimSpace(body.Objective), strings.TrimSpace(body.Rationale)); err != nil {
			log.Printf("Cortex handoff recording failed: %v", err)
			http.Error(w, "handoff recording failed", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.TrimSpace(body.Target) == "scout" {
			admission, err := adapter.AdmitScoutChild(request.Context(), strings.TrimSpace(body.CallID))
			if err != nil {
				log.Printf("Cortex Scout child admission failed: %v", err)
				http.Error(w, "scout child admission failed", http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(w).Encode(admission)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "recorded"})
	})
	mux.HandleFunc("POST /internal/v1/tool-calls/web-fetch", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			SourceCallID    string `json:"source_call_id"`
			AttemptID       string `json:"attempt_id"`
			LeaseGeneration int64  `json:"lease_generation"`
			LeaseToken      string `json:"lease_token"`
			URL             string `json:"url"`
			MaxChars        int    `json:"max_chars"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid tool call", http.StatusBadRequest)
			return
		}
		authorization, err := adapter.AuthorizeWebFetch(request.Context(), strings.TrimSpace(body.SourceCallID), strings.TrimSpace(body.AttemptID),
			body.LeaseGeneration, strings.TrimSpace(body.LeaseToken), strings.TrimSpace(body.URL), body.MaxChars)
		if err != nil {
			log.Printf("Cortex web fetch authorization failed: %v", err)
			http.Error(w, "tool authorization denied", http.StatusForbidden)
			return
		}
		result, err := invokeResearchWebFetch(request.Context(), researchHTTP, researchURL, researchToken, authorization.ToolCallID)
		if err != nil {
			log.Printf("Research web fetch failed for %s: %v", authorization.ToolCallID, err)
			http.Error(w, "research tool unavailable", http.StatusBadGateway)
			return
		}
		if result.Receipt.ToolCallID != authorization.ToolCallID || string(result.Receipt.ToolID) != authorization.ToolID ||
			result.Receipt.RequestDigest != authorization.RequestDigest || result.Evidence.ToolCallID != authorization.ToolCallID {
			http.Error(w, "research tool response invalid", http.StatusBadGateway)
			return
		}
		if err := adapter.RecordWebFetchReceipt(request.Context(), result); err != nil {
			log.Printf("Cortex web fetch receipt recording failed: %v", err)
			http.Error(w, "research receipt unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
	server := &http.Server{
		Addr:              env("CORTEX_INPUT_ADDR", ":8400"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	log.Printf("Cortex Input Gateway listening on %s as %s", server.Addr, principal)
	return server.ListenAndServe()
}

func validateModelOutput(schemas map[string][]byte, outputContractDigest string, output []byte) (outputcontract.Evidence, error) {
	schema, found := schemas[outputContractDigest]
	if !found {
		return outputcontract.Evidence{}, fmt.Errorf("unknown output contract")
	}
	evidence, err := outputcontract.Validate(bytes.NewReader(schema), bytes.NewReader(output))
	if err != nil {
		return outputcontract.Evidence{}, err
	}
	return evidence, nil
}

func invokeResearchWebFetch(ctx context.Context, client *http.Client, baseURL, token, toolCallID string) (inputgateway.CortexWebFetchResult, error) {
	if client == nil || baseURL == "" || token == "" || toolCallID == "" {
		return inputgateway.CortexWebFetchResult{}, fmt.Errorf("Research tool is unavailable")
	}
	body, _ := json.Marshal(map[string]string{"tool_call_id": toolCallID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/internal/v1/cortex-tools/web-fetch", bytes.NewReader(body))
	if err != nil {
		return inputgateway.CortexWebFetchResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return inputgateway.CortexWebFetchResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || len(raw) == 0 || len(raw) >= 64<<10 || response.StatusCode != http.StatusOK {
		return inputgateway.CortexWebFetchResult{}, fmt.Errorf("Research tool HTTP %d", response.StatusCode)
	}
	var result inputgateway.CortexWebFetchResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF || result.Receipt.Validate() != nil || result.Evidence.Validate() != nil {
		return inputgateway.CortexWebFetchResult{}, fmt.Errorf("Research tool returned an invalid receipt")
	}
	return result, nil
}

func validBearer(request *http.Request, token string) bool {
	expected := []byte("Bearer " + token)
	values := request.Header.Values("Authorization")
	return len(values) == 1 && len(values[0]) == len(expected) && subtle.ConstantTimeCompare([]byte(values[0]), expected) == 1
}

func bearerSubject(token string, subject contracts.AuditActor) func(*http.Request) (contracts.AuditActor, error) {
	expected := []byte("Bearer " + token)
	return func(request *http.Request) (contracts.AuditActor, error) {
		values := request.Header.Values("Authorization")
		if len(values) != 1 || len(values[0]) != len(expected) || subtle.ConstantTimeCompare([]byte(values[0]), expected) != 1 {
			return contracts.AuditActor{}, fmt.Errorf("invalid Cortex input credential")
		}
		return subject, nil
	}
}

func loadSecretEnv(name string) (string, error) {
	path := strings.TrimSpace(os.Getenv(name))
	if path == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	raw, err := security.LoadSecret(path)
	if err != nil {
		return "", fmt.Errorf("load %s: %w", name, err)
	}
	return string(raw), nil
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
