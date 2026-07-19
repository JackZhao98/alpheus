package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

// BrokerReconciliationResult describes the durable accounting work caused by
// one complete Provider account observation. Deferred means a possible
// Alpheus effect was still in flight, so no checkpoint or exposure was changed.
type BrokerReconciliationResult struct {
	ObservationID           string                        `json:"observation_id"`
	ObservationGeneration   int64                         `json:"observation_generation"`
	Applied                 bool                          `json:"applied"`
	Deferred                bool                          `json:"deferred"`
	DeferredReason          string                        `json:"deferred_reason,omitempty"`
	Episodes                []BrokerExternalChangeEpisode `json:"episodes,omitempty"`
	InvalidatedOperationIDs []string                      `json:"invalidated_operation_ids,omitempty"`
}

// BrokerExternalChangeEpisode is append-only evidence of a position change
// not explained by the tracked Alpheus exposure ledger. Attribution remains
// uncertain: this is an accounting fact, not causal credit or blame.
type BrokerExternalChangeEpisode struct {
	ID                    string     `json:"id"`
	AccountID             string     `json:"account_id"`
	BrokerObservationID   string     `json:"broker_observation_id"`
	ObservationGeneration int64      `json:"observation_generation"`
	Symbol                string     `json:"symbol"`
	Kind                  string     `json:"kind"`
	ChangeKind            string     `json:"change_kind"`
	Origin                string     `json:"origin"`
	ProviderQtyBefore     *units.Qty `json:"provider_qty_before,omitempty"`
	ProviderQtyAfter      units.Qty  `json:"provider_qty_after"`
	TrackedQtyBefore      units.Qty  `json:"tracked_qty_before"`
	TrackedQtyAfter       units.Qty  `json:"tracked_qty_after"`
	AdjustedTrackedQty    units.Qty  `json:"adjusted_tracked_qty"`
	PositionKeys          []string   `json:"position_keys"`
	AttributionStatus     string     `json:"attribution_status"`
}

type reconciliationPosition struct {
	symbol, kind string
	qty          units.Qty
	keys         []string
}

type reconciliationProjection struct {
	provider units.Qty
	tracked  units.Qty
	keys     []string
}

func (s *Store) BrokerLocalStateGeneration() (int64, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	var generation int64
	if err := s.DB.QueryRowContext(ctx, `SELECT generation FROM broker_local_state_revision
		WHERE singleton=true`).Scan(&generation); err != nil {
		return 0, normalizeDBError(err)
	}
	return generation, nil
}

