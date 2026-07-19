package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

type PreEffectManifestInput struct {
	ID                        string
	AttemptID                 string
	FencingToken              int
	AccountID                 string
	Effect                    string
	ObservationID             string
	ObservationGeneration     int64
	ObservationManifestDigest string
	TargetBrokerOrderID       string
	Facts                     any
	ExpiresAt                 time.Time
	Ledger                    string
	ActivePolicyRevisionID    int64
	ActivePolicyGeneration    int64
	ActivePolicyDigest        string
	ExpectedLocalOpenRisk     units.Micros
	ExpectedLocalHeldCash     units.Micros
	ExpectedOtherCloseQty     units.Qty
}

type PreEffectManifest struct {
	ID                        string          `json:"id"`
	AttemptID                 string          `json:"attempt_id"`
	FencingToken              int             `json:"fencing_token"`
	AccountID                 string          `json:"account_id"`
	Effect                    string          `json:"effect"`
	ObservationID             string          `json:"observation_id"`
	ObservationGeneration     int64           `json:"observation_generation"`
	ObservationManifestDigest string          `json:"observation_manifest_digest"`
	TargetBrokerOrderID       string          `json:"target_broker_order_id,omitempty"`
	Facts                     json.RawMessage `json:"facts"`
	FactsDigest               string          `json:"facts_digest"`
	ExpiresAt                 time.Time       `json:"expires_at"`
	CreatedAt                 time.Time       `json:"created_at"`
	Ledger                    string          `json:"ledger"`
	ActivePolicyRevisionID    int64           `json:"active_policy_revision_id"`
	ActivePolicyGeneration    int64           `json:"active_policy_generation"`
	ActivePolicyDigest        string          `json:"active_policy_digest"`
	ExpectedLocalOpenRisk     units.Micros    `json:"expected_local_open_risk"`
	ExpectedLocalHeldCash     units.Micros    `json:"expected_local_held_cash"`
	ExpectedOtherCloseQty     units.Qty       `json:"expected_other_close_qty"`
}

