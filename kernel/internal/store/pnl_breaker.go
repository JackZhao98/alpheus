package store

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"alpheus/kernel/internal/units"
)

type DayRiskInput struct {
	Ledger                  string
	MarketDay               time.Time
	Start                   time.Time
	End                     time.Time
	ObservedAt              time.Time
	ProviderRealizedPnL     *units.Micros
	MaxDailyLossPct         units.PercentMicros
	ConsecutiveLossDaysHalt int
	PnLReconciliationLimit  units.Micros
}

type DayRiskStats struct {
	LocalRealizedPnL     units.Micros
	ProviderRealizedPnL  *units.Micros
	EffectiveRealizedPnL units.Micros
	DailyLossLimit       units.Micros
	ConsecutiveLossDays  int
	Halted               bool
	Reason               string
}

type BreakerState struct {
	Ledger    string
	Halted    bool
	Reason    string
	UpdatedAt time.Time
	EventID   int64
}

func (t *ledgerTx) EvaluateDayRisk(input DayRiskInput) (DayRiskStats, error) {
	var stats DayRiskStats
	if input.Ledger != "live" && input.Ledger != "shadow" {
		return stats, fmt.Errorf("invalid breaker ledger")
	}
	if input.MarketDay.IsZero() || input.Start.IsZero() || input.End.IsZero() ||
		input.ObservedAt.IsZero() || !input.End.After(input.Start) ||
		input.ObservedAt.Before(input.Start) || !input.ObservedAt.Before(input.End) {
		return stats, fmt.Errorf("invalid market-day window")
	}
	canonicalStart, canonicalEnd, err := marketDayBounds(input.MarketDay, t.marketTZ)
	if err != nil {
		return stats, err
	}
	if !input.Start.Equal(canonicalStart) || !input.End.Equal(canonicalEnd) {
		return stats, fmt.Errorf("market-day window does not match configured timezone")
	}
	if input.MaxDailyLossPct < 0 || input.ConsecutiveLossDaysHalt < 0 || input.PnLReconciliationLimit < 0 {
		return stats, fmt.Errorf("invalid breaker limits")
	}
	local, err := localRealizedPnL(t.ctx, t.tx, input.Ledger, input.Start, input.End)
	if err != nil {
		return stats, err
	}
	stats.LocalRealizedPnL = local
	stats.ProviderRealizedPnL = cloneMicros(input.ProviderRealizedPnL)
	stats.EffectiveRealizedPnL = moreLossMaking(local, input.ProviderRealizedPnL)
	if err := persistDailyPnL(t.ctx, t.tx, input.MarketDay, input.Ledger,
		local, input.ProviderRealizedPnL, stats.EffectiveRealizedPnL, input.ObservedAt); err != nil {
		return stats, err
	}

	var dayOpen int64
	if err := t.tx.QueryRowContext(t.ctx, `SELECT equity_micros FROM day_open
		WHERE market_day=$1::date AND ledger=$2`, input.MarketDay, input.Ledger).Scan(&dayOpen); err != nil {
		return stats, normalizeDBError(err)
	}
	if input.MaxDailyLossPct > 0 && dayOpen > 0 {
		stats.DailyLossLimit, err = units.PercentFloor(units.Micros(dayOpen), input.MaxDailyLossPct)
		if err != nil {
			return stats, err
		}
	}
	stats.ConsecutiveLossDays, err = t.consecutiveLossDays(input, stats.EffectiveRealizedPnL)
	if err != nil {
		return stats, err
	}

	trigger := ""
	diverged := input.ProviderRealizedPnL != nil &&
		units.DifferenceExceeds(local, *input.ProviderRealizedPnL, input.PnLReconciliationLimit)
	divergenceOverride, err := t.breakerOverridden(input.Ledger, "pnl_divergence", input.MarketDay)
	if err != nil {
		return stats, err
	}
	dailyLossOverride, err := t.breakerOverridden(input.Ledger, "daily_loss", input.MarketDay)
	if err != nil {
		return stats, err
	}
	lossStreakOverride, err := t.breakerOverridden(input.Ledger, "loss_streak", input.MarketDay)
	if err != nil {
		return stats, err
	}
	if diverged && !divergenceOverride {
		trigger = "pnl_divergence"
	} else if stats.DailyLossLimit > 0 &&
		lessThanOrEqualNegative(stats.EffectiveRealizedPnL, stats.DailyLossLimit) &&
		!dailyLossOverride {
		trigger = "daily_loss"
	} else if input.ConsecutiveLossDaysHalt > 0 &&
		stats.ConsecutiveLossDays >= input.ConsecutiveLossDaysHalt &&
		!lossStreakOverride {
		trigger = "loss_streak"
	}

	state, changed, err := t.transitionBreaker(input.Ledger, trigger, input.MarketDay, input.ObservedAt, "")
	if err != nil {
		return stats, err
	}
	if changed && trigger == "pnl_divergence" {
		if err := insertEventAt(t.ctx, t.tx, "pnl_divergence", map[string]any{
			"ledger": input.Ledger, "market_day": input.MarketDay,
			"observed_at":        input.ObservedAt,
			"local_realized_pnl": local, "provider_realized_pnl": *input.ProviderRealizedPnL,
			"tolerance": input.PnLReconciliationLimit,
		}, input.ObservedAt); err != nil {
			return stats, normalizeDBError(err)
		}
	}
	stats.Halted, stats.Reason = state.Halted, state.Reason
	return stats, nil
}

