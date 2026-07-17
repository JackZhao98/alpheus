package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestM29OrderFillPersistencePostgres(t *testing.T) {
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

	operationID, reservationID, attemptID, orderID := NewID(), NewID(), NewID(), NewID()
	clientOrderID := NewID()
	if err := s.InsertOperation(operationID, "m29-test", "A", "auto_approved",
		map[string]any{"action": "close", "shadow": false}, map[string]any{"class": "A"}, nil); err != nil {
		t.Fatal(err)
	}
	err = s.WithProposalLock(nil, false, nil, func(gate OperationGate) error {
		if err := gate.InsertCloseReservation(CloseReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", Symbol: "M29",
			OriginalQty: 2, RemainingQty: 2, State: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1,
			CloseReservationID: reservationID, Intent: "place",
			ClientOrderID: clientOrderID, State: "placed", Qty: 2,
			Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientOrderID, Ledger: "live", Symbol: "M29",
			Side: "sell", Kind: "equity", Multiplier: 1, Qty: 2,
			Limit: units.MustMicros("10"), State: "new",
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	fillOne := FillInput{
		BrokerFillID: "m29-fill-1", Qty: 1, Price: units.MustMicros("10"), TS: now,
	}
	partial := OrderUpdate{
		ExecutionAttemptID: attemptID, BrokerOrderID: "m29-order-1",
		State: "partially_filled", FilledQty: 1, Fills: []FillInput{fillOne},
	}
	if err := s.ApplyOrderUpdate(partial); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOrderUpdate(partial); err != nil {
		t.Fatalf("identical fill replay: %v", err)
	}
	assertM29Counts(t, s, orderID, reservationID, 1, 1, "held", "partially_filled")

	fillTwo := FillInput{
		BrokerFillID: "m29-fill-2", Qty: 1, Price: units.MustMicros("10.01"), TS: now.Add(time.Second),
	}
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: attemptID, BrokerOrderID: "m29-order-1",
		State: "filled", FilledQty: 2, Fills: []FillInput{fillOne, fillTwo},
	}); err != nil {
		t.Fatal(err)
	}
	assertM29Counts(t, s, orderID, reservationID, 2, 0, "released", "filled")

	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: attemptID, BrokerOrderID: "m29-order-1",
		State: "submitted", FilledQty: 2, Fills: []FillInput{fillOne, fillTwo},
	}); !errors.Is(err, ErrIllegalOrderTransition) {
		t.Fatalf("illegal transition err=%v", err)
	}
	assertM29Counts(t, s, orderID, reservationID, 2, 0, "released", "filled")

	conflicting := fillOne
	conflicting.Price = units.MustMicros("9.99")
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: attemptID, BrokerOrderID: "m29-order-1",
		State: "filled", FilledQty: 2, Fills: []FillInput{conflicting, fillTwo},
	}); !errors.Is(err, ErrFillIntegrity) {
		t.Fatalf("fill collision err=%v", err)
	}
	assertM29Counts(t, s, orderID, reservationID, 2, 0, "released", "filled")
	halted, reason, err := s.LoadGlobalHalt()
	if err != nil || !halted || reason == "" {
		t.Fatalf("halted=%v reason=%q err=%v", halted, reason, err)
	}
}

func TestM29BackfillsM28PlaceAttemptsPostgres(t *testing.T) {
	databaseURL := os.Getenv("ALPHEUS_TEST_BACKFILL_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_BACKFILL_DATABASE_URL is not set")
	}
	fullDir := os.Getenv("ALPHEUS_TEST_MIGRATIONS_DIR")
	if fullDir == "" {
		fullDir = "../../../db/migrations"
	}
	preBackfillDir := t.TempDir()
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "0001_") && !strings.HasPrefix(entry.Name(), "0002_") &&
			!strings.HasPrefix(entry.Name(), "0003_") && !strings.HasPrefix(entry.Name(), "0004_") {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(fullDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(preBackfillDir, entry.Name()), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(Config{
		URL: databaseURL, MigrationsDir: preBackfillDir,
		Timeout: 3 * time.Second, MarketTZ: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID, attemptID, clientID := NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "m28-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "underlying": "BACKFILL", "side": "buy",
		"kind": "equity", "multiplier": 1,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithProposalLock(nil, false, nil, func(gate OperationGate) error {
		return gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, Intent: "place",
			ClientOrderID: clientID, State: "pending", Qty: 3,
			Limit: units.MustMicros("12.34"),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE execution_attempt SET state='placed',broker_order_id='m28-broker' WHERE id=$1`, attemptID); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = Open(Config{
		URL: databaseURL, MigrationsDir: fullDir,
		Timeout: 3 * time.Second, MarketTZ: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	order, err := s.GetOrderByAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if order.ID != attemptID || order.BrokerOrderID != "m28-broker" || order.State != "submitted" ||
		order.Symbol != "BACKFILL" || order.Kind != "equity" || order.Multiplier != 1 ||
		order.Qty != 3 || order.Limit != units.MustMicros("12.34") {
		t.Fatalf("backfilled order=%+v", order)
	}
}

func assertM29Counts(t *testing.T, s *Store, orderID, reservationID string, wantFills int, wantRemaining int64, wantReservationState, wantOrderState string) {
	t.Helper()
	var fills int
	if err := s.DB.QueryRow(`SELECT count(*) FROM fills WHERE order_id=$1`, orderID).Scan(&fills); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.GetCloseReservation(reservationID)
	if err != nil {
		t.Fatal(err)
	}
	var orderState string
	if err := s.DB.QueryRow(`SELECT state FROM orders WHERE id=$1`, orderID).Scan(&orderState); err != nil {
		t.Fatal(err)
	}
	if fills != wantFills || int64(reservation.RemainingQty) != wantRemaining ||
		reservation.State != wantReservationState || orderState != wantOrderState {
		t.Fatalf("fills=%d reservation=%+v order_state=%s", fills, reservation, orderState)
	}
}
