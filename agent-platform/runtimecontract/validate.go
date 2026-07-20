package runtimecontract

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
)

var (
	ErrInvalidRuntime = errors.New("invalid runtime contract")
	namePattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

func (value BudgetLimit) Validate() error {
	values := []int64{
		value.MaxModelCalls, value.MaxInputTokens, value.MaxOutputTokens,
		value.MaxToolCalls, value.MaxExternalCostMicroUSD, value.MaxWallTimeMS,
		value.MaxIdleTimeMS, value.MaxTasks, value.MaxDepth, value.MaxFanout,
		value.MaxParallelism, value.MaxInvalidOutputRetries,
		value.MaxInfrastructureRetries,
	}
	for _, item := range values {
		if item < 0 {
			return ErrInvalidRuntime
		}
	}
	return nil
}

func (value BudgetUsage) Validate() error {
	values := []int64{
		value.ModelCalls, value.InputTokens, value.OutputTokens, value.ToolCalls,
		value.ExternalCostMicroUSD, value.WallTimeMS, value.Tasks,
		value.ActiveTasks, value.InvalidOutputRetries,
		value.InfrastructureRetries,
	}
	for _, item := range values {
		if item < 0 {
			return ErrInvalidRuntime
		}
	}
	if value.ActiveTasks > value.Tasks {
		return ErrInvalidRuntime
	}
	return nil
}

func (value OutputContractRevision) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.RevisionID) ||
		value.Generation <= 0 || !namePattern.MatchString(value.ArtifactType) ||
		!validRuntimeBlob(value.Schema, "output_contract_schema") ||
		value.Schema.MediaType != "application/json" ||
		value.EffectClass != contracts.EffectNone || value.Author.Validate() != nil ||
		value.Author.Kind != contracts.PrincipalWorkload ||
		value.Author.Audience != contracts.AudienceControlAPI ||
		!namePattern.MatchString(value.ReasonCode) || !validUTC(value.CreatedAt) ||
		value.Schema.CommittedAt.After(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value RuntimePolicy) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.PolicyID) ||
		value.Generation <= 0 || value.DefaultRunLimit.Validate() != nil ||
		value.DefaultRunLimit.MaxWallTimeMS <= 0 || value.DefaultRunLimit.MaxTasks <= 0 ||
		value.DefaultRunLimit.MaxParallelism <= 0 || value.MaxLeaseSeconds <= 0 ||
		value.MaxHeartbeatExtensionSecs <= 0 || value.MaxClaimBatch <= 0 ||
		value.MaxHeartbeatExtensionSecs > value.MaxLeaseSeconds ||
		value.MaxDependencies <= 0 || value.MaxDependencies > AbsoluteMaxDependenciesV1 ||
		value.MaxArtifactSections <= 0 || value.MaxArtifactSections > AbsoluteMaxArtifactSectionsV1 ||
		value.DeadLetterRetentionSeconds <= 0 || value.UpdatedBy.Validate() != nil ||
		!validUTC(value.UpdatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value TriggerRegistration) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.RegistrationID) ||
		value.Generation <= 0 || !knownTriggerKind(value.Kind) || value.Kind == TriggerSystemRecovery ||
		!validID(value.SourceKey) ||
		!validRevision(value.OwnerPolicy, contracts.OwnerPlatformGovernance, "owner_policy_revision") ||
		!validRevision(value.RuntimePolicy, contracts.OwnerAgentControl, "runtime_policy") ||
		value.UpdatedBy.Validate() != nil || !validUTC(value.UpdatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value TriggerOccurrence) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.OccurrenceID) ||
		!knownTriggerKind(value.Kind) || value.Source.Validate() != nil ||
		value.InitiatingActor.Validate() != nil ||
		!validRevision(value.OwnerPolicy, contracts.OwnerPlatformGovernance, "owner_policy_revision") ||
		!validID(value.OccurrenceKey) || !orderedUTC(value.OccurredAt, value.ObservedAt, value.CommittedAt) {
		return ErrInvalidRuntime
	}
	if value.Kind == TriggerSystemRecovery {
		if value.Registration != nil {
			return ErrInvalidRuntime
		}
	} else if value.Registration == nil ||
		!validRevision(*value.Registration, contracts.OwnerAgentControl, "trigger_registration") {
		return ErrInvalidRuntime
	}
	if value.Payload != nil && (!validRuntimeBlob(*value.Payload, "trigger_payload") ||
		value.Payload.CommittedAt.After(value.CommittedAt)) {
		return ErrInvalidRuntime
	}
	switch value.Kind {
	case TriggerKernelEvent:
		if value.Source.Owner != contracts.OwnerKernel || value.Source.RecordType != "kernel_event" ||
			value.InitiatingActor.Kind != contracts.PrincipalKernel ||
			value.InitiatingActor.Audience != contracts.AudienceKernel {
			return ErrInvalidRuntime
		}
	case TriggerSchedule:
		if value.Source.Owner != contracts.OwnerAgentControl || value.Source.RecordType != "schedule_occurrence" ||
			value.InitiatingActor.Kind != contracts.PrincipalWorkload ||
			value.InitiatingActor.Audience != contracts.AudienceControlAPI {
			return ErrInvalidRuntime
		}
	case TriggerExternalEvent:
		if value.Source.Owner != contracts.OwnerAgentControl || value.Source.RecordType != "external_event" ||
			value.InitiatingActor.Kind != contracts.PrincipalWorkload ||
			value.InitiatingActor.Audience != contracts.AudienceControlAPI {
			return ErrInvalidRuntime
		}
	case TriggerSystemMaintenance:
		if value.Source.Owner != contracts.OwnerAgentControl || value.Source.RecordType != "maintenance_occurrence" ||
			value.InitiatingActor.Kind != contracts.PrincipalWorkload ||
			value.InitiatingActor.Audience != contracts.AudienceControlAPI {
			return ErrInvalidRuntime
		}
	case TriggerSystemRecovery:
		if value.Source.Owner != contracts.OwnerAgentControl || value.Source.RecordType != "recovery_occurrence" ||
			value.InitiatingActor.Kind != contracts.PrincipalWorkload ||
			value.InitiatingActor.Audience != contracts.AudienceControlAPI {
			return ErrInvalidRuntime
		}
	}
	return nil
}

