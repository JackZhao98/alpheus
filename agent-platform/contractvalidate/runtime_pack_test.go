package contractvalidate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"alpheus/agentplatform/runtimecontract"
)

func TestRuntimePackInventory(t *testing.T) {
	root := runtimePackRoot(t)
	declared := validateRuntimePackInventory(t, root)
	sort.Strings(declared)
	if strings.Join(declared, ",") != strings.Join(RuntimeTypes(), ",") {
		t.Fatalf("runtime type drift\nvalidator: %v\nmanifest: %v", RuntimeTypes(), declared)
	}
	manifest := readObject(t, filepath.Join(root, "manifest.yaml"))
	if manifest["pack"] != "alpheus.runtime" || manifest["owner"] != "agent_control" ||
		manifest["effect_ceiling"] != "none" || manifest["behavior_registration"] != "disabled_until_ap8" {
		t.Fatalf("runtime boundary drift: %#v", manifest)
	}
}

func validateRuntimePackInventory(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		SchemaRevision int      `json:"schema_revision"`
		Records        []string `json:"records"`
		Commands       []string `json:"commands"`
		Events         []string `json:"events"`
		Assets         struct {
			Schemas          []string `json:"schemas"`
			StateMachines    string   `json:"state_machines"`
			Permissions      string   `json:"permissions"`
			Retention        string   `json:"retention"`
			Canonicalization string   `json:"canonicalization"`
		} `json:"assets"`
		Goldens struct {
			Valid   []string `json:"valid"`
			Invalid []string `json:"invalid"`
			Digests []string `json:"digests"`
		} `json:"goldens"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.SchemaRevision != 1 {
		t.Fatalf("invalid runtime manifest: %v", err)
	}
	paths := append([]string{}, manifest.Assets.Schemas...)
	paths = append(paths, manifest.Assets.StateMachines, manifest.Assets.Permissions,
		manifest.Assets.Retention, manifest.Assets.Canonicalization)
	paths = append(paths, manifest.Goldens.Valid...)
	paths = append(paths, manifest.Goldens.Invalid...)
	paths = append(paths, manifest.Goldens.Digests...)
	for _, relative := range paths {
		if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), "..") {
			t.Fatalf("unsafe runtime asset path %q", relative)
		}
		if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
			t.Fatalf("runtime asset %q: %v", relative, err)
		}
	}
	assertGoldenInventory(t, root, "valid", manifest.Goldens.Valid)
	assertGoldenInventory(t, root, "invalid", manifest.Goldens.Invalid)
	assertGoldenInventory(t, root, "digest", manifest.Goldens.Digests)
	for _, relative := range []string{"manifest.yaml", manifest.Assets.StateMachines, manifest.Assets.Retention} {
		content, err := os.ReadFile(filepath.Join(root, relative))
		if err != nil || !json.Valid(content) {
			t.Fatalf("%s is not strict JSON-compatible YAML: %v", relative, err)
		}
	}
	types := append([]string{}, manifest.Records...)
	types = append(types, manifest.Commands...)
	types = append(types, manifest.Events...)
	return types
}

func TestRuntimeGoFieldsMatchSchema(t *testing.T) {
	schema := readSchema(t, filepath.Join(runtimePackRoot(t), "schema", "runtime.schema.json"))
	types := map[string]reflect.Type{
		"BudgetLimit":                      reflect.TypeOf(runtimecontract.BudgetLimit{}),
		"BudgetUsage":                      reflect.TypeOf(runtimecontract.BudgetUsage{}),
		"AttemptLease":                     reflect.TypeOf(runtimecontract.AttemptLease{}),
		"ArtifactSection":                  reflect.TypeOf(runtimecontract.ArtifactSection{}),
		"ModelCallManifestCandidate":       reflect.TypeOf(runtimecontract.ModelCallManifestCandidate{}),
		"ModelCallResultCandidate":         reflect.TypeOf(runtimecontract.ModelCallResultCandidate{}),
		"ArtifactCandidate":                reflect.TypeOf(runtimecontract.ArtifactCandidate{}),
		"OutputContractRevision":           reflect.TypeOf(runtimecontract.OutputContractRevision{}),
		"RuntimePolicy":                    reflect.TypeOf(runtimecontract.RuntimePolicy{}),
		"TriggerRegistration":              reflect.TypeOf(runtimecontract.TriggerRegistration{}),
		"TriggerOccurrence":                reflect.TypeOf(runtimecontract.TriggerOccurrence{}),
		"Run":                              reflect.TypeOf(runtimecontract.Run{}),
		"Task":                             reflect.TypeOf(runtimecontract.Task{}),
		"Dependency":                       reflect.TypeOf(runtimecontract.Dependency{}),
		"Session":                          reflect.TypeOf(runtimecontract.Session{}),
		"Attempt":                          reflect.TypeOf(runtimecontract.Attempt{}),
		"Turn":                             reflect.TypeOf(runtimecontract.Turn{}),
		"ModelCallManifest":                reflect.TypeOf(runtimecontract.ModelCallManifest{}),
		"ModelCallResult":                  reflect.TypeOf(runtimecontract.ModelCallResult{}),
		"OutputValidationReceipt":          reflect.TypeOf(runtimecontract.OutputValidationReceipt{}),
		"Artifact":                         reflect.TypeOf(runtimecontract.Artifact{}),
		"ArtifactPublicationIntent":        reflect.TypeOf(runtimecontract.ArtifactPublicationIntent{}),
		"Checkpoint":                       reflect.TypeOf(runtimecontract.Checkpoint{}),
		"BudgetLedger":                     reflect.TypeOf(runtimecontract.BudgetLedger{}),
		"CancellationRequest":              reflect.TypeOf(runtimecontract.CancellationRequest{}),
		"SubmitCancellationRequestCommand": reflect.TypeOf(runtimecontract.SubmitCancellationRequestCommand{}),
		"RecoveryRecord":                   reflect.TypeOf(runtimecontract.RecoveryRecord{}),
		"RuntimeEvent":                     reflect.TypeOf(runtimecontract.RuntimeEvent{}),
		"ClaimTaskCommand":                 reflect.TypeOf(runtimecontract.ClaimTaskCommand{}),
		"StartAttemptCommand":              reflect.TypeOf(runtimecontract.StartAttemptCommand{}),
		"HeartbeatAttemptCommand":          reflect.TypeOf(runtimecontract.HeartbeatAttemptCommand{}),
		"DispatchModelCallCommand":         reflect.TypeOf(runtimecontract.DispatchModelCallCommand{}),
		"ResolveModelCallCommand":          reflect.TypeOf(runtimecontract.ResolveModelCallCommand{}),
		"MarkModelCallUnknownCommand":      reflect.TypeOf(runtimecontract.MarkModelCallUnknownCommand{}),
		"CommitAttemptCommand":             reflect.TypeOf(runtimecontract.CommitAttemptCommand{}),
		"RecordOutputValidationCommand":    reflect.TypeOf(runtimecontract.RecordOutputValidationCommand{}),
		"FailAttemptCommand":               reflect.TypeOf(runtimecontract.FailAttemptCommand{}),
		"RequestChildTaskCommand":          reflect.TypeOf(runtimecontract.RequestChildTaskCommand{}),
	}
	for name, goType := range types {
		t.Run(name, func(t *testing.T) {
			assertFieldParity(t, goType, schemaDefinition(t, schema, name))
		})
	}
}

func TestRuntimeEnumsMatchSchema(t *testing.T) {
	schema := readSchema(t, filepath.Join(runtimePackRoot(t), "schema", "runtime.schema.json"))
	tests := []struct {
		definition string
		property   string
		values     []string
	}{
		{"TriggerRegistration", "kind", []string{"external_event", "kernel_event", "schedule", "system_maintenance"}},
		{"TriggerOccurrence", "kind", []string{"external_event", "kernel_event", "schedule", "system_maintenance", "system_recovery"}},
		{"Run", "state", []string{"canceled", "canceling", "dead_lettered", "failed", "queued", "running", "succeeded", "superseded", "waiting"}},
		{"Task", "state", []string{"blocked", "canceled", "dead_lettered", "failed", "ready", "result_committed", "running", "succeeded", "superseded", "waiting"}},
		{"Session", "state", []string{"closed", "open"}},
		{"Attempt", "state", []string{"canceled", "executing", "failed", "leased", "result_committed", "superseded", "timed_out"}},
		{"Turn", "state", []string{"canceled", "dispatched", "failed", "planned", "result_committed", "unknown"}},
		{"ModelCallResult", "finish_reason", []string{"content_filter", "length", "stop", "tool_use"}},
		{"ModelCallResultCandidate", "finish_reason", []string{"content_filter", "length", "stop", "tool_use"}},
		{"ResolveModelCallCommand", "outcome", []string{"failed", "result_committed"}},
		{"FailAttemptCommand", "retry_class", []string{"infrastructure", "invalid_output", "none"}},
		{"BudgetLedger", "scope", []string{"run", "task"}},
		{"BudgetLedger", "state", []string{"closed", "exhausted", "open"}},
		{"CancellationRequest", "target", []string{"run", "task"}},
		{"CancellationRequest", "mode", []string{"cancel", "supersede"}},
		{"RecoveryRecord", "decision", []string{"canceled", "dead_letter", "retry_same_task", "reuse_committed_result"}},
		{"RuntimeEvent", "subject", []string{"attempt", "budget", "publication_intent", "run", "session", "task", "turn"}},
	}
	for _, test := range tests {
		properties := schemaDefinition(t, schema, test.definition)["properties"].(map[string]any)
		assertEnum(t, properties[test.property].(map[string]any), test.values)
	}
}

func TestRuntimeAuthorityRefsAndOutputSchemaAreExact(t *testing.T) {
	schema := readSchema(t, filepath.Join(runtimePackRoot(t), "schema", "runtime.schema.json"))
	tests := []struct {
		definition string
		property   string
		wantRef    string
	}{
		{"TriggerRegistration", "owner_policy", "#/$defs/OwnerPolicyRevisionRef"},
		{"TriggerRegistration", "runtime_policy", "#/$defs/RuntimePolicyRevisionRef"},
		{"TriggerOccurrence", "registration", "#/$defs/TriggerRegistrationRevisionRef"},
		{"TriggerOccurrence", "owner_policy", "#/$defs/OwnerPolicyRevisionRef"},
		{"Run", "runtime_policy", "#/$defs/RuntimePolicyRevisionRef"},
		{"Task", "output_contract", "#/$defs/OutputContractRevisionRef"},
		{"BudgetLedger", "runtime_policy", "#/$defs/RuntimePolicyRevisionRef"},
		{"RequestChildTaskCommand", "output_contract", "#/$defs/OutputContractRevisionRef"},
	}
	for _, test := range tests {
		properties := schemaDefinition(t, schema, test.definition)["properties"].(map[string]any)
		if got := properties[test.property].(map[string]any)["$ref"]; got != test.wantRef {
			t.Fatalf("%s.%s ref=%v want=%s", test.definition, test.property, got, test.wantRef)
		}
	}

	output := schemaDefinition(t, schema, "OutputContractRevision")["properties"].(map[string]any)
	allOf := output["schema"].(map[string]any)["allOf"].([]any)
	constraints := allOf[1].(map[string]any)["properties"].(map[string]any)
	if got := constraints["media_type"].(map[string]any)["const"]; got != "application/json" {
		t.Fatalf("output schema media_type=%v", got)
	}
}

func TestRuntimeStateMachineMatchesCode(t *testing.T) {
	root := runtimePackRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "state-machines", "runtime.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Machines map[string]struct {
			Transitions map[string][]string `json:"transitions"`
		} `json:"machines"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	subjects := map[string]runtimecontract.RuntimeSubject{
		"run": runtimecontract.SubjectRun, "task": runtimecontract.SubjectTask,
		"session": runtimecontract.SubjectSession, "attempt": runtimecontract.SubjectAttempt,
		"turn": runtimecontract.SubjectTurn, "budget": runtimecontract.SubjectBudget,
		"publication_intent": runtimecontract.SubjectIntent,
	}
	states := map[string][]string{
		"run":                {"queued", "running", "waiting", "canceling", "succeeded", "failed", "canceled", "superseded", "dead_lettered"},
		"task":               {"blocked", "ready", "running", "waiting", "result_committed", "succeeded", "failed", "canceled", "superseded", "dead_lettered"},
		"session":            {"open", "closed"},
		"attempt":            {"leased", "executing", "result_committed", "failed", "timed_out", "canceled", "superseded"},
		"turn":               {"planned", "dispatched", "result_committed", "failed", "unknown", "canceled"},
		"budget":             {"open", "exhausted", "closed"},
		"publication_intent": {"disabled"},
	}
	for machine, subject := range subjects {
		declared, ok := document.Machines[machine]
		if !ok {
			t.Fatalf("missing machine %s", machine)
		}
		for _, from := range states[machine] {
			allowed := make(map[string]struct{}, len(declared.Transitions[from]))
			for _, to := range declared.Transitions[from] {
				allowed[to] = struct{}{}
			}
			for _, to := range states[machine] {
				_, want := allowed[to]
				if got := runtimecontract.CanTransition(subject, from, to); got != want {
					t.Fatalf("%s %s -> %s code=%t manifest=%t", machine, from, to, got, want)
				}
			}
		}
	}
}

