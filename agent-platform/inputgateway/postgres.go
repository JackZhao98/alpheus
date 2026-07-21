package inputgateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/inputcontract"
)

const (
	controlDatabaseRole  = "alpheus_agent_control_api"
	rawInputMediaType    = "text/plain; charset=utf-8"
	maxRawInputBytes     = 1 << 20
	stageTTLSeconds      = 300
	controlJSONMediaType = "application/json"
)

// PostgresAdapter connects the existing bounded local BlobStore to the AP0
// metadata protocol and the AP2 immutable intake command. Every database call
// assumes exactly one NOINHERIT Cortex LOGIN and selects only its reviewed
// Control API group role for the transaction.
type PostgresAdapter struct {
	db        *sql.DB
	local     *blob.LocalStore
	principal string
}

func NewPostgresAdapter(db *sql.DB, local *blob.LocalStore, principal string) (*PostgresAdapter, error) {
	actor := contracts.AuditActor{PrincipalID: principal, Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI}
	if db == nil || local == nil || actor.Validate() != nil {
		return nil, fmt.Errorf("invalid Cortex PostgreSQL adapter configuration")
	}
	return &PostgresAdapter{db: db, local: local, principal: principal}, nil
}

type rawInputOrigin struct {
	SchemaRevision     uint16 `json:"schema_revision"`
	InputID            string `json:"input_id"`
	SubjectPrincipalID string `json:"subject_principal_id"`
	ContentDigest      string `json:"content_digest"`
	MediaType          string `json:"media_type"`
	SizeBytes          int64  `json:"size_bytes"`
}

func (adapter *PostgresAdapter) CommitRawInput(ctx context.Context, request RawBlobRequest) (blob.BlobRef, error) {
	if adapter == nil || adapter.db == nil || adapter.local == nil || request.InputID == "" ||
		request.SubjectPrincipalID == "" || request.MediaType != rawInputMediaType || len(request.Text) < 1 || len(request.Text) > maxRawInputBytes {
		return blob.BlobRef{}, fmt.Errorf("invalid raw input")
	}
	hash := sha256.Sum256(request.Text)
	contentDigest := hex.EncodeToString(hash[:])
	size := int64(len(request.Text))
	originDigest, err := canonical.Digest("agent-platform.contract.input_raw.v1", rawInputOrigin{
		SchemaRevision: 1, InputID: request.InputID, SubjectPrincipalID: request.SubjectPrincipalID,
		ContentDigest: contentDigest, MediaType: request.MediaType, SizeBytes: size,
	})
	if err != nil {
		return blob.BlobRef{}, fmt.Errorf("digest raw input origin: %w", err)
	}
	stageID := deterministicStageID(adapter.principal, request.InputID)
	return adapter.commitControlBlob(ctx, stageID, request.InputID, "input_raw", request.MediaType, request.Text, contentDigest, originDigest)
}

// CommitControlJSON commits bytes for one immutable Control-owned record. The
// caller supplies an already-canonical JSON payload and the digest of the
// origin record whose identity the BlobRef carries.
func (adapter *PostgresAdapter) CommitControlJSON(ctx context.Context, recordType, recordID, originDomain string, value any) (blob.BlobRef, error) {
	if adapter == nil || adapter.db == nil || adapter.local == nil || recordID == "" ||
		(recordType != "task_objective" && recordType != "output_contract_schema" &&
			recordType != "execution_binding" && recordType != "context_manifest") || originDomain == "" {
		return blob.BlobRef{}, fmt.Errorf("invalid control JSON blob")
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) < 1 || len(raw) > maxRawInputBytes {
		return blob.BlobRef{}, fmt.Errorf("encode control JSON blob: %w", err)
	}
	hash := sha256.Sum256(raw)
	contentDigest := hex.EncodeToString(hash[:])
	originDigest, err := canonical.Digest(originDomain, value)
	if err != nil {
		return blob.BlobRef{}, fmt.Errorf("digest control JSON origin: %w", err)
	}
	stageID := deterministicControlStageID(adapter.principal, recordType, recordID)
	return adapter.commitControlBlob(ctx, stageID, recordID, recordType, controlJSONMediaType, raw, contentDigest, originDigest)
}

