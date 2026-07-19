package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"alpheus/kernel/internal/units"
)

const (
	BrokerFamilyAccount   = "account"
	BrokerFamilyPositions = "positions"
	BrokerFamilyOrders    = "orders"
	BrokerFamilyFills     = "fills"
)

type BrokerObservationInput struct {
	ID          string
	AccountID   string
	Source      string
	Purpose     string
	StartedAt   time.Time
	CompletedAt time.Time
	Families    []BrokerObservationFamilyInput
}

type BrokerObservationFamilyInput struct {
	Family      string
	Status      string
	ErrorCode   string
	CompletedAt time.Time
	Items       []BrokerObservationItemInput
}

type BrokerObservationItemInput struct {
	ObjectKey  string
	ObservedAt time.Time
	Canonical  any
}

type BrokerObservation struct {
	ID             string    `json:"id"`
	Generation     int64     `json:"generation"`
	AccountID      string    `json:"account_id"`
	Source         string    `json:"source"`
	Purpose        string    `json:"purpose"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
	Status         string    `json:"status"`
	ManifestDigest string    `json:"manifest_digest"`
}

type BrokerObservedObject struct {
	Family       string          `json:"family"`
	ObjectKey    string          `json:"object_key"`
	ObservedAt   time.Time       `json:"observed_at"`
	ObjectDigest string          `json:"object_digest"`
	Canonical    json.RawMessage `json:"canonical"`
	Origin       string          `json:"origin,omitempty"`
	Evidence     string          `json:"origin_evidence,omitempty"`
}

type BrokerAccountView struct {
	Observation BrokerObservation      `json:"observation"`
	Objects     []BrokerObservedObject `json:"objects"`
}

type normalizedObservationFamily struct {
	name        string
	status      string
	errorCode   string
	completedAt time.Time
	digest      [sha256.Size]byte
	items       []normalizedObservationItem
}

type normalizedObservationItem struct {
	key        string
	observedAt time.Time
	canonical  []byte
	digest     [sha256.Size]byte
}

type brokerOrigin struct {
	origin           string
	evidence         string
	matchedOrderID   string
	matchedAttemptID string
}

func (s *Store) RecordBrokerObservation(input BrokerObservationInput) (*BrokerObservation, error) {
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.Source = strings.TrimSpace(input.Source)
	if input.ID == "" {
		input.ID = NewID()
	}
	families, status, manifest, completeAccountView, err := normalizeBrokerObservation(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer tx.Rollback()

	var observation BrokerObservation
	err = tx.QueryRowContext(ctx, `INSERT INTO broker_observation
		(id,account_id,source,purpose,started_at,completed_at,status,manifest_digest)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING generation`, input.ID, input.AccountID, input.Source, input.Purpose,
		input.StartedAt, input.CompletedAt, status, manifest[:]).Scan(&observation.Generation)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	observation = BrokerObservation{
		ID: input.ID, Generation: observation.Generation, AccountID: input.AccountID,
		Source: input.Source, Purpose: input.Purpose, StartedAt: input.StartedAt,
		CompletedAt: input.CompletedAt, Status: status, ManifestDigest: hex.EncodeToString(manifest[:]),
	}
	for _, family := range families {
		var errorCode any
		if family.errorCode != "" {
			errorCode = family.errorCode
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO broker_observation_family
			(observation_id,family,status,error_code,completed_at,item_count,family_digest)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, input.ID, family.name, family.status,
			errorCode, family.completedAt, len(family.items), family.digest[:]); err != nil {
			return nil, normalizeDBError(err)
		}
		for _, item := range family.items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO broker_observation_item
				(observation_id,family,object_key,observed_at,object_digest,canonical)
				VALUES ($1,$2,$3,$4,$5,$6)`, input.ID, family.name, item.key,
				item.observedAt, item.digest[:], string(item.canonical)); err != nil {
				return nil, normalizeDBError(err)
			}
			if family.name == BrokerFamilyAccount {
				continue
			}
			origin, err := classifyBrokerObjectOrigin(ctx, tx, input.AccountID, family.name, item.canonical)
			if err != nil {
				return nil, normalizeDBError(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO broker_object_origin_event
				(observation_id,family,object_key,origin,evidence,matched_order_id,matched_attempt_id)
				VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid)`,
				input.ID, family.name, item.key, origin.origin, origin.evidence,
				origin.matchedOrderID, origin.matchedAttemptID); err != nil {
				return nil, normalizeDBError(err)
			}
		}
	}
	if completeAccountView {
		if _, err := tx.ExecContext(ctx, `INSERT INTO broker_observation_head
			(account_id,observation_id,generation,updated_at) VALUES ($1,$2,$3,$4)
			ON CONFLICT (account_id) DO UPDATE SET
			observation_id=EXCLUDED.observation_id,generation=EXCLUDED.generation,updated_at=EXCLUDED.updated_at
			WHERE broker_observation_head.generation < EXCLUDED.generation`, input.AccountID,
			input.ID, observation.Generation, input.CompletedAt); err != nil {
			return nil, normalizeDBError(err)
		}
	}
	if err := insertEvent(ctx, tx, "broker_observation_recorded", map[string]any{
		"observation_id": input.ID, "generation": observation.Generation,
		"account_id": input.AccountID, "source": input.Source, "purpose": input.Purpose,
		"status": status, "manifest_digest": observation.ManifestDigest,
		"complete_account_view": completeAccountView,
	}); err != nil {
		return nil, normalizeDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, normalizeDBError(err)
	}
	return &observation, nil
}

func normalizeBrokerObservation(input BrokerObservationInput) ([]normalizedObservationFamily, string, [sha256.Size]byte, bool, error) {
	var zero [sha256.Size]byte
	if input.ID == "" || input.AccountID == "" || input.Source == "" ||
		input.StartedAt.IsZero() || input.CompletedAt.IsZero() || input.CompletedAt.Before(input.StartedAt) {
		return nil, "", zero, false, fmt.Errorf("broker observation identity or time range is invalid")
	}
	switch input.Purpose {
	case "decision", "pre_effect", "reconciliation", "read_model", "manual_refresh":
	default:
		return nil, "", zero, false, fmt.Errorf("broker observation purpose is invalid")
	}
	if len(input.Families) == 0 || len(input.Families) > 4 {
		return nil, "", zero, false, fmt.Errorf("broker observation family set is invalid")
	}
	seenFamilies := map[string]bool{}
	normalized := make([]normalizedObservationFamily, 0, len(input.Families))
	status := "complete"
	for _, family := range input.Families {
		if seenFamilies[family.Family] || !validBrokerFamily(family.Family) || family.CompletedAt.IsZero() ||
			family.CompletedAt.Before(input.StartedAt) || family.CompletedAt.After(input.CompletedAt) {
			return nil, "", zero, false, fmt.Errorf("broker observation family is invalid")
		}
		seenFamilies[family.Family] = true
		n := normalizedObservationFamily{name: family.Family, status: family.Status, completedAt: family.CompletedAt}
		switch family.Status {
		case "success":
			if strings.TrimSpace(family.ErrorCode) != "" {
				return nil, "", zero, false, fmt.Errorf("successful broker family has an error")
			}
		case "error":
			n.errorCode = strings.TrimSpace(family.ErrorCode)
			if !validObservationErrorCode(n.errorCode) || len(family.Items) != 0 {
				return nil, "", zero, false, fmt.Errorf("failed broker family is invalid")
			}
			status = "partial"
		default:
			return nil, "", zero, false, fmt.Errorf("broker observation family status is invalid")
		}
		seenItems := map[string]bool{}
		for _, item := range family.Items {
			key := strings.TrimSpace(item.ObjectKey)
			if key == "" || seenItems[key] || item.ObservedAt.IsZero() || item.ObservedAt.After(family.CompletedAt) {
				return nil, "", zero, false, fmt.Errorf("broker observation item is invalid")
			}
			seenItems[key] = true
			canonical, err := canonicalObservationObject(item.Canonical)
			if err != nil {
				return nil, "", zero, false, err
			}
			if err := validateObservationObjectKey(family.Family, key, input.AccountID, canonical); err != nil {
				return nil, "", zero, false, err
			}
			n.items = append(n.items, normalizedObservationItem{
				key: key, observedAt: item.ObservedAt, canonical: canonical, digest: sha256.Sum256(canonical),
			})
		}
		sort.Slice(n.items, func(i, j int) bool { return n.items[i].key < n.items[j].key })
		familyManifest := make([]map[string]string, 0, len(n.items))
		for _, item := range n.items {
			familyManifest = append(familyManifest, map[string]string{
				"key": item.key, "digest": hex.EncodeToString(item.digest[:]),
			})
		}
		familyBytes, _ := json.Marshal(familyManifest)
		n.digest = sha256.Sum256(familyBytes)
		normalized = append(normalized, n)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].name < normalized[j].name })
	manifestFamilies := make([]map[string]any, 0, len(normalized))
	for _, family := range normalized {
		manifestFamilies = append(manifestFamilies, map[string]any{
			"family": family.name, "status": family.status, "error_code": family.errorCode,
			"completed_at": family.completedAt.UTC().Format(time.RFC3339Nano),
			"digest":       hex.EncodeToString(family.digest[:]),
		})
	}
	manifestBytes, _ := json.Marshal(map[string]any{
		"id": input.ID, "account_id": input.AccountID, "source": input.Source, "purpose": input.Purpose,
		"started_at":   input.StartedAt.UTC().Format(time.RFC3339Nano),
		"completed_at": input.CompletedAt.UTC().Format(time.RFC3339Nano), "families": manifestFamilies,
	})
	completeAccountView := status == "complete" && seenFamilies[BrokerFamilyAccount] &&
		seenFamilies[BrokerFamilyPositions] && seenFamilies[BrokerFamilyOrders]
	return normalized, status, sha256.Sum256(manifestBytes), completeAccountView, nil
}

func validBrokerFamily(family string) bool {
	switch family {
	case BrokerFamilyAccount, BrokerFamilyPositions, BrokerFamilyOrders, BrokerFamilyFills:
		return true
	default:
		return false
	}
}

func validObservationErrorCode(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func canonicalObservationObject(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal broker observation item: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, fmt.Errorf("broker observation item must be a JSON object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("canonicalize broker observation item: %w", err)
	}
	return canonical, nil
}

type observedOrderIdentity struct {
	BrokerOrderID   string       `json:"broker_order_id"`
	ClientOrderID   string       `json:"client_order_id"`
	Symbol          string       `json:"symbol"`
	Side            string       `json:"side"`
	Kind            string       `json:"kind"`
	PositionEffect  string       `json:"position_effect"`
	Qty             units.Qty    `json:"qty"`
	LimitPrice      units.Micros `json:"limit_price"`
	LimitPriceKnown bool         `json:"limit_price_known"`
}

type observedFillIdentity struct {
	FillID        string       `json:"fill_id"`
	BrokerOrderID string       `json:"broker_order_id"`
	Qty           units.Qty    `json:"qty"`
	Price         units.Micros `json:"price"`
	Fees          units.Micros `json:"fees"`
}

type observedPositionIdentity struct {
	PositionID string    `json:"position_id"`
	Symbol     string    `json:"symbol"`
	Kind       string    `json:"kind"`
	Qty        units.Qty `json:"qty"`
}

func validateObservationObjectKey(family, key, accountID string, canonical []byte) error {
	switch family {
	case BrokerFamilyAccount:
		var account struct {
			AccountID string `json:"account_id"`
		}
		if err := json.Unmarshal(canonical, &account); err != nil || key != accountID || account.AccountID != accountID {
			return fmt.Errorf("observed account identity is invalid")
		}
	case BrokerFamilyPositions:
		var position observedPositionIdentity
		if err := json.Unmarshal(canonical, &position); err != nil || position.PositionID == "" || position.PositionID != key {
			return fmt.Errorf("observed position key is invalid")
		}
	case BrokerFamilyOrders:
		var order observedOrderIdentity
		if err := json.Unmarshal(canonical, &order); err != nil || order.BrokerOrderID == "" || order.BrokerOrderID != key {
			return fmt.Errorf("observed order key is invalid")
		}
	case BrokerFamilyFills:
		var fill observedFillIdentity
		if err := json.Unmarshal(canonical, &fill); err != nil || fill.FillID == "" || fill.FillID != key {
			return fmt.Errorf("observed fill key is invalid")
		}
	}
	return nil
}

func classifyBrokerObjectOrigin(ctx context.Context, tx *sql.Tx, accountID, family string, canonical []byte) (brokerOrigin, error) {
	switch family {
	case BrokerFamilyOrders:
		return classifyObservedOrder(ctx, tx, accountID, canonical)
	case BrokerFamilyFills:
		return classifyObservedFill(ctx, tx, accountID, canonical)
	case BrokerFamilyPositions:
		var position observedPositionIdentity
		if err := json.Unmarshal(canonical, &position); err != nil || position.PositionID == "" ||
			position.Symbol == "" || position.Kind == "" || position.Qty == 0 {
			return brokerOrigin{}, fmt.Errorf("observed position identity is invalid")
		}
		var internalQty int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(opened_qty-closed_qty),0)
			FROM exposure_lot WHERE ledger='live' AND symbol=$1 AND kind=$2 AND closed_qty<opened_qty`,
			position.Symbol, position.Kind).Scan(&internalQty); err != nil {
			return brokerOrigin{}, err
		}
		if internalQty == 0 {
			return brokerOrigin{origin: "external", evidence: "unmatched"}, nil
		}
		return brokerOrigin{origin: "ambiguous", evidence: "aggregate_overlap"}, nil
	default:
		return brokerOrigin{}, fmt.Errorf("unsupported broker origin family")
	}
}