// ReconcileBrokerObservation advances the account projection and reconciles
// externally reduced long exposure in one transaction. It never inserts an
// order, fill, close allocation, or local realized PnL.
func (s *Store) ReconcileBrokerObservation(observationID string) (*BrokerReconciliationResult, error) {
	if strings.TrimSpace(observationID) == "" {
		return nil, fmt.Errorf("broker observation id is required")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()

	var accountID string
	var requestedGeneration int64
	if err := tx.QueryRowContext(ctx, `SELECT account_id,generation FROM broker_observation WHERE id=$1`, observationID).Scan(&accountID, &requestedGeneration); err != nil {
		return nil, normalizeDBError(err)
	}
	result := &BrokerReconciliationResult{ObservationID: observationID, ObservationGeneration: requestedGeneration}
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock($1)`, brokerReconciliationLockKey(accountID)).Scan(&locked); err != nil {
		return nil, normalizeDBError(err)
	}
	if !locked {
		result.Deferred = true
		result.DeferredReason = "reconciliation_busy"
		return result, nil
	}

	// A caller may have recorded a newer complete snapshot before acquiring the
	// account lock. Always reconcile the immutable current head, never an older
	// observation against a newer projection.
	var generation, boundLocalStateGeneration int64
	var completedAt time.Time
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT h.observation_id,o.generation,o.completed_at,o.status,o.local_state_generation
		FROM broker_observation_head h JOIN broker_observation o ON o.id=h.observation_id
		WHERE h.account_id=$1`, accountID).Scan(&observationID, &generation, &completedAt, &status, &boundLocalStateGeneration); err != nil {
		return nil, normalizeDBError(err)
	}
	result.ObservationID, result.ObservationGeneration = observationID, generation
	if status != "complete" {
		return nil, fmt.Errorf("broker reconciliation requires a complete observation")
	}
	var priorGeneration int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM broker_reconciliation_head WHERE account_id=$1`, accountID).Scan(&priorGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, normalizeDBError(err)
	}
	if err == nil && priorGeneration >= generation {
		return result, nil
	}

	var activeAttempt, unknownAttempt sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT active_attempt_id,unknown_attempt_id
		FROM live_execution_gate WHERE singleton=true FOR UPDATE`).Scan(&activeAttempt, &unknownAttempt); err != nil {
		return nil, normalizeDBError(err)
	}
	if activeAttempt.Valid || unknownAttempt.Valid {
		result.Deferred = true
		result.DeferredReason = "live_execution_unresolved"
		return result, nil
	}
	current, currentOrders, err := loadReconciliationPositions(ctx, tx, observationID)
	if err != nil {
		return nil, err
	}
	lagged, err := hasUnappliedAlpheusOrderEffect(ctx, tx, currentOrders)
	if err != nil {
		return nil, err
	}
	if lagged {
		result.Deferred = true
		result.DeferredReason = "alpheus_order_effect_not_yet_applied"
		return result, nil
	}

	// Exposure and proposal admission use the same ledger lock. Symbol locks are
	// then taken in lexical order, matching the normal close path.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ledgerLockKey(false)); err != nil {
		return nil, normalizeDBError(err)
	}
	previous, err := loadReconciliationProjections(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	tracked, err := loadTrackedExposureQuantities(ctx, tx)
	if err != nil {
		return nil, err
	}
	keys := reconciliationKeys(current, previous, tracked)
	for _, key := range keys {
		parts := strings.SplitN(key, "\x00", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid reconciliation economic key")
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, symbolLockKey("live", parts[1])); err != nil {
			return nil, normalizeDBError(err)
		}
	}
	// Lock the local revision only after ledger/symbol locks. Order and fill
	// writers take those resource locks before their revision trigger, so this
	// order cannot deadlock with a proposal or fill reconciliation transaction.
	var currentLocalStateGeneration int64
	if err := tx.QueryRowContext(ctx, `SELECT generation FROM broker_local_state_revision
		WHERE singleton=true FOR UPDATE`).Scan(&currentLocalStateGeneration); err != nil {
		return nil, normalizeDBError(err)
	}
	if currentLocalStateGeneration != boundLocalStateGeneration {
		result.Deferred = true
		result.DeferredReason = "local_order_changed_after_observation"
		return result, nil
	}

	changed := false
	for _, key := range keys {
		position := current[key]
		if position.keys == nil {
			position.keys = []string{}
		}
		parts := strings.SplitN(key, "\x00", 2)
		kind, symbol := parts[0], parts[1]
		providerQty := position.qty
		trackedBefore := tracked[key]
		providerLong := providerQty
		if providerLong < 0 {
			providerLong = 0
		}
		trackedAfter := trackedBefore
		if trackedAfter > providerLong {
			trackedAfter = providerLong
		}
		adjusted := trackedBefore - trackedAfter
		prior, hasPrior := previous[key]
		beforeExternal, afterExternal := units.Qty(0), providerQty-trackedAfter
		if hasPrior {
			beforeExternal = prior.provider - prior.tracked
		}
		episodeNeeded := adjusted > 0
		if hasPrior {
			episodeNeeded = episodeNeeded || beforeExternal != afterExternal
		} else {
			episodeNeeded = episodeNeeded || providerQty != trackedAfter
		}
		if episodeNeeded {
			episode := BrokerExternalChangeEpisode{
				ID: NewID(), AccountID: accountID, BrokerObservationID: observationID,
				ObservationGeneration: generation, Symbol: symbol, Kind: kind,
				ProviderQtyAfter: providerQty, TrackedQtyBefore: trackedBefore,
				TrackedQtyAfter: trackedAfter, AdjustedTrackedQty: adjusted,
				PositionKeys: append([]string(nil), position.keys...), AttributionStatus: "uncertain",
			}
			if hasPrior {
				before := prior.provider
				episode.ProviderQtyBefore = &before
			}
			episode.ChangeKind = externalPositionChangeKind(hasPrior, beforeExternal, afterExternal, adjusted)
			episode.Origin = externalPositionChangeOrigin(trackedBefore, len(position.keys))
			if err := insertBrokerExternalChangeEpisode(ctx, tx, episode); err != nil {
				return nil, err
			}
			if adjusted > 0 {
				if err := applyExternalExposureReduction(ctx, tx, episode, completedAt); err != nil {
					return nil, err
				}
			}
			if err := insertEvent(ctx, tx, "broker_external_change_reconciled", episode); err != nil {
				return nil, normalizeDBError(err)
			}
			result.Episodes = append(result.Episodes, episode)
			changed = true
		}
		keysJSON, err := json.Marshal(position.keys)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO broker_position_projection
			(account_id,symbol,kind,observation_id,observation_generation,provider_qty,tracked_qty,position_keys)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT (account_id,symbol,kind) DO UPDATE SET
			observation_id=EXCLUDED.observation_id,
			observation_generation=EXCLUDED.observation_generation,
			provider_qty=EXCLUDED.provider_qty,tracked_qty=EXCLUDED.tracked_qty,
			position_keys=EXCLUDED.position_keys,updated_at=clock_timestamp()
			WHERE broker_position_projection.observation_generation < EXCLUDED.observation_generation`,
			accountID, symbol, kind, observationID, generation, int64(providerQty), int64(trackedAfter), keysJSON); err != nil {
			return nil, normalizeDBError(err)
		}
	}

	if changed {
		invalidated, err := invalidateStaleBrokerWork(ctx, tx, observationID, generation, completedAt)
		if err != nil {
			return nil, err
		}
		result.InvalidatedOperationIDs = invalidated
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO broker_reconciliation_head
		(account_id,observation_id,generation) VALUES ($1,$2,$3)
		ON CONFLICT (account_id) DO UPDATE SET observation_id=EXCLUDED.observation_id,
		generation=EXCLUDED.generation,reconciled_at=clock_timestamp()
		WHERE broker_reconciliation_head.generation < EXCLUDED.generation`, accountID, observationID, generation); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := insertEvent(ctx, tx, "broker_observation_reconciled", map[string]any{
		"observation_id": observationID, "generation": generation, "account_id": accountID,
		"external_change_count": len(result.Episodes), "invalidated_operation_count": len(result.InvalidatedOperationIDs),
	}); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	result.Applied = true
	return result, nil
}

