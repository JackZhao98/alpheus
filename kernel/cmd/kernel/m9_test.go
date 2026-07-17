package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type m9BarrierResult struct {
	code   int
	class  string
	status string
}

func runM9ProposalBarrier(s *server, payload string, requests int) []m9BarrierResult {
	ready := sync.WaitGroup{}
	ready.Add(requests)
	start := make(chan struct{})
	results := make(chan m9BarrierResult, requests)
	for i := 0; i < requests; i++ {
		go func() {
			ready.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/operations", bytes.NewBufferString(payload))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			s.propose(response, req)
			var body map[string]any
			_ = json.Unmarshal(response.Body.Bytes(), &body)
			class, _ := body["class"].(string)
			status, _ := body["status"].(string)
			results <- m9BarrierResult{code: response.Code, class: class, status: status}
		}()
	}
	ready.Wait()
	close(start)
	out := make([]m9BarrierResult, 0, requests)
	for i := 0; i < requests; i++ {
		out = append(out, <-results)
	}
	return out
}

func TestM9CounterBarriersSerializeOpenRiskAndBuyingPower(t *testing.T) {
	const requests = 20
	payload := `{"proposer":"m9-barrier","action":"open","kind":"equity","underlying":"M9","symbol":"M9","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	for _, tc := range []struct {
		name        string
		seedRisk    units.Micros
		seedCash    units.Micros
		loserClass  string
		loserStatus string
	}{
		{name: "total_open_risk", seedRisk: units.MustMicros("205"), loserClass: "C", loserStatus: "pending_review"},
		{name: "buying_power", seedCash: units.MustMicros("265"), loserClass: "REJECT", loserStatus: "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newMemoryStore()
			st.openReservations["m9-seed"] = store.OpenReservation{
				ID: "m9-seed", OperationID: "m9-seed-operation", Ledger: "live",
				Symbol: "SEED", Kind: "equity", OriginalQty: units.MustQty("1"),
				RemainingQty: units.MustQty("1"), OriginalRisk: tc.seedRisk,
				RemainingRisk: tc.seedRisk, OriginalCash: tc.seedCash,
				RemainingCash: tc.seedCash, ResourceState: "held", CreatedAt: time.Now().UTC(),
			}
			venue := newFake("300")
			setQuote(venue, "M9", "34.99", "35", 1_000)
			s := &server{limits: dualLedgerLimits(), broker: venue, store: st}
			results := runM9ProposalBarrier(s, payload, requests)
			classes := map[string]int{}
			for _, result := range results {
				if result.code != http.StatusOK {
					t.Fatalf("result=%+v", result)
				}
				classes[result.class+"/"+result.status]++
			}
			if classes["B/auto_approved"]+classes["B/executed"] != 1 ||
				classes[tc.loserClass+"/"+tc.loserStatus] != requests-1 {
				t.Fatalf("classes=%v", classes)
			}
			if len(st.grants) != 1 {
				t.Fatalf("new grants=%d, want 1", len(st.grants))
			}
		})
	}
}

func TestM9FullTradingDayReplayIsIdempotent(t *testing.T) {
	st := newMemoryStore()
	venue := newFake("300")
	setQuote(venue, "DAY", "34.99", "35", 1_000)
	venue.SetInstrument(broker.Instrument{
		Symbol: "DAY", Kind: "equity", Multiplier: 1,
		PriceTick: units.MustMicros("0.01"), QtyIncrement: units.MustQty("1"),
	})
	s := &server{
		mode: protectedMode(config.ModeLive), limits: dualLedgerLimits(),
		broker: venue, store: st, providerDedupeVerified: true,
	}
	handler := s.routes()
	payload := `{"proposer":"m9-replay","action":"open","kind":"equity","underlying":"DAY","symbol":"DAY","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	operationIDs := make([]string, 6)
	for i := range operationIDs {
		key := "m9-full-day-" + string(rune('a'+i))
		response := routeRequestWithKey(handler, http.MethodPost, "/operations", payload, "runtime-secret", key)
		if response.Code != http.StatusOK {
			t.Fatalf("first pass %d: status=%d body=%s", i, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["class"] != "B" {
			t.Fatalf("first pass %d body=%v", i, body)
		}
		operationIDs[i], _ = body["operation_id"].(string)
	}
	for i := len(operationIDs) - 1; i >= 0; i-- {
		key := "m9-full-day-" + string(rune('a'+i))
		response := routeRequestWithKey(handler, http.MethodPost, "/operations", payload, "runtime-secret", key)
		if response.Code != http.StatusOK {
			t.Fatalf("replay %d: status=%d body=%s", i, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["idempotent_replay"] != true || body["operation_id"] != operationIDs[i] {
			t.Fatalf("replay %d body=%v", i, body)
		}
	}

	providerOrders, err := venue.OpenOrders(context.Background())
	if err != nil || len(providerOrders) != 6 {
		t.Fatalf("provider orders=%d err=%v", len(providerOrders), err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.operationRows) != 6 || len(st.grants) != 6 || len(st.openReservations) != 6 ||
		len(st.attempts) != 6 || len(st.orders) != 6 {
		t.Fatalf("operations=%d grants=%d reservations=%d attempts=%d orders=%d",
			len(st.operationRows), len(st.grants), len(st.openReservations), len(st.attempts), len(st.orders))
	}
	for _, reservation := range st.openReservations {
		linked := false
		for _, attempt := range st.attempts {
			if attempt.OpenReservationID == reservation.ID {
				_, linked = st.orders[attempt.ID]
				break
			}
		}
		if !linked {
			t.Fatalf("unsafe orphan reservation=%+v", reservation)
		}
	}
}

type failResolveOnceStore struct {
	storeAPI
	failed atomic.Bool
}

func (s *failResolveOnceStore) ResolveAttempt(id string, fencingToken int, resolution store.AttemptResolution) (bool, error) {
	if s.failed.CompareAndSwap(false, true) {
		return false, store.ErrUnavailable
	}
	return s.storeAPI.ResolveAttempt(id, fencingToken, resolution)
}

func TestM9BrokerAcceptanceSurvivesDatabaseFailureBeforeResolution(t *testing.T) {
	st := newMemoryStore()
	flaky := &failResolveOnceStore{storeAPI: st}
	venue := newFake("300")
	setQuote(venue, "FAULT", "34.99", "35", 1_000)
	s := &server{
		limits: dualLedgerLimits(), broker: venue, store: flaky,
		providerDedupeVerified: true, attemptStale: time.Millisecond,
		claimTimeout: time.Millisecond, brokerTimeout: time.Second,
	}
	payload := `{"proposer":"m9-fault","action":"open","kind":"equity","underlying":"FAULT","symbol":"FAULT","side":"buy","qty":1,"max_risk_usd":35,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}`
	response, _ := postOperation(t, s, payload)
	if response.Code == http.StatusOK {
		t.Fatalf("injected database failure returned success: %s", response.Body.String())
	}

	st.mu.Lock()
	for id, attempt := range st.attempts {
		if attempt.State != "claimed" {
			st.mu.Unlock()
			t.Fatalf("attempt after injected failure=%+v", attempt)
		}
		attempt.ClaimedAt = time.Now().Add(-time.Second)
		st.attempts[id] = attempt
	}
	st.mu.Unlock()
	if err := s.reconcileAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}

	orders, err := venue.OpenOrders(context.Background())
	if err != nil || len(orders) != 1 {
		t.Fatalf("provider orders=%d err=%v, want exactly one", len(orders), err)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, attempt := range st.attempts {
		if attempt.State == "unknown" || attempt.State == "claimed" || attempt.State == "pending" {
			t.Fatalf("unresolved attempt=%+v", attempt)
		}
	}
}
