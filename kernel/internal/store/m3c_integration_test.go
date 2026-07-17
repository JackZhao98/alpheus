package store

import (
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestM3CRealizedPnLAndBreakersPostgres(t *testing.T) {
	databaseURL := os.Getenv("ALPHEUS_TEST_M3C_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_M3C_DATABASE_URL is not set")
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
	day := time.Date(2026, 7, 17, 0, 0, 0, 0, time.UTC)
	start, end, err := marketDayBounds(day, "America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("open without close realizes zero", func(t *testing.T) {
		resetM3CIntegrationData(t, s)
		seedPnLOpenOnly(t, s, "live", "OPEN", "equity", 1, units.MustQty("1"), units.MustMicros("100"), start.Add(time.Hour))
		stats := evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			MaxDailyLossPct: units.MustPercent("40"), ConsecutiveLossDaysHalt: 5,
			PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if stats.LocalRealizedPnL != 0 || stats.Halted {
			t.Fatalf("stats=%+v", stats)
		}
	})

	t.Run("FIFO matched cost partial fees and option multiplier", func(t *testing.T) {
		resetM3CIntegrationData(t, s)
		seedPnLClose(t, s, "live", "OVERNIGHT", "equity", 1, units.MustQty("1"),
			units.MustMicros("100"), units.MustMicros("90"), 0, start.Add(time.Hour))
		seedPnLClose(t, s, "live", "PARTIAL", "equity", 1, units.MustQty("0.5"),
			units.MustMicros("60"), units.MustMicros("100"), units.MustMicros("1"), start.Add(2*time.Hour))
		seedPnLClose(t, s, "live", "OPTION", "option", 100, units.MustQty("1"),
			units.MustMicros("150"), units.MustMicros("2"), units.MustMicros("1"), start.Add(3*time.Hour))
		stats := evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			MaxDailyLossPct: units.MustPercent("40"), ConsecutiveLossDaysHalt: 5,
			PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		// -10 overnight, -11 partial including fee, +49 option after multiplier/fee.
		if stats.LocalRealizedPnL != units.MustMicros("28") || stats.EffectiveRealizedPnL != units.MustMicros("28") {
			t.Fatalf("stats=%+v", stats)
		}
	})

	t.Run("daily loss and provider reconciliation use the lower value", func(t *testing.T) {
		resetM3CIntegrationData(t, s)
		seedPnLClose(t, s, "live", "LOSS", "equity", 1, units.MustQty("1"),
			units.MustMicros("220"), units.MustMicros("100"), 0, start.Add(time.Hour))
		stats := evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			MaxDailyLossPct: units.MustPercent("40"), ConsecutiveLossDaysHalt: 5,
			PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if stats.EffectiveRealizedPnL != units.MustMicros("-120") || stats.DailyLossLimit != units.MustMicros("120") ||
			!stats.Halted || stats.Reason != "daily_loss" {
			t.Fatalf("local loss stats=%+v", stats)
		}

		resetM3CBreakerRows(t, s)
		providerLag := units.MustMicros("-100")
		stats = evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			ProviderRealizedPnL: &providerLag, MaxDailyLossPct: units.MustPercent("40"),
			ConsecutiveLossDaysHalt: 5, PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if stats.EffectiveRealizedPnL != units.MustMicros("-120") || stats.Reason != "pnl_divergence" {
			t.Fatalf("lagging provider stats=%+v", stats)
		}

		resetM3CBreakerRows(t, s)
		providerLower := units.MustMicros("-130")
		stats = evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			ProviderRealizedPnL: &providerLower, MaxDailyLossPct: units.MustPercent("40"),
			ConsecutiveLossDaysHalt: 5, PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if stats.EffectiveRealizedPnL != units.MustMicros("-130") || stats.Reason != "pnl_divergence" {
			t.Fatalf("provider-only loss stats=%+v", stats)
		}
		var local, provider, effective int64
		if err := s.DB.QueryRow(`SELECT local_realized_pnl_micros,provider_realized_pnl_micros,
			effective_realized_pnl_micros FROM daily_pnl WHERE market_day=$1 AND ledger='live'`, day).Scan(
			&local, &provider, &effective); err != nil {
			t.Fatal(err)
		}
		if local != int64(units.MustMicros("-120")) || provider != int64(providerLower) || effective != int64(providerLower) {
			t.Fatalf("persisted pnl=%d/%d/%d", local, provider, effective)
		}
	})

	t.Run("daily loss latches for the market day", func(t *testing.T) {
		resetM3CIntegrationData(t, s)
		seedPnLClose(t, s, "live", "LATCH-LOSS", "equity", 1, units.MustQty("1"),
			units.MustMicros("220"), units.MustMicros("100"), 0, start.Add(time.Hour))
		input := DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			MaxDailyLossPct: units.MustPercent("40"), ConsecutiveLossDaysHalt: 5,
			PnLReconciliationLimit: units.MustMicros("0.01"),
		}
		stats := evaluateM3CDay(t, s, input, units.MustMicros("300"))
		if !stats.Halted || stats.Reason != "daily_loss" {
			t.Fatalf("initial latch stats=%+v", stats)
		}
		seedPnLClose(t, s, "live", "LATCH-PROFIT", "equity", 1, units.MustQty("1"),
			units.MustMicros("100"), units.MustMicros("230"), 0, start.Add(2*time.Hour))
		stats = evaluateM3CDay(t, s, input, units.MustMicros("300"))
		if stats.EffectiveRealizedPnL != units.MustMicros("10") || !stats.Halted || stats.Reason != "daily_loss" {
			t.Fatalf("same-day recovery cleared latch: %+v", stats)
		}
		nextDay := day.AddDate(0, 0, 1)
		nextStart, nextEnd, err := marketDayBounds(nextDay, "America/New_York")
		if err != nil {
			t.Fatal(err)
		}
		stats = evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: nextDay, Start: nextStart, End: nextEnd,
			MaxDailyLossPct: units.MustPercent("40"), ConsecutiveLossDaysHalt: 5,
			PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if stats.Halted {
			t.Fatalf("daily latch did not clear on next market day: %+v", stats)
		}
	})

	t.Run("current-day profit breaks an earlier loss streak", func(t *testing.T) {
		resetM3CIntegrationData(t, s)
		for offset := -5; offset < 0; offset++ {
			lossDay := day.AddDate(0, 0, offset)
			lossStart, _, err := marketDayBounds(lossDay, "America/New_York")
			if err != nil {
				t.Fatal(err)
			}
			seedPnLClose(t, s, "live", fmt.Sprintf("BROKEN-STREAK-%d", offset), "equity", 1, units.MustQty("1"),
				units.MustMicros("2"), units.MustMicros("1"), 0, lossStart.Add(time.Hour))
			insertDayOpenForTest(t, s, lossDay, "live", units.MustMicros("300"))
		}
		seedPnLClose(t, s, "live", "CURRENT-PROFIT", "equity", 1, units.MustQty("1"),
			units.MustMicros("1"), units.MustMicros("2"), 0, start.Add(time.Hour))
		stats := evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			ConsecutiveLossDaysHalt: 5, PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if stats.ConsecutiveLossDays != 0 || stats.Halted {
			t.Fatalf("positive day did not break streak: %+v", stats)
		}
	})

	t.Run("loss streak resume is same-day only", func(t *testing.T) {
		resetM3CIntegrationData(t, s)
		if err := s.WithLedgerLock(false, day, func(gate OperationGate) error {
			_, err := gate.ResumeBreaker("live", "loss_streak", day, "admin")
			return err
		}); !errors.Is(err, ErrBreakerNotActive) {
			t.Fatalf("preemptive resume err=%v", err)
		}
		var overrideCount int
		if err := s.DB.QueryRow(`SELECT count(*) FROM breaker_override`).Scan(&overrideCount); err != nil || overrideCount != 0 {
			t.Fatalf("preemptive override count=%d err=%v", overrideCount, err)
		}
		for offset := -5; offset < 0; offset++ {
			lossDay := day.AddDate(0, 0, offset)
			lossStart, _, err := marketDayBounds(lossDay, "America/New_York")
			if err != nil {
				t.Fatal(err)
			}
			seedPnLClose(t, s, "live", fmt.Sprintf("STREAK-%d", offset), "equity", 1, units.MustQty("1"),
				units.MustMicros("2"), units.MustMicros("1"), 0, lossStart.Add(time.Hour))
			insertDayOpenForTest(t, s, lossDay, "live", units.MustMicros("300"))
		}
		stats := evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			ConsecutiveLossDaysHalt: 5, PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if !stats.Halted || stats.Reason != "loss_streak" || stats.ConsecutiveLossDays != 5 {
			t.Fatalf("streak stats=%+v", stats)
		}
		var resumed BreakerState
		if err := s.WithLedgerLock(false, day, func(gate OperationGate) error {
			var err error
			resumed, err = gate.ResumeBreaker("live", "loss_streak", day, "admin")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if resumed.Halted {
			t.Fatalf("resumed=%+v", resumed)
		}
		stats = evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: day, Start: start, End: end,
			ConsecutiveLossDaysHalt: 5, PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if stats.Halted {
			t.Fatalf("same-day override did not suppress: %+v", stats)
		}

		seedPnLClose(t, s, "live", "STREAK-CURRENT", "equity", 1, units.MustQty("1"),
			units.MustMicros("2"), units.MustMicros("1"), 0, start.Add(time.Hour))
		nextDay := day.AddDate(0, 0, 1)
		nextStart, nextEnd, err := marketDayBounds(nextDay, "America/New_York")
		if err != nil {
			t.Fatal(err)
		}
		stats = evaluateM3CDay(t, s, DayRiskInput{
			Ledger: "live", MarketDay: nextDay, Start: nextStart, End: nextEnd,
			ConsecutiveLossDaysHalt: 5, PnLReconciliationLimit: units.MustMicros("0.01"),
		}, units.MustMicros("300"))
		if !stats.Halted || stats.Reason != "loss_streak" {
			t.Fatalf("override leaked into next day: %+v", stats)
		}
	})
}

func evaluateM3CDay(t *testing.T, s *Store, input DayRiskInput, equity units.Micros) DayRiskStats {
	t.Helper()
	insertDayOpenForTest(t, s, input.MarketDay, input.Ledger, equity)
	var stats DayRiskStats
	if err := s.WithLedgerLock(input.Ledger == "shadow", input.MarketDay, func(gate OperationGate) error {
		var err error
		stats, err = gate.EvaluateDayRisk(input)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return stats
}

func insertDayOpenForTest(t *testing.T, s *Store, day time.Time, ledger string, equity units.Micros) {
	t.Helper()
	if err := s.InsertDayOpen(day, ledger, equity); err != nil {
		t.Fatal(err)
	}
}

func seedPnLOpenOnly(t *testing.T, s *Store, ledger, symbol, kind string, multiplier int64,
	qty units.Qty, entryCost units.Micros, ts time.Time) (string, string) {
	t.Helper()
	operationID, attemptID, orderID, fillID, clientID := NewID(), NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "m3c-test", "B", "executed", map[string]any{
		"action": "open", "shadow": ledger == "shadow", "symbol": symbol, "kind": kind,
		"side": "buy", "qty": qty, "multiplier": multiplier,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	intent := "place"
	if ledger == "shadow" {
		intent, clientID = "paper_place", "shadow:"+attemptID
	}
	if err := s.WithProposalLock(nil, ledger == "shadow", nil, func(gate OperationGate) error {
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, Intent: intent,
			ClientOrderID: clientID, State: "settled", Qty: qty, Limit: units.MustMicros("1"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: ledger, Symbol: symbol, Side: "buy", Kind: kind,
			Multiplier: multiplier, Qty: qty, Limit: units.MustMicros("1"), State: "filled",
		})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO fills
		(id,order_id,broker_fill_id,ledger,qty,price_micros,fees_micros,ts)
		VALUES ($1,$2,$3,$4,$5,$6,0,$7)`, fillID, orderID, "m3c-open-"+fillID,
		ledger, int64(qty), int64(units.MustMicros("1")), ts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO exposure_lot
		(open_fill_id,operation_id,ledger,symbol,kind,multiplier,opened_qty,closed_qty,
		 entry_cost_micros,remaining_cost_basis_micros,remaining_risk_micros,opened_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,0,$8,$8,$8,$9)`, fillID, operationID, ledger,
		symbol, kind, multiplier, int64(qty), int64(entryCost), ts); err != nil {
		t.Fatal(err)
	}
	return operationID, fillID
}

func seedPnLClose(t *testing.T, s *Store, ledger, symbol, kind string, multiplier int64,
	qty units.Qty, matchedCost, exitPrice, fees units.Micros, ts time.Time) {
	t.Helper()
	_, openFillID := seedPnLOpenOnly(t, s, ledger, symbol, kind, multiplier, qty, matchedCost, ts.Add(-time.Hour))
	closeOperationID, attemptID, orderID, closeFillID, clientID := NewID(), NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(closeOperationID, "m3c-test", "A", "executed", map[string]any{
		"action": "close", "shadow": ledger == "shadow", "symbol": symbol, "kind": kind,
		"side": "sell", "qty": qty, "multiplier": multiplier,
	}, map[string]any{"class": "A"}, nil); err != nil {
		t.Fatal(err)
	}
	intent := "place"
	if ledger == "shadow" {
		intent, clientID = "paper_place", "shadow:"+attemptID
	}
	if err := s.WithProposalLock(nil, ledger == "shadow", nil, func(gate OperationGate) error {
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: closeOperationID, Seq: 1, Intent: intent,
			ClientOrderID: clientID, State: "settled", Qty: qty, Limit: exitPrice,
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: closeOperationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: ledger, Symbol: symbol, Side: "sell", Kind: kind,
			Multiplier: multiplier, Qty: qty, Limit: exitPrice, State: "filled",
		})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO fills
		(id,order_id,broker_fill_id,ledger,qty,price_micros,fees_micros,ts)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, closeFillID, orderID, "m3c-close-"+closeFillID,
		ledger, int64(qty), int64(exitPrice), int64(fees), ts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO exposure_close_allocation
		(close_fill_id,open_fill_id,qty,matched_cost_micros,released_risk_micros)
		VALUES ($1,$2,$3,$4,$4)`, closeFillID, openFillID, int64(qty), int64(matchedCost)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE exposure_lot SET closed_qty=opened_qty,
		remaining_cost_basis_micros=0,remaining_risk_micros=0,closed_at=$2 WHERE open_fill_id=$1`,
		openFillID, ts); err != nil {
		t.Fatal(err)
	}
}

func resetM3CIntegrationData(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB.Exec(`TRUNCATE TABLE events,breaker_override,daily_pnl,feature_activation,
		shadow_account,shadow_positions,day_open,operations CASCADE`); err != nil {
		t.Fatal(err)
	}
	resetM3CBreakerRows(t, s)
}

func resetM3CBreakerRows(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB.Exec(`TRUNCATE TABLE breaker_override,breaker_state;
		INSERT INTO breaker_state (ledger,halted) VALUES ('live',false),('shadow',false)`); err != nil {
		t.Fatal(err)
	}
}
