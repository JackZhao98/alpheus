package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/rhmcp"
)

type mcpToolContract struct {
	Schema     rhmcp.ToolSchema
	Properties map[string]json.RawMessage
	Required   []string
}

type mcpReadLab struct {
	caller    rhmcp.Caller
	accountID string
	tools     map[string]mcpToolContract
}

type mcpToolInfo struct {
	Name            string          `json:"name"`
	Category        string          `json:"category"`
	Description     string          `json:"description"`
	InputSchema     json.RawMessage `json:"input_schema"`
	ExampleArgs     map[string]any  `json:"example_args"`
	RequiresAccount bool            `json:"requires_account"`
}

func newMCPReadLab(caller rhmcp.Caller, accountID string, snapshot rhmcp.CapabilitySnapshot) (*mcpReadLab, error) {
	if caller == nil || strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("live MCP lab requires a caller and bound account")
	}
	committed := make(map[string]rhmcp.ToolSchema, len(snapshot.Tools))
	for _, tool := range snapshot.Tools {
		committed[tool.Name] = tool
	}
	tools := make(map[string]mcpToolContract, len(rhmcp.SafeQueryTools))
	for _, name := range rhmcp.SafeQueryTools {
		tool, ok := committed[name]
		if !ok {
			return nil, fmt.Errorf("safe MCP tool missing from capability snapshot")
		}
		var envelope struct {
			Type                 string                     `json:"type"`
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
			Required             []string                   `json:"required"`
		}
		decoder := json.NewDecoder(bytes.NewReader(tool.InputSchema))
		if err := decoder.Decode(&envelope); err != nil {
			return nil, fmt.Errorf("invalid safe MCP tool schema")
		}
		if envelope.Type != "object" || envelope.AdditionalProperties == nil || *envelope.AdditionalProperties {
			return nil, fmt.Errorf("unsafe MCP tool input schema")
		}
		if envelope.Properties == nil {
			envelope.Properties = map[string]json.RawMessage{}
		}
		tools[name] = mcpToolContract{Schema: tool, Properties: envelope.Properties, Required: envelope.Required}
	}
	return &mcpReadLab{caller: caller, accountID: strings.TrimSpace(accountID), tools: tools}, nil
}

func mcpExampleArgs(tool string, now time.Time) map[string]any {
	weekAgo := now.UTC().AddDate(0, 0, -7).Format(time.RFC3339)
	switch tool {
	case "get_earnings_calendar":
		return map[string]any{"days": 7}
	case "get_earnings_results":
		return map[string]any{"symbol": "AAPL"}
	case "get_equity_fundamentals", "get_equity_price_book", "get_equity_quotes", "get_equity_tradability", "get_financials":
		return map[string]any{"symbols": []string{"SPY"}}
	case "get_equity_historicals":
		return map[string]any{"symbols": []string{"SPY"}, "start_time": weekAgo}
	case "get_equity_tax_lots":
		return map[string]any{"symbol": "SPY"}
	case "get_equity_technical_indicators":
		return map[string]any{"symbol": "SPY", "type": "rsi", "interval": "day", "start_time": weekAgo}
	case "get_indexes":
		return map[string]any{"symbols": "SPX,NDX,DJI"}
	case "get_index_quotes":
		return map[string]any{"instrument_ids": []string{"INDEX_UUID"}}
	case "get_option_chains":
		return map[string]any{"underlying_symbol": "SPY"}
	case "get_option_instruments":
		return map[string]any{"chain_symbol": "SPY"}
	case "get_option_positions":
		return map[string]any{"nonzero": true}
	case "get_option_quotes":
		return map[string]any{"instrument_ids": []string{"OPTION_UUID"}}
	case "get_pnl_trade_history":
		return map[string]any{"span": "3month"}
	case "get_realized_pnl":
		return map[string]any{"span": "3month", "asset_classes": []string{"equity", "option", "crypto"}}
	case "get_watchlist_items":
		return map[string]any{"list_id": "WATCHLIST_UUID"}
	case "run_scan":
		return map[string]any{"scan_id": "SCAN_UUID"}
	case "search":
		return map[string]any{"query": "Apple", "limit": 10}
	default:
		return map[string]any{}
	}
}

