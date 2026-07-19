package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

// BrokerCoexistenceView is the read-only Kernel projection for a shared
// brokerage account. ObservedOrigin describes the Provider object evidence;
// ExposureOrigin describes only Alpheus's conservative economic allocation.
// Keeping both prevents an aggregate or mixed position from being adopted as
// a fictional system-owned lifecycle.
type BrokerCoexistenceView struct {
	AccountID             string                        `json:"account_id"`
	Reconciliation        BrokerReconciliationStatus    `json:"reconciliation"`
	Positions             []BrokerPositionExposure      `json:"positions"`
	ExternalChanges       []BrokerExternalChangeRecord  `json:"external_changes"`
	InvalidatedOperations []BrokerOperationInvalidation `json:"invalidated_operations"`
}

type BrokerReconciliationStatus struct {
	State                   string     `json:"state"`
	ObservedObservationID   string     `json:"observed_observation_id"`
	ObservedGeneration      int64      `json:"observed_generation"`
	ObservedAt              time.Time  `json:"observed_at"`
	ReconciledObservationID string     `json:"reconciled_observation_id,omitempty"`
	ReconciledGeneration    int64      `json:"reconciled_generation,omitempty"`
	ReconciledAt            *time.Time `json:"reconciled_at,omitempty"`
}

type BrokerPositionExposure struct {
	Symbol                string    `json:"symbol"`
	Kind                  string    `json:"kind"`
	ObservationID         string    `json:"observation_id"`
	ObservationGeneration int64     `json:"observation_generation"`
	ProviderQty           units.Qty `json:"provider_qty"`
	TrackedQty            units.Qty `json:"tracked_qty"`
	ExternalQty           units.Qty `json:"external_qty"`
	ExposureOrigin        string    `json:"exposure_origin"`
	ObservedOrigin        string    `json:"observed_origin"`
	OriginEvidence        string    `json:"origin_evidence"`
	PositionKeys          []string  `json:"position_keys"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type BrokerExternalExposureAllocation struct {
	OpenFillID   string       `json:"open_fill_id"`
	Qty          units.Qty    `json:"qty"`
	MatchedCost  units.Micros `json:"matched_cost"`
	ReleasedRisk units.Micros `json:"released_risk"`
	CreatedAt    time.Time    `json:"created_at"`
}

type BrokerExternalChangeRecord struct {
	BrokerExternalChangeEpisode
	CreatedAt   time.Time                          `json:"created_at"`
	Allocations []BrokerExternalExposureAllocation `json:"allocations"`
}

type BrokerOperationInvalidation struct {
	OperationID           string    `json:"operation_id"`
	OperationStatus       string    `json:"operation_status"`
	BrokerObservationID   string    `json:"broker_observation_id"`
	ObservationGeneration int64     `json:"observation_generation"`
	Reason                string    `json:"reason"`
	CreatedAt             time.Time `json:"created_at"`
}

type brokerPositionOriginEvidence struct {
	origin   string
	evidence map[string]bool
}

// LoadBrokerCoexistenceView reconstructs the current shared-account read model
// from durable evidence only. It never calls a Provider and never changes
// reconciliation, exposure, order, fill, or PnL state.
func (s *Store) LoadBrokerCoexistenceView(accountID string, historyLimit int) (*BrokerCoexistenceView, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("broker account id is required")
	}
	if historyLimit < 1 || historyLimit > 100 {
		return nil, fmt.Errorf("broker coexistence history limit must be from 1 to 100")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()

	view := &BrokerCoexistenceView{
		AccountID: accountID, Positions: []BrokerPositionExposure{},
		ExternalChanges:       []BrokerExternalChangeRecord{},
		InvalidatedOperations: []BrokerOperationInvalidation{},
	}
	status := &view.Reconciliation
	if err := tx.QueryRowContext(ctx, `SELECT o.id,o.generation,o.completed_at
		FROM broker_observation_head h JOIN broker_observation o ON o.id=h.observation_id
		WHERE h.account_id=$1`, accountID).Scan(
		&status.ObservedObservationID, &status.ObservedGeneration, &status.ObservedAt,
	); err != nil {
		return nil, normalizeDBError(err)
	}
	var reconciledAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT observation_id,generation,reconciled_at
		FROM broker_reconciliation_head WHERE account_id=$1`, accountID).Scan(
		&status.ReconciledObservationID, &status.ReconciledGeneration, &reconciledAt,
	)
	switch {
	case err == sql.ErrNoRows:
		status.State = "uninitialized"
	case err != nil:
		return nil, normalizeDBError(err)
	default:
		status.ReconciledAt = &reconciledAt
		if status.ReconciledGeneration == status.ObservedGeneration {
			status.State = "current"
		} else {
			status.State = "pending"
		}
	}

	positions, err := loadBrokerPositionExposureView(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	view.Positions = positions
	changes, err := loadBrokerExternalChangesView(ctx, tx, accountID, historyLimit)
	if err != nil {
		return nil, err
	}
	view.ExternalChanges = changes
	invalidations, err := loadBrokerOperationInvalidationsView(ctx, tx, accountID, historyLimit)
	if err != nil {
		return nil, err
	}
	view.InvalidatedOperations = invalidations
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return view, nil
}

