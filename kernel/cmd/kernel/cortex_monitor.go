package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

// cortexMonitorQuoteRequest cannot select a provider, account, tool, or effect.
// It asks Kernel for one fresh normalized market fact bound to a persisted
// Cortex Trigger.
type cortexMonitorQuoteRequest struct {
	TriggerID string `json:"trigger_id"`
	Symbol    string `json:"symbol"`
}

type cortexMonitorQuoteResponse struct {
	SchemaRevision uint16    `json:"schema_revision"`
	TriggerID      string    `json:"trigger_id"`
	Provider       string    `json:"provider"`
	Symbol         string    `json:"symbol"`
	Bid            string    `json:"bid"`
	Ask            string    `json:"ask"`
	Mid            string    `json:"mid"`
	ObservedAt     time.Time `json:"observed_at"`
	AvailableAt    time.Time `json:"available_at"`
}

func (s *server) postCortexMonitorQuote(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if !s.authorizeCortexFactBridge(writer, request) {
		return
	}
	var command cortexMonitorQuoteRequest
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		!validCortexBridgeIdentifier(command.TriggerID) ||
		command.Symbol != strings.ToUpper(strings.TrimSpace(command.Symbol)) ||
		!validAgentQuerySymbol(command.Symbol) {
		writeJSON(writer, http.StatusBadRequest,
			map[string]string{"error": "invalid Cortex monitor quote request"})
		return
	}
	provider := s.marketProvider()
	if provider == nil {
		writeJSON(writer, http.StatusServiceUnavailable,
			map[string]string{"error": "market data unavailable"})
		return
	}
	now := time.Now().UTC()
	quote, err := provider.Quote(request.Context(), command.Symbol)
	if err != nil || quote.Symbol != command.Symbol ||
		!quote.Usable(s.limits.QuoteMaxAgeSec, now) {
		writeJSON(writer, http.StatusBadGateway,
			map[string]string{"error": "market data unavailable"})
		return
	}
	availableAt := time.Now().UTC()
	writer.Header().Set("Cache-Control", "no-store")
	writeJSON(writer, http.StatusOK, cortexMonitorQuoteResponse{
		SchemaRevision: 1,
		TriggerID:      command.TriggerID,
		Provider:       quote.Source,
		Symbol:         quote.Symbol,
		Bid:            quote.Bid.String(),
		Ask:            quote.Ask.String(),
		Mid:            quote.Mid().String(),
		ObservedAt:     quote.AsOf,
		AvailableAt:    availableAt,
	})
}
