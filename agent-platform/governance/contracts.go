// Package governance defines AP0's immutable platform-mode, effect-class,
// kill-switch, owner-policy, and activation contracts. It also provides a
// deterministic resolver for cached governance snapshots. The resolver grants
// no authority: malformed, missing, stale, or incompatible state fails closed.
package governance

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
)

const SchemaRevisionV1 uint16 = 1

var ErrInvalidGovernance = errors.New("invalid governance contract")

type GateState string

const (
	GateEnabled GateState = "enabled"
	GateHalted  GateState = "halted"
)

type SwitchID string

const (
	SwitchAgentOperationEmission      SwitchID = "agent_operation_emission"
	SwitchAgentReleaseActivation      SwitchID = "agent_release_activation"
	SwitchCapabilityExternalExecution SwitchID = "capability_external_execution"
	SwitchStrategyActivation          SwitchID = "strategy_activation"
	SwitchGracePublication            SwitchID = "grace_publication"
	SwitchDelegationActivation        SwitchID = "delegation_activation"
	SwitchShadowIntegration           SwitchID = "shadow_integration"
	SwitchExactConfirmationLive       SwitchID = "exact_confirmation_live"
	SwitchAutonomousLive              SwitchID = "autonomous_live"
	SwitchProductEquity               SwitchID = "product_equity"
	SwitchProductOption               SwitchID = "product_option"
	SwitchProductCrypto               SwitchID = "product_crypto"
)

type SubjectKind string

const (
	SubjectPlatformMode SubjectKind = "platform_mode"
	SubjectEffectClass  SubjectKind = "effect_class"
	SubjectKillSwitch   SubjectKind = "kill_switch"
)

type Transition string

const (
	TransitionRaise  Transition = "raise"
	TransitionLower  Transition = "lower"
	TransitionResume Transition = "resume"
	TransitionHalt   Transition = "halt"
)

type EffectRoute string

const (
	RouteNone                 EffectRoute = "none"
	RouteExactConfirmation    EffectRoute = "exact_confirmation"
	RouteAutonomousDelegation EffectRoute = "autonomous_delegation"
)

type PlatformModeRevision struct {
	SchemaRevision uint16                 `json:"schema_revision"`
	RevisionID     string                 `json:"revision_id"`
	Generation     int64                  `json:"generation"`
	Mode           contracts.PlatformMode `json:"mode"`
	Author         contracts.AuditActor   `json:"author"`
	ReasonCode     string                 `json:"reason_code"`
	CreatedAt      time.Time              `json:"created_at"`
}

type EffectClassRevision struct {
	SchemaRevision uint16                `json:"schema_revision"`
	RevisionID     string                `json:"revision_id"`
	Generation     int64                 `json:"generation"`
	EffectClass    contracts.EffectClass `json:"effect_class"`
	State          GateState             `json:"state"`
	Author         contracts.AuditActor  `json:"author"`
	ReasonCode     string                `json:"reason_code"`
	CreatedAt      time.Time             `json:"created_at"`
}

type KillSwitchRevision struct {
	SchemaRevision uint16               `json:"schema_revision"`
	RevisionID     string               `json:"revision_id"`
	Generation     int64                `json:"generation"`
	SwitchID       SwitchID             `json:"switch_id"`
	State          GateState            `json:"state"`
	Author         contracts.AuditActor `json:"author"`
	ReasonCode     string               `json:"reason_code"`
	CreatedAt      time.Time            `json:"created_at"`
}

// OwnerPolicyRevision identifies one exact non-money runtime origin. It does
// not declare Skills, Tools, scopes, or later delegation semantics; AP1 may use
// it only to bind authenticated RunOrigin identity with effect ceiling none.
type OwnerPolicyRevision struct {
	SchemaRevision        uint16                  `json:"schema_revision"`
	PolicyID              string                  `json:"policy_id"`
	RevisionID            string                  `json:"revision_id"`
	Generation            int64                   `json:"generation"`
	OriginKind            contracts.RunOriginKind `json:"origin_kind"`
	SourceOwner           contracts.Owner         `json:"source_owner"`
	SourceRecordType      string                  `json:"source_record_type"`
	InitiatingKind        contracts.PrincipalKind `json:"initiating_kind"`
	InitiatingAudience    contracts.Audience      `json:"initiating_audience"`
	InitiatingPrincipalID *string                 `json:"initiating_principal_id,omitempty"`
	EffectCeiling         contracts.EffectClass   `json:"effect_ceiling"`
	Author                contracts.AuditActor    `json:"author"`
	ReasonCode            string                  `json:"reason_code"`
	CreatedAt             time.Time               `json:"created_at"`
}

