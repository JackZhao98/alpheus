package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
)

func protectedMode(mode string) config.ModeConfig {
	return config.ModeConfig{
		TradingMode: mode, RuntimeToken: "runtime-secret", AdminToken: "admin-secret",
		KernelToken: "kernel-secret", LiveTradingEnabled: true, LiveAccountID: "fake-account",
	}
}

func routeRequest(handler http.Handler, method, target, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestProtectedRoutesEnforceTokenGrants(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	st := newMemoryStore()
	if err := st.InsertOperation(id, "test", "C", "pending_review", risk.Operation{}, risk.Verdict{}); err != nil {
		t.Fatal(err)
	}
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		broker: newFake("300"), store: st,
	}
	handler := s.routes()

	for _, tc := range []struct {
		name, method, target, body string
	}{
		{"review", http.MethodPost, "/operations/" + id + "/review", `{"verdict":"rejected"}`},
		{"journal", http.MethodPost, "/journal", `{"operation_id":"` + id + `","hypothesis":{}}`},
		{"blackboard", http.MethodPut, "/blackboard/2026-07-17", `{}`},
	} {
		t.Run(tc.name+"/missing", func(t *testing.T) {
			w := routeRequest(handler, tc.method, tc.target, tc.body, "")
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}

	w := routeRequest(handler, http.MethodPost, "/operations/"+id+"/review", `{"verdict":"rejected"}`, "runtime-secret")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("runtime review status=%d body=%s", w.Code, w.Body.String())
	}
	w = routeRequest(handler, http.MethodPost, "/halt", `{"reason":"unauthorized"}`, "runtime-secret")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("runtime halt status=%d body=%s", w.Code, w.Body.String())
	}

	// Reviewer is not a client-writable field. Strict decoding rejects the old
	// spoofable shape and leaves the operation pending.
	w = routeRequest(handler, http.MethodPost, "/operations/"+id+"/review", `{"verdict":"rejected","reviewer":"attacker"}`, "admin-secret")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("spoofed reviewer status=%d body=%s", w.Code, w.Body.String())
	}
	w = routeRequest(handler, http.MethodPost, "/operations/"+id+"/review", `{"verdict":"rejected","rationale":"risk"}`, "admin-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("admin review status=%d body=%s", w.Code, w.Body.String())
	}
	row, err := st.GetOperation(id)
	if err != nil {
		t.Fatal(err)
	}
	var verdict map[string]string
	if err := json.Unmarshal(row.Verdict, &verdict); err != nil {
		t.Fatal(err)
	}
	if verdict["reviewer"] != "admin" || verdict["rationale"] != "risk" {
		t.Fatalf("verdict=%v", verdict)
	}

	if w := routeRequest(handler, http.MethodGet, "/state", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read status=%d body=%s", w.Code, w.Body.String())
	}
	if w := routeRequest(handler, http.MethodGet, "/state", "", "kernel-secret"); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), `"mode":"live"`) {
		t.Fatalf("kernel read status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReadOnlyRoutesReturn405AndLiveDoesNotMountSim(t *testing.T) {
	readOnly := &server{
		mode: protectedMode(config.ModeReadOnly), limits: dualLedgerLimits(),
		broker: newFake("300"), store: newMemoryStore(),
	}
	for _, request := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/operations", `{}`},
		{http.MethodPost, "/operations/11111111-1111-4111-8111-111111111111/review", `{}`},
		{http.MethodPost, "/journal", `{}`},
		{http.MethodPut, "/blackboard/2026-07-17", `{}`},
		{http.MethodPost, "/halt", `{"reason":"test"}`},
	} {
		w := routeRequest(readOnly.routes(), request.method, request.path, request.body, "")
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s status=%d body=%s", request.method, request.path, w.Code, w.Body.String())
		}
	}

	live := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		broker: newFake("300"), store: newMemoryStore(),
	}
	w := routeRequest(live.routes(), http.MethodPost, "/sim/quote", `{"symbol":"SPY","bid":1,"ask":2}`, "admin-secret")
	if w.Code != http.StatusNotFound {
		t.Fatalf("live sim route status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestShadowModeForcesEveryOperationOntoPaperLedger(t *testing.T) {
	st := newMemoryStore()
	b := newFake("300")
	s := &server{
		mode: protectedMode(config.ModeShadow), limits: dualLedgerLimits(),
		broker: b, store: st,
	}
	payload := `{"action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	w := routeRequest(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["class"] != "B" || body["shadow"] != true {
		t.Fatalf("body=%v", body)
	}
	id := body["operation_id"].(string)
	st.mu.Lock()
	persisted := st.operations[id]
	st.mu.Unlock()
	if !persisted.Shadow {
		t.Fatalf("persisted operation is live: %+v", persisted)
	}
	journal := `{"operation_id":"` + id + `","hypothesis":{},"shadow":false}`
	w = routeRequest(s.routes(), http.MethodPost, "/journal", journal, "runtime-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("journal status=%d body=%s", w.Code, w.Body.String())
	}
	st.mu.Lock()
	journalShadow := st.journals[len(st.journals)-1].shadow
	st.mu.Unlock()
	if !journalShadow {
		t.Fatal("shadow mode accepted a live journal entry")
	}
	if order, _ := b.GetOrder("fake-1"); order.Reason != "unknown order" {
		t.Fatalf("shadow reached broker: %+v", order)
	}
}

func TestGlobalHaltRejectsOpenButAllowsVerifiedClose(t *testing.T) {
	st := newMemoryStore()
	b := newFake("300")
	setQuote(b, "HALT", "9.90", "10", 1_000)
	if result, err := placeOrder(b, "HALT", "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed position: result=%+v err=%v", result, err)
	}
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		broker: b, store: st,
	}
	handler := s.routes()
	w := routeRequest(handler, http.MethodPost, "/halt", `{"reason":"operator stop"}`, "admin-secret")
	if w.Code != http.StatusOK {
		t.Fatalf("halt status=%d body=%s", w.Code, w.Body.String())
	}

	open := `{"action":"open","kind":"equity","underlying":"HALT","symbol":"HALT","side":"buy","qty":1,"max_risk_usd":10,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	w = routeRequest(handler, http.MethodPost, "/operations", open, "runtime-secret")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"class":"REJECT"`) ||
		!strings.Contains(w.Body.String(), "breaker halted: operator stop") {
		t.Fatalf("halted open status=%d body=%s", w.Code, w.Body.String())
	}

	w = routeRequest(handler, http.MethodPost, "/operations", `{"action":"close","symbol":"HALT","qty":1}`, "runtime-secret")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"class":"A"`) ||
		!strings.Contains(w.Body.String(), `"status":"executed"`) {
		t.Fatalf("halted close status=%d body=%s", w.Code, w.Body.String())
	}
	if !containsString(st.events, globalHaltEvent) {
		t.Fatalf("halt event missing: %v", st.events)
	}

	restarted := &server{mode: protectedMode(config.ModeLive), broker: b, store: st}
	if err := restarted.loadGlobalHalt(); err != nil {
		t.Fatal(err)
	}
	if halted, reason := restarted.haltSnapshot(); !halted || reason != "operator stop" {
		t.Fatalf("restarted halt=%t reason=%q", halted, reason)
	}
}

func TestLiveAccountBindingFailurePrecedesBrokerMutation(t *testing.T) {
	st := newMemoryStore()
	b := newFake("300")
	b.SetAccountID("different-account")
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		broker: b, store: st,
	}
	payload := `{"action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	w := routeRequest(s.routes(), http.MethodPost, "/operations", payload, "runtime-secret")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "account_binding_violation") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if order, _ := b.GetOrder("fake-1"); order.Reason != "unknown order" {
		t.Fatalf("binding failure reached broker: %+v", order)
	}
	if !containsString(st.events, "account_binding_violation") {
		t.Fatalf("binding event missing: %v", st.events)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var _ broker.Adapter = (*broker.Fake)(nil)
