package inputcontract

import (
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
)

var inputTestNow = time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC)

func TestConversationAndUserRequestReferences(t *testing.T) {
	conversation := testConversation()
	conversationRef, err := conversation.Ref()
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(conversationRef)
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	requestRef, err := request.Ref()
	if err != nil {
		t.Fatal(err)
	}
	if requestRef.Owner != contracts.OwnerAgentControl || requestRef.RecordType != "user_request" || requestRef.RecordID != request.RequestID {
		t.Fatalf("unexpected user request ref: %+v", requestRef)
	}

	// Exact raw evidence changes the identity.  An interpreter cannot silently
	// replace the user text while pretending it is the original request.
	changed := request
	changed.RawInput.ContentDigest = digest('b')
	changedRef, err := changed.Ref()
	if err != nil {
		t.Fatal(err)
	}
	if changedRef.RecordDigest == requestRef.RecordDigest {
		t.Fatal("changed raw input retained request digest")
	}
}

func TestInputContractsFailClosed(t *testing.T) {
	conversation := testConversation()
	conversationRef, err := conversation.Ref()
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*UserRequest){
		"unknown request kind": func(value *UserRequest) { value.Kind = "open_position" },
		"worker subject": func(value *UserRequest) {
			value.Subject.Kind, value.Subject.Audience = contracts.PrincipalWorkload, contracts.AudienceWorker
		},
		"wrong conversation ref":       func(value *UserRequest) { value.Conversation.RecordType = "user_request" },
		"future raw blob":              func(value *UserRequest) { value.RawInput.CommittedAt = value.CreatedAt.Add(time.Nanosecond) },
		"raw duplicated as attachment": func(value *UserRequest) { value.Attachments = []blob.BlobRef{value.RawInput} },
		"duplicate referenced object": func(value *UserRequest) {
			value.ReferencedObject = []contracts.RecordRef{testObjectRef(), testObjectRef()}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := testRequest(conversationRef)
			mutate(&candidate)
			if candidate.Validate() == nil {
				t.Fatal("invalid user request accepted")
			}
		})
	}
	conversation.Subject.Audience = contracts.AudienceWorker
	if conversation.Validate() == nil {
		t.Fatal("non-user conversation accepted")
	}
}

func testConversation() Conversation {
	return Conversation{
		SchemaRevision: SchemaRevisionV1, ConversationID: "conversation-1",
		Subject:   contracts.AuditActor{PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceControlAPI},
		CreatedAt: inputTestNow,
	}
}

func testRequest(conversation contracts.RecordRef) UserRequest {
	return UserRequest{
		SchemaRevision: SchemaRevisionV1, RequestID: "request-1", Conversation: conversation,
		Subject: contracts.AuditActor{PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceControlAPI},
		Kind:    RequestNew, RawInput: testBlob("11111111-1111-4111-8111-111111111111", digest('a')),
		Attachments:      []blob.BlobRef{testBlob("22222222-2222-4222-8222-222222222222", digest('c'))},
		ReferencedObject: []contracts.RecordRef{testObjectRef()}, CreatedAt: inputTestNow,
	}
}

func testBlob(id, contentDigest string) blob.BlobRef {
	return blob.BlobRef{
		SchemaRevision: blob.SchemaRevisionV1, BlobID: id, ContentDigest: contentDigest,
		MediaType: "text/plain; charset=utf-8", SizeBytes: 5,
		Origin:      contracts.RecordRef{Owner: contracts.OwnerAgentControl, RecordType: "input_raw", RecordID: "raw-1", SchemaRevision: contracts.SchemaRevisionV1, RecordDigest: digest('d')},
		CommittedAt: inputTestNow,
	}
}

func testObjectRef() contracts.RecordRef {
	return contracts.RecordRef{Owner: contracts.OwnerKernel, RecordType: "account_snapshot", RecordID: "account-1", SchemaRevision: contracts.SchemaRevisionV1, RecordDigest: digest('e')}
}

func digest(char byte) string { return strings.Repeat(string(char), 64) }
