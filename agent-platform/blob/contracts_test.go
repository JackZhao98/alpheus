package blob

import (
	"testing"
	"time"

	"alpheus/agentplatform/contracts"
)

func TestBlobContractsRejectAuthorityAndLifecycleDrift(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	size := int64(5)
	grant := StageGrant{
		SchemaRevision: SchemaRevisionV1,
		StageID:        "11111111-1111-4111-8111-111111111111", PrincipalID: "user-1",
		MediaType: "text/plain; charset=utf-8", MaxBytes: 1024,
		ExpectedDigest:    "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		ExpectedSizeBytes: &size, IssuedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	if err := grant.Validate(); err != nil {
		t.Fatal(err)
	}
	staged := StagedBlob{
		SchemaRevision: SchemaRevisionV1, Grant: grant, ContentDigest: grant.ExpectedDigest,
		SizeBytes: size, StagedAt: now.Add(time.Second),
	}
	if err := staged.Validate(); err != nil {
		t.Fatal(err)
	}

	origin := testRecordRef("raw_document", "raw-1")
	ref := BlobRef{
		SchemaRevision: SchemaRevisionV1, BlobID: "22222222-2222-4222-8222-222222222222",
		ContentDigest: grant.ExpectedDigest, MediaType: grant.MediaType, SizeBytes: size,
		Origin: origin, CommittedAt: now.Add(2 * time.Second),
	}
	if err := ref.Validate(); err != nil {
		t.Fatal(err)
	}
	binding := ReferenceBinding{
		SchemaRevision: SchemaRevisionV1, BindingID: "attachment-1", Blob: ref,
		OwningReference: testRecordRef("user_request", "request-1"), OwnerPrincipal: "user-1",
		AccessClass: AccessPrivate, RetentionUntil: now.Add(24 * time.Hour), State: BindingActive,
		BoundAt: now.Add(3 * time.Second),
	}
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}

	badGrant := grant
	badGrant.MediaType = "Text/Plain"
	if badGrant.Validate() == nil {
		t.Fatal("noncanonical media type accepted")
	}
	badBinding := binding
	badBinding.State = BindingReleased
	if badBinding.Validate() == nil {
		t.Fatal("released binding without released_at accepted")
	}
	badRef := ref
	badRef.ContentDigest = "ABC"
	if badRef.Validate() == nil {
		t.Fatal("malformed digest accepted")
	}
}

func TestLifecycleEventValidation(t *testing.T) {
	event := LifecycleEvent{
		SchemaRevision: SchemaRevisionV1,
		EventID:        "33333333-3333-4333-8333-333333333333",
		SubjectKind:    SubjectBlob, SubjectID: "22222222-2222-4222-8222-222222222222",
		Transition: TransitionCommitted, Generation: 1,
		Actor:      contracts.AuditActor{PrincipalID: "blob-store", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI},
		OccurredAt: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC), ReasonCode: "stage_committed",
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.Transition = "made_up"
	if event.Validate() == nil {
		t.Fatal("unknown transition accepted")
	}
}

func testRecordRef(recordType, id string) contracts.RecordRef {
	return contracts.RecordRef{
		Owner: contracts.OwnerAgentControl, RecordType: recordType, RecordID: id,
		SchemaRevision: contracts.SchemaRevisionV1,
		RecordDigest:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}
