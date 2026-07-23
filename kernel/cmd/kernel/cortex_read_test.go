package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestCortexKernelReadUsesAllowlistAndDropsGuide(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "input-token")
	if err := os.WriteFile(tokenPath, []byte("cortex-input-test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	caller := &recordingMCPCaller{raw: []byte(`{"data":{"account_number":"123456789","cash":"42.50"},"guide":"must-not-cross"}`)}
	lab, err := newMCPReadLab(caller, "123456789", labSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	server := &server{mode: protectedMode(config.ModeReadOnly), mcpLab: lab, cortexTokenFile: tokenPath, store: newMemoryStore()}
	request := httptest.NewRequest(http.MethodPost, "/internal/v1/cortex-tools/read", strings.NewReader(
		`{"tool_call_id":"tool-1","tool_id":"kernel_portfolio","source_tool":"get_portfolio","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","arguments":{}}`,
	))
	request.Header.Set("Authorization", "Bearer cortex-input-test-token")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || caller.tool != "get_portfolio" || caller.args["account_number"] != "123456789" {
		t.Fatalf("Kernel read status=%d tool=%q args=%#v body=%s", response.Code, caller.tool, caller.args, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "must-not-cross") || strings.Contains(body, "123456789") ||
		!strings.Contains(body, `"tool_id":"kernel_portfolio"`) || !strings.Contains(body, "••••6789") {
		t.Fatalf("Kernel read bridge leaked or lost facts: %s", body)
	}
}

func TestCortexKernelReadRejectsMutationAndAccountSelection(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "input-token")
	if err := os.WriteFile(tokenPath, []byte("cortex-input-test-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	caller := &recordingMCPCaller{raw: []byte(`{"data":{},"guide":"unused"}`)}
	lab, err := newMCPReadLab(caller, "123456789", labSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	server := &server{mode: protectedMode(config.ModeReadOnly), mcpLab: lab, cortexTokenFile: tokenPath}
	for _, body := range []string{
		`{"tool_call_id":"tool-1","tool_id":"kernel_place_equity_order","source_tool":"place_equity_order","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","arguments":{}}`,
		`{"tool_call_id":"tool-1","tool_id":"kernel_portfolio","source_tool":"get_portfolio","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","arguments":{"account_number":"other"}}`,
		`{"tool_call_id":"tool-1","tool_id":"kernel_accounts","source_tool":"get_portfolio","request_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","arguments":{}}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/internal/v1/cortex-tools/read", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer cortex-input-test-token")
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || caller.tool != "" {
			t.Fatalf("unsafe request status=%d caller=%q body=%s", response.Code, caller.tool, response.Body.String())
		}
	}
}
