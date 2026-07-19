package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

const (
	liveCanaryLegacyAuthorityVersion = 1
	liveCanaryAuthorityVersion       = 2
)

var (
	ErrLiveCanaryAuthorityMissing = errors.New("live canary authority is missing")
	ErrLiveCanaryAuthorityInvalid = errors.New("live canary authority is invalid")
	ErrLiveCanaryRevisionConflict = errors.New("live canary revision conflict")
	ErrLiveCanaryWideningUnsafe   = errors.New("live canary widening lacks completed-day attestation")
	// ErrLiveCanaryRaiseUnsafe is retained as a source-compatible alias for
	// callers that predate the two-dimensional widening rule.
	ErrLiveCanaryRaiseUnsafe = ErrLiveCanaryWideningUnsafe
)

type LiveCanaryRevision struct {
	ID                        int64        `json:"revision_id"`
	Generation                int64        `json:"generation"`
	AuthorityVersion          int          `json:"authority_version"`
	DailyAuthorizedRiskCapUSD units.Micros `json:"daily_authorized_risk_cap_usd"`
	CleanDaysBeforeRaise      int          `json:"clean_days_before_raise"`
	EffectiveMarketDay        time.Time    `json:"effective_market_day"`
	RecordedAt                time.Time    `json:"recorded_at"`
	RecordedBy                string       `json:"recorded_by"`
	Reason                    string       `json:"reason"`
	ChangeClass               string       `json:"change_class"`
	RequiredAttestations      int          `json:"required_attestations"`
	WideningAccountID         string       `json:"widening_account_id,omitempty"`
	AttestationIDs            []int64      `json:"attestation_ids"`
	ObservedAt                time.Time    `json:"observed_at"`
	auditEventPresent         bool
}

type RecordLiveCanaryRevisionInput struct {
	DailyAuthorizedRiskCapUSD units.Micros
	CleanDaysBeforeRaise      int
	ExpectedRevisionID        int64
	AccountID                 string
	RecordedBy                string
	Reason                    string
}

