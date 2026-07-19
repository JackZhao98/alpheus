package contractvalidate

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func packRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate contract pack")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "contracts", "common", "v1"))
}

func TestGoldenContracts(t *testing.T) {
	root := packRoot(t)
	valid := map[string]struct {
		filename   string
		digestFile string
	}{
		"command_envelope":        {"command_envelope.json", "command_envelope.sha256"},
		"effective_run_authority": {"effective_run_authority.json", "effective_run_authority.sha256"},
		"run_origin":              {"run_origin_schedule.json", "run_origin_schedule.sha256"},
		"run_origin/user":         {"run_origin_user.json", "run_origin_user.sha256"},
	}
	for name, golden := range valid {
		t.Run("valid/"+name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "golden", "valid", golden.filename))
			if err != nil {
				t.Fatal(err)
			}
			contractType := name
			if name == "run_origin/user" {
				contractType = "run_origin"
			}
			canonical, digest, err := Validate(contractType, bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if len(canonical) == 0 || len(digest) != 64 {
				t.Fatalf("canonical=%q digest=%q", canonical, digest)
			}
			expected, err := os.ReadFile(filepath.Join(root, "golden", "digest", golden.digestFile))
			if err != nil {
				t.Fatal(err)
			}
			if digest != strings.TrimSpace(string(expected)) {
				t.Fatalf("digest=%s expected=%s", digest, strings.TrimSpace(string(expected)))
			}
		})
	}

	invalid := map[string]string{
		"command_envelope":        "command_unknown_field.json",
		"effective_run_authority": "authority_unknown_mode.json",
		"run_origin/schedule":     "schedule_fabricated_conversation.json",
		"run_origin/user":         "user_missing_conversation.json",
	}
	for name, filename := range invalid {
		t.Run("invalid/"+name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "golden", "invalid", filename))
			if err != nil {
				t.Fatal(err)
			}
			contractType := name
			if name == "run_origin/schedule" || name == "run_origin/user" {
				contractType = "run_origin"
			}
			if _, _, err := Validate(contractType, bytes.NewReader(raw)); err == nil {
				t.Fatal("invalid golden passed")
			}
		})
	}
}

func TestValidationBoundaryFailsClosed(t *testing.T) {
	tests := map[string]struct {
		contractType string
		raw          []byte
	}{
		"unknown type":    {"new_contract", []byte(`{}`)},
		"duplicate field": {"failure", []byte(`{"code":"x","code":"y","message":"m","retryable":false}`)},
		"float revision":  {"record_ref", []byte(`{"owner":"worker","record_type":"x","record_id":"x","schema_revision":1.0,"record_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)},
		"oversized":       {"failure", bytes.Repeat([]byte(" "), maxContractBytes+1)},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Validate(test.contractType, bytes.NewReader(test.raw)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestSupportedTypesAreSorted(t *testing.T) {
	types := SupportedTypes()
	for index := 1; index < len(types); index++ {
		if types[index-1] >= types[index] {
			t.Fatalf("types are not strictly sorted: %v", types)
		}
	}
}
