package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

const liveCanarySessionCloseHour = 20

var (
	ErrLiveCanaryDayNotComplete      = errors.New("live canary market day is not complete")
	ErrLiveCanaryDayEvidenceInvalid  = errors.New("live canary completed-day evidence is invalid")
	ErrLiveCanaryDayAttestationClash = errors.New("live canary day attestation conflicts with existing evidence")
)

// LiveCanaryDayAttestation is immutable evidence that one past Live market day
// had final Provider PnL, no unresolved unknown, and a complete account
// observation reconciled against the exact local broker state. It is evidence
// for canary governance only; it does not authorize an order by itself.
type LiveCanaryDayAttestation struct {
	ID                           int64        `json:"attestation_id"`
	AccountID                    string       `json:"account_id"`
	MarketDay                    time.Time    `json:"market_day"`
	LiveCanaryRevisionID         int64        `json:"live_canary_revision_id"`
	KernelPolicyRevisionID       int64        `json:"kernel_policy_revision_id"`
	KernelPolicyGeneration       int64        `json:"kernel_policy_generation"`
	KernelPolicyDigest           string       `json:"kernel_policy_digest"`
	DayOpenEquity                units.Micros `json:"day_open_equity"`
	LiveGrantCount               int          `json:"live_grant_count"`
	AuthorizedRisk               units.Micros `json:"authorized_risk"`
	LocalRealizedPnL             units.Micros `json:"local_realized_pnl"`
	ProviderRealizedPnL          units.Micros `json:"provider_realized_pnl"`
	PnLDifference                units.Micros `json:"pnl_difference"`
	PnLTolerance                 units.Micros `json:"pnl_tolerance"`
	PnLObservedAt                time.Time    `json:"pnl_observed_at"`
	BrokerObservationID          string       `json:"broker_observation_id"`
	BrokerObservationGeneration  int64        `json:"broker_observation_generation"`
	BrokerObservationCompletedAt time.Time    `json:"broker_observation_completed_at"`
	BrokerReconciledAt           time.Time    `json:"broker_reconciled_at"`
	BrokerLocalStateGeneration   int64        `json:"broker_local_state_generation"`
	AttestedBy                   string       `json:"attested_by"`
	Reason                       string       `json:"reason"`
	AttestedAt                   time.Time    `json:"attested_at"`
	auditEventPresent            bool
}

type RecordLiveCanaryDayAttestationInput struct {
	AccountID          string
	MarketDay          time.Time
	ExpectedRevisionID int64
	AttestedBy         string
	Reason             string
}

