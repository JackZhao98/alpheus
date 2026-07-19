package main

import (
	"bytes"
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

func writeManifest(t *testing.T, decision release.Decision) (string, string) {
	t.Helper()
	manifest := release.Manifest{
		SchemaRevision: release.SchemaRevisionV1, CanonicalProfile: canonical.Profile,
		ReleaseID: "release-ap0-cli", Stage: release.StageAP0, Decision: decision,
		SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		CreatedAt:    time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
		AuthorizedBy: contracts.AuditActor{
			PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceActivator,
		},
		EffectCeiling: contracts.EffectNone,
		Documents:     []release.Document{{Path: "docs/agent-plan/INDEX.md", Digest: cliDigest}},
		Checks:        []release.Check{{Name: "tests", Status: "pass", EvidenceDigest: cliDigest}},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "release.json")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := manifest.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return file, digest
}

func TestVerifyReleaseCommand(t *testing.T) {
	file, digest := writeManifest(t, release.DecisionAuthorized)
	var output bytes.Buffer
	err := run([]string{"verify-release", "--file", file, "--expect-stage", "AP0", "--expect-digest", digest}, &output)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output.String(), `"status":"verified"`) || !strings.Contains(output.String(), digest) {
		t.Fatalf("unexpected output: %s", output.String())
	}
}

func TestVerifyReleaseRejectsNonAuthorization(t *testing.T) {
	file, digest := writeManifest(t, release.DecisionRejected)
	if err := run([]string{
		"verify-release", "--file", file, "--expect-stage", "AP0", "--expect-digest", digest,
	}, &bytes.Buffer{}); err == nil {
		t.Fatal("rejected decision passed CLI")
	}
}

func TestCommandParsingFailsClosed(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"verify-release"},
		{"verify-release", "--file", "missing", "extra"},
	}
	for _, args := range tests {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("args %q unexpectedly passed", args)
		}
	}
}