// RecordPreEffectManifest durably records the exact read facts which may
// authorize one Provider mutation. It does not mark an attempt sent; the
// manifest is checked and bound atomically by MarkAttemptSentWithManifest.
func (s *Store) RecordPreEffectManifest(input PreEffectManifestInput) (*PreEffectManifest, error) {
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.TargetBrokerOrderID = strings.TrimSpace(input.TargetBrokerOrderID)
	if input.ID == "" {
		input.ID = NewID()
	}
	if input.AttemptID == "" || input.FencingToken <= 0 || input.AccountID == "" ||
		input.ObservationID == "" || input.ObservationGeneration <= 0 || input.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("pre-effect manifest identity is invalid")
	}
	switch input.Effect {
	case "place_open", "place_close":
		if input.TargetBrokerOrderID != "" {
			return nil, fmt.Errorf("place pre-effect manifest has a cancel target")
		}
	case "cancel_order", "replace_cancel":
		if input.TargetBrokerOrderID == "" {
			return nil, fmt.Errorf("cancel pre-effect manifest lacks an exact target")
		}
	default:
		return nil, fmt.Errorf("pre-effect manifest effect is invalid")
	}
	observationDigest, err := hex.DecodeString(input.ObservationManifestDigest)
	if err != nil || len(observationDigest) != sha256.Size {
		return nil, fmt.Errorf("pre-effect observation digest is invalid")
	}
	activePolicyDigest, err := hex.DecodeString(input.ActivePolicyDigest)
	if input.Ledger != "live" || input.ActivePolicyRevisionID <= 0 || input.ActivePolicyGeneration <= 0 ||
		err != nil || len(activePolicyDigest) != sha256.Size || input.ExpectedLocalOpenRisk < 0 ||
		input.ExpectedLocalHeldCash < 0 || input.ExpectedOtherCloseQty < 0 {
		return nil, fmt.Errorf("pre-effect evaluation binding is invalid")
	}
	facts, err := canonicalJSONObject(input.Facts)
	if err != nil {
		return nil, fmt.Errorf("pre-effect facts are invalid: %w", err)
	}
	factsDigest := sha256.Sum256(facts)

	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	var intent, action, target, providerAccount string
	err = tx.QueryRowContext(ctx, `SELECT attempt.intent,COALESCE(operation.payload->>'action',''),
		COALESCE(attempt.target_broker_order_id,''),COALESCE(attempt.provider_account_id,'')
		FROM execution_attempt AS attempt
		JOIN operations AS operation ON operation.id=attempt.operation_id
		WHERE attempt.id=$1 AND attempt.attempt=$2 AND attempt.state='claimed'
		FOR UPDATE OF attempt`, input.AttemptID, input.FencingToken).Scan(&intent, &action, &target, &providerAccount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, preEffectStaleError("pre-effect attempt fencing is stale")
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if input.Effect != expectedPreEffect(intent, action) ||
		(intent == "place" && providerAccount != input.AccountID) ||
		(intent == "cancel" && target != input.TargetBrokerOrderID) {
		return nil, preEffectStaleError("pre-effect manifest does not match persisted intent")
	}
	var observationStatus, observationPurpose, observationAccount string
	var observationGeneration int64
	var persistedObservationDigest []byte
	err = tx.QueryRowContext(ctx, `SELECT status,purpose,account_id,generation,manifest_digest
		FROM broker_observation WHERE id=$1`, input.ObservationID).Scan(
		&observationStatus, &observationPurpose, &observationAccount,
		&observationGeneration, &persistedObservationDigest,
	)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if observationStatus != "complete" || observationPurpose != "pre_effect" ||
		observationAccount != input.AccountID || observationGeneration != input.ObservationGeneration ||
		!equalBytes(persistedObservationDigest, observationDigest) {
		return nil, preEffectStaleError("pre-effect observation identity is invalid")
	}
	var manifest PreEffectManifest
	err = tx.QueryRowContext(ctx, `INSERT INTO execution_pre_effect_manifest
		(id,execution_attempt_id,fencing_token,account_id,effect,broker_observation_id,
		 observation_generation,observation_manifest_digest,target_broker_order_id,
		 facts,facts_digest,expires_at,ledger,active_kernel_policy_revision_id,
		 active_kernel_policy_generation,active_kernel_policy_digest,
		 expected_local_open_risk_micros,expected_local_held_cash_micros,expected_other_close_qty)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10::jsonb,$11,$12,
		 $13,$14,$15,$16,$17,$18,$19)
		RETURNING created_at`, input.ID, input.AttemptID, input.FencingToken,
		input.AccountID, input.Effect, input.ObservationID, input.ObservationGeneration,
		observationDigest, input.TargetBrokerOrderID, facts, factsDigest[:], input.ExpiresAt,
		input.Ledger, input.ActivePolicyRevisionID, input.ActivePolicyGeneration, activePolicyDigest,
		int64(input.ExpectedLocalOpenRisk), int64(input.ExpectedLocalHeldCash), int64(input.ExpectedOtherCloseQty),
	).Scan(&manifest.CreatedAt)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	manifest = PreEffectManifest{
		ID: input.ID, AttemptID: input.AttemptID, FencingToken: input.FencingToken,
		AccountID: input.AccountID, Effect: input.Effect, ObservationID: input.ObservationID,
		ObservationGeneration:     input.ObservationGeneration,
		ObservationManifestDigest: hex.EncodeToString(observationDigest),
		TargetBrokerOrderID:       input.TargetBrokerOrderID, Facts: facts,
		FactsDigest: hex.EncodeToString(factsDigest[:]), ExpiresAt: input.ExpiresAt,
		CreatedAt: manifest.CreatedAt,
		Ledger:    input.Ledger, ActivePolicyRevisionID: input.ActivePolicyRevisionID,
		ActivePolicyGeneration: input.ActivePolicyGeneration,
		ActivePolicyDigest:     hex.EncodeToString(activePolicyDigest),
		ExpectedLocalOpenRisk:  input.ExpectedLocalOpenRisk,
		ExpectedLocalHeldCash:  input.ExpectedLocalHeldCash,
		ExpectedOtherCloseQty:  input.ExpectedOtherCloseQty,
	}
	if err := insertEvent(ctx, tx, "execution_pre_effect_recorded", map[string]any{
		"attempt_id": input.AttemptID, "fencing_token": input.FencingToken,
		"manifest_id": input.ID, "observation_id": input.ObservationID,
		"observation_generation": input.ObservationGeneration, "effect": input.Effect,
		"facts_digest": manifest.FactsDigest, "expires_at": input.ExpiresAt,
	}); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return &manifest, nil
}