func brokerReconciliationLockKey(accountID string) int64 {
	digest := sha256.Sum256([]byte("broker-reconciliation\x00" + accountID))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

func reconciliationEconomicKey(kind, symbol string) string { return kind + "\x00" + symbol }

func loadReconciliationPositions(ctx context.Context, tx *sql.Tx, observationID string) (map[string]reconciliationPosition, map[string]observedOrderIdentity, error) {
	var familyCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM broker_observation_family
		WHERE observation_id=$1 AND family IN ('account','positions','orders') AND status='success'`, observationID).Scan(&familyCount); err != nil {
		return nil, nil, normalizeDBError(err)
	}
	if familyCount != 3 {
		return nil, nil, fmt.Errorf("broker reconciliation observation family set is incomplete")
	}
	rows, err := tx.QueryContext(ctx, `SELECT family,object_key,canonical::text
		FROM broker_observation_item WHERE observation_id=$1 AND family IN ('positions','orders')
		ORDER BY family,object_key`, observationID)
	if err != nil {
		return nil, nil, normalizeDBError(err)
	}
	defer rows.Close()
	positions := map[string]reconciliationPosition{}
	orders := map[string]observedOrderIdentity{}
	for rows.Next() {
		var family, objectKey string
		var canonical []byte
		if err := rows.Scan(&family, &objectKey, &canonical); err != nil {
			return nil, nil, normalizeDBError(err)
		}
		switch family {
		case BrokerFamilyPositions:
			var fact observedPositionIdentity
			if err := json.Unmarshal(canonical, &fact); err != nil || fact.Symbol == "" || fact.Kind == "" || fact.Qty == 0 {
				return nil, nil, fmt.Errorf("broker reconciliation position is invalid")
			}
			key := reconciliationEconomicKey(fact.Kind, fact.Symbol)
			position := positions[key]
			position.symbol, position.kind = fact.Symbol, fact.Kind
			position.qty, err = units.AddQty(position.qty, fact.Qty)
			if err != nil {
				return nil, nil, err
			}
			position.keys = append(position.keys, objectKey)
			positions[key] = position
		case BrokerFamilyOrders:
			var fact observedOrderIdentity
			if err := json.Unmarshal(canonical, &fact); err != nil {
				return nil, nil, fmt.Errorf("broker reconciliation order is invalid")
			}
			orders[objectKey] = fact
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, normalizeDBError(err)
	}
	for key, position := range positions {
		sort.Strings(position.keys)
		positions[key] = position
	}
	return positions, orders, nil
}

func hasUnappliedAlpheusOrderEffect(ctx context.Context, tx *sql.Tx, current map[string]observedOrderIdentity) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.broker_order_id,COALESCE(sum(f.qty),0)
		FROM orders o LEFT JOIN fills f ON f.order_id=o.id
		WHERE o.ledger='live' AND o.broker_order_id IS NOT NULL
		  AND o.state IN ('submitted','partially_filled')
		GROUP BY o.id,o.broker_order_id`)
	if err != nil {
		return false, normalizeDBError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var brokerOrderID string
		var durableFilled int64
		if err := rows.Scan(&brokerOrderID, &durableFilled); err != nil {
			return false, normalizeDBError(err)
		}
		observed, ok := current[brokerOrderID]
		if !ok || int64(observed.FilledQty) != durableFilled {
			return true, nil
		}
	}
	return false, normalizeDBError(rows.Err())
}

