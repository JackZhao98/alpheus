package store

import (
	"errors"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestLiveSendAtomicallyBindsExactPreEffectManifestPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
	observation := seedCompletePreEffectObservation(t, s, "account-m11")
	manifest, err := s.RecordPreEffectManifest(PreEffectManifestInput{
		AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
		Effect: "place_open", ObservationID: observation.ID,
		ObservationGeneration:     observation.Generation,
		ObservationManifestDigest: observation.ManifestDigest,
		Facts: map[string]any{
			"quote":      map[string]any{"symbol": "M11-V17", "bid": units.MustMicros("0.99"), "ask": units.MustMicros("1")},
			"instrument": map[string]any{"instrument_id": "instrument-m11", "kind": "equity"},
		},
		ExpiresAt: time.Now().UTC().Add(time.Minute),
	})
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

func TestLiveSendRejectsMissingStaleAndExpiredPreEffectEvidencePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()

	t.Run("missing", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		marked, err := s.MarkAttemptSentWithManifest(attemptID, fencingToken, false, time.Second, nil, "")
		if marked || err == nil {
			t.Fatalf("marked=%v err=%v", marked, err)
		}
		assertAttemptUnsent(t, s, attemptID)
	})

	t.Run("stale fencing", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		observation := seedCompletePreEffectObservation(t, s, "account-m11")
		_, err := s.RecordPreEffectManifest(PreEffectManifestInput{
			AttemptID: attemptID, FencingToken: fencingToken + 1, AccountID: "account-m11",
			Effect: "place_open", ObservationID: observation.ID,
			ObservationGeneration:     observation.Generation,
			ObservationManifestDigest: observation.ManifestDigest,
			Facts:                     map[string]any{"probe": "stale"}, ExpiresAt: time.Now().UTC().Add(time.Minute),
		})
		if err == nil {
			t.Fatal("stale fencing created a manifest")
		}
		assertAttemptUnsent(t, s, attemptID)
	})

	t.Run("expired", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		observation := seedCompletePreEffectObservation(t, s, "account-m11")
		manifest, err := s.RecordPreEffectManifest(PreEffectManifestInput{
			AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
			Effect: "place_open", ObservationID: observation.ID,
			ObservationGeneration:     observation.Generation,
			ObservationManifestDigest: observation.ManifestDigest,
			Facts:                     map[string]any{"probe": "expiry"}, ExpiresAt: time.Now().UTC().Add(150 * time.Millisecond),
		})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		marked, err := s.MarkAttemptSentWithManifest(attemptID, fencingToken, false, time.Second, nil, manifest.ID)
		if marked || err == nil {
			t.Fatalf("marked=%v err=%v", marked, err)
		}
		assertAttemptUnsent(t, s, attemptID)
	})
}

func TestPreEffectRequiresCompleteExactObservationPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
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
	_, err = s.RecordPreEffectManifest(PreEffectManifestInput{
		AttemptID: attemptID, FencingToken: fencingToken, AccountID: "account-m11",
		Effect: "place_open", ObservationID: partial.ID,
		ObservationGeneration: partial.Generation, ObservationManifestDigest: partial.ManifestDigest,
		Facts: map[string]any{"probe": "partial"}, ExpiresAt: time.Now().UTC().Add(time.Minute),
	})
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
