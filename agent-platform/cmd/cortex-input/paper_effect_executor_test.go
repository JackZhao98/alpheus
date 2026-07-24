package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestReadKernelPaperModeRequiresExactAuthenticatedState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet ||
				r.URL.Path != "/internal/v1/cortex-effects/paper-mode" ||
				r.Header.Get("Authorization") != "Bearer effect-token" {
				t.Fatalf("unexpected request: %s %s auth=%q",
					r.Method, r.URL.Path, r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"schema_revision": 1,
				"environment":     "paper",
				"mode":            "agentic",
				"generation":      7,
				"updated_at":      time.Now().UTC(),
			})
		},
	))
	defer server.Close()
	mode, err := readKernelPaperMode(
		context.Background(), server.Client(), server.URL,
		"effect-token",
	)
	if err != nil || mode.Mode != "agentic" || mode.Generation != 7 {
		t.Fatalf("mode=%+v err=%v", mode, err)
	}
}

func TestKernelPaperFailureCodesAreStable(t *testing.T) {
	for status, expected := range map[int]string{
		http.StatusBadRequest:          "kernel_request_rejected",
		http.StatusUnauthorized:        "kernel_authorization_rejected",
		http.StatusConflict:            "kernel_mode_conflict",
		http.StatusUnprocessableEntity: "kernel_settlement_rejected",
		http.StatusBadGateway:          "kernel_market_data_unavailable",
		http.StatusServiceUnavailable:  "kernel_unavailable",
		http.StatusInternalServerError: "kernel_effect_failed",
	} {
		if actual := kernelPaperFailureCode(status); actual != expected {
			t.Fatalf("status=%d actual=%q expected=%q",
				status, actual, expected)
		}
	}
}