func loadReconciliationProjections(ctx context.Context, tx *sql.Tx, accountID string) (map[string]reconciliationProjection, error) {
	rows, err := tx.QueryContext(ctx, `SELECT symbol,kind,provider_qty,tracked_qty,position_keys::text
		FROM broker_position_projection WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	result := map[string]reconciliationProjection{}
	for rows.Next() {
		var symbol, kind string
		var provider, tracked int64
		var raw []byte
		if err := rows.Scan(&symbol, &kind, &provider, &tracked, &raw); err != nil {
			return nil, normalizeDBError(err)
		}
		var keys []string
		if err := json.Unmarshal(raw, &keys); err != nil {
			return nil, fmt.Errorf("broker reconciliation projection is invalid")
		}
		result[reconciliationEconomicKey(kind, symbol)] = reconciliationProjection{
			provider: units.Qty(provider), tracked: units.Qty(tracked), keys: keys,
		}
	}
	return result, normalizeDBError(rows.Err())
}

func loadTrackedExposureQuantities(ctx context.Context, tx *sql.Tx) (map[string]units.Qty, error) {
	rows, err := tx.QueryContext(ctx, `SELECT symbol,kind,sum(opened_qty-closed_qty)
		FROM exposure_lot WHERE ledger='live' AND closed_qty<opened_qty GROUP BY symbol,kind`)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	result := map[string]units.Qty{}
	for rows.Next() {
		var symbol, kind string
		var qty int64
		if err := rows.Scan(&symbol, &kind, &qty); err != nil {
			return nil, normalizeDBError(err)
		}
		result[reconciliationEconomicKey(kind, symbol)] = units.Qty(qty)
	}
	return result, normalizeDBError(rows.Err())
}

func reconciliationKeys(current map[string]reconciliationPosition, previous map[string]reconciliationProjection, tracked map[string]units.Qty) []string {
	set := map[string]bool{}
	for key := range current {
		set[key] = true
	}
	for key := range previous {
		set[key] = true
	}
	for key := range tracked {
		set[key] = true
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func externalPositionChangeKind(hasPrior bool, beforeExternal, afterExternal, adjusted units.Qty) string {
	if !hasPrior {
		return "baseline"
	}
	if beforeExternal != 0 && afterExternal != 0 && (beforeExternal < 0) != (afterExternal < 0) {
		return "external_reversal"
	}
	if adjusted > 0 || absoluteQty(afterExternal) < absoluteQty(beforeExternal) {
		return "external_reduce"
	}
	return "external_add"
}

func absoluteQty(qty units.Qty) units.Qty {
	if qty < 0 {
		return -qty
	}
	return qty
}

func externalPositionChangeOrigin(trackedBefore units.Qty, positionKeyCount int) string {
	if positionKeyCount > 1 {
		return "ambiguous"
	}
	if trackedBefore > 0 {
		return "mixed"
	}
	return "external"
}

func insertBrokerExternalChangeEpisode(ctx context.Context, tx *sql.Tx, episode BrokerExternalChangeEpisode) error {
	keys, err := json.Marshal(episode.PositionKeys)
	if err != nil {
		return err
	}
	var before any
	if episode.ProviderQtyBefore != nil {
		before = int64(*episode.ProviderQtyBefore)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO broker_external_change_episode
		(id,account_id,broker_observation_id,observation_generation,symbol,kind,
		 change_kind,origin,provider_qty_before,provider_qty_after,tracked_qty_before,
		 tracked_qty_after,adjusted_tracked_qty,position_keys,attribution_status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'uncertain')`,
		episode.ID, episode.AccountID, episode.BrokerObservationID, episode.ObservationGeneration,
		episode.Symbol, episode.Kind, episode.ChangeKind, episode.Origin, before,
		int64(episode.ProviderQtyAfter), int64(episode.TrackedQtyBefore), int64(episode.TrackedQtyAfter),
		int64(episode.AdjustedTrackedQty), keys)
	return normalizeDBError(err)
}