func classifyObservedOrder(ctx context.Context, tx *sql.Tx, accountID string, canonical []byte) (brokerOrigin, error) {
	var observed observedOrderIdentity
	if err := json.Unmarshal(canonical, &observed); err != nil || observed.BrokerOrderID == "" ||
		observed.Symbol == "" || observed.Kind == "" || (observed.Side != "buy" && observed.Side != "sell") || observed.Qty <= 0 {
		return brokerOrigin{}, fmt.Errorf("observed order identity is invalid")
	}
	rows, err := tx.QueryContext(ctx, `SELECT o.id,a.id,a.provider_account_id,o.broker_order_id,
		o.client_order_id,o.symbol,o.side,o.kind,o.qty,o.limit_micros,
		COALESCE(a.provider_intent->>'position_effect','')
		FROM orders o JOIN execution_attempt a ON a.id=o.execution_attempt_id
		WHERE o.broker_order_id=$1 OR ($2<>'' AND o.client_order_id=$2)`,
		observed.BrokerOrderID, observed.ClientOrderID)
	if err != nil {
		return brokerOrigin{}, err
	}
	defer rows.Close()
	type candidate struct {
		orderID, attemptID, accountID, brokerID, clientID, symbol, side, kind, positionEffect string
		qty                                                                                   units.Qty
		limit                                                                                 units.Micros
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		var providerAccount sql.NullString
		if err := rows.Scan(&c.orderID, &c.attemptID, &providerAccount, &c.brokerID, &c.clientID,
			&c.symbol, &c.side, &c.kind, &c.qty, &c.limit, &c.positionEffect); err != nil {
			return brokerOrigin{}, err
		}
		c.accountID = providerAccount.String
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return brokerOrigin{}, err
	}
	if len(candidates) == 0 {
		return brokerOrigin{origin: "external", evidence: "unmatched"}, nil
	}
	if len(candidates) != 1 {
		return brokerOrigin{origin: "ambiguous", evidence: "identity_conflict"}, nil
	}
	c := candidates[0]
	brokerMatch := c.brokerID != "" && c.brokerID == observed.BrokerOrderID
	clientMatch := observed.ClientOrderID != "" && c.clientID == observed.ClientOrderID
	semanticsMatch := c.accountID == accountID && c.symbol == observed.Symbol && c.side == observed.Side &&
		c.kind == observed.Kind && c.qty == observed.Qty && observed.LimitPriceKnown && c.limit == observed.LimitPrice
	if observed.PositionEffect != "" && observed.PositionEffect != "unknown" {
		semanticsMatch = semanticsMatch && c.positionEffect == observed.PositionEffect
	}
	if !semanticsMatch || (!brokerMatch && !clientMatch) || (c.brokerID != "" && !brokerMatch) {
		return brokerOrigin{origin: "ambiguous", evidence: "identity_conflict"}, nil
	}
	evidence := "exact_client_reference"
	if brokerMatch {
		evidence = "exact_broker_order_id"
	}
	return brokerOrigin{origin: "alpheus", evidence: evidence, matchedOrderID: c.orderID, matchedAttemptID: c.attemptID}, nil
}

