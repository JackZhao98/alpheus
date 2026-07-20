package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
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

func TestLiveCanaryOwnerOverrideWidensWithAuditAndWithoutFabricatedEvidencePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	initial, err := s.RecordLiveCanaryRevision(canaryInput("50", 5, 0, "owner:jack", "initial"))
	if err != nil {
		t.Fatal(err)
	}
	invalid := canaryInput("40", 6, initial.ID, "owner:jack", "override cannot tighten")
	invalid.AccountID = "518428891"
	invalid.OwnerOverride = true
	if _, err := s.RecordLiveCanaryRevision(invalid); err == nil {
		t.Fatal("owner override tightening passed")
	}
	input := RecordLiveCanaryRevisionInput{
		DailyAuthorizedRiskCapUSD: units.MustMicros("100"), CleanDaysBeforeRaise: 5,
		ExpectedRevisionID: initial.ID, AccountID: "518428891",
		RecordedBy: "owner:jack", Reason: "owner-directed permanent cap increase",
		OwnerOverride: true,
	}
	widened, err := s.RecordLiveCanaryRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	if widened.AuthorityVersion != liveCanaryOwnerOverrideVersion ||
		widened.ChangeClass != "widen" || widened.RequiredAttestations != 0 ||
		len(widened.AttestationIDs) != 0 || widened.WideningAccountID != "518428891" ||
		widened.DailyAuthorizedRiskCapUSD != units.MustMicros("100") {
		t.Fatalf("widened=%+v", widened)
	}
	retry, err := s.RecordLiveCanaryRevision(input)
	if err != nil || retry.ID != widened.ID {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	active, err := s.LoadLiveCanaryAuthority()
	if err != nil || active.ID != widened.ID ||
		active.AuthorityVersion != liveCanaryOwnerOverrideVersion ||
		active.DailyAuthorizedRiskCapUSD != units.MustMicros("100") {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	assertCanaryRevisionCounts(t, s, 2, 2)
}

func TestLiveCanaryCompletedDayAttestationsGuardWideningPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetK1CIntegrationData(t, s)
	defer cleanupK1CIntegrationData(t, s)
	policyAuthority, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: testKernelPolicy(t), ExpectedGeneration: 0,
		RecordedBy: "deploy:test", Reason: "K1C policy authority",
	})
	if err != nil {
		t.Fatal(err)
	}
	days := priorWeekdays(t, s, 3)
	initial := seedPastLiveCanaryAuthority(t, s, days[0], 3)
	for _, day := range days {
		seedCompletedCanaryExecutionDay(t, s, day, initial, policyAuthority)
	}
	now, err := s.DatabaseNow()
	if err != nil {
		t.Fatal(err)
	}
	observation := recordReconciliationObservation(t, s, "account-k1c", "EMPTY", 0, now.Add(-time.Second))
	reconciled, err := s.ReconcileBrokerObservation(observation.ID)
	if err != nil || !reconciled.Applied || reconciled.Deferred {
		t.Fatalf("reconciliation=%+v err=%v", reconciled, err)
	}

	attestationIDs := []int64{}
	for _, day := range days {
		input := RecordLiveCanaryDayAttestationInput{
			AccountID: "account-k1c", MarketDay: day, ExpectedRevisionID: initial.ID,
			AttestedBy: "deploy:test", Reason: "post-close provider and broker reconciliation",
		}
		first, err := s.RecordLiveCanaryDayAttestation(input)
		if err != nil {
			t.Fatal(err)
		}
		retry, err := s.RecordLiveCanaryDayAttestation(input)
		if err != nil || retry.ID != first.ID {
			t.Fatalf("attestation retry=%+v err=%v, want id=%d", retry, err, first.ID)
		}
		if first.LiveGrantCount != 1 || first.AuthorizedRisk != units.MustMicros("1") ||
			first.KernelPolicyRevisionID != policyAuthority.ID || first.BrokerObservationID != observation.ID {
			t.Fatalf("attestation=%+v", first)
		}
		attestationIDs = append(attestationIDs, first.ID)
	}

	widenInput := RecordLiveCanaryRevisionInput{
		DailyAuthorizedRiskCapUSD: units.MustMicros("60"), CleanDaysBeforeRaise: 3,
		ExpectedRevisionID: initial.ID, AccountID: "account-k1c",
		RecordedBy: "deploy:test", Reason: "three completed clean canary days",
	}
	const wideningWorkers = 20
	start := make(chan struct{})
	results := make(chan *LiveCanaryRevision, wideningWorkers)
	errorsCh := make(chan error, wideningWorkers)
	var wait sync.WaitGroup
	for worker := 0; worker < wideningWorkers; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			revision, err := s.RecordLiveCanaryRevision(widenInput)
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
	var widened *LiveCanaryRevision
	for revision := range results {
		if revision == nil {
			t.Fatal("nil concurrent widening result")
		}
		if widened == nil {
			widened = revision
		} else if revision.ID != widened.ID {
			t.Fatalf("widening generations=%d/%d", widened.ID, revision.ID)
		}
	}
	if widened.ChangeClass != "widen" || widened.AuthorityVersion != liveCanaryAuthorityVersion ||
		widened.RequiredAttestations != 3 || widened.WideningAccountID != "account-k1c" ||
		len(widened.AttestationIDs) != 3 {
		t.Fatalf("widened=%+v", widened)
	}
	active, err := s.LoadLiveCanaryAuthority()
	if err != nil || active.ID != widened.ID || active.ChangeClass != "widen" ||
		active.RequiredAttestations != widened.RequiredAttestations ||
		!reflect.DeepEqual(active.AttestationIDs, widened.AttestationIDs) {
		t.Fatalf("active=%+v err=%v want=%+v", active, err, widened)
	}
	loaded, err := s.LoadLiveCanaryDayAttestations("account-k1c", 10)
	if err != nil || len(loaded) != 3 {
		t.Fatalf("loaded attestations=%+v err=%v", loaded, err)
	}
	if _, err := s.DB.Exec(`UPDATE live_canary_day_attestation SET reason='tampered' WHERE id=$1`, attestationIDs[0]); err == nil {
		t.Fatal("completed-day attestation was mutable")
	}
	if _, err := s.DB.Exec(`DELETE FROM live_canary_widening_evidence WHERE revision_id=$1`, widened.ID); err == nil {
		t.Fatal("widening evidence was deletable")
	}
	assertCanaryRevisionCounts(t, s, 2, 2)
}

