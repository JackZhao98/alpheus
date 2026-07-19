package store

import (
	"context"
	"os"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/units"
)

func TestMixedExternalCloseDoesNotInventInternalPnLPostgres(t *testing.T) {
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

	now := time.Now().UTC()
	openOperationID, openAttemptID, openOrderID, openFillID := NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(openOperationID, "external-control-test", "B", "executed",
		map[string]any{"action": "open", "shadow": false, "symbol": "MIXED", "kind": "equity"},
		map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: openAttemptID, OperationID: openOperationID, Seq: 1, Intent: "place",
			ClientOrderID: NewID(), State: "settled", Qty: units.MustQty("1"), Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: openOrderID, OperationID: openOperationID, ExecutionAttemptID: openAttemptID,
			ClientOrderID: NewID(), Ledger: "live", Symbol: "MIXED", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"), Limit: units.MustMicros("10"), State: "filled",
		})
	}); err != nil {
		t.Fatal(err)
	}
	entryCost, err := units.MulQtyPrice(units.MustQty("1"), units.MustMicros("10"), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO fills
		(id,order_id,broker_fill_id,ledger,qty,price_micros,fees_micros,ts)
		VALUES ($1,$2,$3,'live',$4,$5,0,$6)`, openFillID, openOrderID,
		"mixed-open-fill-"+openFillID, int64(units.MustQty("1")), int64(units.MustMicros("10")), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO exposure_lot
		(open_fill_id,operation_id,ledger,symbol,kind,multiplier,opened_qty,closed_qty,
		 entry_cost_micros,remaining_cost_basis_micros,remaining_risk_micros,opened_at)
		VALUES ($1,$2,'live','MIXED','equity',1,$3,0,$4,$4,$4,$5)`, openFillID,
		openOperationID, int64(units.MustQty("1")), int64(entryCost), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	observation, err := s.RecordBrokerObservation(BrokerObservationInput{
		AccountID: "fixture-account", Source: "fixture", Purpose: "decision",
		StartedAt: now.Add(-time.Second), CompletedAt: now,
		Families: []BrokerObservationFamilyInput{
			{Family: BrokerFamilyAccount, Status: "success", CompletedAt: now, Items: []BrokerObservationItemInput{{
				ObjectKey: "fixture-account", ObservedAt: now,
				Canonical: map[string]any{"account_id": "fixture-account"},
			}}},
			{Family: BrokerFamilyPositions, Status: "success", CompletedAt: now, Items: []BrokerObservationItemInput{{
				ObjectKey: "position-mixed", ObservedAt: now, Canonical: broker.Position{
					PositionID: "position-mixed", InstrumentID: "instrument-mixed", Symbol: "MIXED",
					Qty: units.MustQty("2"), AvgPrice: units.MustMicros("10"), AvgPriceKnown: true,
					Kind: "equity", Multiplier: 1, Source: "fixture", AsOf: now,
				},
			}}},
			{Family: BrokerFamilyOrders, Status: "success", CompletedAt: now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	closeOperationID, reservationID, attemptID, orderID := NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(closeOperationID, "external-control-test", "A", "auto_approved",
		map[string]any{"action": "close", "shadow": false, "symbol": "MIXED", "kind": "equity", "side": "sell", "qty": 2, "multiplier": 1},
		map[string]any{"class": "A"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		if err := gate.InsertExternalControlEpisode(ExternalControlEpisode{
			ID: NewID(), OperationID: closeOperationID, ControlAction: "close_position", Origin: "mixed",
			BrokerObservationID: observation.ID, ObservationGeneration: observation.Generation,
			ObjectKey: "position-mixed", RequestedQty: units.MustQty("2"),
			TrackedQty: units.MustQty("1"), ExternalQty: units.MustQty("1"),
		}); err != nil {
			return err
		}
		if err := gate.InsertCloseReservation(CloseReservation{
			ID: reservationID, OperationID: closeOperationID, Ledger: "live", Symbol: "MIXED",
			OriginalQty: units.MustQty("2"), RemainingQty: units.MustQty("2"), State: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: closeOperationID, Seq: 1, CloseReservationID: reservationID,
			Intent: "place", ClientOrderID: NewID(), State: "placed",
			Qty: units.MustQty("2"), Limit: units.MustMicros("12"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: closeOperationID, ExecutionAttemptID: attemptID,
			ClientOrderID: NewID(), Ledger: "live", Symbol: "MIXED", Side: "sell",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("2"), Limit: units.MustMicros("12"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: attemptID, BrokerOrderID: "mixed-close-order", State: "filled",
		FilledQty: units.MustQty("2"), Fills: []FillInput{{
			BrokerFillID: "mixed-close-fill", Qty: units.MustQty("2"),
			Price: units.MustMicros("12"), Fees: units.MustMicros("2"), TS: now,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	var trackedClosed, externalClosed int64
	if err := s.DB.QueryRow(`SELECT closed_qty FROM exposure_lot WHERE open_fill_id=$1`, openFillID).Scan(&trackedClosed); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT qty FROM external_control_fill_allocation WHERE close_fill_id=(
		SELECT id FROM fills WHERE broker_fill_id='mixed-close-fill')`).Scan(&externalClosed); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pnl, pnlErr := localRealizedPnL(context.Background(), tx, "live", now.Add(-time.Hour), now.Add(time.Hour))
	_ = tx.Rollback()
	if pnlErr != nil {
		t.Fatal(pnlErr)
	}
	if trackedClosed != int64(units.MustQty("1")) || externalClosed != int64(units.MustQty("1")) || pnl != units.MustMicros("1") {
		t.Fatalf("tracked=%s external=%s local_pnl=%s", units.Qty(trackedClosed), units.Qty(externalClosed), pnl)
	}
}