// RecordLiveCanaryDayAttestation records a typed completed-day proof from
// durable Kernel evidence. It never calls a Provider, accepts caller-supplied
// PnL, or treats day_open as completion. The final read-only Provider refresh
// and broker reconciliation must already have landed after the 20:00 market
// timezone session boundary.
func (s *Store) RecordLiveCanaryDayAttestation(input RecordLiveCanaryDayAttestationInput) (*LiveCanaryDayAttestation, error) {
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.AttestedBy = strings.TrimSpace(input.AttestedBy)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.AccountID == "" || len(input.AccountID) > 200 || input.ExpectedRevisionID <= 0 ||
		input.AttestedBy == "" || len(input.AttestedBy) > 200 ||
		input.Reason == "" || len(input.Reason) > 1000 || !canonicalMarketDay(input.MarketDay) {
		return nil, fmt.Errorf("invalid live canary day attestation")
	}

	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(false)); err != nil {
		return nil, normalizeDBError(err)
	}

	observedAt, currentMarketDay, err := liveCanaryDatabaseTime(ctx, tx, s.marketTZ)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if !marketDateAfter(currentMarketDay, input.MarketDay) {
		return nil, ErrLiveCanaryDayNotComplete
	}
	canary, err := latestLiveCanaryRevision(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLiveCanaryAuthorityMissing
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	canary.ObservedAt = observedAt
	if err := validateLiveCanaryAuthority(canary, currentMarketDay); err != nil {
		return nil, err
	}
	if canary.ID != input.ExpectedRevisionID {
		return nil, fmt.Errorf("%w: expected %d, active %d", ErrLiveCanaryRevisionConflict,
			input.ExpectedRevisionID, canary.ID)
	}

	existing, err := loadLiveCanaryDayAttestationByDay(ctx, tx, input.AccountID, input.MarketDay)
	if err == nil {
		if existing.LiveCanaryRevisionID == canary.ID && existing.AttestedBy == input.AttestedBy &&
			existing.Reason == input.Reason {
			if err := validateLiveCanaryDayAttestation(existing); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, normalizeDBError(err)
			}
			return existing, nil
		}
		return nil, ErrLiveCanaryDayAttestationClash
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, normalizeDBError(err)
	}

	kernelAuthority, err := activeKernelPolicyForCanary(ctx, tx)
	if err != nil {
		return nil, err
	}
	attestation, err := buildLiveCanaryDayAttestation(ctx, tx, s.marketTZ, input,
		canary, kernelAuthority, observedAt)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO live_canary_day_attestation
		(account_id,market_day,live_canary_revision_id,kernel_policy_revision_id,
		 kernel_policy_generation,kernel_policy_digest,day_open_equity_micros,
		 live_grant_count,authorized_risk_micros,
		 local_realized_pnl_micros,provider_realized_pnl_micros,pnl_difference_micros,
		 pnl_tolerance_micros,pnl_observed_at,broker_observation_id,
		 broker_observation_generation,broker_observation_completed_at,
		 broker_reconciled_at,broker_local_state_generation,attested_by,reason,attested_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
		RETURNING id`, attestation.AccountID, attestation.MarketDay,
		attestation.LiveCanaryRevisionID, attestation.KernelPolicyRevisionID,
		attestation.KernelPolicyGeneration, kernelAuthority.digestBytes,
		int64(attestation.DayOpenEquity), attestation.LiveGrantCount,
		int64(attestation.AuthorizedRisk), int64(attestation.LocalRealizedPnL),
		int64(attestation.ProviderRealizedPnL), int64(attestation.PnLDifference),
		int64(attestation.PnLTolerance), attestation.PnLObservedAt,
		attestation.BrokerObservationID, attestation.BrokerObservationGeneration,
		attestation.BrokerObservationCompletedAt, attestation.BrokerReconciledAt,
		attestation.BrokerLocalStateGeneration, attestation.AttestedBy,
		attestation.Reason, attestation.AttestedAt).Scan(&attestation.ID)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if err := insertEventAt(ctx, tx, "live_canary_day_attested", map[string]any{
		"attestation_id": attestation.ID, "account_id": attestation.AccountID,
		"market_day": attestation.MarketDay, "live_canary_revision_id": canary.ID,
		"kernel_policy_revision_id":     kernelAuthority.ID,
		"kernel_policy_generation":      kernelAuthority.Generation,
		"kernel_policy_digest":          kernelAuthority.Digest,
		"live_grant_count":              attestation.LiveGrantCount,
		"authorized_risk":               attestation.AuthorizedRisk,
		"local_realized_pnl":            attestation.LocalRealizedPnL,
		"provider_realized_pnl":         attestation.ProviderRealizedPnL,
		"pnl_difference":                attestation.PnLDifference,
		"pnl_tolerance":                 attestation.PnLTolerance,
		"broker_observation_id":         attestation.BrokerObservationID,
		"broker_observation_generation": attestation.BrokerObservationGeneration,
		"broker_local_state_generation": attestation.BrokerLocalStateGeneration,
		"attested_by":                   attestation.AttestedBy, "reason": attestation.Reason,
	}, attestation.AttestedAt); err != nil {
		return nil, normalizeDBError(err)
	}
	attestation.auditEventPresent = true
	if err := validateLiveCanaryDayAttestation(attestation); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return attestation, nil
}

func buildLiveCanaryDayAttestation(ctx context.Context, tx *sql.Tx, marketTZ string,
	input RecordLiveCanaryDayAttestationInput, canary *LiveCanaryRevision,
	kernelAuthority *KernelPolicyRevision, attestedAt time.Time) (*LiveCanaryDayAttestation, error) {
	start, end, err := marketDayBounds(input.MarketDay, marketTZ)
	if err != nil {
		return nil, err
	}
	sessionClose, err := liveCanarySessionClose(input.MarketDay, marketTZ)
	if err != nil {
		return nil, err
	}
	var dayOpen, local, providerValue int64
	var provider sql.NullInt64
	var pnlObservedAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT d.equity_micros,p.local_realized_pnl_micros,
		p.provider_realized_pnl_micros,p.updated_at
		FROM day_open d JOIN daily_pnl p ON p.market_day=d.market_day AND p.ledger=d.ledger
		WHERE d.market_day=$1::date AND d.ledger='live'`, input.MarketDay).Scan(
		&dayOpen, &local, &provider, &pnlObservedAt)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !provider.Valid) {
		return nil, fmt.Errorf("%w: final Provider PnL is missing", ErrLiveCanaryDayEvidenceInvalid)
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	providerValue = provider.Int64
	if pnlObservedAt.Before(sessionClose) || pnlObservedAt.After(attestedAt) {
		return nil, fmt.Errorf("%w: Provider PnL was not observed after session close", ErrLiveCanaryDayEvidenceInvalid)
	}
	freshLocal, err := localRealizedPnL(ctx, tx, "live", start, end)
	if err != nil {
		return nil, err
	}
	if int64(freshLocal) != local {
		return nil, fmt.Errorf("%w: local realized PnL changed after its final observation", ErrLiveCanaryDayEvidenceInvalid)
	}
	difference, err := microsDifference(units.Micros(local), units.Micros(providerValue))
	if err != nil || difference > kernelAuthority.Policy.PnLReconciliationTolerance {
		return nil, fmt.Errorf("%w: realized PnL diverges", ErrLiveCanaryDayEvidenceInvalid)
	}
	if invalid, err := liveCanaryDayHasDivergenceOrUnknown(ctx, tx, input.MarketDay); err != nil {
		return nil, err
	} else if invalid {
		return nil, fmt.Errorf("%w: day has divergence or unresolved unknown evidence", ErrLiveCanaryDayEvidenceInvalid)
	}
	if err := requireLiveExecutionGateIdle(ctx, tx); err != nil {
		return nil, err
	}
	grantCount, authorizedRisk, err := liveCanaryDayGrantEvidence(ctx, tx, input.MarketDay, canary.ID)
	if err != nil {
		return nil, err
	}

	broker, err := loadCurrentBrokerCompletion(ctx, tx, input.AccountID)
	if err != nil {
		return nil, err
	}
	if broker.completedAt.Before(sessionClose) || broker.reconciledAt.Before(broker.completedAt) ||
		broker.observationLocalGeneration != broker.currentLocalGeneration {
		return nil, fmt.Errorf("%w: broker reconciliation is not final for the day", ErrLiveCanaryDayEvidenceInvalid)
	}
	return &LiveCanaryDayAttestation{
		AccountID: input.AccountID, MarketDay: input.MarketDay,
		LiveCanaryRevisionID: canary.ID, KernelPolicyRevisionID: kernelAuthority.ID,
		KernelPolicyGeneration: kernelAuthority.Generation, KernelPolicyDigest: kernelAuthority.Digest,
		DayOpenEquity: units.Micros(dayOpen), LiveGrantCount: grantCount,
		AuthorizedRisk: authorizedRisk, LocalRealizedPnL: units.Micros(local),
		ProviderRealizedPnL: units.Micros(providerValue), PnLDifference: difference,
		PnLTolerance: kernelAuthority.Policy.PnLReconciliationTolerance, PnLObservedAt: pnlObservedAt,
		BrokerObservationID: broker.observationID, BrokerObservationGeneration: broker.generation,
		BrokerObservationCompletedAt: broker.completedAt, BrokerReconciledAt: broker.reconciledAt,
		BrokerLocalStateGeneration: broker.currentLocalGeneration,
		AttestedBy:                 input.AttestedBy, Reason: input.Reason, AttestedAt: attestedAt,
	}, nil
}

