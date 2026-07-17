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
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type journalEntry struct {
	operationID string
	hypothesis  any
	shadow      bool
}

type memoryStore struct {
	mu            sync.Mutex
	ledgerLocks   [2]sync.Mutex
	statuses      map[string]string
	classes       map[string]string
	shadows       map[string]bool
	operations    map[string]risk.Operation
	operationRows map[string]store.OperationRow
	verdicts      map[string]json.RawMessage
	journals      []journalEntry
	events        []string
	blackboards   map[string]json.RawMessage
	journalErr    error
	halted        bool
	haltReason    string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		statuses:      map[string]string{},
		classes:       map[string]string{},
		shadows:       map[string]bool{},
		operations:    map[string]risk.Operation{},
		operationRows: map[string]store.OperationRow{},
		verdicts:      map[string]json.RawMessage{},
		blackboards:   map[string]json.RawMessage{},
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
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, status := range m.statuses {
		if m.classes[id] == "B" && m.shadows[id] == shadow && (status == "auto_approved" || status == "executed") {
			n++
		}
	}
	return n, nil
}

func (m *memoryStore) WithLedgerLock(shadow bool, _ time.Time, fn func(store.OperationGate) error) error {
	index := 0
	if shadow {
		index = 1
	}
	m.ledgerLocks[index].Lock()
	defer m.ledgerLocks[index].Unlock()
	return fn(m)
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

func (m *memoryStore) InsertOperation(id, proposer, class, status string, payload, verdict any) error {
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
	m.operationRows[id] = store.OperationRow{
		ID: id, TS: time.Now().UTC(), Proposer: proposer, Class: class,
		Status: status, Payload: payloadJSON, Verdict: verdictJSON,
	}
	return nil
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
	limits.InstrumentRules.MinOpenInterest = 300
	limits.InstrumentRules.MaxRelativeSpread = units.MustRatio("0.15")
	limits.RiskDeclarationTolerance = units.MustMicros("0.01")
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
		if w.Code != http.StatusOK || body["class"] != "B" || body["status"] != "auto_approved" {
			t.Fatalf("shadow %d: status=%d body=%v, want B/auto_approved", i+1, w.Code, body)
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
	s := &server{limits: config.Limits{}, broker: b, store: newMemoryStore()}
	_, err := s.execute(context.Background(), "test-op", risk.Operation{Action: "close", Symbol: "SPY", Kind: "option", Side: "sell", Qty: units.MustQty("1")}, &broker.Quote{Symbol: "SPY", Bid: units.MustMicros("623.10"), Ask: units.MustMicros("623.14")})
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
	if w.Code != http.StatusBadGateway || body["error"] != "market_data_unavailable" {
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
	if err := st.InsertOperation(id, "test", "C", "pending_review", risk.Operation{}, risk.Verdict{}); err != nil {
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
