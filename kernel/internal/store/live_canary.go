package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"alpheus/kernel/internal/units"
)

const liveCanaryConfigLockKey int64 = 0x414c504843414e59 // "ALPHCANY"

var ErrLiveCanaryRaiseUnsafe = errors.New("live canary raise lacks clean-day evidence")

type LiveCanaryRevision struct {
	ID                        int64
	DailyAuthorizedRiskCapUSD units.Micros
	CleanDaysBeforeRaise      int
	EffectiveMarketDay        time.Time
	RecordedAt                time.Time
}

// RecordLiveCanaryRevision is the startup-only gate for versioned live limit
// changes. There is intentionally no HTTP/runtime wrapper around it. Initial
// configuration and tightening are recorded immediately; widening requires
// completed live-ledger days with no divergence and no unresolved unknown.
func (s *Store) RecordLiveCanaryRevision(cap units.Micros, cleanDays int, marketDay time.Time) error {
	if cap <= 0 || cleanDays <= 0 || marketDay.IsZero() {
		return fmt.Errorf("invalid live canary revision")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return normalizeDBError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, liveCanaryConfigLockKey); err != nil {
		return normalizeDBError(err)
	}

	previous, err := latestLiveCanaryRevision(ctx, tx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return normalizeDBError(err)
	}
	if err == nil && previous.DailyAuthorizedRiskCapUSD == cap && previous.CleanDaysBeforeRaise == cleanDays {
		if err := tx.Commit(); err != nil {
			return normalizeDBError(err)
		}
		committed = true
		return nil
	}

	change := "initial"
	if err == nil {
		switch {
		case cap > previous.DailyAuthorizedRiskCapUSD:
			change = "raise"
			requiredClean := cleanDays
			if previous.CleanDaysBeforeRaise > requiredClean {
				requiredClean = previous.CleanDaysBeforeRaise
			}
			if err := verifyLiveCanaryRaise(ctx, tx, marketDay, requiredClean); err != nil {
				return err
			}
		case cap < previous.DailyAuthorizedRiskCapUSD:
			change = "tighten"
		default:
			change = "policy"
		}
	}

	var revisionID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO live_canary_revision
		(daily_authorized_risk_micros,clean_days_before_raise,effective_market_day)
		VALUES ($1,$2,$3) RETURNING id`, int64(cap), cleanDays, marketDay).Scan(&revisionID)
	if err != nil {
		return normalizeDBError(err)
	}
	payload := map[string]any{
		"revision_id": revisionID, "change": change, "daily_authorized_risk_cap": cap,
		"clean_days_before_raise": cleanDays, "effective_market_day": marketDay,
	}
	if previous != nil {
		payload["previous_revision_id"] = previous.ID
		payload["previous_daily_authorized_risk_cap"] = previous.DailyAuthorizedRiskCapUSD
		payload["previous_clean_days_before_raise"] = previous.CleanDaysBeforeRaise
	}
	if err := insertEvent(ctx, tx, "live_canary_revision_recorded", payload); err != nil {
		return normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return normalizeDBError(err)
	}
	committed = true
	return nil
}

func latestLiveCanaryRevision(ctx context.Context, tx *sql.Tx) (*LiveCanaryRevision, error) {
	var revision LiveCanaryRevision
	var risk int64
	err := tx.QueryRowContext(ctx, `SELECT id,daily_authorized_risk_micros,
		clean_days_before_raise,effective_market_day,recorded_at
		FROM live_canary_revision ORDER BY id DESC LIMIT 1`).Scan(
		&revision.ID, &risk, &revision.CleanDaysBeforeRaise,
		&revision.EffectiveMarketDay, &revision.RecordedAt,
	)
	revision.DailyAuthorizedRiskCapUSD = units.Micros(risk)
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

func verifyLiveCanaryRaise(ctx context.Context, tx *sql.Tx, marketDay time.Time, requiredClean int) error {
	var unresolved int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM execution_attempt WHERE state='unknown'`).Scan(&unresolved); err != nil {
		return normalizeDBError(err)
	}
	if unresolved != 0 {
		return ErrLiveCanaryRaiseUnsafe
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.market_day,
		EXISTS (SELECT 1 FROM events e
			WHERE e.kind='pnl_divergence'
			  AND e.payload->>'ledger'='live'
			  AND left(e.payload->>'market_day',10)=d.market_day::text)
		FROM (SELECT market_day FROM day_open
			WHERE ledger='live' AND market_day < $1::date
			ORDER BY market_day DESC LIMIT $2) d
		ORDER BY d.market_day DESC`, marketDay, requiredClean)
	if err != nil {
		return normalizeDBError(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var day time.Time
		var diverged bool
		if err := rows.Scan(&day, &diverged); err != nil {
			return normalizeDBError(err)
		}
		if diverged {
			return ErrLiveCanaryRaiseUnsafe
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return normalizeDBError(err)
	}
	if count != requiredClean {
		return ErrLiveCanaryRaiseUnsafe
	}
	return nil
}
