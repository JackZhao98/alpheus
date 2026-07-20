package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestAgentSecretEnvelopeIsAuthenticatedAndProviderBound(t *testing.T) {
	root := strings.Repeat("r", 32)
	first, err := sealAgentSecret(root, "openai", "sk-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := sealAgentSecret(root, "openai", "sk-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte("sk-test-secret")) {
		t.Fatal("credential envelope is deterministic or contains plaintext")
	}
	plain, err := openAgentSecret(root, "openai", first)
	if err != nil || plain != "sk-test-secret" {
		t.Fatalf("round trip plain=%q err=%v", plain, err)
	}
	if _, err := openAgentSecret(root, "brave", first); err == nil {
		t.Fatal("credential decrypted under the wrong provider")
	}
	first[len(first)-1] ^= 1
	if _, err := openAgentSecret(root, "openai", first); err == nil {
		t.Fatal("tampered credential decrypted")
	}
}

func TestAgentSecretWebLifecycleNeverReturnsPlaintext(t *testing.T) {
	st := newMemoryStore()
	s := &server{mode: config.ModeConfig{
		TradingMode: config.ModeReadOnly, AgentWebPassword: "correct-horse-battery",
		AgentWebSessionKey: strings.Repeat("k", 32),
	}, store: st}
	handler := s.routes()
	login := routeRequest(handler, http.MethodPost, "/agent/auth/login", `{"password":"correct-horse-battery"}`, "")
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("login status=%d", login.Code)
	}
	cookie := login.Result().Cookies()[0]

	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	put := request(http.MethodPut, "/agent/secrets/openai", `{"value":"sk-test-secret"}`)
	if put.Code != http.StatusOK || strings.Contains(put.Body.String(), "sk-test-secret") {
		t.Fatalf("put status=%d body=%s", put.Code, put.Body.String())
	}
	listed := request(http.MethodGet, "/agent/secrets", "")
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"openai":true`) || strings.Contains(listed.Body.String(), "sk-test-secret") {
		t.Fatalf("list status=%d body=%s", listed.Code, listed.Body.String())
	}
	plain, err := s.loadAgentSecret("openai")
	if err != nil || plain != "sk-test-secret" {
		t.Fatalf("load plain=%q err=%v", plain, err)
	}
	deleted := request(http.MethodDelete, "/agent/secrets/openai", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
	if _, err := s.loadAgentSecret("openai"); err == nil {
		t.Fatal("deleted credential remained loadable")
	}
}
