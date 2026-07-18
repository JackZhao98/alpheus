package store

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestLiveExecutionGateSerializesUnknownAndOneReplayPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	const workers = 20
	attemptIDs := make([]string, workers)
	for index := range attemptIDs {
		operationID := NewID()
		attemptIDs[index] = NewID()
		if err := s.InsertOperation(operationID, "m11-live-gate", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": false, "symbol": "SPY", "kind": "equity", "side": "buy",
		}, map[string]any{"class": "B"}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`INSERT INTO execution_attempt
			(id,operation_id,seq,intent,client_order_id,state,qty,limit_micros)
			VALUES ($1,$2,1,'place',$3,'pending',$4,$5)`, attemptIDs[index], operationID, NewID(),
			int64(units.MustQty("1")), int64(units.MustMicros("1"))); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan *ExecutionAttempt, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for _, attemptID := range attemptIDs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claimed, err := s.ClaimPendingAttemptLive(attemptID, "barrier-worker")
			results <- claimed
			errorsCh <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var winner *ExecutionAttempt
	claimedCount := 0
	for claimed := range results {
		if claimed != nil {
			claimedCount++
			winner = claimed
		}
	}
	if claimedCount != 1 || winner == nil {
		t.Fatalf("claimed=%d winner=%+v", claimedCount, winner)
	}
	gate, err := s.GetLiveExecutionGate()
	if err != nil || gate.ActiveAttemptID != winner.ID || gate.UnknownAttemptID != "" {
		t.Fatalf("active gate=%+v err=%v", gate, err)
	}
	canonical, err := json.Marshal(map[string]any{"kind": "equity", "symbol": "SPY"})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	prepared, err := s.PrepareAttemptProviderIntent(winner.ID, winner.Attempt, "account-1", canonical, digest[:])
	if err != nil || !prepared {
		t.Fatalf("prepared=%v err=%v", prepared, err)
	}
	marked, err := s.MarkAttemptSent(winner.ID, winner.Attempt, false)
	if err != nil || !marked {
		t.Fatalf("marked=%v err=%v", marked, err)
	}
	resolved, err := s.ResolveAttempt(winner.ID, winner.Attempt, AttemptResolution{
		State: "unknown", LastError: "lost response", ProviderErrorCode: "call_failed",
	})
	if err != nil || !resolved {
		t.Fatalf("unknown resolved=%v err=%v", resolved, err)
	}
	gate, _ = s.GetLiveExecutionGate()
	if gate.ActiveAttemptID != "" || gate.UnknownAttemptID != winner.ID {
		t.Fatalf("unknown gate=%+v", gate)
	}
	for _, attemptID := range attemptIDs {
		if attemptID == winner.ID {
			continue
		}
		claimed, claimErr := s.ClaimPendingAttemptLive(attemptID, "blocked-worker")
		if claimErr != nil || claimed != nil {
			t.Fatalf("latched claim=%+v err=%v", claimed, claimErr)
		}
	}
	current, err := s.GetExecutionAttempt(winner.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := s.ClaimRecoverableAttemptLive(winner.ID, "recovery", "unknown", current.Attempt, time.Now())
	if err != nil || recovery == nil {
		t.Fatalf("recovery=%+v err=%v", recovery, err)
	}
	// A worker or its adoption transaction can die after claiming an unknown
	// attempt. The durable unknown latch must remain engaged, but the same
	// fenced attempt must become recoverable again after the claim lease.
	updated, resolveErr := s.ResolveAttempt(recovery.ID, recovery.Attempt, AttemptResolution{
		State: "placed", BrokerOrderID: NewID(),
		OrderUpdate: &OrderUpdate{
			ExecutionAttemptID: recovery.ID, BrokerOrderID: NewID(), State: "submitted",
		},
	})
	if resolveErr == nil || updated {
		t.Fatalf("injected adoption rollback updated=%v err=%v", updated, resolveErr)
	}
	current, err = s.GetExecutionAttempt(recovery.ID)
	if err != nil || current.State != "claimed" || current.Attempt != recovery.Attempt {
		t.Fatalf("rolled-back recovery=%+v err=%v", current, err)
	}
	gate, err = s.GetLiveExecutionGate()
	if err != nil || gate.UnknownAttemptID != recovery.ID || gate.ActiveAttemptID != "" {
		t.Fatalf("rolled-back gate=%+v err=%v", gate, err)
	}
	recovery, err = s.ClaimRecoverableAttemptLive(
		current.ID, "recovery-after-crash", "claimed", current.Attempt, time.Now().Add(time.Second),
	)
	if err != nil || recovery == nil {
		t.Fatalf("reclaim unknown worker=%+v err=%v", recovery, err)
	}
	gate, err = s.GetLiveExecutionGate()
	if err != nil || gate.UnknownAttemptID != recovery.ID || gate.ActiveAttemptID != "" {
		t.Fatalf("reclaimed gate=%+v err=%v", gate, err)
	}
	marked, err = s.MarkAttemptSent(recovery.ID, recovery.Attempt, true)
	if err != nil || !marked {
		t.Fatalf("replay marked=%v err=%v", marked, err)
	}
	marked, err = s.MarkAttemptSent(recovery.ID, recovery.Attempt, true)
	if err != nil || marked {
		t.Fatalf("second replay marked=%v err=%v", marked, err)
	}
	resolved, err = s.ResolveAttempt(recovery.ID, recovery.Attempt, AttemptResolution{
		State: "settled", BrokerOrderID: NewID(), CandidateBrokerOrderID: NewID(),
	})
	if err != nil || !resolved {
		t.Fatalf("settled=%v err=%v", resolved, err)
	}
	gate, _ = s.GetLiveExecutionGate()
	if gate.ActiveAttemptID != "" || gate.UnknownAttemptID != "" {
		t.Fatalf("cleared gate=%+v", gate)
	}
}

func TestTradeGrantCanaryBarrierPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	const workers = 20
	marketDay := time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC)
	cap := units.MustMicros("35")
	operationIDs := make([]string, workers)
	for index := range operationIDs {
		operationIDs[index] = NewID()
		if err := s.InsertOperation(operationIDs[index], "m11-barrier", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": false, "symbol": "M11", "kind": "equity",
		}, map[string]any{"class": "B"}, nil); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errorsCh := make(chan error, workers)
	var granted atomic.Int32
	var wait sync.WaitGroup
	for _, operationID := range operationIDs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			err := s.WithLedgerLock(false, marketDay, func(gate OperationGate) error {
				usage, err := gate.TradeGrantUsage("live", marketDay, operationID)
				if err != nil {
					return err
				}
				if usage.HasLegacyUnknown || usage.AuthorizedRisk > cap-units.MustMicros("35") {
					return nil
				}
				if err := gate.InsertTradeGrant(TradeGrant{
					OperationID: operationID, Ledger: "live", MarketDay: marketDay,
					AuthorizedRisk: units.MustMicros("35"), RiskSource: "computed",
				}); err != nil {
					return err
				}
				granted.Add(1)
				return nil
			})
			errorsCh <- err
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
	if granted.Load() != 1 {
		t.Fatalf("granted=%d, want 1", granted.Load())
	}
	var rows int
	var risk int64
	if err := s.DB.QueryRow(`SELECT count(*),COALESCE(sum(authorized_risk_micros),0)
		FROM trade_grant WHERE ledger='live' AND market_day=$1`, marketDay).Scan(&rows, &risk); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || units.Micros(risk) != cap {
		t.Fatalf("rows=%d risk=%d, want one grant at %d", rows, risk, cap)
	}
}

func TestTradeGrantUsagePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	marketDay := time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC)
	computedOne := seedM11Grant(t, s, "live", marketDay, "computed", units.MustMicros("10"))
	seedM11Grant(t, s, "live", marketDay, "computed", units.MustMicros("25"))
	seedM11Grant(t, s, "shadow", marketDay, "computed", units.MustMicros("99"))
	seedM11Grant(t, s, "live", marketDay.AddDate(0, 0, 1), "computed", units.MustMicros("88"))

	assertUsage := func(exclude string, wantRisk units.Micros, wantLegacy bool, wantCount int) {
		t.Helper()
		err := s.WithLedgerLock(false, marketDay, func(gate OperationGate) error {
			usage, err := gate.TradeGrantUsage("live", marketDay, exclude)
			if err != nil {
				return err
			}
			if usage.AuthorizedRisk != wantRisk || usage.HasLegacyUnknown != wantLegacy || usage.GrantCount != wantCount {
				t.Fatalf("usage=%+v, want risk=%s legacy=%v count=%d", usage, wantRisk, wantLegacy, wantCount)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	assertUsage("", units.MustMicros("35"), false, 2)
	assertUsage(computedOne, units.MustMicros("25"), false, 1)
	legacy := seedM11Grant(t, s, "live", marketDay, "legacy_unknown", 0)
	assertUsage("", units.MustMicros("35"), true, 3)
	assertUsage(legacy, units.MustMicros("35"), false, 2)
}

func openM11IntegrationStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("ALPHEUS_TEST_M3A_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_M3A_DATABASE_URL is not set")
	}
	migrationsDir := os.Getenv("ALPHEUS_TEST_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "../../../db/migrations"
	}
	s, err := Open(Config{
		URL: databaseURL, MigrationsDir: migrationsDir,
		Timeout: 3 * time.Second, MarketTZ: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func seedM11Grant(t *testing.T, s *Store, ledger string, marketDay time.Time, source string, amount units.Micros) string {
	t.Helper()
	operationID := NewID()
	shadow := ledger == "shadow"
	if err := s.InsertOperation(operationID, "m11-integration", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": shadow, "symbol": "M11", "kind": "equity",
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	err := s.WithLedgerLock(shadow, marketDay, func(gate OperationGate) error {
		return gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: ledger, MarketDay: marketDay,
			AuthorizedRisk: amount, RiskSource: source,
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	return operationID
}
