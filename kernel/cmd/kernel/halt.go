package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"alpheus/kernel/internal/config"
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

// refreshGlobalHalt makes the event-backed switch global across kernel
// processes without adding mutable schema before M2.7. An open refreshes before
// its local halt read lock, so a transition already committed by another
// process is observed before classification.
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
	if !s.halted {
		payload := map[string]any{
			"halted": true, "reason": in.Reason,
			"subject": authenticatedSubject(r), "mode": s.tradingMode(),
		}
		if err := s.store.InsertEvent(globalHaltEvent, payload); err != nil {
			writeStoreError(w, "persist global halt", err)
			return
		}
		s.halted, s.haltReason = true, in.Reason
	}
	writeJSON(w, http.StatusOK, map[string]any{"halted": true, "reason": s.haltReason})
}

func (s *server) assertLiveAccountBinding(ctx context.Context, operationID string) error {
	if s.tradingMode() != config.ModeLive {
		return nil
	}
	actual, err := s.accountProvider().AccountID(ctx)
	reason := "mismatch"
	if err != nil {
		reason = "resolution_failed"
	}
	if err == nil && actual == s.mode.LiveAccountID {
		return nil
	}
	if eventErr := s.store.InsertEvent("account_binding_violation", map[string]string{
		"operation_id": operationID, "reason": reason, "mode": s.tradingMode(),
	}); eventErr != nil {
		return fmt.Errorf("%w: event persistence failed: %w", errAccountBindingViolation, eventErr)
	}
	return fmt.Errorf("%w", errAccountBindingViolation)
}
