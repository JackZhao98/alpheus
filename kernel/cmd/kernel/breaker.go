package main

import (
	"errors"
	"net/http"
	"strings"

	"alpheus/kernel/internal/store"
)

func (s *server) postBreakerResume(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Ledger string `json:"ledger"`
		Reason string `json:"reason"`
	}
	if !decodeJSONBody(w, r, &input) {
		return
	}
	input.Ledger, input.Reason = strings.TrimSpace(input.Ledger), strings.TrimSpace(input.Reason)
	if input.Ledger != "live" && input.Ledger != "shadow" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ledger must be live or shadow"})
		return
	}
	if input.Reason != "daily_loss" && input.Reason != "loss_streak" && input.Reason != "pnl_divergence" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid breaker reason"})
		return
	}
	var state store.BreakerState
	err := s.store.WithLedgerLock(input.Ledger == "shadow", func(gate store.OperationGate) error {
		window, err := s.databaseMarketWindow(gate)
		if err != nil {
			return err
		}
		state, err = gate.ResumeBreaker(input.Ledger, input.Reason, window.day, window.asOf, authenticatedSubject(r))
		if err != nil {
			return err
		}
		return s.ensureMarketDay(gate, window)
	})
	if err != nil {
		if errors.Is(err, store.ErrBreakerNotActive) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "breaker_not_active"})
			return
		}
		writeStoreError(w, "resume breaker", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ledger": state.Ledger, "halted": state.Halted,
		"reason": state.Reason, "override_reason": input.Reason,
		"event_id": state.EventID,
	})
}
