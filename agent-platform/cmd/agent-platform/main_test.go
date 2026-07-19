package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/release"
)

const cliDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func writeManifest(t *testing.T, decision release.Decision) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	documentPath := filepath.Join(root, "docs", "agent-plan", "INDEX.md")
	if err := os.MkdirAll(filepath.Dir(documentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	document := []byte("# test index\n")
	if err := os.WriteFile(documentPath, document, 0o600); err != nil {
		t.Fatal(err)
	}
	evidenceRoot := filepath.Join(root, "audit", "agent", "ap0", "checks")
	if err := os.MkdirAll(evidenceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := release.Manifest{
		SchemaRevision: release.SchemaRevisionV1, CanonicalProfile: canonical.Profile,
		ReleaseID: "release-ap0-cli", Stage: release.StageAP0, Decision: decision,
		SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		CreatedAt:    time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
		AuthorizedBy: contracts.AuditActor{
			PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceActivator,
		},
		EffectCeiling: contracts.EffectNone,
		Documents:     []release.Document{{Path: "docs/agent-plan/INDEX.md", Digest: rawDigest(document)}},
	}
	for _, name := range release.RequiredChecks(release.StageAP0) {
		evidence := release.CheckEvidence{
			SchemaRevision: 1, Stage: release.StageAP0,
			SourceCommit: "0123456789abcdef0123456789abcdef01234567",
			Seed:         "ap0-contract-v1", Name: name, Status: "pass", Command: "test " + name,
			Assertions: []string{name + "_pass"},
		}
		evidenceRaw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceRoot, name+".json"), evidenceRaw, 0o600); err != nil {
			t.Fatal(err)
		}
		manifest.Checks = append(manifest.Checks, release.Check{Name: name, Status: "pass", EvidenceDigest: rawDigest(evidenceRaw)})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "release.json")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return file, root, digest
}

func TestVerifyReleaseCommand(t *testing.T) {
	file, root, digest := writeManifest(t, release.DecisionAuthorized)
	var output bytes.Buffer
	err := run([]string{"verify-release", "--file", file, "--root", root, "--expect-stage", "AP0", "--expect-digest", digest}, &output)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output.String(), `"status":"verified"`) || !strings.Contains(output.String(), digest) {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestVerifyReleaseRejectsNonAuthorization(t *testing.T) {
	file, root, digest := writeManifest(t, release.DecisionRejected)
	if err := run([]string{
		"verify-release", "--file", file, "--root", root, "--expect-stage", "AP0", "--expect-digest", digest,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("rejected decision passed CLI")
	}
}

func TestVerifyReleaseRejectsChangedBoundFile(t *testing.T) {
	file, root, digest := writeManifest(t, release.DecisionAuthorized)
	if err := os.WriteFile(filepath.Join(root, "docs", "agent-plan", "INDEX.md"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{
		"verify-release", "--file", file, "--root", root, "--expect-stage", "AP0", "--expect-digest", digest,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("changed bound document passed CLI")
	}
}

func TestVerifyReleaseRejectsChangedCheckEvidence(t *testing.T) {
	file, root, digest := writeManifest(t, release.DecisionAuthorized)
	evidence := filepath.Join(root, "audit", "agent", "ap0", "checks", "go_test_race.json")
	if err := os.WriteFile(evidence, []byte(`{"status":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{
		"verify-release", "--file", file, "--root", root, "--expect-stage", "AP0", "--expect-digest", digest,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("changed check evidence passed CLI")
	}
}

func TestCommandParsingFailsClosed(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"verify-release"},
		{"verify-release", "--file", "missing", "extra"},
		{"validate-contract"},
		{"validate-contract", "--file", "missing", "--type", "failure", "extra"},
	}
	for _, args := range tests {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("args %q unexpectedly passed", args)
		}
	}
}

func rawDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func TestValidateContractCommand(t *testing.T) {
	file := filepath.Join(t.TempDir(), "failure.json")
	if err := os.WriteFile(file, []byte(`{"code":"connector_timeout","message":"unavailable","retryable":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run([]string{"validate-contract", "--file", file, "--type", "failure"}, &output); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output.String(), `"status":"valid"`) || !strings.Contains(output.String(), canonical.Profile) {
		t.Fatalf("unexpected output: %s", output.String())
	}
	if err := run([]string{
		"validate-contract", "--file", file, "--type", "failure", "--expect-digest", cliDigest,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("digest mismatch passed")
	}
}
