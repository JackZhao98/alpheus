package store

import (
	"errors"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestLiveSendAtomicallyBindsExactPreEffectManifestPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
	observation := seedCompletePreEffectObservation(t, s, "account-m11")
	manifest, err := s.RecordPreEffectManifest(evaluatedPreEffectInput(t, s, PreEffectManifestInput{
		AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
		Effect: "place_open", ObservationID: observation.ID,
		ObservationGeneration:     observation.Generation,
		ObservationManifestDigest: observation.ManifestDigest,
		Facts: map[string]any{
			"quote":      map[string]any{"symbol": "M11-V17", "bid": units.MustMicros("0.99"), "ask": units.MustMicros("1")},
			"instrument": map[string]any{"instrument_id": "instrument-m11", "kind": "equity"},
		},
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	}))
	if err != nil {
		t.Fatal(err)
	}
	marked, err := s.MarkAttemptSentWithManifest(
		attemptID, fencingToken, false, time.Second, nil, manifest.ID,
	)
	if err != nil || !marked {
		t.Fatalf("marked=%v err=%v", marked, err)
	}
	var boundManifestID string
	var ordinal, boundFence int
	if err := s.DB.QueryRow(`SELECT manifest_id,send_ordinal,fencing_token
		FROM execution_pre_effect_binding WHERE execution_attempt_id=$1`, attemptID).Scan(
		&boundManifestID, &ordinal, &boundFence,
	); err != nil {
		t.Fatal(err)
	}
	if boundManifestID != manifest.ID || ordinal != 0 || boundFence != fencingToken {
		t.Fatalf("binding manifest=%s ordinal=%d fence=%d", boundManifestID, ordinal, boundFence)
	}
	if _, err := s.DB.Exec(`UPDATE execution_pre_effect_manifest SET effect='place_close' WHERE id=$1`, manifest.ID); err == nil {
		t.Fatal("pre-effect evidence was mutable")
	}
}

func TestLiveSendSharesPolicyLockBeforeItsLedgerBarrierPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
	observation := seedCompletePreEffectObservation(t, s, "account-m11")
	manifest, err := s.RecordPreEffectManifest(evaluatedPreEffectInput(t, s, PreEffectManifestInput{
		AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
		Effect: "place_open", ObservationID: observation.ID,
		ObservationGeneration:     observation.Generation,
		ObservationManifestDigest: observation.ManifestDigest,
		Facts:                     map[string]any{"probe": "policy-lock-order"},
		ExpiresAt:                 time.Now().UTC().Add(time.Minute),
	}))
	if err != nil {
		t.Fatal(err)
	}

	// Model a proposal transaction which already holds the shared policy
	// authority. The send transition must take the same shared mode before its
	// ledger barrier; an exclusive upgrade here would block and can deadlock
	// against policy->ledger proposal traffic.
	proposalTx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proposalTx.Exec(`SELECT pg_advisory_xact_lock_shared($1)`, kernelPolicyLockKey); err != nil {
		_ = proposalTx.Rollback()
		t.Fatal(err)
	}
	type result struct {
		marked bool
		err    error
	}
	resultCh := make(chan result, 1)
	go func() {
		marked, markErr := s.MarkAttemptSentWithManifest(
			attemptID, fencingToken, false, time.Second, nil, manifest.ID,
		)
		resultCh <- result{marked: marked, err: markErr}
	}()
	select {
	case got := <-resultCh:
		_ = proposalTx.Rollback()
		if got.err != nil || !got.marked {
			t.Fatalf("marked=%v err=%v", got.marked, got.err)
		}
	case <-time.After(750 * time.Millisecond):
		_ = proposalTx.Rollback()
		got := <-resultCh
		t.Fatalf("send blocked behind a compatible policy lock: marked=%v err=%v", got.marked, got.err)
	}
}

