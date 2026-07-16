package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

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
	statuses map[string]string
	journals []journalEntry
	events   []string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{statuses: map[string]string{}}
}

func (m *memoryStore) CountTradesToday() (int, error) { return 0, nil }

func (m *memoryStore) Event(kind string, _ any) { m.events = append(m.events, kind) }

func (m *memoryStore) InsertOperation(id, _, _, status string, _, _ any) error {
	m.statuses[id] = status
	return nil
}

func (m *memoryStore) SetOperationStatus(id, status string, _ any) error {
	m.statuses[id] = status
	return nil
}

func (m *memoryStore) GetOperation(string) (*store.OperationRow, error) {
	return nil, errors.New("not found")
}

func (m *memoryStore) InsertJournal(operationID string, hypothesis, _, _ any, shadow bool) error {
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
