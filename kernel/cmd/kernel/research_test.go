package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/config"
)

func TestKernelResearchNewsKeepsCredentialBelowAgentBoundary(t *testing.T) {
	st := newMemoryStore()
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeReadOnly, RuntimeToken: "runtime-secret", KernelToken: "kernel-secret", AgentWebSessionKey: strings.Repeat("k", 32)},
		store: st, researchURL: "http://research.test",
	}
	initial := researchCredentialJSON("old-access", "old-refresh", time.Now().Add(time.Hour))
	ciphertext, err := sealAgentSecret(s.mode.AgentWebSessionKey, "robinhood_research", initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAgentSecret("robinhood_research", ciphertext); err != nil {
		t.Fatal(err)
	}
	refreshed := researchCredentialJSON("new-access", "new-refresh", time.Now().Add(24*time.Hour))
	s.researchHTTP = &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/robinhood/news" || r.Header.Get("Authorization") != "Bearer kernel-secret" {
			t.Fatalf("gateway request=%s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "old-access") || !strings.Contains(string(raw), `"symbol":"SOFI"`) {
			t.Fatalf("gateway body missing bounded credential or symbol")
		}
		body := `{"news":{"available":true,"source":"robinhood-private-api","symbol":"SOFI","retrieved_at":"2026-07-20T12:00:00Z","items":[{"title":"Headline","url":"https://example.com/a","source":"Wire","published_at":"2026-07-20T11:00:00Z"}]},"refreshed_credentials":` + refreshed + `}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}

	response := routeRequest(s.routes(), http.MethodGet, "/research/news/sofi", "", "runtime-secret")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "new-access") || strings.Contains(response.Body.String(), "refreshed_credentials") || !strings.Contains(response.Body.String(), "Headline") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	stored, err := s.loadAgentSecret("robinhood_research")
	if err != nil || !strings.Contains(stored, "new-access") || strings.Contains(stored, "old-access") {
		t.Fatalf("refreshed credential was not atomically replaced err=%v", err)
	}
}

func TestRobinhoodResearchCredentialValidation(t *testing.T) {
	valid := researchCredentialJSON("access", "refresh", time.Now().Add(time.Hour))
	if !validAgentSecretValue("robinhood_research", valid) {
		t.Fatal("valid research credential rejected")
	}
	for _, invalid := range []string{`{}`, `{"access_token":"x"}`, strings.Repeat("x", 4001)} {
		if validAgentSecretValue("robinhood_research", invalid) {
			t.Fatalf("invalid research credential accepted: %.40q", invalid)
		}
	}
}

func researchCredentialJSON(access, refresh string, expires time.Time) string {
	raw, _ := json.Marshal(map[string]any{
		"access_token": access, "refresh_token": refresh, "token_type": "Bearer",
		"expires_at": expires.UTC(), "device_token": "device-token",
	})
	return string(raw)
}