func classifyObservedFill(ctx context.Context, tx *sql.Tx, accountID string, canonical []byte) (brokerOrigin, error) {
	var observed observedFillIdentity
	if err := json.Unmarshal(canonical, &observed); err != nil || observed.FillID == "" ||
		observed.BrokerOrderID == "" || observed.Qty <= 0 || observed.Price <= 0 || observed.Fees < 0 {
		return brokerOrigin{}, fmt.Errorf("observed fill identity is invalid")
	}
	var orderID, attemptID, providerAccountID, brokerOrderID string
	var qty int64
	var price, fees int64
	err := tx.QueryRowContext(ctx, `SELECT o.id,a.id,COALESCE(a.provider_account_id,''),
		COALESCE(o.broker_order_id,''),f.qty,f.price_micros,f.fees_micros
		FROM fills f JOIN orders o ON o.id=f.order_id
		JOIN execution_attempt a ON a.id=o.execution_attempt_id
		WHERE f.broker_fill_id=$1`, observed.FillID).Scan(&orderID, &attemptID, &providerAccountID,
		&brokerOrderID, &qty, &price, &fees)
	if err == sql.ErrNoRows {
		return brokerOrigin{origin: "external", evidence: "unmatched"}, nil
	}
	if err != nil {
		return brokerOrigin{}, err
	}
	if providerAccountID != accountID || brokerOrderID != observed.BrokerOrderID ||
		units.Qty(qty) != observed.Qty || units.Micros(price) != observed.Price || units.Micros(fees) != observed.Fees {
		return brokerOrigin{origin: "ambiguous", evidence: "identity_conflict"}, nil
	}
	return brokerOrigin{origin: "alpheus", evidence: "exact_broker_fill_id", matchedOrderID: orderID, matchedAttemptID: attemptID}, nil
}

