package store

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReviewLockPostgresIsAtomicAndRollbackSafe(t *testing.T) {
	databaseURL := os.Getenv("ALPHEUS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_DATABASE_URL is not set")
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
	defer s.DB.Close()

	operationID := NewID()
	if err := s.InsertOperation(operationID, "m4-postgres", "C", "pending_review",
		map[string]any{"action": "open", "shadow": false}, map[string]any{"class": "C"}, nil); err != nil {
		t.Fatal(err)
	}
	const workers = 20
	start := make(chan struct{})
	var callbacks atomic.Int32
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.WithReviewLock(operationID, func(gate OperationGate, _ *OperationRow) error {
				callbacks.Add(1)
				return gate.SetOperationStatus(operationID, "approved", map[string]string{"decision": "approved"})
			})
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrOperationNotPending):
				conflicts.Add(1)
			default:
				t.Errorf("review: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if callbacks.Load() != 1 || successes.Load() != 1 || conflicts.Load() != workers-1 {
		t.Fatalf("callbacks=%d successes=%d conflicts=%d",
			callbacks.Load(), successes.Load(), conflicts.Load())
	}
	row, err := s.GetOperation(operationID)
	if err != nil || row.Status != "approved" {
		t.Fatalf("row=%+v err=%v", row, err)
	}

	rollbackID := NewID()
	if err := s.InsertOperation(rollbackID, "m4-postgres", "C", "pending_review",
		map[string]any{"action": "open", "shadow": false}, map[string]any{"class": "C"}, nil); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("approval gate rejected")
	rollbackReservationID := NewID()
	rollbackAttemptID := NewID()
	rollbackOrderID := NewID()
	rollbackClientID := NewID()
	err = s.WithReviewLock(rollbackID, func(gate OperationGate, _ *OperationRow) error {
		if err := gate.SetOperationStatus(rollbackID, "approved", map[string]string{"decision": "approved"}); err != nil {
			return err
		}
		if err := gate.InsertEvent("m4_should_rollback", map[string]string{"operation_id": rollbackID}); err != nil {
			return err
		}
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: rollbackID, Ledger: "live", MarketDay: time.Now().UTC(),
			AuthorizedRisk: 1_000_000, RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: rollbackReservationID, OperationID: rollbackID, Ledger: "live",
			MarketDay: time.Now().UTC(), Symbol: "ROLLBACK", Kind: "equity",
			OriginalQty: 1_000_000, RemainingQty: 1_000_000,
			OriginalRisk: 1_000_000, RemainingRisk: 1_000_000,
			OriginalCash: 1_000_000, RemainingCash: 1_000_000, ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: rollbackAttemptID, OperationID: rollbackID, Seq: 1,
			OpenReservationID: rollbackReservationID, Intent: "place",
			ClientOrderID: rollbackClientID, State: "pending", Qty: 1_000_000, Limit: 1_000_000,
		}); err != nil {
			return err
		}
		if err := gate.InsertOrder(Order{
			ID: rollbackOrderID, OperationID: rollbackID, ExecutionAttemptID: rollbackAttemptID,
			ClientOrderID: rollbackClientID, Ledger: "live", Symbol: "ROLLBACK",
			Side: "buy", Kind: "equity", Multiplier: 1, Qty: 1_000_000,
			Limit: 1_000_000, State: "new",
		}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error=%v", err)
	}
	row, err = s.GetOperation(rollbackID)
	if err != nil || row.Status != "pending_review" {
		t.Fatalf("rolled-back row=%+v err=%v", row, err)
	}
	var events int
	if err := s.DB.QueryRow(`SELECT count(*) FROM events
		WHERE kind='m4_should_rollback' AND payload->>'operation_id'=$1`, rollbackID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("rolled-back events=%d", events)
	}
	var entitlements int
	if err := s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM trade_grant WHERE operation_id=$1) +
		(SELECT count(*) FROM open_reservation WHERE operation_id=$1) +
		(SELECT count(*) FROM execution_attempt WHERE operation_id=$1) +
		(SELECT count(*) FROM orders WHERE operation_id=$1)`, rollbackID).Scan(&entitlements); err != nil {
		t.Fatal(err)
	}
	if entitlements != 0 {
		t.Fatalf("rolled-back entitlement rows=%d", entitlements)
	}

	proposalID := NewID()
	proposalReservationID := NewID()
	proposalAttemptID := NewID()
	proposalOrderID := NewID()
	proposalClientID := NewID()
	proposalRollback := errors.New("market day advanced")
	err = s.WithProposalLock(nil, false, true, func(gate OperationGate) error {
		if err := gate.InsertEvent("proposal_should_rollback", map[string]string{"operation_id": proposalID}); err != nil {
			return err
		}
		if err := gate.InsertOperation(proposalID, "clock-test", "B", "auto_approved",
			map[string]any{"action": "open", "shadow": false, "symbol": "CLOCK"},
			map[string]any{"class": "B"}, nil); err != nil {
			return err
		}
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: proposalID, Ledger: "live", MarketDay: time.Now().UTC(),
			AuthorizedRisk: 1_000_000, RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: proposalReservationID, OperationID: proposalID, Ledger: "live",
			MarketDay: time.Now().UTC(), Symbol: "CLOCK", Kind: "equity",
			OriginalQty: 1_000_000, RemainingQty: 1_000_000,
			OriginalRisk: 1_000_000, RemainingRisk: 1_000_000,
			OriginalCash: 1_000_000, RemainingCash: 1_000_000, ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: proposalAttemptID, OperationID: proposalID, Seq: 1,
			OpenReservationID: proposalReservationID, Intent: "place",
			ClientOrderID: proposalClientID, State: "pending", Qty: 1_000_000, Limit: 1_000_000,
		}); err != nil {
			return err
		}
		if err := gate.InsertOrder(Order{
			ID: proposalOrderID, OperationID: proposalID, ExecutionAttemptID: proposalAttemptID,
			ClientOrderID: proposalClientID, Ledger: "live", Symbol: "CLOCK",
			Side: "buy", Kind: "equity", Multiplier: 1, Qty: 1_000_000,
			Limit: 1_000_000, State: "new",
		}); err != nil {
			return err
		}
		return proposalRollback
	})
	if !errors.Is(err, proposalRollback) {
		t.Fatalf("proposal rollback error=%v", err)
	}
	var proposalRows int
	if err := s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM operations WHERE id=$1) +
		(SELECT count(*) FROM trade_grant WHERE operation_id=$1) +
		(SELECT count(*) FROM open_reservation WHERE operation_id=$1) +
		(SELECT count(*) FROM execution_attempt WHERE operation_id=$1) +
		(SELECT count(*) FROM orders WHERE operation_id=$1) +
		(SELECT count(*) FROM events WHERE kind='proposal_should_rollback'
		 AND payload->>'operation_id'=$1::text)`, proposalID).Scan(&proposalRows); err != nil {
		t.Fatal(err)
	}
	if proposalRows != 0 {
		t.Fatalf("proposal rollback left %d staged rows", proposalRows)
	}
}
