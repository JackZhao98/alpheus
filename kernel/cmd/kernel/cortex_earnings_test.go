package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/rhmcp"
)

func TestCortexEarningsBridgeIsNarrowAndNormalizesFacts(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "input-token")
	if err := os.WriteFile(tokenPath, []byte("cortex-input-test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	caller := &recordingMCPCaller{raw: []byte(`{"data":{"results":[{"symbol":"TSLA","year":2026,"quarter":2,"eps":{"estimate":"0.45","actual":"0.50"},"report":{"date":"2026-07-22","timing":"pm","verified":true}}]},"guide":"upstream-only text"}`)}
	lab, err := newMCPReadLab(caller, "123456789", earningsLabSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	server := &server{mode: protectedMode(config.ModeReadOnly), mcpLab: lab, cortexTokenFile: tokenPath, store: newMemoryStore()}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/cortex-tools/earnings-results", strings.NewReader(`{"tool_call_id":"tool-1","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","symbol":"TSLA"}`))
	request.Header.Set("Authorization", "Bearer cortex-input-test-token")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("earnings bridge status = %d body=%s", response.Code, response.Body.String())
	}
	if caller.tool != "get_earnings_results" || caller.args["symbol"] != "TSLA" {
		t.Fatalf("bridge called %#v with %#v", caller.tool, caller.args)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"tool_id":"kernel_earnings_results"`) || !strings.Contains(body, `"actual":"0.50"`) || strings.Contains(body, "upstream-only text") {
		t.Fatalf("bridge response was not normalized: %s", body)
	}
}

func TestCortexEarningsBridgeRejectsGenericMCPShape(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "input-token")
	if err := os.WriteFile(tokenPath, []byte("cortex-input-test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	caller := &recordingMCPCaller{raw: []byte(`{"data":{"results":[]},"guide":"unused"}`)}
	lab, err := newMCPReadLab(caller, "123456789", earningsLabSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	server := &server{mode: protectedMode(config.ModeReadOnly), mcpLab: lab, cortexTokenFile: tokenPath, store: newMemoryStore()}
	for _, body := range []string{
		`{"tool_call_id":"tool-1","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","symbol":"tsla"}`,
		`{"tool":"get_accounts","args":{}}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/internal/v1/cortex-tools/earnings-results", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer cortex-input-test-token")
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || caller.tool != "" {
			t.Fatalf("generic request status=%d caller=%q body=%s", response.Code, caller.tool, response.Body.String())
		}
	}
}

func earningsLabSnapshot() rhmcp.CapabilitySnapshot {
	snapshot := labSnapshot()
	for index := range snapshot.Tools {
		if snapshot.Tools[index].Name == "get_earnings_results" {
			snapshot.Tools[index].InputSchema = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"symbol":{"type":"string"}},"required":["symbol"]}`)
			return snapshot
		}
	}
	panic("earnings tool absent from test capability snapshot")
}
