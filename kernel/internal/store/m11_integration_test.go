package store

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

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
