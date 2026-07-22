package contractvalidate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func capabilityPackRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate capability contract pack")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "contracts", "capability", "v1"))
}

func TestCapabilityPackIsFrozenAndMatchesValidator(t *testing.T) {
	root := capabilityPackRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Pack      string   `json:"pack"`
		Owner     string   `json:"owner"`
		Lifecycle string   `json:"lifecycle"`
		Records   []string `json:"records"`
		Goldens   struct {
			Valid   []string `json:"valid"`
			Invalid []string `json:"invalid"`
		} `json:"goldens"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || manifest.Pack != "alpheus.capability" ||
		manifest.Owner != "agent_control_and_research_gateway" || manifest.Lifecycle != "frozen" {
		t.Fatalf("invalid capability manifest: %v %#v", err, manifest)
	}
	got := append([]string(nil), manifest.Records...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(CapabilityTypes(), ",") {
		t.Fatalf("capability validator/manifest drift: %v %v", CapabilityTypes(), got)
	}
	for _, path := range append(manifest.Goldens.Valid, manifest.Goldens.Invalid...) {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("missing golden %s: %v", path, err)
		}
	}
	valid := map[string]string{
		"tool_call_intent":   "golden/valid/tool_call_intent.json",
		"web_fetch_evidence": "golden/valid/web_fetch_evidence.json",
		"tool_receipt":       "golden/valid/tool_receipt.json",
	}
	for contractType, path := range valid {
		raw, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := Validate(contractType, bytes.NewReader(raw)); err != nil {
			t.Fatalf("valid %s: %v", contractType, err)
		}
	}
	invalid := map[string]string{
		"tool_call_intent": "golden/invalid/tool_call_private_url.json",
		"tool_receipt":     "golden/invalid/receipt_wrong_owner.json",
	}
	for contractType, path := range invalid {
		raw, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := Validate(contractType, bytes.NewReader(raw)); err == nil {
			t.Fatalf("invalid %s passed", contractType)
		}
	}
}
