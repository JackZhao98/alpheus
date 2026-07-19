package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"alpheus/kernel/internal/policy"
)

const kernelPolicyLockKey int64 = 0x414c50484b504f4c // "ALPHKPOL"

var (
	ErrKernelPolicyAuthorityMissing = errors.New("kernel policy authority is missing")
	ErrKernelPolicyAuthorityInvalid = errors.New("kernel policy authority is invalid")
	ErrKernelPolicyRevisionConflict = errors.New("kernel policy revision conflict")
)

type KernelPolicyRevision struct {
	ID            int64              `json:"revision_id"`
	Generation    int64              `json:"generation"`
	SchemaVersion int                `json:"schema_version"`
	Digest        string             `json:"digest"`
	Policy        policy.Policy      `json:"policy"`
	RecordedAt    time.Time          `json:"recorded_at"`
	RecordedBy    string             `json:"recorded_by"`
	Reason        string             `json:"reason"`
	ChangeClass   policy.ChangeClass `json:"change_class"`
	ActivatedAt   time.Time          `json:"activated_at"`
	ActivatedBy   string             `json:"activated_by"`
	ObservedAt    time.Time          `json:"observed_at"`
	auditPresent  bool
	digestBytes   []byte
}

type RecordKernelPolicyRevisionInput struct {
	Policy             policy.Policy
	ExpectedGeneration int64
	RecordedBy         string
	Reason             string
}

