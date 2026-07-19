// Package contracts contains AP0's shared authority-bearing contract profile.
// Unknown enum values, unknown schema revisions, malformed digests, fabricated
// Run origins, and temporally inverted records fail closed in Validate.
package contracts

import (
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"
)

const SchemaRevisionV1 uint16 = 1

var (
	ErrInvalidContract     = errors.New("invalid common contract")
	ErrIdempotencyConflict = errors.New("idempotency key reused with a different request")
	namePattern            = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Owner string

const (
	OwnerAgentControl       Owner = "agent_control"
	OwnerWorker             Owner = "worker"
	OwnerPlatformGovernance Owner = "platform_governance"
	OwnerBlob               Owner = "blob"
	OwnerResearchGateway    Owner = "research_gateway"
	OwnerGrace              Owner = "grace"
	OwnerDelegation         Owner = "delegation"
	OwnerKernel             Owner = "kernel"
)

type PrincipalKind string

const (
	PrincipalUser     PrincipalKind = "user"
	PrincipalWorkload PrincipalKind = "workload"
	PrincipalKernel   PrincipalKind = "kernel"
)

type Audience string

const (
	AudienceControlAPI       Audience = "control_api"
	AudienceWorker           Audience = "worker"
	AudienceResearchGateway  Audience = "research_gateway"
	AudienceGraceIntake      Audience = "grace_intake"
	AudienceGraceEngine      Audience = "grace_engine"
	AudienceDelegationEngine Audience = "delegation_engine"
	AudienceValidator        Audience = "validator"
	AudienceActivator        Audience = "activator"
	AudienceKernel           Audience = "kernel"
	AudienceKernelAdmin      Audience = "kernel_admin"
)

type EffectClass string

const (
	EffectNone              EffectClass = "none"
	EffectExternalRead      EffectClass = "external_read"
	EffectOperationIntent   EffectClass = "operation_intent"
	EffectExactConfirmation EffectClass = "exact_confirmation"
	EffectBrokerMutation    EffectClass = "broker_mutation"
)

type PlatformMode string

const (
	ModeDisabled       PlatformMode = "disabled"
	ModeReadOnly       PlatformMode = "read_only"
	ModeShadow         PlatformMode = "shadow"
	ModeLiveConfirmed  PlatformMode = "live_confirmed"
	ModeLiveAutonomous PlatformMode = "live_autonomous"
)

// EffectiveMode fails absent mode closed to disabled. Unknown non-empty modes
// are rejected by ValidatePlatformMode.
func EffectiveMode(mode PlatformMode) PlatformMode {
	if mode == "" {
		return ModeDisabled
	}
	return mode
}

type AuditActor struct {
	PrincipalID string        `json:"principal_id"`
	Kind        PrincipalKind `json:"kind"`
	Audience    Audience      `json:"audience"`
}

type RecordRef struct {
	Owner          Owner  `json:"owner"`
	RecordType     string `json:"record_type"`
	RecordID       string `json:"record_id"`
	SchemaRevision uint16 `json:"schema_revision"`
	RecordDigest   string `json:"record_digest"`
}

type RevisionRef struct {
	RecordRef
	Generation int64 `json:"generation"`
}

type HeadRef struct {
	Owner              Owner       `json:"owner"`
	HeadType           string      `json:"head_type"`
	HeadID             string      `json:"head_id"`
	ObservedGeneration int64       `json:"observed_generation"`
	Revision           RevisionRef `json:"revision"`
	ObservedAt         time.Time   `json:"observed_at"`
	FreshnessDeadline  time.Time   `json:"freshness_deadline"`
}

type RunOriginKind string

const (
	OriginUserRequest       RunOriginKind = "user_request"
	OriginSchedule          RunOriginKind = "schedule"
	OriginKernelEvent       RunOriginKind = "kernel_event"
	OriginExternalEvent     RunOriginKind = "external_event"
	OriginSystemMaintenance RunOriginKind = "system_maintenance"
	OriginSystemRecovery    RunOriginKind = "system_recovery"
)

type RunOrigin struct {
	SchemaRevision  uint16           `json:"schema_revision"`
	Kind            RunOriginKind    `json:"kind"`
	Source          RecordRef        `json:"source"`
	Conversation    *RecordRef       `json:"conversation,omitempty"`
	InitiatingActor AuditActor       `json:"initiating_actor"`
	OwnerPolicy     RevisionRef      `json:"owner_policy"`
	OccurredAt      time.Time        `json:"occurred_at"`
	ObservedAt      time.Time        `json:"observed_at"`
	CommittedAt     time.Time        `json:"committed_at"`
	Recovery        *RecoveryLineage `json:"recovery,omitempty"`
}

// RecoveryLineage prevents a retry after uncertain work from acquiring fresh
// causal, authority, idempotency, or external-effect identity.
type RecoveryLineage struct {
	OriginalCausationID    string    `json:"original_causation_id"`
	OriginalIdempotencyKey string    `json:"original_idempotency_key"`
	OriginalAuthority      RecordRef `json:"original_authority"`
	OriginalEffect         RecordRef `json:"original_effect"`
}

type EffectiveRunAuthority struct {
	SchemaRevision uint16       `json:"schema_revision"`
	OriginDigest   string       `json:"origin_digest"`
	Actor          AuditActor   `json:"actor"`
	OwnerPolicy    RevisionRef  `json:"owner_policy"`
	Mode           PlatformMode `json:"mode,omitempty"`
	EffectCeiling  EffectClass  `json:"effect_ceiling"`
	IssuedAt       time.Time    `json:"issued_at"`
	ValidUntil     time.Time    `json:"valid_until"`
	SourceHeads    []HeadRef    `json:"source_heads"`
}

type CommandEnvelope struct {
	SchemaRevision uint16     `json:"schema_revision"`
	CommandID      string     `json:"command_id"`
	Actor          AuditActor `json:"actor"`
	Audience       Audience   `json:"audience"`
	CommandType    string     `json:"command_type"`
	IdempotencyKey string     `json:"idempotency_key"`
	RequestDigest  string     `json:"request_digest"`
	CausationID    string     `json:"causation_id"`
	CorrelationID  string     `json:"correlation_id"`
	Deadline       time.Time  `json:"deadline"`
}

type IdempotencyIdentity struct {
	PrincipalID    string
	CommandType    string
	IdempotencyKey string
}

type ReplayDecision string

const (
	ReplayExact    ReplayDecision = "exact_retry"
	ReplayConflict ReplayDecision = "conflict"
)

type EventEnvelope struct {
	SchemaRevision uint16     `json:"schema_revision"`
	EventID        string     `json:"event_id"`
	EventType      string     `json:"event_type"`
	Owner          Owner      `json:"owner"`
	Actor          AuditActor `json:"actor"`
	Record         RecordRef  `json:"record"`
	BodyDigest     string     `json:"body_digest"`
	CausationID    string     `json:"causation_id"`
	CorrelationID  string     `json:"correlation_id"`
	OwnerSequence  int64      `json:"owner_sequence"`
	OccurredAt     time.Time  `json:"occurred_at"`
	CommittedAt    time.Time  `json:"committed_at"`
}

type FreshnessState string

const (
	FreshnessCurrent FreshnessState = "current"
	FreshnessStale   FreshnessState = "stale"
	FreshnessUnknown FreshnessState = "unknown"
)

type Freshness struct {
	State      FreshnessState `json:"state"`
	ObservedAt time.Time      `json:"observed_at"`
	FreshUntil time.Time      `json:"fresh_until"`
}

type Failure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (value AuditActor) Validate() error {
	if !validID(value.PrincipalID) || !knownPrincipalKind(value.Kind) || !knownAudience(value.Audience) {
		return ErrInvalidContract
	}
	return nil
}

func (value RecordRef) Validate() error {
	if !knownOwner(value.Owner) || !namePattern.MatchString(value.RecordType) ||
		!validID(value.RecordID) || value.SchemaRevision != SchemaRevisionV1 ||
		!validSHA256(value.RecordDigest) {
		return ErrInvalidContract
	}
	return nil
}

func (value RevisionRef) Validate() error {
	if err := value.RecordRef.Validate(); err != nil || value.Generation <= 0 {
		return ErrInvalidContract
	}
	return nil
}

func (value HeadRef) Validate() error {
	if !knownOwner(value.Owner) || !namePattern.MatchString(value.HeadType) ||
		!validID(value.HeadID) || value.ObservedGeneration <= 0 ||
		value.Owner != value.Revision.Owner || value.Revision.Generation != value.ObservedGeneration ||
		value.Revision.Validate() != nil || !validUTC(value.ObservedAt) ||
		!validUTC(value.FreshnessDeadline) || !value.ObservedAt.Before(value.FreshnessDeadline) {
		return ErrInvalidContract
	}
	return nil
}

func (value RunOrigin) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.Source.Validate() != nil ||
		value.InitiatingActor.Validate() != nil || value.OwnerPolicy.Validate() != nil ||
		value.OwnerPolicy.Owner != OwnerPlatformGovernance ||
		!orderedUTC(value.OccurredAt, value.ObservedAt, value.CommittedAt) {
		return ErrInvalidContract
	}
	type sourceIdentity struct {
		owner      Owner
		recordType string
	}
	expectedSource := map[RunOriginKind]sourceIdentity{
		OriginUserRequest:       {OwnerAgentControl, "user_request"},
		OriginSchedule:          {OwnerAgentControl, "schedule_occurrence"},
		OriginKernelEvent:       {OwnerKernel, "kernel_event"},
		OriginExternalEvent:     {OwnerAgentControl, "external_event"},
		OriginSystemMaintenance: {OwnerAgentControl, "maintenance_occurrence"},
		OriginSystemRecovery:    {OwnerAgentControl, "recovery_occurrence"},
	}[value.Kind]
	if expectedSource.recordType == "" || value.Source.Owner != expectedSource.owner ||
		value.Source.RecordType != expectedSource.recordType {
		return ErrInvalidContract
	}
	switch value.Kind {
	case OriginUserRequest:
		if value.InitiatingActor.Kind != PrincipalUser || value.InitiatingActor.Audience != AudienceControlAPI ||
			value.Conversation == nil || value.Conversation.Owner != OwnerAgentControl ||
			value.Conversation.RecordType != "conversation" || value.Conversation.Validate() != nil ||
			value.Recovery != nil {
			return ErrInvalidContract
		}
	case OriginKernelEvent:
		if value.InitiatingActor.Kind != PrincipalKernel || value.InitiatingActor.Audience != AudienceKernel ||
			value.Conversation != nil || value.Recovery != nil {
			return ErrInvalidContract
		}
	case OriginSystemRecovery:
		if value.InitiatingActor.Kind != PrincipalWorkload || value.InitiatingActor.Audience != AudienceControlAPI ||
			value.Conversation != nil || value.Recovery == nil || value.Recovery.Validate() != nil {
			return ErrInvalidContract
		}
	default:
		if value.InitiatingActor.Kind != PrincipalWorkload || value.InitiatingActor.Audience != AudienceControlAPI ||
			value.Conversation != nil || value.Recovery != nil {
			return ErrInvalidContract
		}
	}
	return nil
}

