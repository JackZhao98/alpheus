package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// cortexGEXBOTAsOfAuthorization is read only from the immutable Control
// intent.  Research Gateway never accepts a Worker-supplied Provider query.
type cortexGEXBOTAsOfAuthorization struct {
	ToolCallID    string    `json:"tool_call_id"`
	ToolID        string    `json:"tool_id"`
	RequestDigest string    `json:"request_digest"`
	Symbol        string    `json:"symbol"`
	Category      string    `json:"category"`
	AsOf          time.Time `json:"as_of"`
}

type cortexGEXBOTToolResult struct {
	Receipt  cortexGEXBOTToolReceipt `json:"receipt"`
	Evidence cortexGEXBOTEvidence    `json:"evidence"`
}

// Research Gateway is a distinct Go module.  It uses a local wire decoder;
// Cortex Control validates the canonical Agent-Plane capability again before
// acknowledging a receipt.
type cortexGEXBOTToolReceipt struct {
	SchemaRevision uint16             `json:"schema_revision"`
	ReceiptID      string             `json:"receipt_id"`
	ToolCallID     string             `json:"tool_call_id"`
	ToolID         string             `json:"tool_id"`
	RequestDigest  string             `json:"request_digest"`
	State          string             `json:"state"`
	Evidence       cortexGEXRecordRef `json:"evidence"`
	Executor       cortexGEXActor     `json:"executor"`
	CompletedAt    time.Time          `json:"completed_at"`
}

type cortexGEXBOTEvidence struct {
	SchemaRevision    uint16          `json:"schema_revision"`
	EvidenceID        string          `json:"evidence_id"`
	ToolCallID        string          `json:"tool_call_id"`
	Provider          string          `json:"provider"`
	Available         bool            `json:"available"`
	Symbol            string          `json:"symbol"`
	Category          string          `json:"category"`
	AsOf              time.Time       `json:"as_of"`
	ObservationID     string          `json:"observation_id,omitempty"`
	ObservationDigest string          `json:"observation_digest,omitempty"`
	ObservedAt        *time.Time      `json:"observed_at,omitempty"`
	AvailableAt       *time.Time      `json:"available_at,omitempty"`
	Metrics           json.RawMessage `json:"metrics,omitempty"`
	Raw               *cortexGEXRaw   `json:"raw,omitempty"`
}

type cortexGEXRaw struct {
	BlobID        string `json:"blob_id"`
	ContentDigest string `json:"content_digest"`
	SizeBytes     int64  `json:"size_bytes"`
}

type cortexGEXRecordRef struct {
	Owner        string `json:"owner"`
	RecordType   string `json:"record_type"`
	RecordID     string `json:"record_id"`
	Schema       int    `json:"schema_revision"`
	RecordDigest string `json:"record_digest"`
}

type cortexGEXActor struct {
	PrincipalID string `json:"principal_id"`
	Kind        string `json:"kind"`
	Audience    string `json:"audience"`
}