func loadBrokerPositionExposureView(ctx context.Context, tx *sql.Tx, accountID string) ([]BrokerPositionExposure, error) {
	rows, err := tx.QueryContext(ctx, `SELECT symbol,kind,observation_id,observation_generation,
		provider_qty,tracked_qty,position_keys::text,updated_at
		FROM broker_position_projection WHERE account_id=$1
		  AND (provider_qty<>0 OR tracked_qty<>0) ORDER BY symbol,kind`, accountID)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	positions := []BrokerPositionExposure{}
	for rows.Next() {
		var position BrokerPositionExposure
		var providerQty, trackedQty int64
		var rawKeys []byte
		if err := rows.Scan(&position.Symbol, &position.Kind, &position.ObservationID,
			&position.ObservationGeneration, &providerQty, &trackedQty, &rawKeys,
			&position.UpdatedAt); err != nil {
			rows.Close()
			return nil, normalizeDBError(err)
		}
		position.ProviderQty, position.TrackedQty = units.Qty(providerQty), units.Qty(trackedQty)
		if err := json.Unmarshal(rawKeys, &position.PositionKeys); err != nil {
			rows.Close()
			return nil, fmt.Errorf("broker position projection keys are invalid")
		}
		externalQty, err := units.AddQty(position.ProviderQty, -position.TrackedQty)
		if err != nil {
			rows.Close()
			return nil, err
		}
		position.ExternalQty = externalQty
		position.ExposureOrigin = brokerExposureOrigin(position.TrackedQty, externalQty, len(position.PositionKeys))
		positions = append(positions, position)
	}
	if err := rows.Close(); err != nil {
		return nil, normalizeDBError(err)
	}

	evidence := make(map[string]*brokerPositionOriginEvidence, len(positions))
	rows, err = tx.QueryContext(ctx, `SELECT p.symbol,p.kind,COALESCE(e.origin,''),COALESCE(e.evidence,'')
		FROM broker_position_projection p
		LEFT JOIN LATERAL jsonb_array_elements_text(p.position_keys) k(object_key) ON true
		LEFT JOIN broker_object_origin_event e ON e.observation_id=p.observation_id
		 AND e.family='positions' AND e.object_key=k.object_key
		WHERE p.account_id=$1 AND (p.provider_qty<>0 OR p.tracked_qty<>0)
		ORDER BY p.symbol,p.kind,k.object_key`, accountID)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	for rows.Next() {
		var symbol, kind, origin, originEvidence string
		if err := rows.Scan(&symbol, &kind, &origin, &originEvidence); err != nil {
			rows.Close()
			return nil, normalizeDBError(err)
		}
		key := reconciliationEconomicKey(kind, symbol)
		fact := evidence[key]
		if fact == nil {
			fact = &brokerPositionOriginEvidence{evidence: map[string]bool{}}
			evidence[key] = fact
		}
		if brokerOriginPriority(origin) > brokerOriginPriority(fact.origin) {
			fact.origin = origin
		}
		if originEvidence != "" {
			fact.evidence[originEvidence] = true
		}
	}
	if err := rows.Close(); err != nil {
		return nil, normalizeDBError(err)
	}
	for i := range positions {
		key := reconciliationEconomicKey(positions[i].Kind, positions[i].Symbol)
		fact := evidence[key]
		positions[i].ObservedOrigin, positions[i].OriginEvidence = "ambiguous", "unavailable"
		if fact == nil {
			continue
		}
		if fact.origin != "" {
			positions[i].ObservedOrigin = fact.origin
		}
		values := make([]string, 0, len(fact.evidence))
		for value := range fact.evidence {
			values = append(values, value)
		}
		sort.Strings(values)
		if len(values) > 0 {
			positions[i].OriginEvidence = strings.Join(values, ",")
		}
	}
	return positions, nil
}

func brokerExposureOrigin(trackedQty, externalQty units.Qty, positionKeyCount int) string {
	if positionKeyCount > 1 {
		return "ambiguous"
	}
	if trackedQty > 0 && externalQty != 0 {
		return "mixed"
	}
	if trackedQty > 0 {
		return "alpheus"
	}
	return "external"
}