func (s *Store) LoadBrokerAccountView(accountID string) (*BrokerAccountView, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, fmt.Errorf("broker account id is required")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	var observationID string
	err := s.DB.QueryRowContext(ctx, `SELECT observation_id FROM broker_observation_head
		WHERE account_id=$1`, accountID).Scan(&observationID)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	return loadBrokerObservation(ctx, s.DB, observationID)
}

func (s *Store) LoadBrokerObservation(id string) (*BrokerAccountView, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("broker observation id is required")
	}
	ctx, cancel := s.deadline()
	defer cancel()
	return loadBrokerObservation(ctx, s.DB, id)
}

func loadBrokerObservation(ctx context.Context, db *sql.DB, id string) (*BrokerAccountView, error) {
	var view BrokerAccountView
	var digest []byte
	err := db.QueryRowContext(ctx, `SELECT id,generation,account_id,source,purpose,
		started_at,completed_at,status,manifest_digest FROM broker_observation WHERE id=$1`, id).
		Scan(&view.Observation.ID, &view.Observation.Generation, &view.Observation.AccountID,
			&view.Observation.Source, &view.Observation.Purpose, &view.Observation.StartedAt,
			&view.Observation.CompletedAt, &view.Observation.Status, &digest)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	view.Observation.ManifestDigest = hex.EncodeToString(digest)
	rows, err := db.QueryContext(ctx, `SELECT i.family,i.object_key,i.observed_at,i.object_digest,
		i.canonical,COALESCE(e.origin,''),COALESCE(e.evidence,'')
		FROM broker_observation_item i
		LEFT JOIN broker_object_origin_event e ON e.observation_id=i.observation_id
		 AND e.family=i.family AND e.object_key=i.object_key
		WHERE i.observation_id=$1 ORDER BY i.family,i.object_key`, view.Observation.ID)
	if err != nil {
		return nil, normalizeDBError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var object BrokerObservedObject
		var objectDigest []byte
		if err := rows.Scan(&object.Family, &object.ObjectKey, &object.ObservedAt,
			&objectDigest, &object.Canonical, &object.Origin, &object.Evidence); err != nil {
			return nil, normalizeDBError(err)
		}
		object.ObjectDigest = hex.EncodeToString(objectDigest)
		view.Objects = append(view.Objects, object)
	}
	if err := rows.Err(); err != nil {
		return nil, normalizeDBError(err)
	}
	return &view, nil
}