func (value Run) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.RunID) ||
		value.Origin.Validate() != nil ||
		!validRevision(value.Origin.OwnerPolicy, contracts.OwnerPlatformGovernance, "owner_policy_revision") ||
		!validRevision(value.RuntimePolicy, contracts.OwnerAgentControl, "runtime_policy") ||
		!validID(value.BudgetLedgerID) || !validID(value.RootTaskID) ||
		!knownRunState(value.State) || value.StateGeneration <= 0 ||
		!orderedUTC(value.CreatedAt, value.UpdatedAt) || !validUTC(value.DeadlineAt) ||
		!value.CreatedAt.Before(value.DeadlineAt) {
		return ErrInvalidRuntime
	}
	if value.Origin.Kind == contracts.OriginUserRequest {
		if value.Occurrence != nil {
			return ErrInvalidRuntime
		}
	} else if value.Occurrence == nil ||
		!validRecord(*value.Occurrence, contracts.OwnerAgentControl, "trigger_occurrence") {
		return ErrInvalidRuntime
	}
	if value.CreatedAt.Before(value.Origin.CommittedAt) {
		return ErrInvalidRuntime
	}
	if value.State == RunSuperseded {
		if !validID(value.SupersededBy) || value.SupersededBy == value.RunID {
			return ErrInvalidRuntime
		}
	} else if value.SupersededBy != "" {
		return ErrInvalidRuntime
	}
	if (value.State == RunFailed || value.State == RunDeadLettered) != (value.Failure != nil) {
		return ErrInvalidRuntime
	}
	if value.Failure != nil && value.Failure.Validate() != nil {
		return ErrInvalidRuntime
	}
	return validateTerminalTime(terminalRunState(value.State), value.CreatedAt, value.UpdatedAt, value.TerminalAt)
}

