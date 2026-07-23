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

type cortexGEXBOTLiveAuthorization struct {
	ToolCallID    string `json:"tool_call_id"`
	ToolID        string `json:"tool_id"`
	RequestDigest string `json:"request_digest"`
	Symbol        string `json:"symbol"`
	Category      string `json:"category"`
}

type cortexGEXBOTLiveResult struct {
	Receipt  cortexGEXBOTLiveReceipt  `json:"receipt"`
	Evidence cortexGEXBOTLiveEvidence `json:"evidence"`
}

type cortexGEXBOTLiveReceipt struct {
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

type cortexGEXBOTLiveEvidence struct {
	SchemaRevision    uint16          `json:"schema_revision"`
	EvidenceID        string          `json:"evidence_id"`
	ToolCallID        string          `json:"tool_call_id"`
	Provider          string          `json:"provider"`
	Symbol            string          `json:"symbol"`
	Category          string          `json:"category"`
	ObservationID     string          `json:"observation_id"`
	ObservationDigest string          `json:"observation_digest"`
	SourceTimestamp   time.Time       `json:"source_timestamp"`
	ObservedAt        time.Time       `json:"observed_at"`
	FetchedAt         time.Time       `json:"fetched_at"`
	AvailableAt       time.Time       `json:"available_at"`
	Metrics           json.RawMessage `json:"metrics"`
	Raw               cortexGEXRaw    `json:"raw"`
}

func (g *gateway) cortexGEXBOTLiveTool(w http.ResponseWriter, r *http.Request) {
	if g == nil || g.db == nil || g.gexbotURL == "" || g.gexbotToken == "" || g.moodyBlues == nil ||
		!g.moodyBlues.supports("gexbot_classic", "live") || !g.validCortexToken(r) {
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
	auth, err := g.authorizeCortexGEXBOTLive(r.Context(), input.ToolCallID)
	if err != nil {
		log.Printf("Cortex GEXBOT live authorization failed: %v", err)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "tool authorization unavailable"})
		return
	}
	if existing, found, err := g.loadCortexGEXBOTLiveReceipt(r.Context(), auth.ToolCallID); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
		return
	} else if found {
		if !validCortexGEXBOTLiveResult(existing, auth) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	observation, err := g.fetchGEXBOTLive(r.Context(), auth)
	if err != nil {
		log.Printf("Cortex GEXBOT live provider failed for %s: %v", auth.ToolCallID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "GEXBOT live source unavailable"})
		return
	}
	result, err := g.recordCortexGEXBOTLiveReceipt(r.Context(), auth.ToolCallID, observation)
	if err != nil || !validCortexGEXBOTLiveResult(result, auth) {
		log.Printf("Cortex GEXBOT live receipt failed for %s: %v", auth.ToolCallID, err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool receipt unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (g *gateway) authorizeCortexGEXBOTLive(ctx context.Context, toolCallID string) (cortexGEXBOTLiveAuthorization, error) {
	var raw []byte
	err := g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.get_cortex_gexbot_live_authorization($1)::TEXT`, toolCallID).Scan(&raw)
	})
	if err != nil {
		return cortexGEXBOTLiveAuthorization{}, err
	}
	var auth cortexGEXBOTLiveAuthorization
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&auth) != nil || decoder.Decode(&struct{}{}) != io.EOF || auth.ToolCallID != toolCallID ||
		auth.ToolID != "market_gexbot_live" || !gatewayDigest(auth.RequestDigest) || auth.Symbol != "SPX" ||
		!validGEXBOTCategory(auth.Category) {
		return cortexGEXBOTLiveAuthorization{}, fmt.Errorf("invalid GEXBOT live authorization")
	}
	return auth, nil
}

func (g *gateway) loadCortexGEXBOTLiveReceipt(ctx context.Context, toolCallID string) (cortexGEXBOTLiveResult, bool, error) {
	var raw []byte
	err := g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.get_research_gexbot_live_receipt($1)::TEXT`, toolCallID).Scan(&raw)
	})
	if err != nil {
		return cortexGEXBOTLiveResult{}, false, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return cortexGEXBOTLiveResult{}, false, nil
	}
	result, err := decodeCortexGEXBOTLiveResult(raw)
	return result, err == nil, err
}

