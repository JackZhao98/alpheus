package store

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"alpheus/kernel/internal/policy"
)

func TestKernelPolicyBootstrapIsExplicitIdempotentAndImmutablePostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	p := testKernelPolicy(t)
	input := RecordKernelPolicyRevisionInput{
		Policy: p, ExpectedGeneration: 0, RecordedBy: "deploy:test", Reason: "initial K1 authority",
	}
	first, err := s.RecordKernelPolicyRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RecordKernelPolicyRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.Generation != 1 || first.ChangeClass != policy.ChangeInitial {
		t.Fatalf("non-idempotent bootstrap: first=%+v second=%+v", first, second)
	}
	active, err := s.LoadKernelPolicyAuthority()
	if err != nil || active.ID != first.ID || active.Digest != first.Digest {
		t.Fatalf("active=%+v err=%v", active, err)
	}
	if _, err := s.DB.Exec(`UPDATE kernel_policy_revision SET reason='tampered' WHERE id=$1`, first.ID); err == nil {
		t.Fatal("immutable revision was updated")
	}
	if _, err := s.DB.Exec(`DELETE FROM kernel_policy_revision WHERE id=$1`, first.ID); err == nil {
		t.Fatal("immutable revision was deleted")
	}
	var revisions, activations int
	if err := s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM kernel_policy_revision),
		(SELECT count(*) FROM events WHERE kind='kernel_policy_activated')`).Scan(&revisions, &activations); err != nil {
		t.Fatal(err)
	}
	if revisions != 1 || activations != 1 {
		t.Fatalf("revisions=%d activations=%d", revisions, activations)
	}
}

func TestKernelPolicyActivationCASAndChangeClassPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	base := testKernelPolicy(t)
	initial, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: base, ExpectedGeneration: 0, RecordedBy: "deploy:test", Reason: "initial",
	})
	if err != nil {
		t.Fatal(err)
	}

	tight := base
	tight.ProposalTTLSec = base.ProposalTTLSec / 2
	wide := base
	wide.HardLimits.MaxNewTradesPerDay++
	inputs := []RecordKernelPolicyRevisionInput{
		{Policy: tight, ExpectedGeneration: initial.Generation, RecordedBy: "deploy:a", Reason: "tighten TTL"},
		{Policy: wide, ExpectedGeneration: initial.Generation, RecordedBy: "deploy:b", Reason: "widen count"},
	}
	start := make(chan struct{})
	results := make(chan error, len(inputs))
	var wg sync.WaitGroup
	for _, input := range inputs {
		input := input
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.RecordKernelPolicyRevision(input)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var successes, conflicts int
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrKernelPolicyRevisionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected activation result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	active, err := s.LoadKernelPolicyAuthority()
	if err != nil {
		t.Fatal(err)
	}
	if active.Generation != 2 || (active.ChangeClass != policy.ChangeTighten && active.ChangeClass != policy.ChangeWiden) {
		t.Fatalf("active=%+v", active)
	}
	var revisions, activations, conflictsLogged int
	if err := s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM kernel_policy_revision),
		(SELECT count(*) FROM events WHERE kind='kernel_policy_activated'),
		(SELECT count(*) FROM events WHERE kind='kernel_policy_activation_conflict')`).Scan(
		&revisions, &activations, &conflictsLogged); err != nil {
		t.Fatal(err)
	}
	if revisions != 2 || activations != 2 || conflictsLogged != 1 {
		t.Fatalf("activation evidence: revisions=%d activations=%d conflicts=%d",
			revisions, activations, conflictsLogged)
	}
}