// RecordKernelPolicyRevision is the explicit K1 governance path. It creates
// one immutable candidate and activates it under a stable database scope lock.
// Exact-policy retries are idempotent. A different policy requires the active
// generation, so concurrent writers cannot silently overwrite one another.
func (s *Store) RecordKernelPolicyRevision(input RecordKernelPolicyRevisionInput) (*KernelPolicyRevision, error) {
	input.RecordedBy = strings.TrimSpace(input.RecordedBy)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ExpectedGeneration < 0 || input.RecordedBy == "" || len(input.RecordedBy) > 200 ||
		input.Reason == "" || len(input.Reason) > 1000 {
		return nil, fmt.Errorf("invalid kernel policy revision")
	}
	normalized, body, digest, err := policy.Canonical(input.Policy)
	if err != nil {
		return nil, fmt.Errorf("invalid kernel policy revision: %w", err)
	}

	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, kernelPolicyLockKey); err != nil {
		return nil, normalizeDBError(err)
	}

	active, err := activeKernelPolicyRevision(ctx, tx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, normalizeDBError(err)
	}
	if err == nil {
		observedAt, clockErr := databaseClock(ctx, tx)
		if clockErr != nil {
			return nil, normalizeDBError(clockErr)
		}
		active.ObservedAt = observedAt
		if validationErr := validateKernelPolicyAuthority(active); validationErr != nil {
			return nil, validationErr
		}
		if bytes.Equal(active.digestBytes, digest[:]) {
			if err := tx.Commit(); err != nil {
				return nil, normalizeDBError(err)
			}
			committed = true
			return active, nil
		}
		if input.ExpectedGeneration != active.Generation {
			conflictErr := fmt.Errorf("%w: expected %d, active %d", ErrKernelPolicyRevisionConflict,
				input.ExpectedGeneration, active.Generation)
			if err := recordKernelPolicyConflict(ctx, tx, input, hex.EncodeToString(digest[:]), active.Generation); err != nil {
				return nil, normalizeDBError(err)
			}
			if err := tx.Commit(); err != nil {
				return nil, normalizeDBError(err)
			}
			committed = true
			return nil, conflictErr
		}
	} else if input.ExpectedGeneration != 0 {
		conflictErr := fmt.Errorf("%w: expected %d, active generation is missing",
			ErrKernelPolicyRevisionConflict, input.ExpectedGeneration)
		if err := recordKernelPolicyConflict(ctx, tx, input, hex.EncodeToString(digest[:]), 0); err != nil {
			return nil, normalizeDBError(err)
		}
		if err := tx.Commit(); err != nil {
			return nil, normalizeDBError(err)
		}
		committed = true
		return nil, conflictErr
	}

	changeClass := policy.ChangeInitial
	nextGeneration := int64(1)
	if active != nil {
		changeClass, err = policy.ClassifyChange(active.Policy, normalized)
		if err != nil {
			return nil, fmt.Errorf("classify kernel policy revision: %w", err)
		}
		nextGeneration = active.Generation + 1
		if nextGeneration <= active.Generation {
			return nil, ErrKernelPolicyAuthorityInvalid
		}
	}

	revision := &KernelPolicyRevision{
		Generation: nextGeneration, SchemaVersion: policy.SchemaVersion,
		Digest: hex.EncodeToString(digest[:]), Policy: normalized,
		RecordedBy: input.RecordedBy, Reason: input.Reason,
		ChangeClass: changeClass, ActivatedBy: input.RecordedBy,
		digestBytes: append([]byte(nil), digest[:]...),
	}
	if err := tx.QueryRowContext(ctx, `INSERT INTO kernel_policy_revision
		(schema_version,policy,digest,recorded_by,reason,change_class)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id,recorded_at`, policy.SchemaVersion, string(body), digest[:],
		input.RecordedBy, input.Reason, string(changeClass)).Scan(&revision.ID, &revision.RecordedAt); err != nil {
		return nil, normalizeDBError(err)
	}
	if active == nil {
		err = tx.QueryRowContext(ctx, `INSERT INTO kernel_policy_head
			(singleton,revision_id,generation,activated_by,reason)
			VALUES (true,$1,1,$2,$3) RETURNING activated_at`, revision.ID,
			input.RecordedBy, input.Reason).Scan(&revision.ActivatedAt)
	} else {
		err = tx.QueryRowContext(ctx, `UPDATE kernel_policy_head
			SET revision_id=$1,generation=$2,activated_at=clock_timestamp(),activated_by=$3,reason=$4
			WHERE singleton=true AND generation=$5 RETURNING activated_at`, revision.ID,
			nextGeneration, input.RecordedBy, input.Reason, input.ExpectedGeneration).Scan(&revision.ActivatedAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKernelPolicyRevisionConflict
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	payload := map[string]any{
		"revision_id": revision.ID, "generation": revision.Generation,
		"schema_version": revision.SchemaVersion, "digest": revision.Digest,
		"change": revision.ChangeClass, "recorded_by": revision.RecordedBy,
		"reason": revision.Reason,
	}
	if active != nil {
		payload["previous_revision_id"] = active.ID
		payload["previous_generation"] = active.Generation
		payload["previous_digest"] = active.Digest
	}
	if err := insertEventAt(ctx, tx, "kernel_policy_activated", payload, revision.ActivatedAt); err != nil {
		return nil, normalizeDBError(err)
	}
	observedAt, err := databaseClock(ctx, tx)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.ObservedAt = observedAt
	revision.auditPresent = true
	if err := validateKernelPolicyAuthority(revision); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	committed = true
	return revision, nil
}

// LoadKernelPolicyAuthority reads and verifies the single K1 database source.
// It never receives a file fallback.
func (s *Store) LoadKernelPolicyAuthority() (*KernelPolicyRevision, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	revision, err := activeKernelPolicyRevision(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKernelPolicyAuthorityMissing
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.ObservedAt, err = databaseClock(ctx, tx)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if err := validateKernelPolicyAuthority(revision); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return revision, nil
}

// LoadBoundKernelPolicy verifies immutable historical authority outside a
// proposal/review transaction. Recovery and repricing use it to preserve the
// exact policy that authorized already-staged work.
func (s *Store) LoadBoundKernelPolicy(operation *OperationRow) (*KernelPolicyRevision, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	revision, err := (&ledgerTx{tx: tx, ctx: ctx, marketTZ: s.marketTZ}).BoundKernelPolicy(operation)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return revision, nil
}

// KernelPolicyAuthority binds a new operation while holding the stable policy
// lock until the surrounding proposal/review transaction commits.
func (t *ledgerTx) KernelPolicyAuthority() (*KernelPolicyRevision, error) {
	if _, err := t.tx.ExecContext(t.ctx, `SELECT pg_advisory_xact_lock_shared($1)`, kernelPolicyLockKey); err != nil {
		return nil, normalizeDBError(err)
	}
	revision, err := activeKernelPolicyRevision(t.ctx, t.tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKernelPolicyAuthorityMissing
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.ObservedAt, err = databaseClock(t.ctx, t.tx)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if err := validateKernelPolicyAuthority(revision); err != nil {
		return nil, err
	}
	return revision, nil
}

// BoundKernelPolicy loads immutable historical authority for review and
// recovery. It validates the operation's three-part binding and the original
// activation event, but does not require that revision to remain the head.
func (t *ledgerTx) BoundKernelPolicy(operation *OperationRow) (*KernelPolicyRevision, error) {
	if operation == nil || operation.KernelPolicyRevisionID <= 0 ||
		operation.KernelPolicyGeneration <= 0 || len(operation.KernelPolicyDigest) != 64 {
		return nil, ErrKernelPolicyAuthorityInvalid
	}
	var revision KernelPolicyRevision
	var rawPolicy, digest []byte
	var changeClass string
	err := t.tx.QueryRowContext(t.ctx, `SELECT r.id,r.schema_version,r.policy,r.digest,
		r.recorded_at,r.recorded_by,r.reason,r.change_class,a.activated_at,a.activated_by
		FROM kernel_policy_revision r
		JOIN LATERAL (
		  SELECT e.ts AS activated_at,e.payload->>'recorded_by' AS activated_by
		  FROM events e
		  WHERE e.kind='kernel_policy_activated'
		    AND e.payload->>'revision_id'=r.id::text
		    AND e.payload->>'generation'=$2::text
		  ORDER BY e.id LIMIT 1
		) a ON true
		WHERE r.id=$1 AND encode(r.digest,'hex')=$3`, operation.KernelPolicyRevisionID,
		operation.KernelPolicyGeneration, operation.KernelPolicyDigest).Scan(
		&revision.ID, &revision.SchemaVersion, &rawPolicy, &digest,
		&revision.RecordedAt, &revision.RecordedBy, &revision.Reason, &changeClass,
		&revision.ActivatedAt, &revision.ActivatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKernelPolicyAuthorityInvalid
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.Generation = operation.KernelPolicyGeneration
	revision.ChangeClass = policy.ChangeClass(changeClass)
	revision.Digest = hex.EncodeToString(digest)
	revision.digestBytes = append([]byte(nil), digest...)
	revision.auditPresent = true
	revision.ObservedAt, err = databaseClock(t.ctx, t.tx)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.Policy, err = policy.DecodeCanonical(revision.SchemaVersion, rawPolicy, digest)
	if err != nil {
		return nil, ErrKernelPolicyAuthorityInvalid
	}
	if err := validateKernelPolicyAuthority(&revision); err != nil {
		return nil, err
	}
	return &revision, nil
}

func activeKernelPolicyRevision(ctx context.Context, tx *sql.Tx) (*KernelPolicyRevision, error) {
	var revision KernelPolicyRevision
	var rawPolicy, digest []byte
	var changeClass string
	err := tx.QueryRowContext(ctx, `SELECT r.id,h.generation,r.schema_version,r.policy,r.digest,
		r.recorded_at,r.recorded_by,r.reason,r.change_class,h.activated_at,h.activated_by,
		EXISTS (SELECT 1 FROM events e WHERE e.kind='kernel_policy_activated'
		  AND e.payload->>'revision_id'=r.id::text
		  AND e.payload->>'generation'=h.generation::text)
		FROM kernel_policy_head h
		JOIN kernel_policy_revision r ON r.id=h.revision_id
		WHERE h.singleton=true`).Scan(&revision.ID, &revision.Generation,
		&revision.SchemaVersion, &rawPolicy, &digest, &revision.RecordedAt,
		&revision.RecordedBy, &revision.Reason, &changeClass,
		&revision.ActivatedAt, &revision.ActivatedBy, &revision.auditPresent)
	if err != nil {
		return nil, err
	}
	decoded, err := policy.DecodeCanonical(revision.SchemaVersion, rawPolicy, digest)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKernelPolicyAuthorityInvalid, err)
	}
	revision.Policy = decoded
	revision.Digest = hex.EncodeToString(digest)
	revision.digestBytes = append([]byte(nil), digest...)
	revision.ChangeClass = policy.ChangeClass(changeClass)
	return &revision, nil
}

func validateKernelPolicyAuthority(revision *KernelPolicyRevision) error {
	if revision == nil || revision.ID <= 0 || revision.Generation <= 0 ||
		revision.SchemaVersion != policy.SchemaVersion || len(revision.digestBytes) != 32 ||
		strings.TrimSpace(revision.RecordedBy) == "" || strings.TrimSpace(revision.Reason) == "" ||
		strings.TrimSpace(revision.ActivatedBy) == "" || revision.RecordedAt.IsZero() ||
		revision.ActivatedAt.IsZero() || revision.ObservedAt.IsZero() || !revision.auditPresent {
		return ErrKernelPolicyAuthorityInvalid
	}
	switch revision.ChangeClass {
	case policy.ChangeInitial, policy.ChangeTighten, policy.ChangeWiden, policy.ChangeMixed:
	default:
		return ErrKernelPolicyAuthorityInvalid
	}
	if revision.RecordedAt.After(revision.ActivatedAt) || revision.ActivatedAt.After(revision.ObservedAt) {
		return fmt.Errorf("%w: authority is future-dated", ErrKernelPolicyAuthorityInvalid)
	}
	return nil
}

func databaseClock(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var observedAt time.Time
	err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&observedAt)
	return observedAt, err
}

func recordKernelPolicyConflict(ctx context.Context, tx *sql.Tx, input RecordKernelPolicyRevisionInput, digest string, activeGeneration int64) error {
	return insertEvent(ctx, tx, "kernel_policy_activation_conflict", map[string]any{
		"expected_generation": input.ExpectedGeneration,
		"active_generation":   activeGeneration,
		"candidate_digest":    digest,
		"recorded_by":         input.RecordedBy,
		"reason":              input.Reason,
	})
}