func TestLiveCanaryAttestationAndWideningFailClosedOnIncompleteEvidencePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetK1CIntegrationData(t, s)
	defer cleanupK1CIntegrationData(t, s)
	policyAuthority, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: testKernelPolicy(t), ExpectedGeneration: 0,
		RecordedBy: "deploy:test", Reason: "K1C policy authority",
	})
	if err != nil {
		t.Fatal(err)
	}
	days := priorWeekdays(t, s, 2)
	initial := seedPastLiveCanaryAuthority(t, s, days[0], 2)
	seedCompletedCanaryExecutionDay(t, s, days[0], initial, policyAuthority)
	seedFinalDayPnL(t, s, days[1]) // day_open plus PnL is deliberately insufficient.
	now, err := s.DatabaseNow()
	if err != nil {
		t.Fatal(err)
	}
	observation := recordReconciliationObservation(t, s, "account-k1c-fail", "EMPTY", 0, now.Add(-time.Second))
	if _, err := s.ReconcileBrokerObservation(observation.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordLiveCanaryDayAttestation(RecordLiveCanaryDayAttestationInput{
		AccountID: "account-k1c-fail", MarketDay: days[1], ExpectedRevisionID: initial.ID,
		AttestedBy: "deploy:test", Reason: "day_open is not proof",
	}); !errors.Is(err, ErrLiveCanaryDayEvidenceInvalid) {
		t.Fatalf("day_open-only attestation passed: %v", err)
	}
	if _, err := s.RecordLiveCanaryDayAttestation(RecordLiveCanaryDayAttestationInput{
		AccountID: "account-k1c-fail", MarketDay: days[0], ExpectedRevisionID: initial.ID,
		AttestedBy: "deploy:test", Reason: "one clean day",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordLiveCanaryRevision(RecordLiveCanaryRevisionInput{
		DailyAuthorizedRiskCapUSD: units.MustMicros("60"), CleanDaysBeforeRaise: 2,
		ExpectedRevisionID: initial.ID, AccountID: "account-k1c-fail",
		RecordedBy: "deploy:test", Reason: "insufficient evidence",
	}); !errors.Is(err, ErrLiveCanaryWideningUnsafe) {
		t.Fatalf("incomplete widening passed: %v", err)
	}
	assertCanaryRevisionCounts(t, s, 1, 1)
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

func resetK1CIntegrationData(t *testing.T, s *Store) {
	t.Helper()
	resetM3AIntegrationData(t, s)
	if _, err := s.DB.Exec(`TRUNCATE TABLE daily_pnl,kernel_policy_head,kernel_policy_revision,
		broker_observation,broker_local_state_revision CASCADE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO broker_local_state_revision(singleton,generation)
		VALUES (true,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO live_execution_gate(singleton) VALUES (true)
		ON CONFLICT (singleton) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
}

func cleanupK1CIntegrationData(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.DB.Exec(`TRUNCATE TABLE daily_pnl,kernel_policy_head,kernel_policy_revision,
		broker_observation,broker_local_state_revision CASCADE`); err != nil {
		t.Errorf("cleanup K1C integration data: %v", err)
		return
	}
	if _, err := s.DB.Exec(`INSERT INTO broker_local_state_revision(singleton,generation)
		VALUES (true,0) ON CONFLICT (singleton) DO NOTHING`); err != nil {
		t.Errorf("restore broker local state: %v", err)
	}
	if _, err := s.DB.Exec(`INSERT INTO live_execution_gate(singleton) VALUES (true)
		ON CONFLICT (singleton) DO NOTHING`); err != nil {
		t.Errorf("restore live execution gate: %v", err)
	}
}

func priorWeekdays(t *testing.T, s *Store, count int) []time.Time {
	t.Helper()
	now, err := s.DatabaseNow()
	if err != nil {
		t.Fatal(err)
	}
	location, err := time.LoadLocation(s.marketTZ)
	if err != nil {
		t.Fatal(err)
	}
	year, month, date := now.In(location).Date()
	day := time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
	result := make([]time.Time, 0, count)
	for len(result) < count {
		day = day.AddDate(0, 0, -1)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		result = append(result, day)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func seedPastLiveCanaryAuthority(t *testing.T, s *Store, effectiveDay time.Time, cleanDays int) *LiveCanaryRevision {
	t.Helper()
	var id int64
	if err := s.DB.QueryRow(`INSERT INTO live_canary_revision
		(daily_authorized_risk_micros,clean_days_before_raise,effective_market_day,
		 authority_version,recorded_by,reason,change_class,required_attestations)
		VALUES ($1,$2,$3,2,'deploy:test','past K1C fixture','initial',0) RETURNING id`,
		int64(units.MustMicros("50")), cleanDays, effectiveDay).Scan(&id); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"revision_id": id, "generation": id, "change": "initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO events(kind,payload)
		VALUES ('live_canary_revision_recorded',$1)`, payload); err != nil {
		t.Fatal(err)
	}
	authority, err := s.LoadLiveCanaryAuthority()
	if err != nil {
		t.Fatal(err)
	}
	return authority
}

func seedCompletedCanaryExecutionDay(t *testing.T, s *Store, day time.Time,
	canary *LiveCanaryRevision, expectedPolicy *KernelPolicyRevision) {
	t.Helper()
	operationID, attemptID, orderID, clientID := NewID(), NewID(), NewID(), NewID()
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		authority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		if authority.ID != expectedPolicy.ID {
			return fmt.Errorf("policy=%d want=%d", authority.ID, expectedPolicy.ID)
		}
		if _, err := gate.InsertOperationBound(operationID, "k1c-test", "B", "executed",
			map[string]any{"action": "open", "shadow": false, "symbol": "K1C", "kind": "equity"},
			map[string]any{"class": "B"}, nil, authority); err != nil {
			return err
		}
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: day,
			AuthorizedRisk: units.MustMicros("1"), RiskSource: "computed",
			LiveCanaryRevisionID: canary.ID,
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, Intent: "place",
			ClientOrderID: clientID, State: "settled", Qty: units.MustQty("1"),
			Limit: units.MustMicros("1"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "K1C", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
			Limit: units.MustMicros("1"), State: "cancelled",
		})
	}); err != nil {
		t.Fatal(err)
	}
	seedFinalDayPnL(t, s, day)
}

func seedFinalDayPnL(t *testing.T, s *Store, day time.Time) {
	t.Helper()
	closeAt, err := liveCanarySessionClose(day, s.marketTZ)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO day_open(market_day,ledger,equity_micros)
		VALUES ($1,'live',$2) ON CONFLICT (market_day,ledger) DO NOTHING`,
		day, int64(units.MustMicros("300"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO daily_pnl
		(market_day,ledger,local_realized_pnl_micros,provider_realized_pnl_micros,
		 effective_realized_pnl_micros,updated_at)
		VALUES ($1,'live',0,0,0,$2)`, day, closeAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
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