func (g *gateway) cortexGEXBOTAsOfTool(w http.ResponseWriter, r *http.Request) {
	if g == nil || g.db == nil || g.gexbotURL == "" || g.gexbotToken == "" || g.moodyBlues == nil || !g.moodyBlues.supports("gexbot_classic", "as_of") || !g.validCortexToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	var input struct {
		ToolCallID string `json:"tool_call_id"`
	}
	if !decodeGatewayJSON(w, r, &input) {
		return
	}
	input.ToolCallID = strings.TrimSpace(input.ToolCallID)
	if !gatewayIdentifier(input.ToolCallID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid tool call"})
		return
	}
	auth, err := g.authorizeCortexGEXBOTAsOf(r.Context(), input.ToolCallID)
	if err != nil {
		log.Printf("Cortex GEXBOT authorization failed: %v", err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tool authorization unavailable"})
		return
	}
	if existing, found, err := g.loadCortexGEXBOTAsOfReceipt(r.Context(), auth.ToolCallID); err != nil {
		log.Printf("Cortex GEXBOT receipt lookup failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
		return
	} else if found {
		if !validCortexGEXBOTToolResult(existing, auth) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	observation, err := g.fetchGEXBOTAsOf(r.Context(), auth)
	if err != nil {
		log.Printf("Cortex GEXBOT provider failed for %s: %v", auth.ToolCallID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT Provider unavailable"})
		return
	}
	result, err := g.recordCortexGEXBOTAsOfReceipt(r.Context(), auth.ToolCallID, observation)
	if err != nil || !validCortexGEXBOTToolResult(result, auth) {
		log.Printf("Cortex GEXBOT receipt persistence failed: %v", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (g *gateway) authorizeCortexGEXBOTAsOf(ctx context.Context, toolCallID string) (cortexGEXBOTAsOfAuthorization, error) {
	var raw []byte
	err := g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.get_cortex_gexbot_as_of_authorization($1)::TEXT`, toolCallID).Scan(&raw)
	})
	if err != nil || len(raw) == 0 || len(raw) > maxCortexToolResponseBytes {
		return cortexGEXBOTAsOfAuthorization{}, fmt.Errorf("authorization query unavailable: %w", err)
	}
	var auth cortexGEXBOTAsOfAuthorization
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&auth) != nil || decoder.Decode(&struct{}{}) != io.EOF || auth.ToolCallID != toolCallID ||
		auth.ToolID != "research_gexbot_as_of" || !gatewayDigest(auth.RequestDigest) ||
		!safeSymbol(auth.Symbol) || !validGEXBOTCategory(auth.Category) || auth.AsOf.IsZero() || auth.AsOf.Location() != time.UTC || auth.AsOf.After(time.Now().UTC()) {
		return cortexGEXBOTAsOfAuthorization{}, fmt.Errorf("authorization response invalid")
	}
	return auth, nil
}

func (g *gateway) loadCortexGEXBOTAsOfReceipt(ctx context.Context, toolCallID string) (cortexGEXBOTToolResult, bool, error) {
	var raw []byte
	err := g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.get_research_gexbot_as_of_receipt($1)::TEXT`, toolCallID).Scan(&raw)
	})
	if err != nil {
		return cortexGEXBOTToolResult{}, false, fmt.Errorf("receipt lookup unavailable: %w", err)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return cortexGEXBOTToolResult{}, false, nil
	}
	return decodeCortexGEXBOTToolResult(raw)
}

func (g *gateway) fetchGEXBOTAsOf(ctx context.Context, auth cortexGEXBOTAsOfAuthorization) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"symbol": auth.Symbol, "category": auth.Category, "as_of": auth.AsOf})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.gexbotURL+"/v1/as-of", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+g.gexbotToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := g.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxGEXBOTProviderResponseBytes+1))
	if err != nil || response.StatusCode != http.StatusOK || len(raw) == 0 || len(raw) > maxGEXBOTProviderResponseBytes {
		return nil, fmt.Errorf("provider response unavailable")
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value["payload"] != nil {
		return nil, fmt.Errorf("provider response invalid")
	}
	if available, exists := value["available"]; exists {
		if string(available) != "false" {
			return nil, fmt.Errorf("provider availability marker invalid")
		}
	} else {
		value["available"] = json.RawMessage("true")
	}
	normalized, err := json.Marshal(value)
	if err != nil || len(normalized) == 0 || len(normalized) > maxGEXBOTProviderResponseBytes {
		return nil, fmt.Errorf("provider response invalid")
	}
	compacted, err := compactGEXBOTObservation(normalized)
	if err != nil {
		return nil, fmt.Errorf("provider response transform failed: %w", err)
	}
	return compacted, nil
}

