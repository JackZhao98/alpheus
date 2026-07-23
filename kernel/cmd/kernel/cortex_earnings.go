package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// cortexEarningsBridgeRequest is intentionally smaller than the Robinhood MCP
// schema. The caller cannot select an MCP method, an account, or a generic set
// of arguments; it can ask for one already-authorized symbol only.
type cortexEarningsBridgeRequest struct {
	ToolCallID    string `json:"tool_call_id"`
	RequestDigest string `json:"request_digest"`
	Symbol        string `json:"symbol"`
}

// cortexEarningsBridgeResponse is a normalized Kernel fact. It excludes the
// upstream guide and all raw MCP framing, which must not become model context.
type cortexEarningsBridgeResponse struct {
	SchemaRevision uint16                     `json:"schema_revision"`
	ToolCallID     string                     `json:"tool_call_id"`
	ToolID         string                     `json:"tool_id"`
	RequestDigest  string                     `json:"request_digest"`
	Provider       string                     `json:"provider"`
	Symbol         string                     `json:"symbol"`
	Found          bool                       `json:"found"`
	Results        []cortexEarningsResultItem `json:"results"`
	ObservedAt     time.Time                  `json:"observed_at"`
	AvailableAt    time.Time                  `json:"available_at"`
}

type cortexEarningsResultItem struct {
	Symbol  string                    `json:"symbol"`
	Year    int                       `json:"year"`
	Quarter int                       `json:"quarter"`
	EPS     cortexEarningsEPS         `json:"eps"`
	Report  *cortexEarningsReportTime `json:"report"`
}

type cortexEarningsEPS struct {
	Estimate *string `json:"estimate"`
	Actual   *string `json:"actual"`
}

type cortexEarningsReportTime struct {
	Date     *string `json:"date"`
	Timing   *string `json:"timing"`
	Verified bool    `json:"verified"`
}

type robinhoodEarningsResponse struct {
	Data  *robinhoodEarningsData `json:"data"`
	Guide string                 `json:"guide"`
}

type robinhoodEarningsData struct {
	NotFound []string                    `json:"not_found"`
	Results  []*cortexEarningsResultItem `json:"results"`
}

func (s *server) postCortexEarningsResults(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeCortexFactBridge(w, r) {
		return
	}
	var request cortexEarningsBridgeRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		!validCortexBridgeIdentifier(request.ToolCallID) || !validCortexBridgeDigest(request.RequestDigest) ||
		request.Symbol != strings.ToUpper(strings.TrimSpace(request.Symbol)) || !validAgentQuerySymbol(request.Symbol) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Cortex earnings request"})
		return
	}

	s.providerMu.RLock()
	lab := s.mcpLab
	s.providerMu.RUnlock()
	if lab == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Robinhood earnings provider unavailable"})
		return
	}
	observedAt := time.Now().UTC()
	result, err := lab.query(r.Context(), "get_earnings_results", map[string]any{"symbol": request.Symbol})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood earnings provider failed"})
		return
	}
	normalized, err := normalizeCortexEarningsResult(result, request.Symbol)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood earnings provider returned invalid facts"})
		return
	}
	normalized.SchemaRevision = 1
	normalized.ToolCallID = request.ToolCallID
	normalized.ToolID = "kernel_earnings_results"
	normalized.RequestDigest = request.RequestDigest
	normalized.Provider = "kernel_robinhood_mcp"
	normalized.ObservedAt = observedAt
	normalized.AvailableAt = time.Now().UTC()
	if normalized.AvailableAt.Before(normalized.ObservedAt) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Kernel clock invalid"})
		return
	}
	if s.store != nil {
		s.store.Event("cortex_kernel_earnings_results", map[string]string{"tool_call_id": request.ToolCallID, "symbol": request.Symbol})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, normalized)
}

func (s *server) authorizeCortexFactBridge(w http.ResponseWriter, request *http.Request) bool {
	token, err := s.cortexToken()
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Cortex fact bridge unavailable"})
		return false
	}
	expected := []byte("Bearer " + token)
	provided := request.Header.Values("Authorization")
	if len(provided) != 1 || len(provided[0]) != len(expected) || subtle.ConstantTimeCompare([]byte(provided[0]), expected) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return false
	}
	return true
}

func normalizeCortexEarningsResult(value any, symbol string) (cortexEarningsBridgeResponse, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return cortexEarningsBridgeResponse{}, err
	}
	var provider robinhoodEarningsResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&provider) != nil || decoder.Decode(&struct{}{}) != io.EOF || provider.Data == nil ||
		len(provider.Data.NotFound) > 1 || len(provider.Data.Results) > 8 {
		return cortexEarningsBridgeResponse{}, fmt.Errorf("invalid earnings response shape")
	}
	normalized := cortexEarningsBridgeResponse{Symbol: symbol, Found: len(provider.Data.NotFound) == 0, Results: make([]cortexEarningsResultItem, 0, len(provider.Data.Results))}
	if !normalized.Found {
		if len(provider.Data.NotFound) != 1 || provider.Data.NotFound[0] != symbol || len(provider.Data.Results) != 0 {
			return cortexEarningsBridgeResponse{}, fmt.Errorf("invalid earnings not_found result")
		}
		return normalized, nil
	}
	for _, item := range provider.Data.Results {
		if item == nil || item.Symbol != symbol || item.Year < 1900 || item.Year > 2200 || item.Quarter < 1 || item.Quarter > 4 ||
			!validNullableEarningsNumber(item.EPS.Estimate) || !validNullableEarningsNumber(item.EPS.Actual) || !validEarningsReport(item.Report) {
			return cortexEarningsBridgeResponse{}, fmt.Errorf("invalid earnings result item")
		}
		normalized.Results = append(normalized.Results, *item)
	}
	return normalized, nil
}

func validNullableEarningsNumber(value *string) bool {
	if value == nil {
		return true
	}
	return len(*value) > 0 && len(*value) <= 64 && strings.TrimSpace(*value) == *value
}

func validEarningsReport(report *cortexEarningsReportTime) bool {
	if report == nil {
		return true
	}
	if report.Date != nil {
		if len(*report.Date) != len("2006-01-02") || strings.TrimSpace(*report.Date) != *report.Date {
			return false
		}
		if _, err := time.Parse("2006-01-02", *report.Date); err != nil {
			return false
		}
	}
	return report.Timing == nil || *report.Timing == "am" || *report.Timing == "pm"
}

func validCortexBridgeIdentifier(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 200 {
		return false
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

func validCortexBridgeDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= '0' && char <= '9' || char >= 'a' && char <= 'f') {
			return false
		}
	}
	return true
}