type liveCanaryBrokerCompletion struct {
	observationID, accountID               string
	generation, observationLocalGeneration int64
	currentLocalGeneration                 int64
	completedAt, reconciledAt              time.Time
}

func loadCurrentBrokerCompletion(ctx context.Context, tx *sql.Tx, accountID string) (liveCanaryBrokerCompletion, error) {
	var result liveCanaryBrokerCompletion
	err := tx.QueryRowContext(ctx, `SELECT o.id,o.account_id,o.generation,o.completed_at,
		o.local_state_generation,r.reconciled_at,l.generation
		FROM broker_observation_head h
		JOIN broker_observation o ON o.id=h.observation_id
		JOIN broker_reconciliation_head r ON r.account_id=h.account_id
		 AND r.observation_id=h.observation_id AND r.generation=h.generation
		JOIN broker_local_state_revision l ON l.singleton=true
		WHERE h.account_id=$1 AND o.status='complete'`, accountID).Scan(
		&result.observationID, &result.accountID, &result.generation, &result.completedAt,
		&result.observationLocalGeneration, &result.reconciledAt, &result.currentLocalGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return result, fmt.Errorf("%w: current broker observation is not reconciled", ErrLiveCanaryDayEvidenceInvalid)
	}
	if err != nil {
		return result, normalizeDBError(err)
	}
	return result, nil
}

