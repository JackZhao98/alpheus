package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
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

	w, body := postOperation(t, s, `{"proposer":"test","action":"close","kind":"option","symbol":"SPY","side":"buy","qty":1}`)
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

	w, body := postOperation(t, s, `{"proposer":"test","action":"close","kind":"option","symbol":"SPY","side":"sell","qty":1}`)
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
