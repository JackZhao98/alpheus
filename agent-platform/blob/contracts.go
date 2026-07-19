// Package blob defines AP0's immutable BlobRef and local BlobStore protocol.
// BlobRef identifies verified bytes; it is never an authorization capability.
// Every read still requires a current owning reference, principal, ACL, and
// retention decision from the metadata owner.
package blob

import (
	"errors"
	"mime"
	"regexp"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/contracts"
)

const (
	SchemaRevisionV1   uint16 = 1
	AbsoluteMaxBytesV1 int64  = 1 << 30
)

var (
	ErrInvalidBlob = errors.New("invalid blob contract")
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	namePattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type AccessClass string

const (
	AccessPrivate  AccessClass = "private"
	AccessExplicit AccessClass = "explicit"
)

type BindingState string

const (
	BindingActive   BindingState = "active"
	BindingReleased BindingState = "released"
)

type BlobSubjectKind string

const (
	SubjectStage   BlobSubjectKind = "stage"
	SubjectBlob    BlobSubjectKind = "blob"
	SubjectBinding BlobSubjectKind = "binding"
	SubjectACL     BlobSubjectKind = "acl"
)

type BlobTransition string

const (
	TransitionStaged            BlobTransition = "staged"
	TransitionCommitted         BlobTransition = "committed"
	TransitionQuarantined       BlobTransition = "quarantined"
	TransitionGCClaimed         BlobTransition = "gc_claimed"
	TransitionDeleted           BlobTransition = "deleted"
	TransitionReferenceBound    BlobTransition = "reference_bound"
	TransitionReferenceReleased BlobTransition = "reference_released"
	TransitionACLGranted        BlobTransition = "acl_granted"
	TransitionACLRevoked        BlobTransition = "acl_revoked"
)

// StageGrant is issued from current database policy before bytes are read.
// MaxBytes and expiry are policy snapshots, not deployment configuration.
type StageGrant struct {
	SchemaRevision    uint16    `json:"schema_revision"`
	StageID           string    `json:"stage_id"`
	PrincipalID       string    `json:"principal_id"`
	MediaType         string    `json:"media_type"`
	MaxBytes          int64     `json:"max_bytes"`
	ExpectedDigest    string    `json:"expected_digest,omitempty"`
	ExpectedSizeBytes *int64    `json:"expected_size_bytes,omitempty"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type StagedBlob struct {
	SchemaRevision uint16     `json:"schema_revision"`
	Grant          StageGrant `json:"grant"`
	ContentDigest  string     `json:"content_digest"`
	SizeBytes      int64      `json:"size_bytes"`
	StagedAt       time.Time  `json:"staged_at"`
}

type BlobRef struct {
	SchemaRevision uint16              `json:"schema_revision"`
	BlobID         string              `json:"blob_id"`
	ContentDigest  string              `json:"content_digest"`
	MediaType      string              `json:"media_type"`
	SizeBytes      int64               `json:"size_bytes"`
	Origin         contracts.RecordRef `json:"origin"`
	CommittedAt    time.Time           `json:"committed_at"`
}

type ReferenceBinding struct {
	SchemaRevision  uint16              `json:"schema_revision"`
	BindingID       string              `json:"binding_id"`
	Blob            BlobRef             `json:"blob"`
	OwningReference contracts.RecordRef `json:"owning_reference"`
	OwnerPrincipal  string              `json:"owner_principal"`
	AccessClass     AccessClass         `json:"access_class"`
	RetentionUntil  time.Time           `json:"retention_until"`
	State           BindingState        `json:"state"`
	BoundAt         time.Time           `json:"bound_at"`
	ReleasedAt      *time.Time          `json:"released_at,omitempty"`
}

type LifecycleEvent struct {
	SchemaRevision uint16               `json:"schema_revision"`
	EventID        string               `json:"event_id"`
	SubjectKind    BlobSubjectKind      `json:"subject_kind"`
	SubjectID      string               `json:"subject_id"`
	Transition     BlobTransition       `json:"transition"`
	Generation     int64                `json:"generation"`
	Actor          contracts.AuditActor `json:"actor"`
	OccurredAt     time.Time            `json:"occurred_at"`
	ReasonCode     string               `json:"reason_code"`
}

func (value StageGrant) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validUUID(value.StageID) ||
		!validID(value.PrincipalID) || !validMediaType(value.MediaType) ||
		value.MaxBytes < 1 || value.MaxBytes > AbsoluteMaxBytesV1 ||
		value.ExpectedDigest != "" && !validDigest(value.ExpectedDigest) ||
		!orderedUTC(value.IssuedAt, value.ExpiresAt) || !value.IssuedAt.Before(value.ExpiresAt) {
		return ErrInvalidBlob
	}
	if value.ExpectedSizeBytes != nil && (*value.ExpectedSizeBytes < 1 || *value.ExpectedSizeBytes > value.MaxBytes) {
		return ErrInvalidBlob
	}
	return nil
}

func (value StagedBlob) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.Grant.Validate() != nil ||
		!validDigest(value.ContentDigest) || value.SizeBytes < 1 || value.SizeBytes > value.Grant.MaxBytes ||
		!validUTC(value.StagedAt) || value.StagedAt.Before(value.Grant.IssuedAt) || value.StagedAt.After(value.Grant.ExpiresAt) ||
		value.Grant.ExpectedDigest != "" && value.ContentDigest != value.Grant.ExpectedDigest ||
		value.Grant.ExpectedSizeBytes != nil && value.SizeBytes != *value.Grant.ExpectedSizeBytes {
		return ErrInvalidBlob
	}
	return nil
}

func (value BlobRef) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validUUID(value.BlobID) ||
		!validDigest(value.ContentDigest) || !validMediaType(value.MediaType) ||
		value.SizeBytes < 1 || value.SizeBytes > AbsoluteMaxBytesV1 || value.Origin.Validate() != nil ||
		!validUTC(value.CommittedAt) {
		return ErrInvalidBlob
	}
	return nil
}

func (value ReferenceBinding) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.BindingID) || value.Blob.Validate() != nil ||
		value.OwningReference.Validate() != nil || !validID(value.OwnerPrincipal) ||
		(value.AccessClass != AccessPrivate && value.AccessClass != AccessExplicit) ||
		!orderedUTC(value.BoundAt, value.RetentionUntil) || !value.BoundAt.Before(value.RetentionUntil) {
		return ErrInvalidBlob
	}
	switch value.State {
	case BindingActive:
		if value.ReleasedAt != nil {
			return ErrInvalidBlob
		}
	case BindingReleased:
		if value.ReleasedAt == nil || !validUTC(*value.ReleasedAt) || value.ReleasedAt.Before(value.BoundAt) {
			return ErrInvalidBlob
		}
	default:
		return ErrInvalidBlob
	}
	return nil
}

func (value LifecycleEvent) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validUUID(value.EventID) || !validID(value.SubjectID) ||
		!knownSubject(value.SubjectKind) || !knownTransition(value.Transition) || value.Generation < 1 ||
		value.Actor.Validate() != nil || !validUTC(value.OccurredAt) || !namePattern.MatchString(value.ReasonCode) {
		return ErrInvalidBlob
	}
	return nil
}

func validUUID(value string) bool { return uuidPattern.MatchString(value) }

func validDigest(value string) bool { return digestPattern.MatchString(value) }

func validID(value string) bool {
	if value == "" || len(value) > 200 || value != strings.TrimSpace(value) {
		return false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validMediaType(value string) bool {
	if value == "" || len(value) > 200 || value != strings.ToLower(value) {
		return false
	}
	mediaType, parameters, err := mime.ParseMediaType(value)
	return err == nil && mime.FormatMediaType(mediaType, parameters) == value
}

func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func orderedUTC(values ...time.Time) bool {
	for index := range values {
		if !validUTC(values[index]) || index > 0 && values[index].Before(values[index-1]) {
			return false
		}
	}
	return true
}

func knownSubject(value BlobSubjectKind) bool {
	return value == SubjectStage || value == SubjectBlob || value == SubjectBinding || value == SubjectACL
}

func knownTransition(value BlobTransition) bool {
	switch value {
	case TransitionStaged, TransitionCommitted, TransitionQuarantined, TransitionGCClaimed,
		TransitionDeleted, TransitionReferenceBound, TransitionReferenceReleased,
		TransitionACLGranted, TransitionACLRevoked:
		return true
	default:
		return false
	}
}