// PrepareRootSession installs immutable execution/context inputs and grants the
// configured Worker read access before the Task becomes discoverable.
func (adapter *PostgresAdapter) PrepareRootSession(ctx context.Context, admission Admission, run RunAdmission) error {
	worker := "cortex-worker-1"
	executionValue := map[string]any{"schema_revision": 1, "task_id": run.RootTaskID,
		"worker_principal_id": worker, "provider": "openai", "model": "gpt-5.6-sol"}
	execution, err := adapter.CommitControlJSON(ctx, "execution_binding", run.RootTaskID,
		"agent-platform.contract.execution_binding.v1", executionValue)
	if err != nil {
		return fmt.Errorf("commit execution binding: %w", err)
	}
	contextValue := map[string]any{"schema_revision": 1, "request_id": admission.Command.Request.RequestID,
		"raw_input": admission.Blob}
	contextRef, err := adapter.CommitControlJSON(ctx, "context_manifest", admission.Command.Request.RequestID,
		"agent-platform.contract.context_manifest.v1", contextValue)
	if err != nil {
		return fmt.Errorf("commit context manifest: %w", err)
	}
	executionRaw, _ := json.Marshal(execution)
	contextRaw, _ := json.Marshal(contextRef)
	inputRaw, _ := json.Marshal(admission.Blob)
	return adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		var raw []byte
		if err := tx.QueryRowContext(ctx, `SELECT agent_control.prepare_cortex_root_session_v2($1,$2::JSONB,$3::JSONB,$4::JSONB,$5)::TEXT`,
			run.RootTaskID, string(executionRaw), string(contextRaw), string(inputRaw), worker).Scan(&raw); err != nil {
			return err
		}
		var response struct {
			Status    string `json:"status"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(raw, &response) != nil || response.Status != "ready" || response.SessionID == "" {
			return fmt.Errorf("Cortex root Session was not prepared")
		}
		return nil
	})
}

// CommitModelOutput is the Control-owned bridge used after a durable model
// dispatch. The manifest digest is supplied by Control's dispatch response.
func (adapter *PostgresAdapter) CommitModelOutput(ctx context.Context, callID, manifestDigest string, raw []byte) (blob.BlobRef, error) {
	if callID == "" || len(manifestDigest) != 64 || len(raw) < 1 || len(raw) > maxRawInputBytes {
		return blob.BlobRef{}, fmt.Errorf("invalid model output")
	}
	hash := sha256.Sum256(raw)
	contentDigest := hex.EncodeToString(hash[:])
	stageID := deterministicControlStageID(adapter.principal, "model_call_manifest", callID)
	result, err := adapter.commitControlBlob(ctx, stageID, callID, "model_call_manifest", controlJSONMediaType,
		raw, contentDigest, manifestDigest)
	if err != nil {
		return blob.BlobRef{}, err
	}
	encoded, _ := json.Marshal(result)
	err = adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		var responseRaw []byte
		return tx.QueryRowContext(ctx, `SELECT agent_control.publish_cortex_model_output_v2($1,$2,$3::JSONB,$4)::TEXT`,
			callID, manifestDigest, string(encoded), "cortex-worker-1").Scan(&responseRaw)
	})
	if err != nil {
		return blob.BlobRef{}, fmt.Errorf("publish model output: %w", err)
	}
	return result, nil
}

func (adapter *PostgresAdapter) commitControlBlob(ctx context.Context, stageID, recordID, recordType, mediaType string, raw []byte, contentDigest, originDigest string) (blob.BlobRef, error) {
	size := int64(len(raw))
	grant, err := adapter.beginStage(ctx, stageID, mediaType, contentDigest, size)
	if err != nil {
		return blob.BlobRef{}, err
	}
	committed, ok, err := adapter.resumeStage(ctx, grant, recordType, originDigest, recordID)
	if err != nil || ok {
		return committed, err
	}
	staged, err := adapter.local.Stage(ctx, grant, bytes.NewReader(raw), adapter)
	if err != nil {
		return blob.BlobRef{}, fmt.Errorf("stage raw input bytes: %w", err)
	}
	if err := adapter.local.Materialize(ctx, staged, adapter); err != nil {
		return blob.BlobRef{}, fmt.Errorf("materialize raw input bytes: %w", err)
	}
	return adapter.commitStage(ctx, staged, recordType, originDigest, recordID)
}

func (adapter *PostgresAdapter) AuthorizeBlobStage(ctx context.Context, grant blob.StageGrant) error {
	if adapter == nil || grant.ExpectedDigest == "" || grant.ExpectedSizeBytes == nil {
		return fmt.Errorf("invalid exact Blob stage grant")
	}
	issued, err := adapter.beginStage(ctx, grant.StageID, grant.MediaType, grant.ExpectedDigest, *grant.ExpectedSizeBytes)
	if err != nil {
		return err
	}
	if !sameStageGrant(issued, grant) {
		return fmt.Errorf("database Blob stage grant mismatch")
	}
	return nil
}

func sameStageGrant(left, right blob.StageGrant) bool {
	if left.SchemaRevision != right.SchemaRevision || left.StageID != right.StageID ||
		left.PrincipalID != right.PrincipalID || left.MediaType != right.MediaType ||
		left.MaxBytes != right.MaxBytes || left.ExpectedDigest != right.ExpectedDigest ||
		!left.IssuedAt.Equal(right.IssuedAt) || !left.ExpiresAt.Equal(right.ExpiresAt) ||
		(left.ExpectedSizeBytes == nil) != (right.ExpectedSizeBytes == nil) {
		return false
	}
	return left.ExpectedSizeBytes == nil || *left.ExpectedSizeBytes == *right.ExpectedSizeBytes
}

func (adapter *PostgresAdapter) AuthorizeBlobMaterialize(ctx context.Context, staged blob.StagedBlob) error {
	return adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		var changed bool
		return tx.QueryRowContext(ctx, `SELECT blob.reconcile_stage_facts($1,$2,$3,$4,$5)`,
			staged.Grant.StageID, adapter.principal, staged.ContentDigest, staged.SizeBytes, adapter.principal).Scan(&changed)
	})
}

func (adapter *PostgresAdapter) SubmitUserRequest(ctx context.Context, command inputcontract.SubmitUserRequestCommand) error {
	if command.Validate() != nil || command.Envelope.Actor.PrincipalID != adapter.principal {
		return fmt.Errorf("invalid submit-user-request command")
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	var responseRaw []byte
	err = adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_input.submit_user_request($1)::TEXT`, string(raw)).Scan(&responseRaw)
	})
	if err != nil {
		return fmt.Errorf("submit immutable user request: %w", err)
	}
	var response struct {
		Status        string `json:"status"`
		RequestID     string `json:"request_id"`
		RequestDigest string `json:"request_digest"`
		ReasonCode    string `json:"reason_code"`
	}
	if json.Unmarshal(responseRaw, &response) != nil || response.Status != "committed" ||
		response.RequestID != command.Request.RequestID || response.RequestDigest != command.Envelope.RequestDigest ||
		response.ReasonCode != "user_request_recorded" {
		return fmt.Errorf("submit immutable user request was denied or mismatched")
	}
	return nil
}

