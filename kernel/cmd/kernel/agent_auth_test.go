package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

func TestAgentPasswordLoginCreatesHttpOnlySession(t *testing.T) {
	s := &server{mode: config.ModeConfig{
		TradingMode:      config.ModeReadOnly,
		AgentWebPassword: "correct-horse-battery", AgentWebSessionKey: strings.Repeat("k", 32),
	}}
	handler := s.routes()
	wrong := routeRequest(handler, http.MethodPost, "/agent/auth/login", `{"password":"wrong-password"}`, "")
	if wrong.Code != http.StatusUnauthorized || len(wrong.Result().Cookies()) != 0 {
		t.Fatalf("wrong password status=%d cookies=%v", wrong.Code, wrong.Result().Cookies())
	}
	login := routeRequest(handler, http.MethodPost, "/agent/auth/login", `{"password":"correct-horse-battery"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == agentSessionCookie {
			session = cookie
		}
	}
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie=%+v", session)
	}
	req := httptest.NewRequest(http.MethodGet, "/agent/auth/session", nil)
	req.AddCookie(session)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("session status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentLabIsSeparateFromCockpit(t *testing.T) {
	s := &server{}
	handler := s.routes()
	lab := routeRequest(handler, http.MethodGet, "/agent-lab", "", "")
	if lab.Code != http.StatusOK || !strings.Contains(lab.Body.String(), "Ask Scout") || !strings.Contains(lab.Body.String(), "login-form") {
		t.Fatalf("agent lab status=%d", lab.Code)
	}
	cockpit := routeRequest(handler, http.MethodGet, "/cockpit", "", "")
	if strings.Contains(cockpit.Body.String(), "agent-query-form") || strings.Contains(cockpit.Body.String(), "AGENT MVP PREVIEW") {
		t.Fatal("agent lab leaked into cockpit")
	}
}

func TestAgentQueryRejectsMissingSessionAndToken(t *testing.T) {
	s := &server{mode: config.ModeConfig{
		TradingMode: config.ModeSim, AgentWebPassword: "correct-horse-battery",
		AgentWebSessionKey: strings.Repeat("k", 32),
	}}
	req := httptest.NewRequest(http.MethodPost, "/agent/query", bytes.NewBufferString(`{"symbol":"SOFI","query":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
