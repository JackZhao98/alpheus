// Package assemble: context assembly is CODE, not prompt. Whatever the prompt
// says, every run receives the same structured picture: limits, account and
// positions plus day.live/day.shadow ledgers, blackboard, and lessons. The
// state response is passed through as raw JSON. This is what makes sessions
// disposable — state lives outside them. This is also where the marketdata
// facade gets injected once the kernel grows /market/* endpoints (PLAN M8).
package assemble

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"alpheus/agentruntime/internal/roles"
)

type Client struct {
	Kernel string
	Token  string
	HTTP   *http.Client
}

// AssembleQuery adds the minimum live market context needed by the read-only
// Scout query path. The query never receives broker credentials and cannot
// submit an operation.
func (c *Client) AssembleQuery(role roles.Role, symbol, query string) (map[string]json.RawMessage, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	query = strings.TrimSpace(query)
	if !safeSymbol(symbol) || query == "" {
		return nil, fmt.Errorf("symbol and query are required")
	}
	ctx, err := c.Assemble(role)
	if err != nil {
		return nil, err
	}
	queryJSON, _ := json.Marshal(query)
	symbolJSON, _ := json.Marshal(symbol)
	ctx["user_query"] = queryJSON
	ctx["symbol"] = symbolJSON
	bars, err := c.getJSON("/market/bars/" + url.PathEscape(symbol) + "?days=30")
	if err != nil {
		return nil, err
	}
	ctx["market_bars"] = bars
	quote, err := c.getJSON("/market/quote/" + url.PathEscape(symbol))
	if err != nil {
		ctx["market_quote"] = json.RawMessage(`{"available":false,"reason":"unavailable_or_stale"}`)
	} else {
		ctx["market_quote"] = quote
	}
	c.addQueryEnrichment(ctx, role, symbol)
	return ctx, nil
}

type queryEnrichment struct {
	key  string
	tool string
	args map[string]any
}

func (c *Client) addQueryEnrichment(ctx map[string]json.RawMessage, role roles.Role, symbol string) {
	wanted := make(map[string]bool, len(role.InjectedContext))
	for _, key := range role.InjectedContext {
		wanted[key] = true
	}
	indicatorStart := time.Now().UTC().AddDate(0, 0, -180).Format(time.RFC3339)
	all := []queryEnrichment{
		{key: "equity_fundamentals", tool: "get_equity_fundamentals", args: map[string]any{"symbols": []string{symbol}}},
		{key: "company_financials", tool: "get_financials", args: map[string]any{"symbols": []string{symbol}, "period": "quarterly", "limit": 4}},
		{key: "earnings_results", tool: "get_earnings_results", args: map[string]any{"symbol": symbol}},
		{key: "technical_rsi", tool: "get_equity_technical_indicators", args: map[string]any{"symbol": symbol, "type": "rsi", "interval": "day", "start_time": indicatorStart, "output": "latest"}},
		{key: "technical_macd", tool: "get_equity_technical_indicators", args: map[string]any{"symbol": symbol, "type": "macd", "interval": "day", "start_time": indicatorStart, "output": "latest"}},
		{key: "technical_atr", tool: "get_equity_technical_indicators", args: map[string]any{"symbol": symbol, "type": "atr", "interval": "day", "start_time": indicatorStart, "output": "latest"}},
	}
	tasks := make([]queryEnrichment, 0, len(all))
	for _, task := range all {
		if wanted[task.key] {
			tasks = append(tasks, task)
		}
	}
	type result struct {
		key  string
		tool string
		raw  json.RawMessage
		err  error
	}
	results := make(chan result, len(tasks))
	var wait sync.WaitGroup
	for _, task := range tasks {
		wait.Add(1)
		go func(task queryEnrichment) {
			defer wait.Done()
			raw, err := c.mcpReadQuery(task.tool, task.args)
			results <- result{key: task.key, tool: task.tool, raw: raw, err: err}
		}(task)
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			fallback, _ := json.Marshal(map[string]any{
				"available": false, "source": "robinhood-mcp", "tool": result.tool,
			})
			ctx[result.key] = fallback
			continue
		}
		ctx[result.key] = result.raw
	}
}

func (c *Client) mcpReadQuery(tool string, args map[string]any) (json.RawMessage, error) {
	body, err := json.Marshal(map[string]any{"tool": tool, "args": args})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.Kernel+"/mcp/read-query", strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.Authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("MCP %s: HTTP %d", tool, resp.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func safeSymbol(symbol string) bool {
	if len(symbol) == 0 || len(symbol) > 16 {
		return false
	}
	for _, char := range symbol {
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func New(kernelURL, token string) *Client {
	return &Client{Kernel: kernelURL, Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) Authorize(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func (c *Client) getJSON(path string) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodGet, c.Kernel+path, nil)
	if err != nil {
		return nil, err
	}
	c.Authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return raw, nil
}

// Assemble builds the full context for one session.
func (c *Client) Assemble(role roles.Role) (map[string]json.RawMessage, error) {
	today := time.Now().Format("2006-01-02")
	all := map[string]string{
		"limits":     "/limits",
		"state":      "/state",
		"blackboard": "/blackboard/" + today,
		"lessons":    "/lessons?limit=5",
	}
	wants := map[string]bool{}
	for _, k := range role.InjectedContext {
		wants[k] = true
	}
	ctx := map[string]json.RawMessage{"today": json.RawMessage(`"` + today + `"`)}
	for key, path := range all {
		if len(wants) > 0 && !wants[key] {
			continue
		}
		raw, err := c.getJSON(path)
		if err != nil {
			return nil, err
		}
		ctx[key] = raw
	}
	// TODO: unread inbox items addressed to this role
	// TODO: watchlist memory filtered by applicable_when
	return ctx, nil
}
