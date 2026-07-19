package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
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
	claimed, err := s.ClaimPendingAttempt(cancelAttempt.ID, "m5b-test", 30*time.Second)
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

	claimedReplacement, err := s.ClaimPendingAttempt(next.ID, "m5b-test", 30*time.Second)
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

func TestM5BRepriceFillIntegrityCommitsGlobalHaltPostgres(t *testing.T) {
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
	quantity := units.MustQty("2")
	reserved := units.MustMicros("220")
	if err := s.InsertOperation(operationID, "m5b-integrity-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M5B-INTEGRITY", "kind": "equity",
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
			MarketDay: marketDay, Symbol: "M5B-INTEGRITY", Kind: "equity",
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
			ClientOrderID: clientID, Ledger: "live", Symbol: "M5B-INTEGRITY", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: quantity,
			Limit: units.MustMicros("105"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}

	fillTime := time.Now().UTC()
	originalFill := FillInput{
		BrokerFillID: "m5b-integrity-fill", Qty: units.MustQty("1"),
		Price: units.MustMicros("105"), TS: fillTime,
	}
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: placeAttemptID, BrokerOrderID: "m5b-integrity-source",
		State: "partially_filled", FilledQty: units.MustQty("1"),
		Fills: []FillInput{originalFill},
	}); err != nil {
		t.Fatal(err)
	}
	cancelAttempt, err := s.StageRepriceCancel(sourceOrderID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel: attempt=%+v err=%v", cancelAttempt, err)
	}
	claimedCancel, err := s.ClaimPendingAttemptLive(cancelAttempt.ID, "m5b-integrity-test", 30*time.Second)
	if err != nil || claimedCancel == nil {
		t.Fatalf("claim cancel: attempt=%+v err=%v", claimedCancel, err)
	}
	marked, err := s.MarkAttemptSent(claimedCancel.ID, claimedCancel.Attempt, false, 0, nil)
	if err != nil || !marked {
		t.Fatalf("mark cancel sent: marked=%v err=%v", marked, err)
	}

	changedFill := originalFill
	changedFill.Price = units.MustMicros("106")
	next, err := s.FinalizeRepriceCancel(claimedCancel.ID, claimedCancel.Attempt, OrderUpdate{
		BrokerOrderID: "m5b-integrity-source", State: "cancelled",
		FilledQty: units.MustQty("1"), Fills: []FillInput{changedFill},
	}, nil, "integrity_test")
	if next != nil || !errors.Is(err, ErrFillIntegrity) {
		t.Fatalf("finalize integrity result: next=%+v err=%v", next, err)
	}

	// The failed finalize transaction must leave the broker facts and the
	// claimed cancel untouched; only the separately committed integrity event
	// and global Halt may survive it.
	var sourceState, cancelState string
	var fillPrice int64
	if err := s.DB.QueryRow(`SELECT o.state,a.state,f.price_micros
		FROM orders o
		JOIN execution_attempt a ON a.id=$2
		JOIN fills f ON f.order_id=o.id AND f.broker_fill_id=$3
		WHERE o.id=$1`, sourceOrderID, claimedCancel.ID, originalFill.BrokerFillID).Scan(
		&sourceState, &cancelState, &fillPrice,
	); err != nil {
		t.Fatal(err)
	}
	if sourceState != "partially_filled" || cancelState != "claimed" || fillPrice != int64(originalFill.Price) {
		t.Fatalf("rollback source=%s cancel=%s fill_price=%d", sourceState, cancelState, fillPrice)
	}
	var integrityEvents, haltEvents int
	if err := s.DB.QueryRow(`SELECT count(*) FILTER (WHERE kind='fill_integrity_error'),
		count(*) FILTER (WHERE kind='global_halt_transition') FROM events`).Scan(
		&integrityEvents, &haltEvents,
	); err != nil {
		t.Fatal(err)
	}
	if integrityEvents != 1 || haltEvents != 1 {
		t.Errorf("integrity_events=%d halt_events=%d, want 1/1", integrityEvents, haltEvents)
	}

	// Clear the failed cancel effect so a later, independently authorized Live
	// open can reach the final send cut. The committed Halt must reject it.
	resolved, resolveErr := s.ResolveAttempt(claimedCancel.ID, claimedCancel.Attempt, AttemptResolution{
		State: "failed", LastError: "integrity failure recorded",
	})
	if resolveErr != nil || !resolved {
		t.Fatalf("resolve cancel: resolved=%v err=%v", resolved, resolveErr)
	}

	probeOperationID, probeAttemptID, probeClientID := NewID(), NewID(), NewID()
	if err := s.InsertOperation(probeOperationID, "m5b-integrity-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M5B-HALT-PROBE", "kind": "equity",
		"side": "buy", "qty": units.MustQty("1"), "multiplier": 1,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(map[string]any{
		"account_id": "m5b-integrity-account", "kind": "equity", "symbol": "M5B-HALT-PROBE",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	const probeFence = 1
	if _, err := s.DB.Exec(`INSERT INTO execution_attempt
		(id,operation_id,seq,intent,client_order_id,state,qty,limit_micros,attempt,claimed_by,claimed_at,
		 intent_fingerprint,provider_account_id,provider_intent)
		VALUES ($1,$2,1,'place',$3,'claimed',$4,$5,$6,'m5b-integrity-test',clock_timestamp(),
		 $7,$8,$9::jsonb)`,
		probeAttemptID, probeOperationID, probeClientID,
		int64(units.MustQty("1")), int64(units.MustMicros("1")), probeFence,
		digest[:], "m5b-integrity-account", string(canonical),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE live_execution_gate SET
		active_attempt_id=$1,active_since=clock_timestamp(),unknown_attempt_id=NULL,unknown_since=NULL,
		updated_at=clock_timestamp() WHERE singleton=true`, probeAttemptID); err != nil {
		t.Fatal(err)
	}
	marked, err = s.MarkAttemptSent(probeAttemptID, probeFence, false, time.Second, nil)
	if marked || !errors.Is(err, ErrLiveSendHalted) {
		t.Fatalf("post-integrity Live open marked=%v err=%v, want live send halted", marked, err)
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
	claimed, err := s.ClaimPendingAttempt(attemptID, "m5b-test", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("close-buy claim: attempt=%+v err=%v", claimed, err)
	}
}