func (t *ledgerTx) ResumeBreaker(ledger, reason string, marketDay, observedAt time.Time, subject string) (BreakerState, error) {
	var state BreakerState
	if ledger != "live" && ledger != "shadow" {
		return state, fmt.Errorf("invalid breaker ledger")
	}
	if reason != "daily_loss" && reason != "loss_streak" && reason != "pnl_divergence" {
		return state, fmt.Errorf("invalid breaker reason")
	}
	if marketDay.IsZero() || observedAt.IsZero() || subject == "" {
		return state, fmt.Errorf("breaker resume requires market day, observation time, and subject")
	}
	start, end, err := marketDayBounds(marketDay, t.marketTZ)
	if err != nil {
		return state, err
	}
	if observedAt.Before(start) || !observedAt.Before(end) {
		return state, fmt.Errorf("breaker observation is outside market day")
	}
	var currentReason sql.NullString
	if err := t.tx.QueryRowContext(t.ctx, `SELECT ledger,halted,reason,updated_at
		FROM breaker_state WHERE ledger=$1 FOR UPDATE`, ledger).Scan(
		&state.Ledger, &state.Halted, &currentReason, &state.UpdatedAt); err != nil {
		return state, normalizeDBError(err)
	}
	state.Reason = currentReason.String
	if !state.Halted || state.Reason != reason {
		return state, ErrBreakerNotActive
	}
	if reason == "daily_loss" && (state.UpdatedAt.Before(start) || !state.UpdatedAt.Before(end)) {
		// A daily-loss halt is scoped to the market day on which it fired. A
		// stale prior-day row must not create a pre-emptive override for today.
		return state, ErrBreakerNotActive
	}
	if _, err := t.tx.ExecContext(t.ctx, `INSERT INTO breaker_override
		(ledger,reason,market_day,subject,ts) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (ledger,reason,market_day) DO UPDATE
		SET subject=EXCLUDED.subject,ts=EXCLUDED.ts`, ledger, reason, marketDay, subject, observedAt); err != nil {
		return state, normalizeDBError(err)
	}
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE breaker_state SET halted=false,reason=NULL,updated_at=$2
		WHERE ledger=$1`, ledger, observedAt); err != nil {
		return state, normalizeDBError(err)
	}
	eventID, err := insertEventWithIDAt(t.ctx, t.tx, "breaker", map[string]any{
		"ledger": ledger, "halted": false, "reason": reason,
		"market_day": marketDay, "observed_at": observedAt,
		"subject": subject, "override": true,
	}, observedAt)
	if err != nil {
		return state, normalizeDBError(err)
	}
	state.Halted, state.Reason, state.UpdatedAt, state.EventID = false, "", observedAt, eventID
	return state, nil
}

func localRealizedPnL(ctx context.Context, tx *sql.Tx, ledger string, start, end time.Time) (units.Micros, error) {
	rows, err := tx.QueryContext(ctx, `SELECT f.id,f.qty,f.price_micros,f.fees_micros,
		o.multiplier,a.matched_cost_micros
		FROM fills f
		JOIN orders o ON o.id=f.order_id
		JOIN exposure_close_allocation a ON a.close_fill_id=f.id
		WHERE f.ledger=$1 AND f.ts >= $2 AND f.ts < $3
		ORDER BY f.id,a.open_fill_id`, ledger, start, end)
	if err != nil {
		return 0, normalizeDBError(err)
	}
	defer rows.Close()
	type closeFill struct {
		qty, price, fees, multiplier int64
		matched                      *big.Int
	}
	byID := map[string]*closeFill{}
	order := []string{}
	for rows.Next() {
		var id string
		var qty, price, fees, multiplier, matched int64
		if err := rows.Scan(&id, &qty, &price, &fees, &multiplier, &matched); err != nil {
			return 0, normalizeDBError(err)
		}
		fill := byID[id]
		if fill == nil {
			fill = &closeFill{qty: qty, price: price, fees: fees, multiplier: multiplier, matched: new(big.Int)}
			byID[id] = fill
			order = append(order, id)
		} else if fill.qty != qty || fill.price != price || fill.fees != fees || fill.multiplier != multiplier {
			return 0, fmt.Errorf("close fill economics changed across FIFO allocations")
		}
		if matched < 0 {
			return 0, fmt.Errorf("negative matched cost")
		}
		fill.matched.Add(fill.matched, big.NewInt(matched))
	}
	if err := rows.Err(); err != nil {
		return 0, normalizeDBError(err)
	}
	total := new(big.Int)
	for _, id := range order {
		fill := byID[id]
		// Exit proceeds round down, against the account. The durable matched
		// costs were already rounded against the account when M3A allocated them.
		proceeds, err := units.MulQtyPrice(units.Qty(fill.qty), units.Micros(fill.price), fill.multiplier, false)
		if err != nil {
			return 0, err
		}
		pnl := new(big.Int).Sub(big.NewInt(int64(proceeds)), big.NewInt(fill.fees))
		pnl.Sub(pnl, fill.matched)
		total.Add(total, pnl)
	}
	if !total.IsInt64() {
		return 0, fmt.Errorf("realized PnL overflows int64")
	}
	return units.Micros(total.Int64()), nil
}

func (t *ledgerTx) consecutiveLossDays(input DayRiskInput, current units.Micros) (int, error) {
	if input.ConsecutiveLossDaysHalt <= 0 {
		return 0, nil
	}
	// A positive result today has already broken the streak. Zero is kept
	// neutral because dayState evaluates before the current market day is
	// complete; it must not erase a completed multi-day streak at midnight.
	if current > 0 {
		return 0, nil
	}
	count := 0
	if current < 0 {
		count = 1
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT market_day FROM day_open
		WHERE ledger=$1 AND market_day < $2::date ORDER BY market_day DESC LIMIT $3`,
		input.Ledger, input.MarketDay, input.ConsecutiveLossDaysHalt)
	if err != nil {
		return 0, normalizeDBError(err)
	}
	defer rows.Close()
	var days []time.Time
	for rows.Next() {
		var day time.Time
		if err := rows.Scan(&day); err != nil {
			return 0, normalizeDBError(err)
		}
		days = append(days, day)
	}
	if err := rows.Err(); err != nil {
		return 0, normalizeDBError(err)
	}
	for _, day := range days {
		start, end, err := marketDayBounds(day, t.marketTZ)
		if err != nil {
			return 0, err
		}
		local, err := localRealizedPnL(t.ctx, t.tx, input.Ledger, start, end)
		if err != nil {
			return 0, err
		}
		provider, err := persistedProviderPnL(t.ctx, t.tx, day, input.Ledger)
		if err != nil {
			return 0, err
		}
		effective := moreLossMaking(local, provider)
		if err := persistDailyPnL(t.ctx, t.tx, day, input.Ledger, local, provider, effective, input.ObservedAt); err != nil {
			return 0, err
		}
		if effective >= 0 {
			break
		}
		count++
	}
	return count, nil
}