func (value Task) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.TaskID) ||
		!validID(value.RunID) || value.Depth < 0 ||
		!validRuntimeBlob(value.Objective, "task_objective") || value.Objective.CommittedAt.After(value.CreatedAt) ||
		len(value.InputRefs) > AbsoluteMaxReferencesV1 ||
		!validRevision(value.OutputContract, contracts.OwnerAgentControl, "output_contract_revision") ||
		!validID(value.BudgetLedgerID) || !knownTaskState(value.State) ||
		value.StateGeneration <= 0 || !orderedUTC(value.CreatedAt, value.UpdatedAt) ||
		!validUTC(value.DeadlineAt) || !value.CreatedAt.Before(value.DeadlineAt) {
		return ErrInvalidRuntime
	}
	if value.Depth == 0 && value.ParentTaskID != "" ||
		value.Depth > 0 && (!validID(value.ParentTaskID) || value.ParentTaskID == value.TaskID) {
		return ErrInvalidRuntime
	}
	if !validRecordRefs(value.InputRefs) {
		return ErrInvalidRuntime
	}
	needsSession := value.State == TaskRunning || value.State == TaskResultCommitted || value.State == TaskSucceeded
	if needsSession && !validID(value.SessionID) || !needsSession && value.SessionID != "" && !validID(value.SessionID) {
		return ErrInvalidRuntime
	}
	needsResult := value.State == TaskResultCommitted || value.State == TaskSucceeded
	if needsResult && !validID(value.ResultArtifactID) || !needsResult && value.ResultArtifactID != "" && !validID(value.ResultArtifactID) {
		return ErrInvalidRuntime
	}
	if (value.State == TaskFailed || value.State == TaskDeadLettered) != (value.Failure != nil) {
		return ErrInvalidRuntime
	}
	if value.Failure != nil && value.Failure.Validate() != nil {
		return ErrInvalidRuntime
	}
	return validateTerminalTime(terminalTaskState(value.State), value.CreatedAt, value.UpdatedAt, value.TerminalAt)
}

func (value Dependency) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.TaskID) ||
		!validID(value.DependsOnTaskID) || value.TaskID == value.DependsOnTaskID ||
		!validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value Session) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.SessionID) ||
		!validID(value.RunID) || !validID(value.TaskID) || value.Generation <= 0 ||
		!validRuntimeBlob(value.ExecutionBinding, "execution_binding") ||
		value.ExecutionBinding.CommittedAt.After(value.CreatedAt) ||
		!validRuntimeBlob(value.ContextManifest, "context_manifest") ||
		value.ContextManifest.CommittedAt.After(value.CreatedAt) ||
		!knownSessionState(value.State) || !validUTC(value.CreatedAt) ||
		value.LatestCheckpointID != "" && !validID(value.LatestCheckpointID) {
		return ErrInvalidRuntime
	}
	if value.State == SessionClosed {
		if value.ClosedAt == nil || !validUTC(*value.ClosedAt) || value.ClosedAt.Before(value.CreatedAt) {
			return ErrInvalidRuntime
		}
	} else if value.ClosedAt != nil {
		return ErrInvalidRuntime
	}
	return nil
}

func (value AttemptLease) Validate() error {
	if value.Generation <= 0 || !validID(value.Token) || value.Worker.Validate() != nil ||
		value.Worker.Kind != contracts.PrincipalWorkload ||
		value.Worker.Audience != contracts.AudienceWorker ||
		!orderedUTC(value.ClaimedAt, value.HeartbeatAt, value.ExpiresAt) ||
		!value.HeartbeatAt.Before(value.ExpiresAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value Attempt) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.AttemptID) ||
		!validID(value.RunID) || !validID(value.TaskID) || !validID(value.SessionID) ||
		value.Ordinal <= 0 || !knownAttemptState(value.State) || value.StateGeneration <= 0 ||
		value.Lease.Validate() != nil || !orderedUTC(value.CreatedAt, value.UpdatedAt) ||
		value.CreatedAt.Before(value.Lease.ClaimedAt) || value.UpdatedAt.Before(value.Lease.HeartbeatAt) {
		return ErrInvalidRuntime
	}
	needsResult := value.State == AttemptResultCommitted
	if needsResult != (value.ResultArtifact != nil) || value.ResultArtifact != nil &&
		!validRecord(*value.ResultArtifact, contracts.OwnerAgentControl, "artifact") {
		return ErrInvalidRuntime
	}
	needsFailure := value.State == AttemptFailed || value.State == AttemptTimedOut
	if needsFailure != (value.Failure != nil) || value.Failure != nil && value.Failure.Validate() != nil {
		return ErrInvalidRuntime
	}
	return validateTerminalTime(terminalAttemptState(value.State), value.CreatedAt, value.UpdatedAt, value.TerminalAt)
}