func activeKernelPolicyForCanary(ctx context.Context, tx *sql.Tx) (*KernelPolicyRevision, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, kernelPolicyLockKey); err != nil {
		return nil, normalizeDBError(err)
	}
	authority, err := activeKernelPolicyRevision(ctx, tx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKernelPolicyAuthorityMissing
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	authority.ObservedAt, err = databaseClock(ctx, tx)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if err := validateKernelPolicyAuthority(authority); err != nil {
		return nil, err
	}
	return authority, nil
}

func requireLiveExecutionGateIdle(ctx context.Context, tx *sql.Tx) error {
	var active, unknown sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT active_attempt_id,unknown_attempt_id
		FROM live_execution_gate WHERE singleton=true FOR UPDATE`).Scan(&active, &unknown); err != nil {
		return normalizeDBError(err)
	}
	if unknown.Valid {
		return fmt.Errorf("%w: unresolved unknown attempt", ErrLiveCanaryDayEvidenceInvalid)
	}
	if active.Valid {
		return fmt.Errorf("%w: live execution is active", ErrLiveCanaryDayEvidenceInvalid)
	}
	return nil
}

func liveCanaryDayHasDivergenceOrUnknown(ctx context.Context, tx *sql.Tx, marketDay time.Time) (bool, error) {
	var invalid bool
	err := tx.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM events e WHERE e.kind='pnl_divergence'
		 AND e.payload->>'ledger'='live' AND (e.payload->>'market_day')::date=$1::date)
		OR EXISTS (SELECT 1 FROM execution_attempt a
		 JOIN trade_grant g ON g.operation_id=a.operation_id
		 WHERE g.ledger='live' AND g.market_day=$1::date AND a.state='unknown')`, marketDay).Scan(&invalid)
	return invalid, normalizeDBError(err)
}