// RecordLiveCanaryRevision is the deployment-only governance path. The latest
// authoritative immutable row is the active revision and its ID is the
// generation; there is deliberately no generic settings table or head service.
//
// Initial bootstrap and tightening remain immediate. K1C permits widening only
// when the same transaction validates the required immutable completed-day
// attestations; day_open alone is never evidence. Every change serializes with
// Live grant admission on the stable Live ledger lock.
func (s *Store) RecordLiveCanaryRevision(input RecordLiveCanaryRevisionInput) (*LiveCanaryRevision, error) {
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.RecordedBy = strings.TrimSpace(input.RecordedBy)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.DailyAuthorizedRiskCapUSD <= 0 || input.CleanDaysBeforeRaise <= 0 ||
		input.ExpectedRevisionID < 0 || len(input.AccountID) > 200 ||
		input.RecordedBy == "" || len(input.RecordedBy) > 200 ||
		input.Reason == "" || len(input.Reason) > 1000 {
		return nil, fmt.Errorf("invalid live canary revision")
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
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(false)); err != nil {
		return nil, normalizeDBError(err)
	}

	observedAt, marketDay, err := liveCanaryDatabaseTime(ctx, tx, s.marketTZ)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	previous, err := latestLiveCanaryRevision(ctx, tx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, normalizeDBError(err)
	}
	if err == nil {
		previous.ObservedAt = observedAt
		if validationErr := validateLiveCanaryAuthority(previous, marketDay); validationErr != nil {
			return nil, validationErr
		}
		if previous.DailyAuthorizedRiskCapUSD == input.DailyAuthorizedRiskCapUSD &&
			previous.CleanDaysBeforeRaise == input.CleanDaysBeforeRaise {
			if err := tx.Commit(); err != nil {
				return nil, normalizeDBError(err)
			}
			committed = true
			return previous, nil
		}
		if input.ExpectedRevisionID != previous.ID {
			return nil, fmt.Errorf("%w: expected %d, active %d", ErrLiveCanaryRevisionConflict,
				input.ExpectedRevisionID, previous.ID)
		}
	} else if input.ExpectedRevisionID != 0 {
		return nil, fmt.Errorf("%w: expected %d, active revision is missing",
			ErrLiveCanaryRevisionConflict, input.ExpectedRevisionID)
	}

	changeClass := "initial"
	var wideningEvidence []LiveCanaryDayAttestation
	requiredAttestations := 0
	if previous != nil {
		changeClass = classifyLiveCanaryChange(previous.DailyAuthorizedRiskCapUSD,
			previous.CleanDaysBeforeRaise, input.DailyAuthorizedRiskCapUSD,
			input.CleanDaysBeforeRaise)
		if changeClass == "widen" {
			wideningEvidence, err = eligibleLiveCanaryWideningEvidence(ctx, tx, s.marketTZ,
				input.AccountID, previous, input.CleanDaysBeforeRaise, marketDay)
			if err != nil {
				if errors.Is(err, ErrLiveCanaryWideningUnsafe) ||
					errors.Is(err, ErrLiveCanaryDayEvidenceInvalid) {
					return nil, fmt.Errorf("%w: %v", ErrLiveCanaryWideningUnsafe, err)
				}
				return nil, err
			}
			requiredAttestations = liveCanaryRequiredAttestations(previous.CleanDaysBeforeRaise,
				input.CleanDaysBeforeRaise)
		}
	}

	revision := &LiveCanaryRevision{
		AuthorityVersion:          liveCanaryAuthorityVersion,
		DailyAuthorizedRiskCapUSD: input.DailyAuthorizedRiskCapUSD,
		CleanDaysBeforeRaise:      input.CleanDaysBeforeRaise,
		EffectiveMarketDay:        marketDay,
		RecordedBy:                input.RecordedBy,
		Reason:                    input.Reason,
		ChangeClass:               changeClass,
		RequiredAttestations:      requiredAttestations,
		AttestationIDs:            []int64{},
		ObservedAt:                observedAt,
	}
	if changeClass == "widen" {
		revision.WideningAccountID = input.AccountID
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO live_canary_revision
		(daily_authorized_risk_micros,clean_days_before_raise,effective_market_day,
		 authority_version,recorded_by,reason,change_class,required_attestations,recorded_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,clock_timestamp())
		RETURNING id,recorded_at`, int64(input.DailyAuthorizedRiskCapUSD),
		input.CleanDaysBeforeRaise, marketDay, liveCanaryAuthorityVersion,
		input.RecordedBy, input.Reason, changeClass, requiredAttestations).Scan(
		&revision.ID, &revision.RecordedAt)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.Generation = revision.ID
	revision.ObservedAt = revision.RecordedAt
	if changeClass == "widen" {
		if err := insertLiveCanaryWideningEvidence(ctx, tx, revision.ID, wideningEvidence); err != nil {
			return nil, err
		}
		for index := range wideningEvidence {
			revision.AttestationIDs = append(revision.AttestationIDs, wideningEvidence[index].ID)
		}
	}
	payload := map[string]any{
		"revision_id": revision.ID, "generation": revision.Generation,
		"authority_version": liveCanaryAuthorityVersion, "change": changeClass,
		"daily_authorized_risk_cap": input.DailyAuthorizedRiskCapUSD,
		"clean_days_before_raise":   input.CleanDaysBeforeRaise,
		"effective_market_day":      marketDay, "recorded_by": input.RecordedBy,
		"reason": input.Reason, "required_attestations": requiredAttestations,
		"attestation_ids": revision.AttestationIDs,
	}
	if changeClass == "widen" {
		payload["account_id"] = input.AccountID
	}
	if previous != nil {
		payload["previous_revision_id"] = previous.ID
		payload["previous_daily_authorized_risk_cap"] = previous.DailyAuthorizedRiskCapUSD
		payload["previous_clean_days_before_raise"] = previous.CleanDaysBeforeRaise
	}
	if err := insertEvent(ctx, tx, "live_canary_revision_recorded", payload); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	committed = true
	revision.auditEventPresent = true
	return revision, nil
}

// LoadLiveCanaryAuthority reads the only runtime source for the Live canary.
// Legacy pre-K0 rows have no authority_version and are intentionally ignored.
func (s *Store) LoadLiveCanaryAuthority() (*LiveCanaryRevision, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	revision, err := latestLiveCanaryRevision(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLiveCanaryAuthorityMissing
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	// Read the observation clock after the revision. Under READ COMMITTED a
	// concurrent activation may commit between statements; returning the older
	// immutable row for one read is safe, while reading the clock first could
	// falsely label a newly committed row as future-dated.
	observedAt, marketDay, err := liveCanaryDatabaseTime(ctx, tx, s.marketTZ)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.ObservedAt = observedAt
	if err := validateLiveCanaryAuthority(revision, marketDay); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return revision, nil
}

// LiveCanaryAuthority is called inside Live admission transactions. It
// re-acquires the stable Live ledger lock (transaction advisory locks are
// re-entrant) so the revision it returns cannot change before the grant and
// its audit event commit.
func (t *ledgerTx) LiveCanaryAuthority(marketDay time.Time) (*LiveCanaryRevision, error) {
	if marketDay.IsZero() {
		return nil, fmt.Errorf("%w: market day is missing", ErrLiveCanaryAuthorityInvalid)
	}
	if _, err := t.tx.ExecContext(t.ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(false)); err != nil {
		return nil, normalizeDBError(err)
	}
	observedAt, currentMarketDay, err := liveCanaryDatabaseTime(t.ctx, t.tx, t.marketTZ)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if !sameMarketDate(marketDay, currentMarketDay) {
		return nil, fmt.Errorf("%w: admission market day is stale", ErrLiveCanaryAuthorityInvalid)
	}
	revision, err := latestLiveCanaryRevision(t.ctx, t.tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLiveCanaryAuthorityMissing
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	revision.ObservedAt = observedAt
	if err := validateLiveCanaryAuthority(revision, marketDay); err != nil {
		return nil, err
	}
	return revision, nil
}

func classifyLiveCanaryChange(oldCap units.Micros, oldCleanDays int, newCap units.Micros, newCleanDays int) string {
	if oldCap == newCap && oldCleanDays == newCleanDays {
		return "noop"
	}
	if newCap > oldCap || newCleanDays < oldCleanDays {
		return "widen"
	}
	return "tighten"
}

func latestLiveCanaryRevision(ctx context.Context, tx *sql.Tx) (*LiveCanaryRevision, error) {
	var revision LiveCanaryRevision
	var risk int64
	var authorityVersion sql.NullInt64
	var recordedBy, reason, changeClass sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT r.id,r.daily_authorized_risk_micros,
		r.clean_days_before_raise,r.effective_market_day,r.recorded_at,
		r.authority_version,r.recorded_by,r.reason,r.change_class,r.required_attestations,
		EXISTS (SELECT 1 FROM events e
			WHERE e.kind='live_canary_revision_recorded'
			  AND e.payload->>'revision_id'=r.id::text
			  AND e.payload->>'generation'=r.id::text
			  AND e.payload->>'change'=r.change_class)
		FROM live_canary_revision r
		WHERE r.authority_version IS NOT NULL
		ORDER BY r.id DESC LIMIT 1`).Scan(
		&revision.ID, &risk, &revision.CleanDaysBeforeRaise,
		&revision.EffectiveMarketDay, &revision.RecordedAt, &authorityVersion,
		&recordedBy, &reason, &changeClass, &revision.RequiredAttestations,
		&revision.auditEventPresent,
	)
	if err != nil {
		return nil, err
	}
	revision.Generation = revision.ID
	revision.AuthorityVersion = int(authorityVersion.Int64)
	revision.DailyAuthorizedRiskCapUSD = units.Micros(risk)
	revision.RecordedBy = recordedBy.String
	revision.Reason = reason.String
	revision.ChangeClass = changeClass.String
	if err := loadLiveCanaryRevisionEvidence(ctx, tx, &revision); err != nil {
		return nil, err
	}
	return &revision, nil
}