func (value Turn) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.TurnID) ||
		!validID(value.RunID) || !validID(value.TaskID) || !validID(value.SessionID) ||
		!validID(value.AttemptID) || value.Ordinal <= 0 || value.Kind != TurnModelCall ||
		!knownTurnState(value.State) || value.StateGeneration <= 0 ||
		!validDigest(value.RequestDigest) || !orderedUTC(value.CreatedAt, value.UpdatedAt) {
		return ErrInvalidRuntime
	}
	if value.DispatchedAt != nil && (!validUTC(*value.DispatchedAt) || value.DispatchedAt.Before(value.CreatedAt)) ||
		value.FinishedAt != nil && (!validUTC(*value.FinishedAt) || value.FinishedAt.Before(value.CreatedAt)) ||
		value.DispatchedAt != nil && value.FinishedAt != nil && value.FinishedAt.Before(*value.DispatchedAt) {
		return ErrInvalidRuntime
	}
	switch value.State {
	case TurnPlanned:
		if !value.UpdatedAt.Equal(value.CreatedAt) || value.DispatchedAt != nil || value.FinishedAt != nil ||
			value.Result != nil || value.Failure != nil {
			return ErrInvalidRuntime
		}
	case TurnDispatched:
		if value.DispatchedAt == nil || value.UpdatedAt.Before(*value.DispatchedAt) ||
			value.FinishedAt != nil || value.Result != nil || value.Failure != nil {
			return ErrInvalidRuntime
		}
	case TurnResultCommitted:
		if value.DispatchedAt == nil || value.FinishedAt == nil || value.UpdatedAt.Before(*value.FinishedAt) ||
			value.Result == nil ||
			!validRecord(*value.Result, contracts.OwnerAgentControl, "model_call_result") || value.Failure != nil {
			return ErrInvalidRuntime
		}
	case TurnFailed:
		if value.DispatchedAt == nil || value.FinishedAt == nil || value.UpdatedAt.Before(*value.FinishedAt) ||
			value.Result != nil ||
			value.Failure == nil || value.Failure.Validate() != nil {
			return ErrInvalidRuntime
		}
	case TurnUnknown:
		if value.DispatchedAt == nil || value.UpdatedAt.Before(*value.DispatchedAt) ||
			value.FinishedAt != nil || value.Result != nil ||
			value.Failure == nil || value.Failure.Validate() != nil {
			return ErrInvalidRuntime
		}
	case TurnCanceled:
		if value.FinishedAt == nil || value.UpdatedAt.Before(*value.FinishedAt) ||
			value.Result != nil || value.Failure != nil {
			return ErrInvalidRuntime
		}
	}
	return nil
}