func liveCanaryDayGrantEvidence(ctx context.Context, tx *sql.Tx, marketDay time.Time,
	revisionID int64) (int, units.Micros, error) {
	var grants int
	var risk int64
	var invalid, allCompleted bool
	err := tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(g.authorized_risk_micros),0),
		EXISTS (SELECT 1 FROM trade_grant x WHERE x.ledger='live' AND x.market_day=$1::date
		 AND (x.live_canary_revision_id IS DISTINCT FROM $2 OR x.authorized_risk_micros IS NULL))
		OR EXISTS (SELECT 1 FROM execution_attempt a JOIN trade_grant x ON x.operation_id=a.operation_id
		 WHERE x.ledger='live' AND x.market_day=$1::date
		   AND a.state IN ('pending','claimed','placed','unknown'))
		OR EXISTS (SELECT 1 FROM orders o JOIN trade_grant x ON x.operation_id=o.operation_id
		 WHERE x.ledger='live' AND x.market_day=$1::date
		   AND o.state IN ('new','submitted','partially_filled')),
		NOT EXISTS (SELECT 1 FROM trade_grant x WHERE x.ledger='live' AND x.market_day=$1::date
		 AND NOT EXISTS (SELECT 1 FROM execution_attempt a JOIN orders o ON o.execution_attempt_id=a.id
		  WHERE a.operation_id=x.operation_id AND a.state='settled'
		    AND o.state IN ('filled','cancelled','expired')))
		FROM trade_grant g WHERE g.ledger='live' AND g.market_day=$1::date
		 AND g.live_canary_revision_id=$2 AND g.authorized_risk_micros IS NOT NULL`,
		marketDay, revisionID).Scan(&grants, &risk, &invalid, &allCompleted)
	if err != nil {
		return 0, 0, normalizeDBError(err)
	}
	if grants <= 0 || risk <= 0 || invalid || !allCompleted {
		return 0, 0, fmt.Errorf("%w: day has no completed canary execution", ErrLiveCanaryDayEvidenceInvalid)
	}
	return grants, units.Micros(risk), nil
}

func loadLiveCanaryDayAttestationByDay(ctx context.Context, tx *sql.Tx, accountID string, marketDay time.Time) (*LiveCanaryDayAttestation, error) {
	return scanLiveCanaryDayAttestation(tx.QueryRowContext(ctx, liveCanaryDayAttestationSelect+
		` WHERE a.account_id=$1 AND a.market_day=$2::date`, accountID, marketDay))
}

const liveCanaryDayAttestationSelect = `SELECT a.id,a.account_id,a.market_day,
	a.live_canary_revision_id,a.kernel_policy_revision_id,a.kernel_policy_generation,
	a.kernel_policy_digest,a.day_open_equity_micros,a.live_grant_count,a.authorized_risk_micros,
	a.local_realized_pnl_micros,
	a.provider_realized_pnl_micros,a.pnl_difference_micros,a.pnl_tolerance_micros,
	a.pnl_observed_at,a.broker_observation_id,a.broker_observation_generation,
	a.broker_observation_completed_at,a.broker_reconciled_at,
	a.broker_local_state_generation,a.attested_by,a.reason,a.attested_at,
	EXISTS (SELECT 1 FROM events e WHERE e.kind='live_canary_day_attested'
	 AND e.payload->>'attestation_id'=a.id::text
	 AND e.payload->>'account_id'=a.account_id
	 AND (e.payload->>'market_day')::date=a.market_day)
	FROM live_canary_day_attestation a`

type rowScanner interface{ Scan(...any) error }

func scanLiveCanaryDayAttestation(row rowScanner) (*LiveCanaryDayAttestation, error) {
	var result LiveCanaryDayAttestation
	var digest []byte
	var equity, authorizedRisk, local, providerPnL, difference, tolerance int64
	err := row.Scan(&result.ID, &result.AccountID, &result.MarketDay,
		&result.LiveCanaryRevisionID, &result.KernelPolicyRevisionID,
		&result.KernelPolicyGeneration, &digest, &equity, &result.LiveGrantCount,
		&authorizedRisk, &local, &providerPnL,
		&difference, &tolerance, &result.PnLObservedAt, &result.BrokerObservationID,
		&result.BrokerObservationGeneration, &result.BrokerObservationCompletedAt,
		&result.BrokerReconciledAt, &result.BrokerLocalStateGeneration,
		&result.AttestedBy, &result.Reason, &result.AttestedAt, &result.auditEventPresent)
	if err != nil {
		return nil, err
	}
	result.KernelPolicyDigest = hex.EncodeToString(digest)
	result.DayOpenEquity, result.LocalRealizedPnL = units.Micros(equity), units.Micros(local)
	result.AuthorizedRisk = units.Micros(authorizedRisk)
	result.ProviderRealizedPnL, result.PnLDifference = units.Micros(providerPnL), units.Micros(difference)
	result.PnLTolerance = units.Micros(tolerance)
	return &result, nil
}

func validateLiveCanaryDayAttestation(value *LiveCanaryDayAttestation) error {
	if value == nil || value.ID <= 0 || strings.TrimSpace(value.AccountID) == "" ||
		!canonicalMarketDay(value.MarketDay) || value.LiveCanaryRevisionID <= 0 ||
		value.KernelPolicyRevisionID <= 0 || value.KernelPolicyGeneration <= 0 ||
		len(value.KernelPolicyDigest) != 64 || value.PnLDifference < 0 || value.PnLTolerance < 0 ||
		value.LiveGrantCount <= 0 || value.AuthorizedRisk <= 0 ||
		value.PnLDifference > value.PnLTolerance || value.PnLObservedAt.IsZero() ||
		strings.TrimSpace(value.BrokerObservationID) == "" || value.BrokerObservationGeneration <= 0 ||
		value.BrokerObservationCompletedAt.IsZero() || value.BrokerReconciledAt.IsZero() ||
		value.BrokerLocalStateGeneration < 0 || strings.TrimSpace(value.AttestedBy) == "" ||
		strings.TrimSpace(value.Reason) == "" || value.AttestedAt.IsZero() || !value.auditEventPresent {
		return ErrLiveCanaryDayEvidenceInvalid
	}
	if value.PnLObservedAt.After(value.AttestedAt) ||
		value.BrokerObservationCompletedAt.After(value.BrokerReconciledAt) ||
		value.BrokerReconciledAt.After(value.AttestedAt) {
		return fmt.Errorf("%w: evidence is future-dated", ErrLiveCanaryDayEvidenceInvalid)
	}
	return nil
}

func (s *Store) LoadLiveCanaryDayAttestations(accountID string, limit int) ([]LiveCanaryDayAttestation, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid live canary attestation query")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	rows, err := s.DB.QueryContext(ctx, liveCanaryDayAttestationSelect+
		` WHERE a.account_id=$1 ORDER BY a.market_day DESC,a.id DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	result := []LiveCanaryDayAttestation{}
	for rows.Next() {
		value, err := scanLiveCanaryDayAttestation(rows)
		if err != nil {
			return nil, normalizeDBError(err)
		}
		if err := validateLiveCanaryDayAttestation(value); err != nil {
			return nil, err
		}
		result = append(result, *value)
	}
	return result, normalizeDBError(rows.Err())
}