func (value RecoveryLineage) Validate() error {
	if !validID(value.OriginalCausationID) || !validID(value.OriginalIdempotencyKey) ||
		value.OriginalAuthority.Validate() != nil || value.OriginalEffect.Validate() != nil {
		return ErrInvalidContract
	}
	return nil
}

func (value EffectiveRunAuthority) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validSHA256(value.OriginDigest) ||
		value.Actor.Validate() != nil || value.OwnerPolicy.Validate() != nil ||
		value.OwnerPolicy.Owner != OwnerPlatformGovernance ||
		ValidatePlatformMode(value.Mode) != nil || ValidateEffectClass(value.EffectCeiling) != nil ||
		!validUTC(value.IssuedAt) || !validUTC(value.ValidUntil) || !value.IssuedAt.Before(value.ValidUntil) ||
		len(value.SourceHeads) == 0 || len(value.SourceHeads) > 64 {
		return ErrInvalidContract
	}
	for index := range value.SourceHeads {
		if value.SourceHeads[index].Validate() != nil || value.SourceHeads[index].ObservedAt.After(value.IssuedAt) ||
			!value.IssuedAt.Before(value.SourceHeads[index].FreshnessDeadline) ||
			value.ValidUntil.After(value.SourceHeads[index].FreshnessDeadline) {
			return ErrInvalidContract
		}
	}
	return nil
}

