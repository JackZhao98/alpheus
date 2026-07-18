package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

const m4Plan = `"plan":{"stop":"190","invalidation":"x","time_stop":"15:45","target":"250"}`

func newM4Server() (*server, *memoryStore, *broker.Fake) {
	st := newMemoryStore()
	st.m3aActive = true
	venue := newFake("300")
	return &server{
		limits: dualLedgerLimits(), broker: venue, store: st,
		proposalTTL: 30 * time.Minute,
	}, st, venue
}

func proposeM4ClassC(t *testing.T, s *server, venue *broker.Fake, symbol string) string {
	t.Helper()
	setQuote(venue, symbol, "199", "200", 1_000)
	response, body := postOperation(t, s, `{"action":"open","kind":"equity","underlying":"`+symbol+`","symbol":"`+symbol+`","side":"buy","qty":1,`+m4Plan+`}`)
	if response.Code != http.StatusOK || body["class"] != "C" || body["status"] != "pending_review" {
		t.Fatalf("proposal status=%d body=%v", response.Code, body)
	}
	id, _ := body["operation_id"].(string)
	if id == "" {
		t.Fatalf("proposal has no operation id: %v", body)
	}
	return id
}

func reviewM4(s *server, id, verdict string) (*httptest.ResponseRecorder, map[string]any) {
	req := httptest.NewRequest(http.MethodPost, "/operations/"+id+"/review",
		bytes.NewBufferString(`{"verdict":"`+verdict+`","rationale":"human decision"}`))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("id", id)
	response := httptest.NewRecorder()
	s.review(response, req)
	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return response, body
}

func TestM4ApproveStagesOneAtomicEntitlementAndExecutes(t *testing.T) {
	s, st, venue := newM4Server()
	id := proposeM4ClassC(t, s, venue, "M4-APPROVE")

	response, body := reviewM4(s, id, "approved")
	if response.Code != http.StatusOK || body["status"] != "approved" {
		t.Fatalf("approval status=%d body=%v", response.Code, body)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.statuses[id] != "approved" || len(st.grants) != 1 || len(st.openReservations) != 1 ||
		len(st.attempts) != 1 || len(st.orders) != 1 {
		t.Fatalf("status=%s grants=%d reservations=%d attempts=%d orders=%d",
			st.statuses[id], len(st.grants), len(st.openReservations), len(st.attempts), len(st.orders))
	}
	for _, attempt := range st.attempts {
		if attempt.OperationID != id || attempt.OpenReservationID == "" || attempt.State != "placed" {
			t.Fatalf("attempt=%+v", attempt)
		}
	}
	if !containsString(st.events, "operation_reviewed") {
		t.Fatalf("approval event missing: %v", st.events)
	}
}

func TestM4ApprovalDoesNotExecuteWhenStagingCrossesMarketDay(t *testing.T) {
	t.Setenv("TZ_MARKET", "America/New_York")
	s, st, venue := newM4Server()
	id := proposeM4ClassC(t, s, venue, "M4-MIDNIGHT")
	beforeMidnight := time.Date(2026, 1, 16, 4, 59, 59, 0, time.UTC)
	afterMidnight := time.Date(2026, 1, 16, 5, 0, 0, 0, time.UTC)
	clockCalls := 0
	st.databaseNow = func() time.Time {
		clockCalls++
		if clockCalls >= 5 {
			return afterMidnight
		}
		return beforeMidnight
	}
	response, body := reviewM4(s, id, "approved")
	if response.Code != http.StatusServiceUnavailable || body["error"] != "market day advanced; retry" {
		t.Fatalf("approval status=%d body=%v clock_calls=%d", response.Code, body, clockCalls)
	}
	fills, err := venue.RecentFills(context.Background(), time.Time{})
	if err != nil || len(fills) != 0 {
		t.Fatalf("cross-day approval reached broker fills=%v err=%v", fills, err)
	}
}

func TestM4ApprovalAbsoluteFailureRollsBackAndRemainsReviewable(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*memoryStore, *broker.Fake)
		want   string
	}{
		"breaker": {
			mutate: func(st *memoryStore, _ *broker.Fake) {
				st.mu.Lock()
				st.halted, st.haltReason = true, "manual"
				st.mu.Unlock()
			},
			want: "breaker halted: manual",
		},
		"crossed_quote": {
			mutate: func(_ *memoryStore, venue *broker.Fake) {
				_ = venue.SetQuote(broker.Quote{
					Symbol: "M4-ABS", Bid: units.MustMicros("200"), Ask: units.MustMicros("199"),
					OpenInterest: 1_000,
				})
			},
			want: "market_data_unavailable",
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, st, venue := newM4Server()
			id := proposeM4ClassC(t, s, venue, "M4-ABS")
			tc.mutate(st, venue)
			response, body := reviewM4(s, id, "approved")
			if response.Code != http.StatusConflict || body["error"] != tc.want {
				t.Fatalf("approval status=%d body=%v", response.Code, body)
			}
			st.mu.Lock()
			defer st.mu.Unlock()
			if st.statuses[id] != "pending_review" || len(st.grants) != 0 ||
				len(st.openReservations) != 0 || len(st.attempts) != 0 {
				t.Fatalf("status=%s grants=%d reservations=%d attempts=%d",
					st.statuses[id], len(st.grants), len(st.openReservations), len(st.attempts))
			}
		})
	}
}

