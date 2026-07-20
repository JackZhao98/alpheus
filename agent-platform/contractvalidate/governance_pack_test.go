package contractvalidate

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"alpheus/agentplatform/governance"
)

func TestGovernancePackInventory(t *testing.T) {
	root := governancePackRoot(t)
	declared := validatePackInventory(t, root)
	sort.Strings(declared)
	if strings.Join(declared, ",") != strings.Join(GovernanceTypes(), ",") {
		t.Fatalf("governance type drift\nvalidator: %v\nmanifest: %v", GovernanceTypes(), declared)
	}
}

func TestGovernanceGoFieldsMatchSchema(t *testing.T) {
	schema := readSchema(t, filepath.Join(governancePackRoot(t), "schema", "governance.schema.json"))
	types := map[string]reflect.Type{
		"PlatformModeRevision": reflect.TypeOf(governance.PlatformModeRevision{}),
		"PlatformModeHead":     reflect.TypeOf(governance.PlatformModeHead{}),
		"EffectClassRevision":  reflect.TypeOf(governance.EffectClassRevision{}),
		"EffectClassHead":      reflect.TypeOf(governance.EffectClassHead{}),
		"KillSwitchRevision":   reflect.TypeOf(governance.KillSwitchRevision{}),
		"KillSwitchHead":       reflect.TypeOf(governance.KillSwitchHead{}),
		"OwnerPolicyRevision":  reflect.TypeOf(governance.OwnerPolicyRevision{}),
		"OwnerPolicyHead":      reflect.TypeOf(governance.OwnerPolicyHead{}),
		"OwnerPolicyEvent":     reflect.TypeOf(governance.OwnerPolicyEvent{}),
		"ActivationReceipt":    reflect.TypeOf(governance.ActivationReceipt{}),
		"GovernanceEvent":      reflect.TypeOf(governance.GovernanceEvent{}),
	}
	for name, goType := range types {
		assertFieldParity(t, goType, schemaDefinition(t, schema, name))
	}
}

func TestGovernanceEnumsMatchSchema(t *testing.T) {
	schema := readSchema(t, filepath.Join(governancePackRoot(t), "schema", "governance.schema.json"))
	assertEnum(t, schemaDefinition(t, schema, "GateState"), []string{
		string(governance.GateEnabled), string(governance.GateHalted),
	})
	assertEnum(t, schemaDefinition(t, schema, "SwitchID"), []string{
		string(governance.SwitchAgentOperationEmission), string(governance.SwitchAgentReleaseActivation),
		string(governance.SwitchCapabilityExternalExecution), string(governance.SwitchStrategyActivation),
		string(governance.SwitchGracePublication), string(governance.SwitchDelegationActivation),
		string(governance.SwitchShadowIntegration), string(governance.SwitchExactConfirmationLive),
		string(governance.SwitchAutonomousLive), string(governance.SwitchProductEquity),
		string(governance.SwitchProductOption), string(governance.SwitchProductCrypto),
	})
	assertEnum(t, schemaDefinition(t, schema, "SubjectKind"), []string{
		string(governance.SubjectEffectClass), string(governance.SubjectKillSwitch), string(governance.SubjectPlatformMode),
	})
	assertEnum(t, schemaDefinition(t, schema, "Transition"), []string{
		string(governance.TransitionHalt), string(governance.TransitionLower),
		string(governance.TransitionRaise), string(governance.TransitionResume),
	})
	assertEnum(t, schemaDefinition(t, schema, "OwnerPolicyOriginKind"), []string{
		"external_event", "kernel_event", "schedule", "system_maintenance", "user_request",
	})
}