func liveCanarySessionClose(day time.Time, marketTZ string) (time.Time, error) {
	location, err := time.LoadLocation(marketTZ)
	if err != nil {
		return time.Time{}, err
	}
	year, month, date := day.Date()
	return time.Date(year, month, date, liveCanarySessionCloseHour, 0, 0, 0, location).UTC(), nil
}

func canonicalMarketDay(day time.Time) bool {
	if day.IsZero() {
		return false
	}
	year, month, date := day.Date()
	return day.Equal(time.Date(year, month, date, 0, 0, 0, 0, time.UTC))
}

func microsDifference(left, right units.Micros) (units.Micros, error) {
	difference := new(big.Int).Sub(big.NewInt(int64(left)), big.NewInt(int64(right)))
	difference.Abs(difference)
	if !difference.IsInt64() {
		return 0, fmt.Errorf("money difference overflows int64")
	}
	return units.Micros(difference.Int64()), nil
}

func liveCanaryRequiredAttestations(oldDays, newDays int) int {
	if newDays > oldDays {
		return newDays
	}
	return oldDays
}

func eligibleLiveCanaryWideningEvidence(ctx context.Context, tx *sql.Tx, marketTZ, accountID string,
	previous *LiveCanaryRevision, newCleanDays int, currentMarketDay time.Time) ([]LiveCanaryDayAttestation, error) {
	required := liveCanaryRequiredAttestations(previous.CleanDaysBeforeRaise, newCleanDays)
	if required <= 0 || required > 366 || strings.TrimSpace(accountID) == "" {
		return nil, ErrLiveCanaryWideningUnsafe
	}
	kernelAuthority, err := activeKernelPolicyForCanary(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, liveCanaryDayAttestationSelect+`
		WHERE a.account_id=$1 AND a.live_canary_revision_id=$2
		 AND a.market_day >= $3::date AND a.market_day < $4::date
		ORDER BY a.market_day DESC,a.id DESC LIMIT $5`, accountID, previous.ID,
		previous.EffectiveMarketDay, currentMarketDay, required)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	evidence := []LiveCanaryDayAttestation{}
	for rows.Next() {
		value, scanErr := scanLiveCanaryDayAttestation(rows)
		if scanErr != nil {
			rows.Close()
			return nil, normalizeDBError(scanErr)
		}
		if err := validateLiveCanaryDayAttestation(value); err != nil {
			rows.Close()
			return nil, err
		}
		evidence = append(evidence, *value)
	}
	if err := rows.Close(); err != nil {
		return nil, normalizeDBError(err)
	}
	if len(evidence) != required {
		return nil, ErrLiveCanaryWideningUnsafe
	}
	minimumDay := evidence[len(evidence)-1].MarketDay
	var missingObservedDay, invalidDay bool
	if err := tx.QueryRowContext(ctx, `SELECT
		EXISTS (SELECT 1 FROM day_open d WHERE d.ledger='live'
		 AND d.market_day >= $1::date AND d.market_day < $2::date
		 AND NOT EXISTS (SELECT 1 FROM live_canary_day_attestation a
		  WHERE a.account_id=$3 AND a.live_canary_revision_id=$4
		    AND a.market_day=d.market_day)),
		EXISTS (SELECT 1 FROM events e WHERE e.kind='pnl_divergence'
		 AND e.payload->>'ledger'='live' AND (e.payload->>'market_day')::date >= $1::date
		 AND (e.payload->>'market_day')::date < $2::date)
		OR EXISTS (SELECT 1 FROM execution_attempt x JOIN trade_grant g ON g.operation_id=x.operation_id
		 WHERE g.ledger='live' AND g.market_day >= $1::date AND g.market_day < $2::date
		 AND x.state='unknown')`, minimumDay, currentMarketDay, accountID, previous.ID).Scan(
		&missingObservedDay, &invalidDay); err != nil {
		return nil, normalizeDBError(err)
	}
	if missingObservedDay || invalidDay {
		return nil, ErrLiveCanaryWideningUnsafe
	}
	if err := requireLiveExecutionGateIdle(ctx, tx); err != nil {
		return nil, err
	}
	broker, err := loadCurrentBrokerCompletion(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	if broker.observationLocalGeneration != broker.currentLocalGeneration {
		return nil, ErrLiveCanaryWideningUnsafe
	}
	for i := range evidence {
		if evidence[i].PnLDifference > kernelAuthority.Policy.PnLReconciliationTolerance {
			return nil, ErrLiveCanaryWideningUnsafe
		}
		if err := revalidateLiveCanaryDayEvidence(ctx, tx, marketTZ, &evidence[i]); err != nil {
			return nil, err
		}
	}
	return evidence, nil
}

func revalidateLiveCanaryDayEvidence(ctx context.Context, tx *sql.Tx, marketTZ string, evidence *LiveCanaryDayAttestation) error {
	start, end, err := marketDayBounds(evidence.MarketDay, marketTZ)
	if err != nil {
		return err
	}
	local, err := localRealizedPnL(ctx, tx, "live", start, end)
	if err != nil {
		return err
	}
	var storedLocal int64
	var provider sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT local_realized_pnl_micros,provider_realized_pnl_micros
		FROM daily_pnl WHERE market_day=$1::date AND ledger='live'`, evidence.MarketDay).Scan(
		&storedLocal, &provider); err != nil {
		return normalizeDBError(err)
	}
	if !provider.Valid || local != evidence.LocalRealizedPnL || storedLocal != int64(evidence.LocalRealizedPnL) ||
		provider.Int64 != int64(evidence.ProviderRealizedPnL) {
		return ErrLiveCanaryWideningUnsafe
	}
	grantCount, risk, err := liveCanaryDayGrantEvidence(ctx, tx, evidence.MarketDay,
		evidence.LiveCanaryRevisionID)
	if err != nil || grantCount != evidence.LiveGrantCount || risk != evidence.AuthorizedRisk {
		return ErrLiveCanaryWideningUnsafe
	}
	return nil
}

func insertLiveCanaryWideningEvidence(ctx context.Context, tx *sql.Tx, revisionID int64,
	evidence []LiveCanaryDayAttestation) error {
	for index := range evidence {
		if _, err := tx.ExecContext(ctx, `INSERT INTO live_canary_widening_evidence
			(revision_id,ordinal,attestation_id) VALUES ($1,$2,$3)`,
			revisionID, index+1, evidence[index].ID); err != nil {
			return normalizeDBError(err)
		}
	}
	return nil
}

func loadLiveCanaryRevisionEvidence(ctx context.Context, tx *sql.Tx, revision *LiveCanaryRevision) error {
	revision.AttestationIDs = []int64{}
	revision.WideningAccountID = ""
	if revision.ChangeClass != "widen" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.attestation_id,a.account_id
		FROM live_canary_widening_evidence e
		JOIN live_canary_day_attestation a ON a.id=e.attestation_id
		WHERE e.revision_id=$1 ORDER BY e.ordinal`, revision.ID)
	if err != nil {
		return normalizeDBError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var accountID string
		if err := rows.Scan(&id, &accountID); err != nil {
			return normalizeDBError(err)
		}
		if revision.WideningAccountID == "" {
			revision.WideningAccountID = accountID
		} else if revision.WideningAccountID != accountID {
			return ErrLiveCanaryAuthorityInvalid
		}
		revision.AttestationIDs = append(revision.AttestationIDs, id)
	}
	return normalizeDBError(rows.Err())
}