func TestRuntimeGoldens(t *testing.T) {
	root := runtimePackRoot(t)
	tests := []struct {
		class, file, digest, contractType string
		valid                             bool
	}{
		{"valid", "artifact_nonmoney.json", "artifact_nonmoney.sha256", "artifact", true},
		{"valid", "claim_task_command.json", "claim_task_command.sha256", "claim_task_command", true},
		{"valid", "commit_attempt_nonmoney.json", "commit_attempt_nonmoney.sha256", "commit_attempt_command", true},
		{"valid", "fail_attempt_retryable.json", "fail_attempt_retryable.sha256", "fail_attempt_command", true},
		{"valid", "publication_disabled.json", "publication_disabled.sha256", "artifact_publication_intent", true},
		{"valid", "recovery_reuse.json", "recovery_reuse.sha256", "recovery_record", true},
		{"valid", "run_queued.json", "run_queued.sha256", "run", true},
		{"valid", "output_contract_revision.json", "output_contract_revision.sha256", "output_contract_revision", true},
		{"valid", "output_validation_receipt.json", "output_validation_receipt.sha256", "output_validation_receipt", true},
		{"valid", "record_output_validation_command.json", "record_output_validation_command.sha256", "record_output_validation_command", true},
		{"valid", "submit_cancellation_request_command.json", "submit_cancellation_request_command.sha256", "submit_cancellation_request_command", true},
		{"valid", "runtime_policy.json", "runtime_policy.sha256", "runtime_policy", true},
		{"invalid", "artifact_operation_intent.json", "", "artifact", false},
		{"invalid", "budget_overcommitted.json", "", "budget_ledger", false},
		{"invalid", "claim_worker_spoof.json", "", "claim_task_command", false},
		{"invalid", "fail_attempt_nonretryable_infrastructure.json", "", "fail_attempt_command", false},
		{"invalid", "fail_attempt_retryable_none.json", "", "fail_attempt_command", false},
		{"invalid", "publication_behavior_event.json", "", "artifact_publication_intent", false},
		{"invalid", "output_validation_receipt_invalid_decision.json", "", "output_validation_receipt", false},
		{"invalid", "record_output_validation_worker_actor.json", "", "record_output_validation_command", false},
		{"invalid", "record_output_validation_wrong_audience.json", "", "record_output_validation_command", false},
		{"invalid", "recovery_retry_same_attempt.json", "", "recovery_record", false},
		{"invalid", "run_terminal_without_time.json", "", "run", false},
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

func runtimePackRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(filepath.Clean(filepath.Join(packRoot(t), "..", "..")), "runtime", "v1")
}

func readObject(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
