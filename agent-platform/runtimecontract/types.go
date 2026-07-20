// Package runtimecontract defines AP1's durable Control Plane and bounded
// Worker contracts. The Control Plane is deterministic software and remains
// the sole owner of durable Runtime state. Workers submit fenced commands; they
// never receive table-writing, Kernel, GRACE, Delegation, or activation power.
package runtimecontract

import (
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
)

const SchemaRevisionV1 uint16 = 1

// Absolute collection ceilings are parser/resource safety bounds. Operational
// limits below them belong to versioned RuntimePolicy rows and frozen ledgers.
const (
	AbsoluteMaxDependenciesV1     = 4096
	AbsoluteMaxArtifactSectionsV1 = 256
	AbsoluteMaxReferencesV1       = 4096
)

type TriggerKind string

const (
	TriggerSchedule          TriggerKind = "schedule"
	TriggerKernelEvent       TriggerKind = "kernel_event"
	TriggerExternalEvent     TriggerKind = "external_event"
	TriggerSystemMaintenance TriggerKind = "system_maintenance"
	TriggerSystemRecovery    TriggerKind = "system_recovery"
)

type RunState string

const (
	RunQueued       RunState = "queued"
	RunRunning      RunState = "running"
	RunWaiting      RunState = "waiting"
	RunCanceling    RunState = "canceling"
	RunSucceeded    RunState = "succeeded"
	RunFailed       RunState = "failed"
	RunCanceled     RunState = "canceled"
	RunSuperseded   RunState = "superseded"
	RunDeadLettered RunState = "dead_lettered"
)

type TaskState string

const (
	TaskBlocked         TaskState = "blocked"
	TaskReady           TaskState = "ready"
	TaskRunning         TaskState = "running"
	TaskWaiting         TaskState = "waiting"
	TaskResultCommitted TaskState = "result_committed"
	TaskSucceeded       TaskState = "succeeded"
	TaskFailed          TaskState = "failed"
	TaskCanceled        TaskState = "canceled"
	TaskSuperseded      TaskState = "superseded"
	TaskDeadLettered    TaskState = "dead_lettered"
)

type SessionState string

const (
	SessionOpen   SessionState = "open"
	SessionClosed SessionState = "closed"
)

type AttemptState string

const (
	AttemptLeased          AttemptState = "leased"
	AttemptExecuting       AttemptState = "executing"
	AttemptResultCommitted AttemptState = "result_committed"
	AttemptFailed          AttemptState = "failed"
	AttemptTimedOut        AttemptState = "timed_out"
	AttemptCanceled        AttemptState = "canceled"
	AttemptSuperseded      AttemptState = "superseded"
)

type TurnKind string

const (
	TurnModelCall TurnKind = "model_call"
)

type TurnState string

const (
	TurnPlanned         TurnState = "planned"
	TurnDispatched      TurnState = "dispatched"
	TurnResultCommitted TurnState = "result_committed"
	TurnFailed          TurnState = "failed"
	TurnUnknown         TurnState = "unknown"
	TurnCanceled        TurnState = "canceled"
)

type BudgetScope string

const (
	BudgetRun  BudgetScope = "run"
	BudgetTask BudgetScope = "task"
)

type BudgetState string

const (
	BudgetOpen      BudgetState = "open"
	BudgetExhausted BudgetState = "exhausted"
	BudgetClosed    BudgetState = "closed"
)

type PublicationIntentState string

const (
	// AP1 has no BehaviorEvent contract. This is deliberately the only v1
	// state and therefore cannot be used as a provisional scoreable identity.
	PublicationDisabled PublicationIntentState = "disabled"
)

type CancellationMode string

const (
	CancellationCancel    CancellationMode = "cancel"
	CancellationSupersede CancellationMode = "supersede"
)

type CancellationTarget string

const (
	CancellationRun  CancellationTarget = "run"
	CancellationTask CancellationTarget = "task"
)

type RecoveryDecision string