func (g *gateway) fetchGEXBOTLive(ctx context.Context, auth cortexGEXBOTLiveAuthorization) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]string{"symbol": auth.Symbol, "category": auth.Category})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.gexbotURL+"/v1/live", bytes.NewReader(body))
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
		return nil, fmt.Errorf("provider live response unavailable")
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || value["payload"] != nil || value["available"] != nil {
		return nil, fmt.Errorf("provider live response invalid")
	}
	value["available"] = json.RawMessage("true")
	normalized, _ := json.Marshal(value)
	return compactGEXBOTObservation(normalized)
}

func (g *gateway) recordCortexGEXBOTLiveReceipt(ctx context.Context, toolCallID string, observation json.RawMessage) (cortexGEXBOTLiveResult, error) {
	var raw []byte
	err := g.withResearchRole(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT agent_control.record_research_gexbot_live_receipt($1,$2::JSONB)::TEXT`,
			toolCallID, string(observation)).Scan(&raw)
	})
	if err != nil {
		return cortexGEXBOTLiveResult{}, err
	}
	return decodeCortexGEXBOTLiveResult(raw)
}

func decodeCortexGEXBOTLiveResult(raw []byte) (cortexGEXBOTLiveResult, error) {
	var result cortexGEXBOTLiveResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		!validCortexGEXBOTLiveReceipt(result.Receipt) || !validCortexGEXBOTLiveEvidence(result.Evidence) ||
		result.Receipt.ToolCallID != result.Evidence.ToolCallID || result.Receipt.Evidence.RecordID != result.Evidence.EvidenceID {
		return cortexGEXBOTLiveResult{}, fmt.Errorf("invalid GEXBOT live result")
	}
	return result, nil
}

func validCortexGEXBOTLiveResult(result cortexGEXBOTLiveResult, auth cortexGEXBOTLiveAuthorization) bool {
	return validCortexGEXBOTLiveReceipt(result.Receipt) && validCortexGEXBOTLiveEvidence(result.Evidence) &&
		result.Receipt.ToolCallID == auth.ToolCallID && result.Receipt.RequestDigest == auth.RequestDigest &&
		result.Evidence.Symbol == auth.Symbol && result.Evidence.Category == auth.Category
}

func validCortexGEXBOTLiveReceipt(value cortexGEXBOTLiveReceipt) bool {
	return value.SchemaRevision == 1 && gatewayIdentifier(value.ReceiptID) && gatewayIdentifier(value.ToolCallID) &&
		value.ToolID == "market_gexbot_live" && gatewayDigest(value.RequestDigest) && value.State == "succeeded" &&
		value.Evidence.Owner == "research_gateway" && value.Evidence.RecordType == "gexbot_live_evidence" &&
		gatewayIdentifier(value.Evidence.RecordID) && value.Evidence.Schema == 1 && gatewayDigest(value.Evidence.RecordDigest) &&
		gatewayIdentifier(value.Executor.PrincipalID) && value.Executor.Kind == "workload" && value.Executor.Audience == "research_gateway" &&
		!value.CompletedAt.IsZero() && value.CompletedAt.Location() == time.UTC
}

func validCortexGEXBOTLiveEvidence(value cortexGEXBOTLiveEvidence) bool {
	return value.SchemaRevision == 1 && gatewayIdentifier(value.EvidenceID) && gatewayIdentifier(value.ToolCallID) &&
		value.Provider == "gexbot_classic" && value.Symbol == "SPX" && validGEXBOTCategory(value.Category) &&
		validGEXBOTUUID(value.ObservationID) && gatewayDigest(value.ObservationDigest) &&
		!value.SourceTimestamp.IsZero() && value.SourceTimestamp.Location() == time.UTC &&
		!value.ObservedAt.IsZero() && value.ObservedAt.Location() == time.UTC &&
		!value.FetchedAt.IsZero() && value.FetchedAt.Location() == time.UTC &&
		!value.AvailableAt.IsZero() && value.AvailableAt.Location() == time.UTC &&
		!value.FetchedAt.Before(value.ObservedAt) && !value.AvailableAt.Before(value.FetchedAt) &&
		gatewayJSONObject(value.Metrics) && validGEXBOTUUID(value.Raw.BlobID) &&
		gatewayDigest(value.Raw.ContentDigest) && value.Raw.SizeBytes >= 1 && value.Raw.SizeBytes <= 2<<20
}