type PlatformModeHead struct {
	SchemaRevision    uint16                 `json:"schema_revision"`
	HeadID            string                 `json:"head_id"`
	Generation        int64                  `json:"generation"`
	Revision          contracts.RevisionRef  `json:"revision"`
	Mode              contracts.PlatformMode `json:"mode"`
	ActivationReceipt *contracts.RecordRef   `json:"activation_receipt,omitempty"`
	ActivatedBy       contracts.AuditActor   `json:"activated_by"`
	ActivatedAt       time.Time              `json:"activated_at"`
}

type EffectClassHead struct {
	SchemaRevision    uint16                `json:"schema_revision"`
	HeadID            string                `json:"head_id"`
	Generation        int64                 `json:"generation"`
	Revision          contracts.RevisionRef `json:"revision"`
	EffectClass       contracts.EffectClass `json:"effect_class"`
	State             GateState             `json:"state"`
	ActivationReceipt *contracts.RecordRef  `json:"activation_receipt,omitempty"`
	ActivatedBy       contracts.AuditActor  `json:"activated_by"`
	ActivatedAt       time.Time             `json:"activated_at"`
}

type KillSwitchHead struct {
	SchemaRevision    uint16                `json:"schema_revision"`
	HeadID            string                `json:"head_id"`
	Generation        int64                 `json:"generation"`
	Revision          contracts.RevisionRef `json:"revision"`
	SwitchID          SwitchID              `json:"switch_id"`
	State             GateState             `json:"state"`
	ActivationReceipt *contracts.RecordRef  `json:"activation_receipt,omitempty"`
	ActivatedBy       contracts.AuditActor  `json:"activated_by"`
	ActivatedAt       time.Time             `json:"activated_at"`
}

// OwnerPolicyHead is the compare-and-swap selected revision for one policy.
// The head grants no effect beyond the exact immutable revision it references.
type OwnerPolicyHead struct {
	SchemaRevision uint16                `json:"schema_revision"`
	HeadID         string                `json:"head_id"`
	Generation     int64                 `json:"generation"`
	Revision       contracts.RevisionRef `json:"revision"`
	ActivatedBy    contracts.AuditActor  `json:"activated_by"`
	ActivatedAt    time.Time             `json:"activated_at"`
}

type ActivationReceipt struct {
	SchemaRevision          uint16                 `json:"schema_revision"`
	ReceiptID               string                 `json:"receipt_id"`
	TargetKind              SubjectKind            `json:"target_kind"`
	TargetID                string                 `json:"target_id"`
	TargetRevision          contracts.RevisionRef  `json:"target_revision"`
	ExpectedHeadGeneration  int64                  `json:"expected_head_generation"`
	Transition              Transition             `json:"transition"`
	Actor                   contracts.AuditActor   `json:"actor"`
	DeploymentModeCeiling   contracts.PlatformMode `json:"deployment_mode_ceiling"`
	DeploymentEffectCeiling contracts.EffectClass  `json:"deployment_effect_ceiling"`
	RequestDigest           string                 `json:"request_digest"`
	ReasonCode              string                 `json:"reason_code"`
	IssuedAt                time.Time              `json:"issued_at"`
	ExpiresAt               time.Time              `json:"expires_at"`
}

type GovernanceEvent struct {
	SchemaRevision    uint16                 `json:"schema_revision"`
	EventID           string                 `json:"event_id"`
	SubjectKind       SubjectKind            `json:"subject_kind"`
	SubjectID         string                 `json:"subject_id"`
	Generation        int64                  `json:"generation"`
	Transition        Transition             `json:"transition"`
	PreviousRevision  *contracts.RevisionRef `json:"previous_revision,omitempty"`
	CurrentRevision   contracts.RevisionRef  `json:"current_revision"`
	ActivationReceipt *contracts.RecordRef   `json:"activation_receipt,omitempty"`
	Actor             contracts.AuditActor   `json:"actor"`
	OccurredAt        time.Time              `json:"occurred_at"`
	ReasonCode        string                 `json:"reason_code"`
}