const (
	RecoveryReuseCommittedResult RecoveryDecision = "reuse_committed_result"
	RecoveryRetrySameTask        RecoveryDecision = "retry_same_task"
	RecoveryDeadLetter           RecoveryDecision = "dead_letter"
	RecoveryCanceled             RecoveryDecision = "canceled"
)

// RetryClass selects the frozen retry budget charged by a failed Attempt.
// Failure.Retryable describes whether retry is allowed; this enum makes the
// corresponding budget explicit instead of inferring it from a free-form
// failure code.
type RetryClass string

const (
	RetryNone           RetryClass = "none"
	RetryInvalidOutput  RetryClass = "invalid_output"
	RetryInfrastructure RetryClass = "infrastructure"
)

type RuntimeSubject string

const (
	SubjectRun     RuntimeSubject = "run"
	SubjectTask    RuntimeSubject = "task"
	SubjectSession RuntimeSubject = "session"
	SubjectAttempt RuntimeSubject = "attempt"
	SubjectTurn    RuntimeSubject = "turn"
	SubjectBudget  RuntimeSubject = "budget"
	SubjectIntent  RuntimeSubject = "publication_intent"
)

type ModelFinishReason string

const (
	FinishStop          ModelFinishReason = "stop"
	FinishToolUse       ModelFinishReason = "tool_use"
	FinishLength        ModelFinishReason = "length"
	FinishContentFilter ModelFinishReason = "content_filter"
)

type OutputValidationTarget string

const (
	ValidationTargetModelResultOutput OutputValidationTarget = "model_result_output"
)

type OutputValidationDecision string

const (
	ValidationDecisionValid OutputValidationDecision = "valid"
)

const (
	OutputValidationSchemaDialect = "https://json-schema.org/draft/2020-12/schema"
	OutputValidationProfile       = "alpheus_json_schema_2020_12_local_v1"
)

// BudgetLimit is frozen from one database-owned RuntimePolicy when a Run or
// Task is created. Zero is a valid denial for any optional resource.
type BudgetLimit struct {
	MaxModelCalls            int64 `json:"max_model_calls"`
	MaxInputTokens           int64 `json:"max_input_tokens"`
	MaxOutputTokens          int64 `json:"max_output_tokens"`
	MaxToolCalls             int64 `json:"max_tool_calls"`
	MaxExternalCostMicroUSD  int64 `json:"max_external_cost_micro_usd"`
	MaxWallTimeMS            int64 `json:"max_wall_time_ms"`
	MaxIdleTimeMS            int64 `json:"max_idle_time_ms"`
	MaxTasks                 int64 `json:"max_tasks"`
	MaxDepth                 int64 `json:"max_depth"`
	MaxFanout                int64 `json:"max_fanout"`
	MaxParallelism           int64 `json:"max_parallelism"`
	MaxInvalidOutputRetries  int64 `json:"max_invalid_output_retries"`
	MaxInfrastructureRetries int64 `json:"max_infrastructure_retries"`
}