func marketDayBounds(day time.Time, tzName string) (time.Time, time.Time, error) {
	location, err := time.LoadLocation(tzName)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid market timezone")
	}
	year, month, date := day.Date()
	start := time.Date(year, month, date, 0, 0, 0, 0, location)
	return start.UTC(), start.AddDate(0, 0, 1).UTC(), nil
}

func persistedProviderPnL(ctx context.Context, tx *sql.Tx, day time.Time, ledger string) (*units.Micros, error) {
	var value sql.NullInt64
	err := tx.QueryRowContext(ctx, `SELECT provider_realized_pnl_micros FROM daily_pnl
		WHERE market_day=$1::date AND ledger=$2`, day, ledger).Scan(&value)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, normalizeDBError(err)
	}
	if !value.Valid {
		return nil, nil
	}
	pnl := units.Micros(value.Int64)
	return &pnl, nil
}

func persistDailyPnL(ctx context.Context, tx *sql.Tx, day time.Time, ledger string,
	local units.Micros, provider *units.Micros, effective units.Micros, observedAt time.Time) error {
	var providerValue any
	if provider != nil {
		providerValue = int64(*provider)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO daily_pnl
		(market_day,ledger,local_realized_pnl_micros,provider_realized_pnl_micros,
		 effective_realized_pnl_micros,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (market_day,ledger) DO UPDATE SET
		local_realized_pnl_micros=EXCLUDED.local_realized_pnl_micros,
		provider_realized_pnl_micros=COALESCE(EXCLUDED.provider_realized_pnl_micros,daily_pnl.provider_realized_pnl_micros),
		effective_realized_pnl_micros=LEAST(EXCLUDED.local_realized_pnl_micros,
			COALESCE(EXCLUDED.provider_realized_pnl_micros,daily_pnl.provider_realized_pnl_micros,
			         EXCLUDED.local_realized_pnl_micros)),updated_at=EXCLUDED.updated_at`,
		day, ledger, int64(local), providerValue, int64(effective), observedAt)
	return normalizeDBError(err)
}

func (t *ledgerTx) breakerOverridden(ledger, reason string, day time.Time) (bool, error) {
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS (SELECT 1 FROM breaker_override
		WHERE ledger=$1 AND reason=$2 AND market_day=$3::date)`, ledger, reason, day).Scan(&exists)
	return exists, normalizeDBError(err)
}

func (t *ledgerTx) transitionBreaker(ledger, trigger string, day, observedAt time.Time, subject string) (BreakerState, bool, error) {
	var state BreakerState
	var reason sql.NullString
	if err := t.tx.QueryRowContext(t.ctx, `SELECT ledger,halted,reason,updated_at
		FROM breaker_state WHERE ledger=$1 FOR UPDATE`, ledger).Scan(
		&state.Ledger, &state.Halted, &reason, &state.UpdatedAt); err != nil {
		return state, false, normalizeDBError(err)
	}
	state.Reason = reason.String
	if state.Halted {
		overridden, err := t.breakerOverridden(ledger, state.Reason, day)
		if err != nil {
			return state, false, err
		}
		if !overridden {
			switch state.Reason {
			case "pnl_divergence":
				// A provider/local disagreement is latched until an admin records
				// a reconciliation override, even if a later read happens to agree.
				trigger = state.Reason
			case "daily_loss":
				sameDay, err := timestampOnMarketDay(state.UpdatedAt, day, t.marketTZ)
				if err != nil {
					return state, false, err
				}
				if sameDay && trigger != "pnl_divergence" {
					// Once hit, the daily stop remains latched for that market day.
					trigger = state.Reason
				}
			}
		}
	}
	nextHalted := trigger != ""
	if state.Halted == nextHalted && state.Reason == trigger {
		return state, false, nil
	}
	var nextReason any
	if nextHalted {
		nextReason = trigger
	}
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE breaker_state SET halted=$2,reason=$3,updated_at=$4
		WHERE ledger=$1`, ledger, nextHalted, nextReason, observedAt); err != nil {
		return state, false, normalizeDBError(err)
	}
	payload := map[string]any{
		"ledger": ledger, "halted": nextHalted, "market_day": day,
		"observed_at": observedAt,
	}
	if trigger != "" {
		payload["reason"] = trigger
	} else if state.Reason != "" {
		payload["reason"] = state.Reason
	}
	if subject != "" {
		payload["subject"] = subject
	}
	if err := insertEventAt(t.ctx, t.tx, "breaker", payload, observedAt); err != nil {
		return state, false, normalizeDBError(err)
	}
	state.Halted, state.Reason, state.UpdatedAt = nextHalted, trigger, observedAt
	return state, true, nil
}

func timestampOnMarketDay(timestamp, day time.Time, tzName string) (bool, error) {
	location, err := time.LoadLocation(tzName)
	if err != nil {
		return false, fmt.Errorf("invalid market timezone")
	}
	return timestamp.In(location).Format(time.DateOnly) == day.Format(time.DateOnly), nil
}

func moreLossMaking(local units.Micros, provider *units.Micros) units.Micros {
	if provider != nil && *provider < local {
		return *provider
	}
	return local
}

func lessThanOrEqualNegative(value, magnitude units.Micros) bool {
	left := big.NewInt(int64(value))
	right := new(big.Int).Neg(big.NewInt(int64(magnitude)))
	return left.Cmp(right) <= 0
}

func cloneMicros(value *units.Micros) *units.Micros {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
