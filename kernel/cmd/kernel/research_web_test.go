package main

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestKernelWebSearchKeepsBraveKeyBelowAgentBoundary(t *testing.T) {
	st := newMemoryStore()
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeReadOnly, RuntimeToken: "runtime-secret", KernelToken: "kernel-secret", AgentWebSessionKey: strings.Repeat("k", 32)},
		store: st, researchURL: "http://research.test",
	}
	ciphertext, err := sealAgentSecret(s.mode.AgentWebSessionKey, "brave", "brave-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAgentSecret("brave", ciphertext); err != nil {
		t.Fatal(err)
	}
	s.researchHTTP = &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/web/search" || r.Header.Get("Authorization") != "Bearer kernel-secret" || !strings.Contains(string(raw), `"api_key":"brave-secret"`) {
			t.Fatalf("path=%s auth=%q body=%s", r.URL.Path, r.Header.Get("Authorization"), raw)
		}
		body := `{"available":true,"source":"brave-web","query":"SOFI latest","retrieved_at":"2026-07-20T12:00:00Z","items":[{"title":"Source","url":"https://example.com/a","description":"Claim"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r}, nil
	})}

	response := routeRequest(s.routes(), http.MethodGet, "/research/search?q="+url.QueryEscape("SOFI latest")+"&count=5", "", "runtime-secret")
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "brave-secret") || !strings.Contains(response.Body.String(), "Source") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestKernelWebFetchReturnsOnlyBoundedUntrustedDocument(t *testing.T) {
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeReadOnly, RuntimeToken: "runtime-secret", KernelToken: "kernel-secret"},
		store: newMemoryStore(), researchURL: "http://research.test",
	}
	s.researchHTTP = &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/web/fetch" || !strings.Contains(string(raw), `"url":"https://example.com/article"`) {
			t.Fatalf("path=%s body=%s", r.URL.Path, raw)
		}
		body := `{"available":true,"source":"web-page-untrusted","url":"https://example.com/article","title":"Example","content_type":"text/html","text":"Observed claim.","truncated":false,"retrieved_at":"2026-07-20T12:00:00Z"}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: r}, nil
	})}

	response := routeRequest(s.routes(), http.MethodGet, "/research/fetch?url="+url.QueryEscape("https://example.com/article")+"&max_chars=1000", "", "runtime-secret")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"source":"web-page-untrusted"`) || !strings.Contains(response.Body.String(), "Observed claim") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestKernelWebSearchRequiresEncryptedCredential(t *testing.T) {
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeReadOnly, RuntimeToken: "runtime-secret", AgentWebSessionKey: strings.Repeat("k", 32)}, store: newMemoryStore()}
	response := routeRequest(s.routes(), http.MethodGet, "/research/search?q=SOFI&count=5", "", "runtime-secret")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