func (value ModelCallManifest) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.CallID) ||
		!validID(value.TurnID) || !validID(value.AttemptID) || !validID(value.IdempotencyKey) ||
		!validID(value.Provider) || !validID(value.Model) || !validDigest(value.PromptDigest) ||
		!validRuntimeBlob(value.ContextManifest, "context_manifest") ||
		value.ContextManifest.CommittedAt.After(value.CreatedAt) || !validDigest(value.OutputContractDigest) ||
		!validDigest(value.RequestDigest) || value.MaxOutputTokens <= 0 || value.TimeoutMS <= 0 ||
		value.ReservedInputTokens < 0 || value.ReservedExternalCostMicroUSD < 0 ||
		value.TemperatureMicros < 0 || !validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ModelCallResult) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ResultID) ||
		!validID(value.CallID) || !validID(value.AttemptID) || !validID(value.TurnID) ||
		!validID(value.IdempotencyKey) || !validDigest(value.RequestDigest) ||
		!validID(value.ProviderRequestID) || value.Output.Validate() != nil ||
		value.Output.Origin.Owner != contracts.OwnerAgentControl || value.Output.Origin.RecordType != "model_call_manifest" ||
		value.Output.Origin.RecordID != value.CallID || value.Output.CommittedAt.After(value.CommittedAt) ||
		value.InputTokens < 0 || value.OutputTokens < 0 || value.ExternalCostMicroUSD < 0 || value.WallTimeMS < 0 ||
		!knownFinishReason(value.FinishReason) ||
		!validUTC(value.CommittedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ArtifactSection) Validate() error {
	if !namePattern.MatchString(value.Name) || value.Content.Validate() != nil ||
		value.Content.Origin.Owner != contracts.OwnerAgentControl {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ModelCallManifestCandidate) Validate() error {
	manifest := ModelCallManifest{
		SchemaRevision: SchemaRevisionV1, CallID: value.CallID, TurnID: "candidate-turn",
		AttemptID: "candidate-attempt", IdempotencyKey: value.IdempotencyKey,
		Provider: value.Provider, Model: value.Model, PromptDigest: value.PromptDigest,
		ContextManifest: value.ContextManifest, OutputContractDigest: value.OutputContractDigest,
		RequestDigest: value.RequestDigest, MaxOutputTokens: value.MaxOutputTokens,
		ReservedInputTokens:          value.ReservedInputTokens,
		ReservedExternalCostMicroUSD: value.ReservedExternalCostMicroUSD,
		TimeoutMS:                    value.TimeoutMS, TemperatureMicros: value.TemperatureMicros,
		CreatedAt: value.ContextManifest.CommittedAt,
	}
	return manifest.Validate()
}

func (value ModelCallResultCandidate) Validate() error {
	if value.Output.Origin.Owner != contracts.OwnerAgentControl ||
		value.Output.Origin.RecordType != "model_call_manifest" ||
		value.Output.Origin.RecordID != value.CallID {
		return ErrInvalidRuntime
	}
	result := ModelCallResult{
		SchemaRevision: SchemaRevisionV1, ResultID: "candidate-result", CallID: value.CallID,
		AttemptID: "candidate-attempt", TurnID: "candidate-turn", IdempotencyKey: "candidate-key",
		RequestDigest: value.RequestDigest, ProviderRequestID: value.ProviderRequestID,
		Output: value.Output, InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
		ExternalCostMicroUSD: value.ExternalCostMicroUSD, WallTimeMS: value.WallTimeMS,
		FinishReason: value.FinishReason, CommittedAt: value.Output.CommittedAt,
	}
	return result.Validate()
}

func (value ArtifactCandidate) Validate() error {
	createdAt := time.Unix(1, 0).UTC()
	for _, section := range value.Sections {
		if section.Content.CommittedAt.After(createdAt) {
			createdAt = section.Content.CommittedAt
		}
	}
	artifact := Artifact{
		SchemaRevision: SchemaRevisionV1, ArtifactID: "candidate-artifact", RunID: "candidate-run",
		TaskID: "candidate-task", SessionID: "candidate-session", AttemptID: "candidate-attempt",
		SourceResult: contracts.RecordRef{
			Owner: contracts.OwnerAgentControl, RecordType: "model_call_result",
			RecordID: "candidate-result", SchemaRevision: contracts.SchemaRevisionV1,
			RecordDigest: strings.Repeat("a", 64),
		},
		ArtifactType: value.ArtifactType, OutputContractDigest: value.OutputContractDigest,
		EffectClass: value.EffectClass, Sections: value.Sections, CreatedAt: createdAt,
	}
	return artifact.Validate()
}

func (value Artifact) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ArtifactID) ||
		!validID(value.RunID) || !validID(value.TaskID) || !validID(value.SessionID) ||
		!validID(value.AttemptID) ||
		!validRecord(value.SourceResult, contracts.OwnerAgentControl, "model_call_result") ||
		!namePattern.MatchString(value.ArtifactType) ||
		!validDigest(value.OutputContractDigest) || value.EffectClass != contracts.EffectNone ||
		len(value.Sections) == 0 || len(value.Sections) > AbsoluteMaxArtifactSectionsV1 ||
		!validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	seen := make(map[string]struct{}, len(value.Sections))
	required := false
	for _, section := range value.Sections {
		if section.Validate() != nil || section.Content.CommittedAt.After(value.CreatedAt) {
			return ErrInvalidRuntime
		}
		if _, exists := seen[section.Name]; exists {
			return ErrInvalidRuntime
		}
		seen[section.Name] = struct{}{}
		required = required || section.Required
	}
	if !required {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ArtifactPublicationIntent) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.IntentID) ||
		!validRecord(value.Artifact, contracts.OwnerAgentControl, "artifact") ||
		value.State != PublicationDisabled || !namePattern.MatchString(value.ReasonCode) ||
		!validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value Checkpoint) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.CheckpointID) ||
		!validID(value.RunID) || !validID(value.TaskID) || !validID(value.SessionID) ||
		value.Generation <= 0 || !validRuntimeBlob(value.Manifest, "checkpoint_manifest") ||
		value.Manifest.CommittedAt.After(value.CreatedAt) ||
		len(value.MustPreserveRefs) > AbsoluteMaxReferencesV1 ||
		!validRecordRefs(value.MustPreserveRefs) || !validID(value.CreatedByAttemptID) ||
		!validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	if value.PreviousCheckpointID != "" &&
		(!validID(value.PreviousCheckpointID) || value.PreviousCheckpointID == value.CheckpointID || value.Generation <= 1) {
		return ErrInvalidRuntime
	}
	if value.Generation > 1 && value.PreviousCheckpointID == "" ||
		value.Narrative != nil && (!validRuntimeBlob(*value.Narrative, "checkpoint_narrative") ||
			value.Narrative.CommittedAt.After(value.CreatedAt)) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value BudgetLedger) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.LedgerID) ||
		!knownBudgetScope(value.Scope) || !validID(value.ScopeID) ||
		!validRevision(value.RuntimePolicy, contracts.OwnerAgentControl, "runtime_policy") ||
		!validRunnableLimit(value.Limit) || value.Consumed.Validate() != nil || value.Reserved.Validate() != nil ||
		!budgetWithin(value.Consumed, value.Reserved, value.Limit) || value.Generation <= 0 ||
		!knownBudgetState(value.State) || !validUTC(value.UpdatedAt) {
		return ErrInvalidRuntime
	}
	if value.Scope == BudgetRun && value.ParentLedgerID != "" ||
		value.Scope == BudgetTask && !validID(value.ParentLedgerID) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value CancellationRequest) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.RequestID) ||
		!knownCancellationTarget(value.Target) || !validID(value.TargetID) ||
		value.ExpectedStateGeneration <= 0 || !knownCancellationMode(value.Mode) ||
		value.Actor.Validate() != nil || !namePattern.MatchString(value.ReasonCode) ||
		!validUTC(value.RequestedAt) {
		return ErrInvalidRuntime
	}
	if value.Mode == CancellationSupersede {
		if !validID(value.SupersededByRunID) || value.Target != CancellationRun || value.SupersededByRunID == value.TargetID {
			return ErrInvalidRuntime
		}
	} else if value.SupersededByRunID != "" {
		return ErrInvalidRuntime
	}
	return nil
}

