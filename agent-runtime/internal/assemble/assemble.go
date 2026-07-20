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
	return ctx, nil
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
