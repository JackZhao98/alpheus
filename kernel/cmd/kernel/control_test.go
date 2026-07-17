package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

func TestNormalizeConsoleOrigin(t *testing.T) {
	for _, valid := range []struct{ input, want string }{
		{"http://localhost:8100", "http://localhost:8100"},
		{"HTTPS://EXAMPLE.COM:443/", "https://example.com:443"},
	} {
		got, err := normalizeConsoleOrigin(valid.input)
		if err != nil || got != valid.want {
			t.Fatalf("origin %q = %q, %v; want %q", valid.input, got, err, valid.want)
		}
	}
	for _, invalid := range []string{"", "null", "ftp://example.com", "https://example.com/path", "https://user@example.com", "https://example.com?x=1"} {
		if _, err := normalizeConsoleOrigin(invalid); err == nil {
			t.Fatalf("invalid origin %q accepted", invalid)
		}
	}
}

func TestAdminControlsRequireTokenAndExactOrigin(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	st := newMemoryStore()
	if err := st.InsertOperation(id, "scout", "C", "pending_review", risk.Operation{Action: "open"}, risk.Verdict{}, nil); err != nil {
		t.Fatal(err)
	}
	s := &server{
		mode: protectedMode(config.ModeShadow), store: st, broker: newFake("300"),
		limits: dualLedgerLimits(), consoleOrigin: "https://cockpit.example",
	}
	handler := s.routes()

	request := func(token, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/operations/"+id+"/review",
			bytes.NewBufferString(`{"verdict":"rejected"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	if response := request("", "https://cockpit.example"); response.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request("runtime-secret", "https://cockpit.example"); response.Code != http.StatusUnauthorized {
		t.Fatalf("runtime token status=%d body=%s", response.Code, response.Body.String())
	}
	for _, origin := range []string{"", "https://evil.example", "null"} {
		if response := request("admin-secret", origin); response.Code != http.StatusForbidden {
			t.Fatalf("origin=%q status=%d body=%s", origin, response.Code, response.Body.String())
		}
		row, _ := st.GetOperation(id)
		if row.Status != "pending_review" {
			t.Fatalf("origin=%q changed operation to %s", origin, row.Status)
		}
	}
	if response := request("admin-secret", "https://cockpit.example"); response.Code != http.StatusOK {
		t.Fatalf("valid control status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestControlCapabilitiesAndWarningsAreReadOnly(t *testing.T) {
	st := newMemoryStore()
	now := time.Now().UTC()
	st.attempts["attempt-1"] = store.ExecutionAttempt{
		ID: "attempt-1", OperationID: "op-1", State: "unknown", CreatedAt: now,
		OpenReservationID: "reservation-1", LastError: "broker result uncertain",
	}
	st.openReservations["reservation-1"] = store.OpenReservation{
		ID: "reservation-1", OperationID: "op-1", Ledger: "live", Symbol: "SPY",
		OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"),
		ResourceState: "held", CreatedAt: now,
	}
	s := &server{mode: protectedMode(config.ModeShadow), store: st, attemptStale: time.Second}
	handler := s.routes()

	capabilities := routeRequest(handler, http.MethodGet, "/auth/capabilities", "", "kernel-secret")
	if capabilities.Code != http.StatusOK || !strings.Contains(capabilities.Body.String(), `"admin":false`) ||
		!strings.Contains(capabilities.Body.String(), `"mutations_enabled":true`) {
		t.Fatalf("read capabilities status=%d body=%s", capabilities.Code, capabilities.Body.String())
	}
	admin := routeRequest(handler, http.MethodGet, "/auth/capabilities", "", "admin-secret")
	if admin.Code != http.StatusOK || !strings.Contains(admin.Body.String(), `"admin":true`) {
		t.Fatalf("admin capabilities status=%d body=%s", admin.Code, admin.Body.String())
	}
	warnings := routeRequest(handler, http.MethodGet, "/control/warnings", "", "kernel-secret")
	if warnings.Code != http.StatusOK || !strings.Contains(warnings.Body.String(), "execution_attempt") ||
		!strings.Contains(warnings.Body.String(), "open_reservation") {
		t.Fatalf("warnings status=%d body=%s", warnings.Code, warnings.Body.String())
	}

	readOnly := &server{mode: protectedMode(config.ModeReadOnly), store: newMemoryStore()}
	disabled := routeRequest(readOnly.routes(), http.MethodGet, "/auth/capabilities", "", "admin-secret")
	if disabled.Code != http.StatusOK || !strings.Contains(disabled.Body.String(), `"mutations_enabled":false`) {
		t.Fatalf("read-only capabilities status=%d body=%s", disabled.Code, disabled.Body.String())
	}
}

func TestHaltAndResumeReturnAuditEventIDs(t *testing.T) {
	st := newMemoryStore()
	s := &server{mode: protectedMode(config.ModeShadow), store: st, consoleOrigin: defaultConsoleOrigin}
	handler := s.routes()
	halt := routeRequest(handler, http.MethodPost, "/halt", `{"reason":"operator verification"}`, "admin-secret")
	if halt.Code != http.StatusOK {
		t.Fatalf("halt status=%d body=%s", halt.Code, halt.Body.String())
	}
	var haltBody map[string]any
	if err := json.Unmarshal(halt.Body.Bytes(), &haltBody); err != nil || haltBody["event_id"].(float64) < 1 {
		t.Fatalf("halt body=%v err=%v", haltBody, err)
	}

	st.mu.Lock()
	st.breakerStates["live"] = store.BreakerState{Ledger: "live", Halted: true, Reason: "daily_loss", UpdatedAt: time.Now().UTC()}
	st.mu.Unlock()
	resume := routeRequest(handler, http.MethodPost, "/breaker/resume", `{"ledger":"live","reason":"daily_loss"}`, "admin-secret")
	if resume.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", resume.Code, resume.Body.String())
	}
	var resumeBody map[string]any
	if err := json.Unmarshal(resume.Body.Bytes(), &resumeBody); err != nil || resumeBody["event_id"].(float64) < 1 {
		t.Fatalf("resume body=%v err=%v", resumeBody, err)
	}
}
