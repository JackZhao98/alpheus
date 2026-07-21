// Package contractvalidate strictly decodes versioned Agent Platform boundary contracts.
// JSON Schema is the machine contract; this package is the Go enforcement
// path and rejects lexical ambiguity before typed semantic validation.
package contractvalidate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/delivery"
	"alpheus/agentplatform/governance"
	"alpheus/agentplatform/release"
	"alpheus/agentplatform/runtimecontract"
	"alpheus/agentplatform/security"
)

const maxContractBytes = 1 << 20

type validatable interface {
	Validate() error
}

var commonTypes = map[string]func() validatable{
	"audit_actor":             func() validatable { return &contracts.AuditActor{} },
	"command_envelope":        func() validatable { return &contracts.CommandEnvelope{} },
	"effective_run_authority": func() validatable { return &contracts.EffectiveRunAuthority{} },
	"event_envelope":          func() validatable { return &contracts.EventEnvelope{} },
	"failure":                 func() validatable { return &contracts.Failure{} },
	"freshness":               func() validatable { return &contracts.Freshness{} },
	"head_ref":                func() validatable { return &contracts.HeadRef{} },
	"record_ref":              func() validatable { return &contracts.RecordRef{} },
	"revision_ref":            func() validatable { return &contracts.RevisionRef{} },
	"run_origin":              func() validatable { return &contracts.RunOrigin{} },
}

var securityDeliveryTypes = map[string]func() validatable{
	"inbox_receipt":     func() validatable { return &delivery.InboxReceipt{} },
	"outbox_record":     func() validatable { return &delivery.OutboxRecord{} },
	"profile_config":    func() validatable { return &security.ProfileConfig{} },
	"profile_set":       func() validatable { return &security.ProfileSet{} },
	"quarantine_record": func() validatable { return &delivery.QuarantineRecord{} },
}

var blobTypes = map[string]func() validatable{
	"blob_lifecycle_event": func() validatable { return &blob.LifecycleEvent{} },
	"blob_ref":             func() validatable { return &blob.BlobRef{} },
	"blob_stage_grant":     func() validatable { return &blob.StageGrant{} },
	"blob_staged":          func() validatable { return &blob.StagedBlob{} },
	"blob_reference":       func() validatable { return &blob.ReferenceBinding{} },
}

var governanceTypes = map[string]func() validatable{
	"activation_receipt":     func() validatable { return &governance.ActivationReceipt{} },
	"effect_class_head":      func() validatable { return &governance.EffectClassHead{} },
	"effect_class_revision":  func() validatable { return &governance.EffectClassRevision{} },
	"governance_event":       func() validatable { return &governance.GovernanceEvent{} },
	"kill_switch_head":       func() validatable { return &governance.KillSwitchHead{} },
	"kill_switch_revision":   func() validatable { return &governance.KillSwitchRevision{} },
	"owner_policy_event":     func() validatable { return &governance.OwnerPolicyEvent{} },
	"owner_policy_head":      func() validatable { return &governance.OwnerPolicyHead{} },
	"owner_policy_revision":  func() validatable { return &governance.OwnerPolicyRevision{} },
	"platform_mode_head":     func() validatable { return &governance.PlatformModeHead{} },
	"platform_mode_revision": func() validatable { return &governance.PlatformModeRevision{} },
}