// OwnerPolicyEvent is the append-only audit edge for an OwnerPolicyHead CAS.
type OwnerPolicyEvent struct {
	SchemaRevision   uint16                 `json:"schema_revision"`
	EventID          string                 `json:"event_id"`
	PolicyID         string                 `json:"policy_id"`
	Generation       int64                  `json:"generation"`
	PreviousRevision *contracts.RevisionRef `json:"previous_revision,omitempty"`
	CurrentRevision  contracts.RevisionRef  `json:"current_revision"`
	Actor            contracts.AuditActor   `json:"actor"`
	ReasonCode       string                 `json:"reason_code"`
	OccurredAt       time.Time              `json:"occurred_at"`
}

func (value PlatformModeRevision) Validate() error {
	if !baseRevisionValid(value.SchemaRevision, value.RevisionID, value.Generation, value.Author, value.ReasonCode, value.CreatedAt) ||
		contracts.ValidatePlatformMode(value.Mode) != nil {
		return ErrInvalidGovernance
	}
	return nil
}

func (value PlatformModeRevision) Ref() (contracts.RevisionRef, error) {
	return revisionRef("platform_mode_revision", value.RevisionID, value.Generation, value)
}

func (value EffectClassRevision) Validate() error {
	if !baseRevisionValid(value.SchemaRevision, value.RevisionID, value.Generation, value.Author, value.ReasonCode, value.CreatedAt) ||
		contracts.ValidateEffectClass(value.EffectClass) != nil || value.EffectClass == contracts.EffectNone || !knownGateState(value.State) {
		return ErrInvalidGovernance
	}
	return nil
}

func (value EffectClassRevision) Ref() (contracts.RevisionRef, error) {
	return revisionRef("effect_class_revision", value.RevisionID, value.Generation, value)
}

func (value KillSwitchRevision) Validate() error {
	if !baseRevisionValid(value.SchemaRevision, value.RevisionID, value.Generation, value.Author, value.ReasonCode, value.CreatedAt) ||
		!knownSwitch(value.SwitchID) || !knownGateState(value.State) {
		return ErrInvalidGovernance
	}
	return nil
}

func (value KillSwitchRevision) Ref() (contracts.RevisionRef, error) {
	return revisionRef("kill_switch_revision", value.RevisionID, value.Generation, value)
}

func (value OwnerPolicyRevision) Validate() error {
	if !baseRevisionValid(value.SchemaRevision, value.RevisionID, value.Generation, value.Author, value.ReasonCode, value.CreatedAt) ||
		!validID(value.PolicyID) || contracts.ValidateOwner(value.SourceOwner) != nil ||
		!validName(value.SourceRecordType) || contracts.ValidateEffectClass(value.EffectCeiling) != nil ||
		value.EffectCeiling != contracts.EffectNone || !ownerPolicyOriginValid(value) {
		return ErrInvalidGovernance
	}
	if value.InitiatingPrincipalID != nil && !validID(*value.InitiatingPrincipalID) {
		return ErrInvalidGovernance
	}
	return nil
}

func (value OwnerPolicyRevision) Ref() (contracts.RevisionRef, error) {
	return revisionRef("owner_policy_revision", value.RevisionID, value.Generation, value)
}

// MatchesRunOrigin verifies both the exact revision binding and every
// authority-bearing origin field. Missing or malformed values never match.
func (value OwnerPolicyRevision) MatchesRunOrigin(origin contracts.RunOrigin) bool {
	if value.Validate() != nil || origin.Validate() != nil {
		return false
	}
	ref, err := value.Ref()
	if err != nil || origin.OwnerPolicy != ref {
		return false
	}
	// Recovery reuses the original policy and causal authority; it never gets a
	// separately registrable recovery policy or a fresh origin match.
	if origin.Kind == contracts.OriginSystemRecovery {
		return true
	}
	if origin.Kind != value.OriginKind ||
		origin.Source.Owner != value.SourceOwner || origin.Source.RecordType != value.SourceRecordType ||
		origin.InitiatingActor.Kind != value.InitiatingKind ||
		origin.InitiatingActor.Audience != value.InitiatingAudience {
		return false
	}
	return value.InitiatingPrincipalID == nil || origin.InitiatingActor.PrincipalID == *value.InitiatingPrincipalID
}

