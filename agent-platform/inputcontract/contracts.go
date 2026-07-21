// Package inputcontract defines the immutable facts accepted by Cortex Input
// Gateway before an LLM interprets them.  These records are deliberately not
// tasks, instructions, confirmations, or trading authority: raw user input is
// durable evidence, and every later IntentDraft/PolicyResolution must refer
// back to it instead of rewriting it.
package inputcontract

import (
	"errors"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
)

const (
	SchemaRevisionV1               uint16 = 1
	AbsoluteMaxAttachmentsV1              = 64
	AbsoluteMaxReferencedObjectsV1        = 64
)

var ErrInvalidInput = errors.New("invalid input contract")

// RequestKind records what the user supplied, not what the system decides to
// do with it.  Natural-language approval/rejection remains merely an intent;
// it cannot become a broker or confirmation authority through this contract.
type RequestKind string

const (
	RequestNew               RequestKind = "new_request"
	RequestContinuation      RequestKind = "continuation"
	RequestAdditionalContext RequestKind = "additional_context"
	RequestClarification     RequestKind = "clarification_answer"
	RequestCorrection        RequestKind = "correction"
	RequestPause             RequestKind = "pause"
	RequestResume            RequestKind = "resume"
	RequestCancel            RequestKind = "cancel"
	RequestApprovalIntent    RequestKind = "approval_intent"
	RequestRejectionIntent   RequestKind = "rejection_intent"
)

// Conversation is the stable user-owned thread. It contains no mutable model
// summary; context and interpretation remain separately attributable records.
type Conversation struct {
	SchemaRevision uint16               `json:"schema_revision"`
	ConversationID string               `json:"conversation_id"`
	Subject        contracts.AuditActor `json:"subject"`
	CreatedAt      time.Time            `json:"created_at"`
}

// UserRequest is one immutable raw user input. RawInput and attachments are
// BlobRefs, so their exact bytes and checksums are addressable without copying
// them into an LLM transcript or rewriting them in an IntentDraft.
type UserRequest struct {
	SchemaRevision   uint16                `json:"schema_revision"`
	RequestID        string                `json:"request_id"`
	Conversation     contracts.RecordRef   `json:"conversation"`
	Subject          contracts.AuditActor  `json:"subject"`
	Kind             RequestKind           `json:"kind"`
	RawInput         blob.BlobRef          `json:"raw_input"`
	Attachments      []blob.BlobRef        `json:"attachments,omitempty"`
	ReferencedObject []contracts.RecordRef `json:"referenced_objects,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
}

func (value Conversation) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ConversationID) ||
		value.Subject.Validate() != nil || value.Subject.Kind != contracts.PrincipalUser ||
		value.Subject.Audience != contracts.AudienceControlAPI || !validUTC(value.CreatedAt) {
		return ErrInvalidInput
	}
	return nil
}

func (value Conversation) Ref() (contracts.RecordRef, error) {
	if value.Validate() != nil {
		return contracts.RecordRef{}, ErrInvalidInput
	}
	digest, err := canonical.Digest("agent-platform.contract.conversation.v1", value)
	if err != nil {
		return contracts.RecordRef{}, err
	}
	return contracts.RecordRef{
		Owner: contracts.OwnerAgentControl, RecordType: "conversation",
		RecordID: value.ConversationID, SchemaRevision: SchemaRevisionV1,
		RecordDigest: digest,
	}, nil
}

func (value UserRequest) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.RequestID) ||
		!validConversationRef(value.Conversation) || value.Subject.Validate() != nil ||
		value.Subject.Kind != contracts.PrincipalUser || value.Subject.Audience != contracts.AudienceControlAPI ||
		!knownRequestKind(value.Kind) || value.RawInput.Validate() != nil || !validUTC(value.CreatedAt) ||
		len(value.Attachments) > AbsoluteMaxAttachmentsV1 || len(value.ReferencedObject) > AbsoluteMaxReferencedObjectsV1 {
		return ErrInvalidInput
	}
	if value.RawInput.CommittedAt.After(value.CreatedAt) {
		return ErrInvalidInput
	}
	seenBlobs := map[string]struct{}{value.RawInput.BlobID: {}}
	for _, attachment := range value.Attachments {
		if attachment.Validate() != nil || attachment.CommittedAt.After(value.CreatedAt) {
			return ErrInvalidInput
		}
		if _, exists := seenBlobs[attachment.BlobID]; exists {
			return ErrInvalidInput
		}
		seenBlobs[attachment.BlobID] = struct{}{}
	}
	seenRefs := make(map[string]struct{}, len(value.ReferencedObject))
	for _, reference := range value.ReferencedObject {
		if reference.Validate() != nil {
			return ErrInvalidInput
		}
		key := string(reference.Owner) + "\x00" + reference.RecordType + "\x00" + reference.RecordID + "\x00" + reference.RecordDigest
		if _, exists := seenRefs[key]; exists {
			return ErrInvalidInput
		}
		seenRefs[key] = struct{}{}
	}
	return nil
}

func (value UserRequest) Ref() (contracts.RecordRef, error) {
	if value.Validate() != nil {
		return contracts.RecordRef{}, ErrInvalidInput
	}
	digest, err := canonical.Digest("agent-platform.contract.user_request.v1", value)
	if err != nil {
		return contracts.RecordRef{}, err
	}
	return contracts.RecordRef{
		Owner: contracts.OwnerAgentControl, RecordType: "user_request",
		RecordID: value.RequestID, SchemaRevision: SchemaRevisionV1,
		RecordDigest: digest,
	}, nil
}

func validConversationRef(value contracts.RecordRef) bool {
	return value.Validate() == nil && value.Owner == contracts.OwnerAgentControl && value.RecordType == "conversation"
}

func knownRequestKind(value RequestKind) bool {
	switch value {
	case RequestNew, RequestContinuation, RequestAdditionalContext, RequestClarification,
		RequestCorrection, RequestPause, RequestResume, RequestCancel, RequestApprovalIntent, RequestRejectionIntent:
		return true
	default:
		return false
	}
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

func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }
