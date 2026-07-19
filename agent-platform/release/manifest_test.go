package release

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/contracts"
)

const (
	testDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testCommit = "0123456789abcdef0123456789abcdef01234567"
)

func validManifest() Manifest {
	return Manifest{
		SchemaRevision: SchemaRevisionV1, CanonicalProfile: CanonicalProfile,
		ReleaseID: "release-ap0-1", Stage: StageAP0, Decision: DecisionAuthorized,
		SourceCommit: testCommit, CreatedAt: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
		AuthorizedBy: contracts.AuditActor{
			PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceActivator,
		},
		EffectCeiling: contracts.EffectNone,
		Documents: []Document{
			{Path: "docs/agent-plan/BUILD_ROADMAP.md", Digest: testDigest},
			{Path: "docs/agent-plan/INDEX.md", Digest: testDigest},
		},
		Checks: []Check{
			{Name: "contracts", Status: "pass", EvidenceDigest: testDigest},
			{Name: "tests", Status: "pass", EvidenceDigest: testDigest},
		},
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVerifyDigestBoundManifest(t *testing.T) {
	value := validManifest()
	digest, err := value.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	got, gotDigest, err := Verify(mustJSON(t, value), StageAP0, digest)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.ReleaseID != value.ReleaseID || gotDigest != digest {
		t.Fatalf("verified manifest mismatch: %#v digest=%s", got, gotDigest)
	}

	changed := value
	changed.ReleaseID = "release-ap0-2"
	if _, _, err := Verify(mustJSON(t, changed), StageAP0, digest); err == nil {
		t.Fatal("changed manifest retained authorization digest")
	}
	if _, _, err := Verify(mustJSON(t, value), StageAP1, ""); err == nil {
		t.Fatal("wrong stage accepted")
	}
	if _, _, err := Verify(mustJSON(t, value), "AP99", ""); err == nil {
		t.Fatal("unknown expected stage accepted")
	}
	if _, _, err := Verify(mustJSON(t, value), StageAP0, "not-a-digest"); err == nil {
		t.Fatal("malformed expected digest accepted")
	}
}

func TestDecodeStrictRejectsMalformedBoundary(t *testing.T) {
	valid := mustJSON(t, validManifest())
	unknown := bytes.Replace(valid, []byte(`{"schema_revision":1`), []byte(`{"unknown":true,"schema_revision":1`), 1)
	tests := map[string][]byte{
		"unknown field": unknown,
		"duplicate field": bytes.Replace(valid, []byte(`{"schema_revision":1`),
			[]byte(`{"schema_revision":1,"schema_revision":1`), 1),
		"trailing value": append(append([]byte{}, valid...), []byte(` {}`)...),
		"too large":      append(append([]byte{}, valid...), bytes.Repeat([]byte(" "), maxManifestBytes)...),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeStrict(bytes.NewReader(raw)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestManifestValidationFailsClosed(t *testing.T) {
	tests := map[string]func(*Manifest){
		"AP0 effect":       func(value *Manifest) { value.EffectCeiling = contracts.EffectExternalRead },
		"unknown effect":   func(value *Manifest) { value.Stage = StageAP1; value.EffectCeiling = "new_effect" },
		"unknown stage":    func(value *Manifest) { value.Stage = "AP99" },
		"wrong actor":      func(value *Manifest) { value.AuthorizedBy.Kind = contracts.PrincipalWorkload },
		"failed check":     func(value *Manifest) { value.Checks[0].Status = "fail" },
		"unsorted docs":    func(value *Manifest) { value.Documents[0], value.Documents[1] = value.Documents[1], value.Documents[0] },
		"duplicate checks": func(value *Manifest) { value.Checks[1].Name = value.Checks[0].Name },
		"absolute path":    func(value *Manifest) { value.Documents[0].Path = "/etc/passwd" },
		"bad commit":       func(value *Manifest) { value.SourceCommit = strings.Repeat("z", 40) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validManifest()
			mutate(&value)
			if err := value.Validate(); !errors.Is(err, ErrInvalidManifest) {
				t.Fatalf("Validate error=%v", err)
			}
		})
	}
}
