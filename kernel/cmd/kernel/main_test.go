package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	orderstate "alpheus/kernel/internal/state"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type journalEntry struct {
	operationID string
	hypothesis  any
	shadow      bool
}

type memoryFill struct {
	orderID string
	ledger  string
	fill    store.FillInput
}

type memoryStore struct {
	mu                  sync.Mutex
	idempotencyMu       sync.Mutex
	symbolMu            sync.Mutex
	ledgerLocks         [2]sync.Mutex
	idempotencyLocks    map[string]*sync.Mutex
	symbolLocks         map[string]*sync.Mutex
	statuses            map[string]string
	classes             map[string]string
	shadows             map[string]bool
	operations          map[string]risk.Operation
	operationRows       map[string]store.OperationRow
	grants              map[string]store.TradeGrant
	reservations        map[string]store.CloseReservation
	openReservations    map[string]store.OpenReservation
	shadowAccount       store.ShadowAccount
	shadowPositions     map[string]store.ShadowPosition
	exposureQty         map[string]units.Qty
	firstExposureOp     map[string]string
	attempts            map[string]store.ExecutionAttempt
	orders              map[string]store.Order
	fills               map[string]memoryFill
	verdicts            map[string]json.RawMessage
	journals            []journalEntry
	events              []string
	blackboards         map[string]json.RawMessage
	journalErr          error
	halted              bool
	haltReason          string
	proposalLockErr     error
	m3aActive           bool
	databaseNow         func() time.Time
	dayOpenEquity       map[string]units.Micros
	realizedPnL         map[string]units.Micros
	breakerStates       map[string]store.BreakerState
	breakerOverrides    map[string]bool
	consecutiveLossDays map[string]int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		statuses:         map[string]string{},
		classes:          map[string]string{},
		shadows:          map[string]bool{},
		operations:       map[string]risk.Operation{},
		operationRows:    map[string]store.OperationRow{},
		idempotencyLocks: map[string]*sync.Mutex{},
		symbolLocks:      map[string]*sync.Mutex{},
		grants:           map[string]store.TradeGrant{},
		reservations:     map[string]store.CloseReservation{},
		openReservations: map[string]store.OpenReservation{},
		shadowAccount: store.ShadowAccount{
			Cash: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
		},
		shadowPositions: map[string]store.ShadowPosition{},
		exposureQty:     map[string]units.Qty{},
		firstExposureOp: map[string]string{},
		attempts:        map[string]store.ExecutionAttempt{},
		orders:          map[string]store.Order{},
		fills:           map[string]memoryFill{},
		verdicts:        map[string]json.RawMessage{},
		blackboards:     map[string]json.RawMessage{},
		dayOpenEquity:   map[string]units.Micros{},
		realizedPnL:     map[string]units.Micros{},
		breakerStates: map[string]store.BreakerState{
			"live": {Ledger: "live"}, "shadow": {Ledger: "shadow"},
		},
		breakerOverrides:    map[string]bool{},
		consecutiveLossDays: map[string]int{},
	}
}

func TestProductionQuoteAgeCannotRemainDisabled(t *testing.T) {
	if err := validateProductionQuoteAge("fake", 0); err != nil {
		t.Fatalf("sim should retain the disabled-age fixture: %v", err)
	}
	if err := validateProductionQuoteAge("robinhood", 0); err == nil {
		t.Fatal("Robinhood accepted a disabled quote age")
	}
	if err := validateProductionQuoteAge("robinhood", 1); err != nil {
		t.Fatalf("positive production quote age rejected: %v", err)
	}
}

func (m *memoryStore) CountTradesForDay(shadow bool, _, _ time.Time) (int, error) {
	return m.CountTradesForDayExcluding(shadow, time.Time{}, time.Time{}, "")
}

func (m *memoryStore) CountTradesForDayExcluding(shadow bool, _, _ time.Time, operationID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	ledger := "live"
	if shadow {
		ledger = "shadow"
	}
	for id, grant := range m.grants {
		if id != operationID && grant.Ledger == ledger {
			n++
		}
	}
	return n, nil
}

func (m *memoryStore) WithLedgerLock(shadow bool, _ time.Time, fn func(store.OperationGate) error) error {
	marketDay := time.Time{}
	return m.WithProposalLock(nil, shadow, &marketDay, fn)
}

func (m *memoryStore) WithProposalLock(identity *store.IdempotencyIdentity, shadow bool, marketDay *time.Time, fn func(store.OperationGate) error) error {
	if m.proposalLockErr != nil {
		return m.proposalLockErr
	}
	if identity != nil {
		key := identity.Subject + "\x00" + identity.Key
		m.idempotencyMu.Lock()
		lock := m.idempotencyLocks[key]
		if lock == nil {
			lock = &sync.Mutex{}
			m.idempotencyLocks[key] = lock
		}
		m.idempotencyMu.Unlock()
		lock.Lock()
		defer lock.Unlock()
	}
	if marketDay != nil {
		index := 0
		if shadow {
			index = 1
		}
		m.ledgerLocks[index].Lock()
		defer m.ledgerLocks[index].Unlock()
	}
	gate := &memoryGate{memoryStore: m}
	defer gate.release()
	return fn(gate)
}

func (m *memoryStore) WithReviewLock(id string, fn func(store.OperationGate, *store.OperationRow) error) error {
	m.idempotencyMu.Lock()
	key := "review\x00" + id
	lock := m.idempotencyLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		m.idempotencyLocks[key] = lock
	}
	m.idempotencyMu.Unlock()
	lock.Lock()
	defer lock.Unlock()
	row, err := m.GetOperation(id)
	if err != nil || row.Status != "pending_review" {
		return store.ErrOperationNotPending
	}
	gate := &memoryGate{memoryStore: m}
	defer gate.release()
	return fn(gate, row)
}

type memoryGate struct {
	*memoryStore
	held []*sync.Mutex
}

func (g *memoryGate) LockLedger(shadow bool) error {
	index := 0
	if shadow {
		index = 1
	}
	g.ledgerLocks[index].Lock()
	g.held = append(g.held, &g.ledgerLocks[index])
	return nil
}

func (g *memoryGate) LockLedgerSymbol(ledger, symbol string) error {
	key := ledger + "\x00" + symbol
	g.symbolMu.Lock()
	lock := g.symbolLocks[key]
	if lock == nil {
		lock = &sync.Mutex{}
		g.symbolLocks[key] = lock
	}
	g.symbolMu.Unlock()
	lock.Lock()
	g.held = append(g.held, lock)
	return nil
}

func (g *memoryGate) release() {
	for i := len(g.held) - 1; i >= 0; i-- {
		g.held[i].Unlock()
	}
}

func (m *memoryStore) Event(kind string, _ any) {
	_ = m.InsertEvent(kind, nil)
}

func (m *memoryStore) InsertEvent(kind string, payload any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, kind)
	if kind == globalHaltEvent {
		if fields, ok := payload.(map[string]any); ok {
			m.halted, _ = fields["halted"].(bool)
			m.haltReason, _ = fields["reason"].(string)
		}
	}
	return nil
}

func (m *memoryStore) InsertEventWithID(kind string, payload any) (int64, error) {
	if err := m.InsertEvent(kind, payload); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.events)), nil
}

func (m *memoryStore) ListControlWarnings(pendingBefore, claimBefore time.Time, limit int) ([]store.ControlWarning, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	warnings := make([]store.ControlWarning, 0)
	for _, attempt := range m.attempts {
		stale := attempt.State == "unknown" ||
			(attempt.State == "pending" && attempt.CreatedAt.Before(pendingBefore)) ||
			(attempt.State == "claimed" && attempt.ClaimedAt.Before(claimBefore))
		if !stale {
			continue
		}
		warning := store.ControlWarning{
			Kind: "execution_attempt", ID: attempt.ID, OperationID: attempt.OperationID,
			State: attempt.State, CreatedAt: attempt.CreatedAt, Detail: attempt.LastError,
		}
		if reservation := m.openReservations[attempt.OpenReservationID]; attempt.OpenReservationID != "" {
			warning.Ledger, warning.Symbol = reservation.Ledger, reservation.Symbol
		} else if reservation := m.reservations[attempt.CloseReservationID]; attempt.CloseReservationID != "" {
			warning.Ledger, warning.Symbol = reservation.Ledger, reservation.Symbol
		}
		warnings = append(warnings, warning)
	}
	for _, reservation := range m.openReservations {
		if reservation.ResourceState == "held" {
			warnings = append(warnings, store.ControlWarning{
				Kind: "open_reservation", ID: reservation.ID, OperationID: reservation.OperationID,
				Ledger: reservation.Ledger, Symbol: reservation.Symbol, State: reservation.ResourceState,
				CreatedAt: reservation.CreatedAt, Detail: "funds and risk remain reserved",
			})
		}
	}
	for _, reservation := range m.reservations {
		if reservation.State == "held" {
			warnings = append(warnings, store.ControlWarning{
				Kind: "close_reservation", ID: reservation.ID, OperationID: reservation.OperationID,
				Ledger: reservation.Ledger, Symbol: reservation.Symbol, State: reservation.State,
				CreatedAt: reservation.CreatedAt, Detail: "position quantity remains reserved",
			})
		}
	}
	sort.Slice(warnings, func(i, j int) bool {
		if warnings[i].CreatedAt.Equal(warnings[j].CreatedAt) {
			return warnings[i].ID < warnings[j].ID
		}
		return warnings[i].CreatedAt.Before(warnings[j].CreatedAt)
	})
	if len(warnings) > limit {
		warnings = warnings[:limit]
	}
	return warnings, nil
}

