package store

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestM3AInvalidExecutionEntitlementCannotBeClaimedPostgres(t *testing.T) {
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
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	if err := s.ActivateM3A(M3AActivationSnapshot{
		Equity: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
	}); err != nil {
		t.Fatal(err)
	}

	operationID, reservationID, attemptID, orderID, clientID := NewID(), NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "invalid-entitlement-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "NO-GRANT", "kind": "equity",
		"side": "buy", "qty": 1, "multiplier": 1,
		"derived_max_risk": 10, "required_cash": 10,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live",
			MarketDay: time.Now(), Symbol: "NO-GRANT", Kind: "equity",
			OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"),
			OriginalRisk: units.MustMicros("10"), RemainingRisk: units.MustMicros("10"),
			OriginalCash: units.MustMicros("10"), RemainingCash: units.MustMicros("10"),
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1,
			OpenReservationID: reservationID, Intent: "place", ClientOrderID: clientID,
			State: "pending", Qty: units.MustQty("1"), Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "NO-GRANT", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
			Limit: units.MustMicros("10"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.ClaimPendingAttempt(attemptID, "invalid-entitlement-test", 30*time.Second)
	if !errors.Is(err, ErrFillIntegrity) || claimed != nil {
		t.Fatalf("invalid entitlement claim: claimed=%+v err=%v", claimed, err)
	}
	attempt, err := s.GetExecutionAttempt(attemptID)
	if err != nil || attempt.State != "pending" || attempt.Attempt != 0 {
		t.Fatalf("invalid claim mutated attempt: attempt=%+v err=%v", attempt, err)
	}
	halted, reason, err := s.LoadGlobalHalt()
	if err != nil || !halted || reason == "" {
		t.Fatalf("invalid entitlement did not halt: halted=%v reason=%q err=%v", halted, reason, err)
	}
}

func TestShadowFullCloseDeletesPositionPostgres(t *testing.T) {
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
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	if err := s.ActivateM3A(M3AActivationSnapshot{
		Equity: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO shadow_positions
		(symbol,kind,multiplier,qty,updated_at) VALUES ('FULL-CLOSE','equity',1,$1,now())`,
		int64(units.MustQty("1"))); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	order := &Order{Symbol: "FULL-CLOSE", Kind: "equity", Multiplier: 1, Ledger: "shadow"}
	fill := FillInput{Qty: units.MustQty("1"), Price: units.MustMicros("10")}
	if err := applyShadowCloseFill(ctx, tx, order, fill); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var positions int
	if err := s.DB.QueryRow(`SELECT count(*) FROM shadow_positions WHERE symbol='FULL-CLOSE'`).Scan(&positions); err != nil {
		t.Fatal(err)
	}
	if positions != 0 {
		t.Fatalf("fully closed shadow position count=%d", positions)
	}
}

func TestM3AExposureTransferAndFIFOAllocationPostgres(t *testing.T) {
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
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	if err := s.ActivateM3A(M3AActivationSnapshot{
		Equity: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
	}); err != nil {
		t.Fatal(err)
	}

	openOperationID, openReservationID, openAttemptID, openOrderID := NewID(), NewID(), NewID(), NewID()
	clientID := NewID()
	if err := s.InsertOperation(openOperationID, "m3a-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M3A", "kind": "equity",
		"side": "buy", "qty": 3, "multiplier": 1,
		"derived_max_risk": 30, "required_cash": 30,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	err = s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: openOperationID, Ledger: "live", MarketDay: time.Now(),
			AuthorizedRisk: units.MustMicros("30"), RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: openReservationID, OperationID: openOperationID, Ledger: "live",
			MarketDay: time.Now(), Symbol: "M3A", Kind: "equity",
			OriginalQty: units.MustQty("3"), RemainingQty: units.MustQty("3"),
			OriginalRisk: units.MustMicros("30"), RemainingRisk: units.MustMicros("30"),
			OriginalCash: units.MustMicros("30"), RemainingCash: units.MustMicros("30"),
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: openAttemptID, OperationID: openOperationID, Seq: 1,
			OpenReservationID: openReservationID, Intent: "place",
			ClientOrderID: clientID, State: "placed", Qty: units.MustQty("3"),
			Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: openOrderID, OperationID: openOperationID, ExecutionAttemptID: openAttemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "M3A", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("3"),
			Limit: units.MustMicros("10"), State: "new",
		})
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	openFill := FillInput{
		BrokerFillID: "m3a-open-fill-1", Qty: units.MustQty("1"),
		Price: units.MustMicros("10"), TS: now,
	}
	partial := OrderUpdate{
		ExecutionAttemptID: openAttemptID, BrokerOrderID: "m3a-open-order",
		State: "partially_filled", FilledQty: units.MustQty("1"), Fills: []FillInput{openFill},
	}
	if err := s.ApplyOrderUpdate(partial); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOrderUpdate(partial); err != nil {
		t.Fatalf("idempotent partial replay: %v", err)
	}
	reservation, err := s.GetOpenReservation(openReservationID)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := s.LedgerResources("live", "")
	if err != nil {
		t.Fatal(err)
	}
	if reservation.RemainingQty != units.MustQty("2") || reservation.RemainingRisk != units.MustMicros("20") ||
		reservation.RemainingCash != units.MustMicros("20") || resources.OpenRisk != units.MustMicros("30") {
		t.Fatalf("partial reservation=%+v resources=%+v", reservation, resources)
	}

	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: openAttemptID, BrokerOrderID: "m3a-open-order",
		State: "cancelled", FilledQty: units.MustQty("1"), Fills: []FillInput{openFill},
	}); err != nil {
		t.Fatal(err)
	}
	reservation, err = s.GetOpenReservation(openReservationID)
	if err != nil {
		t.Fatal(err)
	}
	resources, err = s.LedgerResources("live", "")
	if err != nil {
		t.Fatal(err)
	}
	if reservation.ResourceState != "released" || reservation.RemainingQty != units.MustQty("2") ||
		reservation.RemainingRisk != 0 || resources.OpenRisk != units.MustMicros("10") {
		t.Fatalf("cancelled reservation=%+v resources=%+v", reservation, resources)
	}
	granted, err := s.HasTradeGrant(openOperationID)
	if err != nil || !granted {
		t.Fatalf("grant restored or missing: granted=%v err=%v", granted, err)
	}
	secondOpenOperationID, secondOpenReservationID, secondOpenAttemptID, secondOpenOrderID := NewID(), NewID(), NewID(), NewID()
	secondClientID := NewID()
	if err := s.InsertOperation(secondOpenOperationID, "m3a-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M3A", "kind": "equity",
		"side": "buy", "qty": 1, "multiplier": 1,
		"derived_max_risk": 11, "required_cash": 11,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: secondOpenOperationID, Ledger: "live", MarketDay: time.Now(),
			AuthorizedRisk: units.MustMicros("11"), RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: secondOpenReservationID, OperationID: secondOpenOperationID, Ledger: "live",
			MarketDay: time.Now(), Symbol: "M3A", Kind: "equity",
			OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"),
			OriginalRisk: units.MustMicros("11"), RemainingRisk: units.MustMicros("11"),
			OriginalCash: units.MustMicros("11"), RemainingCash: units.MustMicros("11"),
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: secondOpenAttemptID, OperationID: secondOpenOperationID, Seq: 1,
			OpenReservationID: secondOpenReservationID, Intent: "place",
			ClientOrderID: secondClientID, State: "placed", Qty: units.MustQty("1"),
			Limit: units.MustMicros("11"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: secondOpenOrderID, OperationID: secondOpenOperationID,
			ExecutionAttemptID: secondOpenAttemptID, ClientOrderID: secondClientID,
			Ledger: "live", Symbol: "M3A", Side: "buy", Kind: "equity",
			Multiplier: 1, Qty: units.MustQty("1"), Limit: units.MustMicros("11"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: secondOpenAttemptID, BrokerOrderID: "m3a-open-order-2",
		State: "filled", FilledQty: units.MustQty("1"), Fills: []FillInput{{
			BrokerFillID: "m3a-open-fill-2", Qty: units.MustQty("1"),
			Price: units.MustMicros("11"), TS: now.Add(500 * time.Millisecond),
		}},
	}); err != nil {
		t.Fatal(err)
	}

	closeOperationID, closeReservationID, closeAttemptID, closeOrderID := NewID(), NewID(), NewID(), NewID()
	closeClientID := NewID()
	if err := s.InsertOperation(closeOperationID, "m3a-test", "A", "auto_approved", map[string]any{
		"action": "close", "shadow": false, "symbol": "M3A", "kind": "equity",
		"side": "sell", "qty": 2, "multiplier": 1,
	}, map[string]any{"class": "A"}, nil); err != nil {
		t.Fatal(err)
	}
	err = s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		if err := gate.LockLedgerSymbol("live", "M3A"); err != nil {
			return err
		}
		if err := gate.InsertCloseReservation(CloseReservation{
			ID: closeReservationID, OperationID: closeOperationID, Ledger: "live", Symbol: "M3A",
			OriginalQty: units.MustQty("2"), RemainingQty: units.MustQty("2"), State: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: closeAttemptID, OperationID: closeOperationID, Seq: 1,
			CloseReservationID: closeReservationID, Intent: "place",
			ClientOrderID: closeClientID, State: "placed", Qty: units.MustQty("2"),
			Limit: units.MustMicros("9.9"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: closeOrderID, OperationID: closeOperationID, ExecutionAttemptID: closeAttemptID,
			ClientOrderID: closeClientID, Ledger: "live", Symbol: "M3A", Side: "sell",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("2"),
			Limit: units.MustMicros("9.9"), State: "new",
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	closeFill := FillInput{
		BrokerFillID: "m3a-close-fill-1", Qty: units.MustQty("2"),
		Price: units.MustMicros("9.9"), TS: now.Add(time.Second),
	}
	closeUpdate := OrderUpdate{
		ExecutionAttemptID: closeAttemptID, BrokerOrderID: "m3a-close-order",
		State: "filled", FilledQty: units.MustQty("2"), Fills: []FillInput{closeFill},
	}
	if err := s.ApplyOrderUpdate(closeUpdate); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOrderUpdate(closeUpdate); err != nil {
		t.Fatalf("idempotent close replay: %v", err)
	}
	var allocations, fills int
	if err := s.DB.QueryRow(`SELECT count(*) FROM exposure_close_allocation`).Scan(&allocations); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM fills`).Scan(&fills); err != nil {
		t.Fatal(err)
	}
	resources, err = s.LedgerResources("live", "")
	if err != nil {
		t.Fatal(err)
	}
	if allocations != 2 || fills != 3 || resources.OpenRisk != 0 {
		t.Fatalf("allocations=%d fills=%d resources=%+v", allocations, fills, resources)
	}

	for i := 0; i < 6; i++ {
		settleShadowOpen(t, s, fmt.Sprintf("SHADOW-%d", i))
	}
	shadowResources, err := s.LedgerResources("shadow", "")
	if err != nil {
		t.Fatal(err)
	}
	liveResources, err := s.LedgerResources("live", "")
	if err != nil {
		t.Fatal(err)
	}
	shadowPositions, err := s.shadowPositionsForTest()
	if err != nil {
		t.Fatal(err)
	}
	shadowTrades, err := s.CountTradesForDay(true, time.Now(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if shadowResources.OpenRisk != units.MustMicros("6") || liveResources.OpenRisk != 0 ||
		len(shadowPositions) != 6 || shadowTrades != 6 {
		t.Fatalf("shadow_resources=%+v live_resources=%+v positions=%v trades=%d",
			shadowResources, liveResources, shadowPositions, shadowTrades)
	}
}

func TestStableLedgerLockSerializesLiveAcrossConnectionsPostgres(t *testing.T) {
	databaseURL := os.Getenv("ALPHEUS_TEST_M3A_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_M3A_DATABASE_URL is not set")
	}
	migrationsDir := os.Getenv("ALPHEUS_TEST_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "../../../db/migrations"
	}
	first, err := Open(Config{
		URL: databaseURL, MigrationsDir: migrationsDir,
		Timeout: 3 * time.Second, MarketTZ: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.DB.Close()
	second, err := Open(Config{
		URL: databaseURL, MigrationsDir: migrationsDir,
		Timeout: 3 * time.Second, MarketTZ: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer second.DB.Close()

	acquired := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithLedgerLock(false, func(OperationGate) error {
			close(acquired)
			<-release
			return nil
		})
	}()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("first live ledger lock was not acquired")
	}

	secondAcquired := make(chan struct{})
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.WithLedgerLock(false, func(OperationGate) error {
			close(secondAcquired)
			return nil
		})
	}()
	select {
	case <-secondAcquired:
		close(release)
		t.Fatal("second connection acquired the live ledger lock concurrently")
	case <-time.After(150 * time.Millisecond):
	}

	shadowDone := make(chan error, 1)
	go func() {
		shadowDone <- second.WithLedgerLock(true, func(OperationGate) error { return nil })
	}()
	select {
	case err := <-shadowDone:
		if err != nil {
			close(release)
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("shadow ledger was blocked by the live ledger key")
	}

	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondAcquired:
	case <-time.After(time.Second):
		t.Fatal("second connection did not acquire after live lock release")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
}

func settleShadowOpen(t *testing.T, s *Store, symbol string) {
	t.Helper()
	operationID, reservationID, attemptID, orderID := NewID(), NewID(), NewID(), NewID()
	clientID := "shadow:" + attemptID
	if err := s.InsertOperation(operationID, "paper-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": true, "symbol": symbol, "kind": "equity",
		"side": "buy", "qty": 1, "multiplier": 1,
		"derived_max_risk": 1, "required_cash": 1,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(true, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "shadow", MarketDay: time.Now(),
			AuthorizedRisk: units.MustMicros("1"), RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "shadow",
			MarketDay: time.Now(), Symbol: symbol, Kind: "equity",
			OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"),
			OriginalRisk: units.MustMicros("1"), RemainingRisk: units.MustMicros("1"),
			OriginalCash: units.MustMicros("1"), RemainingCash: units.MustMicros("1"),
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1,
			OpenReservationID: reservationID, Intent: "paper_place",
			ClientOrderID: clientID, State: "pending", Qty: units.MustQty("1"),
			Limit: units.MustMicros("1"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "shadow", Symbol: symbol, Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
			Limit: units.MustMicros("1"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPendingAttempt(attemptID, "paper-test", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim paper attempt: attempt=%+v err=%v", claimed, err)
	}
	now := time.Now().UTC()
	resolution := AttemptResolution{
		State: "settled", BrokerOrderID: "shadow-order:" + attemptID,
		OperationStatus: "executed",
		OrderUpdate: &OrderUpdate{
			ExecutionAttemptID: attemptID, BrokerOrderID: "shadow-order:" + attemptID,
			State: "filled", FilledQty: units.MustQty("1"),
			Fills: []FillInput{{
				BrokerFillID: "shadow-fill:" + attemptID + ":1",
				Qty:          units.MustQty("1"), Price: units.MustMicros("1"), TS: now,
			}},
		},
	}
	updated, err := s.ResolveAttempt(attemptID, claimed.Attempt, resolution)
	if err != nil || !updated {
		t.Fatalf("resolve paper attempt: updated=%v err=%v", updated, err)
	}
	updated, err = s.ResolveAttempt(attemptID, claimed.Attempt, resolution)
	if err != nil || updated {
		t.Fatalf("paper replay escaped fencing: updated=%v err=%v", updated, err)
	}
}

func (s *Store) shadowPositionsForTest() ([]ShadowPosition, error) {
	ctx, cancel := s.deadline()
	defer cancel()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	positions, err := (&ledgerTx{tx: tx, ctx: ctx}).ShadowPositions()
	if err != nil {
		return nil, err
	}
	return positions, tx.Commit()
}

func TestM3AActivationBackfillAndRollbackPostgres(t *testing.T) {
	databaseURL := os.Getenv("ALPHEUS_TEST_M3A_ACTIVATION_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_M3A_ACTIVATION_DATABASE_URL is not set")
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

	t.Run("backfills fills allocations and resting reservation once", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		symbol := "UPGRADE"
		legacyShadowID := NewID()
		if err := s.InsertOperation(legacyShadowID, "pre-m3a-shadow", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": true, "symbol": "OLD-SHADOW", "kind": "equity",
			"side": "buy", "qty": 1, "derived_max_risk": 1, "required_cash": 1,
		}, map[string]any{"class": "B"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.WithLedgerLock(true, func(gate OperationGate) error {
			return gate.InsertTradeGrant(TradeGrant{
				OperationID: legacyShadowID, Ledger: "shadow", MarketDay: time.Now(),
				AuthorizedRisk: units.MustMicros("1"), RiskSource: "computed",
			})
		}); err != nil {
			t.Fatal(err)
		}
		openOne := seedHistoricalOpen(t, s, symbol, units.MustQty("2"), units.MustQty("2"),
			units.MustMicros("20"), "filled", time.Now().UTC().Add(-3*time.Minute))
		seedHistoricalClose(t, s, symbol, units.MustQty("1"), units.MustMicros("9"),
			time.Now().UTC().Add(-2*time.Minute))
		openTwo := seedHistoricalOpen(t, s, symbol, units.MustQty("3"), units.MustQty("1"),
			units.MustMicros("30"), "partially_filled", time.Now().UTC().Add(-time.Minute))

		if err := s.ActivateM3A(M3AActivationSnapshot{
			Equity: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
			Positions: []ActivationPosition{{
				Symbol: symbol, Kind: "equity", Multiplier: 1, Qty: units.MustQty("2"),
			}},
		}); err != nil {
			t.Fatal(err)
		}
		var markers, lots, allocations int
		if err := s.DB.QueryRow(`SELECT count(*) FROM feature_activation WHERE name='m3a'`).Scan(&markers); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow(`SELECT count(*) FROM exposure_lot WHERE ledger='live' AND symbol=$1`, symbol).Scan(&lots); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow(`SELECT count(*) FROM exposure_close_allocation`).Scan(&allocations); err != nil {
			t.Fatal(err)
		}
		first, err := s.GetOpenReservation(openOne.operationID)
		if err != nil {
			t.Fatal(err)
		}
		second, err := s.GetOpenReservation(openTwo.operationID)
		if err != nil {
			t.Fatal(err)
		}
		resources, err := s.LedgerResources("live", "")
		if err != nil {
			t.Fatal(err)
		}
		if markers != 1 || lots != 2 || allocations != 1 ||
			first.ResourceState != "converted" || first.RemainingQty != 0 ||
			second.ResourceState != "held" || second.RemainingQty != units.MustQty("2") ||
			second.RemainingRisk != units.MustMicros("20") || second.RemainingCash != units.MustMicros("20") ||
			resources.OpenRisk != units.MustMicros("40") || resources.HeldCash != units.MustMicros("20") {
			t.Fatalf("markers=%d lots=%d allocations=%d first=%+v second=%+v resources=%+v",
				markers, lots, allocations, first, second, resources)
		}

		// The durable marker fences a restart from rewriting the ledger, even if
		// a later provider snapshot differs.
		if err := s.ActivateM3A(M3AActivationSnapshot{
			Equity: units.MustMicros("999"), BuyingPower: units.MustMicros("999"),
			Positions: []ActivationPosition{{
				Symbol: symbol, Kind: "equity", Multiplier: 1, Qty: units.MustQty("999"),
			}},
		}); err != nil {
			t.Fatalf("idempotent activation: %v", err)
		}
		var cash int64
		if err := s.DB.QueryRow(`SELECT cash_micros FROM shadow_account WHERE singleton=true`).Scan(&cash); err != nil {
			t.Fatal(err)
		}
		if cash != int64(units.MustMicros("300")) {
			t.Fatalf("second activation rewrote shadow cash: %d", cash)
		}
		windowDay := time.Now()
		shadowCount, err := s.CountTradesForDay(true, windowDay, windowDay.Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if shadowCount != 0 {
			t.Fatalf("pre-activation shadow grant leaked into paper statistics: %d", shadowCount)
		}
		newShadowID := NewID()
		if err := s.InsertOperation(newShadowID, "post-m3a-shadow", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": true, "symbol": "NEW-SHADOW", "kind": "equity",
			"side": "buy", "qty": 1, "derived_max_risk": 1, "required_cash": 1,
		}, map[string]any{"class": "B"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.WithLedgerLock(true, func(gate OperationGate) error {
			return gate.InsertTradeGrant(TradeGrant{
				OperationID: newShadowID, Ledger: "shadow", MarketDay: time.Now(),
				AuthorizedRisk: units.MustMicros("1"), RiskSource: "computed",
			})
		}); err != nil {
			t.Fatal(err)
		}
		shadowCount, err = s.CountTradesForDay(true, windowDay, windowDay.Add(24*time.Hour))
		if err != nil || shadowCount != 1 {
			t.Fatalf("post-activation shadow count=%d err=%v", shadowCount, err)
		}
	})

	t.Run("position mismatch rolls the whole activation back", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		seedHistoricalOpen(t, s, "BLOCKED", units.MustQty("1"), units.MustQty("1"),
			units.MustMicros("10"), "filled", time.Now().UTC().Add(-time.Minute))
		err := s.ActivateM3A(M3AActivationSnapshot{
			Equity: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
		})
		if err == nil {
			t.Fatal("activation accepted a broker/durable position mismatch")
		}
		var markers, lots, reservations, paperAccounts int
		if err := s.DB.QueryRow(`SELECT
			(SELECT count(*) FROM feature_activation),
			(SELECT count(*) FROM exposure_lot),
			(SELECT count(*) FROM open_reservation),
			(SELECT count(*) FROM shadow_account)`).Scan(
			&markers, &lots, &reservations, &paperAccounts); err != nil {
			t.Fatal(err)
		}
		if markers != 0 || lots != 0 || reservations != 0 || paperAccounts != 0 {
			t.Fatalf("partial activation survived rollback: marker=%d lots=%d reservations=%d paper=%d",
				markers, lots, reservations, paperAccounts)
		}
	})

	t.Run("position metadata mismatch rolls the whole activation back", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		seedHistoricalOpen(t, s, "WRONG-KIND", units.MustQty("1"), units.MustQty("1"),
			units.MustMicros("10"), "filled", time.Now().UTC().Add(-time.Minute))
		err := s.ActivateM3A(M3AActivationSnapshot{
			Equity: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
			Positions: []ActivationPosition{{
				Symbol: "WRONG-KIND", Kind: "option", Multiplier: 100, Qty: units.MustQty("1"),
			}},
		})
		if err == nil {
			t.Fatal("activation accepted broker position metadata that differs from durable exposure")
		}
		var markers, lots, reservations, paperAccounts int
		if err := s.DB.QueryRow(`SELECT
			(SELECT count(*) FROM feature_activation),
			(SELECT count(*) FROM exposure_lot),
			(SELECT count(*) FROM open_reservation),
			(SELECT count(*) FROM shadow_account)`).Scan(
			&markers, &lots, &reservations, &paperAccounts); err != nil {
			t.Fatal(err)
		}
		if markers != 0 || lots != 0 || reservations != 0 || paperAccounts != 0 {
			t.Fatalf("metadata mismatch left partial activation: marker=%d lots=%d reservations=%d paper=%d",
				markers, lots, reservations, paperAccounts)
		}
	})

	t.Run("grant mismatch rolls the whole activation back", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		historical := seedHistoricalOpen(t, s, "WRONG-GRANT", units.MustQty("1"), units.MustQty("1"),
			units.MustMicros("10"), "filled", time.Now().UTC().Add(-time.Minute))
		if _, err := s.DB.Exec(`UPDATE trade_grant SET authorized_risk_micros=$2
			WHERE operation_id=$1`, historical.operationID, int64(units.MustMicros("9"))); err != nil {
			t.Fatal(err)
		}
		err := s.ActivateM3A(M3AActivationSnapshot{
			Equity: units.MustMicros("300"), BuyingPower: units.MustMicros("300"),
			Positions: []ActivationPosition{{
				Symbol: "WRONG-GRANT", Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
			}},
		})
		if err == nil {
			t.Fatal("activation accepted a grant that differs from durable risk facts")
		}
		var markers, lots, reservations, paperAccounts int
		if err := s.DB.QueryRow(`SELECT
			(SELECT count(*) FROM feature_activation),
			(SELECT count(*) FROM exposure_lot),
			(SELECT count(*) FROM open_reservation),
			(SELECT count(*) FROM shadow_account)`).Scan(
			&markers, &lots, &reservations, &paperAccounts); err != nil {
			t.Fatal(err)
		}
		if markers != 0 || lots != 0 || reservations != 0 || paperAccounts != 0 {
			t.Fatalf("grant mismatch left partial activation: marker=%d lots=%d reservations=%d paper=%d",
				markers, lots, reservations, paperAccounts)
		}
	})
}

func TestTerminalReservationSweepRequiresCompleteProofPostgres(t *testing.T) {
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
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	neverClaimed := seedOrphanOpenReservation(t, s, "SAFE", "failed", "new", "")
	unknown := seedOrphanOpenReservation(t, s, "UNKNOWN", "unknown", "new", "")
	brokerTerminal := seedOrphanOpenReservation(t, s, "PROOF", "failed", "cancelled", "proof-order")
	missingOrder := seedOrphanOpenReservation(t, s, "NO-ORDER", "failed", "cancelled", "missing-order-proof")
	if _, err := s.DB.Exec(`DELETE FROM orders WHERE execution_attempt_id=$1`, missingOrder.attemptID); err != nil {
		t.Fatal(err)
	}
	candidates, err := s.ListTerminalReservationCandidates(10)
	if err != nil {
		t.Fatal(err)
	}
	byReservation := make(map[string]TerminalReservationCandidate, len(candidates))
	for _, candidate := range candidates {
		byReservation[candidate.ReservationID] = candidate
	}
	if _, exists := byReservation[unknown.reservationID]; exists {
		t.Fatal("unknown attempt was offered to the terminal sweeper")
	}
	if _, exists := byReservation[missingOrder.reservationID]; exists {
		t.Fatal("reservation without a durable order was offered to the terminal sweeper")
	}
	safe, exists := byReservation[neverClaimed.reservationID]
	if !exists || !safe.SafeWithoutBroker {
		t.Fatalf("never-claimed candidate=%+v exists=%v", safe, exists)
	}
	released, err := s.ReleaseProvenTerminalReservation(safe, 0, true)
	if err != nil || !released {
		t.Fatalf("release never-claimed: released=%v err=%v", released, err)
	}
	grant, err := s.HasTradeGrant(neverClaimed.operationID)
	if err != nil || !grant {
		t.Fatalf("terminal sweep removed immutable grant: grant=%v err=%v", grant, err)
	}

	proof, exists := byReservation[brokerTerminal.reservationID]
	if !exists || proof.SafeWithoutBroker {
		t.Fatalf("broker candidate=%+v exists=%v", proof, exists)
	}
	if released, err := s.ReleaseProvenTerminalReservation(proof, units.MustQty("1"), true); err != nil || released {
		t.Fatalf("mismatched cumulative fill released resource: released=%v err=%v", released, err)
	}
	reservation, err := s.GetOpenReservation(brokerTerminal.reservationID)
	if err != nil || reservation.ResourceState != "held" {
		t.Fatalf("mismatched proof changed reservation: reservation=%+v err=%v", reservation, err)
	}
	if released, err := s.ReleaseProvenTerminalReservation(proof, 0, true); err != nil || !released {
		t.Fatalf("matching terminal proof did not release: released=%v err=%v", released, err)
	}
}

type orphanOpen struct {
	operationID   string
	reservationID string
	attemptID     string
}

func seedOrphanOpenReservation(t *testing.T, s *Store, symbol, attemptState, orderState, brokerOrderID string) orphanOpen {
	t.Helper()
	operationID, reservationID, attemptID, clientID := NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "sweep-test", "B", "failed", map[string]any{
		"action": "open", "shadow": false, "symbol": symbol, "kind": "equity",
		"side": "buy", "qty": 1, "multiplier": 1,
		"derived_max_risk": 10, "required_cash": 10,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: time.Now(),
			AuthorizedRisk: units.MustMicros("10"), RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live",
			MarketDay: time.Now(), Symbol: symbol, Kind: "equity",
			OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"),
			OriginalRisk: units.MustMicros("10"), RemainingRisk: units.MustMicros("10"),
			OriginalCash: units.MustMicros("10"), RemainingCash: units.MustMicros("10"),
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1,
			OpenReservationID: reservationID, Intent: "place", ClientOrderID: clientID,
			State: attemptState, Qty: units.MustQty("1"), Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: NewID(), OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: symbol, Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
			Limit: units.MustMicros("10"), State: orderState,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if brokerOrderID != "" {
		if _, err := s.DB.Exec(`UPDATE execution_attempt SET broker_order_id=$2 WHERE id=$1`, attemptID, brokerOrderID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`UPDATE orders SET broker_order_id=$2 WHERE execution_attempt_id=$1`, attemptID, brokerOrderID); err != nil {
			t.Fatal(err)
		}
	}
	return orphanOpen{operationID: operationID, reservationID: reservationID, attemptID: attemptID}
}

type historicalOpen struct {
	operationID string
	attemptID   string
	orderID     string
}

func seedHistoricalOpen(t *testing.T, s *Store, symbol string, quantity, filled units.Qty, risk units.Micros, state string, fillTime time.Time) historicalOpen {
	t.Helper()
	operationID, attemptID, orderID, clientID := NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "m29-upgrade", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": symbol, "kind": "equity",
		"side": "buy", "qty": quantity, "multiplier": 1,
		"derived_max_risk": risk, "required_cash": risk, "approved_price_cap": 10,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	attemptState := "placed"
	if state == "filled" {
		attemptState = "settled"
	}
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: fillTime,
			AuthorizedRisk: risk, RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, Intent: "place",
			ClientOrderID: clientID, State: attemptState, Qty: quantity,
			Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: symbol, Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: quantity,
			Limit: units.MustMicros("10"), State: state,
		})
	}); err != nil {
		t.Fatal(err)
	}
	if filled > 0 {
		if _, err := s.DB.Exec(`INSERT INTO fills
			(id,order_id,broker_fill_id,ledger,qty,price_micros,fees_micros,ts)
			VALUES ($1,$2,$3,'live',$4,$5,0,$6)`, NewID(), orderID,
			"activation-open-"+orderID, int64(filled), int64(units.MustMicros("10")), fillTime); err != nil {
			t.Fatal(err)
		}
	}
	return historicalOpen{operationID: operationID, attemptID: attemptID, orderID: orderID}
}

func seedHistoricalClose(t *testing.T, s *Store, symbol string, quantity units.Qty, price units.Micros, fillTime time.Time) {
	t.Helper()
	operationID, reservationID, attemptID, orderID, clientID := NewID(), NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "m29-upgrade", "A", "executed", map[string]any{
		"action": "close", "shadow": false, "symbol": symbol, "kind": "equity",
		"side": "sell", "qty": quantity, "multiplier": 1,
	}, map[string]any{"class": "A"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		if err := gate.InsertCloseReservation(CloseReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", Symbol: symbol,
			OriginalQty: quantity, RemainingQty: quantity, State: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1,
			CloseReservationID: reservationID, Intent: "place", ClientOrderID: clientID,
			State: "settled", Qty: quantity, Limit: price,
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: symbol, Side: "sell",
			Kind: "equity", Multiplier: 1, Qty: quantity, Limit: price, State: "filled",
		})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE close_reservation SET remaining_qty=0,state='released',released_at=$2 WHERE id=$1`,
		reservationID, fillTime); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO fills
		(id,order_id,broker_fill_id,ledger,qty,price_micros,fees_micros,ts)
		VALUES ($1,$2,$3,'live',$4,$5,0,$6)`, NewID(), orderID,
		"activation-close-"+orderID, int64(quantity), int64(price), fillTime); err != nil {
		t.Fatal(err)
	}
}

func resetM3AIntegrationData(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB.Exec(`TRUNCATE TABLE events,live_canary_revision,feature_activation,shadow_account,
		shadow_positions,day_open,operations CASCADE`); err != nil {
		t.Fatal(fmt.Errorf("reset M3A integration database: %w", err))
	}
	if _, err := s.DB.Exec(`INSERT INTO live_execution_gate(singleton) VALUES (true)
		ON CONFLICT (singleton) DO NOTHING`); err != nil {
		t.Fatal(fmt.Errorf("reset live execution gate: %w", err))
	}
}

func TestProportionalInt64RoundsAgainstAccount(t *testing.T) {
	up, err := proportionalInt64(100, 2, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	down, err := proportionalInt64(100, 2, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	if up != 67 || down != 66 {
		t.Fatalf("ceil=%d floor=%d", up, down)
	}
	remainingRisk := int64(100)
	remainingCost := int64(100)
	var releasedRisk, matchedCost int64
	for remainingQty := int64(2); remainingQty >= 0; remainingQty-- {
		nextRisk, err := proportionalInt64(100, remainingQty, 3, true)
		if err != nil {
			t.Fatal(err)
		}
		nextCost, err := proportionalInt64(100, remainingQty, 3, false)
		if err != nil {
			t.Fatal(err)
		}
		if remainingQty > 0 && nextRisk*3 < 100*remainingQty {
			t.Fatalf("risk remainder rounded down: remaining_qty=%d risk=%d", remainingQty, nextRisk)
		}
		releasedRisk += remainingRisk - nextRisk
		matchedCost += remainingCost - nextCost
		remainingRisk, remainingCost = nextRisk, nextCost
	}
	if releasedRisk != 100 || matchedCost != 100 || remainingRisk != 0 || remainingCost != 0 {
		t.Fatalf("released_risk=%d matched_cost=%d remaining=%d/%d",
			releasedRisk, matchedCost, remainingRisk, remainingCost)
	}
}
