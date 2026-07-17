package rhmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCapabilitySnapshotFailsClosedOnRequiredToolDrift(t *testing.T) {
	base := ToolSchema{
		Name:         "get_accounts",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema: json.RawMessage(`{"type":"object","required":["data"]}`),
	}
	snapshot := CapabilitySnapshot{
		Version: "test-v1", Endpoint: DefaultEndpoint, GeneratedAt: time.Now().UTC(), Tools: []ToolSchema{base},
	}
	if err := validateSnapshotSchemas(snapshot, []ToolSchema{base}, []string{"get_accounts"}); err != nil {
		t.Fatalf("matching snapshot rejected: %v", err)
	}

	for name, live := range map[string][]ToolSchema{
		"renamed": {{Name: "list_accounts", InputSchema: base.InputSchema, OutputSchema: base.OutputSchema}},
		"input":   {{Name: base.Name, InputSchema: json.RawMessage(`{"type":"object","required":["account"]}`), OutputSchema: base.OutputSchema}},
		"output":  {{Name: base.Name, InputSchema: base.InputSchema, OutputSchema: json.RawMessage(`{"type":"array"}`)}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateSnapshotSchemas(snapshot, live, []string{"get_accounts"}); err == nil {
				t.Fatal("capability drift was accepted")
			}
		})
	}
}

func TestCapabilitySnapshotDocumentIsStrict(t *testing.T) {
	tool := ToolSchema{Name: "get_accounts", InputSchema: json.RawMessage(`{"type":"object"}`)}
	snapshot := CapabilitySnapshot{
		Version: "test-v1", Endpoint: DefaultEndpoint, GeneratedAt: time.Now().UTC(), Tools: []ToolSchema{tool},
	}
	path := filepath.Join(t.TempDir(), "capabilities.json")
	if err := SaveCapabilitySnapshot(path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilitySnapshot(path); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, []byte("{}")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCapabilitySnapshot(path); err == nil {
		t.Fatal("snapshot with trailing JSON was accepted")
	}
	snapshot.Tools = append(snapshot.Tools, tool)
	if err := SaveCapabilitySnapshot(path, snapshot); err == nil {
		t.Fatal("duplicate capability tool was accepted")
	}
}
