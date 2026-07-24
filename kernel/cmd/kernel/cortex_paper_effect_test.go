package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/units"
)

func TestCortexPaperOrderIsAgenticServerPricedAndIdempotent(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "paper-effect-token")
	if err := os.WriteFile(
		tokenPath, []byte("paper-effect-test-token"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	st := newMemoryStore()
	if _, err := st.SetAgentAutonomy(
		"paper", "agentic", 1, "test",
	); err != nil {
		t.Fatal(err)
	}
	fake := newFake("300")
	if err := fake.SetQuote(broker.Quote{
		Symbol: "SPY", Bid: units.MustMicros("638.10"),
		Ask: units.MustMicros("638.14"), Source: "test-feed",
		AsOf: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	server := &server{
		mode: protectedMode(config.ModeReadOnly), broker: fake, store: st,
		cortexPaperEffectTokenFile: tokenPath, limits: dualLedgerLimits(),
	}
	body := `{"schema_revision":1,` +
		`"authorization_id":"authorization-1",` +
		`"authorization_kind":"agentic",` +
		`"authorization_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"kernel_mode_generation":2,` +
		`"candidate_id":"candidate-1","effect_id":"effect-1",` +
		`"run_id":"run-1","task_id":"task-1","symbol":"SPY",` +
		`"kind":"equity","side":"buy","multiplier":1,"qty":1}`
	first := postCortexPaperEffect(t, server, body, "paper-effect-test-token")
	if first.Code != http.StatusOK ||
		!strings.Contains(first.Body.String(), `"fill_price":638.14`) ||
		!strings.Contains(first.Body.String(), `"idempotent_replay":false`) ||
		!strings.Contains(first.Body.String(), `"environment":"paper"`) {
		t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
	}
	replay := postCortexPaperEffect(t, server, body, "paper-effect-test-token")
	if replay.Code != http.StatusOK ||
		!strings.Contains(replay.Body.String(), `"idempotent_replay":true`) {
		t.Fatalf("status=%d body=%s", replay.Code, replay.Body.String())
	}
}

func TestCortexPaperOrderFailsClosed(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "paper-effect-token")
	if err := os.WriteFile(
		tokenPath, []byte("paper-effect-test-token"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	server := &server{
		mode: protectedMode(config.ModeReadOnly), broker: newFake("300"),
		store: newMemoryStore(), cortexPaperEffectTokenFile: tokenPath,
		limits: dualLedgerLimits(),
	}
	valid := `{"schema_revision":1,` +
		`"authorization_id":"authorization-1",` +
		`"authorization_kind":"agentic",` +
		`"authorization_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"kernel_mode_generation":2,` +
		`"candidate_id":"candidate-1","effect_id":"effect-1",` +
		`"run_id":"run-1","task_id":"task-1","symbol":"SPY",` +
		`"kind":"equity","side":"buy","multiplier":1,"qty":1}`
	observe := postCortexPaperEffect(
		t, server, valid, "paper-effect-test-token",
	)
	if observe.Code != http.StatusConflict ||
		!strings.Contains(observe.Body.String(), "authorization mode") {
		t.Fatalf("status=%d body=%s", observe.Code, observe.Body.String())
	}
	unauthorized := postCortexPaperEffect(t, server, valid, "wrong")
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s",
			unauthorized.Code, unauthorized.Body.String())
	}
	injected := strings.Replace(valid, `"qty":1}`,
		`"qty":1,"fill_price":1}`, 1)
	rejected := postCortexPaperEffect(
		t, server, injected, "paper-effect-test-token",
	)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s",
			rejected.Code, rejected.Body.String())
	}
}

func TestCortexPaperModeAndAuthorizationKindAreBound(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "paper-effect-token")
	if err := os.WriteFile(
		tokenPath, []byte("paper-effect-test-token"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	st := newMemoryStore()
	if _, err := st.SetAgentAutonomy(
		"paper", "copilot", 1, "test",
	); err != nil {
		t.Fatal(err)
	}
	server := &server{
		mode: protectedMode(config.ModeReadOnly), broker: newFake("300"),
		store: st, cortexPaperEffectTokenFile: tokenPath,
		limits: dualLedgerLimits(),
	}
	modeRequest := httptest.NewRequest(
		http.MethodGet, "/internal/v1/cortex-effects/paper-mode", nil,
	)
	modeRequest.Header.Set(
		"Authorization", "Bearer paper-effect-test-token",
	)
	mode := httptest.NewRecorder()
	server.routes().ServeHTTP(mode, modeRequest)
	if mode.Code != http.StatusOK ||
		!strings.Contains(mode.Body.String(), `"mode":"copilot"`) ||
		!strings.Contains(mode.Body.String(), `"generation":2`) {
		t.Fatalf("mode status=%d body=%s", mode.Code, mode.Body.String())
	}
	mismatch := `{"schema_revision":1,` +
		`"authorization_id":"authorization-1",` +
		`"authorization_kind":"agentic",` +
		`"authorization_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
		`"kernel_mode_generation":2,` +
		`"candidate_id":"candidate-1","effect_id":"effect-1",` +
		`"run_id":"run-1","task_id":"task-1","symbol":"SPY",` +
		`"kind":"equity","side":"buy","multiplier":1,"qty":1}`
	response := postCortexPaperEffect(
		t, server, mismatch, "paper-effect-test-token",
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "authorization mode") {
		t.Fatalf("status=%d body=%s",
			response.Code, response.Body.String())
	}
}

func postCortexPaperEffect(
	t *testing.T,
	server *server,
	body string,
	token string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost, "/internal/v1/cortex-effects/paper-order",
		strings.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	return response
}
