package rhmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type CapabilitySnapshot struct {
	Version     string       `json:"version"`
	Endpoint    string       `json:"endpoint"`
	GeneratedAt time.Time    `json:"generated_at"`
	Tools       []ToolSchema `json:"tools"`
}

func LoadCapabilitySnapshot(path string) (CapabilitySnapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return CapabilitySnapshot{}, fmt.Errorf("load capability snapshot")
	}
	var snapshot CapabilitySnapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return CapabilitySnapshot{}, fmt.Errorf("invalid capability snapshot")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return CapabilitySnapshot{}, fmt.Errorf("invalid capability snapshot")
	}
	if err := validateCapabilitySnapshot(snapshot); err != nil {
		return CapabilitySnapshot{}, err
	}
	return snapshot, nil
}

func SaveCapabilitySnapshot(path string, snapshot CapabilitySnapshot) error {
	if err := validateCapabilitySnapshot(snapshot); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode capability snapshot")
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write capability snapshot")
	}
	return nil
}

func validateCapabilitySnapshot(snapshot CapabilitySnapshot) error {
	if snapshot.Version == "" || snapshot.Endpoint != DefaultEndpoint || snapshot.GeneratedAt.IsZero() || len(snapshot.Tools) == 0 {
		return fmt.Errorf("invalid capability snapshot")
	}
	seen := make(map[string]bool, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		if tool.Name == "" || seen[tool.Name] || len(tool.InputSchema) == 0 {
			return fmt.Errorf("invalid capability snapshot")
		}
		seen[tool.Name] = true
		input, err := canonicalSchema(tool.InputSchema)
		if err != nil || input == "" || input == "null" {
			return fmt.Errorf("invalid capability snapshot")
		}
		if len(tool.OutputSchema) > 0 {
			if _, err := canonicalSchema(tool.OutputSchema); err != nil {
				return fmt.Errorf("invalid capability snapshot")
			}
		}
	}
	return nil
}

func canonicalSchema(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	return string(canonical), err
}

func ValidateSnapshot(ctx context.Context, client *Client, snapshot CapabilitySnapshot, required []string) error {
	live, err := client.Discover(ctx)
	if err != nil {
		return err
	}
	return validateSnapshotSchemas(snapshot, live, required)
}

func validateSnapshotSchemas(snapshot CapabilitySnapshot, live []ToolSchema, required []string) error {
	if err := validateCapabilitySnapshot(snapshot); err != nil {
		return err
	}
	committedByName := make(map[string]ToolSchema, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		committedByName[tool.Name] = tool
	}
	liveByName := make(map[string]ToolSchema, len(live))
	for _, tool := range live {
		if tool.Name == "" {
			return fmt.Errorf("invalid provider tool schema")
		}
		if _, exists := liveByName[tool.Name]; exists {
			return fmt.Errorf("duplicate provider tool schema")
		}
		liveByName[tool.Name] = tool
	}
	requiredSeen := make(map[string]bool, len(required))
	drifted := make([]string, 0)
	for _, name := range required {
		if name == "" || requiredSeen[name] {
			return fmt.Errorf("invalid required provider tool")
		}
		requiredSeen[name] = true
		committed, ok := committedByName[name]
		if !ok {
			return fmt.Errorf("required tool missing from committed snapshot")
		}
		current, ok := liveByName[name]
		if !ok {
			return fmt.Errorf("required provider tool missing")
		}
		committedInput, err := canonicalSchema(committed.InputSchema)
		if err != nil {
			return fmt.Errorf("invalid committed tool schema")
		}
		currentInput, err := canonicalSchema(current.InputSchema)
		if err != nil {
			return fmt.Errorf("invalid provider tool schema")
		}
		committedOutput, err := canonicalSchema(committed.OutputSchema)
		if err != nil {
			return fmt.Errorf("invalid committed tool schema")
		}
		currentOutput, err := canonicalSchema(current.OutputSchema)
		if err != nil {
			return fmt.Errorf("invalid provider tool schema")
		}
		if committedInput != currentInput || committedOutput != currentOutput {
			drifted = append(drifted, name)
		}
	}
	if len(drifted) > 0 {
		return fmt.Errorf("required provider tool schema drift: %s", strings.Join(drifted, ","))
	}
	return nil
}
