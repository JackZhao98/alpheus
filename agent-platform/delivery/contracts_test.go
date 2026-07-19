package delivery

import (
	"errors"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/contracts"
)

const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var now = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func event() contracts.EventEnvelope {
	return contracts.EventEnvelope{
		SchemaRevision: contracts.SchemaRevisionV1, EventID: "event-1", EventType: "artifact_published",
		Owner: contracts.OwnerAgentControl,
		Actor: contracts.AuditActor{PrincipalID: "control-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI},
		Record: contracts.RecordRef{
			Owner: contracts.OwnerAgentControl, RecordType: "user_request", RecordID: "request-1",
			SchemaRevision: contracts.SchemaRevisionV1, RecordDigest: digest,
		},
		BodyDigest: digest, CausationID: "task-1", CorrelationID: "run-1", OwnerSequence: 1,
		OccurredAt: now, CommittedAt: now.Add(time.Second),
	}
}

func TestOutboxStatesFailClosed(t *testing.T) {
	value := OutboxRecord{
		SchemaRevision: SchemaRevisionV1, Event: event(), Destination: "grace-intake",
		State: OutboxAvailable, AvailableAt: now.Add(time.Second),
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("available: %v", err)
	}
	value.AttemptCount = 1
	if err := value.Validate(); err == nil {
		t.Fatal("available record retained prior attempts")
	}
	value.AttemptCount = 0
	lease := Lease{
		DispatcherID: "dispatcher-1", LeaseToken: "123e4567-e89b-42d3-a456-426614174000",
		ClaimedAt: now.Add(time.Second), ExpiresAt: now.Add(time.Minute),
	}
	value.State, value.AttemptCount, value.Lease = OutboxLeased, 1, &lease
	if err := value.Validate(); err != nil {
		t.Fatalf("leased: %v", err)
	}

	deliveredAt := now.Add(2 * time.Minute)
	value.State, value.Lease, value.DeliveredAt = OutboxDelivered, nil, &deliveredAt
	if err := value.Validate(); err != nil {
		t.Fatalf("delivered: %v", err)
	}
	value.Lease = &lease
	if err := value.Validate(); err == nil {
		t.Fatal("delivered record retained a lease")
	}
}

func TestInboxChangedDigestConflicts(t *testing.T) {
	original := InboxReceipt{
		SchemaRevision: SchemaRevisionV1, ConsumerID: "grace-intake", EventID: "event-1",
		EventDigest: digest, SourceOwner: contracts.OwnerAgentControl, OwnerSequence: 1,
		EffectDigest: digest, AppliedAt: now,
	}
	retry := original
	if err := CompareInbox(original, retry); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	retry.EventDigest = strings.Repeat("b", 64)
	if err := CompareInbox(original, retry); !errors.Is(err, ErrInboxConflict) {
		t.Fatalf("changed digest: %v", err)
	}
}

func TestQuarantineRequiresExplicitReplayGeneration(t *testing.T) {
	value := QuarantineRecord{
		SchemaRevision: SchemaRevisionV1, ConsumerID: "grace-intake", EventID: "event-1",
		EventDigest: digest, SourceOwner: contracts.OwnerAgentControl, OwnerSequence: 1, ReasonCode: "unsupported_revision",
		AttemptCount: 3, State: QuarantineActive, FirstFailedAt: now, LastFailedAt: now.Add(time.Minute),
	}
	if err := value.Validate(); err != nil {
		t.Fatalf("active quarantine: %v", err)
	}
	value.State = QuarantineReplayRequested
	if err := value.Validate(); err == nil {
		t.Fatal("replay without generation accepted")
	}
	value.ReplayGeneration = 1
	if err := value.Validate(); err != nil {
		t.Fatalf("replay request: %v", err)
	}
}
