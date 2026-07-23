package capability

import (
	"bytes"
	"encoding/json"
	"time"

	"alpheus/agentplatform/contracts"
)

const maxKernelReadResultBytes = 64 << 10

// KernelReadObservation is the sanitized `data` member returned by Kernel.
// The upstream guide, MCP framing, credential, and unmasked account identifier
// are excluded before this crosses into Cortex.
type KernelReadObservation struct {
	SchemaRevision uint16    `json:"schema_revision"`
	ToolCallID     string    `json:"tool_call_id"`
	ToolID         ToolID    `json:"tool_id"`
	RequestDigest  string    `json:"request_digest"`
	Provider       string    `json:"provider"`
	SourceTool     string    `json:"source_tool"`
	ResultJSON     string    `json:"result_json"`
	ObservedAt     time.Time `json:"observed_at"`
	AvailableAt    time.Time `json:"available_at"`
}

type KernelReadEvidence struct {
	SchemaRevision uint16    `json:"schema_revision"`
	EvidenceID     string    `json:"evidence_id"`
	ToolCallID     string    `json:"tool_call_id"`
	ToolID         ToolID    `json:"tool_id"`
	Provider       string    `json:"provider"`
	SourceTool     string    `json:"source_tool"`
	ResultJSON     string    `json:"result_json"`
	ResultDigest   string    `json:"result_digest"`
	ObservedAt     time.Time `json:"observed_at"`
	AvailableAt    time.Time `json:"available_at"`
}

type KernelReadToolReceipt struct {
	SchemaRevision uint16               `json:"schema_revision"`
	ReceiptID      string               `json:"receipt_id"`
	ToolCallID     string               `json:"tool_call_id"`
	ToolID         ToolID               `json:"tool_id"`
	RequestDigest  string               `json:"request_digest"`
	State          ReceiptState         `json:"state"`
	Evidence       contracts.RecordRef  `json:"evidence"`
	Executor       contracts.AuditActor `json:"executor"`
	CompletedAt    time.Time            `json:"completed_at"`
}

func (value KernelReadObservation) Validate() error {
	spec, ok := KernelReadToolSpecForID(value.ToolID)
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ToolCallID) || !ok || value.SourceTool != spec.SourceTool ||
		!validDigest(value.RequestDigest) || value.Provider != "kernel_robinhood_mcp" || !validKernelResult(value.ResultJSON) ||
		!validUTC(value.ObservedAt) || !validUTC(value.AvailableAt) || value.AvailableAt.Before(value.ObservedAt) {
		return ErrInvalidCapability
	}
	return nil
}

func (value KernelReadEvidence) Validate() error {
	spec, ok := KernelReadToolSpecForID(value.ToolID)
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.EvidenceID) || !validID(value.ToolCallID) || !ok ||
		value.SourceTool != spec.SourceTool || value.Provider != "kernel_robinhood_mcp" || !validKernelResult(value.ResultJSON) ||
		!validDigest(value.ResultDigest) ||
		!validUTC(value.ObservedAt) || !validUTC(value.AvailableAt) || value.AvailableAt.Before(value.ObservedAt) {
		return ErrInvalidCapability
	}
	return nil
}

func (value KernelReadToolReceipt) Validate() error {
	_, knownTool := KernelReadToolSpecForID(value.ToolID)
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.ReceiptID) || !validID(value.ToolCallID) ||
		!knownTool || !validDigest(value.RequestDigest) || value.State != ReceiptSucceeded ||
		value.Evidence.Validate() != nil || value.Evidence.Owner != contracts.OwnerKernel ||
		value.Evidence.RecordType != "kernel_read_evidence" || value.Executor.Validate() != nil ||
		value.Executor.PrincipalID != "kernel-1" || value.Executor.Kind != contracts.PrincipalKernel ||
		value.Executor.Audience != contracts.AudienceKernel || !validUTC(value.CompletedAt) {
		return ErrInvalidCapability
	}
	return nil
}

func validKernelResult(resultJSON string) bool {
	raw := []byte(resultJSON)
	if len(raw) == 0 || len(raw) > maxKernelReadResultBytes || !json.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return false
	}
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}