func applyExternalExposureReduction(ctx context.Context, tx *sql.Tx, episode BrokerExternalChangeEpisode, observedAt time.Time) error {
	remaining := int64(episode.AdjustedTrackedQty)
	rows, err := tx.QueryContext(ctx, `SELECT open_fill_id,opened_qty,closed_qty,
		entry_cost_micros,remaining_cost_basis_micros,remaining_risk_micros
		FROM exposure_lot WHERE ledger='live' AND symbol=$1 AND kind=$2 AND closed_qty<opened_qty
		ORDER BY opened_at,open_fill_id FOR UPDATE`, episode.Symbol, episode.Kind)
	if err != nil {
		return normalizeDBError(err)
	}
	type lotRow struct {
		id                                         string
		opened, closed, entry, remainingCost, risk int64
	}
	var lots []lotRow
	for rows.Next() {
		var lot lotRow
		if err := rows.Scan(&lot.id, &lot.opened, &lot.closed, &lot.entry, &lot.remainingCost, &lot.risk); err != nil {
			rows.Close()
			return normalizeDBError(err)
		}
		lots = append(lots, lot)
	}
	if err := rows.Close(); err != nil {
		return normalizeDBError(err)
	}
	for _, lot := range lots {
		if remaining == 0 {
			break
		}
		available := lot.opened - lot.closed
		matched := available
		if matched > remaining {
			matched = remaining
		}
		nextClosed := lot.closed + matched
		nextRemaining := lot.opened - nextClosed
		nextRisk, err := proportionalInt64(lot.entry, nextRemaining, lot.opened, true)
		if err != nil {
			return err
		}
		nextCost, err := proportionalInt64(lot.entry, nextRemaining, lot.opened, false)
		if err != nil {
			return err
		}
		matchedCost, releasedRisk := lot.remainingCost-nextCost, lot.risk-nextRisk
		if matchedCost < 0 || releasedRisk < 0 {
			return fmt.Errorf("%w: external reduction increased exposure remainder", ErrFillIntegrity)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO broker_external_exposure_allocation
			(episode_id,open_fill_id,qty,matched_cost_micros,released_risk_micros)
			VALUES ($1,$2,$3,$4,$5)`, episode.ID, lot.id, matched, matchedCost, releasedRisk); err != nil {
			return normalizeDBError(err)
		}
		var closedAt any
		if nextRemaining == 0 {
			closedAt = observedAt
		}
		if _, err := tx.ExecContext(ctx, `UPDATE exposure_lot SET closed_qty=$2,
			remaining_cost_basis_micros=$3,remaining_risk_micros=$4,closed_at=$5
			WHERE open_fill_id=$1`, lot.id, nextClosed, nextCost, nextRisk, closedAt); err != nil {
			return normalizeDBError(err)
		}
		remaining -= matched
	}
	if remaining != 0 {
		return fmt.Errorf("%w: external reduction exceeds FIFO exposure", ErrFillIntegrity)
	}
	return nil
}

func invalidateStaleBrokerWork(ctx context.Context, tx *sql.Tx, observationID string, generation int64, completedAt time.Time) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.status FROM operations o
		WHERE o.ts < $1 AND o.status IN ('auto_approved','pending_review','approved')
		  AND NOT COALESCE((o.payload->>'shadow')::boolean,false)
		  AND NOT EXISTS (SELECT 1 FROM execution_attempt a WHERE a.operation_id=o.id AND a.state IN ('claimed','unknown'))
		  AND NOT EXISTS (SELECT 1 FROM orders ord WHERE ord.operation_id=o.id AND ord.state IN ('submitted','partially_filled'))
		ORDER BY o.ts,o.id FOR UPDATE`, completedAt)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	type candidate struct{ id, status string }
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.status); err != nil {
			rows.Close()
			return nil, normalizeDBError(err)
		}
		candidates = append(candidates, c)
	}
	if err := rows.Close(); err != nil {
		return nil, normalizeDBError(err)
	}
	invalidated := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		result, err := tx.ExecContext(ctx, `INSERT INTO broker_operation_invalidation
			(operation_id,broker_observation_id,observation_generation,reason)
			VALUES ($1,$2,$3,'external_broker_state_changed') ON CONFLICT (operation_id) DO NOTHING`,
			candidate.id, observationID, generation)
		if err != nil {
			return nil, normalizeDBError(err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return nil, normalizeDBError(err)
		}
		if inserted == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE execution_attempt SET state='failed',
			last_error='external broker state changed',resolved_at=clock_timestamp()
			WHERE operation_id=$1 AND state='pending'`, candidate.id); err != nil {
			return nil, normalizeDBError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET state='rejected',updated_at=clock_timestamp()
			WHERE operation_id=$1 AND state='new' AND broker_order_id IS NULL`, candidate.id); err != nil {
			return nil, normalizeDBError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE close_reservation SET state='released',remaining_qty=0,
			released_at=COALESCE(released_at,clock_timestamp()) WHERE operation_id=$1 AND state='held'`, candidate.id); err != nil {
			return nil, normalizeDBError(err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE open_reservation SET resource_state='released',
			remaining_risk_micros=0,remaining_cash_micros=0,settled_at=COALESCE(settled_at,clock_timestamp())
			WHERE operation_id=$1 AND resource_state='held'`, candidate.id); err != nil {
			return nil, normalizeDBError(err)
		}
		nextStatus := "expired"
		if candidate.status != "pending_review" {
			nextStatus, err = operationStatusAfterFailedAttempt(ctx, tx, candidate.id)
			if err != nil {
				return nil, normalizeDBError(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET status=$2 WHERE id=$1`, candidate.id, nextStatus); err != nil {
			return nil, normalizeDBError(err)
		}
		if err := insertEvent(ctx, tx, "operation_stale_broker_state", map[string]any{
			"operation_id": candidate.id, "observation_id": observationID,
			"observation_generation": generation, "reason": "external_broker_state_changed",
		}); err != nil {
			return nil, normalizeDBError(err)
		}
		invalidated = append(invalidated, candidate.id)
	}
	return invalidated, nil
}
