package governance

import (
	"testing"
	"time"

	"alpheus/agentplatform/contracts"
)

var (
	testNow   = time.Date(2026, 7, 19, 18, 0, 0, 0, time.UTC)
	testUser  = contracts.AuditActor{PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceActivator}
	testActor = contracts.AuditActor{PrincipalID: "activator-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceActivator}
)

func TestGovernanceContractsAndReferences(t *testing.T) {
	mode := modeRevision(1, contracts.ModeShadow)
	modeRef, err := mode.Ref()
	if err != nil {
		t.Fatal(err)
	}
	receipt := ActivationReceipt{
		SchemaRevision: SchemaRevisionV1, ReceiptID: "20000000-0000-4000-8000-000000000001",
		TargetKind: SubjectPlatformMode, TargetID: "global", TargetRevision: modeRef,
		ExpectedHeadGeneration: 0, Transition: TransitionRaise, Actor: testUser,
		DeploymentModeCeiling: contracts.ModeShadow, DeploymentEffectCeiling: contracts.EffectOperationIntent,
		RequestDigest: digest('a'), ReasonCode: "owner_activation", IssuedAt: testNow, ExpiresAt: testNow.Add(time.Minute),
	}
	receiptRef, err := receipt.Ref()
	if err != nil {
		t.Fatal(err)
	}
	head := PlatformModeHead{SchemaRevision: SchemaRevisionV1, HeadID: "global", Generation: 1,
		Revision: modeRef, Mode: mode.Mode, ActivationReceipt: &receiptRef, ActivatedBy: testActor, ActivatedAt: testNow}
	if err := head.Validate(); err != nil {
		t.Fatal(err)
	}
	withoutReceipt := head
	withoutReceipt.ActivationReceipt = nil
	if withoutReceipt.Validate() == nil {
		t.Fatal("non-disabled head without activation receipt accepted")
	}

	bad := receipt
	bad.ExpectedHeadGeneration = 1
	if bad.Validate() == nil {
		t.Fatal("receipt with non-successor target generation accepted")
	}
	bad = receipt
	bad.Actor = testActor
	if bad.Validate() == nil {
		t.Fatal("workload-authored owner receipt accepted")
	}
	bad = receipt
	bad.ExpiresAt = bad.IssuedAt.Add(time.Hour + time.Nanosecond)
	if bad.Validate() == nil {
		t.Fatal("receipt beyond absolute one-hour ceiling accepted")
	}
	head.Revision.RecordDigest = digest('f')
	if validModeBinding(PlatformModeBinding{Revision: mode, Head: head}) {
		t.Fatal("head with mismatched revision digest accepted")
	}
}

func TestResolveAllowsExactCurrentIntersection(t *testing.T) {
	snapshot := liveSnapshot(t)
	decision := Resolve(snapshot, ResolveRequest{
		Effect: contracts.EffectBrokerMutation, Route: RouteExactConfirmation,
		RequiredSwitches: []SwitchID{SwitchProductEquity},
	}, testNow.Add(time.Second))
	if !decision.Allowed || decision.ReasonCode != "allowed" || len(decision.SourceRevisions) != 5 {
		t.Fatalf("unexpected decision: %+v", decision)
	}

	autonomous := Resolve(snapshot, ResolveRequest{
		Effect: contracts.EffectBrokerMutation, Route: RouteAutonomousDelegation,
		RequiredSwitches: []SwitchID{SwitchProductEquity},
	}, testNow.Add(time.Second))
	if autonomous.Allowed || autonomous.ReasonCode != "mode_or_route_denied" {
		t.Fatalf("confirmed mode allowed autonomous route: %+v", autonomous)
	}
}

func TestResolveFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Snapshot)
		reason string
	}{
		{"stale", func(value *Snapshot) { value.FreshUntil = testNow.Add(time.Second) }, "invalid_or_stale_snapshot"},
		{"missing-mode", func(value *Snapshot) { value.Mode = nil }, "missing_or_invalid_mode_head"},
		{"malformed-mode-digest", func(value *Snapshot) { value.Mode.Head.Revision.RecordDigest = digest('e') }, "missing_or_invalid_mode_head"},
		{"deploy-lower", func(value *Snapshot) { value.DeploymentModeCeiling = contracts.ModeReadOnly }, "mode_or_route_denied"},
		{"effect-ceiling-lower", func(value *Snapshot) { value.DeploymentEffectCeiling = contracts.EffectOperationIntent }, "effect_exceeds_deployment_ceiling"},
		{"missing-effect", func(value *Snapshot) { delete(value.Effects, contracts.EffectBrokerMutation) }, "missing_or_invalid_effect_head"},
		{"halted-effect", func(value *Snapshot) {
			binding := value.Effects[contracts.EffectBrokerMutation]
			binding.Revision.State, binding.Head.State = GateHalted, GateHalted
			binding.Head.Revision, _ = binding.Revision.Ref()
			value.Effects[contracts.EffectBrokerMutation] = binding
		}, "effect_halted"},
		{"missing-switch", func(value *Snapshot) { delete(value.Switches, SwitchExactConfirmationLive) }, "missing_or_invalid_kill_switch"},
		{"halted-switch", func(value *Snapshot) {
			binding := value.Switches[SwitchProductEquity]
			binding.Revision.State, binding.Head.State = GateHalted, GateHalted
			binding.Head.Revision, _ = binding.Revision.Ref()
			value.Switches[SwitchProductEquity] = binding
		}, "kill_switch_halted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := liveSnapshot(t)
			test.mutate(&snapshot)
			decision := Resolve(snapshot, ResolveRequest{
				Effect: contracts.EffectBrokerMutation, Route: RouteExactConfirmation,
				RequiredSwitches: []SwitchID{SwitchProductEquity},
			}, testNow.Add(2*time.Second))
			if decision.Allowed || decision.ReasonCode != test.reason {
				t.Fatalf("decision=%+v want reason=%s", decision, test.reason)
			}
		})
	}
}