func (value PlatformModeHead) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.HeadID != "global" || value.Generation <= 0 ||
		contracts.ValidatePlatformMode(value.Mode) != nil || !validRevision(value.Revision, "platform_mode_revision", value.Generation) ||
		!validOptionalReceipt(value.ActivationReceipt) || !validActivator(value.ActivatedBy) || !validUTC(value.ActivatedAt) {
		return ErrInvalidGovernance
	}
	if value.Mode != contracts.ModeDisabled && value.ActivationReceipt == nil {
		return ErrInvalidGovernance
	}
	return nil
}

func (value EffectClassHead) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.HeadID != string(value.EffectClass) || value.Generation <= 0 ||
		contracts.ValidateEffectClass(value.EffectClass) != nil || value.EffectClass == contracts.EffectNone || !knownGateState(value.State) ||
		!validRevision(value.Revision, "effect_class_revision", value.Generation) || !validOptionalReceipt(value.ActivationReceipt) ||
		!validActivator(value.ActivatedBy) || !validUTC(value.ActivatedAt) {
		return ErrInvalidGovernance
	}
	if value.State == GateEnabled && value.ActivationReceipt == nil {
		return ErrInvalidGovernance
	}
	return nil
}

func (value KillSwitchHead) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.HeadID != string(value.SwitchID) || value.Generation <= 0 ||
		!knownSwitch(value.SwitchID) || !knownGateState(value.State) ||
		!validRevision(value.Revision, "kill_switch_revision", value.Generation) || !validOptionalReceipt(value.ActivationReceipt) ||
		!validActivator(value.ActivatedBy) || !validUTC(value.ActivatedAt) {
		return ErrInvalidGovernance
	}
	if value.State == GateEnabled && value.ActivationReceipt == nil {
		return ErrInvalidGovernance
	}
	return nil
}

func (value OwnerPolicyHead) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.HeadID) || value.Generation <= 0 ||
		!validRevision(value.Revision, "owner_policy_revision", value.Generation) ||
		!validActivator(value.ActivatedBy) || !validUTC(value.ActivatedAt) {
		return ErrInvalidGovernance
	}
	return nil
}

func (value ActivationReceipt) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ReceiptID) || !knownSubject(value.TargetKind) ||
		!validID(value.TargetID) || value.ExpectedHeadGeneration < 0 || value.TargetRevision.Generation != value.ExpectedHeadGeneration+1 ||
		!knownTransition(value.Transition) || value.Actor.Validate() != nil || value.Actor.Kind != contracts.PrincipalUser ||
		value.Actor.Audience != contracts.AudienceActivator || contracts.ValidatePlatformMode(value.DeploymentModeCeiling) != nil ||
		contracts.ValidateEffectClass(value.DeploymentEffectCeiling) != nil || !validDigest(value.RequestDigest) ||
		!validName(value.ReasonCode) || !validUTC(value.IssuedAt) || !validUTC(value.ExpiresAt) || !value.IssuedAt.Before(value.ExpiresAt) ||
		value.ExpiresAt.Sub(value.IssuedAt) > time.Hour ||
		!receiptTargetValid(value) {
		return ErrInvalidGovernance
	}
	return nil
}

func (value ActivationReceipt) Ref() (contracts.RecordRef, error) {
	if err := value.Validate(); err != nil {
		return contracts.RecordRef{}, err
	}
	digest, err := canonical.Digest("agent-platform.contract.activation_receipt.v1", value)
	if err != nil {
		return contracts.RecordRef{}, err
	}
	return contracts.RecordRef{Owner: contracts.OwnerPlatformGovernance, RecordType: "activation_receipt", RecordID: value.ReceiptID,
		SchemaRevision: SchemaRevisionV1, RecordDigest: digest}, nil
}