func (value CommandEnvelope) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.CommandID) ||
		value.Actor.Validate() != nil || !knownAudience(value.Audience) ||
		!namePattern.MatchString(value.CommandType) || !validID(value.IdempotencyKey) ||
		!validSHA256(value.RequestDigest) || !validID(value.CausationID) ||
		!validID(value.CorrelationID) || !validUTC(value.Deadline) {
		return ErrInvalidContract
	}
	return nil
}

func (value CommandEnvelope) Identity() IdempotencyIdentity {
	return IdempotencyIdentity{PrincipalID: value.Actor.PrincipalID,
		CommandType: value.CommandType, IdempotencyKey: value.IdempotencyKey}
}

func CompareReplay(original, candidate CommandEnvelope) (ReplayDecision, error) {
	if original.Validate() != nil || candidate.Validate() != nil || original.Identity() != candidate.Identity() {
		return ReplayConflict, ErrIdempotencyConflict
	}
	if original.RequestDigest != candidate.RequestDigest {
		return ReplayConflict, ErrIdempotencyConflict
	}
	return ReplayExact, nil
}

func (value EventEnvelope) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EventID) ||
		!namePattern.MatchString(value.EventType) || !knownOwner(value.Owner) ||
		value.Actor.Validate() != nil || value.Record.Validate() != nil ||
		value.Record.Owner != value.Owner || !validSHA256(value.BodyDigest) ||
		!validID(value.CausationID) || !validID(value.CorrelationID) || value.OwnerSequence <= 0 ||
		!validUTC(value.OccurredAt) || !validUTC(value.CommittedAt) || value.OccurredAt.After(value.CommittedAt) {
		return ErrInvalidContract
	}
	return nil
}