type RunAdmission struct {
	RunID      string `json:"run_id"`
	RootTaskID string `json:"root_task_id"`
	RunState   string `json:"run_state"`
	TaskState  string `json:"task_state"`
}

type CortexRunResult struct {
	RunID string `json:"run_id"`
	State string `json:"state"`
	Text  string `json:"text,omitempty"`
}

func (adapter *PostgresAdapter) GetRunResult(ctx context.Context, runID string) (CortexRunResult, error) {
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.get_cortex_run_result($1)::TEXT`, runID).Scan(&raw)
	}); err != nil {
		return CortexRunResult{}, err
	}
	var result struct {
		RunID           string              `json:"run_id"`
		State           string              `json:"state"`
		Output          *blob.BlobRef       `json:"output"`
		BindingID       string              `json:"binding_id"`
		OwningReference contracts.RecordRef `json:"owning_reference"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &result) != nil {
		return CortexRunResult{}, sql.ErrNoRows
	}
	answer := CortexRunResult{RunID: result.RunID, State: result.State}
	if result.State != "succeeded" {
		return answer, nil
	}
	if result.Output == nil {
		return CortexRunResult{}, fmt.Errorf("Cortex result output missing")
	}
	read, err := adapter.local.OpenVerified(ctx, blob.ReadRequest{PrincipalID: adapter.principal, BindingID: result.BindingID, BlobID: result.Output.BlobID, OwningReference: result.OwningReference}, adapter)
	if err != nil {
		return CortexRunResult{}, err
	}
	defer read.Close()
	data, err := io.ReadAll(io.LimitReader(read, result.Output.SizeBytes+1))
	if err != nil {
		return CortexRunResult{}, err
	}
	var value struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &value) != nil || strings.TrimSpace(value.Text) == "" {
		return CortexRunResult{}, fmt.Errorf("invalid Cortex result output")
	}
	answer.Text = value.Text
	return answer, nil
}

