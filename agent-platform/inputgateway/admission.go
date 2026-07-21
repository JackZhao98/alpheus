// Package inputgateway assembles the one safe path from authenticated browser
// text to Cortex's immutable input command. It owns no model, Run, task, or
// Kernel authority. Blob materialization happens first because a UserRequest
// may only name verified bytes through a BlobRef.
package inputgateway

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/inputcontract"
)

const (
	CodeInvalidRequest = "input_gateway_invalid_request"
	CodeBlobCommit     = "input_gateway_blob_commit_failed"
	CodeAdmission      = "input_gateway_admission_failed"
)

// Error always carries a stable code suitable for the local API and an
// operator-facing message. Causes remain available to server logs only.
type Error struct {
	Code    string
	Message string
	Cause   error
}

func (err *Error) Error() string { return err.Code + ": " + err.Message }
func (err *Error) Unwrap() error { return err.Cause }

// RawBlobCommitter is the dedicated Blob service boundary. Implementations
// must return a committed, verified BlobRef; they must not merely hash input
// bytes or fabricate a reference.
type RawBlobCommitter interface {
	CommitRawInput(context.Context, RawBlobRequest) (blob.BlobRef, error)
}

// CommandSubmitter is the scoped Input Gateway database command boundary.
// Its implementation is responsible for the Control API database identity.
type CommandSubmitter interface {
	SubmitUserRequest(context.Context, inputcontract.SubmitUserRequestCommand) error
}

type RawBlobRequest struct {
	SubjectPrincipalID string
	InputID            string
	Text               []byte
	MediaType          string
}

// Request contains only authenticated input facts. In particular, Actor is
// service identity supplied by configuration, while Subject is the verified
// browser user; callers cannot merge the two identities.
type Request struct {
	Actor          contracts.AuditActor
	Subject        contracts.AuditActor
	ConversationID string
	// ConversationCreatedAt is a caller-retained immutable fact. A later
	// request in the same Conversation must replay its original value.
	ConversationCreatedAt time.Time
	RequestID             string
	Kind                  inputcontract.RequestKind
	Text                  []byte
	IdempotencyKey        string
	CausationID           string
	CorrelationID         string
	Deadline              time.Time
}

type Admission struct {
	Blob    blob.BlobRef
	Command inputcontract.SubmitUserRequestCommand
}

type Gateway struct {
	blobs RawBlobCommitter
	store CommandSubmitter
	now   func() time.Time
	newID func() (string, error)
}

func New(blobs RawBlobCommitter, store CommandSubmitter) (*Gateway, error) {
	if blobs == nil || store == nil {
		return nil, &Error{Code: CodeInvalidRequest, Message: "blob committer and command submitter are required"}
	}
	return &Gateway{blobs: blobs, store: store, now: func() time.Time { return time.Now().UTC() }, newID: newUUID}, nil
}

// Admit commits raw bytes, then submits exactly one immutable UserRequest.
// If database admission fails after Blob commit, it returns a coded error;
// the committed blob is intentionally retained for normal lifecycle cleanup
// rather than being deleted speculatively.
func (gateway *Gateway) Admit(ctx context.Context, request Request) (Admission, error) {
	if gateway == nil || gateway.blobs == nil || gateway.store == nil || !validRequest(request) {
		return Admission{}, &Error{Code: CodeInvalidRequest, Message: "request facts are invalid"}
	}
	now := gateway.now().UTC()
	if now.IsZero() || now.Location() != time.UTC || !now.Before(request.Deadline) {
		return Admission{}, &Error{Code: CodeInvalidRequest, Message: "request deadline is expired"}
	}
	// The immutable Request identity is also the raw-input origin identity.
	// This is deliberate: a transport retry must address the same Blob stage
	// and receive the same BlobRef, otherwise the request digest would change
	// underneath an otherwise identical idempotency key.
	rawID := request.RequestID
	raw, err := gateway.blobs.CommitRawInput(ctx, RawBlobRequest{
		SubjectPrincipalID: request.Subject.PrincipalID, InputID: rawID,
		Text: request.Text, MediaType: "text/plain; charset=utf-8",
	})
	if err != nil {
		return Admission{}, &Error{Code: CodeBlobCommit, Message: "raw input was not committed", Cause: err}
	}
	acceptedAt := gateway.now().UTC()
	if raw.Validate() != nil || acceptedAt.IsZero() || acceptedAt.Location() != time.UTC || raw.CommittedAt.After(acceptedAt) ||
		request.ConversationCreatedAt.After(raw.CommittedAt) {
		return Admission{}, &Error{Code: CodeBlobCommit, Message: "blob service returned an invalid raw input reference"}
	}
	conversation := inputcontract.Conversation{
		SchemaRevision: inputcontract.SchemaRevisionV1, ConversationID: request.ConversationID,
		Subject: request.Subject, CreatedAt: request.ConversationCreatedAt,
	}
	conversationRef, err := conversation.Ref()
	if err != nil {
		return Admission{}, &Error{Code: CodeInvalidRequest, Message: "conversation facts are invalid", Cause: err}
	}
	userRequest := inputcontract.UserRequest{
		SchemaRevision: inputcontract.SchemaRevisionV1, RequestID: request.RequestID,
		Conversation: conversationRef, Subject: request.Subject, Kind: request.Kind,
		RawInput: raw, CreatedAt: raw.CommittedAt,
	}
	requestRef, err := userRequest.Ref()
	if err != nil {
		return Admission{}, &Error{Code: CodeInvalidRequest, Message: "user request facts are invalid", Cause: err}
	}
	commandID, err := gateway.newID()
	if err != nil {
		return Admission{}, &Error{Code: CodeInvalidRequest, Message: "could not allocate command identity", Cause: err}
	}
	command := inputcontract.SubmitUserRequestCommand{
		SchemaRevision: inputcontract.SchemaRevisionV1,
		Envelope: contracts.CommandEnvelope{
			SchemaRevision: contracts.SchemaRevisionV1, CommandID: commandID,
			Actor: request.Actor, Audience: contracts.AudienceControlAPI,
			CommandType: "submit_user_request", IdempotencyKey: request.IdempotencyKey,
			RequestDigest: requestRef.RecordDigest, CausationID: request.CausationID,
			CorrelationID: request.CorrelationID, Deadline: request.Deadline,
		},
		Conversation: conversation, Request: userRequest,
	}
	if err := command.Validate(); err != nil {
		return Admission{}, &Error{Code: CodeInvalidRequest, Message: "assembled admission command is invalid", Cause: err}
	}
	if err := gateway.store.SubmitUserRequest(ctx, command); err != nil {
		return Admission{}, &Error{Code: CodeAdmission, Message: "immutable user request was not admitted", Cause: err}
	}
	return Admission{Blob: raw, Command: command}, nil
}

func validRequest(request Request) bool {
	return request.Actor.Validate() == nil && request.Actor.Kind == contracts.PrincipalWorkload &&
		request.Actor.Audience == contracts.AudienceControlAPI && request.Subject.Validate() == nil &&
		request.Subject.Kind == contracts.PrincipalUser && request.Subject.Audience == contracts.AudienceControlAPI &&
		request.ConversationID != "" && validUTC(request.ConversationCreatedAt) && request.RequestID != "" && request.Kind != "" &&
		len(request.Text) > 0 && request.IdempotencyKey != "" && request.CausationID != "" &&
		request.CorrelationID != "" && !request.Deadline.IsZero() && request.Deadline.Location() == time.UTC
}

func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("random UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