func (value Freshness) Validate() error {
	if value.State != FreshnessCurrent && value.State != FreshnessStale && value.State != FreshnessUnknown {
		return ErrInvalidContract
	}
	if !validUTC(value.ObservedAt) || !validUTC(value.FreshUntil) || !value.ObservedAt.Before(value.FreshUntil) {
		return ErrInvalidContract
	}
	return nil
}

func (value Failure) Validate() error {
	if !namePattern.MatchString(value.Code) || strings.TrimSpace(value.Message) == "" || len(value.Message) > 1000 {
		return ErrInvalidContract
	}
	return nil
}

func ValidatePlatformMode(mode PlatformMode) error {
	switch EffectiveMode(mode) {
	case ModeDisabled, ModeReadOnly, ModeShadow, ModeLiveConfirmed, ModeLiveAutonomous:
		return nil
	default:
		return ErrInvalidContract
	}
}

func ValidateEffectClass(effect EffectClass) error {
	if !knownEffect(effect) {
		return ErrInvalidContract
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

func validID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 200 {
		return false
	}
	for _, char := range value {
		if char < 0x21 || char == 0x7f {
			return false
		}
	}
	return true
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

func knownOwner(value Owner) bool {
	switch value {
	case OwnerAgentControl, OwnerWorker, OwnerPlatformGovernance, OwnerBlob,
		OwnerResearchGateway, OwnerGrace, OwnerDelegation, OwnerKernel:
		return true
	default:
		return false
	}
}

func knownPrincipalKind(value PrincipalKind) bool {
	return value == PrincipalUser || value == PrincipalWorkload || value == PrincipalKernel
}

func knownAudience(value Audience) bool {
	switch value {
	case AudienceControlAPI, AudienceWorker, AudienceResearchGateway, AudienceGraceIntake,
		AudienceGraceEngine, AudienceDelegationEngine, AudienceValidator, AudienceActivator,
		AudienceKernel, AudienceKernelAdmin:
		return true
	default:
		return false
	}
}

func knownEffect(value EffectClass) bool {
	switch value {
	case EffectNone, EffectExternalRead, EffectOperationIntent, EffectExactConfirmation, EffectBrokerMutation:
		return true
	default:
		return false
	}
}
