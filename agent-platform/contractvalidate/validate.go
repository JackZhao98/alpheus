// Package contractvalidate strictly decodes AP0 common boundary contracts.
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

	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/release"
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

func SupportedTypes() []string {
	values := make([]string, 0, len(commonTypes)+1)
	for name := range commonTypes {
		values = append(values, name)
	}
	values = append(values, "release_manifest")
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
