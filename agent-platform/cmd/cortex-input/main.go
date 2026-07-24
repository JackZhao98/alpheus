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
	"alpheus/agentplatform/capability"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/inputgateway"
	"alpheus/agentplatform/outputcontract"
	"alpheus/agentplatform/security"
	"alpheus/agentplatform/taskgraphproposal"
	"alpheus/agentplatform/taskgraphround"
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
	kernelURL := strings.TrimRight(env("CORTEX_KERNEL_URL", "http://kernel:8100"), "/")
	kernelHTTP := &http.Client{Timeout: 25 * time.Second}
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
	startCortexTaskGraphJoinRecovery(recoveryContext, adapter)
	startCortexExpiredRunRecovery(recoveryContext, adapter)
	startCortexRunCancellationRecovery(recoveryContext, adapter)
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
	gexbotWorkflowSchema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"kind", "target", "objective", "rationale", "text", "gexbot_action", "gexbot_symbol", "gexbot_category", "gexbot_as_of"},
		"properties": map[string]any{
			"kind":            map[string]any{"type": "string", "enum": []string{"answer", "handoff"}},
			"target":          map[string]any{"type": "string", "enum": []string{"desk", "scout", "user"}},
			"objective":       map[string]any{"type": "string", "maxLength": 4000},
			"rationale":       map[string]any{"type": "string", "maxLength": 4000},
			"text":            map[string]any{"type": "string", "maxLength": 16000},
			"gexbot_action":   map[string]any{"type": "string", "enum": []string{"none", "as_of"}},
			"gexbot_symbol":   map[string]any{"type": "string", "maxLength": 16},
			"gexbot_category": map[string]any{"type": "string", "enum": []string{"", "gex_full", "gex_zero", "gex_one"}},
			"gexbot_as_of":    map[string]any{"type": "string", "maxLength": 64},
		},
	}
	earningsWorkflowSchema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"kind", "target", "objective", "rationale", "text", "gexbot_action", "gexbot_symbol", "gexbot_category", "gexbot_as_of", "earnings_action", "earnings_symbol"},
		"properties": map[string]any{
			"kind":            map[string]any{"type": "string", "enum": []string{"answer", "handoff"}},
			"target":          map[string]any{"type": "string", "enum": []string{"desk", "scout", "user"}},
			"objective":       map[string]any{"type": "string", "maxLength": 4000},
			"rationale":       map[string]any{"type": "string", "maxLength": 4000},
			"text":            map[string]any{"type": "string", "maxLength": 16000},
			"gexbot_action":   map[string]any{"type": "string", "enum": []string{"none", "as_of"}},
			"gexbot_symbol":   map[string]any{"type": "string", "maxLength": 16},
			"gexbot_category": map[string]any{"type": "string", "enum": []string{"", "gex_full", "gex_zero", "gex_one"}},
			"gexbot_as_of":    map[string]any{"type": "string", "maxLength": 64},
			"earnings_action": map[string]any{"type": "string", "enum": []string{"none", "results"}},
			"earnings_symbol": map[string]any{"type": "string", "maxLength": 16},
		},
	}
	kernelToolIDs := append([]string{""}, capability.KernelReadToolIDs()...)
	kernelWorkflowSchema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"kind", "target", "objective", "rationale", "text", "gexbot_action", "gexbot_symbol", "gexbot_category", "gexbot_as_of", "earnings_action", "earnings_symbol", "kernel_action", "kernel_tool_id", "kernel_arguments"},
		"properties": map[string]any{
			"kind":             map[string]any{"type": "string", "enum": []string{"answer", "handoff"}},
			"target":           map[string]any{"type": "string", "enum": []string{"desk", "scout", "user"}},
			"objective":        map[string]any{"type": "string", "maxLength": 4000},
			"rationale":        map[string]any{"type": "string", "maxLength": 4000},
			"text":             map[string]any{"type": "string", "maxLength": 16000},
			"gexbot_action":    map[string]any{"type": "string", "enum": []string{"none", "as_of"}},
			"gexbot_symbol":    map[string]any{"type": "string", "maxLength": 16},
			"gexbot_category":  map[string]any{"type": "string", "enum": []string{"", "gex_full", "gex_zero", "gex_one"}},
			"gexbot_as_of":     map[string]any{"type": "string", "maxLength": 64},
			"earnings_action":  map[string]any{"type": "string", "enum": []string{"none", "results"}},
			"earnings_symbol":  map[string]any{"type": "string", "maxLength": 16},
			"kernel_action":    map[string]any{"type": "string", "enum": []string{"none", "read"}},
			"kernel_tool_id":   map[string]any{"type": "string", "enum": kernelToolIDs},
			"kernel_arguments": map[string]any{"type": "string", "maxLength": 12288},
		},
	}
	specialistWorkflowSchemaRaw, err := json.Marshal(kernelWorkflowSchema)
	if err != nil {
		return fmt.Errorf("clone Cortex Specialist workflow schema: %w", err)
	}
	var specialistWorkflowSchema map[string]any
	if json.Unmarshal(specialistWorkflowSchemaRaw, &specialistWorkflowSchema) != nil {
		return fmt.Errorf("clone Cortex Specialist workflow schema")
	}
	specialistTargets := append([]string{"desk", "scout", "user"}, capability.AgentRoleIDs()...)
	specialistWorkflowSchema["properties"].(map[string]any)["target"] = map[string]any{"type": "string", "enum": specialistTargets}
	liveWorkflowSchemaRaw, err := json.Marshal(specialistWorkflowSchema)
	if err != nil {
		return fmt.Errorf("clone Cortex GEXBOT live workflow schema: %w", err)
	}
	var liveWorkflowSchema map[string]any
	if json.Unmarshal(liveWorkflowSchemaRaw, &liveWorkflowSchema) != nil {
		return fmt.Errorf("clone Cortex GEXBOT live workflow schema")
	}
	liveWorkflowSchema["properties"].(map[string]any)["gexbot_action"] = map[string]any{"type": "string", "enum": []string{"none", "as_of", "live"}}
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
	taskGraphProposalSchema := taskgraphproposal.OutputSchema()
	taskGraphRoundSchema := taskgraphround.OutputSchema()
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
	gexbotWorkflowSchemaRaw, err := json.Marshal(gexbotWorkflowSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex GEXBOT workflow schema: %w", err)
	}
	earningsWorkflowSchemaRaw, err := json.Marshal(earningsWorkflowSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex Kernel earnings workflow schema: %w", err)
	}
	kernelWorkflowSchemaRaw, err := json.Marshal(kernelWorkflowSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex Kernel read workflow schema: %w", err)
	}
	specialistWorkflowSchemaRaw, err = json.Marshal(specialistWorkflowSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex Specialist workflow schema: %w", err)
	}
	liveWorkflowSchemaRaw, err = json.Marshal(liveWorkflowSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex GEXBOT live workflow schema: %w", err)
	}
	scoutMemoSchemaRaw, err := json.Marshal(scoutMemoSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex Scout memo schema: %w", err)
	}
	taskGraphProposalSchemaRaw, err := json.Marshal(taskGraphProposalSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex TaskGraph proposal schema: %w", err)
	}
	taskGraphRoundSchemaRaw, err := json.Marshal(taskGraphRoundSchema)
	if err != nil {
		return fmt.Errorf("encode Cortex TaskGraph round schema: %w", err)
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
	gexbotWorkflowSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-workflow-output-schema-v4",
		"agent-platform.contract.output_contract_schema.v1", gexbotWorkflowSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex GEXBOT workflow schema: %w", err)
	}
	earningsWorkflowSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-workflow-output-schema-v5",
		"agent-platform.contract.output_contract_schema.v1", earningsWorkflowSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex Kernel earnings workflow schema: %w", err)
	}
	kernelWorkflowSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-workflow-output-schema-v6",
		"agent-platform.contract.output_contract_schema.v1", kernelWorkflowSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex Kernel read workflow schema: %w", err)
	}
	specialistWorkflowSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-workflow-output-schema-v7",
		"agent-platform.contract.output_contract_schema.v1", specialistWorkflowSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex Specialist workflow schema: %w", err)
	}
	liveWorkflowSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-workflow-output-schema-v8",
		"agent-platform.contract.output_contract_schema.v1", liveWorkflowSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex GEXBOT live workflow schema: %w", err)
	}
	scoutMemoSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-scout-research-memo-schema-v1",
		"agent-platform.contract.output_contract_schema.v1", scoutMemoSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex Scout memo schema: %w", err)
	}
	taskGraphProposalSchemaRef, err := adapter.CommitControlJSON(ctx, "output_contract_schema", "cortex-task-graph-proposal-schema-v1",
		"agent-platform.contract.output_contract_schema.v1", taskGraphProposalSchema)
	if err != nil {
		return fmt.Errorf("commit Cortex TaskGraph proposal schema: %w", err)
	}
	taskGraphRoundSchemaRef, err := adapter.CommitControlJSON(
		ctx, "output_contract_schema",
		"cortex-task-graph-round-schema-v1",
		"agent-platform.contract.output_contract_schema.v1",
		taskGraphRoundSchema,
	)
	if err != nil {
		return fmt.Errorf("commit Cortex TaskGraph round schema: %w", err)
	}
	runtimeDefinitions, err := adapter.EnsureRuntimeDefinitions(ctx, answerSchemaRef, workflowSchemaRef, scoutWorkflowSchemaRef, gexbotWorkflowSchemaRef, earningsWorkflowSchemaRef, kernelWorkflowSchemaRef, specialistWorkflowSchemaRef, liveWorkflowSchemaRef, scoutMemoSchemaRef, taskGraphProposalSchemaRef)
	if err != nil {
		return fmt.Errorf("select Cortex runtime definitions: %w", err)
	}
	runtimeDefinitions.TaskGraphRoundOutputContractDigest, err =
		adapter.EnsureTaskGraphRoundOutputContract(
			ctx, taskGraphRoundSchemaRef,
		)
	if err != nil {
		return fmt.Errorf(
			"select Cortex TaskGraph round output contract: %w", err)
	}
	outputSchemas := map[string][]byte{
		runtimeDefinitions.AnswerOutputContractDigest:             answerSchemaRaw,
		runtimeDefinitions.WorkflowOutputContractDigest:           workflowSchemaRaw,
		runtimeDefinitions.ScoutWorkflowOutputContractDigest:      scoutWorkflowSchemaRaw,
		runtimeDefinitions.GEXBOTWorkflowOutputContractDigest:     gexbotWorkflowSchemaRaw,
		runtimeDefinitions.EarningsWorkflowOutputContractDigest:   earningsWorkflowSchemaRaw,
		runtimeDefinitions.KernelWorkflowOutputContractDigest:     kernelWorkflowSchemaRaw,
		runtimeDefinitions.SpecialistWorkflowOutputContractDigest: specialistWorkflowSchemaRaw,
		runtimeDefinitions.LiveWorkflowOutputContractDigest:       liveWorkflowSchemaRaw,
		runtimeDefinitions.ScoutMemoOutputContractDigest:          scoutMemoSchemaRaw,
		runtimeDefinitions.TaskGraphProposalOutputContractDigest:  taskGraphProposalSchemaRaw,
		runtimeDefinitions.TaskGraphRoundOutputContractDigest:     taskGraphRoundSchemaRaw,
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
	mux.HandleFunc("POST /v1/runs/{id}/cancel", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, serviceToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			RequestID      string `json:"request_id"`
			IdempotencyKey string `json:"idempotency_key"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(
			w, request.Body, 4<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil ||
			strings.TrimSpace(body.RequestID) != body.RequestID ||
			strings.TrimSpace(body.IdempotencyKey) != body.IdempotencyKey ||
			body.RequestID == "" || len(body.RequestID) > 200 ||
			body.IdempotencyKey == "" || len(body.IdempotencyKey) > 200 {
			http.Error(w, "invalid cancellation request", http.StatusBadRequest)
			return
		}
		result, err := adapter.CancelRun(
			request.Context(),
			strings.TrimSpace(request.PathValue("id")),
			subject.PrincipalID,
			body.RequestID,
			body.IdempotencyKey,
			time.Now().UTC(),
		)
		if err != nil {
			log.Printf("Cortex Run cancellation failed: %v", err)
			http.Error(w, "Run cancellation unavailable", http.StatusServiceUnavailable)
			return
		}
		statusCode := http.StatusOK
		switch result.Status {
		case "canceling":
			statusCode = http.StatusAccepted
		case "terminal":
			statusCode = http.StatusConflict
		case "denied":
			if result.ReasonCode == "cancellation_target_not_found" {
				statusCode = http.StatusNotFound
			} else {
				statusCode = http.StatusConflict
			}
		case "canceled":
		default:
			http.Error(w, "invalid cancellation result", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(statusCode)
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
	mux.HandleFunc("GET /v1/operations/health", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, serviceToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		result, err := adapter.GetOperationsHealth(request.Context())
		if err != nil {
			log.Printf("Cortex operations health read failed: %v", err)
			http.Error(w, "operations health unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("GET /v1/operations/overview", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, serviceToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		cortex, err := adapter.GetOperationsHealth(request.Context())
		if err != nil {
			log.Printf("Cortex operations overview read failed: %v", err)
			http.Error(w, "operations overview unavailable", http.StatusServiceUnavailable)
			return
		}
		now := time.Now().UTC()
		research, researchErr := getMoodyBluesResearchHealth(
			request.Context(), researchHTTP, researchURL, researchToken, now)
		if researchErr != nil {
			log.Printf("Moody Blues operations health read failed: %v", researchErr)
			research = moodyBluesResearchHealth{
				Status:          "unavailable",
				Provider:        "gexbot_classic",
				FreshnessPolicy: moodyBluesFreshnessPolicy,
				Series:          []moodyBluesSeriesHealth{},
			}
		}
		status := "healthy"
		if cortex.Status != "healthy" || research.Status != "healthy" {
			status = "degraded"
		}
		result := cortexOperationsOverview{
			GeneratedAt: now.Format(time.RFC3339Nano),
			Status:      status,
			Cortex:      cortex,
			Research:    research,
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
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
	mux.HandleFunc("POST /internal/v1/task-graphs", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			SourceCallID    string `json:"source_call_id"`
			AttemptID       string `json:"attempt_id"`
			LeaseGeneration int64  `json:"lease_generation"`
			LeaseToken      string `json:"lease_token"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(
			w, request.Body, 8<<10,
		))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil ||
			decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid TaskGraph proposal", http.StatusBadRequest)
			return
		}
		admission, err := adapter.AdmitTaskGraphProposal(
			request.Context(), strings.TrimSpace(body.SourceCallID),
			strings.TrimSpace(body.AttemptID), body.LeaseGeneration,
			strings.TrimSpace(body.LeaseToken),
		)
		if err != nil {
			log.Printf("Cortex TaskGraph proposal admission failed: %v", err)
			http.Error(w, "TaskGraph proposal admission denied",
				http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(admission)
	})
	mux.HandleFunc("POST /internal/v1/task-graph-rounds", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			SourceCallID    string `json:"source_call_id"`
			AttemptID       string `json:"attempt_id"`
			LeaseGeneration int64  `json:"lease_generation"`
			LeaseToken      string `json:"lease_token"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(
			w, request.Body, 8<<10,
		))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil ||
			decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid TaskGraph round request",
				http.StatusBadRequest)
			return
		}
		continuation, err := adapter.PrepareTaskGraphNextRound(
			request.Context(), strings.TrimSpace(body.SourceCallID),
			strings.TrimSpace(body.AttemptID), body.LeaseGeneration,
			strings.TrimSpace(body.LeaseToken),
		)
		if err != nil {
			log.Printf("Cortex TaskGraph next round failed: %v", err)
			http.Error(w, "TaskGraph next round denied",
				http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(continuation)
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
	mux.HandleFunc("POST /internal/v1/tool-calls/gexbot-as-of", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			SourceCallID    string    `json:"source_call_id"`
			AttemptID       string    `json:"attempt_id"`
			LeaseGeneration int64     `json:"lease_generation"`
			LeaseToken      string    `json:"lease_token"`
			Symbol          string    `json:"symbol"`
			Category        string    `json:"category"`
			AsOf            time.Time `json:"as_of"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid tool call", http.StatusBadRequest)
			return
		}
		toolRequest := capability.GEXBOTAsOfRequest{Symbol: strings.ToUpper(strings.TrimSpace(body.Symbol)), Category: strings.TrimSpace(body.Category), AsOf: body.AsOf.UTC()}
		authorization, err := adapter.AuthorizeGEXBOTAsOf(request.Context(), strings.TrimSpace(body.SourceCallID), strings.TrimSpace(body.AttemptID),
			body.LeaseGeneration, strings.TrimSpace(body.LeaseToken), toolRequest)
		if err != nil {
			log.Printf("Cortex GEXBOT authorization failed: %v", err)
			http.Error(w, "tool authorization denied", http.StatusForbidden)
			return
		}
		result, err := invokeResearchGEXBOTAsOf(request.Context(), researchHTTP, researchURL, researchToken, authorization.ToolCallID)
		if err != nil {
			log.Printf("Research GEXBOT as_of failed for %s: %v", authorization.ToolCallID, err)
			http.Error(w, "research tool unavailable", http.StatusBadGateway)
			return
		}
		if result.Receipt.ToolCallID != authorization.ToolCallID || result.Receipt.ToolID != capability.ToolResearchGEXBOTAsOf ||
			result.Receipt.RequestDigest != authorization.RequestDigest || result.Evidence.ToolCallID != authorization.ToolCallID ||
			result.Evidence.Symbol != authorization.Symbol || result.Evidence.Category != authorization.Category || !result.Evidence.AsOf.Equal(authorization.AsOf) {
			http.Error(w, "research tool response invalid", http.StatusBadGateway)
			return
		}
		if err := adapter.RecordGEXBOTAsOfReceipt(request.Context(), result); err != nil {
			log.Printf("Cortex GEXBOT receipt recording failed: %v", err)
			http.Error(w, "research receipt unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /internal/v1/tool-calls/gexbot-live", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			SourceCallID    string `json:"source_call_id"`
			AttemptID       string `json:"attempt_id"`
			LeaseGeneration int64  `json:"lease_generation"`
			LeaseToken      string `json:"lease_token"`
			Symbol          string `json:"symbol"`
			Category        string `json:"category"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid tool call", http.StatusBadRequest)
			return
		}
		toolRequest := capability.GEXBOTLiveRequest{
			Symbol: strings.ToUpper(strings.TrimSpace(body.Symbol)), Category: strings.TrimSpace(body.Category),
		}
		authorization, err := adapter.AuthorizeGEXBOTLive(request.Context(), strings.TrimSpace(body.SourceCallID),
			strings.TrimSpace(body.AttemptID), body.LeaseGeneration, strings.TrimSpace(body.LeaseToken), toolRequest)
		if err != nil {
			log.Printf("Cortex GEXBOT live authorization failed: %v", err)
			http.Error(w, "tool authorization denied", http.StatusForbidden)
			return
		}
		result, err := invokeResearchGEXBOTLive(request.Context(), researchHTTP, researchURL, researchToken, authorization.ToolCallID)
		if err != nil {
			log.Printf("Research GEXBOT live failed for %s: %v", authorization.ToolCallID, err)
			http.Error(w, "research tool unavailable", http.StatusBadGateway)
			return
		}
		if result.Receipt.ToolCallID != authorization.ToolCallID || result.Receipt.ToolID != capability.ToolMarketGEXBOTLive ||
			result.Receipt.RequestDigest != authorization.RequestDigest || result.Evidence.ToolCallID != authorization.ToolCallID ||
			result.Evidence.Symbol != authorization.Symbol || result.Evidence.Category != authorization.Category {
			http.Error(w, "research tool response invalid", http.StatusBadGateway)
			return
		}
		if err := adapter.RecordGEXBOTLiveReceipt(request.Context(), result); err != nil {
			log.Printf("Cortex GEXBOT live receipt recording failed: %v", err)
			http.Error(w, "research receipt unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /internal/v1/tool-calls/kernel-earnings-results", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			SourceCallID    string `json:"source_call_id"`
			AttemptID       string `json:"attempt_id"`
			LeaseGeneration int64  `json:"lease_generation"`
			LeaseToken      string `json:"lease_token"`
			Symbol          string `json:"symbol"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid tool call", http.StatusBadRequest)
			return
		}
		toolRequest := capability.KernelEarningsResultsRequest{Symbol: strings.ToUpper(strings.TrimSpace(body.Symbol))}
		authorization, err := adapter.AuthorizeKernelEarningsResults(request.Context(), strings.TrimSpace(body.SourceCallID), strings.TrimSpace(body.AttemptID),
			body.LeaseGeneration, strings.TrimSpace(body.LeaseToken), toolRequest)
		if err != nil {
			log.Printf("Cortex Kernel earnings authorization failed: %v", err)
			http.Error(w, "tool authorization denied", http.StatusForbidden)
			return
		}
		observation, err := invokeKernelEarningsResults(request.Context(), kernelHTTP, kernelURL, serviceToken, authorization)
		if err != nil {
			log.Printf("Kernel earnings Tool failed for %s: %v", authorization.ToolCallID, err)
			http.Error(w, "Kernel tool unavailable", http.StatusBadGateway)
			return
		}
		if observation.ToolCallID != authorization.ToolCallID || observation.ToolID != capability.ToolKernelEarningsResults ||
			observation.RequestDigest != authorization.RequestDigest || observation.Symbol != authorization.Symbol {
			http.Error(w, "Kernel tool response invalid", http.StatusBadGateway)
			return
		}
		result, err := adapter.RecordKernelEarningsResults(request.Context(), observation)
		if err != nil {
			log.Printf("Cortex Kernel earnings receipt recording failed: %v", err)
			http.Error(w, "Kernel receipt unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("POST /internal/v1/tool-calls/kernel-read", func(w http.ResponseWriter, request *http.Request) {
		if !validBearer(request, workerToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var body struct {
			SourceCallID    string            `json:"source_call_id"`
			AttemptID       string            `json:"attempt_id"`
			LeaseGeneration int64             `json:"lease_generation"`
			LeaseToken      string            `json:"lease_token"`
			ToolID          capability.ToolID `json:"tool_id"`
			SourceTool      string            `json:"source_tool"`
			Arguments       map[string]any    `json:"arguments"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 20<<10))
		decoder.UseNumber()
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF {
			http.Error(w, "invalid tool call", http.StatusBadRequest)
			return
		}
		toolRequest := capability.KernelReadRequest{
			ToolID: body.ToolID, SourceTool: strings.TrimSpace(body.SourceTool), Arguments: body.Arguments,
		}
		authorization, err := adapter.AuthorizeKernelRead(request.Context(), strings.TrimSpace(body.SourceCallID), strings.TrimSpace(body.AttemptID),
			body.LeaseGeneration, strings.TrimSpace(body.LeaseToken), toolRequest)
		if err != nil {
			log.Printf("Cortex Kernel read authorization failed: %v", err)
			http.Error(w, "tool authorization denied", http.StatusForbidden)
			return
		}
		observation, err := invokeKernelRead(request.Context(), kernelHTTP, kernelURL, serviceToken, authorization)
		if err != nil {
			log.Printf("Kernel read Tool failed for %s: %v", authorization.ToolCallID, err)
			http.Error(w, "Kernel tool unavailable", http.StatusBadGateway)
			return
		}
		if observation.ToolCallID != authorization.ToolCallID || observation.ToolID != authorization.ToolID ||
			observation.RequestDigest != authorization.RequestDigest || observation.SourceTool != authorization.SourceTool {
			http.Error(w, "Kernel tool response invalid", http.StatusBadGateway)
			return
		}
		result, err := adapter.RecordKernelRead(request.Context(), observation)
		if err != nil {
			log.Printf("Cortex Kernel read receipt recording failed: %v", err)
			http.Error(w, "Kernel receipt unavailable", http.StatusServiceUnavailable)
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

func invokeResearchGEXBOTAsOf(ctx context.Context, client *http.Client, baseURL, token, toolCallID string) (inputgateway.CortexGEXBOTAsOfResult, error) {
	if client == nil || baseURL == "" || token == "" || toolCallID == "" {
		return inputgateway.CortexGEXBOTAsOfResult{}, fmt.Errorf("Research GEXBOT Tool is unavailable")
	}
	body, _ := json.Marshal(map[string]string{"tool_call_id": toolCallID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/internal/v1/cortex-tools/gexbot-as-of", bytes.NewReader(body))
	if err != nil {
		return inputgateway.CortexGEXBOTAsOfResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return inputgateway.CortexGEXBOTAsOfResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || len(raw) == 0 || len(raw) >= 64<<10 || response.StatusCode != http.StatusOK {
		return inputgateway.CortexGEXBOTAsOfResult{}, fmt.Errorf("Research GEXBOT Tool HTTP %d", response.StatusCode)
	}
	var result inputgateway.CortexGEXBOTAsOfResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF || result.Receipt.Validate() != nil || result.Evidence.Validate() != nil ||
		result.Receipt.ToolCallID != result.Evidence.ToolCallID || result.Receipt.Evidence.RecordID != result.Evidence.EvidenceID {
		return inputgateway.CortexGEXBOTAsOfResult{}, fmt.Errorf("Research GEXBOT Tool returned an invalid receipt")
	}
	return result, nil
}

func invokeResearchGEXBOTLive(ctx context.Context, client *http.Client, baseURL, token, toolCallID string) (inputgateway.CortexGEXBOTLiveResult, error) {
	if client == nil || baseURL == "" || token == "" || toolCallID == "" {
		return inputgateway.CortexGEXBOTLiveResult{}, fmt.Errorf("Research GEXBOT live Tool is unavailable")
	}
	body, _ := json.Marshal(map[string]string{"tool_call_id": toolCallID})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/internal/v1/cortex-tools/gexbot-live", bytes.NewReader(body))
	if err != nil {
		return inputgateway.CortexGEXBOTLiveResult{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return inputgateway.CortexGEXBOTLiveResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || len(raw) == 0 || len(raw) >= 64<<10 || response.StatusCode != http.StatusOK {
		return inputgateway.CortexGEXBOTLiveResult{}, fmt.Errorf("Research GEXBOT live Tool HTTP %d", response.StatusCode)
	}
	var result inputgateway.CortexGEXBOTLiveResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		result.Receipt.Validate() != nil || result.Evidence.Validate() != nil ||
		result.Receipt.ToolCallID != result.Evidence.ToolCallID ||
		result.Receipt.Evidence.RecordID != result.Evidence.EvidenceID {
		return inputgateway.CortexGEXBOTLiveResult{}, fmt.Errorf("Research GEXBOT live Tool returned an invalid receipt")
	}
	return result, nil
}

func invokeKernelEarningsResults(ctx context.Context, client *http.Client, baseURL, token string, authorization inputgateway.KernelEarningsAuthorization) (capability.KernelEarningsObservation, error) {
	if client == nil || baseURL == "" || token == "" || authorization.ToolCallID == "" || authorization.ToolID != string(capability.ToolKernelEarningsResults) ||
		len(authorization.RequestDigest) != 64 || (capability.KernelEarningsResultsRequest{Symbol: authorization.Symbol}).Validate() != nil {
		return capability.KernelEarningsObservation{}, fmt.Errorf("Kernel earnings Tool is unavailable")
	}
	body, _ := json.Marshal(map[string]string{"tool_call_id": authorization.ToolCallID, "request_digest": authorization.RequestDigest, "symbol": authorization.Symbol})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/internal/v1/cortex-tools/earnings-results", bytes.NewReader(body))
	if err != nil {
		return capability.KernelEarningsObservation{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return capability.KernelEarningsObservation{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || len(raw) == 0 || len(raw) >= 64<<10 || response.StatusCode != http.StatusOK {
		return capability.KernelEarningsObservation{}, fmt.Errorf("Kernel earnings Tool HTTP %d", response.StatusCode)
	}
	var observation capability.KernelEarningsObservation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&observation) != nil || decoder.Decode(&struct{}{}) != io.EOF || observation.Validate() != nil {
		return capability.KernelEarningsObservation{}, fmt.Errorf("Kernel earnings Tool returned invalid facts")
	}
	return observation, nil
}

func invokeKernelRead(ctx context.Context, client *http.Client, baseURL, token string, authorization inputgateway.KernelReadAuthorization) (capability.KernelReadObservation, error) {
	requestValue := capability.KernelReadRequest{ToolID: authorization.ToolID, SourceTool: authorization.SourceTool, Arguments: authorization.Arguments}
	if client == nil || baseURL == "" || token == "" || authorization.ToolCallID == "" ||
		len(authorization.RequestDigest) != 64 || requestValue.Validate() != nil {
		return capability.KernelReadObservation{}, fmt.Errorf("Kernel read Tool is unavailable")
	}
	body, _ := json.Marshal(map[string]any{
		"tool_call_id": authorization.ToolCallID, "tool_id": authorization.ToolID,
		"source_tool": authorization.SourceTool, "request_digest": authorization.RequestDigest,
		"arguments": authorization.Arguments,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/internal/v1/cortex-tools/read", bytes.NewReader(body))
	if err != nil {
		return capability.KernelReadObservation{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return capability.KernelReadObservation{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 80<<10))
	if err != nil || len(raw) == 0 || len(raw) >= 80<<10 || response.StatusCode != http.StatusOK {
		return capability.KernelReadObservation{}, fmt.Errorf("Kernel read Tool HTTP %d", response.StatusCode)
	}
	var observation capability.KernelReadObservation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&observation) != nil || decoder.Decode(&struct{}{}) != io.EOF || observation.Validate() != nil {
		return capability.KernelReadObservation{}, fmt.Errorf("Kernel read Tool returned invalid facts")
	}
	return observation, nil
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
