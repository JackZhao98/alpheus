package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	agentSessionCookie = "alpheus_agent_session"
	agentSessionTTL    = 12 * time.Hour
)

func (s *server) agentWebEnabled() bool {
	return s.mode.AgentWebPassword != "" && s.mode.AgentWebSessionKey != ""
}

func (s *server) postAgentLogin(w http.ResponseWriter, r *http.Request) {
	if !s.agentWebEnabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent login is not configured"})
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	if len(input.Password) > 256 || !passwordMatches(input.Password, s.mode.AgentWebPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid password"})
		return
	}
	expires := time.Now().UTC().Add(agentSessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name: agentSessionCookie, Value: s.signAgentSession(expires), Path: "/",
		Expires: expires, MaxAge: int(agentSessionTTL.Seconds()), HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil,
	})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "expires_at": expires})
}

func (s *server) getAgentSession(w http.ResponseWriter, r *http.Request) {
	if !s.agentSessionValid(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]bool{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *server) postAgentLogout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: agentSessionCookie, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false})
}

func (s *server) authorizeAgentWeb(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.agentSessionValid(r) {
			ctx := r.Context()
			ctx = contextWithSubject(ctx, "agent-web")
			next(w, r.WithContext(ctx))
			return
		}
		token := bearerToken(r)
		if constantTimeTokenMatch(token, s.mode.RuntimeToken) ||
			constantTimeTokenMatch(token, s.mode.AdminToken) ||
			constantTimeTokenMatch(token, s.mode.KernelToken) {
			ctx := contextWithSubject(r.Context(), "service-token")
			next(w, r.WithContext(ctx))
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}
}

func contextWithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, subjectContextKey{}, subject)
}

func passwordMatches(candidate, expected string) bool {
	candidateHash := sha256.Sum256([]byte(candidate))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(candidateHash[:], expectedHash[:]) == 1
}

func (s *server) signAgentSession(expires time.Time) string {
	payload := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(s.mode.AgentWebSessionKey))
	_, _ = mac.Write([]byte("alpheus-agent-web-v1\n" + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *server) agentSessionValid(r *http.Request) bool {
	if !s.agentWebEnabled() {
		return false
	}
	cookie, err := r.Cookie(agentSessionCookie)
	if err != nil {
		return false
	}
	payload, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || payload == "" || signature == "" {
		return false
	}
	expiresUnix, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresUnix {
		return false
	}
	provided, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.mode.AgentWebSessionKey))
	_, _ = mac.Write([]byte("alpheus-agent-web-v1\n" + payload))
	return hmac.Equal(provided, mac.Sum(nil))
}