type BudgetUsage struct {
	ModelCalls            int64 `json:"model_calls"`
	InputTokens           int64 `json:"input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ToolCalls             int64 `json:"tool_calls"`
	ExternalCostMicroUSD  int64 `json:"external_cost_micro_usd"`
	WallTimeMS            int64 `json:"wall_time_ms"`
	Tasks                 int64 `json:"tasks"`
	ActiveTasks           int64 `json:"active_tasks"`
	InvalidOutputRetries  int64 `json:"invalid_output_retries"`
	InfrastructureRetries int64 `json:"infrastructure_retries"`
}

// OutputContractRevision freezes the schema an AP1 Task must satisfy. It is a
// deliberately small data contract: Control owns the immutable schema bytes,
// and AP1 cannot use it to authorize any external effect.
type OutputContractRevision struct {
	SchemaRevision uint16                `json:"schema_revision"`
	RevisionID     string                `json:"revision_id"`
	Generation     int64                 `json:"generation"`
	ArtifactType   string                `json:"artifact_type"`
	Schema         blob.BlobRef          `json:"schema"`
	EffectClass    contracts.EffectClass `json:"effect_class"`
	Author         contracts.AuditActor  `json:"author"`
	ReasonCode     string                `json:"reason_code"`
	CreatedAt      time.Time             `json:"created_at"`
}

func (value OutputContractRevision) Ref() (contracts.RevisionRef, error) {
	if value.Validate() != nil {
		return contracts.RevisionRef{}, ErrInvalidRuntime
	}
	digest, err := canonical.Digest("agent-platform.contract.output_contract_revision.v1", value)
	if err != nil {
		return contracts.RevisionRef{}, err
	}
	return contracts.RevisionRef{
		RecordRef: contracts.RecordRef{
			Owner:          contracts.OwnerAgentControl,
			RecordType:     "output_contract_revision",
			RecordID:       value.RevisionID,
			SchemaRevision: SchemaRevisionV1,
			RecordDigest:   digest,
		},
		Generation: value.Generation,
	}, nil
}

type RuntimePolicy struct {
	SchemaRevision             uint16               `json:"schema_revision"`
	PolicyID                   string               `json:"policy_id"`
	Generation                 int64                `json:"generation"`
	DefaultRunLimit            BudgetLimit          `json:"default_run_limit"`
	MaxLeaseSeconds            int64                `json:"max_lease_seconds"`
	MaxHeartbeatExtensionSecs  int64                `json:"max_heartbeat_extension_seconds"`
	MaxClaimBatch              int64                `json:"max_claim_batch"`
	MaxDependencies            int64                `json:"max_dependencies"`
	MaxArtifactSections        int64                `json:"max_artifact_sections"`
	DeadLetterRetentionSeconds int64                `json:"dead_letter_retention_seconds"`
	UpdatedBy                  contracts.AuditActor `json:"updated_by"`
	UpdatedAt                  time.Time            `json:"updated_at"`
}

type TriggerRegistration struct {
	SchemaRevision uint16                `json:"schema_revision"`
	RegistrationID string                `json:"registration_id"`
	Generation     int64                 `json:"generation"`
	Kind           TriggerKind           `json:"kind"`
	SourceKey      string                `json:"source_key"`
	OwnerPolicy    contracts.RevisionRef `json:"owner_policy"`
	RuntimePolicy  contracts.RevisionRef `json:"runtime_policy"`
	Enabled        bool                  `json:"enabled"`
	UpdatedBy      contracts.AuditActor  `json:"updated_by"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

type TriggerOccurrence struct {
	SchemaRevision  uint16                 `json:"schema_revision"`
	OccurrenceID    string                 `json:"occurrence_id"`
	Registration    *contracts.RevisionRef `json:"registration,omitempty"`
	Kind            TriggerKind            `json:"kind"`
	Source          contracts.RecordRef    `json:"source"`
	InitiatingActor contracts.AuditActor   `json:"initiating_actor"`
	OwnerPolicy     contracts.RevisionRef  `json:"owner_policy"`
	OccurrenceKey   string                 `json:"occurrence_key"`
	Payload         *blob.BlobRef          `json:"payload,omitempty"`
	OccurredAt      time.Time              `json:"occurred_at"`
	ObservedAt      time.Time              `json:"observed_at"`
	CommittedAt     time.Time              `json:"committed_at"`
}

type Run struct {
	SchemaRevision  uint16                `json:"schema_revision"`
	RunID           string                `json:"run_id"`
	Occurrence      *contracts.RecordRef  `json:"occurrence,omitempty"`
	Origin          contracts.RunOrigin   `json:"origin"`
	RuntimePolicy   contracts.RevisionRef `json:"runtime_policy"`
	BudgetLedgerID  string                `json:"budget_ledger_id"`
	RootTaskID      string                `json:"root_task_id"`
	State           RunState              `json:"state"`
	StateGeneration int64                 `json:"state_generation"`
	SupersededBy    string                `json:"superseded_by,omitempty"`
	Failure         *contracts.Failure    `json:"failure,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
	DeadlineAt      time.Time             `json:"deadline_at"`
	TerminalAt      *time.Time            `json:"terminal_at,omitempty"`
}

type Task struct {
	SchemaRevision   uint16                `json:"schema_revision"`
	TaskID           string                `json:"task_id"`
	RunID            string                `json:"run_id"`
	ParentTaskID     string                `json:"parent_task_id,omitempty"`
	Depth            int64                 `json:"depth"`
	Objective        blob.BlobRef          `json:"objective"`
	InputRefs        []contracts.RecordRef `json:"input_refs"`
	OutputContract   contracts.RevisionRef `json:"output_contract"`
	BudgetLedgerID   string                `json:"budget_ledger_id"`
	SessionID        string                `json:"session_id,omitempty"`
	ResultArtifactID string                `json:"result_artifact_id,omitempty"`
	State            TaskState             `json:"state"`
	StateGeneration  int64                 `json:"state_generation"`
	Failure          *contracts.Failure    `json:"failure,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	DeadlineAt       time.Time             `json:"deadline_at"`
	TerminalAt       *time.Time            `json:"terminal_at,omitempty"`
}

type Dependency struct {
	SchemaRevision  uint16    `json:"schema_revision"`
	TaskID          string    `json:"task_id"`
	DependsOnTaskID string    `json:"depends_on_task_id"`
	RequiresSuccess bool      `json:"requires_success"`
	CreatedAt       time.Time `json:"created_at"`
}

type Session struct {
	SchemaRevision     uint16       `json:"schema_revision"`
	SessionID          string       `json:"session_id"`
	RunID              string       `json:"run_id"`
	TaskID             string       `json:"task_id"`
	Generation         int64        `json:"generation"`
	ExecutionBinding   blob.BlobRef `json:"execution_binding"`
	ContextManifest    blob.BlobRef `json:"context_manifest"`
	LatestCheckpointID string       `json:"latest_checkpoint_id,omitempty"`
	State              SessionState `json:"state"`
	CreatedAt          time.Time    `json:"created_at"`
	ClosedAt           *time.Time   `json:"closed_at,omitempty"`
}

type AttemptLease struct {
	Generation  int64                `json:"generation"`
	Token       string               `json:"token"`
	Worker      contracts.AuditActor `json:"worker"`
	ClaimedAt   time.Time            `json:"claimed_at"`
	HeartbeatAt time.Time            `json:"heartbeat_at"`
	ExpiresAt   time.Time            `json:"expires_at"`
}

type Attempt struct {
	SchemaRevision  uint16               `json:"schema_revision"`
	AttemptID       string               `json:"attempt_id"`
	RunID           string               `json:"run_id"`
	TaskID          string               `json:"task_id"`
	SessionID       string               `json:"session_id"`
	Ordinal         int64                `json:"ordinal"`
	State           AttemptState         `json:"state"`
	StateGeneration int64                `json:"state_generation"`
	Lease           AttemptLease         `json:"lease"`
	ResultArtifact  *contracts.RecordRef `json:"result_artifact,omitempty"`
	Failure         *contracts.Failure   `json:"failure,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	TerminalAt      *time.Time           `json:"terminal_at,omitempty"`
}

type Turn struct {
	SchemaRevision  uint16               `json:"schema_revision"`
	TurnID          string               `json:"turn_id"`
	RunID           string               `json:"run_id"`
	TaskID          string               `json:"task_id"`
	SessionID       string               `json:"session_id"`
	AttemptID       string               `json:"attempt_id"`
	Ordinal         int64                `json:"ordinal"`
	Kind            TurnKind             `json:"kind"`
	State           TurnState            `json:"state"`
	StateGeneration int64                `json:"state_generation"`
	RequestDigest   string               `json:"request_digest"`
	Result          *contracts.RecordRef `json:"result,omitempty"`
	Failure         *contracts.Failure   `json:"failure,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
	DispatchedAt    *time.Time           `json:"dispatched_at,omitempty"`
	FinishedAt      *time.Time           `json:"finished_at,omitempty"`
}

type ModelCallManifest struct {
	SchemaRevision               uint16       `json:"schema_revision"`
	CallID                       string       `json:"call_id"`
	TurnID                       string       `json:"turn_id"`
	AttemptID                    string       `json:"attempt_id"`
	IdempotencyKey               string       `json:"idempotency_key"`
	Provider                     string       `json:"provider"`
	Model                        string       `json:"model"`
	PromptDigest                 string       `json:"prompt_digest"`
	ContextManifest              blob.BlobRef `json:"context_manifest"`
	OutputContractDigest         string       `json:"output_contract_digest"`
	RequestDigest                string       `json:"request_digest"`
	MaxOutputTokens              int64        `json:"max_output_tokens"`
	ReservedInputTokens          int64        `json:"reserved_input_tokens"`
	ReservedExternalCostMicroUSD int64        `json:"reserved_external_cost_micro_usd"`
	TimeoutMS                    int64        `json:"timeout_ms"`
	TemperatureMicros            int64        `json:"temperature_micros"`
	CreatedAt                    time.Time    `json:"created_at"`
}

type ModelCallResult struct {
	SchemaRevision       uint16            `json:"schema_revision"`
	ResultID             string            `json:"result_id"`
	CallID               string            `json:"call_id"`
	AttemptID            string            `json:"attempt_id"`
	TurnID               string            `json:"turn_id"`
	IdempotencyKey       string            `json:"idempotency_key"`
	RequestDigest        string            `json:"request_digest"`
	ProviderRequestID    string            `json:"provider_request_id"`
	Output               blob.BlobRef      `json:"output"`
	InputTokens          int64             `json:"input_tokens"`
	OutputTokens         int64             `json:"output_tokens"`
	ExternalCostMicroUSD int64             `json:"external_cost_micro_usd"`
	WallTimeMS           int64             `json:"wall_time_ms"`
	FinishReason         ModelFinishReason `json:"finish_reason"`
	CommittedAt          time.Time         `json:"committed_at"`
}

// OutputValidationReceipt is Control-owned proof that one exact model-result
// Blob was validated against one exact OutputContract schema and one exact
// ArtifactCandidate digest. Presence means valid; AP1 has no self-asserted or
// mutable verdict state.
type OutputValidationReceipt struct {
	SchemaRevision          uint16                   `json:"schema_revision"`
	ReceiptID               string                   `json:"receipt_id"`
	RunID                   string                   `json:"run_id"`
	TaskID                  string                   `json:"task_id"`
	SessionID               string                   `json:"session_id"`
	AttemptID               string                   `json:"attempt_id"`
	SourceResult            contracts.RecordRef      `json:"source_result"`
	OutputContract          contracts.RevisionRef    `json:"output_contract"`
	Schema                  blob.BlobRef             `json:"schema"`
	Output                  blob.BlobRef             `json:"output"`
	ArtifactCandidateDigest string                   `json:"artifact_candidate_digest"`
	ValidationTarget        OutputValidationTarget   `json:"validation_target"`
	SchemaDialect           string                   `json:"schema_dialect"`
	ValidationProfile       string                   `json:"validation_profile"`
	ValidatorImplementation string                   `json:"validator_implementation"`
	ValidatorBuildDigest    string                   `json:"validator_build_digest"`
	Decision                OutputValidationDecision `json:"decision"`
	ValidatedBy             contracts.AuditActor     `json:"validated_by"`
	ValidatedAt             time.Time                `json:"validated_at"`
}

func (value OutputValidationReceipt) Ref() (contracts.RecordRef, error) {
	if value.Validate() != nil {
		return contracts.RecordRef{}, ErrInvalidRuntime
	}
	digest, err := canonical.Digest("agent-platform.contract.output_validation_receipt.v1", value)
	if err != nil {
		return contracts.RecordRef{}, err
	}
	return contracts.RecordRef{
		Owner: contracts.OwnerAgentControl, RecordType: "output_validation_receipt",
		RecordID: value.ReceiptID, SchemaRevision: SchemaRevisionV1, RecordDigest: digest,
	}, nil
}

type ArtifactSection struct {
	Name     string       `json:"name"`
	Required bool         `json:"required"`
	Content  blob.BlobRef `json:"content"`
}

// ModelCallManifestCandidate is Worker-supplied intent. The stable call ID is
// proposed for replay; lineage, timestamps, state, and acceptance remain owned
// by the Control Plane transaction.
type ModelCallManifestCandidate struct {
	CallID                       string       `json:"call_id"`
	IdempotencyKey               string       `json:"idempotency_key"`
	Provider                     string       `json:"provider"`
	Model                        string       `json:"model"`
	PromptDigest                 string       `json:"prompt_digest"`
	ContextManifest              blob.BlobRef `json:"context_manifest"`
	OutputContractDigest         string       `json:"output_contract_digest"`
	RequestDigest                string       `json:"request_digest"`
	MaxOutputTokens              int64        `json:"max_output_tokens"`
	ReservedInputTokens          int64        `json:"reserved_input_tokens"`
	ReservedExternalCostMicroUSD int64        `json:"reserved_external_cost_micro_usd"`
	TimeoutMS                    int64        `json:"timeout_ms"`
	TemperatureMicros            int64        `json:"temperature_micros"`
}

// ModelCallResultCandidate contains provider facts only. The Control Plane
// derives the durable Result identity, Attempt/Turn lineage and commit time.
type ModelCallResultCandidate struct {
	CallID               string            `json:"call_id"`
	RequestDigest        string            `json:"request_digest"`
	ProviderRequestID    string            `json:"provider_request_id"`
	Output               blob.BlobRef      `json:"output"`
	InputTokens          int64             `json:"input_tokens"`
	OutputTokens         int64             `json:"output_tokens"`
	ExternalCostMicroUSD int64             `json:"external_cost_micro_usd"`
	WallTimeMS           int64             `json:"wall_time_ms"`
	FinishReason         ModelFinishReason `json:"finish_reason"`
}

// ArtifactCandidate is untrusted Worker output. The Control Plane derives all
// canonical identity, lineage and time fields when accepting it.
type ArtifactCandidate struct {
	ArtifactType         string                `json:"artifact_type"`
	OutputContractDigest string                `json:"output_contract_digest"`
	EffectClass          contracts.EffectClass `json:"effect_class"`
	Sections             []ArtifactSection     `json:"sections"`
}

type Artifact struct {
	SchemaRevision       uint16                `json:"schema_revision"`
	ArtifactID           string                `json:"artifact_id"`
	RunID                string                `json:"run_id"`
	TaskID               string                `json:"task_id"`
	SessionID            string                `json:"session_id"`
	AttemptID            string                `json:"attempt_id"`
	SourceResult         contracts.RecordRef   `json:"source_result"`
	ArtifactType         string                `json:"artifact_type"`
	OutputContractDigest string                `json:"output_contract_digest"`
	EffectClass          contracts.EffectClass `json:"effect_class"`
	Sections             []ArtifactSection     `json:"sections"`
	CreatedAt            time.Time             `json:"created_at"`
}

type ArtifactPublicationIntent struct {
	SchemaRevision uint16                 `json:"schema_revision"`
	IntentID       string                 `json:"intent_id"`
	Artifact       contracts.RecordRef    `json:"artifact"`
	State          PublicationIntentState `json:"state"`
	ReasonCode     string                 `json:"reason_code"`
	CreatedAt      time.Time              `json:"created_at"`
}

type Checkpoint struct {
	SchemaRevision       uint16                `json:"schema_revision"`
	CheckpointID         string                `json:"checkpoint_id"`
	RunID                string                `json:"run_id"`
	TaskID               string                `json:"task_id"`
	SessionID            string                `json:"session_id"`
	Generation           int64                 `json:"generation"`
	PreviousCheckpointID string                `json:"previous_checkpoint_id,omitempty"`
	Manifest             blob.BlobRef          `json:"manifest"`
	MustPreserveRefs     []contracts.RecordRef `json:"must_preserve_refs"`
	Narrative            *blob.BlobRef         `json:"narrative,omitempty"`
	CreatedByAttemptID   string                `json:"created_by_attempt_id"`
	CreatedAt            time.Time             `json:"created_at"`
}

type BudgetLedger struct {
	SchemaRevision uint16                `json:"schema_revision"`
	LedgerID       string                `json:"ledger_id"`
	Scope          BudgetScope           `json:"scope"`
	ScopeID        string                `json:"scope_id"`
	ParentLedgerID string                `json:"parent_ledger_id,omitempty"`
	RuntimePolicy  contracts.RevisionRef `json:"runtime_policy"`
	Limit          BudgetLimit           `json:"limit"`
	Consumed       BudgetUsage           `json:"consumed"`
	Reserved       BudgetUsage           `json:"reserved"`
	Generation     int64                 `json:"generation"`
	State          BudgetState           `json:"state"`
	UpdatedAt      time.Time             `json:"updated_at"`
}

type CancellationRequest struct {
	SchemaRevision          uint16               `json:"schema_revision"`
	RequestID               string               `json:"request_id"`
	Target                  CancellationTarget   `json:"target"`
	TargetID                string               `json:"target_id"`
	ExpectedStateGeneration int64                `json:"expected_state_generation"`
	Mode                    CancellationMode     `json:"mode"`
	SupersededByRunID       string               `json:"superseded_by_run_id,omitempty"`
	Actor                   contracts.AuditActor `json:"actor"`
	ReasonCode              string               `json:"reason_code"`
	RequestedAt             time.Time            `json:"requested_at"`
}

type RecoveryRecord struct {
	SchemaRevision         uint16               `json:"schema_revision"`
	RecoveryID             string               `json:"recovery_id"`
	RunID                  string               `json:"run_id"`
	TaskID                 string               `json:"task_id"`
	PreviousAttemptID      string               `json:"previous_attempt_id"`
	OriginalCausationID    string               `json:"original_causation_id"`
	OriginalIdempotencyKey string               `json:"original_idempotency_key"`
	Decision               RecoveryDecision     `json:"decision"`
	CommittedArtifact      *contracts.RecordRef `json:"committed_artifact,omitempty"`
	NextAttemptID          string               `json:"next_attempt_id,omitempty"`
	ReasonCode             string               `json:"reason_code"`
	DecidedAt              time.Time            `json:"decided_at"`
}

type RuntimeEvent struct {
	SchemaRevision uint16               `json:"schema_revision"`
	EventID        string               `json:"event_id"`
	Subject        RuntimeSubject       `json:"subject"`
	SubjectID      string               `json:"subject_id"`
	FromState      string               `json:"from_state,omitempty"`
	ToState        string               `json:"to_state"`
	Generation     int64                `json:"generation"`
	Actor          contracts.AuditActor `json:"actor"`
	CausationID    string               `json:"causation_id"`
	CorrelationID  string               `json:"correlation_id"`
	ReasonCode     string               `json:"reason_code"`
	OccurredAt     time.Time            `json:"occurred_at"`
}

type ClaimTaskCommand struct {
	SchemaRevision              uint16                    `json:"schema_revision"`
	Envelope                    contracts.CommandEnvelope `json:"envelope"`
	TaskID                      string                    `json:"task_id"`
	ExpectedTaskStateGeneration int64                     `json:"expected_task_state_generation"`
	RequestedLeaseSeconds       int64                     `json:"requested_lease_seconds"`
}

type HeartbeatAttemptCommand struct {
	SchemaRevision                 uint16                    `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope `json:"envelope"`
	AttemptID                      string                    `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                     `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                     `json:"lease_generation"`
	LeaseToken                     string                    `json:"lease_token"`
	RequestedExtensionSeconds      int64                     `json:"requested_extension_seconds"`
}

type StartAttemptCommand struct {
	SchemaRevision                 uint16                    `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope `json:"envelope"`
	AttemptID                      string                    `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                     `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                     `json:"lease_generation"`
	LeaseToken                     string                    `json:"lease_token"`
}

// DispatchModelCallCommand durably creates the Turn and Manifest and reserves
// worst-case budget before the Worker performs the external model call.
type DispatchModelCallCommand struct {
	SchemaRevision                 uint16                     `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope  `json:"envelope"`
	AttemptID                      string                     `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                      `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                      `json:"lease_generation"`
	LeaseToken                     string                     `json:"lease_token"`
	TurnID                         string                     `json:"turn_id"`
	Manifest                       ModelCallManifestCandidate `json:"manifest"`
}

// ResolveModelCallCommand records a successful or failed provider outcome. It
// also resolves an existing unknown Turn without creating another provider
// call identity. MarkModelCallUnknownCommand owns the unresolved transition.
type ResolveModelCallCommand struct {
	SchemaRevision                 uint16                    `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope `json:"envelope"`
	AttemptID                      string                    `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                     `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                     `json:"lease_generation"`
	LeaseToken                     string                    `json:"lease_token"`
	TurnID                         string                    `json:"turn_id"`
	ExpectedTurnStateGeneration    int64                     `json:"expected_turn_state_generation"`
	Outcome                        TurnState                 `json:"outcome"`
	Result                         *ModelCallResultCandidate `json:"result,omitempty"`
	Failure                        *contracts.Failure        `json:"failure,omitempty"`
}

type MarkModelCallUnknownCommand struct {
	SchemaRevision                 uint16                    `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope `json:"envelope"`
	AttemptID                      string                    `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                     `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                     `json:"lease_generation"`
	LeaseToken                     string                    `json:"lease_token"`
	TurnID                         string                    `json:"turn_id"`
	ExpectedTurnStateGeneration    int64                     `json:"expected_turn_state_generation"`
	Failure                        contracts.Failure         `json:"failure"`
}

type CommitAttemptCommand struct {
	SchemaRevision                 uint16                    `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope `json:"envelope"`
	AttemptID                      string                    `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                     `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                     `json:"lease_generation"`
	LeaseToken                     string                    `json:"lease_token"`
	Result                         contracts.RecordRef       `json:"result"`
	Artifact                       ArtifactCandidate         `json:"artifact"`
}

// RecordOutputValidationCommand is submitted by the deterministic Control
// path after its pinned validator has checked the exact schema/output bytes.
// Durable identity, lineage, time, decision and the ArtifactCandidate digest
// remain Control-owned; Workers cannot author this command.
type RecordOutputValidationCommand struct {
	SchemaRevision          uint16                    `json:"schema_revision"`
	Envelope                contracts.CommandEnvelope `json:"envelope"`
	AttemptID               string                    `json:"attempt_id"`
	Result                  contracts.RecordRef       `json:"result"`
	Artifact                ArtifactCandidate         `json:"artifact"`
	SchemaBindingID         string                    `json:"schema_binding_id"`
	OutputBindingID         string                    `json:"output_binding_id"`
	ValidatorImplementation string                    `json:"validator_implementation"`
	ValidatorBuildDigest    string                    `json:"validator_build_digest"`
}

type FailAttemptCommand struct {
	SchemaRevision                 uint16                    `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope `json:"envelope"`
	AttemptID                      string                    `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                     `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                     `json:"lease_generation"`
	LeaseToken                     string                    `json:"lease_token"`
	RetryClass                     RetryClass                `json:"retry_class"`
	Failure                        contracts.Failure         `json:"failure"`
}

type RequestChildTaskCommand struct {
	SchemaRevision                 uint16                    `json:"schema_revision"`
	Envelope                       contracts.CommandEnvelope `json:"envelope"`
	ParentTaskID                   string                    `json:"parent_task_id"`
	AttemptID                      string                    `json:"attempt_id"`
	ExpectedAttemptStateGeneration int64                     `json:"expected_attempt_state_generation"`
	LeaseGeneration                int64                     `json:"lease_generation"`
	LeaseToken                     string                    `json:"lease_token"`
	Objective                      blob.BlobRef              `json:"objective"`
	InputRefs                      []contracts.RecordRef     `json:"input_refs"`
	OutputContract                 contracts.RevisionRef     `json:"output_contract"`
	RequestedLimit                 BudgetLimit               `json:"requested_limit"`
}