func (value GovernanceEvent) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EventID) || !knownSubject(value.SubjectKind) ||
		!validID(value.SubjectID) || value.Generation <= 0 || !knownTransition(value.Transition) ||
		!validRevision(value.CurrentRevision, recordTypeFor(value.SubjectKind), value.Generation) ||
		value.PreviousRevision != nil && (!validRevision(*value.PreviousRevision, recordTypeFor(value.SubjectKind), value.Generation-1) || value.Generation <= 1) ||
		value.Generation > 1 && value.PreviousRevision == nil || !validOptionalReceipt(value.ActivationReceipt) ||
		(value.Transition == TransitionRaise || value.Transition == TransitionResume) && value.ActivationReceipt == nil ||
		!validActivator(value.Actor) || !validUTC(value.OccurredAt) || !validName(value.ReasonCode) {
		return ErrInvalidGovernance
	}
	return nil
}

func (value OwnerPolicyEvent) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EventID) || !validID(value.PolicyID) ||
		value.Generation <= 0 || !validRevision(value.CurrentRevision, "owner_policy_revision", value.Generation) ||
		value.PreviousRevision != nil && (!validRevision(*value.PreviousRevision, "owner_policy_revision", value.Generation-1) || value.Generation <= 1) ||
		value.Generation > 1 && value.PreviousRevision == nil || !validActivator(value.Actor) ||
		!validName(value.ReasonCode) || !validUTC(value.OccurredAt) {
		return ErrInvalidGovernance
	}
	return nil
}

type OwnerPolicyBinding struct {
	Revision OwnerPolicyRevision
	Head     OwnerPolicyHead
}

func (value OwnerPolicyBinding) Validate() error {
	if value.Revision.Validate() != nil || value.Head.Validate() != nil || value.Head.HeadID != value.Revision.PolicyID {
		return ErrInvalidGovernance
	}
	ref, err := value.Revision.Ref()
	if err != nil || ref != value.Head.Revision {
		return ErrInvalidGovernance
	}
	return nil
}

type PlatformModeBinding struct {
	Revision PlatformModeRevision
	Head     PlatformModeHead
}

type EffectClassBinding struct {
	Revision EffectClassRevision
	Head     EffectClassHead
}

type KillSwitchBinding struct {
	Revision KillSwitchRevision
	Head     KillSwitchHead
}

type Snapshot struct {
	DeploymentModeCeiling   contracts.PlatformMode
	DeploymentEffectCeiling contracts.EffectClass
	ObservedAt              time.Time
	FreshUntil              time.Time
	Mode                    *PlatformModeBinding
	Effects                 map[contracts.EffectClass]EffectClassBinding
	Switches                map[SwitchID]KillSwitchBinding
}

type ResolveRequest struct {
	Effect           contracts.EffectClass
	Route            EffectRoute
	RequiredSwitches []SwitchID
}

type Decision struct {
	Allowed         bool
	ReasonCode      string
	EffectiveMode   contracts.PlatformMode
	Effect          contracts.EffectClass
	SourceRevisions []contracts.RevisionRef
}

