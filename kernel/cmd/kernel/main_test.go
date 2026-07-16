package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
)

type journalEntry struct {
	operationID string
	hypothesis  any
	shadow      bool
}

type memoryStore struct {
	mu          sync.Mutex
	ledgerLocks [2]sync.Mutex
	statuses    map[string]string
	classes     map[string]string
	shadows     map[string]bool
	journals    []journalEntry
	events      []string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		statuses: map[string]string{},
		classes:  map[string]string{},
		shadows:  map[string]bool{},
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

func (m *memoryStore) InsertEvent(kind string, _ any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, kind)
	return nil
}

func (m *memoryStore) InsertOperation(id, _, class, status string, payload, _ any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[id] = status
	m.classes[id] = class
	if op, ok := payload.(risk.Operation); ok {
		m.shadows[id] = op.Shadow
	}
	return nil
}

func (m *memoryStore) SetOperationStatus(id, status string, _ any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.statuses[id] = status
	return nil
}

func (m *memoryStore) GetOperation(string) (*store.OperationRow, error) {
	return nil, errors.New("not found")
}

func (m *memoryStore) InsertJournal(operationID string, hypothesis, _, _ any, shadow bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.journals = append(m.journals, journalEntry{operationID: operationID, hypothesis: hypothesis, shadow: shadow})
	return nil
}

func (m *memoryStore) TopLessons(int) ([]store.Lesson, error) { return []store.Lesson{}, nil }

func (m *memoryStore) GetBlackboard(string) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (m *memoryStore) PutBlackboard(string, json.RawMessage) error { return nil }

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

func dualLedgerLimits() config.Limits {
	limits := config.Limits{}
	limits.HardLimits.MaxRiskPerTradePct = 35
	limits.HardLimits.MaxTotalOpenRiskPct = 80
	limits.HardLimits.MaxNewTradesPerDay = 6
	limits.InstrumentRules.MinOpenInterest = 300
	limits.InstrumentRules.MaxRelativeSpread = 0.15
	limits.PlanRequirements = []string{"stop", "invalidation", "time_stop", "target"}
	return limits
}

