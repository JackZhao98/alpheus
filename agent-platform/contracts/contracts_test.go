package contracts

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const testDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var testTime = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func ref(owner Owner, recordType, id string) RecordRef {
	return RecordRef{
		Owner: owner, RecordType: recordType, RecordID: id,
		SchemaRevision: SchemaRevisionV1, RecordDigest: testDigest,
	}
}

func revision(owner Owner, recordType, id string) RevisionRef {
	return RevisionRef{RecordRef: ref(owner, recordType, id), Generation: 1}
}

func validUserOrigin() RunOrigin {
	conversation := ref(OwnerAgentControl, "conversation", "conversation-1")
	return RunOrigin{
		SchemaRevision: SchemaRevisionV1,
		Kind:           OriginUserRequest,
		Source:         ref(OwnerAgentControl, "user_request", "request-1"),
		Conversation:   &conversation,
		InitiatingActor: AuditActor{
			PrincipalID: "user-1", Kind: PrincipalUser, Audience: AudienceControlAPI,
		},
		OwnerPolicy: revision(OwnerPlatformGovernance, "owner_policy_revision", "policy-1"),
		OccurredAt:  testTime,
		ObservedAt:  testTime.Add(time.Second),
		CommittedAt: testTime.Add(2 * time.Second),
	}
}