func (adapter *PostgresAdapter) AuthorizeBlobRead(ctx context.Context, request blob.ReadRequest) (blob.ReadAuthorization, error) {
	result := blob.ReadAuthorization{PrincipalID: adapter.principal, BindingID: request.BindingID, OwningReference: request.OwningReference}
	err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT schema_revision,blob_id::TEXT,content_digest,media_type,size_bytes,origin_owner,origin_record_type,origin_record_id,origin_record_digest,committed_at,authorized_at,valid_until FROM blob.authorize_read($1,$2,$3,$4,$5,$6,$7)`, adapter.principal, request.BindingID, request.BlobID, request.OwningReference.Owner, request.OwningReference.RecordType, request.OwningReference.RecordID, request.OwningReference.RecordDigest).Scan(&result.Blob.SchemaRevision, &result.Blob.BlobID, &result.Blob.ContentDigest, &result.Blob.MediaType, &result.Blob.SizeBytes, &result.Blob.Origin.Owner, &result.Blob.Origin.RecordType, &result.Blob.Origin.RecordID, &result.Blob.Origin.RecordDigest, &result.Blob.CommittedAt, &result.AuthorizedAt, &result.ValidUntil)
	})
	result.Blob.Origin.SchemaRevision = 1
	result.Blob.CommittedAt = result.Blob.CommittedAt.UTC()
	result.AuthorizedAt = result.AuthorizedAt.UTC()
	result.ValidUntil = result.ValidUntil.UTC()
	return result, err
}

func (adapter *PostgresAdapter) EnsureRuntimeDefinitions(ctx context.Context, schema blob.BlobRef) error {
	if schema.Validate() != nil || schema.Origin.RecordType != "output_contract_schema" || schema.MediaType != controlJSONMediaType {
		return fmt.Errorf("invalid Cortex output schema BlobRef")
	}
	raw, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	return adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		var responseRaw []byte
		if err := tx.QueryRowContext(ctx, `SELECT agent_control.ensure_cortex_mvp_runtime($1::JSONB)::TEXT`, string(raw)).Scan(&responseRaw); err != nil {
			return err
		}
		var response struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(responseRaw, &response) != nil || response.Status != "ready" {
			return fmt.Errorf("Cortex runtime definitions were not selected")
		}
		return nil
	})
}

func (adapter *PostgresAdapter) AdmitRun(ctx context.Context, admission Admission) (RunAdmission, error) {
	requestRef, err := admission.Command.Request.Ref()
	if err != nil {
		return RunAdmission{}, err
	}
	objectiveValue := struct {
		SchemaRevision uint16              `json:"schema_revision"`
		Request        contracts.RecordRef `json:"request"`
	}{SchemaRevision: 1, Request: requestRef}
	objective, err := adapter.CommitControlJSON(ctx, "task_objective", admission.Command.Request.RequestID,
		"agent-platform.contract.task_objective.v1", objectiveValue)
	if err != nil {
		return RunAdmission{}, fmt.Errorf("commit root task objective: %w", err)
	}
	command := struct {
		RequestID      string       `json:"request_id"`
		IdempotencyKey string       `json:"idempotency_key"`
		CausationID    string       `json:"causation_id"`
		CorrelationID  string       `json:"correlation_id"`
		Deadline       time.Time    `json:"deadline"`
		Objective      blob.BlobRef `json:"objective"`
	}{admission.Command.Request.RequestID, admission.Command.Envelope.IdempotencyKey,
		admission.Command.Envelope.CausationID, admission.Command.Envelope.CorrelationID,
		admission.Command.Envelope.Deadline, objective}
	raw, err := json.Marshal(command)
	if err != nil {
		return RunAdmission{}, err
	}
	var responseRaw []byte
	err = adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.admit_cortex_user_request_run($1::JSONB)::TEXT`, string(raw)).Scan(&responseRaw)
	})
	if err != nil {
		return RunAdmission{}, fmt.Errorf("admit canonical Run: %w", err)
	}
	var envelope struct {
		Status string `json:"status"`
		RunAdmission
	}
	if json.Unmarshal(responseRaw, &envelope) != nil || envelope.Status != "admitted" ||
		envelope.RunID == "" || envelope.RootTaskID == "" || envelope.RunState != "queued" || envelope.TaskState != "ready" {
		return RunAdmission{}, fmt.Errorf("canonical Run admission was denied or mismatched")
	}
	return envelope.RunAdmission, nil
}

