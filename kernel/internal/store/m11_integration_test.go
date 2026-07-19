package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestReplayWindowExpiryRetainsUnknownWithoutConsumingReplayPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()

	tests := []struct {
		name        string
		sentAt      func(time.Time) time.Time
		windowStart func(time.Time) time.Time
		windowEnd   func(time.Time) time.Time
	}{
		{
			name:        "expired",
			sentAt:      func(now time.Time) time.Time { return now.Add(-2 * time.Minute) },
			windowStart: func(now time.Time) time.Time { return now.Add(-2*time.Minute - 30*time.Second) },
			windowEnd:   func(now time.Time) time.Time { return now.Add(-time.Second) },
		},
		{
			name:        "guard equality boundary",
			sentAt:      func(now time.Time) time.Time { return now },
			windowStart: func(now time.Time) time.Time { return now.Add(-30 * time.Second) },
			windowEnd:   func(now time.Time) time.Time { return now.Add(time.Second) },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetM3AIntegrationData(t, s)
			var databaseNow time.Time
			if err := s.DB.QueryRow(`SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
				t.Fatal(err)
			}
			attemptID, fencingToken := seedM11ClaimedOpenAttempt(
				t, s, "unknown", test.sentAt(databaseNow), test.windowStart(databaseNow), test.windowEnd(databaseNow),
			)

			marked, err := s.MarkAttemptSent(attemptID, fencingToken, true, time.Second, m11SeedReplayEvidence(t))
			if marked || !errors.Is(err, ErrReplayWindowExpired) {
				t.Fatalf("marked=%v err=%v, want replay window expired", marked, err)
			}

			var state, providerAccountID string
			var replayCount, fingerprintBytes int
			var evidencePresent, sentEvidencePresent bool
			if err := s.DB.QueryRow(`SELECT state,replay_count,provider_account_id,
				octet_length(intent_fingerprint),provider_intent IS NOT NULL,
				sent_at IS NOT NULL AND send_window_start IS NOT NULL AND send_window_end IS NOT NULL
				FROM execution_attempt WHERE id=$1`, attemptID).Scan(
				&state, &replayCount, &providerAccountID, &fingerprintBytes, &evidencePresent, &sentEvidencePresent,
			); err != nil {
				t.Fatal(err)
			}
			gate, err := s.GetLiveExecutionGate()
			if err != nil {
				t.Fatal(err)
			}
			if state != "claimed" || replayCount != 0 || providerAccountID != "account-m11" ||
				fingerprintBytes != sha256.Size || !evidencePresent || !sentEvidencePresent ||
				gate.ActiveAttemptID != "" || gate.UnknownAttemptID != attemptID {
				t.Fatalf("attempt state=%s replay=%d account=%q fingerprint=%d evidence=%v sent_evidence=%v gate=%+v",
					state, replayCount, providerAccountID, fingerprintBytes, evidencePresent, sentEvidencePresent, gate)
			}
			var sentEvents int
			if err := s.DB.QueryRow(`SELECT count(*) FROM events
				WHERE kind='execution_attempt_sent' AND payload->>'attempt_id'=$1`, attemptID).Scan(&sentEvents); err != nil {
				t.Fatal(err)
			}
			if sentEvents != 0 {
				t.Fatalf("sent events=%d, want 0", sentEvents)
			}
		})
	}
}

func TestConcurrentReplayAuthorizationHasOneWinnerPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	var databaseNow time.Time
	if err := s.DB.QueryRow(`SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(
		t, s, "unknown", databaseNow.Add(-time.Second), databaseNow.Add(-31*time.Second), databaseNow.Add(time.Minute),
	)
	const workers = 20
	replayEvidence := m11SeedReplayEvidence(t)
	start := make(chan struct{})
	results := make(chan bool, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			marked, err := s.MarkAttemptSent(attemptID, fencingToken, true, time.Second, replayEvidence)
			results <- marked
			errorsCh <- err
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)
	winners := 0
	for marked := range results {
		if marked {
			winners++
		}
	}
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	var replayCount, sentEvents int
	if err := s.DB.QueryRow(`SELECT replay_count FROM execution_attempt WHERE id=$1`, attemptID).Scan(&replayCount); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT count(*) FROM events
		WHERE kind='execution_attempt_sent' AND payload->>'attempt_id'=$1 AND (payload->>'replay')::boolean`,
		attemptID).Scan(&sentEvents); err != nil {
		t.Fatal(err)
	}
	if winners != 1 || replayCount != 1 || sentEvents != 1 {
		t.Fatalf("winners=%d replay_count=%d sent_events=%d", winners, replayCount, sentEvents)
	}
}

func TestReplayAuthorizationAtomicallyRejectsDifferentProviderEvidencePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	var databaseNow time.Time
	if err := s.DB.QueryRow(`SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
		t.Fatal(err)
	}
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(
		t, s, "unknown", databaseNow.Add(-time.Second),
		databaseNow.Add(-31*time.Second), databaseNow.Add(time.Minute),
	)
	evidence := m11SeedReplayEvidence(t)
	evidence.AccountID = "different-account"
	marked, err := s.MarkAttemptSent(attemptID, fencingToken, true, time.Second, evidence)
	if err != nil || marked {
		t.Fatalf("marked=%v err=%v, want atomic evidence mismatch refusal", marked, err)
	}
	current, err := s.GetExecutionAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := s.GetLiveExecutionGate()
	if err != nil || current.ReplayCount != 0 || gate.UnknownAttemptID != attemptID || gate.ActiveAttemptID != "" {
		t.Fatalf("attempt=%+v gate=%+v err=%v, replay evidence was consumed or latch changed", current, gate, err)
	}
}

func TestInitialSendWindowStartsAfterGateWaitPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
	blocker, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	var singleton bool
	if err := blocker.QueryRow(`SELECT singleton FROM live_execution_gate
		WHERE singleton=true FOR UPDATE`).Scan(&singleton); err != nil || !singleton {
		t.Fatalf("lock live gate singleton=%v err=%v", singleton, err)
	}
	type result struct {
		marked bool
		err    error
	}
	done := make(chan result, 1)
	go func() {
		marked, markErr := s.MarkAttemptSent(attemptID, fencingToken, false, time.Second, nil)
		done <- result{marked: marked, err: markErr}
	}()
	waitForM11GlobalHaltSendLock(t, s)
	time.Sleep(75 * time.Millisecond)
	var releaseFloor time.Time
	if err := s.DB.QueryRow(`SELECT clock_timestamp()`).Scan(&releaseFloor); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil || !result.marked {
			t.Fatalf("marked=%v err=%v", result.marked, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for sent marker")
	}
	var sentAt, windowStart, windowEnd time.Time
	if err := s.DB.QueryRow(`SELECT sent_at,send_window_start,send_window_end
		FROM execution_attempt WHERE id=$1`, attemptID).Scan(&sentAt, &windowStart, &windowEnd); err != nil {
		t.Fatal(err)
	}
	if sentAt.Before(releaseFloor) || !windowStart.Equal(sentAt.Add(-30*time.Second)) ||
		!windowEnd.Equal(sentAt.Add(2*time.Minute)) {
		t.Fatalf("release_floor=%s sent_at=%s window=%s..%s", releaseFloor, sentAt, windowStart, windowEnd)
	}
}

func TestGlobalHaltAndSentMarkerLinearizeInDatabasePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	type markResult struct {
		marked bool
		err    error
	}
	type haltResult struct {
		transition GlobalHaltTransition
		err        error
	}

	t.Run("halt commits first", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		blocker, err := s.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback()
		if _, err := blocker.Exec(`LOCK TABLE events IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		haltDone := make(chan haltResult, 1)
		go func() {
			transition, haltErr := s.ActivateGlobalHalt("m11 halt-first", "test:m11", "live")
			haltDone <- haltResult{transition: transition, err: haltErr}
		}()
		waitForM11GlobalHaltSendLock(t, s)
		markDone := make(chan markResult, 1)
		go func() {
			marked, markErr := s.MarkAttemptSent(attemptID, fencingToken, false, time.Second, nil)
			markDone <- markResult{marked: marked, err: markErr}
		}()
		select {
		case result := <-markDone:
			t.Fatalf("sent marker crossed an uncommitted Halt cut: %+v", result)
		case <-time.After(75 * time.Millisecond):
		}
		if err := blocker.Commit(); err != nil {
			t.Fatal(err)
		}
		var halt haltResult
		select {
		case halt = <-haltDone:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for Halt to commit")
		}
		if halt.err != nil {
			t.Fatal(halt.err)
		}
		transition := halt.transition
		if transition.EventID == 0 || transition.Reason != "m11 halt-first" ||
			transition.InFlightAttemptID != "" || transition.InFlightAttemptState != "" ||
			transition.BlockedUnsentAttemptID != attemptID {
			t.Fatalf("halt-first transition=%+v", transition)
		}
		var mark markResult
		select {
		case mark = <-markDone:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for the blocked sent marker")
		}
		if mark.marked || !errors.Is(mark.err, ErrLiveSendHalted) {
			t.Fatalf("marked=%v err=%v, want live send halted", mark.marked, mark.err)
		}
		var sent bool
		if err := s.DB.QueryRow(`SELECT sent_at IS NOT NULL FROM execution_attempt WHERE id=$1`, attemptID).Scan(&sent); err != nil {
			t.Fatal(err)
		}
		if sent {
			t.Fatal("halt-first attempt acquired a durable sent marker")
		}
	})

	t.Run("sent marker commits first", func(t *testing.T) {
		resetM3AIntegrationData(t, s)
		attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
		blocker, err := s.DB.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback()
		var singleton bool
		if err := blocker.QueryRow(`SELECT singleton FROM live_execution_gate
			WHERE singleton=true FOR UPDATE`).Scan(&singleton); err != nil || !singleton {
			t.Fatalf("lock live execution gate singleton=%v err=%v", singleton, err)
		}
		markDone := make(chan markResult, 1)
		go func() {
			marked, markErr := s.MarkAttemptSent(attemptID, fencingToken, false, time.Second, nil)
			markDone <- markResult{marked: marked, err: markErr}
		}()
		waitForM11GlobalHaltSendLock(t, s)
		haltDone := make(chan haltResult, 1)
		go func() {
			transition, haltErr := s.ActivateGlobalHalt("m11 sent-first", "test:m11", "live")
			haltDone <- haltResult{transition: transition, err: haltErr}
		}()
		select {
		case result := <-haltDone:
			t.Fatalf("Halt crossed an uncommitted sent marker: %+v", result)
		case <-time.After(75 * time.Millisecond):
		}
		if err := blocker.Commit(); err != nil {
			t.Fatal(err)
		}
		var mark markResult
		select {
		case mark = <-markDone:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for sent marker to commit")
		}
		if mark.err != nil || !mark.marked {
			t.Fatalf("marked=%v err=%v", mark.marked, mark.err)
		}
		var halt haltResult
		select {
		case halt = <-haltDone:
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for Halt behind sent marker")
		}
		if halt.err != nil {
			t.Fatal(halt.err)
		}
		transition := halt.transition
		if transition.EventID == 0 || transition.Reason != "m11 sent-first" ||
			transition.InFlightAttemptID != attemptID || transition.InFlightAttemptState != "active" ||
			transition.BlockedUnsentAttemptID != "" {
			t.Fatalf("sent-first transition=%+v, want active in-flight attempt %s", transition, attemptID)
		}
		var sent bool
		if err := s.DB.QueryRow(`SELECT sent_at IS NOT NULL FROM execution_attempt WHERE id=$1`, attemptID).Scan(&sent); err != nil {
			t.Fatal(err)
		}
		if !sent {
			t.Fatal("sent-first attempt lost its durable sent marker")
		}
		halted, reason, err := s.LoadGlobalHalt()
		if err != nil || !halted || reason != "m11 sent-first" {
			t.Fatalf("halted=%v reason=%q err=%v", halted, reason, err)
		}
	})
}

func TestRequireLiveExecutionIdleReportsDurableGateStatePostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()

	tests := []struct {
		name            string
		gateState       string
		activateHalt    bool
		blockWhenHalted bool
		wantErr         error
	}{
		{name: "idle"},
		{name: "active is busy", gateState: "active", wantErr: ErrLiveExecutionBusy},
		{name: "unknown is suspended", gateState: "unknown", wantErr: ErrLiveExecutionSuspended},
		{name: "halt blocks opens", activateHalt: true, blockWhenHalted: true, wantErr: ErrLiveSendHalted},
		{name: "halt permits reduction admission", activateHalt: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetM3AIntegrationData(t, s)
			if test.gateState != "" {
				seedM11ClaimedOpenAttempt(t, s, test.gateState, time.Time{}, time.Time{}, time.Time{})
			}
			if test.activateHalt {
				if _, err := s.ActivateGlobalHalt("m11 idle gate", "test:m11", "live"); err != nil {
					t.Fatal(err)
				}
			}
			err := s.WithLedgerLock(false, func(gate OperationGate) error {
				return gate.RequireLiveExecutionIdle(test.blockWhenHalted)
			})
			if !errors.Is(err, test.wantErr) || (test.wantErr == nil && err != nil) {
				t.Fatalf("err=%v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestIntegrityHaltUsesTheSendCutPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	attemptID, fencingToken := seedM11ClaimedOpenAttempt(t, s, "active", time.Time{}, time.Time{}, time.Time{})
	cause := fmt.Errorf("%w: injected M11 integrity failure", ErrFillIntegrity)
	if err := s.recordIntegrityFailure("m11_integrity_probe", map[string]any{"attempt_id": attemptID}, cause); !errors.Is(err, ErrFillIntegrity) {
		t.Fatalf("integrity error=%v", err)
	}
	marked, err := s.MarkAttemptSent(attemptID, fencingToken, false, time.Second, nil)
	if marked || !errors.Is(err, ErrLiveSendHalted) {
		t.Fatalf("marked=%v err=%v", marked, err)
	}
	var integrityEvents, haltEvents int
	if err := s.DB.QueryRow(`SELECT count(*) FILTER (WHERE kind='m11_integrity_probe'),
		count(*) FILTER (WHERE kind='global_halt_transition') FROM events`).Scan(&integrityEvents, &haltEvents); err != nil {
		t.Fatal(err)
	}
	if integrityEvents != 1 || haltEvents != 1 {
		t.Fatalf("integrity_events=%d halt_events=%d", integrityEvents, haltEvents)
	}
}

func TestPartialFillReplacementHaltCleanupPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()

	for _, test := range []struct {
		name    string
		cleanup func(*testing.T, *Store, m11PartialFillReplacement)
	}{
		{
			name: "pending",
			cleanup: func(t *testing.T, s *Store, fixture m11PartialFillReplacement) {
				transition, err := s.ActivateGlobalHalt("halt pending replacement", "test:m11", "live")
				if err != nil {
					t.Fatal(err)
				}
				if transition.InFlightAttemptID != "" || transition.BlockedUnsentAttemptID != "" {
					t.Fatalf("pending replacement unexpectedly occupied live gate: %+v", transition)
				}
				updated, err := s.FailPendingAttempt(
					fixture.Replacement.ID, "order_expired_policy:ledger_halted_before_replacement",
				)
				if err != nil || !updated {
					t.Fatalf("pending cleanup updated=%v err=%v", updated, err)
				}
			},
		},
		{
			name: "claimed unsent",
			cleanup: func(t *testing.T, s *Store, fixture m11PartialFillReplacement) {
				claimed, err := s.ClaimPendingAttemptLive(fixture.Replacement.ID, "m11-halt-claimed", 30*time.Second)
				if err != nil || claimed == nil {
					t.Fatalf("claim replacement=%+v err=%v", claimed, err)
				}
				transition, err := s.ActivateGlobalHalt("halt claimed replacement", "test:m11", "live")
				if err != nil {
					t.Fatal(err)
				}
				if transition.InFlightAttemptID != "" || transition.BlockedUnsentAttemptID != claimed.ID {
					t.Fatalf("claimed-unsent transition=%+v", transition)
				}
				marked, markErr := s.MarkAttemptSent(claimed.ID, claimed.Attempt, false, time.Second, nil)
				if marked || !errors.Is(markErr, ErrLiveSendHalted) {
					t.Fatalf("marked=%v err=%v, want live send halted", marked, markErr)
				}
				updated, err := s.ResolveAttempt(claimed.ID, claimed.Attempt, AttemptResolution{
					State: "failed", LastError: "global halt committed before provider send",
					ProviderErrorCode: "send_suppressed_halt", ReleaseReservation: true,
					OrderUpdate: &OrderUpdate{ExecutionAttemptID: claimed.ID, State: "rejected"},
				})
				if err != nil || !updated {
					t.Fatalf("claimed cleanup updated=%v err=%v", updated, err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetM3AIntegrationData(t, s)
			fixture := seedM11PartialFillReplacement(t, s)
			operation, err := s.GetOperation(fixture.OperationID)
			if err != nil || operation.Status != "executed" {
				t.Fatalf("replacement was not staged as continuation of a real fill: operation=%+v err=%v", operation, err)
			}
			test.cleanup(t, s, fixture)
			assertM11PartialFillReplacementCleanup(t, s, fixture)
		})
	}
}

func TestHaltedClaimedReplacementRejectsStaleFencingTokenPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	fixture := seedM11PartialFillReplacement(t, s)
	stale, err := s.ClaimPendingAttemptLive(fixture.Replacement.ID, "m11-stale-worker", time.Nanosecond)
	if err != nil || stale == nil {
		t.Fatalf("stale claim=%+v err=%v", stale, err)
	}
	if _, err := s.ActivateGlobalHalt("halt fencing race", "test:m11", "live"); err != nil {
		t.Fatal(err)
	}
	winner, err := s.ClaimRecoverableAttemptLive(
		stale.ID, "m11-winning-worker", "claimed", stale.Attempt, 30*time.Second,
	)
	if err != nil || winner == nil || winner.Attempt != stale.Attempt+1 {
		t.Fatalf("winning claim=%+v stale=%+v err=%v", winner, stale, err)
	}

	updated, err := s.ResolveAttempt(stale.ID, stale.Attempt, AttemptResolution{
		State: "failed", LastError: "stale cleanup must not commit", OperationStatus: "failed",
		ReleaseReservation: true,
		OrderUpdate:        &OrderUpdate{ExecutionAttemptID: stale.ID, State: "rejected"},
	})
	if err != nil || updated {
		t.Fatalf("stale cleanup updated=%v err=%v", updated, err)
	}
	current, attemptErr := s.GetExecutionAttempt(winner.ID)
	order, orderErr := s.GetOrderByAttempt(winner.ID)
	reservation, reservationErr := s.GetOpenReservation(fixture.ReservationID)
	operation, operationErr := s.GetOperation(fixture.OperationID)
	gate, gateErr := s.GetLiveExecutionGate()
	if attemptErr != nil || orderErr != nil || reservationErr != nil || operationErr != nil || gateErr != nil ||
		current.State != "claimed" || current.Attempt != winner.Attempt || order.State != "new" ||
		reservation.ResourceState != "held" || reservation.RemainingQty != units.MustQty("2") ||
		reservation.RemainingRisk != units.MustMicros("220") || reservation.RemainingCash != units.MustMicros("220") ||
		operation.Status != "executed" || gate.ActiveAttemptID != winner.ID || gate.UnknownAttemptID != "" {
		t.Fatalf("stale resolve mutated winner: attempt=%+v order=%+v reservation=%+v operation=%+v gate=%+v errors=%v/%v/%v/%v/%v",
			current, order, reservation, operation, gate, attemptErr, orderErr, reservationErr, operationErr, gateErr)
	}

	updated, err = s.ResolveAttempt(winner.ID, winner.Attempt, AttemptResolution{
		State: "failed", LastError: "global halt committed before provider send",
		ProviderErrorCode: "send_suppressed_halt", ReleaseReservation: true,
		OrderUpdate: &OrderUpdate{ExecutionAttemptID: winner.ID, State: "rejected"},
	})
	if err != nil || !updated {
		t.Fatalf("winning cleanup updated=%v err=%v", updated, err)
	}
	assertM11PartialFillReplacementCleanup(t, s, fixture)
}

func TestLiveExecutionGateSerializesUnknownAndOneReplayPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	const workers = 20
	attemptIDs := make([]string, workers)
	for index := range attemptIDs {
		operationID := NewID()
		attemptIDs[index] = NewID()
		if err := s.InsertOperation(operationID, "m11-live-gate", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": false, "symbol": "SPY", "kind": "equity", "side": "buy",
		}, map[string]any{"class": "B"}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(`INSERT INTO execution_attempt
			(id,operation_id,seq,intent,client_order_id,state,qty,limit_micros)
			VALUES ($1,$2,1,'place',$3,'pending',$4,$5)`, attemptIDs[index], operationID, NewID(),
			int64(units.MustQty("1")), int64(units.MustMicros("1"))); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan *ExecutionAttempt, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for _, attemptID := range attemptIDs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			claimed, err := s.ClaimPendingAttemptLive(attemptID, "barrier-worker", 30*time.Second)
			results <- claimed
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
	var winner *ExecutionAttempt
	claimedCount := 0
	for claimed := range results {
		if claimed != nil {
			claimedCount++
			winner = claimed
		}
	}
	if claimedCount != 1 || winner == nil {
		t.Fatalf("claimed=%d winner=%+v", claimedCount, winner)
	}
	gate, err := s.GetLiveExecutionGate()
	if err != nil || gate.ActiveAttemptID != winner.ID || gate.UnknownAttemptID != "" {
		t.Fatalf("active gate=%+v err=%v", gate, err)
	}
	canonical, err := json.Marshal(map[string]any{"kind": "equity", "symbol": "SPY"})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	prepared, err := s.PrepareAttemptProviderIntent(winner.ID, winner.Attempt, "account-1", canonical, digest[:])
	if err != nil || !prepared {
		t.Fatalf("prepared=%v err=%v", prepared, err)
	}
	marked, err := s.MarkAttemptSent(winner.ID, winner.Attempt, false, time.Second, nil)
	if err != nil || !marked {
		t.Fatalf("marked=%v err=%v", marked, err)
	}
	resolved, err := s.ResolveAttempt(winner.ID, winner.Attempt, AttemptResolution{
		State: "unknown", LastError: "lost response", ProviderErrorCode: "call_failed",
	})
	if err != nil || !resolved {
		t.Fatalf("unknown resolved=%v err=%v", resolved, err)
	}
	gate, _ = s.GetLiveExecutionGate()
	if gate.ActiveAttemptID != "" || gate.UnknownAttemptID != winner.ID {
		t.Fatalf("unknown gate=%+v", gate)
	}
	for _, attemptID := range attemptIDs {
		if attemptID == winner.ID {
			continue
		}
		claimed, claimErr := s.ClaimPendingAttemptLive(attemptID, "blocked-worker", 30*time.Second)
		if claimErr != nil || claimed != nil {
			t.Fatalf("latched claim=%+v err=%v", claimed, claimErr)
		}
	}
	current, err := s.GetExecutionAttempt(winner.ID)
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := s.ClaimRecoverableAttemptLive(winner.ID, "recovery", "unknown", current.Attempt, time.Nanosecond)
	if err != nil || recovery == nil {
		t.Fatalf("recovery=%+v err=%v", recovery, err)
	}
	// A worker or its adoption transaction can die after claiming an unknown
	// attempt. The durable unknown latch must remain engaged, but the same
	// fenced attempt must become recoverable again after the claim lease.
	updated, resolveErr := s.ResolveAttempt(recovery.ID, recovery.Attempt, AttemptResolution{
		State: "placed", BrokerOrderID: NewID(),
		OrderUpdate: &OrderUpdate{
			ExecutionAttemptID: recovery.ID, BrokerOrderID: NewID(), State: "submitted",
		},
	})
	if resolveErr == nil || updated {
		t.Fatalf("injected adoption rollback updated=%v err=%v", updated, resolveErr)
	}
	current, err = s.GetExecutionAttempt(recovery.ID)
	if err != nil || current.State != "claimed" || current.Attempt != recovery.Attempt {
		t.Fatalf("rolled-back recovery=%+v err=%v", current, err)
	}
	gate, err = s.GetLiveExecutionGate()
	if err != nil || gate.UnknownAttemptID != recovery.ID || gate.ActiveAttemptID != "" {
		t.Fatalf("rolled-back gate=%+v err=%v", gate, err)
	}
	recovery, err = s.ClaimRecoverableAttemptLive(
		current.ID, "recovery-after-crash", "claimed", current.Attempt, 30*time.Second,
	)
	if err != nil || recovery == nil {
		t.Fatalf("reclaim unknown worker=%+v err=%v", recovery, err)
	}
	gate, err = s.GetLiveExecutionGate()
	if err != nil || gate.UnknownAttemptID != recovery.ID || gate.ActiveAttemptID != "" {
		t.Fatalf("reclaimed gate=%+v err=%v", gate, err)
	}
	replayEvidence := &ProviderIntentEvidence{AccountID: "account-1", Canonical: canonical, Fingerprint: digest[:]}
	marked, err = s.MarkAttemptSent(recovery.ID, recovery.Attempt, true, time.Second, replayEvidence)
	if err != nil || !marked {
		t.Fatalf("replay marked=%v err=%v", marked, err)
	}
	marked, err = s.MarkAttemptSent(recovery.ID, recovery.Attempt, true, time.Second, replayEvidence)
	if err != nil || marked {
		t.Fatalf("second replay marked=%v err=%v", marked, err)
	}
	resolved, err = s.ResolveAttempt(recovery.ID, recovery.Attempt, AttemptResolution{
		State: "settled", BrokerOrderID: NewID(), CandidateBrokerOrderID: NewID(),
	})
	if err != nil || !resolved {
		t.Fatalf("settled=%v err=%v", resolved, err)
	}
	gate, _ = s.GetLiveExecutionGate()
	if gate.ActiveAttemptID != "" || gate.UnknownAttemptID != "" {
		t.Fatalf("cleared gate=%+v", gate)
	}
}

func TestCandidateAdoptionCommitsFillAndClearsLatchAtomicallyPostgres(t *testing.T) {
	s := openM11IntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	operationID, reservationID := NewID(), NewID()
	attemptID, orderID, clientID := NewID(), NewID(), NewID()
	marketDay := time.Date(2026, time.July, 17, 0, 0, 0, 0, time.UTC)
	if err := s.InsertOperation(operationID, "m11-adoption", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M11-ADOPT", "kind": "equity",
		"side": "buy", "qty": 1, "multiplier": 1,
		"derived_max_risk": 10, "required_cash": 10,
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: marketDay,
			AuthorizedRisk: units.MustMicros("10"), RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", MarketDay: marketDay,
			Symbol: "M11-ADOPT", Kind: "equity",
			OriginalQty: units.MustQty("1"), RemainingQty: units.MustQty("1"),
			OriginalRisk: units.MustMicros("10"), RemainingRisk: units.MustMicros("10"),
			OriginalCash: units.MustMicros("10"), RemainingCash: units.MustMicros("10"),
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, OpenReservationID: reservationID,
			Intent: "place", ClientOrderID: clientID, State: "pending",
			Qty: units.MustQty("1"), Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "M11-ADOPT", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"),
			Limit: units.MustMicros("10"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.ClaimPendingAttemptLive(attemptID, "m11-adoption-send", 30*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim=%+v err=%v", claimed, err)
	}
	canonical, err := json.Marshal(map[string]any{"kind": "equity", "symbol": "M11-ADOPT"})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	prepared, err := s.PrepareAttemptProviderIntent(claimed.ID, claimed.Attempt, "account-1", canonical, digest[:])
	if err != nil || !prepared {
		t.Fatalf("prepared=%v err=%v", prepared, err)
	}
	marked, err := s.MarkAttemptSent(claimed.ID, claimed.Attempt, false, time.Second, nil)
	if err != nil || !marked {
		t.Fatalf("marked=%v err=%v", marked, err)
	}
	resolved, err := s.ResolveAttempt(claimed.ID, claimed.Attempt, AttemptResolution{
		State: "unknown", LastError: "lost placement response", ProviderErrorCode: "call_failed",
	})
	if err != nil || !resolved {
		t.Fatalf("unknown resolved=%v err=%v", resolved, err)
	}
	unknown, err := s.GetExecutionAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := s.ClaimRecoverableAttemptLive(
		unknown.ID, "m11-adoption-admin", "unknown", unknown.Attempt, 30*time.Second,
	)
	if err != nil || adoption == nil {
		t.Fatalf("adoption=%+v err=%v", adoption, err)
	}
	brokerOrderID, brokerFillID := NewID(), NewID()
	resolved, err = s.ResolveAttempt(adoption.ID, adoption.Attempt, AttemptResolution{
		State: "settled", BrokerOrderID: brokerOrderID,
		CandidateBrokerOrderID: brokerOrderID, OperatorSubject: "admin:m11-test",
		OperationStatus: "executed",
		OrderUpdate: &OrderUpdate{
			ExecutionAttemptID: adoption.ID, BrokerOrderID: brokerOrderID,
			State: "filled", FilledQty: units.MustQty("1"),
			Fills: []FillInput{{
				BrokerFillID: brokerFillID, Qty: units.MustQty("1"),
				Price: units.MustMicros("9"), TS: time.Now().UTC(),
			}},
		},
	})
	if err != nil || !resolved {
		t.Fatalf("adopt resolved=%v err=%v", resolved, err)
	}

	current, err := s.GetExecutionAttempt(attemptID)
	order, orderErr := s.GetOrderByAttempt(attemptID)
	reservation, reservationErr := s.GetOpenReservation(reservationID)
	gate, gateErr := s.GetLiveExecutionGate()
	var fillCount int
	fillErr := s.DB.QueryRow(`SELECT count(*) FROM fills
		WHERE order_id=$1 AND broker_fill_id=$2`, orderID, brokerFillID).Scan(&fillCount)
	if err != nil || orderErr != nil || reservationErr != nil || gateErr != nil || fillErr != nil ||
		current.State != "settled" || current.BrokerOrderID != brokerOrderID ||
		current.CandidateBrokerOrderID != brokerOrderID || order.State != "filled" ||
		order.BrokerOrderID != brokerOrderID || reservation.ResourceState != "converted" ||
		reservation.RemainingQty != 0 || fillCount != 1 || gate.ActiveAttemptID != "" ||
		gate.UnknownAttemptID != "" {
		t.Fatalf("attempt=%+v order=%+v reservation=%+v gate=%+v fills=%d errors=%v/%v/%v/%v/%v",
			current, order, reservation, gate, fillCount, err, orderErr, reservationErr, gateErr, fillErr)
	}
}

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
			err := s.WithLedgerLock(false, func(gate OperationGate) error {
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

	assertUsage := func(exclude string, wantRisk units.Micros, wantLegacy, wantUnbound bool, wantCount int) {
		t.Helper()
		err := s.WithLedgerLock(false, func(gate OperationGate) error {
			usage, err := gate.TradeGrantUsage("live", marketDay, exclude)
			if err != nil {
				return err
			}
			if usage.AuthorizedRisk != wantRisk || usage.HasLegacyUnknown != wantLegacy ||
				usage.HasUnboundCanary != wantUnbound || usage.GrantCount != wantCount {
				t.Fatalf("usage=%+v, want risk=%s legacy=%v unbound=%v count=%d",
					usage, wantRisk, wantLegacy, wantUnbound, wantCount)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	assertUsage("", units.MustMicros("35"), false, true, 2)
	assertUsage(computedOne, units.MustMicros("25"), false, true, 1)
	legacy := seedM11Grant(t, s, "live", marketDay, "legacy_unknown", 0)
	assertUsage("", units.MustMicros("35"), true, true, 3)
	assertUsage(legacy, units.MustMicros("35"), false, true, 2)
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
	err := s.WithLedgerLock(shadow, func(gate OperationGate) error {
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

func seedM11ClaimedOpenAttempt(
	t *testing.T,
	s *Store,
	gateState string,
	sentAt, windowStart, windowEnd time.Time,
) (string, int) {
	t.Helper()
	operationID, attemptID, clientOrderID := NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "m11-v1.7-integration", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M11-V17", "kind": "equity", "side": "buy",
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(map[string]any{
		"account_id": "account-m11", "kind": "equity", "symbol": "M11-V17",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	const fencingToken = 1
	var persistedSentAt, persistedWindowStart, persistedWindowEnd any
	if !sentAt.IsZero() {
		persistedSentAt = sentAt
		persistedWindowStart = windowStart
		persistedWindowEnd = windowEnd
	}
	if _, err := s.DB.Exec(`INSERT INTO execution_attempt
		(id,operation_id,seq,intent,client_order_id,state,qty,limit_micros,attempt,claimed_by,claimed_at,
		 intent_fingerprint,provider_account_id,provider_intent,sent_at,send_window_start,send_window_end)
		VALUES ($1,$2,1,'place',$3,'claimed',$4,$5,$6,'m11-v1.7-test',clock_timestamp(),
		 $7,$8,$9::jsonb,$10,$11,$12)`,
		attemptID, operationID, clientOrderID, int64(units.MustQty("1")), int64(units.MustMicros("1")), fencingToken,
		digest[:], "account-m11", string(canonical), persistedSentAt, persistedWindowStart, persistedWindowEnd,
	); err != nil {
		t.Fatal(err)
	}
	switch gateState {
	case "active":
		if _, err := s.DB.Exec(`UPDATE live_execution_gate SET
			active_attempt_id=$1,active_since=clock_timestamp(),unknown_attempt_id=NULL,unknown_since=NULL,updated_at=clock_timestamp()
			WHERE singleton=true`, attemptID); err != nil {
			t.Fatal(err)
		}
	case "unknown":
		if _, err := s.DB.Exec(`UPDATE live_execution_gate SET
			active_attempt_id=NULL,active_since=NULL,unknown_attempt_id=$1,unknown_since=clock_timestamp(),updated_at=clock_timestamp()
			WHERE singleton=true`, attemptID); err != nil {
			t.Fatal(err)
		}
	case "":
	default:
		t.Fatalf("invalid live execution gate state %q", gateState)
	}
	return attemptID, fencingToken
}

func m11SeedReplayEvidence(t *testing.T) *ProviderIntentEvidence {
	t.Helper()
	canonical, err := json.Marshal(map[string]any{
		"account_id": "account-m11", "kind": "equity", "symbol": "M11-V17",
	})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	return &ProviderIntentEvidence{
		AccountID: "account-m11", Canonical: canonical, Fingerprint: digest[:],
	}
}

type m11PartialFillReplacement struct {
	OperationID, ReservationID, SourceAttemptID, SourceOrderID, FillID string
	Replacement                                                        *ExecutionAttempt
}

func seedM11PartialFillReplacement(t *testing.T, s *Store) m11PartialFillReplacement {
	t.Helper()
	if err := s.ActivateM3A(M3AActivationSnapshot{
		Equity: units.MustMicros("100000"), BuyingPower: units.MustMicros("100000"),
	}); err != nil {
		t.Fatal(err)
	}
	operationID, reservationID := NewID(), NewID()
	placeAttemptID, sourceOrderID, clientOrderID := NewID(), NewID(), NewID()
	quantity, reserved := units.MustQty("4"), units.MustMicros("440")
	if err := s.InsertOperation(operationID, "m11-replacement-halt", "B", "auto_approved", map[string]any{
		"action": "open", "shadow": false, "symbol": "M11-RP", "kind": "equity",
		"side": "buy", "qty": quantity, "multiplier": 1,
		"approved_price_cap": units.MustMicros("110"),
	}, map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithLedgerLock(false, func(gate OperationGate) error {
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: time.Now().UTC(),
			AuthorizedRisk: reserved, RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", MarketDay: time.Now().UTC(),
			Symbol: "M11-RP", Kind: "equity", OriginalQty: quantity, RemainingQty: quantity,
			OriginalRisk: reserved, RemainingRisk: reserved, OriginalCash: reserved, RemainingCash: reserved,
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: placeAttemptID, OperationID: operationID, Seq: 1, OpenReservationID: reservationID,
			Intent: "place", ClientOrderID: clientOrderID, State: "placed",
			Qty: quantity, Limit: units.MustMicros("105"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: sourceOrderID, OperationID: operationID, ExecutionAttemptID: placeAttemptID,
			ClientOrderID: clientOrderID, Ledger: "live", Symbol: "M11-RP", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: quantity, Limit: units.MustMicros("105"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	brokerOrderID := "m11-source-" + sourceOrderID
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: placeAttemptID, BrokerOrderID: brokerOrderID,
		State: "submitted", FilledQty: 0,
	}); err != nil {
		t.Fatal(err)
	}
	cancelAttempt, err := s.StageRepriceCancel(sourceOrderID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel=%+v err=%v", cancelAttempt, err)
	}
	claimedCancel, err := s.ClaimPendingAttempt(cancelAttempt.ID, "m11-reprice-cancel", 30*time.Second)
	if err != nil || claimedCancel == nil {
		t.Fatalf("claim cancel=%+v err=%v", claimedCancel, err)
	}
	fillID := NewID()
	replacement := RepriceReplacement{
		AttemptID: NewID(), OrderID: NewID(), ClientOrderID: NewID(), Limit: units.MustMicros("108"),
	}
	next, err := s.FinalizeRepriceCancel(claimedCancel.ID, claimedCancel.Attempt, OrderUpdate{
		BrokerOrderID: brokerOrderID, State: "cancelled", FilledQty: units.MustQty("2"),
		Fills: []FillInput{{
			BrokerFillID: fillID, Qty: units.MustQty("2"), Price: units.MustMicros("105"), TS: time.Now().UTC(),
		}},
	}, &replacement, "")
	if err != nil || next == nil {
		t.Fatalf("finalize partial cancel next=%+v err=%v", next, err)
	}
	return m11PartialFillReplacement{
		OperationID: operationID, ReservationID: reservationID,
		SourceAttemptID: placeAttemptID, SourceOrderID: sourceOrderID,
		FillID: fillID, Replacement: next,
	}
}

func assertM11PartialFillReplacementCleanup(t *testing.T, s *Store, fixture m11PartialFillReplacement) {
	t.Helper()
	attempt, attemptErr := s.GetExecutionAttempt(fixture.Replacement.ID)
	replacementOrder, replacementErr := s.GetOrderByAttempt(fixture.Replacement.ID)
	sourceOrder, sourceErr := s.GetOrderByAttempt(fixture.SourceAttemptID)
	reservation, reservationErr := s.GetOpenReservation(fixture.ReservationID)
	operation, operationErr := s.GetOperation(fixture.OperationID)
	gate, gateErr := s.GetLiveExecutionGate()
	resources, resourcesErr := s.LedgerResources("live", "")
	if attemptErr != nil || replacementErr != nil || sourceErr != nil || reservationErr != nil ||
		operationErr != nil || gateErr != nil || resourcesErr != nil {
		t.Fatalf("cleanup reads failed: %v/%v/%v/%v/%v/%v/%v",
			attemptErr, replacementErr, sourceErr, reservationErr, operationErr, gateErr, resourcesErr)
	}
	if attempt.State != "failed" || !attempt.SentAt.IsZero() || attempt.ResolvedAt.IsZero() ||
		replacementOrder.State != "rejected" || replacementOrder.BrokerOrderID != "" ||
		sourceOrder.State != "cancelled" || sourceOrder.BrokerOrderID == "" || operation.Status != "executed" ||
		reservation.ResourceState != "released" || reservation.RemainingQty != units.MustQty("2") ||
		reservation.RemainingRisk != 0 || reservation.RemainingCash != 0 || reservation.SettledAt.IsZero() ||
		gate.ActiveAttemptID != "" || gate.UnknownAttemptID != "" ||
		resources.OpenRisk != units.MustMicros("210") || resources.HeldCash != 0 {
		t.Fatalf("attempt=%+v replacement=%+v source=%+v reservation=%+v operation=%+v gate=%+v resources=%+v",
			attempt, replacementOrder, sourceOrder, reservation, operation, gate, resources)
	}

	var attempts, settledAttempts, failedAttempts int
	if err := s.DB.QueryRow(`SELECT count(*),count(*) FILTER (WHERE state='settled'),
		count(*) FILTER (WHERE state='failed') FROM execution_attempt WHERE operation_id=$1`,
		fixture.OperationID).Scan(&attempts, &settledAttempts, &failedAttempts); err != nil {
		t.Fatal(err)
	}
	var grants int
	var grantRisk int64
	if err := s.DB.QueryRow(`SELECT count(*),COALESCE(sum(authorized_risk_micros),0)
		FROM trade_grant WHERE operation_id=$1`, fixture.OperationID).Scan(&grants, &grantRisk); err != nil {
		t.Fatal(err)
	}
	var fills int
	var fillQty int64
	if err := s.DB.QueryRow(`SELECT count(*),COALESCE(sum(f.qty),0) FROM fills f
		JOIN orders o ON o.id=f.order_id WHERE o.operation_id=$1 AND f.broker_fill_id=$2`,
		fixture.OperationID, fixture.FillID).Scan(&fills, &fillQty); err != nil {
		t.Fatal(err)
	}
	var lots int
	var openedQty, closedQty, remainingRisk int64
	if err := s.DB.QueryRow(`SELECT count(*),COALESCE(sum(opened_qty),0),COALESCE(sum(closed_qty),0),
		COALESCE(sum(remaining_risk_micros),0) FROM exposure_lot WHERE operation_id=$1`,
		fixture.OperationID).Scan(&lots, &openedQty, &closedQty, &remainingRisk); err != nil {
		t.Fatal(err)
	}
	if attempts != 3 || settledAttempts != 2 || failedAttempts != 1 ||
		grants != 1 || grantRisk != int64(units.MustMicros("440")) ||
		fills != 1 || fillQty != int64(units.MustQty("2")) || lots != 1 ||
		openedQty != int64(units.MustQty("2")) || closedQty != 0 || remainingRisk != int64(units.MustMicros("210")) {
		t.Fatalf("attempts=%d/%d/%d grant=%d/%d fills=%d/%d lots=%d/%d/%d/%d",
			attempts, settledAttempts, failedAttempts, grants, grantRisk, fills, fillQty,
			lots, openedQty, closedQty, remainingRisk)
	}
}

func waitForM11GlobalHaltSendLock(t *testing.T, s *Store) {
	t.Helper()
	key := uint64(globalHaltSendLockKey())
	classID, objectID := int64(uint32(key>>32)), int64(uint32(key))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var held bool
		if err := s.DB.QueryRow(`SELECT EXISTS (
			SELECT 1 FROM pg_locks
			WHERE locktype='advisory' AND classid=$1::oid AND objid=$2::oid
			  AND objsubid=1 AND granted
		)`, classID, objectID).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if held {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for global Halt/send advisory lock owner")
}