// Resolve deterministically intersects the deployment ceiling, current mode,
// exact effect head, and every mandatory/applicable kill switch. The caller
// may require additional product or feature switches; unknown switches fail.
func Resolve(snapshot Snapshot, request ResolveRequest, now time.Time) Decision {
	deny := func(reason string, mode contracts.PlatformMode, refs []contracts.RevisionRef) Decision {
		sort.Slice(refs, func(i, j int) bool {
			if refs[i].RecordType == refs[j].RecordType {
				return refs[i].RecordID < refs[j].RecordID
			}
			return refs[i].RecordType < refs[j].RecordType
		})
		return Decision{ReasonCode: reason, EffectiveMode: mode, Effect: request.Effect, SourceRevisions: refs}
	}
	if contracts.ValidateEffectClass(request.Effect) != nil || !knownRoute(request.Route) || !validUTC(now) ||
		!validUTC(snapshot.ObservedAt) || !validUTC(snapshot.FreshUntil) || !snapshot.ObservedAt.Before(snapshot.FreshUntil) ||
		now.Before(snapshot.ObservedAt) || !now.Before(snapshot.FreshUntil) {
		return deny("invalid_or_stale_snapshot", contracts.ModeDisabled, nil)
	}
	if contracts.ValidatePlatformMode(snapshot.DeploymentModeCeiling) != nil ||
		contracts.ValidateEffectClass(snapshot.DeploymentEffectCeiling) != nil {
		return deny("invalid_deployment_ceiling", contracts.ModeDisabled, nil)
	}
	if request.Effect == contracts.EffectNone {
		if request.Route != RouteNone || len(request.RequiredSwitches) != 0 {
			return deny("invalid_no_effect_request", contracts.ModeDisabled, nil)
		}
		return Decision{Allowed: true, ReasonCode: "no_effect", EffectiveMode: contracts.ModeDisabled, Effect: request.Effect}
	}
	if snapshot.Mode == nil || !validModeBinding(*snapshot.Mode) {
		return deny("missing_or_invalid_mode_head", contracts.ModeDisabled, nil)
	}
	modeRef, _ := snapshot.Mode.Revision.Ref()
	refs := []contracts.RevisionRef{modeRef}
	effectiveMode := lesserMode(snapshot.Mode.Head.Mode, contracts.EffectiveMode(snapshot.DeploymentModeCeiling))
	if effectRank(request.Effect) > effectRank(snapshot.DeploymentEffectCeiling) {
		return deny("effect_exceeds_deployment_ceiling", effectiveMode, refs)
	}
	if !modeAllows(effectiveMode, request.Effect, request.Route) {
		return deny("mode_or_route_denied", effectiveMode, refs)
	}
	effect, ok := snapshot.Effects[request.Effect]
	if !ok || !validEffectBinding(effect) {
		return deny("missing_or_invalid_effect_head", effectiveMode, refs)
	}
	effectRef, _ := effect.Revision.Ref()
	refs = append(refs, effectRef)
	if effect.Head.State != GateEnabled {
		return deny("effect_halted", effectiveMode, refs)
	}
	required, ok := requiredSwitches(request)
	if !ok {
		return deny("invalid_required_switches", effectiveMode, refs)
	}
	for _, switchID := range required {
		binding, exists := snapshot.Switches[switchID]
		if !exists || !validSwitchBinding(binding) {
			return deny("missing_or_invalid_kill_switch", effectiveMode, refs)
		}
		ref, _ := binding.Revision.Ref()
		refs = append(refs, ref)
		if binding.Head.State != GateEnabled {
			return deny("kill_switch_halted", effectiveMode, refs)
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].RecordType == refs[j].RecordType {
			return refs[i].RecordID < refs[j].RecordID
		}
		return refs[i].RecordType < refs[j].RecordType
	})
	return Decision{Allowed: true, ReasonCode: "allowed", EffectiveMode: effectiveMode, Effect: request.Effect, SourceRevisions: refs}
}

func validModeBinding(value PlatformModeBinding) bool {
	if value.Revision.Validate() != nil || value.Head.Validate() != nil || value.Head.Mode != value.Revision.Mode {
		return false
	}
	ref, err := value.Revision.Ref()
	return err == nil && ref == value.Head.Revision
}

func validEffectBinding(value EffectClassBinding) bool {
	if value.Revision.Validate() != nil || value.Head.Validate() != nil || value.Head.EffectClass != value.Revision.EffectClass ||
		value.Head.State != value.Revision.State {
		return false
	}
	ref, err := value.Revision.Ref()
	return err == nil && ref == value.Head.Revision
}

func validSwitchBinding(value KillSwitchBinding) bool {
	if value.Revision.Validate() != nil || value.Head.Validate() != nil || value.Head.SwitchID != value.Revision.SwitchID ||
		value.Head.State != value.Revision.State {
		return false
	}
	ref, err := value.Revision.Ref()
	return err == nil && ref == value.Head.Revision
}