func TestResolveNoEffectNeedsNoGovernanceHead(t *testing.T) {
	decision := Resolve(Snapshot{
		DeploymentModeCeiling: contracts.ModeDisabled, DeploymentEffectCeiling: contracts.EffectNone,
		ObservedAt: testNow, FreshUntil: testNow.Add(time.Minute),
	}, ResolveRequest{Effect: contracts.EffectNone, Route: RouteNone}, testNow.Add(time.Second))
	if !decision.Allowed || decision.ReasonCode != "no_effect" {
		t.Fatalf("unexpected no-effect decision: %+v", decision)
	}
}

func TestResolveExternalReadCannotOmitCapabilitySwitch(t *testing.T) {
	snapshot := liveSnapshot(t)
	mode := modeRevision(1, contracts.ModeReadOnly)
	modeRef, _ := mode.Ref()
	snapshot.Mode.Revision = mode
	snapshot.Mode.Head.Mode = contracts.ModeReadOnly
	snapshot.Mode.Head.Revision = modeRef
	effect := effectRevision(1, contracts.EffectExternalRead, GateEnabled)
	effectRef, _ := effect.Ref()
	receipt := snapshot.Mode.Head.ActivationReceipt
	snapshot.Effects = map[contracts.EffectClass]EffectClassBinding{
		contracts.EffectExternalRead: {Revision: effect, Head: EffectClassHead{
			SchemaRevision: 1, HeadID: string(effect.EffectClass), Generation: 1, Revision: effectRef,
			EffectClass: effect.EffectClass, State: GateEnabled, ActivationReceipt: receipt,
			ActivatedBy: testActor, ActivatedAt: testNow,
		}},
	}
	snapshot.Switches = nil
	decision := Resolve(snapshot, ResolveRequest{Effect: contracts.EffectExternalRead, Route: RouteNone}, testNow.Add(time.Second))
	if decision.Allowed || decision.ReasonCode != "missing_or_invalid_kill_switch" {
		t.Fatalf("external read omitted mandatory capability switch: %+v", decision)
	}
}

func liveSnapshot(t *testing.T) Snapshot {
	t.Helper()
	receiptRef := contracts.RecordRef{Owner: contracts.OwnerPlatformGovernance, RecordType: "activation_receipt",
		RecordID: "20000000-0000-4000-8000-000000000001", SchemaRevision: 1, RecordDigest: digest('b')}
	mode := modeRevision(1, contracts.ModeLiveConfirmed)
	modeRef, _ := mode.Ref()
	modeBinding := PlatformModeBinding{Revision: mode, Head: PlatformModeHead{
		SchemaRevision: 1, HeadID: "global", Generation: 1, Revision: modeRef, Mode: mode.Mode,
		ActivationReceipt: &receiptRef, ActivatedBy: testActor, ActivatedAt: testNow,
	}}
	effect := effectRevision(1, contracts.EffectBrokerMutation, GateEnabled)
	effectRef, _ := effect.Ref()
	effectBinding := EffectClassBinding{Revision: effect, Head: EffectClassHead{
		SchemaRevision: 1, HeadID: string(effect.EffectClass), Generation: 1, Revision: effectRef,
		EffectClass: effect.EffectClass, State: effect.State, ActivationReceipt: &receiptRef, ActivatedBy: testActor, ActivatedAt: testNow,
	}}
	switches := map[SwitchID]KillSwitchBinding{}
	for index, switchID := range []SwitchID{SwitchAgentOperationEmission, SwitchExactConfirmationLive, SwitchProductEquity} {
		revision := switchRevision(int64(index+1), switchID, GateEnabled)
		ref, _ := revision.Ref()
		switches[switchID] = KillSwitchBinding{Revision: revision, Head: KillSwitchHead{
			SchemaRevision: 1, HeadID: string(switchID), Generation: revision.Generation, Revision: ref,
			SwitchID: switchID, State: revision.State, ActivationReceipt: &receiptRef, ActivatedBy: testActor, ActivatedAt: testNow,
		}}
	}
	return Snapshot{
		DeploymentModeCeiling: contracts.ModeLiveAutonomous, DeploymentEffectCeiling: contracts.EffectBrokerMutation,
		ObservedAt: testNow, FreshUntil: testNow.Add(time.Minute), Mode: &modeBinding,
		Effects: map[contracts.EffectClass]EffectClassBinding{contracts.EffectBrokerMutation: effectBinding}, Switches: switches,
	}
}

func modeRevision(generation int64, mode contracts.PlatformMode) PlatformModeRevision {
	return PlatformModeRevision{SchemaRevision: 1, RevisionID: "10000000-0000-4000-8000-000000000001", Generation: generation,
		Mode: mode, Author: testUser, ReasonCode: "test_revision", CreatedAt: testNow}
}

func effectRevision(generation int64, effect contracts.EffectClass, state GateState) EffectClassRevision {
	return EffectClassRevision{SchemaRevision: 1, RevisionID: "30000000-0000-4000-8000-000000000001", Generation: generation,
		EffectClass: effect, State: state, Author: testUser, ReasonCode: "test_revision", CreatedAt: testNow}
}

func switchRevision(generation int64, switchID SwitchID, state GateState) KillSwitchRevision {
	return KillSwitchRevision{SchemaRevision: 1, RevisionID: "40000000-0000-4000-8000-00000000000" + string(rune('0'+generation)), Generation: generation,
		SwitchID: switchID, State: state, Author: testUser, ReasonCode: "test_revision", CreatedAt: testNow}
}

func digest(char byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = char
	}
	return string(value)
}