func (value RecoveryRecord) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.RecoveryID) ||
		!validID(value.RunID) || !validID(value.TaskID) || !validID(value.PreviousAttemptID) ||
		!validID(value.OriginalCausationID) || !validID(value.OriginalIdempotencyKey) ||
		!knownRecoveryDecision(value.Decision) || !namePattern.MatchString(value.ReasonCode) ||
		!validUTC(value.DecidedAt) {
		return ErrInvalidRuntime
	}
	switch value.Decision {
	case RecoveryReuseCommittedResult:
		if value.CommittedArtifact == nil ||
			!validRecord(*value.CommittedArtifact, contracts.OwnerAgentControl, "artifact") || value.NextAttemptID != "" {
			return ErrInvalidRuntime
		}
	case RecoveryRetrySameTask:
		if value.CommittedArtifact != nil || !validID(value.NextAttemptID) || value.NextAttemptID == value.PreviousAttemptID {
			return ErrInvalidRuntime
		}
	default:
		if value.CommittedArtifact != nil || value.NextAttemptID != "" {
			return ErrInvalidRuntime
		}
	}
	return nil
}

func (value RuntimeEvent) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EventID) ||
		!knownRuntimeSubject(value.Subject) || !validID(value.SubjectID) ||
		!knownSubjectState(value.Subject, value.ToState) || value.Generation <= 0 ||
		value.Actor.Validate() != nil || !validID(value.CausationID) ||
		!validID(value.CorrelationID) || !namePattern.MatchString(value.ReasonCode) ||
		!validUTC(value.OccurredAt) {
		return ErrInvalidRuntime
	}
	if value.FromState != "" && (!knownSubjectState(value.Subject, value.FromState) || value.Generation <= 1 ||
		value.FromState == value.ToState || !CanTransition(value.Subject, value.FromState, value.ToState)) {
		return ErrInvalidRuntime
	}
	if value.FromState == "" && value.Generation != 1 {
		return ErrInvalidRuntime
	}
	if value.FromState == "" && !initialSubjectState(value.Subject, value.ToState) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ClaimTaskCommand) Validate() error {
	if !validWorkerEnvelope(value.SchemaRevision, value.Envelope, "claim_task") ||
		!validID(value.TaskID) || value.ExpectedTaskStateGeneration <= 0 ||
		value.RequestedLeaseSeconds <= 0 {
		return ErrInvalidRuntime
	}
	return nil
}

