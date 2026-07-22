package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
)

const globalHaltEvent = "global_halt_transition"

var errAccountBindingViolation = errors.New("account binding violation")

func (s *server) loadGlobalHalt() error {
	halted, reason, err := s.store.LoadGlobalHalt()
	if err != nil {
		return err
	}
	s.halted, s.haltReason = halted, reason
	return nil
}

// refreshGlobalHalt refreshes the local classification cache. The database
// event plus Halt/send advisory lock remain authoritative across processes.
func (s *server) refreshGlobalHalt() error {
	halted, reason, err := s.store.LoadGlobalHalt()
	if err != nil {
		return err
	}
	s.haltMu.Lock()
	s.halted, s.haltReason = halted, reason
	s.haltMu.Unlock()
	return nil
}

func (s *server) haltSnapshot() (bool, string) {
	s.haltMu.RLock()
	defer s.haltMu.RUnlock()
	return s.halted, s.haltReason
}

func (s *server) postHalt(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}

	s.haltMu.Lock()
	defer s.haltMu.Unlock()
	transition, err := s.store.ActivateGlobalHalt(in.Reason, authenticatedSubject(r), s.tradingMode())
	if err != nil {
		writeStoreError(w, "persist global halt", err)
		return
	}
	s.halted, s.haltReason = true, transition.Reason
	response := map[string]any{
		"halted": true, "reason": transition.Reason, "event_id": transition.EventID,
		"cut_at": transition.CutAt,
	}
	if transition.InFlightAttemptID != "" {
		response["in_flight_attempt_id"] = transition.InFlightAttemptID
		response["in_flight_attempt_state"] = transition.InFlightAttemptState
	}
	if transition.BlockedUnsentAttemptID != "" {
		response["blocked_unsent_attempt_id"] = transition.BlockedUnsentAttemptID
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) postHaltResume(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if !decodeJSONBody(w, r, &in) {
		return
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}

	s.haltMu.Lock()
	defer s.haltMu.Unlock()
	transition, err := s.store.ResumeGlobalHalt(in.Reason, authenticatedSubject(r), s.tradingMode())
	if err != nil {
		switch {
		case errors.Is(err, store.ErrGlobalHaltNotActive):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "global_halt_not_active"})
		case errors.Is(err, store.ErrGlobalHaltExecutionPending):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "global_halt_execution_pending"})
		default:
			writeStoreError(w, "resume global halt", err)
		}
		return
	}
	s.halted, s.haltReason = false, ""
	writeJSON(w, http.StatusOK, map[string]any{
		"halted": false, "reason": transition.Reason, "event_id": transition.EventID,
		"resumed_at": transition.CutAt,
	})
}

func (s *server) assertLiveAccountBinding(ctx context.Context, operationID string) error {
	if s.tradingMode() != config.ModeLive {
		return nil
	}
	actual, err := s.authorityAccountProvider().AccountID(ctx)
	reason := "mismatch"
	if err != nil {
		reason = "resolution_failed"
	}
	if err == nil && actual != "" && actual == s.boundRobinhoodAccountID() {
		return nil
	}
	if eventErr := s.store.InsertEvent("account_binding_violation", map[string]string{
		"operation_id": operationID, "reason": reason, "mode": s.tradingMode(),
	}); eventErr != nil {
		return fmt.Errorf("%w: event persistence failed: %w", errAccountBindingViolation, eventErr)
	}
	return fmt.Errorf("%w", errAccountBindingViolation)
}
