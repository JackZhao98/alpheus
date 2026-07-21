package inputgateway

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/inputcontract"
)

type testBlobCommitter struct {
	seen RawBlobRequest
	err  error
}

func (fake *testBlobCommitter) CommitRawInput(_ context.Context, request RawBlobRequest) (blob.BlobRef, error) {
	fake.seen = request
	if fake.err != nil {
		return blob.BlobRef{}, fake.err
	}
	return blob.BlobRef{SchemaRevision: 1, BlobID: "11111111-1111-4111-8111-111111111111",
		ContentDigest: strings.Repeat("a", 64), MediaType: "text/plain; charset=utf-8", SizeBytes: int64(len(request.Text)),
		Origin:      contracts.RecordRef{Owner: contracts.OwnerAgentControl, RecordType: "input_raw", RecordID: request.InputID, SchemaRevision: 1, RecordDigest: strings.Repeat("b", 64)},
		CommittedAt: time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC)}, nil
}

type testSubmitter struct {
	command inputcontract.SubmitUserRequestCommand
	err     error
}

func (fake *testSubmitter) SubmitUserRequest(_ context.Context, command inputcontract.SubmitUserRequestCommand) error {
	fake.command = command
	return fake.err
}

func validAdmissionRequest() Request {
	return Request{Actor: contracts.AuditActor{PrincipalID: "control-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI},
		Subject:        contracts.AuditActor{PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceControlAPI},
		ConversationID: "conversation-1", ConversationCreatedAt: time.Date(2026, 7, 21, 15, 59, 0, 0, time.UTC),
		RequestID: "request-1", Kind: inputcontract.RequestNew, Text: []byte("test"),
		IdempotencyKey: "idem-1", CausationID: "cause-1", CorrelationID: "correlation-1", Deadline: time.Date(2026, 7, 21, 16, 5, 0, 0, time.UTC)}
}

func TestAdmitCommitsBlobThenSubmitsBoundCommand(t *testing.T) {
	blobs, submitted := &testBlobCommitter{}, &testSubmitter{}
	gateway, err := New(blobs, submitted)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time { return time.Date(2026, 7, 21, 16, 1, 0, 0, time.UTC) }
	admission, err := gateway.Admit(context.Background(), validAdmissionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if blobs.seen.SubjectPrincipalID != "owner-1" || blobs.seen.MediaType != "text/plain; charset=utf-8" || submitted.command.Validate() != nil {
		t.Fatalf("bad admission: blob=%+v command=%+v", blobs.seen, submitted.command)
	}
	if admission.Command.Request.RawInput != admission.Blob || admission.Command.Envelope.RequestDigest == "" {
		t.Fatalf("admission=%+v", admission)
	}
}

func TestAdmitReturnsStableCodedFailures(t *testing.T) {
	blobs, submitted := &testBlobCommitter{err: errors.New("disk")}, &testSubmitter{}
	gateway, err := New(blobs, submitted)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time { return time.Date(2026, 7, 21, 16, 1, 0, 0, time.UTC) }
	_, err = gateway.Admit(context.Background(), validAdmissionRequest())
	var coded *Error
	if !errors.As(err, &coded) || coded.Code != CodeBlobCommit {
		t.Fatalf("err=%v", err)
	}
	blobs.err = nil
	submitted.err = errors.New("database")
	_, err = gateway.Admit(context.Background(), validAdmissionRequest())
	if !errors.As(err, &coded) || coded.Code != CodeAdmission {
		t.Fatalf("err=%v", err)
	}
}

func TestAdmitTransportRetryReplaysExactImmutableRequest(t *testing.T) {
	blobs, submitted := &testBlobCommitter{}, &testSubmitter{}
	gateway, err := New(blobs, submitted)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 21, 16, 1, 0, 0, time.UTC)
	gateway.now = func() time.Time { return now }
	first, err := gateway.Admit(context.Background(), validAdmissionRequest())
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	second, err := gateway.Admit(context.Background(), validAdmissionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Command.Request, second.Command.Request) || first.Command.Conversation != second.Command.Conversation ||
		first.Command.Envelope.RequestDigest != second.Command.Envelope.RequestDigest {
		t.Fatalf("retry changed immutable request: first=%+v second=%+v", first.Command, second.Command)
	}
}