func (value HeartbeatAttemptCommand) Validate() error {
	if !validWorkerEnvelope(value.SchemaRevision, value.Envelope, "heartbeat_attempt") ||
		!validID(value.AttemptID) || value.ExpectedAttemptStateGeneration <= 0 ||
		value.LeaseGeneration <= 0 || !validID(value.LeaseToken) || value.RequestedExtensionSeconds <= 0 {
		return ErrInvalidRuntime
	}
	return nil
}

func (value StartAttemptCommand) Validate() error {
	return validateAttemptFence(value.SchemaRevision, value.Envelope, "start_attempt", value.AttemptID,
		value.ExpectedAttemptStateGeneration, value.LeaseGeneration, value.LeaseToken)
}

func (value DispatchModelCallCommand) Validate() error {
	if validateAttemptFence(value.SchemaRevision, value.Envelope, "dispatch_model_call", value.AttemptID,
		value.ExpectedAttemptStateGeneration, value.LeaseGeneration, value.LeaseToken) != nil ||
		!validID(value.TurnID) || value.Manifest.Validate() != nil {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ResolveModelCallCommand) Validate() error {
	if validateAttemptFence(value.SchemaRevision, value.Envelope, "resolve_model_call", value.AttemptID,
		value.ExpectedAttemptStateGeneration, value.LeaseGeneration, value.LeaseToken) != nil ||
		!validID(value.TurnID) || value.ExpectedTurnStateGeneration <= 0 {
		return ErrInvalidRuntime
	}
	switch value.Outcome {
	case TurnResultCommitted:
		if value.Result == nil || value.Result.Validate() != nil || value.Failure != nil {
			return ErrInvalidRuntime
		}
	case TurnFailed:
		if value.Result != nil || value.Failure == nil || value.Failure.Validate() != nil {
			return ErrInvalidRuntime
		}
	default:
		return ErrInvalidRuntime
	}
	return nil
}

func (value MarkModelCallUnknownCommand) Validate() error {
	if validateAttemptFence(value.SchemaRevision, value.Envelope, "mark_model_call_unknown", value.AttemptID,
		value.ExpectedAttemptStateGeneration, value.LeaseGeneration, value.LeaseToken) != nil ||
		!validID(value.TurnID) || value.ExpectedTurnStateGeneration <= 0 || value.Failure.Validate() != nil {
		return ErrInvalidRuntime
	}
	return nil
}

func (value CommitAttemptCommand) Validate() error {
	if validateAttemptFence(value.SchemaRevision, value.Envelope, "commit_attempt", value.AttemptID,
		value.ExpectedAttemptStateGeneration, value.LeaseGeneration, value.LeaseToken) != nil ||
		!validRecord(value.Result, contracts.OwnerAgentControl, "model_call_result") ||
		value.Artifact.Validate() != nil {
		return ErrInvalidRuntime
	}
	return nil
}

func (value FailAttemptCommand) Validate() error {
	if validateAttemptFence(value.SchemaRevision, value.Envelope, "fail_attempt", value.AttemptID,
		value.ExpectedAttemptStateGeneration, value.LeaseGeneration, value.LeaseToken) != nil ||
		value.Failure.Validate() != nil {
		return ErrInvalidRuntime
	}
	return nil
}

func (value RequestChildTaskCommand) Validate() error {
	if !validWorkerEnvelope(value.SchemaRevision, value.Envelope, "request_child_task") ||
		!validID(value.ParentTaskID) || !validID(value.AttemptID) ||
		value.ExpectedAttemptStateGeneration <= 0 || value.LeaseGeneration <= 0 ||
		!validID(value.LeaseToken) || !validRuntimeBlob(value.Objective, "task_objective") ||
		len(value.InputRefs) > AbsoluteMaxReferencesV1 || !validRecordRefs(value.InputRefs) ||
		!validRevision(value.OutputContract, contracts.OwnerAgentControl, "output_contract_revision") ||
		!validRunnableLimit(value.RequestedLimit) {
		return ErrInvalidRuntime
	}
	return nil
}

func validateAttemptFence(revision uint16, envelope contracts.CommandEnvelope, commandType, attemptID string,
	stateGeneration, leaseGeneration int64, leaseToken string) error {
	if !validWorkerEnvelope(revision, envelope, commandType) || !validID(attemptID) ||
		stateGeneration <= 0 || leaseGeneration <= 0 || !validID(leaseToken) {
		return ErrInvalidRuntime
	}
	return nil
}

func validWorkerEnvelope(revision uint16, envelope contracts.CommandEnvelope, commandType string) bool {
	return revision == SchemaRevisionV1 && envelope.Validate() == nil &&
		envelope.CommandType == commandType && envelope.Audience == contracts.AudienceControlAPI &&
		envelope.Actor.Kind == contracts.PrincipalWorkload && envelope.Actor.Audience == contracts.AudienceWorker
}

func validRunnableLimit(value BudgetLimit) bool {
	return value.Validate() == nil && value.MaxWallTimeMS > 0 && value.MaxParallelism > 0
}

func validRecord(value contracts.RecordRef, owner contracts.Owner, recordType string) bool {
	return value.Validate() == nil && value.Owner == owner && value.RecordType == recordType
}

func validRevision(value contracts.RevisionRef, owner contracts.Owner, recordType string) bool {
	return value.Validate() == nil && value.Owner == owner && value.RecordType == recordType
}

func validRecordRefs(values []contracts.RecordRef) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value.Validate() != nil {
			return false
		}
		key := string(value.Owner) + "\x00" + value.RecordType + "\x00" + value.RecordID + "\x00" +
			fmt.Sprint(value.SchemaRevision)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validRuntimeBlob(value blob.BlobRef, recordType string) bool {
	return value.Validate() == nil && value.Origin.Owner == contracts.OwnerAgentControl &&
		value.Origin.RecordType == recordType
}

func budgetWithin(consumed, reserved BudgetUsage, limit BudgetLimit) bool {
	checks := [][3]int64{
		{consumed.ModelCalls, reserved.ModelCalls, limit.MaxModelCalls},
		{consumed.InputTokens, reserved.InputTokens, limit.MaxInputTokens},
		{consumed.OutputTokens, reserved.OutputTokens, limit.MaxOutputTokens},
		{consumed.ToolCalls, reserved.ToolCalls, limit.MaxToolCalls},
		{consumed.ExternalCostMicroUSD, reserved.ExternalCostMicroUSD, limit.MaxExternalCostMicroUSD},
		{consumed.WallTimeMS, reserved.WallTimeMS, limit.MaxWallTimeMS},
		{consumed.Tasks, reserved.Tasks, limit.MaxTasks},
		{consumed.InvalidOutputRetries, reserved.InvalidOutputRetries, limit.MaxInvalidOutputRetries},
		{consumed.InfrastructureRetries, reserved.InfrastructureRetries, limit.MaxInfrastructureRetries},
	}
	for _, check := range checks {
		if check[0] > check[2] || check[1] > check[2]-check[0] {
			return false
		}
	}
	return consumed.ActiveTasks+reserved.ActiveTasks <= limit.MaxParallelism
}

func validateTerminalTime(terminal bool, created, updated time.Time, value *time.Time) error {
	if terminal {
		if value == nil || !validUTC(*value) || value.Before(created) || value.After(updated) {
			return ErrInvalidRuntime
		}
	} else if value != nil {
		return ErrInvalidRuntime
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 200 {
		return false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func orderedUTC(values ...time.Time) bool {
	for index, value := range values {
		if !validUTC(value) || index > 0 && value.Before(values[index-1]) {
			return false
		}
	}
	return true
}