func TestKernelPolicyMissingOrCorruptAuthorityFailsClosedPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	if _, err := s.LoadKernelPolicyAuthority(); !errors.Is(err, ErrKernelPolicyAuthorityMissing) {
		t.Fatalf("missing authority=%v", err)
	}
	p := testKernelPolicy(t)
	active, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: p, ExpectedGeneration: 0, RecordedBy: "deploy:test", Reason: "initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, body, _, err := policy.Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	var corruptID int64
	if err := s.DB.QueryRow(`INSERT INTO kernel_policy_revision
		(schema_version,policy,digest,recorded_by,reason,change_class)
		VALUES (1,$1,$2,'db:test','corrupt candidate','tighten') RETURNING id`,
		string(body), make([]byte, 32)).Scan(&corruptID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE kernel_policy_head SET revision_id=$1,generation=$2,
		activated_at=clock_timestamp(),activated_by='db:test',reason='corrupt head'
		WHERE singleton=true`, corruptID, active.Generation+1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadKernelPolicyAuthority(); !errors.Is(err, ErrKernelPolicyAuthorityInvalid) {
		t.Fatalf("corrupt authority did not fail closed: %v", err)
	}
}

func TestOperationBindsPolicyAndDatabaseExpiryWithoutRuntimeFallbackPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	base := testKernelPolicy(t)
	initial, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: base, ExpectedGeneration: 0, RecordedBy: "deploy:test", Reason: "initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := NewID()
	var binding OperationPolicyBinding
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		authority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		binding, err = gate.InsertOperationBound(operationID, "k1-test", "C", "pending_review",
			map[string]any{"action": "open", "shadow": false}, map[string]any{"class": "C"}, nil, authority)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if row.KernelPolicyRevisionID != initial.ID || row.KernelPolicyGeneration != initial.Generation ||
		row.KernelPolicyDigest != initial.Digest || binding.Digest != initial.Digest ||
		!row.ExpiresAt.Equal(binding.ExpiresAt) {
		t.Fatalf("row=%+v binding=%+v initial=%+v", row, binding, initial)
	}
	lifetime := row.ExpiresAt.Sub(row.TS)
	if lifetime < 1800*time.Second || lifetime > 1801*time.Second {
		t.Fatalf("database proposal lifetime=%s", lifetime)
	}

	wide := base
	wide.ProposalTTLSec = 3600
	newAuthority, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: wide, ExpectedGeneration: initial.Generation,
		RecordedBy: "deploy:test", Reason: "widen new proposal TTL",
	})
	if err != nil || newAuthority.Generation != 2 {
		t.Fatalf("new authority=%+v err=%v", newAuthority, err)
	}
	unchanged, err := s.GetOperation(operationID)
	if err != nil || !unchanged.ExpiresAt.Equal(row.ExpiresAt) || unchanged.KernelPolicyGeneration != 1 {
		t.Fatalf("old proposal expanded after policy change: row=%+v err=%v", unchanged, err)
	}
	if err := s.WithReviewLock(operationID, func(gate OperationGate, locked *OperationRow) error {
		bound, err := gate.BoundKernelPolicy(locked)
		if err != nil {
			return err
		}
		if bound.Generation != 1 || bound.Policy.ProposalTTLSec != 1800 {
			t.Fatalf("bound authority=%+v", bound)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.InsertOperation(NewID(), "bypass", "B", "auto_approved",
		map[string]any{"action": "open"}, map[string]any{"class": "B"}, nil); err == nil {
		t.Fatal("unbound operation bypassed an active Kernel policy")
	}
}

func TestPolicyActivationSerializesWithNewOperationBindingPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	base := testKernelPolicy(t)
	initial, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: base, ExpectedGeneration: 0, RecordedBy: "deploy:test", Reason: "initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := NewID()
	bound := make(chan struct{})
	release := make(chan struct{})
	proposalResult := make(chan error, 1)
	go func() {
		proposalResult <- s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
			authority, err := gate.KernelPolicyAuthority()
			if err != nil {
				return err
			}
			close(bound)
			<-release
			_, err = gate.InsertOperationBound(operationID, "barrier", "B", "auto_approved",
				map[string]any{"action": "open"}, map[string]any{"class": "B"}, nil, authority)
			return err
		})
	}()
	<-bound
	wide := base
	wide.ProposalTTLSec = 3600
	activationStarted := make(chan struct{})
	activationResult := make(chan error, 1)
	go func() {
		close(activationStarted)
		_, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
			Policy: wide, ExpectedGeneration: initial.Generation,
			RecordedBy: "deploy:test", Reason: "concurrent activation",
		})
		activationResult <- err
	}()
	<-activationStarted
	select {
	case err := <-activationResult:
		t.Fatalf("activation crossed a shared operation binding lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-proposalResult; err != nil {
		t.Fatal(err)
	}
	if err := <-activationResult; err != nil {
		t.Fatal(err)
	}
	row, err := s.GetOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if row.KernelPolicyRevisionID != initial.ID || row.KernelPolicyGeneration != 1 ||
		row.KernelPolicyDigest != initial.Digest {
		t.Fatalf("operation tore across policy generations: %+v", row)
	}
}

func openKernelPolicyIntegrationStore(t *testing.T) *Store {
	t.Helper()
	databaseURL := os.Getenv("ALPHEUS_TEST_M3A_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ALPHEUS_TEST_M3A_DATABASE_URL is not set")
	}
	migrationsDir := os.Getenv("ALPHEUS_TEST_MIGRATIONS_DIR")
	if migrationsDir == "" {
		migrationsDir = "../../../db/migrations"
	}
	schema := "k1_policy_" + strings.ReplaceAll(NewID(), "-", "")
	admin, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`)
		_ = admin.Close()
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	s, err := Open(Config{
		URL: parsed.String(), MigrationsDir: migrationsDir,
		Timeout: 3 * time.Second, MarketTZ: "America/New_York",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testKernelPolicy(t *testing.T) policy.Policy {
	t.Helper()
	raw, err := os.ReadFile("../../limits.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p, err := policy.DecodeBootstrapYAML(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
