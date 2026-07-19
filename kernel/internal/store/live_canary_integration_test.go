package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestClassifyLiveCanaryChangeTwoDimensions(t *testing.T) {
	oldCap := units.MustMicros("50")
	tests := []struct {
		name     string
		newCap   units.Micros
		newDays  int
		expected string
	}{
		{name: "equal", newCap: units.MustMicros("50"), newDays: 5, expected: "noop"},
		{name: "cap down", newCap: units.MustMicros("40"), newDays: 5, expected: "tighten"},
		{name: "days up", newCap: units.MustMicros("50"), newDays: 6, expected: "tighten"},
		{name: "both tighten", newCap: units.MustMicros("40"), newDays: 6, expected: "tighten"},
		{name: "cap up", newCap: units.MustMicros("60"), newDays: 5, expected: "widen"},
		{name: "days down", newCap: units.MustMicros("50"), newDays: 4, expected: "widen"},
		{name: "mixed cap down days down", newCap: units.MustMicros("40"), newDays: 4, expected: "widen"},
		{name: "mixed cap up days up", newCap: units.MustMicros("60"), newDays: 6, expected: "widen"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyLiveCanaryChange(oldCap, 5, test.newCap, test.newDays); got != test.expected {
				t.Fatalf("change=%s, want %s", got, test.expected)
			}
		})
	}
}

