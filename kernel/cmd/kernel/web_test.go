package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
)

func TestCockpitIsReadOnlyAndHardened(t *testing.T) {
	s := &server{mode: protectedMode(config.ModeReadOnly), store: newMemoryStore()}
	handler := s.routes()

	page := routeRequest(handler, http.MethodGet, "/", "", "")
	if page.Code != http.StatusOK {
		t.Fatalf("page status=%d body=%s", page.Code, page.Body.String())
	}
	csp := page.Header().Get("Content-Security-Policy")
	if csp == "" || strings.Contains(csp, "unsafe-inline") || !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("unsafe CSP %q", csp)
	}
	if strings.Contains(page.Body.String(), "<script>") {
		t.Fatal("cockpit contains an inline script")
	}
	for _, required := range []string{"query-form", "Option chain", "Provider status", "mcp-form", "LIVE MCP TOOL LAB", "34 SAFE / 15 BLOCKED", "provider-cash", "live-pnl", "shadow-pnl", "live-streak", "shadow-streak", "control-actions hidden", "admin-auth-form", "pending-list", "warning-list", "halt-form"} {
		if !strings.Contains(page.Body.String(), required) {
			t.Fatalf("cockpit query lab missing %q", required)
		}
	}
	script := routeRequest(handler, http.MethodGet, "/assets/cockpit.js", "", "")
	if script.Code != http.StatusOK {
		t.Fatalf("script status=%d", script.Code)
	}
	for _, forbidden := range []string{"innerHTML", "localStorage", "sessionStorage", "document.cookie", "indexedDB"} {
		if strings.Contains(script.Body.String(), forbidden) {
			t.Fatalf("cockpit script contains forbidden browser API %q", forbidden)
		}
	}
	if !strings.Contains(script.Body.String(), `method:"POST"`) || !strings.Contains(script.Body.String(), "/mcp/read-query") {
		t.Fatal("read-only cockpit is missing the allowlisted MCP query POST")
	}
	for _, safePath := range []string{"/market/quote/", "/market/bars/", "/market/expirations/", "/market/chain/", "/provider/status"} {
		if !strings.Contains(script.Body.String(), safePath) {
			t.Fatalf("query lab missing safe path %q", safePath)
		}
	}
	for _, breakerFact := range []string{"realized_pnl", "daily_loss_limit", "consecutive_loss_days"} {
		if !strings.Contains(script.Body.String(), breakerFact) {
			t.Fatalf("cockpit missing breaker fact %q", breakerFact)
		}
	}
	for _, controlContract := range []string{"/auth/capabilities", "/control/warnings", "/review", "/halt", "/breaker/resume", "approved_price_cap", "derived_max_risk", "event_id"} {
		if !strings.Contains(script.Body.String(), controlContract) {
			t.Fatalf("cockpit missing M7 control contract %q", controlContract)
		}
	}
	if strings.Contains(script.Body.String(), "reviewer:") {
		t.Fatal("cockpit sends a client-controlled reviewer")
	}
	for _, forbidden := range []string{"CallTool", "tool_name", "place_", "cancel_", "remove_", "update_"} {
		if strings.Contains(script.Body.String(), forbidden) {
			t.Fatalf("query lab exposes forbidden MCP surface %q", forbidden)
		}
	}

	if response := routeRequest(handler, http.MethodGet, "/state", "", ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("state without token status=%d", response.Code)
	}
	if response := routeRequest(handler, http.MethodGet, "/not-a-cockpit-route", "", ""); response.Code != http.StatusNotFound {
		t.Fatalf("unknown route status=%d", response.Code)
	}
}

func TestOperationListPaginationAndInputValidation(t *testing.T) {
	const (
		id1 = "11111111-1111-4111-8111-111111111111"
		id2 = "22222222-2222-4222-8222-222222222222"
		id3 = "33333333-3333-4333-8333-333333333333"
	)
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	st := newMemoryStore()
	st.operationRows[id1] = store.OperationRow{ID: id1, TS: base.Add(3 * time.Minute), Status: "executed", Class: "B", Proposer: "scout", Payload: json.RawMessage(`{"action":"open","symbol":"SPY"}`)}
	st.operationRows[id2] = store.OperationRow{ID: id2, TS: base.Add(2 * time.Minute), Status: "pending_review", Class: "C", Proposer: "scout", Payload: json.RawMessage(`{"action":"open","symbol":"QQQ"}`)}
	st.operationRows[id3] = store.OperationRow{ID: id3, TS: base.Add(time.Minute), Status: "rejected", Class: "R", Proposer: "<img src=x onerror=alert(1)>", Payload: json.RawMessage(`{"action":"close","symbol":"BAD"}`)}
	s := &server{store: st}
	handler := s.routes()

	first := routeRequest(handler, http.MethodGet, "/operations?limit=2", "", "")
	if first.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}
	var page struct {
		Operations []store.OperationRow `json:"operations"`
		Next       string               `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Operations) != 2 || page.Operations[0].ID != id1 || page.Operations[1].ID != id2 || page.Next == "" {
		t.Fatalf("first page=%+v", page)
	}

	second := routeRequest(handler, http.MethodGet, "/operations?limit=2&cursor="+page.Next, "", "")
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), id3) {
		t.Fatalf("second page status=%d body=%s", second.Code, second.Body.String())
	}
	if strings.Contains(second.Body.String(), "<img") {
		t.Fatalf("operation text was not JSON escaped: %s", second.Body.String())
	}

	for _, target := range []string{
		"/operations?limit=0",
		"/operations?limit=nope",
		"/operations?status=banana",
		"/operations?cursor=not-base64!",
		"/operations?limit=1&limit=2",
		"/operations?unknown=value",
	} {
		response := routeRequest(handler, http.MethodGet, target, "", "")
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", target, response.Code, response.Body.String())
		}
	}
}

func TestOperationListLimitClampsToOneHundred(t *testing.T) {
	status, limit, cursor, err := parseOperationPage(routeRequestInput("/operations?limit=1000000"))
	if err != nil || status != "" || limit != maxOperationPageSize || cursor != nil {
		t.Fatalf("status=%q limit=%d cursor=%v err=%v", status, limit, cursor, err)
	}
}

func routeRequestInput(target string) *http.Request {
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		panic(err)
	}
	return request
}