func TestLiveSendRejectsMissingStaleAndExpiredPreEffectEvidencePostgres(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		s := openKernelPolicyIntegrationStore(t)
		defer s.DB.Close()
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		marked, err := s.MarkAttemptSentWithManifest(attemptID, fencingToken, false, time.Second, nil, "")
		if marked || err == nil {
			t.Fatalf("marked=%v err=%v", marked, err)
		}
		assertAttemptUnsent(t, s, attemptID)
	})

	t.Run("stale fencing", func(t *testing.T) {
		s := openKernelPolicyIntegrationStore(t)
		defer s.DB.Close()
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		observation := seedCompletePreEffectObservation(t, s, "account-m11")
		_, err := s.RecordPreEffectManifest(evaluatedPreEffectInput(t, s, PreEffectManifestInput{
			AttemptID: attemptID, FencingToken: fencingToken + 1, AccountID: "account-m11",
			Effect: "place_open", ObservationID: observation.ID,
			ObservationGeneration:     observation.Generation,
			ObservationManifestDigest: observation.ManifestDigest,
			Facts:                     map[string]any{"probe": "stale"}, ExpiresAt: time.Now().UTC().Add(time.Minute),
		}))
		if err == nil {
			t.Fatal("stale fencing created a manifest")
		}
		assertAttemptUnsent(t, s, attemptID)
	})

	t.Run("expired", func(t *testing.T) {
		s := openKernelPolicyIntegrationStore(t)
		defer s.DB.Close()
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		observation := seedCompletePreEffectObservation(t, s, "account-m11")
		manifest, err := s.RecordPreEffectManifest(evaluatedPreEffectInput(t, s, PreEffectManifestInput{
			AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
			Effect: "place_open", ObservationID: observation.ID,
			ObservationGeneration:     observation.Generation,
			ObservationManifestDigest: observation.ManifestDigest,
			Facts:                     map[string]any{"probe": "expiry"}, ExpiresAt: time.Now().UTC().Add(150 * time.Millisecond),
		}))
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		marked, err := s.MarkAttemptSentWithManifest(attemptID, fencingToken, false, time.Second, nil, manifest.ID)
		if marked || !errors.Is(err, ErrPreEffectStale) {
			t.Fatalf("marked=%v err=%v", marked, err)
		}
		assertAttemptUnsent(t, s, attemptID)
	})

	t.Run("active policy changed", func(t *testing.T) {
		s := openKernelPolicyIntegrationStore(t)
		defer s.DB.Close()
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		observation := seedCompletePreEffectObservation(t, s, "account-m11")
		input := evaluatedPreEffectInput(t, s, PreEffectManifestInput{
			AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
			Effect: "place_open", ObservationID: observation.ID,
			ObservationGeneration:     observation.Generation,
			ObservationManifestDigest: observation.ManifestDigest,
			Facts:                     map[string]any{"probe": "policy"}, ExpiresAt: time.Now().UTC().Add(time.Minute),
		})
		manifest, err := s.RecordPreEffectManifest(input)
		if err != nil {
			t.Fatal(err)
		}
		current, err := s.LoadKernelPolicyAuthority()
		if err != nil {
			t.Fatal(err)
		}
		next := current.Policy
		next.QuoteMaxAgeSec++
		if _, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
			Policy: next, ExpectedGeneration: current.Generation,
			RecordedBy: "test:pre-effect", Reason: "policy drift probe",
		}); err != nil {
			t.Fatal(err)
		}
		marked, err := s.MarkAttemptSentWithManifest(attemptID, fencingToken, false, time.Second, nil, manifest.ID)
		if marked || !errors.Is(err, ErrPreEffectStale) {
			t.Fatalf("marked=%v err=%v", marked, err)
		}
		assertAttemptUnsent(t, s, attemptID)
	})

	t.Run("local resources changed", func(t *testing.T) {
		s := openKernelPolicyIntegrationStore(t)
		defer s.DB.Close()
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		observation := seedCompletePreEffectObservation(t, s, "account-m11")
		manifest, err := s.RecordPreEffectManifest(evaluatedPreEffectInput(t, s, PreEffectManifestInput{
			AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
			Effect: "place_open", ObservationID: observation.ID,
			ObservationGeneration:     observation.Generation,
			ObservationManifestDigest: observation.ManifestDigest,
			Facts:                     map[string]any{"probe": "resources"}, ExpiresAt: time.Now().UTC().Add(time.Minute),
		}))
		if err != nil {
			t.Fatal(err)
		}
		otherOperationID := NewID()
		if err := s.WithProposalLock(nil, false, true, func(gate OperationGate) error {
			authority, err := gate.KernelPolicyAuthority()
			if err != nil {
				return err
			}
			if _, err := gate.InsertOperationBound(
				otherOperationID, "pre-effect-resource", "B", "auto_approved",
				map[string]any{"action": "open", "shadow": false, "symbol": "OTHER", "kind": "equity"},
				map[string]any{"class": "B"}, nil, authority,
			); err != nil {
				return err
			}
			return gate.InsertOpenReservation(OpenReservation{
				ID: NewID(), OperationID: otherOperationID, Ledger: "live",
				MarketDay: time.Now().UTC(), Symbol: "OTHER", Kind: "equity",
				OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"),
				OriginalRisk: units.MustMicros("10"), RemainingRisk: units.MustMicros("10"),
				OriginalCash: units.MustMicros("10"), RemainingCash: units.MustMicros("10"),
				ResourceState: "held",
			})
		}); err != nil {
			t.Fatal(err)
		}
		marked, err := s.MarkAttemptSentWithManifest(attemptID, fencingToken, false, time.Second, nil, manifest.ID)
		if marked || !errors.Is(err, ErrPreEffectStale) {
			t.Fatalf("marked=%v err=%v", marked, err)
		}
		assertAttemptUnsent(t, s, attemptID)
	})

	t.Run("proposal expires after evaluation", func(t *testing.T) {
		s := openKernelPolicyIntegrationStore(t)
		defer s.DB.Close()
		resetM3AIntegrationData(t, s)
		policy := testKernelPolicy(t)
		policy.ProposalTTLSec = 1
		authority, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
			Policy: policy, ExpectedGeneration: 0,
			RecordedBy: "test:pre-effect", Reason: "short TTL race probe",
		})
		if err != nil {
			t.Fatal(err)
		}
		operationID, attemptID, clientOrderID := NewID(), NewID(), NewID()
		var binding OperationPolicyBinding
		if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
			var insertErr error
			binding, insertErr = gate.InsertOperationBound(
				operationID, "pre-effect-ttl", "B", "auto_approved",
				map[string]any{"action": "open", "shadow": false, "symbol": "TTL", "kind": "equity"},
				map[string]any{"class": "B"}, nil, authority,
			)
			return insertErr
		}); err != nil {
			t.Fatal(err)
		}
		evidence := m11SeedReplayEvidence(t)
		if _, err := s.DB.Exec(`INSERT INTO execution_attempt
			(id,operation_id,seq,intent,client_order_id,state,qty,limit_micros,attempt,
			 claimed_by,claimed_at,lease_expires_at,intent_fingerprint,provider_account_id,provider_intent,
			 kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest,
			 authorization_expires_at,max_reprices,reprice_interval_sec,quote_max_age_sec)
			SELECT $1,$2,1,'place',$3,'claimed',$4,$5,1,'ttl-test',clock_timestamp(),
			 clock_timestamp()+interval '30 seconds',$6,$7,$8::jsonb,
			 o.kernel_policy_revision_id,o.kernel_policy_generation,o.kernel_policy_digest,o.expires_at,
			 (r.policy #>> '{execution_policy,max_reprices}')::integer,
			 (r.policy #>> '{execution_policy,reprice_interval_sec}')::integer,
			 (r.policy ->> 'quote_max_age_sec')::integer
			FROM operations o JOIN kernel_policy_revision r ON r.id=o.kernel_policy_revision_id
			WHERE o.id=$2`, attemptID, operationID, clientOrderID, int64(units.MustQty("1")),
			int64(units.MustMicros("1")), evidence.Fingerprint, evidence.AccountID, evidence.Canonical); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`UPDATE live_execution_gate SET active_attempt_id=$1,
			active_since=clock_timestamp(),unknown_attempt_id=NULL,unknown_since=NULL,updated_at=clock_timestamp()
			WHERE singleton=true`, attemptID); err != nil {
			t.Fatal(err)
		}
		observation := seedCompletePreEffectObservation(t, s, "account-m11")
		manifest, err := s.RecordPreEffectManifest(PreEffectManifestInput{
			AttemptID: attemptID, FencingToken: 1, AccountID: "account-m11",
			Effect: "place_open", ObservationID: observation.ID,
			ObservationGeneration: observation.Generation, ObservationManifestDigest: observation.ManifestDigest,
			Facts: map[string]any{"probe": "proposal-expiry"}, ExpiresAt: time.Now().UTC().Add(time.Minute),
			Ledger: "live", ActivePolicyRevisionID: authority.ID,
			ActivePolicyGeneration: authority.Generation, ActivePolicyDigest: authority.Digest,
		})
		if err != nil {
			t.Fatal(err)
		}
		if wait := time.Until(binding.ExpiresAt.Add(50 * time.Millisecond)); wait > 0 {
			time.Sleep(wait)
		}
		marked, err := s.MarkAttemptSentWithManifest(attemptID, 1, false, time.Second, nil, manifest.ID)
		if marked || !errors.Is(err, ErrPreEffectStale) {
			t.Fatalf("marked=%v err=%v", marked, err)
		}
		assertAttemptUnsent(t, s, attemptID)
	})
}