func validateLiveCanaryAuthority(revision *LiveCanaryRevision, marketDay time.Time) error {
	if revision == nil || revision.ID <= 0 || revision.Generation != revision.ID ||
		(revision.AuthorityVersion != liveCanaryLegacyAuthorityVersion &&
			revision.AuthorityVersion != liveCanaryAuthorityVersion) ||
		revision.DailyAuthorizedRiskCapUSD <= 0 || revision.CleanDaysBeforeRaise <= 0 ||
		revision.EffectiveMarketDay.IsZero() || revision.RecordedAt.IsZero() ||
		strings.TrimSpace(revision.RecordedBy) == "" || strings.TrimSpace(revision.Reason) == "" ||
		!revision.auditEventPresent {
		return ErrLiveCanaryAuthorityInvalid
	}
	switch {
	case revision.AuthorityVersion == liveCanaryLegacyAuthorityVersion:
		if revision.ChangeClass != "initial" && revision.ChangeClass != "tighten" ||
			revision.RequiredAttestations != 0 || len(revision.AttestationIDs) != 0 {
			return ErrLiveCanaryAuthorityInvalid
		}
	case revision.ChangeClass == "initial" || revision.ChangeClass == "tighten":
		if revision.RequiredAttestations != 0 || len(revision.AttestationIDs) != 0 {
			return ErrLiveCanaryAuthorityInvalid
		}
	case revision.ChangeClass == "widen":
		if revision.RequiredAttestations <= 0 ||
			len(revision.AttestationIDs) != revision.RequiredAttestations ||
			strings.TrimSpace(revision.WideningAccountID) == "" {
			return ErrLiveCanaryAuthorityInvalid
		}
	default:
		return ErrLiveCanaryAuthorityInvalid
	}
	if revision.ObservedAt.IsZero() || revision.RecordedAt.After(revision.ObservedAt) ||
		marketDateAfter(revision.EffectiveMarketDay, marketDay) {
		return fmt.Errorf("%w: authority is future-dated", ErrLiveCanaryAuthorityInvalid)
	}
	return nil
}

func liveCanaryDatabaseTime(ctx context.Context, tx *sql.Tx, marketTZ string) (time.Time, time.Time, error) {
	var observedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&observedAt); err != nil {
		return time.Time{}, time.Time{}, err
	}
	location, err := time.LoadLocation(marketTZ)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	year, month, day := observedAt.In(location).Date()
	return observedAt, time.Date(year, month, day, 0, 0, 0, 0, time.UTC), nil
}

func sameMarketDate(left, right time.Time) bool {
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	return ly == ry && lm == rm && ld == rd
}

func marketDateAfter(left, right time.Time) bool {
	ly, lm, ld := left.Date()
	ry, rm, rd := right.Date()
	if ly != ry {
		return ly > ry
	}
	if lm != rm {
		return lm > rm
	}
	return ld > rd
}
