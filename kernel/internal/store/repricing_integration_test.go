package store

import (
	"os"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestM5BRepriceTransfersReservationAtomicallyPostgres(t *testing.T) {
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
		Equity: units.MustMicros("100000"), BuyingPower: units.MustMicros("100000"),
	}); err != nil {
		t.Fatal(err)
	}

	operationID, reservationID := NewID(), NewID()
	placeAttemptID, sourceOrderID, clientID := NewID(), NewID(), NewID()
	marketDay := time.Now()
	quantity := units.MustQty("4")
	reserved := units.MustMicros("440")
	if err := s.InsertOperation(operationID, "m5b-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M5B", "kind": "equity",
		"side": "buy", "qty": quantity, "multiplier": 1,
		"approved_price_cap": units.MustMicros("110"),
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: marketDay,
			AuthorizedRisk: reserved, RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live",
			MarketDay: marketDay, Symbol: "M5B", Kind: "equity",
			OriginalQty: quantity, RemainingQty: quantity,
			OriginalRisk: reserved, RemainingRisk: reserved,
			OriginalCash: reserved, RemainingCash: reserved,
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: placeAttemptID, OperationID: operationID, Seq: 1,
			OpenReservationID: reservationID, Intent: "place", ClientOrderID: clientID,
			State: "placed", Qty: quantity, Limit: units.MustMicros("105"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: sourceOrderID, OperationID: operationID, ExecutionAttemptID: placeAttemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "M5B", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: quantity,
			Limit: units.MustMicros("105"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: placeAttemptID, BrokerOrderID: "m5b-source-order",
		State: "submitted", FilledQty: 0,
	}); err != nil {
		t.Fatal(err)
	}

	cancelAttempt, err := s.StageRepriceCancel(sourceOrderID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel: attempt=%+v err=%v", cancelAttempt, err)
	}
	duplicate, err := s.StageRepriceCancel(sourceOrderID)
	if err != nil || duplicate != nil {
		t.Fatalf("duplicate cancel stage: attempt=%+v err=%v", duplicate, err)
	}
	claimed, err := s.ClaimPendingAttempt(cancelAttempt.ID, "m5b-test")
	if err != nil || claimed == nil {
		t.Fatalf("claim cancel: attempt=%+v err=%v", claimed, err)
	}

	now := time.Now().UTC()
	replacement := RepriceReplacement{
		AttemptID: NewID(), OrderID: NewID(), ClientOrderID: NewID(),
		Limit: units.MustMicros("108"),
	}
	invalidReplacement := replacement
	invalidReplacement.AttemptID, invalidReplacement.OrderID, invalidReplacement.ClientOrderID = NewID(), NewID(), NewID()
	invalidReplacement.Limit = units.MustMicros("111")
	if next, err := s.FinalizeRepriceCancel(claimed.ID, claimed.Attempt, OrderUpdate{
		BrokerOrderID: "m5b-source-order", State: "cancelled", FilledQty: 0,
	}, &invalidReplacement, ""); err == nil || next != nil {
		t.Fatalf("replacement above cap was accepted: next=%+v err=%v", next, err)
	}
	next, err := s.FinalizeRepriceCancel(claimed.ID, claimed.Attempt, OrderUpdate{
		BrokerOrderID: "m5b-source-order", State: "cancelled",
		FilledQty: units.MustQty("2"), Fills: []FillInput{{
			BrokerFillID: "m5b-racing-fill", Qty: units.MustQty("2"),
			Price: units.MustMicros("105"), TS: now,
		}},
	}, &replacement, "")
	if err != nil || next == nil {
		t.Fatalf("finalize cancel: next=%+v err=%v", next, err)
	}
	if next.Seq != 3 || next.Qty != units.MustQty("2") ||
		next.OpenReservationID != reservationID || next.Limit != units.MustMicros("108") {
		t.Fatalf("replacement attempt=%+v", next)
	}
	reservation, err := s.GetOpenReservation(reservationID)
	if err != nil {
		t.Fatal(err)
	}
	if reservation.ResourceState != "held" || reservation.RemainingQty != units.MustQty("2") ||
		reservation.RemainingRisk != units.MustMicros("220") || reservation.RemainingCash != units.MustMicros("220") {
		t.Fatalf("transferred reservation=%+v", reservation)
	}
	// A normal order reconciler may have listed the source before the reprice
	// cancel was staged. Its stale terminal update must be a no-op now that the
	// reservation belongs to the replacement.
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: placeAttemptID, BrokerOrderID: "m5b-source-order",
		State: "cancelled", FilledQty: units.MustQty("2"), Fills: []FillInput{{
			BrokerFillID: "m5b-racing-fill", Qty: units.MustQty("2"),
			Price: units.MustMicros("105"), TS: now,
		}},
	}); err != nil {
		t.Fatal(err)
	}
	reservation, err = s.GetOpenReservation(reservationID)
	if err != nil || reservation.ResourceState != "held" || reservation.RemainingQty != units.MustQty("2") {
		t.Fatalf("stale reconciliation released transferred reservation: reservation=%+v err=%v", reservation, err)
	}
	source, err := s.GetOrderByBrokerID("m5b-source-order")
	if err != nil || source.State != "cancelled" || source.Reprices != 1 {
		t.Fatalf("source order=%+v err=%v", source, err)
	}
	replacementOrder, err := s.GetOrderByAttempt(next.ID)
	if err != nil || replacementOrder.State != "new" || replacementOrder.Reprices != 1 ||
		replacementOrder.Qty != units.MustQty("2") {
		t.Fatalf("replacement order=%+v err=%v", replacementOrder, err)
	}

	claimedReplacement, err := s.ClaimPendingAttempt(next.ID, "m5b-test")
	if err != nil || claimedReplacement == nil {
		t.Fatalf("replacement entitlement rejected: attempt=%+v err=%v", claimedReplacement, err)
	}
	updated, err := s.ResolveAttempt(claimedReplacement.ID, claimedReplacement.Attempt, AttemptResolution{
		State: "placed", BrokerOrderID: "m5b-replacement-order",
		OrderUpdate: &OrderUpdate{
			ExecutionAttemptID: next.ID, BrokerOrderID: "m5b-replacement-order",
			State: "submitted", FilledQty: 0,
		},
	})
	if err != nil || !updated {
		t.Fatalf("place replacement: updated=%v err=%v", updated, err)
	}
	replacementFill := FillInput{
		BrokerFillID: "m5b-replacement-fill", Qty: units.MustQty("1"),
		Price: units.MustMicros("108"), TS: now.Add(time.Second),
	}
	partialReplacement := OrderUpdate{
		ExecutionAttemptID: next.ID, BrokerOrderID: "m5b-replacement-order",
		State: "partially_filled", FilledQty: units.MustQty("1"),
		Fills: []FillInput{replacementFill},
	}
	if err := s.ApplyOrderUpdate(partialReplacement); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOrderUpdate(partialReplacement); err != nil {
		t.Fatalf("replacement partial replay: %v", err)
	}
	reservation, err = s.GetOpenReservation(reservationID)
	if err != nil || reservation.ResourceState != "held" || reservation.RemainingQty != units.MustQty("1") ||
		reservation.RemainingRisk != units.MustMicros("110") || reservation.RemainingCash != units.MustMicros("110") {
		t.Fatalf("replacement partial reservation=%+v err=%v", reservation, err)
	}
	replayed, err := s.FinalizeRepriceCancel(claimed.ID, claimed.Attempt, OrderUpdate{
		BrokerOrderID: "m5b-source-order", State: "cancelled",
		FilledQty: units.MustQty("2"), Fills: []FillInput{{
			BrokerFillID: "m5b-racing-fill", Qty: units.MustQty("2"),
			Price: units.MustMicros("105"), TS: now,
		}},
	}, &replacement, "")
	if err != nil || replayed != nil {
		t.Fatalf("stale finalize changed state: next=%+v err=%v", replayed, err)
	}
	var attempts, grants, fills int
	if err := s.DB.QueryRow(`SELECT count(*) FROM execution_attempt WHERE operation_id=$1`, operationID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM trade_grant WHERE operation_id=$1`, operationID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM fills WHERE order_id=$1`, sourceOrderID).Scan(&fills); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || grants != 1 || fills != 1 {
		t.Fatalf("attempts=%d grants=%d fills=%d, want 3/1/1", attempts, grants, fills)
	}
}

func TestM5BCloseBuyEntitlementCanBeClaimedPostgres(t *testing.T) {
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

	operationID, reservationID, attemptID, clientID := NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "m5b-test", "A", "auto_approved", map[string]any{
		"action": "close", "shadow": false, "symbol": "SHORT", "kind": "equity",
		"side": "buy", "qty": units.MustQty("1"), "multiplier": 1,
	}, map[string]any{"class": "A"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertCloseReservation(CloseReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", Symbol: "SHORT",
			OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"), State: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1,
			CloseReservationID: reservationID, Intent: "place", ClientOrderID: clientID,
			State: "pending", Qty: units.MustQty("1"), Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: NewID(), OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "SHORT",
			Side: "buy", Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
			Limit: units.MustMicros("10"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPendingAttempt(attemptID, "m5b-test")
	if err != nil || claimed == nil {
		t.Fatalf("close-buy claim: attempt=%+v err=%v", claimed, err)
	}
}