func TestPreEffectRequiresCompleteExactObservationPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
	now := time.Now().UTC()
	partial, err := s.RecordBrokerObservation(BrokerObservationInput{
		AccountID: "account-m11", Source: "fixture", Purpose: "pre_effect",
		StartedAt: now.Add(-time.Second), CompletedAt: now,
		Families: []BrokerObservationFamilyInput{
			{Family: BrokerFamilyAccount, Status: "success", CompletedAt: now, Items: []BrokerObservationItemInput{{
				ObjectKey: "account-m11", ObservedAt: now, Canonical: map[string]any{"account_id": "account-m11"},
			}}},
			{Family: BrokerFamilyPositions, Status: "error", ErrorCode: "unavailable", CompletedAt: now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordPreEffectManifest(evaluatedPreEffectInput(t, s, PreEffectManifestInput{
		AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
		Effect: "place_open", ObservationID: partial.ID,
		ObservationGeneration: partial.Generation, ObservationManifestDigest: partial.ManifestDigest,
		Facts: map[string]any{"probe": "partial"}, ExpiresAt: time.Now().UTC().Add(time.Minute),
	}))
	if err == nil {
		t.Fatal("partial observation created a pre-effect manifest")
	}
	assertAttemptUnsent(t, s, attemptID)
}

func seedCompletePreEffectObservation(t *testing.T, s *Store, accountID string) *BrokerObservation {
	t.Helper()
	now := time.Now().UTC()
	observation, err := s.RecordBrokerObservation(BrokerObservationInput{
		AccountID: accountID, Source: "fixture", Purpose: "pre_effect",
		StartedAt: now.Add(-time.Second), CompletedAt: now,
		Families: []BrokerObservationFamilyInput{
			{Family: BrokerFamilyAccount, Status: "success", CompletedAt: now, Items: []BrokerObservationItemInput{{
				ObjectKey: accountID, ObservedAt: now, Canonical: map[string]any{"account_id": accountID, "buying_power": units.MustMicros("100")},
			}}},
			{Family: BrokerFamilyPositions, Status: "success", CompletedAt: now},
			{Family: BrokerFamilyOrders, Status: "success", CompletedAt: now},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

func evaluatedPreEffectInput(t *testing.T, s *Store, input PreEffectManifestInput) PreEffectManifestInput {
	t.Helper()
	authority, err := s.LoadKernelPolicyAuthority()
	if errors.Is(err, ErrKernelPolicyAuthorityMissing) {
		authority, err = s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
			Policy: testKernelPolicy(t), ExpectedGeneration: 0,
			RecordedBy: "test:pre-effect", Reason: "pre-effect integration authority",
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	input.Ledger = "live"
	input.ActivePolicyRevisionID = authority.ID
	input.ActivePolicyGeneration = authority.Generation
	input.ActivePolicyDigest = authority.Digest
	return input
}

func assertAttemptUnsent(t *testing.T, s *Store, attemptID string) {
	t.Helper()
	var sent bool
	if err := s.DB.QueryRow(`SELECT sent_at IS NOT NULL FROM execution_attempt WHERE id=$1`, attemptID).Scan(&sent); err != nil {
		t.Fatal(err)
	}
	if sent {
		t.Fatal("attempt was marked sent without valid pre-effect evidence")
	}
	var bindings int
	if err := s.DB.QueryRow(`SELECT count(*) FROM execution_pre_effect_binding
		WHERE execution_attempt_id=$1`, attemptID).Scan(&bindings); err != nil && !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("bindings=%d, want 0", bindings)
	}
}
