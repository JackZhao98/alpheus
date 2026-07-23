package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/rhmcp"
)

func TestRobinhoodWebOAuthPersistsEncryptedConnectionAndConsumesCallbackOnce(t *testing.T) {
	st := newMemoryStore()
	mode := config.ModeConfig{
		TradingMode: config.ModeReadOnly, AgentWebPassword: "correct-horse-battery",
		AgentWebSessionKey: strings.Repeat("k", 32),
	}
	exchanged := 0
	s := &server{
		mode: mode, store: st, robinhoodEnabled: true, consoleOrigin: "http://localhost:8100",
		brokerTimeout: time.Second,
		robinhoodBegin: func(_ context.Context, redirect string) (rhmcp.AuthorizationStart, error) {
			if redirect != "http://localhost:8100/agent/robinhood/callback" {
				t.Fatalf("redirect=%q", redirect)
			}
			return rhmcp.AuthorizationStart{AuthorizationURL: "https://robinhood.example/authorize", State: "state-secret", Verifier: "verifier-secret", ClientID: "client-id", RedirectURI: redirect}, nil
		},
		robinhoodExchange: func(_ context.Context, clientID, code, verifier, redirect string) (rhmcp.OAuthToken, error) {
			exchanged++
			if clientID != "client-id" || code != "code-secret" || verifier != "verifier-secret" || redirect != "http://localhost:8100/agent/robinhood/callback" {
				t.Fatalf("unexpected exchange input")
			}
			return rhmcp.OAuthToken{Version: 1, AccessToken: "access-secret", RefreshToken: "refresh-secret", TokenType: "Bearer", ExpiresAt: time.Now().UTC().Add(time.Hour), ClientID: clientID}, nil
		},
	}
	handler := s.routes()
	login := routeRequest(handler, http.MethodPost, "/agent/auth/login", `{"password":"correct-horse-battery"}`, "")
	if login.Code != http.StatusOK || len(login.Result().Cookies()) != 1 {
		t.Fatalf("login=%d", login.Code)
	}
	cookie := login.Result().Cookies()[0]
	request := func(method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}

	start := request(http.MethodPost, "/agent/robinhood/connect", "")
	if start.Code != http.StatusOK || !strings.Contains(start.Body.String(), "robinhood.example") || strings.Contains(start.Body.String(), "verifier-secret") {
		t.Fatalf("start=%d body=%s", start.Code, start.Body.String())
	}
	if len(st.robinhoodOAuthFlows) != 1 {
		t.Fatalf("flow count=%d", len(st.robinhoodOAuthFlows))
	}
	for digest, flow := range st.robinhoodOAuthFlows {
		if digest == "state-secret" || strings.Contains(string(flow.VerifierCiphertext), "verifier-secret") {
			t.Fatal("OAuth state or verifier persisted in plaintext")
		}
	}

	callback := routeRequest(handler, http.MethodGet, "/agent/robinhood/callback?code=code-secret&state=state-secret", "", "")
	if callback.Code != http.StatusOK || !strings.Contains(callback.Body.String(), "robinhood=connected") || strings.Contains(callback.Body.String(), "code-secret") {
		t.Fatalf("callback=%d body=%s", callback.Code, callback.Body.String())
	}
	if exchanged != 1 {
		t.Fatalf("exchange calls=%d", exchanged)
	}
	if raw := st.agentSecrets[robinhoodMCPSecretName]; strings.Contains(string(raw), "access-secret") || strings.Contains(string(raw), "refresh-secret") {
		t.Fatal("OAuth token persisted in plaintext")
	}
	connection, err := s.loadRobinhoodConnection()
	if err != nil || connection.Token.AccessToken != "access-secret" || connection.BoundAccountID != "" {
		t.Fatalf("connection=%+v err=%v", connection, err)
	}
	status := request(http.MethodGet, "/agent/robinhood/connection", "")
	var payload map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &payload); err != nil || payload["status"] != "needs_account" || strings.Contains(status.Body.String(), "access-secret") {
		t.Fatalf("status=%d payload=%s err=%v", status.Code, status.Body.String(), err)
	}
	replay := routeRequest(handler, http.MethodGet, "/agent/robinhood/callback?code=code-secret&state=state-secret", "", "")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), "robinhood=failed") || exchanged != 1 {
		t.Fatalf("replay=%d calls=%d", replay.Code, exchanged)
	}
}

func TestRobinhoodCapabilityReviewExposesOnlySafeSecretFreeSchemas(t *testing.T) {
	tools := make([]rhmcp.ToolSchema, 0, len(rhmcp.SafeQueryTools)+1)
	for _, name := range rhmcp.SafeQueryTools {
		tools = append(tools, rhmcp.ToolSchema{
			Name: name, Description: "reviewable read schema",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		})
	}
	tools = append(tools, rhmcp.ToolSchema{
		Name: "place_equity_order", Description: "must not escape",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	})
	s := &server{
		mode: protectedMode(config.ModeReadOnly), robinhoodEnabled: true, brokerTimeout: time.Second,
		robinhoodDiscover: func(context.Context) ([]rhmcp.ToolSchema, error) {
			return tools, nil
		},
	}
	response := routeRequest(s.routes(), http.MethodGet, "/agent/robinhood/capabilities", "", "kernel-secret")
	if response.Code != http.StatusOK {
		t.Fatalf("capability review status=%d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "place_equity_order") || strings.Contains(response.Body.String(), "access_token") {
		t.Fatalf("capability review leaked an excluded surface: %s", response.Body.String())
	}
	var payload struct {
		Tools []rhmcp.ToolSchema `json:"tools"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || len(payload.Tools) != len(rhmcp.SafeQueryTools) {
		t.Fatalf("capability review tools=%d err=%v", len(payload.Tools), err)
	}
}
