package store

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestLiveCanaryRevisionRaiseRequiresDurableCleanEvidencePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	marketDay := time.Date(2026, time.July, 20, 0, 0, 0, 0, time.UTC)
	if err := s.RecordLiveCanaryRevision(units.MustMicros("35"), 2, marketDay); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLiveCanaryRevision(units.MustMicros("35"), 2, marketDay); err != nil {
		t.Fatal(err)
	}
	assertCanaryRevisionCounts(t, s, 1, 1)

	seedLiveCanaryDay(t, s, marketDay.AddDate(0, 0, -1))
	if err := s.RecordLiveCanaryRevision(units.MustMicros("70"), 1, marketDay); !errors.Is(err, ErrLiveCanaryRaiseUnsafe) {
		t.Fatalf("one clean day bypassed previous two-day policy: %v", err)
	}
	seedLiveCanaryDay(t, s, marketDay.AddDate(0, 0, -2))

	const workers = 20
	start := make(chan struct{})
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errorsCh <- s.RecordLiveCanaryRevision(units.MustMicros("70"), 1, marketDay)
		}()
	}
	close(start)
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertCanaryRevisionCounts(t, s, 2, 2)

	operationID, attemptID := NewID(), NewID()
	if err := s.InsertOperation(operationID, "m11-config", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO execution_attempt
		(id,operation_id,seq,intent,client_order_id,state,qty,limit_micros)
		VALUES ($1,$2,1,'place',$3,'unknown',$4,$5)`,
		attemptID, operationID, NewID(), int64(units.MustQty("1")), int64(units.MustMicros("1"))); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLiveCanaryRevision(units.MustMicros("80"), 2, marketDay); !errors.Is(err, ErrLiveCanaryRaiseUnsafe) {
		t.Fatalf("unknown attempt did not block raise: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE execution_attempt SET state='failed',resolved_at=now() WHERE id=$1`, attemptID); err != nil {
		t.Fatal(err)
	}

	divergenceDay := marketDay.AddDate(0, 0, -1)
	payload, err := json.Marshal(map[string]any{
		"ledger": "live", "market_day": divergenceDay,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO events (kind,payload) VALUES ('pnl_divergence',$1)`, payload); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLiveCanaryRevision(units.MustMicros("80"), 2, marketDay); !errors.Is(err, ErrLiveCanaryRaiseUnsafe) {
		t.Fatalf("PnL divergence did not block raise: %v", err)
	}
	if _, err := s.DB.Exec(`DELETE FROM events WHERE kind='pnl_divergence'`); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLiveCanaryRevision(units.MustMicros("80"), 2, marketDay); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordLiveCanaryRevision(units.MustMicros("10"), 3, marketDay); err != nil {
		t.Fatalf("tightening should not need clean-day evidence: %v", err)
	}
	assertCanaryRevisionCounts(t, s, 4, 4)
}

func seedLiveCanaryDay(t *testing.T, s *Store, marketDay time.Time) {
	t.Helper()
	if _, err := s.DB.Exec(`INSERT INTO day_open (market_day,ledger,equity_micros)
		VALUES ($1,'live',$2)`, marketDay, int64(units.MustMicros("300"))); err != nil {
		t.Fatal(err)
	}
}

func assertCanaryRevisionCounts(t *testing.T, s *Store, revisions, events int) {
	t.Helper()
	var revisionCount, eventCount int
	if err := s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM live_canary_revision),
		(SELECT count(*) FROM events WHERE kind='live_canary_revision_recorded')`).Scan(
		&revisionCount, &eventCount,
	); err != nil {
		t.Fatal(err)
	}
	if revisionCount != revisions || eventCount != events {
		t.Fatalf("revisions=%d events=%d, want %d/%d", revisionCount, eventCount, revisions, events)
	}
}
