// Package delivery defines AP0's durable at-least-once delivery records. It
// models owner outbox, consumer inbox, and poison quarantine state without
// starting a dispatcher or changing Runtime behavior.
package delivery

import (
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/contracts"
)

const SchemaRevisionV1 uint16 = 1

var (
	ErrInvalidDelivery = errors.New("invalid delivery contract")
	ErrInboxConflict   = errors.New("inbox identity reused with a different event digest")
	uuidPattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type OutboxState string

const (
	OutboxAvailable   OutboxState = "available"
	OutboxLeased      OutboxState = "leased"
	OutboxDelivered   OutboxState = "delivered"
	OutboxQuarantined OutboxState = "quarantined"
)

type QuarantineState string

const (
	QuarantineActive          QuarantineState = "active"
	QuarantineReplayRequested QuarantineState = "replay_requested"
	QuarantineResolved        QuarantineState = "resolved"
)

type Lease struct {
	DispatcherID string    `json:"dispatcher_id"`
	LeaseToken   string    `json:"lease_token"`
	ClaimedAt    time.Time `json:"claimed_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type OutboxRecord struct {
	SchemaRevision uint16                  `json:"schema_revision"`
	Event          contracts.EventEnvelope `json:"event"`
	Destination    string                  `json:"destination"`
	State          OutboxState             `json:"state"`
	AvailableAt    time.Time               `json:"available_at"`
	AttemptCount   uint32                  `json:"attempt_count"`
	Lease          *Lease                  `json:"lease,omitempty"`
	DeliveredAt    *time.Time              `json:"delivered_at,omitempty"`
	QuarantinedAt  *time.Time              `json:"quarantined_at,omitempty"`
}

type InboxReceipt struct {
	SchemaRevision uint16          `json:"schema_revision"`
	ConsumerID     string          `json:"consumer_id"`
	EventID        string          `json:"event_id"`
	EventDigest    string          `json:"event_digest"`
	SourceOwner    contracts.Owner `json:"source_owner"`
	OwnerSequence  int64           `json:"owner_sequence"`
	EffectDigest   string          `json:"effect_digest"`
	AppliedAt      time.Time       `json:"applied_at"`
}

type QuarantineRecord struct {
	SchemaRevision   uint16          `json:"schema_revision"`
	ConsumerID       string          `json:"consumer_id"`
	EventID          string          `json:"event_id"`
	EventDigest      string          `json:"event_digest"`
	SourceOwner      contracts.Owner `json:"source_owner"`
	OwnerSequence    int64           `json:"owner_sequence"`
	ReasonCode       string          `json:"reason_code"`
	AttemptCount     uint32          `json:"attempt_count"`
	State            QuarantineState `json:"state"`
	FirstFailedAt    time.Time       `json:"first_failed_at"`
	LastFailedAt     time.Time       `json:"last_failed_at"`
	ReplayGeneration uint32          `json:"replay_generation"`
}

func (value Lease) Validate() error {
	if !validID(value.DispatcherID) || !uuidPattern.MatchString(value.LeaseToken) ||
		!orderedUTC(value.ClaimedAt, value.ExpiresAt) || !value.ClaimedAt.Before(value.ExpiresAt) {
		return ErrInvalidDelivery
	}
	return nil
}

func (value OutboxRecord) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.Event.Validate() != nil ||
		!validID(value.Destination) || !validUTC(value.AvailableAt) ||
		value.AvailableAt.Before(value.Event.CommittedAt) {
		return ErrInvalidDelivery
	}
	switch value.State {
	case OutboxAvailable:
		if value.AttemptCount != 0 || value.Lease != nil || value.DeliveredAt != nil || value.QuarantinedAt != nil {
			return ErrInvalidDelivery
		}
	case OutboxLeased:
		if value.AttemptCount == 0 || value.Lease == nil || value.Lease.Validate() != nil ||
			value.Lease.ClaimedAt.Before(value.AvailableAt) || value.DeliveredAt != nil || value.QuarantinedAt != nil {
			return ErrInvalidDelivery
		}
	case OutboxDelivered:
		if value.AttemptCount == 0 || value.Lease != nil ||
			!validOptionalAfter(value.DeliveredAt, value.AvailableAt) || value.QuarantinedAt != nil {
			return ErrInvalidDelivery
		}
	case OutboxQuarantined:
		if value.AttemptCount == 0 || value.Lease != nil || value.DeliveredAt != nil ||
			!validOptionalAfter(value.QuarantinedAt, value.AvailableAt) {
			return ErrInvalidDelivery
		}
	default:
		return ErrInvalidDelivery
	}
	return nil
}

func (value InboxReceipt) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ConsumerID) || !validID(value.EventID) ||
		!validDigest(value.EventDigest) || contracts.ValidateOwner(value.SourceOwner) != nil || value.OwnerSequence <= 0 ||
		!validDigest(value.EffectDigest) || !validUTC(value.AppliedAt) {
		return ErrInvalidDelivery
	}
	return nil
}

func (value QuarantineRecord) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ConsumerID) || !validID(value.EventID) ||
		!validDigest(value.EventDigest) || contracts.ValidateOwner(value.SourceOwner) != nil || value.OwnerSequence <= 0 ||
		!validName(value.ReasonCode) ||
		value.AttemptCount == 0 || !orderedUTC(value.FirstFailedAt, value.LastFailedAt) {
		return ErrInvalidDelivery
	}
	switch value.State {
	case QuarantineActive:
	case QuarantineReplayRequested, QuarantineResolved:
		if value.ReplayGeneration == 0 {
			return ErrInvalidDelivery
		}
	default:
		return ErrInvalidDelivery
	}
	return nil
}

func CompareInbox(original, candidate InboxReceipt) error {
	if original.Validate() != nil || candidate.Validate() != nil ||
		original.ConsumerID != candidate.ConsumerID || original.EventID != candidate.EventID ||
		original.EventDigest != candidate.EventDigest || original.SourceOwner != candidate.SourceOwner ||
		original.OwnerSequence != candidate.OwnerSequence || original.EffectDigest != candidate.EffectDigest {
		return ErrInboxConflict
	}
	return nil
}

func validOptionalAfter(value *time.Time, minimum time.Time) bool {
	return value != nil && validUTC(*value) && !value.Before(minimum)
}

func validUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func orderedUTC(values ...time.Time) bool {
	for index := range values {
		if !validUTC(values[index]) || index > 0 && values[index].Before(values[index-1]) {
			return false
		}
	}
	return true
}

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

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validName(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if char != '_' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}