func (m *memoryStore) InsertOperation(id, proposer, class, status string, payload, verdict any, identity *store.IdempotencyIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[id] = status
	m.classes[id] = class
	if op, ok := payload.(risk.Operation); ok {
		m.shadows[id] = op.Shadow
		m.operations[id] = op
	}
	payloadJSON, _ := json.Marshal(payload)
	verdictJSON, _ := json.Marshal(verdict)
	row := store.OperationRow{
		ID: id, TS: time.Now().UTC(), Proposer: proposer, Class: class,
		Status: status, Payload: payloadJSON, Verdict: verdictJSON,
	}
	if identity != nil {
		row.AuthenticatedSubject = identity.Subject
		row.IdempotencyKey = identity.Key
		row.RequestHash = append([]byte(nil), identity.RequestHash[:]...)
	}
	m.operationRows[id] = row
	return nil
}

func (m *memoryStore) FindOperationByIdempotency(subject, key string) (*store.OperationRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, row := range m.operationRows {
		if row.AuthenticatedSubject == subject && row.IdempotencyKey == key {
			copy := row
			copy.RequestHash = append([]byte(nil), row.RequestHash...)
			return &copy, nil
		}
	}
	return nil, nil
}

func (m *memoryStore) HeldCloseQuantity(ledger, symbol string) (units.Qty, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var quantity units.Qty
	for _, reservation := range m.reservations {
		if reservation.Ledger == ledger && reservation.Symbol == symbol && reservation.State == "held" {
			quantity += reservation.RemainingQty
		}
	}
	return quantity, nil
}

func (m *memoryStore) InsertTradeGrant(grant store.TradeGrant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.grants[grant.OperationID] = grant
	return nil
}

func (m *memoryStore) InsertCloseReservation(reservation store.CloseReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reservations[reservation.ID] = reservation
	return nil
}

func (m *memoryStore) InsertOpenReservation(reservation store.OpenReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.openReservations[reservation.ID] = reservation
	return nil
}

func (m *memoryStore) LedgerResources(ledger, excludeOperationID string) (store.LedgerResources, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var resources store.LedgerResources
	for _, reservation := range m.openReservations {
		if reservation.Ledger == ledger && reservation.OperationID != excludeOperationID && reservation.ResourceState == "held" {
			resources.OpenRisk += reservation.RemainingRisk
			resources.HeldCash += reservation.RemainingCash
		}
	}
	return resources, nil
}

func (m *memoryStore) InsertDayOpen(_ time.Time, ledger string, equity units.Micros) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.dayOpenEquity[ledger]; !exists {
		m.dayOpenEquity[ledger] = equity
	}
	return nil
}

func (m *memoryStore) EvaluateDayRisk(input store.DayRiskInput) (store.DayRiskStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	local := m.realizedPnL[input.Ledger]
	effective := local
	if input.ProviderRealizedPnL != nil && *input.ProviderRealizedPnL < effective {
		effective = *input.ProviderRealizedPnL
	}
	stats := store.DayRiskStats{
		LocalRealizedPnL: local, EffectiveRealizedPnL: effective,
		ConsecutiveLossDays: m.consecutiveLossDays[input.Ledger],
	}
	if input.ProviderRealizedPnL != nil {
		copy := *input.ProviderRealizedPnL
		stats.ProviderRealizedPnL = &copy
	}
	if input.MaxDailyLossPct > 0 && m.dayOpenEquity[input.Ledger] > 0 {
		limit, err := units.PercentFloor(m.dayOpenEquity[input.Ledger], input.MaxDailyLossPct)
		if err != nil {
			return stats, err
		}
		stats.DailyLossLimit = limit
	}
	reason := ""
	day := input.MarketDay.Format(time.DateOnly)
	overridden := func(candidate string) bool {
		return m.breakerOverrides[input.Ledger+"\x00"+candidate+"\x00"+day]
	}
	if input.ProviderRealizedPnL != nil &&
		units.DifferenceExceeds(local, *input.ProviderRealizedPnL, input.PnLReconciliationLimit) &&
		!overridden("pnl_divergence") {
		reason = "pnl_divergence"
	} else if stats.DailyLossLimit > 0 && effective <= -stats.DailyLossLimit && !overridden("daily_loss") {
		reason = "daily_loss"
	} else if input.ConsecutiveLossDaysHalt > 0 && stats.ConsecutiveLossDays >= input.ConsecutiveLossDaysHalt && !overridden("loss_streak") {
		reason = "loss_streak"
	}
	state := m.breakerStates[input.Ledger]
	if state.Halted && !overridden(state.Reason) {
		switch state.Reason {
		case "pnl_divergence":
			reason = state.Reason
		case "daily_loss":
			if state.UpdatedAt.Format(time.DateOnly) == day && reason != "pnl_divergence" {
				reason = state.Reason
			}
		}
	}
	nextHalted := reason != ""
	if state.Halted != nextHalted || state.Reason != reason {
		m.events = append(m.events, "breaker")
	}
	state.Halted, state.Reason, state.UpdatedAt = nextHalted, reason, input.MarketDay
	m.breakerStates[input.Ledger] = state
	stats.Halted, stats.Reason = state.Halted, state.Reason
	return stats, nil
}

func (m *memoryStore) ResumeBreaker(ledger, reason string, marketDay time.Time, _ string) (store.BreakerState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.breakerStates[ledger]
	if !state.Halted || state.Reason != reason {
		return state, store.ErrBreakerNotActive
	}
	m.breakerOverrides[ledger+"\x00"+reason+"\x00"+marketDay.Format(time.DateOnly)] = true
	state.Halted, state.Reason = false, ""
	state.UpdatedAt = marketDay
	m.events = append(m.events, "breaker")
	state.EventID = int64(len(m.events))
	m.breakerStates[ledger] = state
	return state, nil
}

func (m *memoryStore) DatabaseNow() (time.Time, error) {
	if m.databaseNow != nil {
		return m.databaseNow(), nil
	}
	return time.Now().UTC(), nil
}

func (m *memoryStore) ShadowAccount() (store.ShadowAccount, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shadowAccount, nil
}

func (m *memoryStore) ShadowPositions() ([]store.ShadowPosition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	positions := make([]store.ShadowPosition, 0, len(m.shadowPositions))
	for _, position := range m.shadowPositions {
		positions = append(positions, position)
	}
	sort.Slice(positions, func(i, j int) bool { return positions[i].Symbol < positions[j].Symbol })
	return positions, nil
}

func memoryExposureKey(ledger, symbol, kind string) string {
	return ledger + "\x00" + symbol + "\x00" + kind
}

func (m *memoryStore) OpenExposureQuantity(ledger, symbol, kind string) (units.Qty, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	quantity, exists := m.exposureQty[memoryExposureKey(ledger, symbol, kind)]
	if exists || m.m3aActive || ledger == "shadow" {
		return quantity, nil
	}
	// Legacy handler fixtures seed broker positions directly, before M3A's
	// exposure ledger exists in the in-memory double.
	return units.Qty(1<<63 - 1), nil
}

func (m *memoryStore) FirstOpenExposureOperation(ledger, symbol, kind string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstExposureOp[memoryExposureKey(ledger, symbol, kind)], nil
}

func (m *memoryStore) InsertExecutionAttempt(attempt store.ExecutionAttempt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[attempt.ID] = attempt
	return nil
}

func (m *memoryStore) InsertOrder(order store.Order) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orders[order.ExecutionAttemptID] = order
	return nil
}

func (m *memoryStore) GetExecutionAttempt(id string) (*store.ExecutionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt, ok := m.attempts[id]
	if !ok {
		return nil, errors.New("not found")
	}
	copy := attempt
	return &copy, nil
}

func (m *memoryStore) UpdatePendingAttemptLimit(id string, limit units.Micros) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt, ok := m.attempts[id]
	if !ok || attempt.State != "pending" || attempt.Intent != "place" {
		return false, nil
	}
	if limit < attempt.Limit {
		attempt.Limit = limit
		m.attempts[id] = attempt
		order := m.orders[id]
		order.Limit = limit
		m.orders[id] = order
	}
	return true, nil
}

func (m *memoryStore) ClaimPendingAttempt(id, instance string) (*store.ExecutionAttempt, error) {
	return m.claimMemoryAttempt(id, instance, "pending", 0, time.Time{})
}

func (m *memoryStore) ClaimRecoverableAttempt(id, instance, expectedState string, expectedToken int, claimBefore time.Time) (*store.ExecutionAttempt, error) {
	return m.claimMemoryAttempt(id, instance, expectedState, expectedToken, claimBefore)
}

func (m *memoryStore) claimMemoryAttempt(id, instance, expectedState string, expectedToken int, claimBefore time.Time) (*store.ExecutionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt, ok := m.attempts[id]
	if !ok || attempt.State != expectedState {
		return nil, nil
	}
	if expectedState != "pending" && attempt.Attempt != expectedToken {
		return nil, nil
	}
	if expectedState == "claimed" && !attempt.ClaimedAt.Before(claimBefore) {
		return nil, nil
	}
	attempt.State = "claimed"
	attempt.Attempt++
	attempt.ClaimedBy = instance
	attempt.ClaimedAt = time.Now().UTC()
	m.attempts[id] = attempt
	copy := attempt
	return &copy, nil
}

