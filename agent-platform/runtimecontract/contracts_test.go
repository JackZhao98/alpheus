package runtimecontract

import (
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
)

type fixture struct {
	policy       RuntimePolicy
	registration TriggerRegistration
	occurrence   TriggerOccurrence
	run          Run
	task         Task
	dependency   Dependency
	session      Session
	attempt      Attempt
	turn         Turn
	manifest     ModelCallManifest
	result       ModelCallResult
	artifact     Artifact
	publication  ArtifactPublicationIntent
	checkpoint   Checkpoint
	ledger       BudgetLedger
	cancellation CancellationRequest
	recovery     RecoveryRecord
	event        RuntimeEvent
	claim        ClaimTaskCommand
	heartbeat    HeartbeatAttemptCommand
	commit       CommitAttemptCommand
	child        RequestChildTaskCommand
}

func TestContractsValidate(t *testing.T) {
	value := validFixture()
	tests := map[string]interface{ Validate() error }{
		"policy":       value.policy,
		"registration": value.registration,
		"occurrence":   value.occurrence,
		"run":          value.run,
		"task":         value.task,
		"dependency":   value.dependency,
		"session":      value.session,
		"attempt":      value.attempt,
		"turn":         value.turn,
		"manifest":     value.manifest,
		"result":       value.result,
		"artifact":     value.artifact,
		"publication":  value.publication,
		"checkpoint":   value.checkpoint,
		"ledger":       value.ledger,
		"cancellation": value.cancellation,
		"recovery":     value.recovery,
		"event":        value.event,
		"claim":        value.claim,
		"heartbeat":    value.heartbeat,
		"commit":       value.commit,
		"child":        value.child,
	}
	for name, contract := range tests {
		t.Run(name, func(t *testing.T) {
			if err := contract.Validate(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContractsFailClosed(t *testing.T) {
	t.Run("artifact cannot emit operation intent", func(t *testing.T) {
		value := validFixture().artifact
		value.EffectClass = contracts.EffectOperationIntent
		if value.Validate() == nil {
			t.Fatal("operation intent passed AP1 boundary")
		}
	})

	t.Run("publication is disabled", func(t *testing.T) {
		value := validFixture().publication
		value.State = "published"
		if value.Validate() == nil {
			t.Fatal("early publication passed")
		}
	})

	t.Run("budget cannot overcommit", func(t *testing.T) {
		value := validFixture().ledger
		value.Reserved.ModelCalls = value.Limit.MaxModelCalls
		if value.Validate() == nil {
			t.Fatal("overcommitted budget passed")
		}
	})

	t.Run("self dependency denied", func(t *testing.T) {
		value := validFixture().dependency
		value.DependsOnTaskID = value.TaskID
		if value.Validate() == nil {
			t.Fatal("self dependency passed")
		}
	})

	t.Run("worker identity comes from envelope", func(t *testing.T) {
		value := validFixture().claim
		value.Envelope.Actor.Audience = contracts.AudienceControlAPI
		if value.Validate() == nil {
			t.Fatal("non-worker actor passed")
		}
	})

	t.Run("commit binds model output", func(t *testing.T) {
		value := validFixture().commit
		value.Artifact.Sections[0].Content.BlobID = "20000000-0000-4000-8000-000000000099"
		if value.Validate() == nil {
			t.Fatal("unbound artifact output passed")
		}
	})

	t.Run("supersession requires replacement", func(t *testing.T) {
		value := validFixture().cancellation
		value.Mode = CancellationSupersede
		if value.Validate() == nil {
			t.Fatal("unbound supersession passed")
		}
	})

	t.Run("initial event must enter initial state", func(t *testing.T) {
		value := validFixture().event
		value.FromState = ""
		value.ToState = string(TaskSucceeded)
		value.Generation = 1
		if value.Validate() == nil {
			t.Fatal("terminal initial event passed")
		}
	})
}

func TestStateTransitions(t *testing.T) {
	allowed := []struct {
		subject RuntimeSubject
		from    string
		to      string
	}{
		{SubjectRun, string(RunQueued), string(RunRunning)},
		{SubjectRun, string(RunCanceling), string(RunCanceled)},
		{SubjectTask, string(TaskRunning), string(TaskResultCommitted)},
		{SubjectTask, string(TaskResultCommitted), string(TaskSucceeded)},
		{SubjectSession, string(SessionOpen), string(SessionClosed)},
		{SubjectAttempt, string(AttemptExecuting), string(AttemptResultCommitted)},
		{SubjectBudget, string(BudgetOpen), string(BudgetExhausted)},
	}
	for _, test := range allowed {
		if !CanTransition(test.subject, test.from, test.to) {
			t.Fatalf("transition denied: %s %s -> %s", test.subject, test.from, test.to)
		}
	}
	denied := []struct {
		subject RuntimeSubject
		from    string
		to      string
	}{
		{SubjectRun, string(RunSucceeded), string(RunRunning)},
		{SubjectTask, string(TaskSucceeded), string(TaskReady)},
		{SubjectAttempt, string(AttemptResultCommitted), string(AttemptExecuting)},
		{SubjectIntent, string(PublicationDisabled), "published"},
	}
	for _, test := range denied {
		if CanTransition(test.subject, test.from, test.to) {
			t.Fatalf("transition allowed: %s %s -> %s", test.subject, test.from, test.to)
		}
	}
}

func validFixture() fixture {
	t0 := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Minute)
	t2 := t0.Add(2 * time.Minute)
	t3 := t0.Add(3 * time.Minute)
	terminal := t0.Add(4 * time.Minute)
	closed := t0.Add(5 * time.Minute)
	deadline := t0.Add(time.Hour)
	worker := contracts.AuditActor{PrincipalID: "worker-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceWorker}
	control := contracts.AuditActor{PrincipalID: "control-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI}
	ownerPolicy := revision(contracts.OwnerPlatformGovernance, "owner_policy", "owner-policy-1", 1, 'a')
	runtimePolicy := revision(contracts.OwnerAgentControl, "runtime_policy", "runtime-policy-1", 1, 'b')
	registrationRef := revision(contracts.OwnerAgentControl, "trigger_registration", "trigger-1", 1, 'c')
	occurrenceRef := record(contracts.OwnerAgentControl, "trigger_occurrence", "occurrence-1", 'd')
	originSource := record(contracts.OwnerAgentControl, "schedule_occurrence", "schedule-occurrence-1", 'e')
	origin := contracts.RunOrigin{
		SchemaRevision: 1, Kind: contracts.OriginSchedule, Source: originSource,
		InitiatingActor: control, OwnerPolicy: ownerPolicy,
		OccurredAt: t0, ObservedAt: t1, CommittedAt: t2,
	}
	limit := BudgetLimit{
		MaxModelCalls: 10, MaxInputTokens: 10000, MaxOutputTokens: 2000,
		MaxExternalCostMicroUSD: 100000, MaxWallTimeMS: 3600000, MaxIdleTimeMS: 600000,
		MaxTasks: 10, MaxDepth: 3, MaxFanout: 4, MaxParallelism: 2,
		MaxInvalidOutputRetries: 1, MaxInfrastructureRetries: 2,
	}
	output := blob.BlobRef{
		SchemaRevision: 1, BlobID: "20000000-0000-4000-8000-000000000001",
		ContentDigest: digest('f'), MediaType: "application/json", SizeBytes: 128,
		Origin: record(contracts.OwnerWorker, "model_call_output", "result-1", '1'), CommittedAt: t2,
	}
	result := ModelCallResult{
		SchemaRevision: 1, ResultID: "result-1", CallID: "call-1", IdempotencyKey: "call-key-1",
		RequestDigest: digest('2'), ProviderRequestID: "provider-request-1", Output: output,
		InputTokens: 100, OutputTokens: 20, FinishReason: FinishStop, CommittedAt: t2,
	}
	artifact := Artifact{
		SchemaRevision: 1, ArtifactID: "artifact-1", RunID: "run-1", TaskID: "task-1",
		SessionID: "session-1", AttemptID: "attempt-1", ArtifactType: "decision_draft",
		OutputContractDigest: digest('3'), EffectClass: contracts.EffectNone,
		Sections: []ArtifactSection{{Name: "result", Required: true, Content: output}}, CreatedAt: t3,
	}
	envelope := func(commandType string, marker byte) contracts.CommandEnvelope {
		return contracts.CommandEnvelope{
			SchemaRevision: 1, CommandID: "command-" + commandType, Actor: worker,
			Audience: contracts.AudienceControlAPI, CommandType: commandType,
			IdempotencyKey: "key-" + commandType, RequestDigest: digest(marker),
			CausationID: "cause-1", CorrelationID: "correlation-1", Deadline: deadline,
		}
	}
	artifactRef := record(contracts.OwnerAgentControl, "artifact", "artifact-1", '9')
	return fixture{
		policy: RuntimePolicy{
			SchemaRevision: 1, PolicyID: "runtime-policy-1", Generation: 1, DefaultRunLimit: limit,
			MaxLeaseSeconds: 300, MaxHeartbeatExtensionSecs: 60, MaxClaimBatch: 20,
			MaxDependencies: 100, MaxArtifactSections: 32, DeadLetterRetentionSeconds: 86400,
			UpdatedBy: control, UpdatedAt: t0,
		},
		registration: TriggerRegistration{
			SchemaRevision: 1, RegistrationID: "trigger-1", Generation: 1, Kind: TriggerSchedule,
			SourceKey: "daily-research", OwnerPolicy: ownerPolicy, RuntimePolicy: runtimePolicy,
			Enabled: true, UpdatedBy: control, UpdatedAt: t0,
		},
		occurrence: TriggerOccurrence{
			SchemaRevision: 1, OccurrenceID: "occurrence-1", Registration: registrationRef,
			Kind: TriggerSchedule, Source: originSource, InitiatingActor: control, OwnerPolicy: ownerPolicy,
			OccurrenceKey: "2026-07-20", OccurredAt: t0, ObservedAt: t1, CommittedAt: t2,
		},
		run: Run{
			SchemaRevision: 1, RunID: "run-1", Occurrence: &occurrenceRef, Origin: origin,
			RuntimePolicy: runtimePolicy, BudgetLedgerID: "ledger-run-1", RootTaskID: "task-1",
			State: RunSucceeded, StateGeneration: 3, CreatedAt: t0, UpdatedAt: terminal,
			DeadlineAt: deadline, TerminalAt: &terminal,
		},
		task: Task{
			SchemaRevision: 1, TaskID: "task-1", RunID: "run-1", Depth: 0,
			ObjectiveDigest: digest('4'), InputRefs: []contracts.RecordRef{originSource},
			BudgetLedgerID: "ledger-task-1", SessionID: "session-1", ResultArtifactID: "artifact-1",
			State: TaskSucceeded, StateGeneration: 4, CreatedAt: t0, UpdatedAt: terminal,
			DeadlineAt: deadline, TerminalAt: &terminal,
		},
		dependency: Dependency{
			SchemaRevision: 1, TaskID: "task-2", DependsOnTaskID: "task-1",
			RequiresSuccess: true, CreatedAt: t0,
		},
		session: Session{
			SchemaRevision: 1, SessionID: "session-1", RunID: "run-1", TaskID: "task-1",
			Generation: 1, ExecutionBindingDigest: digest('5'), ContextManifestDigest: digest('6'),
			LatestCheckpointID: "checkpoint-1", State: SessionClosed, CreatedAt: t0, ClosedAt: &closed,
		},
		attempt: Attempt{
			SchemaRevision: 1, AttemptID: "attempt-1", RunID: "run-1", TaskID: "task-1",
			SessionID: "session-1", Ordinal: 1, State: AttemptResultCommitted, StateGeneration: 3,
			Lease:        AttemptLease{Generation: 1, Token: "lease-1", Worker: worker, ClaimedAt: t0, HeartbeatAt: t1, ExpiresAt: deadline},
			ResultDigest: digest('7'), CreatedAt: t0, UpdatedAt: terminal, TerminalAt: &terminal,
		},
		turn: Turn{
			SchemaRevision: 1, TurnID: "turn-1", RunID: "run-1", TaskID: "task-1",
			SessionID: "session-1", AttemptID: "attempt-1", Ordinal: 1, Kind: TurnModelCall,
			State: TurnResultCommitted, RequestDigest: digest('2'), ResultDigest: digest('7'),
			CreatedAt: t0, DispatchedAt: &t1, FinishedAt: &t2,
		},
		manifest: ModelCallManifest{
			SchemaRevision: 1, CallID: "call-1", TurnID: "turn-1", AttemptID: "attempt-1",
			IdempotencyKey: "call-key-1", Provider: "anthropic", Model: "claude-sonnet",
			PromptDigest: digest('8'), ContextManifestDigest: digest('6'), OutputContractDigest: digest('3'),
			RequestDigest: digest('2'), MaxOutputTokens: 2000, TimeoutMS: 60000, TemperatureMicros: 200000,
			CreatedAt: t0,
		},
		result:   result,
		artifact: artifact,
		publication: ArtifactPublicationIntent{
			SchemaRevision: 1, IntentID: "publication-1", Artifact: artifactRef,
			State: PublicationDisabled, ReasonCode: "ap8_not_installed", CreatedAt: t3,
		},
		checkpoint: Checkpoint{
			SchemaRevision: 1, CheckpointID: "checkpoint-1", RunID: "run-1", TaskID: "task-1",
			SessionID: "session-1", Generation: 1, ManifestDigest: digest('a'),
			MustPreserveRefs: []contracts.RecordRef{artifactRef}, CreatedByAttemptID: "attempt-1", CreatedAt: t3,
		},
		ledger: BudgetLedger{
			SchemaRevision: 1, LedgerID: "ledger-run-1", Scope: BudgetRun, ScopeID: "run-1",
			RuntimePolicy: runtimePolicy, Limit: limit,
			Consumed: BudgetUsage{ModelCalls: 1, InputTokens: 100, OutputTokens: 20, WallTimeMS: 1000, Tasks: 1, ActiveTasks: 1},
			Reserved: BudgetUsage{}, Generation: 2, State: BudgetOpen, UpdatedAt: t3,
		},
		cancellation: CancellationRequest{
			SchemaRevision: 1, RequestID: "cancel-1", Target: CancellationTask, TargetID: "task-1",
			ExpectedStateGeneration: 4, Mode: CancellationCancel, Actor: control,
			ReasonCode: "user_cancel", RequestedAt: t3,
		},
		recovery: RecoveryRecord{
			SchemaRevision: 1, RecoveryID: "recovery-1", RunID: "run-1", TaskID: "task-1",
			PreviousAttemptID: "attempt-1", OriginalCausationID: "cause-1",
			OriginalIdempotencyKey: "call-key-1", Decision: RecoveryReuseCommittedResult,
			CommittedArtifact: &artifactRef, ReasonCode: "committed_result_found", DecidedAt: terminal,
		},
		event: RuntimeEvent{
			SchemaRevision: 1, EventID: "event-1", Subject: SubjectTask, SubjectID: "task-1",
			FromState: string(TaskReady), ToState: string(TaskRunning), Generation: 2,
			Actor: control, CausationID: "cause-1", CorrelationID: "correlation-1",
			ReasonCode: "worker_claimed", OccurredAt: t1,
		},
		claim: ClaimTaskCommand{
			SchemaRevision: 1, Envelope: envelope("claim_task", 'b'), TaskID: "task-1",
			ExpectedTaskStateGeneration: 1, RequestedLeaseSeconds: 60,
		},
		heartbeat: HeartbeatAttemptCommand{
			SchemaRevision: 1, Envelope: envelope("heartbeat_attempt", 'c'), AttemptID: "attempt-1",
			ExpectedAttemptStateGeneration: 1, LeaseGeneration: 1, LeaseToken: "lease-1", RequestedExtensionSeconds: 30,
		},
		commit: CommitAttemptCommand{
			SchemaRevision: 1, Envelope: envelope("commit_attempt", 'd'), AttemptID: "attempt-1",
			ExpectedAttemptStateGeneration: 2, LeaseGeneration: 1, LeaseToken: "lease-1",
			Result: result, Artifact: artifact,
		},
		child: RequestChildTaskCommand{
			SchemaRevision: 1, Envelope: envelope("request_child_task", 'e'), ParentTaskID: "task-1",
			AttemptID: "attempt-1", ExpectedAttemptStateGeneration: 2, LeaseGeneration: 1,
			LeaseToken: "lease-1", ObjectiveDigest: digest('f'), InputRefs: []contracts.RecordRef{artifactRef},
			RequestedLimit: limit,
		},
	}
}

func revision(owner contracts.Owner, recordType, id string, generation int64, marker byte) contracts.RevisionRef {
	return contracts.RevisionRef{RecordRef: record(owner, recordType, id, marker), Generation: generation}
}

func record(owner contracts.Owner, recordType, id string, marker byte) contracts.RecordRef {
	return contracts.RecordRef{Owner: owner, RecordType: recordType, RecordID: id, SchemaRevision: 1, RecordDigest: digest(marker)}
}

func digest(marker byte) string {
	return strings.Repeat(string(marker), 64)
}