func (l *mcpReadLab) catalog(now time.Time) []mcpToolInfo {
	tools := make([]mcpToolInfo, 0, len(l.tools))
	for name, contract := range l.tools {
		_, requiresAccount := contract.Properties["account_number"]
		tools = append(tools, mcpToolInfo{
			Name: name, Category: rhmcp.SafeQueryCategory(name), Description: contract.Schema.Description,
			InputSchema: contract.Schema.InputSchema, ExampleArgs: mcpExampleArgs(name, now), RequiresAccount: requiresAccount,
		})
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Category != tools[j].Category {
			return tools[i].Category < tools[j].Category
		}
		return tools[i].Name < tools[j].Name
	})
	return tools
}

func (l *mcpReadLab) boundArgs(tool string, supplied map[string]any) (map[string]any, error) {
	contract, ok := l.tools[tool]
	if !ok || !rhmcp.IsSafeQueryTool(tool) {
		return nil, fmt.Errorf("tool is not available in the live read lab")
	}
	args := make(map[string]any, len(supplied)+1)
	for key, value := range supplied {
		if _, ok := contract.Properties[key]; !ok {
			return nil, fmt.Errorf("unknown argument %q", key)
		}
		args[key] = value
	}
	if _, requiresAccount := contract.Properties["account_number"]; requiresAccount {
		if suppliedAccount, exists := args["account_number"]; exists {
			value, ok := suppliedAccount.(string)
			if !ok || (value != "$ACCOUNT" && value != l.accountID) {
				return nil, fmt.Errorf("account_number is fixed to the bound account")
			}
		}
		args["account_number"] = l.accountID
	}
	for _, required := range contract.Required {
		if _, ok := args[required]; !ok {
			return nil, fmt.Errorf("missing required argument %q", required)
		}
	}
	return args, nil
}

func (l *mcpReadLab) query(ctx context.Context, tool string, supplied map[string]any) (any, error) {
	args, err := l.boundArgs(tool, supplied)
	if err != nil {
		return nil, err
	}
	raw, err := l.caller.Call(ctx, tool, args)
	if err != nil {
		return nil, fmt.Errorf("provider query failed")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("provider result is invalid")
	}
	return sanitizeMCPValue("", value), nil
}

func sanitizeMCPValue(key string, value any) any {
	normalizedKey := strings.ToLower(key)
	switch normalizedKey {
	case "account_number", "rhs_account_number", "rhc_account_number":
		if account, ok := value.(string); ok {
			return maskedAccountID(account)
		}
		return "••••"
	}
	if strings.Contains(normalizedKey, "token") || strings.Contains(normalizedKey, "secret") || normalizedKey == "authorization" {
		return "[redacted]"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = sanitizeMCPValue(childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, child := range typed {
			out[i] = sanitizeMCPValue(key, child)
		}
		return out
	default:
		return value
	}
}

func (s *server) getMCPReadTools(w http.ResponseWriter, _ *http.Request) {
	if s.mcpLab == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live MCP lab unavailable"})
		return
	}
	tools := s.mcpLab.catalog(time.Now().UTC())
	writeJSON(w, http.StatusOK, map[string]any{
		"source": "robinhood-mcp", "account": maskedAccountID(s.mcpLab.accountID),
		"safe_tools": len(tools), "blocked_mutations": 15, "tools": tools,
	})
}

func (s *server) postMCPReadQuery(w http.ResponseWriter, r *http.Request) {
	if s.mcpLab == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "live MCP lab unavailable"})
		return
	}
	var input struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Tool = strings.TrimSpace(input.Tool)
	if input.Tool == "" || !rhmcp.IsSafeQueryTool(input.Tool) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tool is not available in the live read lab"})
		return
	}
	if input.Args == nil {
		input.Args = map[string]any{}
	}
	started := time.Now()
	result, err := s.mcpLab.query(r.Context(), input.Tool, input.Args)
	if err != nil {
		if strings.Contains(err.Error(), "argument") || strings.Contains(err.Error(), "account_number") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "MCP query failed"})
		return
	}
	if s.store != nil {
		s.store.Event("mcp_read_query", map[string]string{"tool": input.Tool, "subject": authenticatedSubject(r)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tool": input.Tool, "source": "robinhood-mcp", "account": maskedAccountID(s.mcpLab.accountID),
		"duration_ms": time.Since(started).Milliseconds(), "result": result,
	})
}