func brokerOriginPriority(origin string) int {
	switch origin {
	case "ambiguous":
		return 3
	case "external":
		return 2
	case "alpheus":
		return 1
	default:
		return 0
	}
}

func loadBrokerExternalChangesView(ctx context.Context, tx *sql.Tx, accountID string, limit int) ([]BrokerExternalChangeRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,broker_observation_id,observation_generation,
		symbol,kind,change_kind,origin,provider_qty_before,provider_qty_after,
		tracked_qty_before,tracked_qty_after,adjusted_tracked_qty,position_keys::text,
		attribution_status,created_at
		FROM broker_external_change_episode WHERE account_id=$1
		ORDER BY observation_generation DESC,id DESC LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	changes := []BrokerExternalChangeRecord{}
	changeIndex := map[string]int{}
	for rows.Next() {
		change := BrokerExternalChangeRecord{Allocations: []BrokerExternalExposureAllocation{}}
		var before sql.NullInt64
		var providerAfter, trackedBefore, trackedAfter, adjusted int64
		var rawKeys []byte
		if err := rows.Scan(&change.ID, &change.BrokerObservationID,
			&change.ObservationGeneration, &change.Symbol, &change.Kind, &change.ChangeKind,
			&change.Origin, &before, &providerAfter, &trackedBefore, &trackedAfter,
			&adjusted, &rawKeys, &change.AttributionStatus, &change.CreatedAt); err != nil {
			rows.Close()
			return nil, normalizeDBError(err)
		}
		change.AccountID = accountID
		if before.Valid {
			qty := units.Qty(before.Int64)
			change.ProviderQtyBefore = &qty
		}
		change.ProviderQtyAfter = units.Qty(providerAfter)
		change.TrackedQtyBefore = units.Qty(trackedBefore)
		change.TrackedQtyAfter = units.Qty(trackedAfter)
		change.AdjustedTrackedQty = units.Qty(adjusted)
		if err := json.Unmarshal(rawKeys, &change.PositionKeys); err != nil {
			rows.Close()
			return nil, fmt.Errorf("broker external change keys are invalid")
		}
		changeIndex[change.ID] = len(changes)
		changes = append(changes, change)
	}
	if err := rows.Close(); err != nil {
		return nil, normalizeDBError(err)
	}
	if len(changes) == 0 {
		return changes, nil
	}
	rows, err = tx.QueryContext(ctx, `WITH recent AS (
		SELECT id FROM broker_external_change_episode WHERE account_id=$1
		ORDER BY observation_generation DESC,id DESC LIMIT $2
	) SELECT a.episode_id,a.open_fill_id,a.qty,a.matched_cost_micros,
		a.released_risk_micros,a.created_at
		FROM broker_external_exposure_allocation a JOIN recent r ON r.id=a.episode_id
		ORDER BY a.episode_id,a.created_at,a.open_fill_id`, accountID, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	for rows.Next() {
		var episodeID string
		var allocation BrokerExternalExposureAllocation
		var qty, cost, risk int64
		if err := rows.Scan(&episodeID, &allocation.OpenFillID, &qty, &cost, &risk,
			&allocation.CreatedAt); err != nil {
			rows.Close()
			return nil, normalizeDBError(err)
		}
		allocation.Qty, allocation.MatchedCost, allocation.ReleasedRisk =
			units.Qty(qty), units.Micros(cost), units.Micros(risk)
		if index, ok := changeIndex[episodeID]; ok {
			changes[index].Allocations = append(changes[index].Allocations, allocation)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, normalizeDBError(err)
	}
	return changes, nil
}

func loadBrokerOperationInvalidationsView(ctx context.Context, tx *sql.Tx, accountID string, limit int) ([]BrokerOperationInvalidation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT i.operation_id,o.status,i.broker_observation_id,
		i.observation_generation,i.reason,i.created_at
		FROM broker_operation_invalidation i
		JOIN broker_observation b ON b.id=i.broker_observation_id
		JOIN operations o ON o.id=i.operation_id
		WHERE b.account_id=$1 ORDER BY i.observation_generation DESC,i.operation_id DESC LIMIT $2`,
		accountID, limit)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	invalidations := []BrokerOperationInvalidation{}
	for rows.Next() {
		var invalidation BrokerOperationInvalidation
		if err := rows.Scan(&invalidation.OperationID, &invalidation.OperationStatus,
			&invalidation.BrokerObservationID, &invalidation.ObservationGeneration,
			&invalidation.Reason, &invalidation.CreatedAt); err != nil {
			return nil, normalizeDBError(err)
		}
		invalidations = append(invalidations, invalidation)
	}
	return invalidations, normalizeDBError(rows.Err())
}
