package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"alpheus/kernel/internal/config"
)

type permission int

const (
	permissionRead permission = iota
	permissionRuntime
	permissionAdmin
)

type subjectContextKey struct{}

func constantTimeTokenMatch(candidate, expected string) bool {
	if candidate == "" || expected == "" {
		return false
	}
	candidateHash := sha256.Sum256([]byte(candidate))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(candidateHash[:], expectedHash[:]) == 1
}

func bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.Contains(token, " ") {
		return ""
	}
	return token
}

func (s *server) tradingMode() string {
	if s.mode.TradingMode == "" {
		return config.ModeSim
	}
	return s.mode.TradingMode
}

func (s *server) authenticate(r *http.Request, required permission) (string, bool) {
	if s.tradingMode() == config.ModeSim {
		return "sim", true
	}
	token := bearerToken(r)
	subject, granted := "", permissionRead
	switch {
	case constantTimeTokenMatch(token, s.mode.AdminToken):
		subject, granted = "admin", permissionAdmin
	case constantTimeTokenMatch(token, s.mode.RuntimeToken):
		subject, granted = "runtime", permissionRuntime
	case constantTimeTokenMatch(token, s.mode.KernelToken):
		subject, granted = "kernel", permissionRead
	default:
		return "", false
	}
	return subject, granted >= required
}

func (s *server) authorize(required permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subject, ok := s.authenticate(r, required)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx := context.WithValue(r.Context(), subjectContextKey{}, subject)
		next(w, r.WithContext(ctx))
	}
}

func authenticatedSubject(r *http.Request) string {
	if subject, ok := r.Context().Value(subjectContextKey{}).(string); ok && subject != "" {
		return subject
	}
	return "sim"
}
