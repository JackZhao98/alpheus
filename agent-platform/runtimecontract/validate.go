package runtimecontract

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

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
		value.Generation <= 0 || !knownTriggerKind(value.Kind) || !validID(value.SourceKey) ||
		!validRevision(value.OwnerPolicy, contracts.OwnerPlatformGovernance, "owner_policy") ||
		!validRevision(value.RuntimePolicy, contracts.OwnerAgentControl, "runtime_policy") ||
		value.UpdatedBy.Validate() != nil || !validUTC(value.UpdatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value TriggerOccurrence) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.OccurrenceID) ||
		!validRevision(value.Registration, contracts.OwnerAgentControl, "trigger_registration") ||
		!knownTriggerKind(value.Kind) || value.Source.Validate() != nil ||
		value.InitiatingActor.Validate() != nil ||
		!validRevision(value.OwnerPolicy, contracts.OwnerPlatformGovernance, "owner_policy") ||
		!validID(value.OccurrenceKey) || !orderedUTC(value.OccurredAt, value.ObservedAt, value.CommittedAt) {
		return ErrInvalidRuntime
	}
	if value.Payload != nil && value.Payload.Validate() != nil {
		return ErrInvalidRuntime
	}
	switch value.Kind {
	case TriggerKernelEvent:
		if value.Source.Owner != contracts.OwnerKernel ||
			value.InitiatingActor.Kind != contracts.PrincipalKernel ||
			value.InitiatingActor.Audience != contracts.AudienceKernel {
			return ErrInvalidRuntime
		}
	default:
		if value.Source.Owner != contracts.OwnerAgentControl ||
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
		!validRevision(value.RuntimePolicy, contracts.OwnerAgentControl, "runtime_policy") ||
		!validID(value.BudgetLedgerID) || !validID(value.RootTaskID) ||
		!knownRunState(value.State) || value.StateGeneration <= 0 ||
		!orderedUTC(value.CreatedAt, value.UpdatedAt) || !validUTC(value.DeadlineAt) ||
		!value.CreatedAt.Before(value.DeadlineAt) {
		return ErrInvalidRuntime
	}
	if value.Occurrence != nil && !validRecord(*value.Occurrence, contracts.OwnerAgentControl, "trigger_occurrence") {
		return ErrInvalidRuntime
	}
	if value.Origin.Kind == contracts.OriginUserRequest {
		if value.Occurrence != nil {
			return ErrInvalidRuntime
		}
	} else if value.Occurrence == nil {
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
	return validateTerminalTime(terminalRunState(value.State), value.CreatedAt, value.TerminalAt)
}

func (value Task) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.TaskID) ||
		!validID(value.RunID) || value.Depth < 0 || !validDigest(value.ObjectiveDigest) ||
		len(value.InputRefs) > AbsoluteMaxReferencesV1 ||
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
	return validateTerminalTime(terminalTaskState(value.State), value.CreatedAt, value.TerminalAt)
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
		!validDigest(value.ExecutionBindingDigest) || !validDigest(value.ContextManifestDigest) ||
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
		value.Lease.Validate() != nil || !orderedUTC(value.CreatedAt, value.UpdatedAt) {
		return ErrInvalidRuntime
	}
	needsResult := value.State == AttemptResultCommitted
	if needsResult != (value.ResultDigest != "") || value.ResultDigest != "" && !validDigest(value.ResultDigest) {
		return ErrInvalidRuntime
	}
	needsFailure := value.State == AttemptFailed || value.State == AttemptTimedOut
	if needsFailure != (value.Failure != nil) || value.Failure != nil && value.Failure.Validate() != nil {
		return ErrInvalidRuntime
	}
	return validateTerminalTime(terminalAttemptState(value.State), value.CreatedAt, value.TerminalAt)
}

func (value Turn) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.TurnID) ||
		!validID(value.RunID) || !validID(value.TaskID) || !validID(value.SessionID) ||
		!validID(value.AttemptID) || value.Ordinal <= 0 || value.Kind != TurnModelCall ||
		!knownTurnState(value.State) || !validDigest(value.RequestDigest) || !validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	if value.DispatchedAt != nil && (!validUTC(*value.DispatchedAt) || value.DispatchedAt.Before(value.CreatedAt)) ||
		value.FinishedAt != nil && (!validUTC(*value.FinishedAt) || value.FinishedAt.Before(value.CreatedAt)) ||
		value.DispatchedAt != nil && value.FinishedAt != nil && value.FinishedAt.Before(*value.DispatchedAt) {
		return ErrInvalidRuntime
	}
	switch value.State {
	case TurnPlanned:
		if value.DispatchedAt != nil || value.FinishedAt != nil || value.ResultDigest != "" || value.Failure != nil {
			return ErrInvalidRuntime
		}
	case TurnDispatched:
		if value.DispatchedAt == nil || value.FinishedAt != nil || value.ResultDigest != "" || value.Failure != nil {
			return ErrInvalidRuntime
		}
	case TurnResultCommitted:
		if value.DispatchedAt == nil || value.FinishedAt == nil || !validDigest(value.ResultDigest) || value.Failure != nil {
			return ErrInvalidRuntime
		}
	case TurnFailed, TurnUnknown:
		if value.DispatchedAt == nil || value.FinishedAt == nil || value.ResultDigest != "" ||
			value.Failure == nil || value.Failure.Validate() != nil {
			return ErrInvalidRuntime
		}
	case TurnCanceled:
		if value.FinishedAt == nil || value.ResultDigest != "" || value.Failure != nil {
			return ErrInvalidRuntime
		}
	}
	return nil
}

