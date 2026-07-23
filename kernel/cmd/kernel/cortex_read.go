package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const maxCortexKernelReadResultBytes = 64 << 10

type cortexKernelReadRequest struct {
	ToolCallID    string         `json:"tool_call_id"`
	ToolID        string         `json:"tool_id"`
	SourceTool    string         `json:"source_tool"`
	RequestDigest string         `json:"request_digest"`
	Arguments     map[string]any `json:"arguments"`
}

type cortexKernelReadObservation struct {
	SchemaRevision uint16    `json:"schema_revision"`
	ToolCallID     string    `json:"tool_call_id"`
	ToolID         string    `json:"tool_id"`
	RequestDigest  string    `json:"request_digest"`
	Provider       string    `json:"provider"`
	SourceTool     string    `json:"source_tool"`
	ResultJSON     string    `json:"result_json"`
	ObservedAt     time.Time `json:"observed_at"`
	AvailableAt    time.Time `json:"available_at"`
}

func (s *server) postCortexKernelRead(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeCortexFactBridge(w, r) {
		return
	}
	var request cortexKernelReadRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		!validCortexBridgeIdentifier(request.ToolCallID) || !validCortexBridgeIdentifier(request.ToolID) ||
		!validCortexBridgeIdentifier(request.SourceTool) || !validCortexBridgeDigest(request.RequestDigest) ||
		request.Arguments == nil || request.ToolID != cortexKernelToolID(request.SourceTool) ||
		request.SourceTool == "get_earnings_results" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Cortex Kernel read request"})
		return
	}
	if _, selectedAccount := request.Arguments["account_number"]; selectedAccount {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Cortex cannot select a brokerage account"})
		return
	}
	argumentBytes, err := json.Marshal(request.Arguments)
	if err != nil || len(argumentBytes) > 12<<10 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Cortex Kernel read arguments"})
		return
	}

	s.providerMu.RLock()
	lab := s.mcpLab
	s.providerMu.RUnlock()
	if lab == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Robinhood read provider unavailable"})
		return
	}
	observedAt := time.Now().UTC()
	value, err := lab.query(r.Context(), request.SourceTool, request.Arguments)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood read provider failed"})
		return
	}
	resultJSON, err := cortexKernelDataJSON(value)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Robinhood read provider returned invalid facts"})
		return
	}
	availableAt := time.Now().UTC()
	observation := cortexKernelReadObservation{
		SchemaRevision: 1, ToolCallID: request.ToolCallID, ToolID: request.ToolID,
		RequestDigest: request.RequestDigest, Provider: "kernel_robinhood_mcp",
		SourceTool: request.SourceTool, ResultJSON: resultJSON,
		ObservedAt: observedAt, AvailableAt: availableAt,
	}
	if s.store != nil {
		s.store.Event("cortex_kernel_read", map[string]string{"tool_call_id": request.ToolCallID, "tool_id": request.ToolID})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, observation)
}

func cortexKernelDataJSON(value any) (string, error) {
	envelope, ok := value.(map[string]any)
	if !ok || len(envelope) != 2 {
		return "", fmt.Errorf("invalid provider envelope")
	}
	data, hasData := envelope["data"]
	guide, hasGuide := envelope["guide"]
	if !hasData || !hasGuide {
		return "", fmt.Errorf("invalid provider envelope")
	}
	if _, ok := guide.(string); !ok {
		return "", fmt.Errorf("invalid provider guide")
	}
	raw, err := json.Marshal(data)
	if err != nil || len(raw) < 2 || len(raw) > maxCortexKernelReadResultBytes ||
		(raw[0] != '{' && raw[0] != '[') {
		return "", fmt.Errorf("invalid provider data")
	}
	return string(raw), nil
}

func cortexKernelToolID(sourceTool string) string {
	if sourceTool == "" {
		return ""
	}
	switch sourceTool {
	case "get_accounts":
		return "kernel_accounts"
	case "get_earnings_calendar":
		return "kernel_earnings_calendar"
	case "get_earnings_results":
		return "kernel_earnings_results"
	case "get_equity_fundamentals":
		return "kernel_equity_fundamentals"
	case "get_equity_historicals":
		return "kernel_equity_historicals"
	case "get_equity_orders":
		return "kernel_equity_orders"
	case "get_equity_positions":
		return "kernel_equity_positions"
	case "get_equity_price_book":
		return "kernel_equity_price_book"
	case "get_equity_quotes":
		return "kernel_equity_quotes"
	case "get_equity_tax_lots":
		return "kernel_equity_tax_lots"
	case "get_equity_technical_indicators":
		return "kernel_equity_technical_indicators"
	case "get_equity_tradability":
		return "kernel_equity_tradability"
	case "get_financials":
		return "kernel_financials"
	case "get_index_quotes":
		return "kernel_index_quotes"
	case "get_indexes":
		return "kernel_indexes"
	case "get_option_chains":
		return "kernel_option_chains"
	case "get_option_instruments":
		return "kernel_option_instruments"
	case "get_option_level_upgrade_info":
		return "kernel_option_level_upgrade_info"
	case "get_option_orders":
		return "kernel_option_orders"
	case "get_option_positions":
		return "kernel_option_positions"
	case "get_option_quotes":
		return "kernel_option_quotes"
	case "get_option_watchlist":
		return "kernel_option_watchlist"
	case "get_pnl_trade_history":
		return "kernel_pnl_trade_history"
	case "get_popular_watchlists":
		return "kernel_popular_watchlists"
	case "get_portfolio":
		return "kernel_portfolio"
	case "get_realized_pnl":
		return "kernel_realized_pnl"
	case "get_scanner_filter_specs":
		return "kernel_scanner_filter_specs"
	case "get_scans":
		return "kernel_scans"
	case "get_watchlist_items":
		return "kernel_watchlist_items"
	case "get_watchlists":
		return "kernel_watchlists"
	case "review_equity_order":
		return "kernel_review_equity_order"
	case "review_option_order":
		return "kernel_review_option_order"
	case "run_scan":
		return "kernel_run_scan"
	case "search":
		return "kernel_search"
	default:
		return ""
	}
}
