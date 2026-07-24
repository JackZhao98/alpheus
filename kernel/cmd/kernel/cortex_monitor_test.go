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

func TestCortexMonitorQuoteReturnsOnlyFreshNormalizedFact(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "input-token")
	if err := os.WriteFile(
		tokenPath, []byte("cortex-input-test-token"), 0o600,
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
		mode: protectedMode(config.ModeReadOnly), broker: fake,
		cortexTokenFile: tokenPath, limits: dualLedgerLimits(),
	}
	request := httptest.NewRequest(
		http.MethodPost, "/internal/v1/cortex-monitor/quote",
		strings.NewReader(`{"trigger_id":"trigger-1","symbol":"SPY"}`),
	)
	request.Header.Set("Authorization", "Bearer cortex-input-test-token")
	response := httptest.NewRecorder()
	server.routes().ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK ||
		!strings.Contains(body, `"mid":"638.12"`) ||
		!strings.Contains(body, `"provider":"test-feed"`) ||
		!strings.Contains(body, `"trigger_id":"trigger-1"`) {
		t.Fatalf("monitor quote status=%d body=%s", response.Code, body)
	}
}

func TestCortexMonitorQuoteRejectsGenericOrUnauthorizedRequests(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "input-token")
	if err := os.WriteFile(
		tokenPath, []byte("cortex-input-test-token"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	server := &server{
		mode: protectedMode(config.ModeReadOnly), broker: newFake("300"),
		cortexTokenFile: tokenPath, limits: dualLedgerLimits(),
	}
	for _, test := range []struct {
		body  string
		token string
		code  int
	}{
		{`{"trigger_id":"trigger-1","symbol":"spy"}`,
			"cortex-input-test-token", http.StatusBadRequest},
		{`{"trigger_id":"trigger-1","symbol":"SPY","tool":"place_order"}`,
			"cortex-input-test-token", http.StatusBadRequest},
		{`{"trigger_id":"trigger-1","symbol":"SPY"}`,
			"wrong", http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(
			http.MethodPost, "/internal/v1/cortex-monitor/quote",
			strings.NewReader(test.body),
		)
		request.Header.Set("Authorization", "Bearer "+test.token)
		response := httptest.NewRecorder()
		server.routes().ServeHTTP(response, request)
		if response.Code != test.code {
			t.Fatalf("status=%d want=%d body=%s",
				response.Code, test.code, response.Body.String())
		}
	}
}