func (m *memoryStore) ListRecoverableAttempts(pendingBefore, claimBefore time.Time, limit int) ([]store.ExecutionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.ExecutionAttempt, 0, limit)
	for _, attempt := range m.attempts {
		unknownReference := attempt.ClaimedAt
		if unknownReference.IsZero() {
			unknownReference = attempt.CreatedAt
		}
		if (attempt.State == "pending" && attempt.CreatedAt.Before(pendingBefore)) ||
			(attempt.State == "claimed" && attempt.ClaimedAt.Before(claimBefore)) ||
			(attempt.State == "unknown" && unknownReference.Before(pendingBefore)) {
			result = append(result, attempt)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (m *memoryStore) ResolveAttempt(id string, fencingToken int, resolution store.AttemptResolution) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt, ok := m.attempts[id]
	if !ok || attempt.State != "claimed" || attempt.Attempt != fencingToken {
		return false, nil
	}
	if resolution.OrderUpdate != nil {
		if err := m.applyMemoryOrderUpdateLocked(*resolution.OrderUpdate, false, true); err != nil {
			return false, err
		}
	}
	attempt.State = resolution.State
	attempt.BrokerOrderID = resolution.BrokerOrderID
	attempt.LastError = resolution.LastError
	m.attempts[id] = attempt
	if resolution.OperationStatus != "" {
		m.statuses[attempt.OperationID] = resolution.OperationStatus
		row := m.operationRows[attempt.OperationID]
		row.Status = resolution.OperationStatus
		m.operationRows[attempt.OperationID] = row
	}
	if resolution.ReleaseReservation && attempt.CloseReservationID != "" {
		reservation := m.reservations[attempt.CloseReservationID]
		reservation.State = "released"
		reservation.RemainingQty = 0
		reservation.ReleasedAt = time.Now().UTC()
		m.reservations[attempt.CloseReservationID] = reservation
	}
	if resolution.ReleaseReservation && attempt.OpenReservationID != "" {
		reservation := m.openReservations[attempt.OpenReservationID]
		reservation.ResourceState = "released"
		reservation.RemainingRisk = 0
		reservation.RemainingCash = 0
		reservation.SettledAt = time.Now().UTC()
		m.openReservations[attempt.OpenReservationID] = reservation
	}
	if resolution.OrderEvent != nil {
		m.events = append(m.events, "order_update")
	}
	return true, nil
}

func (m *memoryStore) ListWorkingOrders(limit int) ([]store.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]store.Order, 0, limit)
	for _, order := range m.orders {
		unresolvedCancel := false
		for _, attempt := range m.attempts {
			if attempt.OperationID == order.OperationID && attempt.Intent == "cancel" &&
				attempt.TargetBrokerOrderID == order.BrokerOrderID &&
				(attempt.State == "pending" || attempt.State == "claimed" || attempt.State == "unknown") {
				unresolvedCancel = true
				break
			}
		}
		if !unresolvedCancel && order.BrokerOrderID != "" &&
			(order.State == "submitted" || order.State == "partially_filled") {
			result = append(result, order)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

func (m *memoryStore) GetOrderByBrokerID(brokerOrderID string) (*store.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, order := range m.orders {
		if order.BrokerOrderID == brokerOrderID {
			copy := order
			return &copy, nil
		}
	}
	return nil, errors.New("not found")
}

func (m *memoryStore) GetOrderByAttempt(attemptID string) (*store.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	order, ok := m.orders[attemptID]
	if !ok {
		return nil, errors.New("not found")
	}
	copy := order
	return &copy, nil
}

func (m *memoryStore) StageRepriceCancel(orderID string) (*store.ExecutionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var source store.Order
	found := false
	for _, order := range m.orders {
		if order.ID == orderID {
			source, found = order, true
			break
		}
	}
	if !found || source.BrokerOrderID == "" ||
		(source.State != "submitted" && source.State != "partially_filled") {
		return nil, nil
	}
	seq := 1
	for _, attempt := range m.attempts {
		if attempt.OperationID != source.OperationID {
			continue
		}
		if attempt.Seq >= seq {
			seq = attempt.Seq + 1
		}
		if attempt.Intent == "cancel" && attempt.TargetBrokerOrderID == source.BrokerOrderID &&
			(attempt.State == "pending" || attempt.State == "claimed" || attempt.State == "unknown") {
			return nil, nil
		}
	}
	attempt := store.ExecutionAttempt{
		ID: store.NewID(), OperationID: source.OperationID, Seq: seq,
		Intent: "cancel", TargetBrokerOrderID: source.BrokerOrderID,
		State: "pending", CreatedAt: time.Now().UTC(),
	}
	m.attempts[attempt.ID] = attempt
	m.events = append(m.events, "reprice_cancel_staged")
	copy := attempt
	return &copy, nil
}

func (m *memoryStore) FinalizeRepriceCancel(cancelAttemptID string, fencingToken int, update store.OrderUpdate, replacement *store.RepriceReplacement, policyReason string) (*store.ExecutionAttempt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cancelAttempt, ok := m.attempts[cancelAttemptID]
	if !ok || cancelAttempt.Intent != "cancel" || cancelAttempt.State != "claimed" ||
		cancelAttempt.Attempt != fencingToken {
		return nil, nil
	}
	var source store.Order
	found := false
	for _, order := range m.orders {
		if order.BrokerOrderID == cancelAttempt.TargetBrokerOrderID {
			source, found = order, true
			break
		}
	}
	if !found {
		return nil, errors.New("reprice source order not found")
	}
	if !isTerminalOrderState(update.State) {
		return nil, errors.New("reprice cancel lacks terminal proof")
	}
	update.ExecutionAttemptID = source.ExecutionAttemptID
	update.BrokerOrderID = source.BrokerOrderID
	if err := m.applyMemoryOrderUpdateLocked(update, true, false); err != nil {
		return nil, err
	}
	placeAttempt := m.attempts[source.ExecutionAttemptID]
	placeAttempt.State = "settled"
	m.attempts[placeAttempt.ID] = placeAttempt
	cancelAttempt.State = "settled"
	cancelAttempt.BrokerOrderID = source.BrokerOrderID
	m.attempts[cancelAttempt.ID] = cancelAttempt
	m.events = append(m.events, "execution_attempt_resolved")

	durableFilled := units.Qty(0)
	for _, fill := range m.fills {
		if fill.orderID == source.ID {
			durableFilled += fill.fill.Qty
		}
	}
	remaining := source.Qty - durableFilled
	if remaining < 0 {
		return nil, store.ErrFillIntegrity
	}
	if update.State == "filled" || remaining == 0 {
		m.setMemoryOperationStatusLocked(source.OperationID, "executed")
		return nil, nil
	}
	if replacement == nil {
		if policyReason == "" {
			return nil, errors.New("terminal reprice cancel lacks policy reason")
		}
		m.releaseMemoryRepriceReservationLocked(placeAttempt)
		status := "failed"
		for _, fill := range m.fills {
			order := m.orderByIDLocked(fill.orderID)
			if order != nil && order.OperationID == source.OperationID {
				status = "executed"
				break
			}
		}
		m.setMemoryOperationStatusLocked(source.OperationID, status)
		m.events = append(m.events, "order_expired_policy")
		return nil, nil
	}
	if replacement.AttemptID == "" || replacement.OrderID == "" ||
		replacement.ClientOrderID == "" || replacement.Limit <= 0 {
		return nil, errors.New("replacement identity or limit is incomplete")
	}
	op := m.operations[source.OperationID]
	if (op.Action == "open" && (op.Side != "buy" || replacement.Limit > op.ApprovedPriceCap)) ||
		(op.Action == "close" && (op.Limit == nil ||
			(op.Side == "buy" && replacement.Limit > *op.Limit) ||
			(op.Side == "sell" && replacement.Limit < *op.Limit))) {
		return nil, errors.New("replacement exceeds persisted price bound")
	}
	if m.heldMemoryRepriceQuantityLocked(placeAttempt) != remaining {
		return nil, errors.New("replacement quantity differs from held reservation")
	}
	freshSource := m.orders[source.ExecutionAttemptID]
	freshSource.Reprices++
	m.orders[source.ExecutionAttemptID] = freshSource
	next := store.ExecutionAttempt{
		ID: replacement.AttemptID, OperationID: source.OperationID, Seq: cancelAttempt.Seq + 1,
		CloseReservationID: placeAttempt.CloseReservationID,
		OpenReservationID:  placeAttempt.OpenReservationID,
		Intent:             "place", ClientOrderID: replacement.ClientOrderID,
		State: "pending", Qty: remaining, Limit: replacement.Limit,
		CreatedAt: time.Now().UTC(),
	}
	m.attempts[next.ID] = next
	m.orders[next.ID] = store.Order{
		ID: replacement.OrderID, OperationID: source.OperationID, ExecutionAttemptID: next.ID,
		ClientOrderID: next.ClientOrderID, Ledger: source.Ledger, Symbol: source.Symbol,
		Side: source.Side, Kind: source.Kind, Multiplier: source.Multiplier,
		Qty: remaining, Limit: next.Limit, State: "new", Reprices: freshSource.Reprices,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	m.events = append(m.events, "order_replacement_staged")
	copy := next
	return &copy, nil
}

func (m *memoryStore) orderByIDLocked(id string) *store.Order {
	for _, order := range m.orders {
		if order.ID == id {
			copy := order
			return &copy
		}
	}
	return nil
}

func (m *memoryStore) setMemoryOperationStatusLocked(operationID, status string) {
	m.statuses[operationID] = status
	row := m.operationRows[operationID]
	row.Status = status
	m.operationRows[operationID] = row
}

func (m *memoryStore) releaseMemoryRepriceReservationLocked(attempt store.ExecutionAttempt) {
	if attempt.CloseReservationID != "" {
		reservation := m.reservations[attempt.CloseReservationID]
		if reservation.State == "held" {
			reservation.State, reservation.RemainingQty = "released", 0
			reservation.ReleasedAt = time.Now().UTC()
			m.reservations[attempt.CloseReservationID] = reservation
		}
	}
	if attempt.OpenReservationID != "" {
		reservation := m.openReservations[attempt.OpenReservationID]
		if reservation.ResourceState == "held" {
			reservation.ResourceState = "released"
			reservation.RemainingRisk, reservation.RemainingCash = 0, 0
			reservation.SettledAt = time.Now().UTC()
			m.openReservations[attempt.OpenReservationID] = reservation
		}
	}
}

func (m *memoryStore) heldMemoryRepriceQuantityLocked(attempt store.ExecutionAttempt) units.Qty {
	if attempt.CloseReservationID != "" && attempt.OpenReservationID == "" {
		reservation := m.reservations[attempt.CloseReservationID]
		if reservation.State == "held" {
			return reservation.RemainingQty
		}
	}
	if attempt.OpenReservationID != "" && attempt.CloseReservationID == "" {
		reservation := m.openReservations[attempt.OpenReservationID]
		if reservation.ResourceState == "held" {
			return reservation.RemainingQty
		}
	}
	return 0
}

func (m *memoryStore) ApplyOrderUpdate(update store.OrderUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	order, ok := m.orders[update.ExecutionAttemptID]
	if !ok && update.BrokerOrderID != "" {
		for _, candidate := range m.orders {
			if candidate.BrokerOrderID == update.BrokerOrderID {
				order, ok = candidate, true
				break
			}
		}
	}
	if ok {
		for _, attempt := range m.attempts {
			if attempt.OperationID == order.OperationID && attempt.Intent == "cancel" &&
				attempt.TargetBrokerOrderID == order.BrokerOrderID &&
				(attempt.State == "pending" || attempt.State == "claimed" ||
					attempt.State == "unknown" || attempt.State == "settled") {
				return nil
			}
		}
	}
	return m.applyMemoryOrderUpdateLocked(update, false, true)
}

func (m *memoryStore) ListTerminalReservationCandidates(int) ([]store.TerminalReservationCandidate, error) {
	return nil, nil
}

func (m *memoryStore) ReleaseProvenTerminalReservation(store.TerminalReservationCandidate, units.Qty, bool) (bool, error) {
	return false, nil
}

func (m *memoryStore) applyMemoryOrderUpdateLocked(update store.OrderUpdate, preserveReservation, finalizePlacement bool) error {
	order, ok := m.orders[update.ExecutionAttemptID]
	if !ok {
		return nil // Older recovery fixtures predate M2.9 order creation.
	}
	if order.State != update.State {
		current := order.State
		if current == "new" && update.State != "submitted" && update.State != "rejected" {
			if _, err := orderstate.Advance(current, "submitted"); err != nil {
				m.events = append(m.events, "order_transition_rejected")
				return store.ErrIllegalOrderTransition
			}
			current = "submitted"
		}
		if _, err := orderstate.Advance(current, update.State); err != nil {
			m.events = append(m.events, "order_transition_rejected")
			return store.ErrIllegalOrderTransition
		}
	}
	for _, fill := range update.Fills {
		if existing, exists := m.fills[fill.BrokerFillID]; exists {
			if existing.orderID != order.ID || existing.ledger != order.Ledger ||
				existing.fill.Qty != fill.Qty || existing.fill.Price != fill.Price || existing.fill.Fees != fill.Fees {
				m.events = append(m.events, "fill_integrity_error", globalHaltEvent)
				m.halted, m.haltReason = true, "fill integrity failure"
				return store.ErrFillIntegrity
			}
			continue
		}
		m.fills[fill.BrokerFillID] = memoryFill{orderID: order.ID, ledger: order.Ledger, fill: fill}
		m.events = append(m.events, "fill")
		attempt := m.attempts[order.ExecutionAttemptID]
		if attempt.CloseReservationID != "" {
			reservation := m.reservations[attempt.CloseReservationID]
			if reservation.State != "held" || fill.Qty > reservation.RemainingQty {
				return store.ErrFillIntegrity
			}
			reservation.RemainingQty -= fill.Qty
			if reservation.RemainingQty == 0 {
				reservation.State = "released"
				reservation.ReleasedAt = time.Now().UTC()
			}
			m.reservations[attempt.CloseReservationID] = reservation
		}
		op := m.operations[order.OperationID]
		key := memoryExposureKey(order.Ledger, order.Symbol, order.Kind)
		switch op.Action {
		case "open":
			if attempt.OpenReservationID != "" {
				reservation := m.openReservations[attempt.OpenReservationID]
				if reservation.ResourceState != "held" || fill.Qty > reservation.RemainingQty {
					return store.ErrFillIntegrity
				}
				reservation.RemainingQty -= fill.Qty
				reservation.RemainingRisk = memoryProportionalCeil(
					reservation.OriginalRisk, reservation.RemainingQty, reservation.OriginalQty,
				)
				reservation.RemainingCash = memoryProportionalCeil(
					reservation.OriginalCash, reservation.RemainingQty, reservation.OriginalQty,
				)
				if reservation.RemainingQty == 0 {
					reservation.RemainingRisk, reservation.RemainingCash = 0, 0
					reservation.ResourceState = "converted"
					reservation.SettledAt = time.Now().UTC()
				}
				m.openReservations[attempt.OpenReservationID] = reservation
			}
			m.exposureQty[key] += fill.Qty
			if m.firstExposureOp[key] == "" {
				m.firstExposureOp[key] = order.OperationID
			}
			if order.Ledger == "shadow" {
				cost, err := units.MulQtyPrice(fill.Qty, fill.Price, order.Multiplier, true)
				if err != nil {
					return store.ErrFillIntegrity
				}
				cost, err = units.Add(cost, fill.Fees)
				if err != nil || cost > m.shadowAccount.Cash || cost > m.shadowAccount.BuyingPower {
					return store.ErrFillIntegrity
				}
				m.shadowAccount.Cash -= cost
				m.shadowAccount.BuyingPower -= cost
				position := m.shadowPositions[order.Symbol]
				if position.Symbol != "" && (position.Kind != order.Kind || position.Multiplier != order.Multiplier) {
					return store.ErrFillIntegrity
				}
				position.Symbol, position.Kind, position.Multiplier = order.Symbol, order.Kind, order.Multiplier
				position.Qty += fill.Qty
				m.shadowPositions[order.Symbol] = position
			}
		case "close":
			if m.m3aActive || order.Ledger == "shadow" {
				if m.exposureQty[key] < fill.Qty {
					return store.ErrFillIntegrity
				}
				m.exposureQty[key] -= fill.Qty
			}
			if order.Ledger == "shadow" {
				position := m.shadowPositions[order.Symbol]
				if position.Kind != order.Kind || position.Multiplier != order.Multiplier || position.Qty < fill.Qty {
					return store.ErrFillIntegrity
				}
				position.Qty -= fill.Qty
				if position.Qty == 0 {
					delete(m.shadowPositions, order.Symbol)
				} else {
					m.shadowPositions[order.Symbol] = position
				}
				proceeds, err := units.MulQtyPrice(fill.Qty, fill.Price, order.Multiplier, false)
				if err != nil {
					return store.ErrFillIntegrity
				}
				proceeds, err = units.Add(proceeds, -fill.Fees)
				if err != nil || proceeds < 0 {
					return store.ErrFillIntegrity
				}
				m.shadowAccount.Cash += proceeds
				m.shadowAccount.BuyingPower += proceeds
			}
		}
	}
	attempt := m.attempts[order.ExecutionAttemptID]
	if attempt.CloseReservationID != "" {
		reservation := m.reservations[attempt.CloseReservationID]
		switch update.State {
		case "cancelled", "rejected", "expired":
			if !preserveReservation && reservation.State == "held" {
				reservation.State = "released"
				reservation.RemainingQty = 0
				reservation.ReleasedAt = time.Now().UTC()
				m.reservations[attempt.CloseReservationID] = reservation
			}
		case "filled":
			if reservation.State != "released" || reservation.RemainingQty != 0 {
				return store.ErrFillIntegrity
			}
		}
	}
	if attempt.OpenReservationID != "" {
		reservation := m.openReservations[attempt.OpenReservationID]
		switch update.State {
		case "cancelled", "rejected", "expired":
			if !preserveReservation && reservation.ResourceState == "held" {
				reservation.ResourceState = "released"
				reservation.RemainingRisk, reservation.RemainingCash = 0, 0
				reservation.SettledAt = time.Now().UTC()
				m.openReservations[attempt.OpenReservationID] = reservation
			}
		case "filled":
			if reservation.ResourceState != "converted" {
				return store.ErrFillIntegrity
			}
		}
	}
	order.BrokerOrderID = update.BrokerOrderID
	order.State = update.State
	order.UpdatedAt = time.Now().UTC()
	m.orders[order.ExecutionAttemptID] = order
	m.events = append(m.events, "order_update")
	if attemptState, operationStatus := memoryTerminalPlacementState(update.State); finalizePlacement && attemptState != "" {
		attempt := m.attempts[order.ExecutionAttemptID]
		if attempt.State == "placed" {
			attempt.State = attemptState
			m.attempts[attempt.ID] = attempt
		}
		m.statuses[order.OperationID] = operationStatus
	}
	return nil
}

func memoryProportionalCeil(original units.Micros, remaining, total units.Qty) units.Micros {
	if remaining == 0 {
		return 0
	}
	numerator := int64(original) * int64(remaining)
	denominator := int64(total)
	return units.Micros((numerator + denominator - 1) / denominator)
}

func memoryTerminalPlacementState(orderState string) (string, string) {
	switch orderState {
	case "filled":
		return "settled", "executed"
	case "rejected":
		return "failed", "failed"
	case "cancelled", "expired":
		return "settled", "failed"
	default:
		return "", ""
	}
}

func (m *memoryStore) FailPendingAttempt(id, reason string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt, ok := m.attempts[id]
	if !ok || attempt.State != "pending" {
		return false, nil
	}
	attempt.State = "failed"
	attempt.LastError = reason
	m.attempts[id] = attempt
	m.statuses[attempt.OperationID] = "failed"
	if attempt.CloseReservationID != "" {
		reservation := m.reservations[attempt.CloseReservationID]
		reservation.State = "released"
		reservation.RemainingQty = 0
		m.reservations[attempt.CloseReservationID] = reservation
	}
	if attempt.OpenReservationID != "" {
		reservation := m.openReservations[attempt.OpenReservationID]
		reservation.ResourceState = "released"
		reservation.RemainingRisk, reservation.RemainingCash = 0, 0
		reservation.SettledAt = time.Now().UTC()
		m.openReservations[attempt.OpenReservationID] = reservation
	}
	if strings.HasPrefix(reason, "order_expired_policy:") {
		m.events = append(m.events, "order_expired_policy")
	}
	return true, nil
}

func (m *memoryStore) GetCloseReservation(id string) (*store.CloseReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reservation, ok := m.reservations[id]
	if !ok {
		return nil, errors.New("not found")
	}
	copy := reservation
	return &copy, nil
}

func (m *memoryStore) GetOpenReservation(id string) (*store.OpenReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reservation, ok := m.openReservations[id]
	if !ok {
		return nil, errors.New("not found")
	}
	copy := reservation
	return &copy, nil
}

func (m *memoryStore) HasTradeGrant(operationID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.grants[operationID]
	return ok, nil
}

func (m *memoryStore) FeatureActive(name string) (bool, error) {
	return name == "m3a" && m.m3aActive, nil
}

func (m *memoryStore) SetOperationStatus(id, status string, verdict any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[id] = status
	if verdict != nil {
		encoded, err := json.Marshal(verdict)
		if err != nil {
			return err
		}
		m.verdicts[id] = encoded
	}
	row := m.operationRows[id]
	row.Status = status
	if verdict != nil {
		row.Verdict = m.verdicts[id]
	}
	m.operationRows[id] = row
	return nil
}

func (m *memoryStore) GetOperation(id string) (*store.OperationRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status, ok := m.statuses[id]
	if !ok {
		return nil, errors.New("not found")
	}
	row := m.operationRows[id]
	row.ID, row.Class, row.Status, row.Verdict = id, m.classes[id], status, m.verdicts[id]
	return &row, nil
}

func (m *memoryStore) ListOperations(status string, limit int, cursor *store.OperationCursor) ([]store.OperationRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]store.OperationRow, 0, len(m.operationRows))
	for _, row := range m.operationRows {
		if status != "" && row.Status != status {
			continue
		}
		if cursor != nil && !(row.TS.Before(cursor.TS) || (row.TS.Equal(cursor.TS) && row.ID < cursor.ID)) {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TS.Equal(rows[j].TS) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].TS.After(rows[j].TS)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (m *memoryStore) InsertJournal(operationID string, hypothesis, _, _ any, shadow bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.journalErr != nil {
		return m.journalErr
	}
	m.journals = append(m.journals, journalEntry{operationID: operationID, hypothesis: hypothesis, shadow: shadow})
	return nil
}

func (m *memoryStore) TopLessons(int) ([]store.Lesson, error) { return []store.Lesson{}, nil }

func (m *memoryStore) GetBlackboard(day string) (json.RawMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if doc, ok := m.blackboards[day]; ok {
		return doc, nil
	}
	return json.RawMessage(`{}`), nil
}

func (m *memoryStore) PutBlackboard(day string, doc json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blackboards[day] = append(json.RawMessage(nil), doc...)
	return nil
}

func (m *memoryStore) LoadGlobalHalt() (bool, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.halted, m.haltReason, nil
}

func postOperation(t *testing.T, s *server, payload string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/operations", bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.propose(w, req)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return w, body
}

func newFake(cash string) *broker.Fake {
	return broker.NewFake(units.MustMicros(cash))
}

func setQuote(b *broker.Fake, symbol, bid, ask string, openInterest int) {
	if err := b.SetQuote(broker.Quote{
		Symbol: symbol, Bid: units.MustMicros(bid), Ask: units.MustMicros(ask),
		OpenInterest: openInterest,
	}); err != nil {
		panic(fmt.Sprintf("set quote %s: %v", symbol, err))
	}
}

func placeOrder(b *broker.Fake, symbol, side, qty, limit, kind string) (broker.OrderResult, error) {
	return b.PlaceLimitOrder(context.Background(), broker.PlaceRequest{
		Symbol: symbol, Side: side, Qty: units.MustQty(qty), Limit: units.MustMicros(limit), Kind: kind,
	})
}

func dualLedgerLimits() config.Limits {
	limits := config.Limits{}
	limits.HardLimits.MaxRiskPerTradePct = units.MustPercent("35")
	limits.HardLimits.MaxTotalOpenRiskPct = units.MustPercent("80")
	limits.HardLimits.MaxNewTradesPerDay = 6
	limits.HardLimits.MaxDailyLossPct = units.MustPercent("40")
	limits.HardLimits.ConsecutiveLossDaysHalt = 5
	limits.InstrumentRules.MinOpenInterest = 300
	limits.InstrumentRules.MaxRelativeSpread = units.MustRatio("0.15")
	limits.RiskDeclarationTolerance = units.MustMicros("0.01")
	limits.PnLReconciliationTolerance = units.MustMicros("0.01")
	limits.ProposalTTLSec = 1800
	limits.ExecutionPolicy.StartAt = "mid"
	limits.PlanRequirements = []string{"stop", "invalidation", "time_stop", "target"}
	return limits
}

func TestProposeUsesIndependentLiveAndShadowLedgers(t *testing.T) {
	st := newMemoryStore()
	b := newFake("300")
	setQuote(b, "SPY", "0.34", "0.35", 45_000)
	s := &server{limits: dualLedgerLimits(), broker: b, store: st}
	shadowPayload := `{"proposer":"test","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"shadow":true,"plan":{"stop":"-30%","invalidation":"x","time_stop":"15:45","target":"+50%"}}`

	for i := 0; i < 6; i++ {
		w, body := postOperation(t, s, shadowPayload)
		if w.Code != http.StatusOK || body["class"] != "B" || body["status"] != "executed" {
			t.Fatalf("shadow %d: status=%d body=%v, want B/executed", i+1, w.Code, body)
		}
	}

	livePayload := `{"proposer":"test","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"-30%","invalidation":"x","time_stop":"15:45","target":"+50%"}}`
	w, body := postOperation(t, s, livePayload)
	if w.Code != http.StatusOK || body["class"] != "B" {
		t.Fatalf("live after six shadow: status=%d body=%v, want B", w.Code, body)
	}

	w, body = postOperation(t, s, shadowPayload)
	if w.Code != http.StatusOK || body["class"] != "C" || body["status"] != "pending_review" {
		t.Fatalf("seventh shadow: status=%d body=%v, want C/pending_review", w.Code, body)
	}
	checks, ok := body["checks"].(map[string]any)
	if !ok || checks["daily_trade_count"] != false {
		t.Fatalf("seventh shadow checks=%v, want daily_trade_count=false", body["checks"])
	}

	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	w = httptest.NewRecorder()
	s.getState(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("state status=%d body=%s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	day, ok := body["day"].(map[string]any)
	if !ok {
		t.Fatalf("day=%v, want object", body["day"])
	}
	live, liveOK := day["live"].(map[string]any)
	shadow, shadowOK := day["shadow"].(map[string]any)
	if !liveOK || !shadowOK || live["trades_today"] != float64(1) || shadow["trades_today"] != float64(6) {
		t.Fatalf("day=%v, want live=1 shadow=6", day)
	}
}

func TestConcurrentOpensCannotExceedEitherLedgerCap(t *testing.T) {
	for _, shadow := range []bool{false, true} {
		name := "live"
		payload := `{"proposer":"barrier","action":"open","kind":"equity","underlying":"I4","symbol":"I4","side":"buy","qty":0.01,"max_risk_usd":1.001,"plan":{"stop":"90","invalidation":"x","time_stop":"15:45","target":"120"}}`
		if shadow {
			name = "shadow"
			payload = `{"proposer":"barrier","action":"open","kind":"equity","underlying":"I4","symbol":"I4","side":"buy","qty":0.01,"max_risk_usd":1.001,"shadow":true,"plan":{"stop":"90","invalidation":"x","time_stop":"15:45","target":"120"}}`
		}
		t.Run(name, func(t *testing.T) {
			st := newMemoryStore()
			b := newFake("300")
			setQuote(b, "I4", "100", "100.1", 1_000)
			s := &server{limits: dualLedgerLimits(), broker: b, store: st}
			for i := 0; i < 5; i++ {
				w, body := postOperation(t, s, payload)
				if w.Code != http.StatusOK || body["class"] != "B" {
					t.Fatalf("seed %d: status=%d body=%v", i+1, w.Code, body)
				}
			}

			type result struct {
				code  int
				class string
			}
			const requests = 20
			start := make(chan struct{})
			ready := sync.WaitGroup{}
			ready.Add(requests)
			results := make(chan result, requests)
			for i := 0; i < requests; i++ {
				go func() {
					ready.Done()
					<-start
					w, body := postOperation(t, s, payload)
					class, _ := body["class"].(string)
					results <- result{code: w.Code, class: class}
				}()
			}
			ready.Wait()
			close(start)

			classes := map[string]int{}
			for i := 0; i < requests; i++ {
				res := <-results
				if res.code != http.StatusOK {
					t.Fatalf("request status=%d class=%q", res.code, res.class)
				}
				classes[res.class]++
			}
			if classes["B"] != 1 || classes["C"] != requests-1 {
				t.Fatalf("classes=%v, want B=1 C=%d", classes, requests-1)
			}
			count, err := st.CountTradesForDay(shadow, time.Time{}, time.Time{})
			if err != nil || count != 6 {
				t.Fatalf("count=%d err=%v, want 6", count, err)
			}
		})
	}
}

func TestMarketDayWindowUsesNewYorkBoundaries(t *testing.T) {
	winter, err := marketDayWindow(time.Date(2026, time.January, 16, 0, 30, 0, 0, time.UTC), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if got := winter.day.Format("2006-01-02"); got != "2026-01-15" {
		t.Fatalf("winter market day=%s, want 2026-01-15", got)
	}
	if !winter.start.Equal(time.Date(2026, time.January, 15, 5, 0, 0, 0, time.UTC)) ||
		!winter.end.Equal(time.Date(2026, time.January, 16, 5, 0, 0, 0, time.UTC)) {
		t.Fatalf("winter window=%s..%s", winter.start, winter.end)
	}

	dstStart, err := marketDayWindow(time.Date(2026, time.March, 8, 16, 0, 0, 0, time.UTC), "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if got := dstStart.end.Sub(dstStart.start); got != 23*time.Hour {
		t.Fatalf("DST-start market day length=%s, want 23h", got)
	}
}

func TestProposeCloseUsesBid(t *testing.T) {
	b := newFake("100000")
	setQuote(b, "SPY", "4.20", "4.40", 10_000)
	if seeded, err := placeOrder(b, "SPY", "buy", "1", "4.40", "option"); err != nil || seeded.State != "filled" {
		t.Fatalf("seed long position: result=%+v err=%v", seeded, err)
	}
	s := &server{limits: config.Limits{}, broker: b, store: newMemoryStore()}

	// No side is required: the kernel derives sell from the signed long position.
	w, body := postOperation(t, s, `{"proposer":"test","action":"close","kind":"option","symbol":"SPY","qty":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if body["class"] != "A" || body["status"] != "executed" {
		t.Fatalf("class/status = %v/%v, want A/executed", body["class"], body["status"])
	}
	order, ok := body["order"].(map[string]any)
	if !ok {
		t.Fatalf("order missing: %v", body)
	}
	if order["state"] != "filled" || order["filled_price"] != 4.20 {
		t.Fatalf("order=%v, want filled at bid 4.20", order)
	}
}

func TestProposeCloseShortUsesAsk(t *testing.T) {
	b := newFake("100000")
	setQuote(b, "SPY", "4.20", "4.40", 10_000)
	if seeded, err := placeOrder(b, "SPY", "sell", "1", "4.20", "option"); err != nil || seeded.State != "filled" {
		t.Fatalf("seed short position: result=%+v err=%v", seeded, err)
	}
	s := &server{limits: config.Limits{}, broker: b, store: newMemoryStore()}

	// Even a legacy/conflicting side cannot control execution; the short
	// position requires a buy at ask.
	w, body := postOperation(t, s, `{"proposer":"test","action":"close","kind":"option","symbol":"SPY","side":"buy","qty":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	order, ok := body["order"].(map[string]any)
	if !ok || order["state"] != "filled" || order["filled_price"] != 4.40 {
		t.Fatalf("order=%v, want filled at ask 4.40", body["order"])
	}
}

func TestProposeCancelRequiresBrokerOrderID(t *testing.T) {
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: newMemoryStore()}
	w, body := postOperation(t, s, `{"proposer":"test","action":"cancel"}`)
	if w.Code != http.StatusBadRequest || body["error"] != "cancel requires broker_order_id" {
		t.Fatalf("status=%d body=%v", w.Code, body)
	}
}

func TestProposeCloseRequiresAndCannotExceedPosition(t *testing.T) {
	b := newFake("10000")
	s := &server{limits: config.Limits{}, broker: b, store: newMemoryStore()}

	w, body := postOperation(t, s, `{"proposer":"test","action":"close","symbol":"SPY","qty":1}`)
	if w.Code != http.StatusBadRequest || body["error"] != "close requires an existing position for SPY" {
		t.Fatalf("missing position: status=%d body=%v", w.Code, body)
	}

	if seeded, err := placeOrder(b, "SPY", "buy", "1", "623.14", "option"); err != nil || seeded.State != "filled" {
		t.Fatalf("seed long position: result=%+v err=%v", seeded, err)
	}
	w, body = postOperation(t, s, `{"proposer":"test","action":"close","kind":"option","symbol":"SPY","qty":2}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("over-close: status=%d body=%v", w.Code, body)
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 1 || positions[0].Qty != units.MustQty("1") {
		t.Fatalf("position changed after rejected close: positions=%v err=%v", positions, err)
	}
}

func TestProposeCloseRejectsMalformedTradingFields(t *testing.T) {
	tests := []string{
		`{"proposer":"test","action":"close","symbol":"SPY","qty":0}`,
		`{"proposer":"test","action":"close","symbol":"SPY","qty":-1}`,
		`{"proposer":"test","action":"close","symbol":"SPY","side":"XXXX","qty":1}`,
		`{"proposer":"test","action":"close","symbol":"SPY","qty":1,"limit":0}`,
	}
	for _, payload := range tests {
		s := &server{limits: config.Limits{}, broker: newFake("300"), store: newMemoryStore()}
		w, body := postOperation(t, s, payload)
		if w.Code != http.StatusBadRequest {
			t.Errorf("payload=%s status=%d body=%v, want 400", payload, w.Code, body)
		}
	}
}

func TestConcurrentCloseCannotOpenReversePosition(t *testing.T) {
	b := newFake("10000")
	if seeded, err := placeOrder(b, "SPY", "buy", "1", "623.14", "option"); err != nil || seeded.State != "filled" {
		t.Fatalf("seed long position: result=%+v err=%v", seeded, err)
	}
	s := &server{limits: config.Limits{}, broker: b, store: newMemoryStore()}

	type result struct {
		code int
		body map[string]any
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, body := postOperation(t, s, `{"proposer":"test","action":"close","symbol":"SPY","qty":1}`)
			results <- result{code: w.Code, body: body}
		}()
	}
	wg.Wait()
	close(results)

	executed, rejected := 0, 0
	for res := range results {
		if res.code == http.StatusOK && res.body["status"] == "executed" {
			executed++
		} else if res.code == http.StatusBadRequest {
			rejected++
		} else {
			t.Fatalf("unexpected concurrent close result: code=%d body=%v", res.code, res.body)
		}
	}
	if executed != 1 || rejected != 1 {
		t.Fatalf("executed/rejected=%d/%d, want 1/1", executed, rejected)
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 0 {
		t.Fatalf("positions=%v err=%v, want flat", positions, err)
	}
}

func TestExecuteRefusesUnverifiedClose(t *testing.T) {
	b := newFake("10000")
	if seeded, err := placeOrder(b, "SPY", "buy", "1", "623.14", "option"); err != nil || seeded.State != "filled" {
		t.Fatalf("seed long position: result=%+v err=%v", seeded, err)
	}
	st := newMemoryStore()
	opID, attemptID := store.NewID(), store.NewID()
	op := risk.Operation{Action: "close", Symbol: "SPY", Kind: "option", Side: "sell", Qty: units.MustQty("1")}
	if err := st.InsertOperation(opID, "test", "A", "auto_approved", op, risk.Verdict{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertExecutionAttempt(store.ExecutionAttempt{
		ID: attemptID, OperationID: opID, Seq: 1, Intent: "place", ClientOrderID: store.NewID(),
		State: "pending", Qty: op.Qty, Limit: units.MustMicros("623.10"),
	}); err != nil {
		t.Fatal(err)
	}
	s := &server{limits: config.Limits{}, broker: b, store: st}
	_, err := s.executePendingAttempt(context.Background(), attemptID)
	if err == nil {
		t.Fatal("unverified direct close execution succeeded")
	}
	positions, getErr := b.Positions(context.Background())
	if getErr != nil || len(positions) != 1 || positions[0].Qty != units.MustQty("1") {
		t.Fatalf("position changed: positions=%v err=%v", positions, getErr)
	}
}

func TestProposeCancelUnknownOrder(t *testing.T) {
	st := newMemoryStore()
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: st}
	w, body := postOperation(t, s, `{"proposer":"test","action":"cancel","broker_order_id":"missing-order"}`)
	if w.Code != http.StatusOK || body["class"] != "A" {
		t.Fatalf("status=%d body=%v", w.Code, body)
	}
	order, ok := body["order"].(map[string]any)
	if !ok || order["state"] != "rejected" {
		t.Fatalf("order=%v, want rejected", body["order"])
	}
	if len(st.events) == 0 || st.events[len(st.events)-1] != "order_update" {
		t.Fatalf("events=%v, want trailing order_update", st.events)
	}
}

func TestProposeTightenStopJournalsWithoutBrokerOrder(t *testing.T) {
	st := newMemoryStore()
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: st}
	w, body := postOperation(t, s, `{"proposer":"test","action":"tighten_stop","kind":"option","symbol":"SPY","plan":{"stop":"4.00"}}`)
	if w.Code != http.StatusOK || body["class"] != "A" || body["stop"] != "4.00" {
		t.Fatalf("status=%d body=%v", w.Code, body)
	}
	if len(st.journals) != 1 {
		t.Fatalf("journals=%d, want 1", len(st.journals))
	}
	hypothesis, ok := st.journals[0].hypothesis.(map[string]any)
	if !ok || hypothesis["stop"] != "4.00" {
		t.Fatalf("journal hypothesis=%v", st.journals[0].hypothesis)
	}
}

func TestProposeTightenStopRejectsWhitespace(t *testing.T) {
	st := newMemoryStore()
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: st}
	w, body := postOperation(t, s, `{"proposer":"test","action":"tighten_stop","symbol":"SPY","plan":{"stop":"   "}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%v, want 400", w.Code, body)
	}
	if len(st.journals) != 0 {
		t.Fatalf("journals=%d, want 0", len(st.journals))
	}
}

func TestProposeOpenInfersNakedShortAndRejectsWhitespacePlan(t *testing.T) {
	limits := config.Limits{}
	limits.HardLimits.MaxRiskPerTradePct = units.MustPercent("35")
	limits.HardLimits.MaxTotalOpenRiskPct = units.MustPercent("80")
	limits.HardLimits.MaxNewTradesPerDay = 6
	limits.InstrumentRules.MinOpenInterest = 300
	limits.InstrumentRules.MaxRelativeSpread = units.MustRatio("0.15")
	limits.RiskDeclarationTolerance = units.MustMicros("0.01")
	limits.ExecutionPolicy.StartAt = "mid"
	limits.PlanRequirements = []string{"stop", "invalidation", "time_stop", "target"}
	b := newFake("300")
	setQuote(b, "SPY", "0.34", "0.35", 45_000)
	s := &server{limits: limits, broker: b, store: newMemoryStore()}

	validPlan := `{"stop":"-30%","invalidation":"x","time_stop":"15:45","target":"+50%"}`
	w, body := postOperation(t, s, `{"proposer":"test","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"sell","qty":1,"limit":0.35,"max_risk_usd":35,"plan":`+validPlan+`}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" || body["status"] != "rejected" {
		t.Fatalf("inferred naked short: status=%d body=%v", w.Code, body)
	}

	blankPlan := `{"stop":" ","invalidation":"x","time_stop":"15:45","target":"+50%"}`
	w, body = postOperation(t, s, `{"proposer":"test","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"plan":`+blankPlan+`}`)
	if w.Code != http.StatusOK || body["class"] != "C" || body["status"] != "pending_review" {
		t.Fatalf("whitespace plan: status=%d body=%v", w.Code, body)
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 0 {
		t.Fatalf("broker changed after rejected/pending opens: positions=%v err=%v", positions, err)
	}
}

func TestProposeRequiresJSONAndRejectsUnknownFields(t *testing.T) {
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: newMemoryStore()}
	req := httptest.NewRequest(http.MethodPost, "/operations", bytes.NewBufferString(`{"action":"cancel","broker_order_id":"x"}`))
	w := httptest.NewRecorder()
	s.propose(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing content type: status=%d body=%s", w.Code, w.Body.String())
	}

	w, body := postOperation(t, s, `{"action":"cancel","broker_order_id":"x","surprise":true}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: status=%d body=%v", w.Code, body)
	}
}

func TestCrossedQuoteFailsClosedAtLiquidityGate(t *testing.T) {
	b := newFake("300")
	s := &server{limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}

	req := httptest.NewRequest(http.MethodPost, "/sim/quote", bytes.NewBufferString(
		`{"symbol":"XSD","bid":100,"ask":50,"open_interest":1000}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.simQuote(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set crossed quote: status=%d body=%s", w.Code, w.Body.String())
	}

	w, body := postOperation(t, s, `{"proposer":"test","action":"open","kind":"equity","underlying":"XSD","symbol":"XSD","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"90","invalidation":"x","time_stop":"15:45","target":"120"}}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" || body["status"] != "rejected" {
		t.Fatalf("crossed quote: status=%d body=%v, want REJECT/rejected", w.Code, body)
	}
	reasons, ok := body["reasons"].([]any)
	if !ok || len(reasons) == 0 || reasons[0] != "market_data_unavailable" {
		t.Fatalf("crossed quote reasons=%v", body["reasons"])
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 0 {
		t.Fatalf("crossed quote reached broker: positions=%v err=%v", positions, err)
	}
}

func TestComputedRiskCannotBeUnderDeclared(t *testing.T) {
	plan := `{"stop":"-30%","invalidation":"x","time_stop":"15:45","target":"+50%"}`
	tests := []struct {
		name        string
		declaration string
		class       string
		status      string
		reason      string
	}{
		{"under-declared", `,"max_risk_usd":10`, "REJECT", "rejected", "risk_declaration_mismatch"},
		{"truthful", `,"max_risk_usd":300`, "C", "pending_review", ""},
		{"explicit zero", `,"max_risk_usd":0`, "REJECT", "rejected", "risk_declaration_mismatch"},
		{"omitted", "", "C", "pending_review", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := newFake("300")
			setQuote(b, "SPY", "2.99", "3.00", 45_000)
			limits := dualLedgerLimits()
			st := newMemoryStore()
			s := &server{limits: limits, broker: b, store: st}
			payload := `{"proposer":"m25","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1` +
				tc.declaration + `,"plan":` + plan + `}`
			w, body := postOperation(t, s, payload)
			if w.Code != http.StatusOK || body["class"] != tc.class || body["status"] != tc.status {
				t.Fatalf("status=%d body=%v", w.Code, body)
			}
			if body["derived_max_risk"] != 300.0 || body["required_cash"] != 300.0 {
				t.Fatalf("risk facts=%v/%v, want 300", body["derived_max_risk"], body["required_cash"])
			}
			if tc.reason != "" {
				reasons, ok := body["reasons"].([]any)
				if !ok || len(reasons) == 0 || reasons[0] != tc.reason {
					t.Fatalf("reasons=%v, want %s", body["reasons"], tc.reason)
				}
			}
			positions, err := b.Positions(context.Background())
			if err != nil || len(positions) != 0 {
				t.Fatalf("broker effect: positions=%v err=%v", positions, err)
			}
			id, _ := body["operation_id"].(string)
			st.mu.Lock()
			persisted, ok := st.operations[id]
			st.mu.Unlock()
			if !ok || persisted.DerivedMaxRisk != units.MustMicros("300") ||
				persisted.RequiredCash != units.MustMicros("300") ||
				persisted.ApprovedPriceCap != units.MustMicros("3") ||
				persisted.WorkingPrice != units.MustMicros("2.995") ||
				persisted.Qty != units.MustQty("1") || persisted.Multiplier != 100 {
				t.Fatalf("persisted=%+v", persisted)
			}
		})
	}
}

func TestRequiredCashBuyingPowerBoundary(t *testing.T) {
	plan := `{"stop":"-30%","invalidation":"x","time_stop":"15:45","target":"+50%"}`
	tests := []struct {
		cash   string
		class  string
		reason string
	}{
		{"299.999999", "REJECT", "insufficient_buying_power"},
		{"300", "C", ""},
	}
	for _, tc := range tests {
		t.Run(tc.cash, func(t *testing.T) {
			b := newFake(tc.cash)
			setQuote(b, "SPY", "2.99", "3.00", 45_000)
			s := &server{limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}
			w, body := postOperation(t, s,
				`{"action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"plan":`+plan+`}`)
			if w.Code != http.StatusOK || body["class"] != tc.class {
				t.Fatalf("status=%d body=%v", w.Code, body)
			}
			if tc.reason != "" {
				reasons := body["reasons"].([]any)
				if reasons[0] != tc.reason {
					t.Fatalf("reasons=%v", reasons)
				}
			}
		})
	}
}

func TestSpendableBuyingPowerUsesProviderValueMinusLocalReservations(t *testing.T) {
	available, err := spendableBuyingPower(units.MustMicros("401.16"), units.MustMicros("125.123456"))
	if err != nil || available != units.MustMicros("276.036544") {
		t.Fatalf("available=%s err=%v", available, err)
	}
	available, err = spendableBuyingPower(units.MustMicros("50"), units.MustMicros("60"))
	if err != nil || available != units.MustMicros("-10") {
		t.Fatalf("negative available=%s err=%v", available, err)
	}
}

func TestDerivedRequestFieldsAreStructurallyRejected(t *testing.T) {
	s := &server{limits: dualLedgerLimits(), broker: newFake("300"), store: newMemoryStore()}
	for _, field := range []string{
		`"derived_max_risk":1`,
		`"required_cash":1`,
		`"verified_reduction":true`,
		`"multiplier":100`,
	} {
		payload := `{"action":"open","kind":"equity","underlying":"SPY","symbol":"SPY","side":"buy","qty":0.1,` +
			field + `}`
		w, _ := postOperation(t, s, payload)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("field=%s status=%d body=%s", field, w.Code, w.Body.String())
		}
	}
}

func TestOpenSellAlwaysRejectsBothKinds(t *testing.T) {
	for _, kind := range []string{"equity", "option"} {
		for _, seeded := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/seeded=%t", kind, seeded), func(t *testing.T) {
				b := newFake("1000")
				setQuote(b, "SELL", "0.34", "0.35", 45_000)
				if kind == "option" {
					b.SetInstrument(broker.Instrument{Symbol: "SELL", Kind: "option", Multiplier: 100})
				}
				if seeded {
					if result, err := placeOrder(b, "SELL", "buy", "1", "0.35", kind); err != nil || result.State != "filled" {
						t.Fatalf("seed: result=%+v err=%v", result, err)
					}
				}
				s := &server{limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}
				w, body := postOperation(t, s,
					`{"action":"open","kind":"`+kind+`","underlying":"SELL","symbol":"SELL","side":"sell","qty":1,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`)
				if w.Code != http.StatusOK || body["class"] != "REJECT" {
					t.Fatalf("status=%d body=%v", w.Code, body)
				}
				reasons := body["reasons"].([]any)
				if reasons[0] != "uncovered_short" {
					t.Fatalf("reasons=%v", reasons)
				}
			})
		}
	}
}

func TestUnknownEquityBlocksOpenButNotQuotedClose(t *testing.T) {
	b := newFake("1000")
	setQuote(b, "A", "9.90", "10", 1_000)
	setQuote(b, "B", "9.90", "10", 1_000)
	for _, symbol := range []string{"A", "B"} {
		if result, err := placeOrder(b, symbol, "buy", "1", "10", "equity"); err != nil || result.State != "filled" {
			t.Fatalf("seed %s: result=%+v err=%v", symbol, result, err)
		}
	}
	b.DeleteQuote("A")
	s := &server{limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}
	stateResponse := httptest.NewRecorder()
	s.getState(stateResponse, httptest.NewRequest(http.MethodGet, "/state", nil))
	if stateResponse.Code != http.StatusOK {
		t.Fatalf("state: status=%d body=%s", stateResponse.Code, stateResponse.Body.String())
	}
	var state map[string]any
	if err := json.Unmarshal(stateResponse.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	account := state["account"].(map[string]any)
	if account["equity_known"] != false {
		t.Fatalf("account=%v, want equity_known=false", account)
	}

	w, body := postOperation(t, s,
		`{"action":"open","kind":"equity","underlying":"B","symbol":"B","side":"buy","qty":1,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" || body["reasons"].([]any)[0] != "equity_unknown" {
		t.Fatalf("unknown-equity open: status=%d body=%v", w.Code, body)
	}

	w, body = postOperation(t, s, `{"action":"close","symbol":"B","qty":1}`)
	if w.Code != http.StatusOK || body["class"] != "A" || body["status"] != "executed" {
		t.Fatalf("quoted close: status=%d body=%v", w.Code, body)
	}

	w, body = postOperation(t, s, `{"action":"close","symbol":"A","qty":1}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" || body["reasons"].([]any)[0] != "market_data_unavailable" {
		t.Fatalf("unquoted close: status=%d body=%v", w.Code, body)
	}
	positions, err := b.Positions(context.Background())
	if err != nil || len(positions) != 1 || positions[0].Symbol != "A" {
		t.Fatalf("positions=%v err=%v, want untouched A", positions, err)
	}
}

func TestNonpositiveEquityRejectsOpenAndCloseStillWorks(t *testing.T) {
	for _, cash := range []string{"0", "-1"} {
		t.Run("open/"+cash, func(t *testing.T) {
			b := newFake(cash)
			setQuote(b, "Z", "1", "1.10", 1_000)
			s := &server{limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}
			w, body := postOperation(t, s,
				`{"action":"open","kind":"equity","underlying":"Z","symbol":"Z","side":"buy","qty":0.5,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`)
			if w.Code != http.StatusOK || body["class"] != "REJECT" ||
				body["reasons"].([]any)[0] != "nonpositive_equity" {
				t.Fatalf("status=%d body=%v", w.Code, body)
			}
		})
	}

	b := newFake("0")
	setQuote(b, "Z", "1", "1.10", 1_000)
	if result, err := placeOrder(b, "Z", "buy", "1", "1.10", "equity"); err != nil || result.State != "filled" {
		t.Fatalf("seed negative equity: result=%+v err=%v", result, err)
	}
	s := &server{limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}
	w, body := postOperation(t, s, `{"action":"close","symbol":"Z","qty":1}`)
	if w.Code != http.StatusOK || body["class"] != "A" || body["status"] != "executed" {
		t.Fatalf("close: status=%d body=%v", w.Code, body)
	}
}

func TestQuantityInstrumentAndOverflowBoundaries(t *testing.T) {
	b := newFake("1000")
	setQuote(b, "Q", "1", "1.10", 1_000)
	b.SetInstrument(broker.Instrument{Symbol: "Q", Kind: "option", Multiplier: 100})
	s := &server{limits: dualLedgerLimits(), broker: b, store: newMemoryStore()}

	w, _ := postOperation(t, s,
		`{"action":"open","kind":"option","underlying":"Q","symbol":"Q","side":"buy","qty":1.5}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("fractional option status=%d body=%s", w.Code, w.Body.String())
	}
	w, body := postOperation(t, s,
		`{"action":"open","kind":"equity","underlying":"Q","symbol":"Q","side":"buy","qty":0.5,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`)
	if w.Code != http.StatusOK || body["class"] != "B" {
		t.Fatalf("fractional equity status=%d body=%v", w.Code, body)
	}

	b.SetInstrument(broker.Instrument{Symbol: "Q", Kind: "option", Multiplier: 10})
	w, body = postOperation(t, s,
		`{"action":"open","kind":"option","underlying":"Q","symbol":"Q","side":"buy","qty":1,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" ||
		body["reasons"].([]any)[0] != "unsupported_contract" {
		t.Fatalf("nonstandard multiplier status=%d body=%v", w.Code, body)
	}
	b.DeleteInstrument("Q")
	w, body = postOperation(t, s,
		`{"action":"open","kind":"option","underlying":"Q","symbol":"Q","side":"buy","qty":1,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" ||
		body["reasons"].([]any)[0] != "unsupported_contract" {
		t.Fatalf("missing multiplier status=%d body=%v", w.Code, body)
	}

	w, body = postOperation(t, s,
		`{"action":"open","kind":"equity","underlying":"SPY","symbol":"SPY","side":"buy","qty":9223372036854.775807,"limit":9223372036854.775807}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" ||
		body["reasons"].([]any)[0] != "risk_overflow" {
		t.Fatalf("overflow status=%d body=%v", w.Code, body)
	}

	w, _ = postOperation(t, s,
		`{"action":"open","kind":"equity","underlying":"SPY","symbol":"SPY","side":"buy","qty":1e-6}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("exponent qty status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestReviewRejectsGarbageVerdictWithoutMutation(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	st := newMemoryStore()
	if err := st.InsertOperation(id, "test", "C", "pending_review", risk.Operation{}, risk.Verdict{}, nil); err != nil {
		t.Fatal(err)
	}
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: st}
	req := httptest.NewRequest(http.MethodPost, "/operations/"+id+"/review", bytes.NewBufferString(`{"verdict":"BANANA"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.review(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
	row, err := st.GetOperation(id)
	if err != nil || row.Status != "pending_review" {
		t.Fatalf("operation mutated: row=%+v err=%v", row, err)
	}
}

func TestJSONWriteBoundaryAppliesToSmallEndpoints(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: newMemoryStore()}
	tests := []struct {
		name    string
		target  string
		body    string
		pathKey string
		pathVal string
		handler http.HandlerFunc
	}{
		{name: "review", target: "/operations/" + id + "/review", body: `{"verdict":"rejected"}`, pathKey: "id", pathVal: id, handler: s.review},
		{name: "journal", target: "/journal", body: `{"operation_id":"` + id + `"}`, handler: s.postJournal},
		{name: "blackboard", target: "/blackboard/2026-07-17", body: `{}`, pathKey: "day", pathVal: "2026-07-17", handler: s.putBlackboard},
		{name: "sim_quote", target: "/sim/quote", body: `{"symbol":"SPY","bid":100,"ask":100.1}`, handler: s.simQuote},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.target, bytes.NewBufferString(tc.body))
			if tc.pathKey != "" {
				req.SetPathValue(tc.pathKey, tc.pathVal)
			}
			w := httptest.NewRecorder()
			tc.handler(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("missing content-type: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBlackboardRejectsInvalidDayAndOversizedDocument(t *testing.T) {
	st := newMemoryStore()
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: st}

	req := httptest.NewRequest(http.MethodPut, "/blackboard/not-a-date", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("day", "not-a-date")
	w := httptest.NewRecorder()
	s.putBlackboard(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid day: status=%d body=%s", w.Code, w.Body.String())
	}

	large := `{"doc":"` + strings.Repeat("x", int(maxJSONBodyBytes)) + `"}`
	req = httptest.NewRequest(http.MethodPut, "/blackboard/2026-07-17", bytes.NewBufferString(large))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("day", "2026-07-17")
	w = httptest.NewRecorder()
	s.putBlackboard(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized doc: status=%d body=%s, want 413", w.Code, w.Body.String())
	}
	if len(st.blackboards) != 0 {
		t.Fatalf("oversized document was persisted: %v", st.blackboards)
	}
}

func TestJournalInvalidReferenceIs400WithoutDatabaseDetails(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	st := newMemoryStore()
	st.journalErr = store.ErrInvalidOperationReference
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: st}
	req := httptest.NewRequest(http.MethodPost, "/journal", bytes.NewBufferString(`{"operation_id":"`+id+`","hypothesis":{}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.postJournal(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "pq:") || strings.Contains(w.Body.String(), "constraint") {
		t.Fatalf("database detail leaked: %s", w.Body.String())
	}
}

func TestLessonsLimitIsStrictlyBounded(t *testing.T) {
	s := &server{limits: config.Limits{}, broker: newFake("300"), store: newMemoryStore()}
	for _, value := range []string{"-1", "1000000000", "banana"} {
		req := httptest.NewRequest(http.MethodGet, "/lessons?limit="+value, nil)
		w := httptest.NewRecorder()
		s.getLessons(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("limit=%q status=%d body=%s, want 400", value, w.Code, w.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/lessons?limit=100", nil)
	w := httptest.NewRecorder()
	s.getLessons(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("limit=100 status=%d body=%s, want 200", w.Code, w.Body.String())
	}
}