func TestRunOriginRejectsFabricatedAuthority(t *testing.T) {
	valid := validUserOrigin()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid user origin: %v", err)
	}

	tests := map[string]func(*RunOrigin){
		"wrong source owner": func(value *RunOrigin) { value.Source.Owner = OwnerWorker },
		"wrong audience":     func(value *RunOrigin) { value.InitiatingActor.Audience = AudienceActivator },
		"wrong policy owner": func(value *RunOrigin) { value.OwnerPolicy.Owner = OwnerAgentControl },
		"missing conversation": func(value *RunOrigin) {
			value.Conversation = nil
		},
		"recovery identity on user": func(value *RunOrigin) {
			value.Recovery = &RecoveryLineage{
				OriginalCausationID: "cause-1", OriginalIdempotencyKey: "idempotency-1",
				OriginalAuthority: ref(OwnerPlatformGovernance, "effective_run_authority", "authority-1"),
				OriginalEffect:    ref(OwnerWorker, "tool_effect", "effect-1"),
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validUserOrigin()
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestNonUserOriginsCannotClaimConversation(t *testing.T) {
	value := validUserOrigin()
	value.Kind = OriginSchedule
	value.Source = ref(OwnerAgentControl, "schedule_occurrence", "occurrence-1")
	value.Conversation = nil
	value.InitiatingActor = AuditActor{
		PrincipalID: "scheduler-1", Kind: PrincipalWorkload, Audience: AudienceControlAPI,
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("valid schedule origin: %v", err)
	}

	conversation := ref(OwnerAgentControl, "conversation", "fabricated-1")
	value.Conversation = &conversation
	if err := value.Validate(); err == nil {
		t.Fatal("schedule origin claimed a conversation")
	}
}

func TestKernelAndRecoveryOrigins(t *testing.T) {
	kernel := validUserOrigin()
	kernel.Kind = OriginKernelEvent
	kernel.Source = ref(OwnerKernel, "kernel_event", "event-1")
	kernel.Conversation = nil
	kernel.InitiatingActor = AuditActor{
		PrincipalID: "kernel-1", Kind: PrincipalKernel, Audience: AudienceKernel,
	}
	if err := kernel.Validate(); err != nil {
		t.Fatalf("valid kernel origin: %v", err)
	}

	recovery := validUserOrigin()
	recovery.Kind = OriginSystemRecovery
	recovery.Source = ref(OwnerAgentControl, "recovery_occurrence", "recovery-1")
	recovery.Conversation = nil
	recovery.InitiatingActor = AuditActor{
		PrincipalID: "recovery-worker-1", Kind: PrincipalWorkload, Audience: AudienceControlAPI,
	}
	recovery.Recovery = &RecoveryLineage{
		OriginalCausationID: "cause-1", OriginalIdempotencyKey: "idempotency-1",
		OriginalAuthority: ref(OwnerPlatformGovernance, "effective_run_authority", "authority-1"),
		OriginalEffect:    ref(OwnerWorker, "tool_effect", "effect-1"),
	}
	if err := recovery.Validate(); err != nil {
		t.Fatalf("valid recovery origin: %v", err)
	}
	recovery.Recovery.OriginalEffect.RecordDigest = ""
	if err := recovery.Validate(); err == nil {
		t.Fatal("recovery origin minted without original effect identity")
	}
}

func validCommand() CommandEnvelope {
	return CommandEnvelope{
		SchemaRevision: SchemaRevisionV1,
		CommandID:      "command-1",
		Actor: AuditActor{
			PrincipalID: "worker-1", Kind: PrincipalWorkload, Audience: AudienceWorker,
		},
		Audience: AudienceResearchGateway, CommandType: "fetch_evidence",
		IdempotencyKey: "task-1:fetch-1", RequestDigest: testDigest,
		CausationID: "task-1", CorrelationID: "run-1", Deadline: testTime.Add(time.Minute),
	}
}

func TestChangedBodyReplayConflicts(t *testing.T) {
	original := validCommand()
	retry := original
	retry.CommandID = "command-2"
	decision, err := CompareReplay(original, retry)
	if err != nil || decision != ReplayExact {
		t.Fatalf("exact retry decision=%s err=%v", decision, err)
	}

	retry.RequestDigest = strings.Repeat("b", 64)
	decision, err = CompareReplay(original, retry)
	if decision != ReplayConflict || !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed replay decision=%s err=%v", decision, err)
	}
}

func TestOpaqueIDsRejectSurroundingWhitespace(t *testing.T) {
	value := validCommand()
	value.CommandID = " command-1"
	if err := value.Validate(); err == nil {
		t.Fatal("leading whitespace was silently normalized")
	}
	value = validCommand()
	value.IdempotencyKey = "task-1 "
	if err := value.Validate(); err == nil {
		t.Fatal("trailing whitespace was silently normalized")
	}
}

func TestEffectiveAuthorityFailsClosed(t *testing.T) {
	headRevision := revision(OwnerPlatformGovernance, "platform_mode_revision", "mode-1")
	value := EffectiveRunAuthority{
		SchemaRevision: SchemaRevisionV1,
		OriginDigest:   testDigest,
		Actor: AuditActor{
			PrincipalID: "worker-1", Kind: PrincipalWorkload, Audience: AudienceWorker,
		},
		OwnerPolicy:   revision(OwnerPlatformGovernance, "owner_policy_revision", "policy-1"),
		EffectCeiling: EffectExternalRead,
		IssuedAt:      testTime,
		ValidUntil:    testTime.Add(time.Minute),
		SourceHeads: []HeadRef{{
			Owner: OwnerPlatformGovernance, HeadType: "platform_mode_head", HeadID: "mode-head-1",
			ObservedGeneration: 1, Revision: headRevision, ObservedAt: testTime.Add(-time.Second),
			FreshnessDeadline: testTime.Add(2 * time.Minute),
		}},
	}
	if EffectiveMode(value.Mode) != ModeDisabled {
		t.Fatal("absent mode did not fail closed to disabled")
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("valid authority: %v", err)
	}

	malformed := value
	malformed.Mode = "live-ish"
	if err := malformed.Validate(); err == nil {
		t.Fatal("unknown mode accepted")
	}
	malformed = value
	malformed.EffectCeiling = "new_effect"
	if err := malformed.Validate(); err == nil {
		t.Fatal("unknown effect class accepted")
	}
	malformed = value
	malformed.SourceHeads = nil
	if err := malformed.Validate(); err == nil {
		t.Fatal("authority without source heads accepted")
	}
	malformed = value
	malformed.ValidUntil = value.SourceHeads[0].FreshnessDeadline.Add(time.Second)
	if err := malformed.Validate(); err == nil {
		t.Fatal("authority outlived source freshness")
	}
}

func TestEventOwnerMustMatchRecordOwner(t *testing.T) {
	value := EventEnvelope{
		SchemaRevision: SchemaRevisionV1, EventID: "event-1", EventType: "record_published",
		Owner:  OwnerAgentControl,
		Actor:  AuditActor{PrincipalID: "worker-1", Kind: PrincipalWorkload, Audience: AudienceWorker},
		Record: ref(OwnerAgentControl, "artifact", "artifact-1"), BodyDigest: testDigest,
		CausationID: "task-1", CorrelationID: "run-1", OwnerSequence: 1,
		OccurredAt: testTime, CommittedAt: testTime.Add(time.Second),
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("valid event: %v", err)
	}
	value.Record.Owner = OwnerWorker
	if err := value.Validate(); err == nil {
		t.Fatal("cross-owner event accepted")
	}
}