func TestProposeUsesIndependentLiveAndShadowLedgers(t *testing.T) {
	st := newMemoryStore()
	s := &server{limits: dualLedgerLimits(), broker: broker.NewFake(300), store: st}
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
		payload := `{"proposer":"barrier","action":"open","kind":"equity","underlying":"I4","symbol":"I4","side":"buy","qty":0.01,"max_risk_usd":35,"plan":{"stop":"90","invalidation":"x","time_stop":"15:45","target":"120"}}`
		if shadow {
			name = "shadow"
			payload = `{"proposer":"barrier","action":"open","kind":"equity","underlying":"I4","symbol":"I4","side":"buy","qty":0.01,"max_risk_usd":35,"shadow":true,"plan":{"stop":"90","invalidation":"x","time_stop":"15:45","target":"120"}}`
		}
		t.Run(name, func(t *testing.T) {
			st := newMemoryStore()
			s := &server{limits: dualLedgerLimits(), broker: broker.NewFake(300), store: st}
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
	b := broker.NewFake(100_000)
	b.SetQuote(broker.Quote{Symbol: "SPY", Bid: 4.20, Ask: 4.40, OpenInterest: 10_000})
	if seeded, err := b.PlaceLimitOrder("SPY", "buy", 1, 4.40, "option"); err != nil || seeded.State != "filled" {
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
	b := broker.NewFake(100_000)
	b.SetQuote(broker.Quote{Symbol: "SPY", Bid: 4.20, Ask: 4.40, OpenInterest: 10_000})
	if seeded, err := b.PlaceLimitOrder("SPY", "sell", 1, 4.20, "option"); err != nil || seeded.State != "filled" {
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
	s := &server{limits: config.Limits{}, broker: broker.NewFake(300), store: newMemoryStore()}
	w, body := postOperation(t, s, `{"proposer":"test","action":"cancel"}`)
	if w.Code != http.StatusBadRequest || body["error"] != "cancel requires broker_order_id" {
		t.Fatalf("status=%d body=%v", w.Code, body)
	}
}

func TestProposeCloseRequiresAndCannotExceedPosition(t *testing.T) {
	b := broker.NewFake(10_000)
	s := &server{limits: config.Limits{}, broker: b, store: newMemoryStore()}

	w, body := postOperation(t, s, `{"proposer":"test","action":"close","symbol":"SPY","qty":1}`)
	if w.Code != http.StatusBadRequest || body["error"] != "close requires an existing position for SPY" {
		t.Fatalf("missing position: status=%d body=%v", w.Code, body)
	}

	if seeded, err := b.PlaceLimitOrder("SPY", "buy", 1, 623.14, "option"); err != nil || seeded.State != "filled" {
		t.Fatalf("seed long position: result=%+v err=%v", seeded, err)
	}
	w, body = postOperation(t, s, `{"proposer":"test","action":"close","kind":"option","symbol":"SPY","qty":2}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("over-close: status=%d body=%v", w.Code, body)
	}
	positions, err := b.GetPositions()
	if err != nil || len(positions) != 1 || positions[0].Qty != 1 {
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
		s := &server{limits: config.Limits{}, broker: broker.NewFake(300), store: newMemoryStore()}
		w, body := postOperation(t, s, payload)
		if w.Code != http.StatusBadRequest {
			t.Errorf("payload=%s status=%d body=%v, want 400", payload, w.Code, body)
		}
	}
}

func TestConcurrentCloseCannotOpenReversePosition(t *testing.T) {
	b := broker.NewFake(10_000)
	if seeded, err := b.PlaceLimitOrder("SPY", "buy", 1, 623.14, "option"); err != nil || seeded.State != "filled" {
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
	positions, err := b.GetPositions()
	if err != nil || len(positions) != 0 {
		t.Fatalf("positions=%v err=%v, want flat", positions, err)
	}
}

func TestExecuteRefusesUnverifiedClose(t *testing.T) {
	b := broker.NewFake(10_000)
	if seeded, err := b.PlaceLimitOrder("SPY", "buy", 1, 623.14, "option"); err != nil || seeded.State != "filled" {
		t.Fatalf("seed long position: result=%+v err=%v", seeded, err)
	}
	s := &server{limits: config.Limits{}, broker: b, store: newMemoryStore()}
	_, err := s.execute("test-op", risk.Operation{Action: "close", Symbol: "SPY", Kind: "option", Side: "sell", Qty: 1}, &broker.Quote{Symbol: "SPY", Bid: 623.10, Ask: 623.14})
	if err == nil {
		t.Fatal("unverified direct close execution succeeded")
	}
	positions, getErr := b.GetPositions()
	if getErr != nil || len(positions) != 1 || positions[0].Qty != 1 {
		t.Fatalf("position changed: positions=%v err=%v", positions, getErr)
	}
}

func TestProposeCancelUnknownOrder(t *testing.T) {
	st := newMemoryStore()
	s := &server{limits: config.Limits{}, broker: broker.NewFake(300), store: st}
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
	s := &server{limits: config.Limits{}, broker: broker.NewFake(300), store: st}
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
	s := &server{limits: config.Limits{}, broker: broker.NewFake(300), store: st}
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
	limits.HardLimits.MaxRiskPerTradePct = 35
	limits.HardLimits.MaxTotalOpenRiskPct = 80
	limits.HardLimits.MaxNewTradesPerDay = 6
	limits.InstrumentRules.MinOpenInterest = 300
	limits.InstrumentRules.MaxRelativeSpread = 0.15
	limits.PlanRequirements = []string{"stop", "invalidation", "time_stop", "target"}
	b := broker.NewFake(300)
	s := &server{limits: limits, broker: b, store: newMemoryStore()}

	validPlan := `{"stop":"-30%","invalidation":"x","time_stop":"15:45","target":"+50%"}`
	w, body := postOperation(t, s, `{"proposer":"test","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"sell","qty":1,"limit":623.10,"max_risk_usd":35,"plan":`+validPlan+`}`)
	if w.Code != http.StatusOK || body["class"] != "REJECT" || body["status"] != "rejected" {
		t.Fatalf("inferred naked short: status=%d body=%v", w.Code, body)
	}

	blankPlan := `{"stop":" ","invalidation":"x","time_stop":"15:45","target":"+50%"}`
	w, body = postOperation(t, s, `{"proposer":"test","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"plan":`+blankPlan+`}`)
	if w.Code != http.StatusOK || body["class"] != "C" || body["status"] != "pending_review" {
		t.Fatalf("whitespace plan: status=%d body=%v", w.Code, body)
	}
	positions, err := b.GetPositions()
	if err != nil || len(positions) != 0 {
		t.Fatalf("broker changed after rejected/pending opens: positions=%v err=%v", positions, err)
	}
}

func TestProposeRequiresJSONAndRejectsUnknownFields(t *testing.T) {
	s := &server{limits: config.Limits{}, broker: broker.NewFake(300), store: newMemoryStore()}
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