func TestLiveCanaryBootstrapIsExplicitIdempotentAndAuthoritativePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	// Pre-K0 rows were descriptive only and must never become runtime authority.
	if _, err := s.DB.Exec(`INSERT INTO live_canary_revision
		(daily_authorized_risk_micros,clean_days_before_raise,effective_market_day)
		VALUES ($1,1,current_date)`, int64(units.MustMicros("999999"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadLiveCanaryAuthority(); !errors.Is(err, ErrLiveCanaryAuthorityMissing) {
		t.Fatalf("legacy row became authority: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE live_canary_revision SET
		authority_version=1,recorded_by='db:test',reason='promote',change_class='initial'
		WHERE authority_version IS NULL`); err == nil {
		t.Fatal("legacy revision was promotable in place")
	}

	input := canaryInput("35", 5, 0, "deploy:test", "initial one-position canary")
	first, err := s.RecordLiveCanaryRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RecordLiveCanaryRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Generation != first.ID {
		t.Fatalf("non-idempotent bootstrap: first=%+v second=%+v", first, second)
	}
	assertCanaryRevisionCounts(t, s, 2, 1)

	active, err := s.LoadLiveCanaryAuthority()
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != first.ID || active.DailyAuthorizedRiskCapUSD != units.MustMicros("35") ||
		active.CleanDaysBeforeRaise != 5 || active.RecordedBy != "deploy:test" ||
		active.ChangeClass != "initial" {
		t.Fatalf("active authority=%+v", active)
	}
	if _, err := s.DB.Exec(`UPDATE live_canary_revision
		SET daily_authorized_risk_micros=$1 WHERE id=$2`, int64(units.MustMicros("999999")), active.ID); err == nil {
		t.Fatal("authoritative revision was mutable in place")
	}
	if _, err := s.DB.Exec(`DELETE FROM live_canary_revision WHERE id=$1`, active.ID); err == nil {
		t.Fatal("authoritative revision was deletable")
	}
	unchanged, err := s.LoadLiveCanaryAuthority()
	if err != nil || unchanged.ID != active.ID || unchanged.DailyAuthorizedRiskCapUSD != units.MustMicros("35") {
		t.Fatalf("authority changed after rejected mutation: %+v err=%v", unchanged, err)
	}
	conflict := canaryInput("34", 6, 0, "deploy:other", "stale bootstrap")
	if _, err := s.RecordLiveCanaryRevision(conflict); !errors.Is(err, ErrLiveCanaryRevisionConflict) {
		t.Fatalf("different bootstrap did not conflict: %v", err)
	}
	assertCanaryRevisionCounts(t, s, 2, 1)
}

func TestLiveCanaryMigrationPreservesLegacyDataWithoutPromotingItPostgres(t *testing.T) {
	databaseURL := os.Getenv("ALPHEUS_TEST_M3A_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_M3A_DATABASE_URL is not set")
	}
	migrationsDir := os.Getenv("ALPHEUS_TEST_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "../../../db/migrations"
	}
	migrations, err := LoadMigrations(migrationsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 10 {
		t.Fatalf("migrations=%d, want at least 10", len(migrations))
	}

	schema := "k0_upgrade_" + strings.ReplaceAll(NewID(), "-", "")
	admin, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := Migrate(ctx, db, migrations[:9], "America/New_York"); err != nil {
		t.Fatal(err)
	}
	operationID := NewID()
	if _, err := db.Exec(`INSERT INTO operations(id,proposer,class,status,payload,verdict)
		VALUES ($1,'legacy','B','auto_approved','{"action":"open","shadow":false}'::jsonb,'{"class":"B"}'::jsonb)`,
		operationID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO live_canary_revision
		(daily_authorized_risk_micros,clean_days_before_raise,effective_market_day)
		VALUES ($1,5,current_date)`, int64(units.MustMicros("999999"))); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO trade_grant
		(operation_id,ledger,market_day,authorized_risk_micros,risk_source)
		VALUES ($1,'live',current_date,$2,'computed')`, operationID, int64(units.MustMicros("1"))); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db, migrations, "America/New_York"); err != nil {
		t.Fatal(err)
	}

	var legacyRows, legacyGrants, nullBindings int
	if err := db.QueryRow(`SELECT
		(SELECT count(*) FROM live_canary_revision WHERE authority_version IS NULL),
		(SELECT count(*) FROM trade_grant),
		(SELECT count(*) FROM trade_grant WHERE live_canary_revision_id IS NULL)`).Scan(
		&legacyRows, &legacyGrants, &nullBindings); err != nil {
		t.Fatal(err)
	}
	if legacyRows != 1 || legacyGrants != 1 || nullBindings != 1 {
		t.Fatalf("legacy rows=%d grants=%d null bindings=%d", legacyRows, legacyGrants, nullBindings)
	}
	upgraded := &Store{DB: db, timeout: 3 * time.Second, marketTZ: "America/New_York"}
	if _, err := upgraded.LoadLiveCanaryAuthority(); !errors.Is(err, ErrLiveCanaryAuthorityMissing) {
		t.Fatalf("legacy revision promoted during migration: %v", err)
	}
}

func TestLiveCanaryWideningFailsClosedAndTighteningUsesCASPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	initial, err := s.RecordLiveCanaryRevision(canaryInput("50", 5, 0, "deploy:test", "initial"))
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []RecordLiveCanaryRevisionInput{
		canaryInput("60", 5, initial.ID, "deploy:test", "cap increase"),
		canaryInput("50", 4, initial.ID, "deploy:test", "clean-days decrease"),
		canaryInput("40", 4, initial.ID, "deploy:test", "mixed widening"),
		canaryInput("60", 6, initial.ID, "deploy:test", "mixed widening"),
	} {
		if _, err := s.RecordLiveCanaryRevision(candidate); !errors.Is(err, ErrLiveCanaryWideningUnsafe) {
			t.Fatalf("widening candidate passed: %+v err=%v", candidate, err)
		}
	}
	assertCanaryRevisionCounts(t, s, 1, 1)

	tightened, err := s.RecordLiveCanaryRevision(canaryInput("40", 6, initial.ID, "deploy:test", "tighten"))
	if err != nil {
		t.Fatal(err)
	}
	if tightened.ChangeClass != "tighten" || tightened.ID == initial.ID {
		t.Fatalf("tightening revision=%+v", tightened)
	}
	if _, err := s.RecordLiveCanaryRevision(canaryInput("30", 7, initial.ID, "deploy:test", "stale CAS")); !errors.Is(err, ErrLiveCanaryRevisionConflict) {
		t.Fatalf("stale expected revision passed: %v", err)
	}
	assertCanaryRevisionCounts(t, s, 2, 2)
}

func TestConcurrentIdenticalLiveCanaryBootstrapHasOneGenerationPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	const workers = 20
	start := make(chan struct{})
	results := make(chan *LiveCanaryRevision, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			revision, err := s.RecordLiveCanaryRevision(canaryInput("35", 5, 0,
				"deploy:test", "concurrent bootstrap"))
			results <- revision
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
	var generation int64
	for revision := range results {
		if revision == nil {
			t.Fatal("nil revision")
		}
		if generation == 0 {
			generation = revision.Generation
		} else if revision.Generation != generation {
			t.Fatalf("multiple generations: %d and %d", generation, revision.Generation)
		}
	}
	assertCanaryRevisionCounts(t, s, 1, 1)
}

func TestLiveCanaryActivationAndGrantAdmissionLinearizePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	initial, err := s.RecordLiveCanaryRevision(canaryInput("50", 5, 0, "deploy:test", "initial"))
	if err != nil {
		t.Fatal(err)
	}
	operationID := insertCanaryTestOperation(t, s, "linearized admission")
	authorityRead := make(chan int64, 1)
	releaseAdmission := make(chan struct{})
	admissionDone := make(chan error, 1)
	go func() {
		admissionDone <- s.WithProposalLock(nil, false, true, func(gate OperationGate) error {
			authority, err := gate.LiveCanaryAuthority(initial.EffectiveMarketDay)
			if err != nil {
				return err
			}
			authorityRead <- authority.ID
			<-releaseAdmission
			if err := gate.InsertTradeGrant(TradeGrant{
				OperationID: operationID, Ledger: "live", MarketDay: initial.EffectiveMarketDay,
				AuthorizedRisk: units.MustMicros("1"), RiskSource: "computed",
				LiveCanaryRevisionID: authority.ID,
			}); err != nil {
				return err
			}
			return gate.InsertEvent("trade_grant_created", map[string]any{
				"operation_id": operationID,
				"live_canary":  map[string]any{"revision_id": authority.ID, "generation": authority.Generation},
			})
		})
	}()
	if revisionID := <-authorityRead; revisionID != initial.ID {
		t.Fatalf("admission read revision=%d, want %d", revisionID, initial.ID)
	}

	activationDone := make(chan struct {
		revision *LiveCanaryRevision
		err      error
	}, 1)
	go func() {
		revision, err := s.RecordLiveCanaryRevision(canaryInput("40", 6, initial.ID,
			"deploy:test", "tighten behind admission"))
		activationDone <- struct {
			revision *LiveCanaryRevision
			err      error
		}{revision: revision, err: err}
	}()
	select {
	case result := <-activationDone:
		t.Fatalf("activation crossed held admission lock: revision=%+v err=%v", result.revision, result.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseAdmission)
	if err := <-admissionDone; err != nil {
		t.Fatal(err)
	}
	result := <-activationDone
	if result.err != nil || result.revision == nil || result.revision.ID == initial.ID {
		t.Fatalf("activation result=%+v err=%v", result.revision, result.err)
	}

	var grantRevision int64
	var eventRevision string
	if err := s.DB.QueryRow(`SELECT g.live_canary_revision_id,
		(SELECT e.payload->'live_canary'->>'revision_id' FROM events e
		 WHERE e.kind='trade_grant_created' AND e.payload->>'operation_id'=$1::text LIMIT 1)
		FROM trade_grant g WHERE g.operation_id=$1::uuid`, operationID).Scan(&grantRevision, &eventRevision); err != nil {
		t.Fatal(err)
	}
	if grantRevision != initial.ID || eventRevision != fmt.Sprint(initial.ID) {
		t.Fatalf("grant revision=%d event revision=%s, want old revision %d",
			grantRevision, eventRevision, initial.ID)
	}
	active, err := s.LoadLiveCanaryAuthority()
	if err != nil || active.ID != result.revision.ID {
		t.Fatalf("active=%+v err=%v", active, err)
	}
}

func TestLiveCanaryAuthorityRejectsMissingAuditAndFutureDayPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	var missingEventID int64
	if err := s.DB.QueryRow(`INSERT INTO live_canary_revision
		(daily_authorized_risk_micros,clean_days_before_raise,effective_market_day,
		 authority_version,recorded_by,reason,change_class)
		VALUES ($1,5,current_date,1,'db:test','missing event','initial') RETURNING id`,
		int64(units.MustMicros("35"))).Scan(&missingEventID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadLiveCanaryAuthority(); !errors.Is(err, ErrLiveCanaryAuthorityInvalid) {
		t.Fatalf("missing audit event accepted: %v", err)
	}

	resetM3AIntegrationData(t, s)
	var futureID int64
	if err := s.DB.QueryRow(`INSERT INTO live_canary_revision
		(daily_authorized_risk_micros,clean_days_before_raise,effective_market_day,
		 authority_version,recorded_by,reason,change_class)
		VALUES ($1,5,current_date+1,1,'db:test','future','initial') RETURNING id`,
		int64(units.MustMicros("35"))).Scan(&futureID); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"revision_id": futureID})
	if _, err := s.DB.Exec(`INSERT INTO events(kind,payload) VALUES ('live_canary_revision_recorded',$1)`, payload); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadLiveCanaryAuthority(); !errors.Is(err, ErrLiveCanaryAuthorityInvalid) {
		t.Fatalf("future authority accepted: %v", err)
	}
}

func TestLiveGrantBindsImmutableCanaryRevisionPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	first, err := s.RecordLiveCanaryRevision(canaryInput("50", 5, 0, "deploy:test", "initial"))
	if err != nil {
		t.Fatal(err)
	}
	firstOperation := insertCanaryTestOperation(t, s, "first")
	insertBoundCanaryGrant(t, s, firstOperation, first)

	second, err := s.RecordLiveCanaryRevision(canaryInput("40", 6, first.ID, "deploy:test", "tighten"))
	if err != nil {
		t.Fatal(err)
	}
	secondOperation := insertCanaryTestOperation(t, s, "second")
	insertBoundCanaryGrant(t, s, secondOperation, second)

	rows, err := s.DB.Query(`SELECT operation_id::text,live_canary_revision_id
		FROM trade_grant ORDER BY granted_at,operation_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	bindings := map[string]int64{}
	for rows.Next() {
		var operationID string
		var revisionID int64
		if err := rows.Scan(&operationID, &revisionID); err != nil {
			t.Fatal(err)
		}
		bindings[operationID] = revisionID
	}
	if bindings[firstOperation] != first.ID || bindings[secondOperation] != second.ID {
		t.Fatalf("grant bindings=%v, want %s:%d %s:%d", bindings,
			firstOperation, first.ID, secondOperation, second.ID)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		usage, err := gate.TradeGrantUsage("live", first.EffectiveMarketDay, firstOperation)
		if err != nil {
			return err
		}
		if usage.HasUnboundCanary || usage.HasLegacyUnknown || usage.GrantCount != 1 ||
			usage.AuthorizedRisk != units.MustMicros("1") {
			return fmt.Errorf("bound usage=%+v", usage)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func canaryInput(cap string, cleanDays int, expected int64, subject, reason string) RecordLiveCanaryRevisionInput {
	return RecordLiveCanaryRevisionInput{
		DailyAuthorizedRiskCapUSD: units.MustMicros(cap), CleanDaysBeforeRaise: cleanDays,
		ExpectedRevisionID: expected, RecordedBy: subject, Reason: reason,
	}
}

func insertCanaryTestOperation(t *testing.T, s *Store, label string) string {
	t.Helper()
	operationID := NewID()
	if err := s.InsertOperation(operationID, "k0-test", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "label": label,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	return operationID
}

func insertBoundCanaryGrant(t *testing.T, s *Store, operationID string, expected *LiveCanaryRevision) {
	t.Helper()
	if err := s.WithProposalLock(nil, false, true, func(gate OperationGate) error {
		authority, err := gate.LiveCanaryAuthority(expected.EffectiveMarketDay)
		if err != nil {
			return err
		}
		if authority.ID != expected.ID {
			return fmt.Errorf("authority=%d, want %d", authority.ID, expected.ID)
		}
		return gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: expected.EffectiveMarketDay,
			AuthorizedRisk: units.MustMicros("1"), RiskSource: "computed",
			LiveCanaryRevisionID: authority.ID,
		})
	}); err != nil {
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

func TestSameMarketDateIgnoresClockComponent(t *testing.T) {
	left := time.Date(2026, time.July, 20, 1, 2, 3, 0, time.UTC)
	right := time.Date(2026, time.July, 20, 23, 59, 0, 0, time.UTC)
	if !sameMarketDate(left, right) {
		t.Fatal("same date rejected")
	}
	positiveOffset := time.FixedZone("test-positive", 14*60*60)
	sameCalendarDayEarlierInstant := time.Date(2026, time.July, 20, 0, 0, 0, 0, positiveOffset)
	if marketDateAfter(left, sameCalendarDayEarlierInstant) {
		t.Fatal("same market calendar day was compared as an absolute timestamp")
	}
}