func (g *gateway) recordCortexGEXBOTAsOfReceipt(ctx context.Context, toolCallID string, observation json.RawMessage) (cortexGEXBOTToolResult, error) {
	if len(observation) == 0 || len(observation) > maxGEXBOTProviderResponseBytes || !json.Valid(observation) {
		return cortexGEXBOTToolResult{}, fmt.Errorf("invalid normalized observation")
	}
	var raw []byte
	err := g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.record_research_gexbot_as_of_receipt($1,$2::JSONB)::TEXT`, toolCallID, string(observation)).Scan(&raw)
	})
	if err != nil || len(raw) == 0 || len(raw) > maxCortexToolResponseBytes {
		return cortexGEXBOTToolResult{}, fmt.Errorf("receipt unavailable: %w", err)
	}
	result, _, err := decodeCortexGEXBOTToolResult(raw)
	return result, err
}

func decodeCortexGEXBOTToolResult(raw []byte) (cortexGEXBOTToolResult, bool, error) {
	if len(raw) == 0 || len(raw) > maxCortexToolResponseBytes {
		return cortexGEXBOTToolResult{}, false, fmt.Errorf("tool result is too large")
	}
	var result cortexGEXBOTToolResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF || !validCortexGEXBOTReceipt(result.Receipt) || !validCortexGEXBOTEvidence(result.Evidence) ||
		result.Receipt.ToolCallID != result.Evidence.ToolCallID || result.Receipt.Evidence.RecordID != result.Evidence.EvidenceID {
		return cortexGEXBOTToolResult{}, false, fmt.Errorf("tool result is invalid")
	}
	return result, true, nil
}

func validCortexGEXBOTToolResult(result cortexGEXBOTToolResult, auth cortexGEXBOTAsOfAuthorization) bool {
	return validCortexGEXBOTReceipt(result.Receipt) && validCortexGEXBOTEvidence(result.Evidence) && result.Receipt.ToolCallID == auth.ToolCallID &&
		result.Receipt.ToolID == "research_gexbot_as_of" && result.Receipt.RequestDigest == auth.RequestDigest &&
		result.Receipt.Evidence.RecordID == result.Evidence.EvidenceID && result.Evidence.Symbol == auth.Symbol &&
		result.Evidence.Category == auth.Category && result.Evidence.AsOf.Equal(auth.AsOf)
}

func validCortexGEXBOTReceipt(value cortexGEXBOTToolReceipt) bool {
	return value.SchemaRevision == 1 && gatewayIdentifier(value.ReceiptID) && gatewayIdentifier(value.ToolCallID) &&
		value.ToolID == "research_gexbot_as_of" && gatewayDigest(value.RequestDigest) && value.State == "succeeded" &&
		value.Evidence.Owner == "research_gateway" && value.Evidence.RecordType == "gexbot_as_of_evidence" &&
		gatewayIdentifier(value.Evidence.RecordID) && value.Evidence.Schema == 1 && gatewayDigest(value.Evidence.RecordDigest) &&
		gatewayIdentifier(value.Executor.PrincipalID) && value.Executor.Kind == "workload" && value.Executor.Audience == "research_gateway" &&
		!value.CompletedAt.IsZero() && value.CompletedAt.Location() == time.UTC
}

func validCortexGEXBOTEvidence(value cortexGEXBOTEvidence) bool {
	if value.SchemaRevision != 1 || !gatewayIdentifier(value.EvidenceID) || !gatewayIdentifier(value.ToolCallID) ||
		value.Provider != "gexbot_classic" || !safeSymbol(value.Symbol) || !validGEXBOTCategory(value.Category) ||
		value.AsOf.IsZero() || value.AsOf.Location() != time.UTC {
		return false
	}
	if !value.Available {
		return value.ObservationID == "" && value.ObservationDigest == "" && value.ObservedAt == nil && value.AvailableAt == nil && value.Raw == nil &&
			(len(value.Metrics) == 0 || string(value.Metrics) == "{}")
	}
	return gatewayJSONObject(value.Metrics) && validGEXBOTUUID(value.ObservationID) && gatewayDigest(value.ObservationDigest) && value.ObservedAt != nil && value.AvailableAt != nil &&
		value.ObservedAt.Location() == time.UTC && value.AvailableAt.Location() == time.UTC && !value.AvailableAt.Before(*value.ObservedAt) &&
		!value.AvailableAt.After(value.AsOf) && value.Raw != nil && validGEXBOTUUID(value.Raw.BlobID) && gatewayDigest(value.Raw.ContentDigest) && value.Raw.SizeBytes >= 1 && value.Raw.SizeBytes <= 2<<20
}

func validGEXBOTUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if char != '-' {
				return false
			}
			continue
		}
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}

func gatewayJSONObject(raw json.RawMessage) bool {
	var value map[string]any
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil
}