func TestGovernanceAuthoritySchemaIsExact(t *testing.T) {
	schema := readSchema(t, filepath.Join(governancePackRoot(t), "schema", "governance.schema.json"))
	for _, definition := range []string{
		"PlatformModeRevision", "EffectClassRevision", "KillSwitchRevision", "OwnerPolicyRevision",
	} {
		properties := schemaDefinition(t, schema, definition)["properties"].(map[string]any)
		if got := properties["author"].(map[string]any)["$ref"]; got != "#/$defs/ActivatorActor" {
			t.Fatalf("%s.author ref=%v", definition, got)
		}
	}
	for _, definition := range []string{
		"PlatformModeHead", "EffectClassHead", "KillSwitchHead", "OwnerPolicyHead",
	} {
		properties := schemaDefinition(t, schema, definition)["properties"].(map[string]any)
		if got := properties["activated_by"].(map[string]any)["$ref"]; got != "#/$defs/ActivatorActor" {
			t.Fatalf("%s.activated_by ref=%v", definition, got)
		}
	}
	ownerHead := schemaDefinition(t, schema, "OwnerPolicyHead")["properties"].(map[string]any)
	if got := ownerHead["revision"].(map[string]any)["$ref"]; got != "#/$defs/OwnerPolicyRevisionRef" {
		t.Fatalf("OwnerPolicyHead.revision ref=%v", got)
	}
	ownerEvent := schemaDefinition(t, schema, "OwnerPolicyEvent")["properties"].(map[string]any)
	for _, property := range []string{"previous_revision", "current_revision"} {
		if got := ownerEvent[property].(map[string]any)["$ref"]; got != "#/$defs/OwnerPolicyRevisionRef" {
			t.Fatalf("OwnerPolicyEvent.%s ref=%v", property, got)
		}
	}
}

func TestGovernanceGoldens(t *testing.T) {
	root := governancePackRoot(t)
	tests := []struct {
		class, file, digest, contractType string
		valid                             bool
	}{
		{"valid", "platform_mode_revision.json", "platform_mode_revision.sha256", "platform_mode_revision", true},
		{"valid", "effect_class_revision.json", "effect_class_revision.sha256", "effect_class_revision", true},
		{"valid", "kill_switch_revision.json", "kill_switch_revision.sha256", "kill_switch_revision", true},
		{"valid", "activation_receipt.json", "activation_receipt.sha256", "activation_receipt", true},
		{"valid", "platform_mode_head.json", "platform_mode_head.sha256", "platform_mode_head", true},
		{"valid", "effect_class_head.json", "effect_class_head.sha256", "effect_class_head", true},
		{"valid", "kill_switch_head.json", "kill_switch_head.sha256", "kill_switch_head", true},
		{"valid", "governance_event.json", "governance_event.sha256", "governance_event", true},
		{"valid", "owner_policy_revision.json", "owner_policy_revision.sha256", "owner_policy_revision", true},
		{"valid", "owner_policy_head.json", "owner_policy_head.sha256", "owner_policy_head", true},
		{"valid", "owner_policy_event.json", "owner_policy_event.sha256", "owner_policy_event", true},
		{"invalid", "receipt_stale_generation.json", "", "activation_receipt", false},
		{"invalid", "switch_unknown.json", "", "kill_switch_revision", false},
		{"invalid", "owner_policy_money_effect.json", "", "owner_policy_revision", false},
	}
	for _, test := range tests {
		t.Run(test.class+"/"+test.file, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "golden", test.class, test.file))
			if err != nil {
				t.Fatal(err)
			}
			_, digest, err := Validate(test.contractType, bytes.NewReader(raw))
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t err=%v", test.valid, err)
			}
			if test.valid {
				expected, err := os.ReadFile(filepath.Join(root, "golden", "digest", test.digest))
				if err != nil {
					t.Fatal(err)
				}
				if digest != strings.TrimSpace(string(expected)) {
					t.Fatalf("digest=%s expected=%s", digest, strings.TrimSpace(string(expected)))
				}
			}
		})
	}
}

func governancePackRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Clean(filepath.Join(packRoot(t), "..", "..")), "governance", "v1")
}
