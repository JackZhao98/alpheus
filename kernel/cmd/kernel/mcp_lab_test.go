package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/rhmcp"
)

type recordingMCPCaller struct {
	tool string
	args map[string]any
	raw  json.RawMessage
	err  error
}

func (c *recordingMCPCaller) Call(_ context.Context, tool string, args map[string]any) (json.RawMessage, error) {
	c.tool = tool
	c.args = make(map[string]any, len(args))
	for key, value := range args {
		c.args[key] = value
	}
	return c.raw, c.err
}

func labSnapshot() rhmcp.CapabilitySnapshot {
	tools := make([]rhmcp.ToolSchema, 0, len(rhmcp.SafeQueryTools))
	for _, name := range rhmcp.SafeQueryTools {
		schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
		if name == "get_portfolio" {
			schema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"account_number":{"type":"string"}},"required":["account_number"]}`)
		}
		tools = append(tools, rhmcp.ToolSchema{Name: name, Description: name + " description", InputSchema: schema})
	}
	return rhmcp.CapabilitySnapshot{
		Version: "test-v1", Endpoint: rhmcp.DefaultEndpoint, GeneratedAt: time.Now().UTC(), Tools: tools,
	}
}

func TestMCPReadLabCatalogAndBoundQuery(t *testing.T) {
	caller := &recordingMCPCaller{raw: json.RawMessage(`{"data":{"account_number":"123456789","cash":"42.50","access_token":"never-show"}}`)}
	lab, err := newMCPReadLab(caller, "123456789", labSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(lab.catalog(time.Now())); got != 34 {
		t.Fatalf("catalog count = %d", got)
	}
	result, err := lab.query(context.Background(), "get_portfolio", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if caller.tool != "get_portfolio" || caller.args["account_number"] != "123456789" {
		t.Fatalf("bound call = %q %#v", caller.tool, caller.args)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "123456789") || strings.Contains(string(raw), "never-show") {
		t.Fatalf("sensitive value leaked: %s", raw)
	}
	if !strings.Contains(string(raw), "6789") || !strings.Contains(string(raw), "[redacted]") {
		t.Fatalf("sanitized result missing expected markers: %s", raw)
	}
}

func TestMCPReadLabRejectsMutationsAndAccountOverride(t *testing.T) {
	caller := &recordingMCPCaller{raw: json.RawMessage(`{"data":{}}`)}
	lab, err := newMCPReadLab(caller, "123456789", labSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lab.query(context.Background(), "place_equity_order", map[string]any{}); err == nil {
		t.Fatal("mutation tool reached the caller")
	}
	if _, err := lab.query(context.Background(), "get_portfolio", map[string]any{"account_number": "other"}); err == nil {
		t.Fatal("account override reached the caller")
	}
	if caller.tool != "" {
		t.Fatalf("caller invoked after rejected query: %s", caller.tool)
	}
}

func TestMCPReadLabRoutesRemainReadOnlyAndAuthenticated(t *testing.T) {
	caller := &recordingMCPCaller{raw: json.RawMessage(`{"data":{"cash":"42.50"}}`)}
	lab, err := newMCPReadLab(caller, "123456789", labSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	s := &server{mode: protectedMode(config.ModeReadOnly), mcpLab: lab, store: newMemoryStore()}
	handler := s.routes()
	if response := routeRequest(handler, http.MethodGet, "/mcp/read-tools", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated catalog = %d", response.Code)
	}
	response := routeRequest(handler, http.MethodPost, "/mcp/read-query", `{"tool":"get_portfolio","args":{}}`, "kernel-secret")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"source":"robinhood-mcp"`) {
		t.Fatalf("safe read query = %d %s", response.Code, response.Body.String())
	}
	response = routeRequest(handler, http.MethodPost, "/mcp/read-query", `{"tool":"place_equity_order","args":{}}`, "kernel-secret")
	if response.Code != http.StatusBadRequest || caller.tool != "get_portfolio" {
		t.Fatalf("mutation query = %d caller=%s", response.Code, caller.tool)
	}
	response = routeRequest(handler, http.MethodPost, "/mcp/read-query", `{"tool":"get_portfolio","args":{"unexpected":true}}`, "kernel-secret")
	if response.Code != http.StatusBadRequest || caller.tool != "get_portfolio" {
		t.Fatalf("unknown argument = %d caller=%s", response.Code, caller.tool)
	}
	response = routeRequest(handler, http.MethodPost, "/mcp/read-query", `{"tool":`, "kernel-secret")
	if response.Code != http.StatusBadRequest || caller.tool != "get_portfolio" {
		t.Fatalf("malformed query = %d caller=%s", response.Code, caller.tool)
	}
	oversized := `{"tool":"get_portfolio","args":{"padding":"` + strings.Repeat("x", int(maxJSONBodyBytes)) + `"}}`
	response = routeRequest(handler, http.MethodPost, "/mcp/read-query", oversized, "kernel-secret")
	if response.Code != http.StatusRequestEntityTooLarge || caller.tool != "get_portfolio" {
		t.Fatalf("oversized query = %d caller=%s", response.Code, caller.tool)
	}
	response = routeRequest(handler, http.MethodPost, "/operations", `{}`, "kernel-secret")
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("read-only operation route = %d", response.Code)
	}
}