func (adapter *PostgresAdapter) beginStage(ctx context.Context, stageID, mediaType, digest string, size int64) (blob.StageGrant, error) {
	var returnedID string
	var maxBytes int64
	var issuedAt, expiresAt time.Time
	err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT stage_id::TEXT,max_bytes,issued_at,expires_at FROM blob.begin_stage($1,$2,$3,$4,$5,$6,$7,$8)`,
			stageID, adapter.principal, mediaType, size, digest, size, stageTTLSeconds, adapter.principal).
			Scan(&returnedID, &maxBytes, &issuedAt, &expiresAt)
	})
	if err != nil {
		return blob.StageGrant{}, fmt.Errorf("begin Blob stage: %w", err)
	}
	grant := blob.StageGrant{SchemaRevision: blob.SchemaRevisionV1, StageID: returnedID,
		PrincipalID: adapter.principal, MediaType: mediaType, MaxBytes: maxBytes,
		ExpectedDigest: digest, ExpectedSizeBytes: &size, IssuedAt: issuedAt.UTC(), ExpiresAt: expiresAt.UTC()}
	if grant.Validate() != nil {
		return blob.StageGrant{}, fmt.Errorf("database returned invalid Blob stage grant")
	}
	return grant, nil
}

func (adapter *PostgresAdapter) resumeStage(ctx context.Context, grant blob.StageGrant, recordType, originDigest, recordID string) (blob.BlobRef, bool, error) {
	var state string
	var blobID, digest, mediaType sql.NullString
	var size sql.NullInt64
	var committedAt sql.NullTime
	err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT stage_state,blob_id::TEXT,content_digest,media_type,size_bytes,committed_at
			FROM blob.resume_agent_control_stage($1,$2,$3,$4,$5,$6,$7,$8)`,
			grant.StageID, adapter.principal, grant.ExpectedDigest, *grant.ExpectedSizeBytes,
			recordType, recordID, originDigest, adapter.principal).
			Scan(&state, &blobID, &digest, &mediaType, &size, &committedAt)
	})
	if err != nil {
		return blob.BlobRef{}, false, fmt.Errorf("resume Blob stage: %w", err)
	}
	if state != "committed" {
		if state != "open" && state != "materialized" {
			return blob.BlobRef{}, false, fmt.Errorf("Blob stage cannot resume from %s", state)
		}
		return blob.BlobRef{}, false, nil
	}
	result := blob.BlobRef{SchemaRevision: blob.SchemaRevisionV1, BlobID: blobID.String,
		ContentDigest: digest.String, MediaType: mediaType.String, SizeBytes: size.Int64,
		Origin: contracts.RecordRef{Owner: contracts.OwnerAgentControl, RecordType: recordType, RecordID: recordID,
			SchemaRevision: contracts.SchemaRevisionV1, RecordDigest: originDigest}, CommittedAt: committedAt.Time.UTC()}
	if !blobID.Valid || !digest.Valid || !mediaType.Valid || !size.Valid || !committedAt.Valid || result.Validate() != nil {
		return blob.BlobRef{}, false, fmt.Errorf("database returned invalid committed Blob stage")
	}
	return result, true, nil
}

func (adapter *PostgresAdapter) commitStage(ctx context.Context, staged blob.StagedBlob, recordType, originDigest, recordID string) (blob.BlobRef, error) {
	result := blob.BlobRef{SchemaRevision: blob.SchemaRevisionV1,
		Origin: contracts.RecordRef{Owner: contracts.OwnerAgentControl, RecordType: recordType, RecordID: recordID,
			SchemaRevision: contracts.SchemaRevisionV1, RecordDigest: originDigest}}
	err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT blob_id::TEXT,content_digest,media_type,size_bytes,committed_at
			FROM blob.commit_stage($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			staged.Grant.StageID, adapter.principal, staged.ContentDigest, staged.SizeBytes,
			"agent_control", recordType, recordID, originDigest, adapter.principal).
			Scan(&result.BlobID, &result.ContentDigest, &result.MediaType, &result.SizeBytes, &result.CommittedAt)
	})
	result.CommittedAt = result.CommittedAt.UTC()
	if err != nil || result.Validate() != nil {
		return blob.BlobRef{}, fmt.Errorf("commit Blob stage: %w", errors.Join(err, result.Validate()))
	}
	return result, nil
}

func (adapter *PostgresAdapter) withRoleTx(ctx context.Context, operation func(*sql.Tx) error) error {
	tx, err := adapter.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL ROLE "+controlDatabaseRole); err != nil {
		return err
	}
	if err := operation(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func deterministicStageID(principal, inputID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{"alpheus-cortex-input-stage-v1", principal, inputID}, "\n")))
	digest[6] = (digest[6] & 0x0f) | 0x50
	digest[8] = (digest[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}

func deterministicControlStageID(principal, recordType, recordID string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{"alpheus-cortex-control-stage-v1", principal, recordType, recordID}, "\n")))
	digest[6] = (digest[6] & 0x0f) | 0x50
	digest[8] = (digest[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
}