func (value ModelCallManifest) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.CallID) ||
		!validID(value.TurnID) || !validID(value.AttemptID) || !validID(value.IdempotencyKey) ||
		!validID(value.Provider) || !validID(value.Model) || !validDigest(value.PromptDigest) ||
		!validDigest(value.ContextManifestDigest) || !validDigest(value.OutputContractDigest) ||
		!validDigest(value.RequestDigest) || value.MaxOutputTokens <= 0 || value.TimeoutMS <= 0 ||
		value.TemperatureMicros < 0 || !validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ModelCallResult) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ResultID) ||
		!validID(value.CallID) || !validID(value.IdempotencyKey) || !validDigest(value.RequestDigest) ||
		!validID(value.ProviderRequestID) || value.Output.Validate() != nil ||
		value.Output.Origin.Owner != contracts.OwnerWorker || value.Output.Origin.RecordType != "model_call_output" ||
		value.InputTokens < 0 || value.OutputTokens < 0 || !knownFinishReason(value.FinishReason) ||
		!validUTC(value.CommittedAt) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value ArtifactSection) Validate() error {
	if !namePattern.MatchString(value.Name) || value.Content.Validate() != nil ||
		value.Content.Origin.Owner != contracts.OwnerWorker {
		return ErrInvalidRuntime
	}
	return nil
}

func (value Artifact) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ArtifactID) ||
		!validID(value.RunID) || !validID(value.TaskID) || !validID(value.SessionID) ||
		!validID(value.AttemptID) || !namePattern.MatchString(value.ArtifactType) ||
		!validDigest(value.OutputContractDigest) || value.EffectClass != contracts.EffectNone ||
		len(value.Sections) == 0 || len(value.Sections) > AbsoluteMaxArtifactSectionsV1 ||
		!validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	seen := make(map[string]struct{}, len(value.Sections))
	required := false
	for _, section := range value.Sections {
		if section.Validate() != nil {
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
		value.Generation <= 0 || !validDigest(value.ManifestDigest) ||
		len(value.MustPreserveRefs) > AbsoluteMaxReferencesV1 ||
		!validRecordRefs(value.MustPreserveRefs) || !validID(value.CreatedByAttemptID) ||
		!validUTC(value.CreatedAt) {
		return ErrInvalidRuntime
	}
	if value.PreviousCheckpointID != "" &&
		(!validID(value.PreviousCheckpointID) || value.PreviousCheckpointID == value.CheckpointID || value.Generation <= 1) {
		return ErrInvalidRuntime
	}
	if value.Generation > 1 && value.PreviousCheckpointID == "" || value.Narrative != nil && value.Narrative.Validate() != nil {
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

func (value CommitAttemptCommand) Validate() error {
	if !validWorkerEnvelope(value.SchemaRevision, value.Envelope, "commit_attempt") ||
		!validID(value.AttemptID) || value.ExpectedAttemptStateGeneration <= 0 ||
		value.LeaseGeneration <= 0 || !validID(value.LeaseToken) ||
		value.Result.Validate() != nil || value.Artifact.Validate() != nil ||
		value.Artifact.AttemptID != value.AttemptID || value.Artifact.CreatedAt.Before(value.Result.CommittedAt) ||
		!artifactContainsOutput(value.Artifact, value.Result) {
		return ErrInvalidRuntime
	}
	return nil
}

func (value RequestChildTaskCommand) Validate() error {
	if !validWorkerEnvelope(value.SchemaRevision, value.Envelope, "request_child_task") ||
		!validID(value.ParentTaskID) || !validID(value.AttemptID) ||
		value.ExpectedAttemptStateGeneration <= 0 || value.LeaseGeneration <= 0 ||
		!validID(value.LeaseToken) || !validDigest(value.ObjectiveDigest) ||
		len(value.InputRefs) > AbsoluteMaxReferencesV1 || !validRecordRefs(value.InputRefs) ||
		!validRunnableLimit(value.RequestedLimit) {
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
		key := string(value.Owner) + "\x00" + value.RecordType + "\x00" + value.RecordID + "\x00" + value.RecordDigest
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func artifactContainsOutput(artifact Artifact, result ModelCallResult) bool {
	for _, section := range artifact.Sections {
		if section.Content.BlobID == result.Output.BlobID &&
			section.Content.ContentDigest == result.Output.ContentDigest &&
			section.Content.SizeBytes == result.Output.SizeBytes {
			return true
		}
	}
	return false
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

func validateTerminalTime(terminal bool, created time.Time, value *time.Time) error {
	if terminal {
		if value == nil || !validUTC(*value) || value.Before(created) {
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
