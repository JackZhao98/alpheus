package main

import (
	"context"
	"errors"
	"net/http"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
)

type adoptCandidateInput struct {
	ConfirmAttemptID     string `json:"confirm_attempt_id"`
	ConfirmBrokerOrderID string `json:"confirm_broker_order_id"`
}

func (s *server) adoptExecutionCandidate(w http.ResponseWriter, r *http.Request) {
	if s.tradingMode() != config.ModeLive {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate adoption requires live mode"})
		return
	}
	attemptID := r.PathValue("id")
	var input adoptCandidateInput
	if !decodeJSONBody(w, r, &input) {
		return
	}
	if !validUUID(attemptID) || input.ConfirmAttemptID != attemptID || !validUUID(input.ConfirmBrokerOrderID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "exact attempt and broker order confirmation is required"})
		return
	}
	seen, err := s.store.GetExecutionAttempt(attemptID)
	if err != nil {
		if errors.Is(err, store.ErrUnavailable) {
			writeStoreError(w, "read candidate attempt", err)
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate attempt unavailable"})
		return
	}
	if seen.State != "unknown" || seen.Intent != "place" ||
		seen.CandidateBrokerOrderID == "" || seen.CandidateBrokerOrderID != input.ConfirmBrokerOrderID {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate is not awaiting this exact approval"})
		return
	}
	claimed, err := s.store.ClaimRecoverableAttemptLive(
		seen.ID, s.workerID(), seen.State, seen.Attempt, s.attemptClaimTimeout(),
	)
	if err != nil {
		writeStoreError(w, "claim candidate attempt", err)
		return
	}
	if claimed == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate attempt changed"})
		return
	}
	execution := s.executionProvider()
	if execution == nil {
		_ = s.keepAttemptUnknown(claimed, "execution capability unavailable during candidate approval", "candidate_query_failed", "")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "execution capability unavailable"})
		return
	}
	candidates, _, queryErr := s.exactPlaceCandidatesForAttempt(r.Context(), execution, claimed)
	if queryErr != nil {
		_ = s.keepAttemptUnknown(claimed, "exact broker candidate query failed during approval", "candidate_query_failed", "")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "candidate verification unavailable"})
		return
	}
	if len(candidates) != 1 || candidates[0].BrokerOrderID != input.ConfirmBrokerOrderID {
		code := "candidate_zero"
		if len(candidates) > 1 {
			code = "candidate_ambiguous"
		} else if len(candidates) == 1 {
			code = "candidate_mismatch"
		}
		_ = s.keepAttemptUnknown(claimed, "candidate approval no longer has one exact match", code, "")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate is not uniquely verified"})
		return
	}
	canonicalCtx, cancel := context.WithTimeout(r.Context(), s.brokerCallTimeout())
	result, canonicalErr := execution.GetOrder(canonicalCtx, candidates[0].BrokerOrderID)
	cancel()
	if canonicalErr != nil || result.BrokerOrderID != input.ConfirmBrokerOrderID {
		_ = s.keepAttemptUnknown(claimed, "canonical candidate order unavailable during approval", "candidate_order_unavailable", "")
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "canonical candidate order unavailable"})
		return
	}
	result.ClientOrderID = claimed.ClientOrderID
	resolution := resolutionForOrder(claimed, result)
	if resolution.State == "unknown" {
		_ = s.keepAttemptUnknown(claimed, "candidate broker state is not adoptable", "candidate_state_unknown", "")
		writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate broker state is not adoptable"})
		return
	}
	resolution.CandidateBrokerOrderID = result.BrokerOrderID
	resolution.OperatorSubject = authenticatedSubject(r)
	updated, err := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, resolution)
	if err != nil {
		if errors.Is(err, store.ErrFillIntegrity) {
			_ = s.refreshGlobalHalt()
		}
		writeStoreError(w, "adopt candidate attempt", err)
		return
	}
	if !updated {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "candidate attempt changed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"attempt_id": claimed.ID, "broker_order_id": result.BrokerOrderID,
		"attempt_state": resolution.State, "order": result,
	})
}