var runtimeTypes = map[string]func() validatable{
	"artifact":                    func() validatable { return &runtimecontract.Artifact{} },
	"artifact_publication_intent": func() validatable { return &runtimecontract.ArtifactPublicationIntent{} },
	"attempt":                     func() validatable { return &runtimecontract.Attempt{} },
	"budget_ledger":               func() validatable { return &runtimecontract.BudgetLedger{} },
	"cancellation_request":        func() validatable { return &runtimecontract.CancellationRequest{} },
	"submit_cancellation_request_command": func() validatable {
		return &runtimecontract.SubmitCancellationRequestCommand{}
	},
	"checkpoint":                  func() validatable { return &runtimecontract.Checkpoint{} },
	"claim_task_command":          func() validatable { return &runtimecontract.ClaimTaskCommand{} },
	"commit_attempt_command":      func() validatable { return &runtimecontract.CommitAttemptCommand{} },
	"dependency":                  func() validatable { return &runtimecontract.Dependency{} },
	"dispatch_model_call_command": func() validatable { return &runtimecontract.DispatchModelCallCommand{} },
	"fail_attempt_command":        func() validatable { return &runtimecontract.FailAttemptCommand{} },
	"heartbeat_attempt_command":   func() validatable { return &runtimecontract.HeartbeatAttemptCommand{} },
	"mark_model_call_unknown_command": func() validatable {
		return &runtimecontract.MarkModelCallUnknownCommand{}
	},
	"model_call_manifest":       func() validatable { return &runtimecontract.ModelCallManifest{} },
	"model_call_result":         func() validatable { return &runtimecontract.ModelCallResult{} },
	"output_contract_revision":  func() validatable { return &runtimecontract.OutputContractRevision{} },
	"output_validation_receipt": func() validatable { return &runtimecontract.OutputValidationReceipt{} },
	"recovery_record":           func() validatable { return &runtimecontract.RecoveryRecord{} },
	"record_output_validation_command": func() validatable {
		return &runtimecontract.RecordOutputValidationCommand{}
	},
	"request_child_task_command": func() validatable { return &runtimecontract.RequestChildTaskCommand{} },
	"resolve_model_call_command": func() validatable { return &runtimecontract.ResolveModelCallCommand{} },
	"run":                        func() validatable { return &runtimecontract.Run{} },
	"runtime_event":              func() validatable { return &runtimecontract.RuntimeEvent{} },
	"runtime_policy":             func() validatable { return &runtimecontract.RuntimePolicy{} },
	"session":                    func() validatable { return &runtimecontract.Session{} },
	"task":                       func() validatable { return &runtimecontract.Task{} },
	"start_attempt_command":      func() validatable { return &runtimecontract.StartAttemptCommand{} },
	"trigger_occurrence":         func() validatable { return &runtimecontract.TriggerOccurrence{} },
	"trigger_registration":       func() validatable { return &runtimecontract.TriggerRegistration{} },
	"turn":                       func() validatable { return &runtimecontract.Turn{} },
}

func SupportedTypes() []string {
	values := make([]string, 0, len(commonTypes)+len(securityDeliveryTypes)+len(blobTypes)+len(governanceTypes)+len(runtimeTypes)+1)
	for name := range commonTypes {
		values = append(values, name)
	}
	for name := range securityDeliveryTypes {
		values = append(values, name)
	}
	for name := range blobTypes {
		values = append(values, name)
	}
	for name := range governanceTypes {
		values = append(values, name)
	}
	for name := range runtimeTypes {
		values = append(values, name)
	}
	values = append(values, "release_manifest")
	sort.Strings(values)
	return values
}

func CommonTypes() []string {
	values := make([]string, 0, len(commonTypes)+1)
	for name := range commonTypes {
		values = append(values, name)
	}
	values = append(values, "release_manifest")
	sort.Strings(values)
	return values
}

func SecurityDeliveryTypes() []string {
	values := make([]string, 0, len(securityDeliveryTypes))
	for name := range securityDeliveryTypes {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

func BlobTypes() []string {
	values := make([]string, 0, len(blobTypes))
	for name := range blobTypes {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

func GovernanceTypes() []string {
	values := make([]string, 0, len(governanceTypes))
	for name := range governanceTypes {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

func RuntimeTypes() []string {
	values := make([]string, 0, len(runtimeTypes))
	for name := range runtimeTypes {
		values = append(values, name)
	}
	sort.Strings(values)
	return values
}

// Validate returns canonical JSON and its contract-domain digest.
func Validate(contractType string, reader io.Reader) ([]byte, string, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxContractBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read contract: %w", err)
	}
	if len(raw) > maxContractBytes {
		return nil, "", fmt.Errorf("contract exceeds %d bytes", maxContractBytes)
	}
	strict, err := canonical.JSON(raw)
	if err != nil {
		return nil, "", err
	}

	if contractType == "release_manifest" {
		manifest, err := release.DecodeStrict(bytes.NewReader(strict))
		if err != nil {
			return nil, "", err
		}
		digest, err := manifest.Digest()
		return strict, digest, err
	}
	factory, ok := commonTypes[contractType]
	if !ok {
		factory, ok = securityDeliveryTypes[contractType]
	}
	if !ok {
		factory, ok = blobTypes[contractType]
	}
	if !ok {
		factory, ok = governanceTypes[contractType]
	}
	if !ok {
		factory, ok = runtimeTypes[contractType]
	}
	if !ok {
		return nil, "", fmt.Errorf("unknown contract type %q", contractType)
	}
	target := factory()
	decoder := json.NewDecoder(bytes.NewReader(strict))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", contractType, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, "", fmt.Errorf("decode %s: trailing value", contractType)
	}
	if err := target.Validate(); err != nil {
		return nil, "", fmt.Errorf("validate %s: %w", contractType, err)
	}
	digest, err := canonical.DigestJSON("agent-platform.contract."+contractType+".v1", strict)
	if err != nil {
		return nil, "", err
	}
	return strict, digest, nil
}