func canonicalJSONObject(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) == 0 || !json.Valid(encoded) {
		return nil, fmt.Errorf("canonical JSON is unavailable")
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("canonical JSON must be an object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON is unavailable")
	}
	return canonical, nil
}

func expectedPreEffect(intent, action string) string {
	if intent == "place" {
		switch action {
		case "open":
			return "place_open"
		case "close":
			return "place_close"
		}
	}
	if intent == "cancel" {
		if action == "cancel" {
			return "cancel_order"
		}
		if action == "open" || action == "close" {
			return "replace_cancel"
		}
	}
	return ""
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for i := range left {
		different |= left[i] ^ right[i]
	}
	return different == 0
}

func validatePreEffectManifestForSend(
	ctx context.Context,
	tx *sql.Tx,
	manifestID, attemptID string,
	fencingToken, sendOrdinal int,
	databaseNow time.Time,
) error {
	if strings.TrimSpace(manifestID) == "" {
		return preEffectStaleError("pre-effect manifest is required")
	}
	var expiresAt time.Time
	var observationStatus, observationPurpose, ledger, effect, operationID, symbol string
	var attemptIntent, operationClass, operationStatus string
	var facts, factsDigest, observationDigest, persistedObservationDigest, activePolicyDigest []byte
	var activePolicyRevisionID, activePolicyGeneration int64
	var attemptSeq int
	var operationExpiresAt sql.NullTime
	var operationPolicyRevisionID sql.NullInt64
	var expectedOpenRisk, expectedHeldCash, expectedOtherClose int64
	err := tx.QueryRowContext(ctx, `SELECT manifest.expires_at,manifest.facts::text,
		manifest.facts_digest,manifest.observation_manifest_digest,observation.manifest_digest,
		observation.status,observation.purpose,manifest.ledger,manifest.effect,
		manifest.active_kernel_policy_revision_id,manifest.active_kernel_policy_generation,
		manifest.active_kernel_policy_digest,manifest.expected_local_open_risk_micros,
		manifest.expected_local_held_cash_micros,manifest.expected_other_close_qty,
		attempt.operation_id,COALESCE(operation.payload->>'symbol',operation.payload->>'underlying',''),
		attempt.intent,attempt.seq,operation.class,operation.status,operation.expires_at,
		operation.kernel_policy_revision_id
		FROM execution_pre_effect_manifest AS manifest
		JOIN broker_observation AS observation ON observation.id=manifest.broker_observation_id
		JOIN execution_attempt AS attempt ON attempt.id=manifest.execution_attempt_id
		JOIN operations AS operation ON operation.id=attempt.operation_id
		WHERE manifest.id=$1 AND manifest.execution_attempt_id=$2 AND manifest.fencing_token=$3`,
		manifestID, attemptID, fencingToken).Scan(&expiresAt, &facts, &factsDigest,
		&observationDigest, &persistedObservationDigest, &observationStatus, &observationPurpose,
		&ledger, &effect, &activePolicyRevisionID, &activePolicyGeneration, &activePolicyDigest,
		&expectedOpenRisk, &expectedHeldCash, &expectedOtherClose, &operationID, &symbol,
		&attemptIntent, &attemptSeq, &operationClass, &operationStatus, &operationExpiresAt,
		&operationPolicyRevisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return preEffectStaleError("pre-effect manifest does not match attempt fencing")
	}
	if err != nil {
		return normalizeDBError(err)
	}
	canonical, err := canonicalJSONObject(json.RawMessage(facts))
	if err != nil {
		return preEffectStaleError("pre-effect manifest facts are invalid")
	}
	digest := sha256.Sum256(canonical)
	if !databaseNow.Before(expiresAt) || observationStatus != "complete" || observationPurpose != "pre_effect" ||
		ledger != "live" || activePolicyRevisionID <= 0 || activePolicyGeneration <= 0 ||
		expectedOpenRisk < 0 || expectedHeldCash < 0 || expectedOtherClose < 0 ||
		!equalBytes(digest[:], factsDigest) || !equalBytes(observationDigest, persistedObservationDigest) {
		return preEffectStaleError("pre-effect manifest is expired or inconsistent")
	}
	var currentPolicyRevisionID, currentPolicyGeneration int64
	var currentPolicyDigest []byte
	if err := tx.QueryRowContext(ctx, `SELECT head.revision_id,head.generation,revision.digest
		FROM kernel_policy_head AS head
		JOIN kernel_policy_revision AS revision ON revision.id=head.revision_id
		WHERE head.singleton=true`).Scan(
		&currentPolicyRevisionID, &currentPolicyGeneration, &currentPolicyDigest,
	); err != nil {
		return normalizeDBError(err)
	}
	if currentPolicyRevisionID != activePolicyRevisionID || currentPolicyGeneration != activePolicyGeneration ||
		!equalBytes(currentPolicyDigest, activePolicyDigest) {
		return preEffectStaleError("active kernel policy changed after pre-effect evaluation")
	}
	reviewApproved := operationClass == "C" && operationStatus == "approved"
	ttlRequired := effect != "replace_cancel" && !(attemptIntent == "place" && attemptSeq > 1)
	if operationPolicyRevisionID.Valid && ttlRequired && !reviewApproved &&
		(!operationExpiresAt.Valid || !databaseNow.Before(operationExpiresAt.Time)) {
		return preEffectStaleError("proposal expired after pre-effect evaluation")
	}
	resources, err := ledgerResources(ctx, tx, ledger, operationID)
	if err != nil {
		return err
	}
	if int64(resources.OpenRisk) != expectedOpenRisk || int64(resources.HeldCash) != expectedHeldCash {
		return preEffectStaleError("local resources changed after pre-effect evaluation")
	}
	if effect == "place_close" {
		var otherClose int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(remaining_qty),0)
			FROM close_reservation
			WHERE ledger=$1 AND symbol=$2 AND state='held' AND operation_id<>$3::uuid`,
			ledger, symbol, operationID).Scan(&otherClose); err != nil {
			return normalizeDBError(err)
		}
		if otherClose != expectedOtherClose {
			return preEffectStaleError("closable reservations changed after pre-effect evaluation")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO execution_pre_effect_binding
		(execution_attempt_id,send_ordinal,fencing_token,manifest_id)
		VALUES ($1,$2,$3,$4)`, attemptID, sendOrdinal, fencingToken, manifestID); err != nil {
		return normalizeDBError(err)
	}
	return nil
}

func lockPreEffectEvaluationScope(ctx context.Context, tx *sql.Tx, manifestID string) error {
	var ledger string
	if err := tx.QueryRowContext(ctx, `SELECT ledger FROM execution_pre_effect_manifest
		WHERE id=$1 AND ledger IS NOT NULL`, manifestID).Scan(&ledger); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return preEffectStaleError("pre-effect manifest lacks an aggregate evaluation")
		}
		return normalizeDBError(err)
	}
	if ledger != "live" {
		return preEffectStaleError("pre-effect manifest ledger is invalid")
	}
	// Match the global proposal/review order: policy -> ledger -> halt -> live
	// gate. A shared policy lock freezes the active revision while allowing
	// independent sends to validate against the same authority.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, kernelPolicyLockKey); err != nil {
		return normalizeDBError(err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(false)); err != nil {
		return normalizeDBError(err)
	}
	return nil
}

func preEffectStaleError(message string) error {
	return fmt.Errorf("%w: %s", ErrPreEffectStale, message)
}