func TestM4ExpiredApprovalIsTerminalWithoutEntitlement(t *testing.T) {
	s, st, venue := newM4Server()
	id := proposeM4ClassC(t, s, venue, "M4-EXPIRED")
	st.mu.Lock()
	row := st.operationRows[id]
	row.TS = time.Now().UTC().Add(-time.Hour)
	st.operationRows[id] = row
	st.mu.Unlock()

	response, body := reviewM4(s, id, "approved")
	if response.Code != http.StatusConflict || body["error"] != "proposal_expired" {
		t.Fatalf("approval status=%d body=%v", response.Code, body)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.statuses[id] != "expired" || len(st.grants) != 0 || len(st.attempts) != 0 {
		t.Fatalf("status=%s grants=%d attempts=%d", st.statuses[id], len(st.grants), len(st.attempts))
	}
}

func TestM4ApprovalNeverRaisesPersistedPriceCap(t *testing.T) {
	s, st, venue := newM4Server()
	id := proposeM4ClassC(t, s, venue, "M4-CAP")
	setQuote(venue, "M4-CAP", "249", "250", 1_000)

	response, body := reviewM4(s, id, "approved")
	if response.Code != http.StatusOK || body["status"] != "approved" {
		t.Fatalf("approval status=%d body=%v", response.Code, body)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, attempt := range st.attempts {
		if attempt.Limit != units.MustMicros("200") {
			t.Fatalf("attempt limit=%s, want persisted cap 200", attempt.Limit)
		}
	}
	for _, reservation := range st.openReservations {
		if reservation.OriginalCash != units.MustMicros("200") || reservation.OriginalRisk != units.MustMicros("200") {
			t.Fatalf("reservation=%+v", reservation)
		}
	}
}

func TestM4ConcurrentApprovalCreatesExactlyOneAttempt(t *testing.T) {
	s, st, venue := newM4Server()
	id := proposeM4ClassC(t, s, venue, "M4-RACE")
	const workers = 20
	start := make(chan struct{})
	results := make(chan int, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			response, _ := reviewM4(s, id, "approved")
			results <- response.Code
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for code := range results {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected response status %d", code)
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if ok != 1 || conflicts != workers-1 || len(st.grants) != 1 || len(st.openReservations) != 1 ||
		len(st.attempts) != 1 || len(st.orders) != 1 {
		t.Fatalf("ok=%d conflicts=%d grants=%d reservations=%d attempts=%d orders=%d",
			ok, conflicts, len(st.grants), len(st.openReservations), len(st.attempts), len(st.orders))
	}
}

func TestM4ApprovedClassCConsumesTheSixthDailyGrant(t *testing.T) {
	s, st, venue := newM4Server()
	for i := 0; i < 5; i++ {
		symbol := "M4-B-" + string(rune('A'+i))
		setQuote(venue, symbol, "0.99", "1", 1_000)
		response, body := postOperation(t, s, `{"action":"open","kind":"equity","underlying":"`+symbol+`","symbol":"`+symbol+`","side":"buy","qty":1,`+m4Plan+`}`)
		if response.Code != http.StatusOK || body["class"] != "B" {
			t.Fatalf("B proposal %d status=%d body=%v", i, response.Code, body)
		}
	}
	id := proposeM4ClassC(t, s, venue, "M4-SIXTH")
	response, body := reviewM4(s, id, "approved")
	if response.Code != http.StatusOK {
		t.Fatalf("sixth approval status=%d body=%v", response.Code, body)
	}
	setQuote(venue, "M4-SEVENTH", "0.99", "1", 1_000)
	response, body = postOperation(t, s, `{"action":"open","kind":"equity","underlying":"M4-SEVENTH","symbol":"M4-SEVENTH","side":"buy","qty":1,`+m4Plan+`}`)
	if response.Code != http.StatusOK || body["class"] != "C" {
		t.Fatalf("seventh proposal status=%d body=%v", response.Code, body)
	}
	checks, _ := body["checks"].(map[string]any)
	if checks["daily_trade_count"] != false {
		t.Fatalf("seventh checks=%v", checks)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.grants) != 6 {
		t.Fatalf("grants=%d, want 6", len(st.grants))
	}
}

func TestM4RejectReviewRemainsAtomic(t *testing.T) {
	s, st, venue := newM4Server()
	id := proposeM4ClassC(t, s, venue, "M4-REJECT")
	response, body := reviewM4(s, id, "rejected")
	if response.Code != http.StatusOK || body["status"] != "rejected" {
		t.Fatalf("review status=%d body=%v", response.Code, body)
	}
	row, err := st.GetOperation(id)
	if err != nil || row.Status != "rejected" {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	st.mu.Lock()
	attempts := len(st.attempts)
	st.mu.Unlock()
	if attempts != 0 {
		t.Fatalf("unexpected execution attempts=%d", attempts)
	}
}

func TestM4CommittedApprovalRecoversAfterOriginalProposalTTL(t *testing.T) {
	s, st, venue := newM4Server()
	setQuote(venue, "M4-RECOVER", "199", "200", 1_000)
	operationID := store.NewID()
	op := risk.Operation{
		Action: "open", Kind: "equity", Underlying: "M4-RECOVER", Symbol: "M4-RECOVER",
		Side: "buy", Qty: units.MustQty("1"), Multiplier: 1,
		ApprovedPriceCap: units.MustMicros("200"), WorkingPrice: units.MustMicros("200"),
		RequiredCash: units.MustMicros("200"), DerivedMaxRisk: units.MustMicros("200"),
		Plan: map[string]string{"stop": "190", "invalidation": "x", "time_stop": "15:45", "target": "250"},
	}
	if err := st.InsertOperation(operationID, "m4-recovery", "C", "approved", op,
		map[string]string{"decision": "approved"}, nil); err != nil {
		t.Fatal(err)
	}
	var attempt *store.ExecutionAttempt
	if err := st.WithProposalLock(nil, false, false, func(gate store.OperationGate) error {
		var err error
		attempt, err = s.stageApprovedOpen(gate, operationID, op, time.Now().UTC())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	st.mu.Lock()
	row := st.operationRows[operationID]
	row.TS = time.Now().UTC().Add(-time.Hour)
	st.operationRows[operationID] = row
	st.mu.Unlock()

	if err := s.reconcilePendingAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	current, err := st.GetExecutionAttempt(attempt.ID)
	if err != nil || current.State != "placed" {
		t.Fatalf("attempt=%+v err=%v", current, err)
	}
	operation, err := st.GetOperation(operationID)
	if err != nil || operation.Status != "approved" {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
}