func requiredSwitches(request ResolveRequest) ([]SwitchID, bool) {
	values := append([]SwitchID(nil), request.RequiredSwitches...)
	switch request.Effect {
	case contracts.EffectExternalRead:
		values = append(values, SwitchCapabilityExternalExecution)
	case contracts.EffectOperationIntent:
		values = append(values, SwitchAgentOperationEmission)
	case contracts.EffectExactConfirmation:
		values = append(values, SwitchExactConfirmationLive)
	case contracts.EffectBrokerMutation:
		values = append(values, SwitchAgentOperationEmission)
		if request.Route == RouteExactConfirmation {
			values = append(values, SwitchExactConfirmationLive)
		} else if request.Route == RouteAutonomousDelegation {
			values = append(values, SwitchAutonomousLive, SwitchDelegationActivation)
		}
	}
	seen := make(map[SwitchID]struct{}, len(values))
	for _, value := range values {
		if !knownSwitch(value) {
			return nil, false
		}
		seen[value] = struct{}{}
	}
	values = values[:0]
	for value := range seen {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values, true
}

func modeAllows(mode contracts.PlatformMode, effect contracts.EffectClass, route EffectRoute) bool {
	switch effect {
	case contracts.EffectExternalRead:
		return modeRank(mode) >= modeRank(contracts.ModeReadOnly) && route == RouteNone
	case contracts.EffectOperationIntent:
		return modeRank(mode) >= modeRank(contracts.ModeShadow) && route == RouteNone
	case contracts.EffectExactConfirmation:
		return modeRank(mode) >= modeRank(contracts.ModeLiveConfirmed) && route == RouteExactConfirmation
	case contracts.EffectBrokerMutation:
		return route == RouteExactConfirmation && modeRank(mode) >= modeRank(contracts.ModeLiveConfirmed) ||
			route == RouteAutonomousDelegation && mode == contracts.ModeLiveAutonomous
	default:
		return false
	}
}

func lesserMode(left, right contracts.PlatformMode) contracts.PlatformMode {
	if modeRank(left) <= modeRank(right) {
		return left
	}
	return right
}

func modeRank(value contracts.PlatformMode) int {
	switch value {
	case contracts.ModeDisabled:
		return 0
	case contracts.ModeReadOnly:
		return 1
	case contracts.ModeShadow:
		return 2
	case contracts.ModeLiveConfirmed:
		return 3
	case contracts.ModeLiveAutonomous:
		return 4
	default:
		return -1
	}
}

func effectRank(value contracts.EffectClass) int {
	switch value {
	case contracts.EffectNone:
		return 0
	case contracts.EffectExternalRead:
		return 1
	case contracts.EffectOperationIntent:
		return 2
	case contracts.EffectExactConfirmation:
		return 3
	case contracts.EffectBrokerMutation:
		return 4
	default:
		return 100
	}
}

func revisionRef(recordType, revisionID string, generation int64, value any) (contracts.RevisionRef, error) {
	validatable, ok := value.(interface{ Validate() error })
	if !ok || validatable.Validate() != nil {
		return contracts.RevisionRef{}, ErrInvalidGovernance
	}
	digest, err := canonical.Digest("agent-platform.contract."+recordType+".v1", value)
	if err != nil {
		return contracts.RevisionRef{}, err
	}
	return contracts.RevisionRef{RecordRef: contracts.RecordRef{Owner: contracts.OwnerPlatformGovernance,
		RecordType: recordType, RecordID: revisionID, SchemaRevision: SchemaRevisionV1, RecordDigest: digest}, Generation: generation}, nil
}

func baseRevisionValid(schema uint16, id string, generation int64, actor contracts.AuditActor, reason string, created time.Time) bool {
	return schema == SchemaRevisionV1 && validID(id) && generation > 0 && actor.Validate() == nil &&
		validActivator(actor) && validName(reason) && validUTC(created)
}

func validRevision(value contracts.RevisionRef, recordType string, generation int64) bool {
	return value.Validate() == nil && value.Owner == contracts.OwnerPlatformGovernance && value.RecordType == recordType && value.Generation == generation
}

func validReceiptRef(value contracts.RecordRef) bool {
	return value.Validate() == nil && value.Owner == contracts.OwnerPlatformGovernance && value.RecordType == "activation_receipt"
}

func validOptionalReceipt(value *contracts.RecordRef) bool {
	return value == nil || validReceiptRef(*value)
}

func validActivator(value contracts.AuditActor) bool {
	return value.Validate() == nil && value.Audience == contracts.AudienceActivator &&
		(value.Kind == contracts.PrincipalWorkload || value.Kind == contracts.PrincipalUser)
}

func receiptTargetValid(value ActivationReceipt) bool {
	if value.TargetRevision.Owner != contracts.OwnerPlatformGovernance || value.TargetRevision.Validate() != nil ||
		value.TargetRevision.RecordType != recordTypeFor(value.TargetKind) {
		return false
	}
	switch value.TargetKind {
	case SubjectPlatformMode:
		return value.TargetID == "global"
	case SubjectEffectClass:
		effect := contracts.EffectClass(value.TargetID)
		return effect != contracts.EffectNone && contracts.ValidateEffectClass(effect) == nil
	case SubjectKillSwitch:
		return knownSwitch(SwitchID(value.TargetID))
	default:
		return false
	}
}

func recordTypeFor(kind SubjectKind) string {
	switch kind {
	case SubjectPlatformMode:
		return "platform_mode_revision"
	case SubjectEffectClass:
		return "effect_class_revision"
	case SubjectKillSwitch:
		return "kill_switch_revision"
	default:
		return ""
	}
}

func ownerPolicyOriginValid(value OwnerPolicyRevision) bool {
	type identity struct {
		owner      contracts.Owner
		recordType string
		kind       contracts.PrincipalKind
		audience   contracts.Audience
	}
	expected := map[contracts.RunOriginKind]identity{
		contracts.OriginUserRequest: {
			owner: contracts.OwnerAgentControl, recordType: "user_request",
			kind: contracts.PrincipalUser, audience: contracts.AudienceControlAPI,
		},
		contracts.OriginSchedule: {
			owner: contracts.OwnerAgentControl, recordType: "schedule_occurrence",
			kind: contracts.PrincipalWorkload, audience: contracts.AudienceControlAPI,
		},
		contracts.OriginKernelEvent: {
			owner: contracts.OwnerKernel, recordType: "kernel_event",
			kind: contracts.PrincipalKernel, audience: contracts.AudienceKernel,
		},
		contracts.OriginExternalEvent: {
			owner: contracts.OwnerAgentControl, recordType: "external_event",
			kind: contracts.PrincipalWorkload, audience: contracts.AudienceControlAPI,
		},
		contracts.OriginSystemMaintenance: {
			owner: contracts.OwnerAgentControl, recordType: "maintenance_occurrence",
			kind: contracts.PrincipalWorkload, audience: contracts.AudienceControlAPI,
		},
	}[value.OriginKind]
	return expected.recordType != "" && value.SourceOwner == expected.owner &&
		value.SourceRecordType == expected.recordType && value.InitiatingKind == expected.kind &&
		value.InitiatingAudience == expected.audience
}

func knownSwitch(value SwitchID) bool {
	switch value {
	case SwitchAgentOperationEmission, SwitchAgentReleaseActivation, SwitchCapabilityExternalExecution,
		SwitchStrategyActivation, SwitchGracePublication, SwitchDelegationActivation,
		SwitchShadowIntegration, SwitchExactConfirmationLive, SwitchAutonomousLive,
		SwitchProductEquity, SwitchProductOption, SwitchProductCrypto:
		return true
	default:
		return false
	}
}

func knownGateState(value GateState) bool { return value == GateEnabled || value == GateHalted }
func knownSubject(value SubjectKind) bool {
	return value == SubjectPlatformMode || value == SubjectEffectClass || value == SubjectKillSwitch
}
func knownTransition(value Transition) bool {
	return value == TransitionRaise || value == TransitionLower || value == TransitionResume || value == TransitionHalt
}
func knownRoute(value EffectRoute) bool {
	return value == RouteNone || value == RouteExactConfirmation || value == RouteAutonomousDelegation
}

func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }
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

func (value Decision) String() string {
	return fmt.Sprintf("allowed=%t reason=%s mode=%s effect=%s", value.Allowed, value.ReasonCode, value.EffectiveMode, value.Effect)
}
