package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"alpheus/kernel/internal/config"
)

const (
	defaultConsoleOrigin = "http://localhost:8100"
	controlWarningLimit  = 200
)

func normalizeConsoleOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("CONSOLE_ORIGIN must be one http(s) origin without a path")
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

func (s *server) configuredConsoleOrigin() string {
	if s.consoleOrigin != "" {
		return s.consoleOrigin
	}
	return defaultConsoleOrigin
}

func (s *server) requireConsoleOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin, err := normalizeConsoleOrigin(r.Header.Get("Origin"))
		if err != nil || origin != s.configuredConsoleOrigin() {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin_not_allowed"})
			return
		}
		next(w, r)
	}
}

func (s *server) getAuthCapabilities(w http.ResponseWriter, r *http.Request) {
	subject := authenticatedSubject(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"subject":           subject,
		"admin":             subject == "admin" || s.tradingMode() == config.ModeSim,
		"mutations_enabled": s.tradingMode() != config.ModeReadOnly,
		"mode":              s.tradingMode(),
	})
}

func (s *server) getControlWarnings(w http.ResponseWriter, _ *http.Request) {
	now := time.Now().UTC()
	warnings, err := s.store.ListControlWarnings(
		now.Add(-s.attemptStaleAfter()), now.Add(-s.attemptClaimTimeout()), controlWarningLimit,
	)
	if err != nil {
		writeStoreError(w, "list control warnings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"warnings": warnings, "as_of": now, "source": "kernel_db",
	})
}
