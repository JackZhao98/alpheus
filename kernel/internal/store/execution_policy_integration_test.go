package store

import (
	"bytes"
	"testing"
	"time"

	"alpheus/kernel/internal/policy"
	"alpheus/kernel/internal/units"
)

func TestExecutionFactsInheritImmutablePolicyEnvelopePostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	base := testKernelPolicy(t)
	initial, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: base, ExpectedGeneration: 0, RecordedBy: "test:k1b2", Reason: "initial envelope authority",
	})
	if err != nil {
		t.Fatal(err)
	}

	operationID, reservationID, attemptID, orderID := NewID(), NewID(), NewID(), NewID()
	quantity := units.MustQty("2")
	riskAmount := units.MustMicros("220")
	priceCap := units.MustMicros("110")
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		authority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		if _, err := gate.InsertOperationBound(operationID, "k1b2", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": false, "symbol": "K1B2", "kind": "equity",
			"side": "buy", "qty": quantity, "multiplier": 1,
			"approved_price_cap": priceCap,
		}, map[string]any{"class": "B"}, nil, authority); err != nil {
			return err
		}
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: time.Now(),
			AuthorizedRisk: riskAmount, RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", MarketDay: time.Now(),
			Symbol: "K1B2", Kind: "equity", OriginalQty: quantity, RemainingQty: quantity,
			OriginalRisk: riskAmount, RemainingRisk: riskAmount,
			OriginalCash: riskAmount, RemainingCash: riskAmount, ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, OpenReservationID: reservationID,
			Intent: "place", ClientOrderID: NewID(), State: "pending", Qty: quantity,
			Limit: units.MustMicros("105"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: NewID(), Ledger: "live", Symbol: "K1B2", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: quantity, Limit: units.MustMicros("105"),
			ApprovedPriceBound: priceCap, State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	closeOperationID, closeReservationID := NewID(), NewID()
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		authority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		if _, err := gate.InsertOperationBound(closeOperationID, "k1b2", "A", "auto_approved", map[string]any{
			"action": "close", "shadow": false, "symbol": "K1B2", "kind": "equity",
			"side": "sell", "qty": quantity, "multiplier": 1, "limit": units.MustMicros("100"),
		}, map[string]any{"class": "A"}, nil, authority); err != nil {
			return err
		}
		return gate.InsertCloseReservation(CloseReservation{
			ID: closeReservationID, OperationID: closeOperationID, Ledger: "live", Symbol: "K1B2",
			OriginalQty: quantity, RemainingQty: quantity, State: "held",
		})
	}); err != nil {
		t.Fatal(err)
	}

	operation, err := s.GetOperation(operationID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.GetExecutionAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	order, err := s.GetOrderByAttempt(attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.KernelPolicyRevisionID != initial.ID || attempt.KernelPolicyGeneration != 1 ||
		attempt.KernelPolicyDigest != initial.Digest || !attempt.AuthorizationExpiresAt.Equal(operation.ExpiresAt) ||
		attempt.MaxReprices != base.ExecutionPolicy.MaxReprices ||
		attempt.RepriceIntervalSec != base.ExecutionPolicy.RepriceIntervalSec ||
		attempt.QuoteMaxAgeSec != base.QuoteMaxAgeSec {
		t.Fatalf("attempt envelope=%+v operation=%+v authority=%+v", attempt, operation, initial)
	}
	if order.KernelPolicyRevisionID != initial.ID || order.KernelPolicyGeneration != 1 ||
		order.KernelPolicyDigest != initial.Digest || !order.AuthorizationExpiresAt.Equal(operation.ExpiresAt) ||
		order.ApprovedPriceBound != priceCap || order.MaxReprices != base.ExecutionPolicy.MaxReprices ||
		order.RepriceIntervalSec != base.ExecutionPolicy.RepriceIntervalSec ||
		order.QuoteMaxAgeSec != base.QuoteMaxAgeSec {
		t.Fatalf("order envelope=%+v", order)
	}
	for _, target := range []struct {
		table       string
		operationID string
	}{
		{"trade_grant", operationID}, {"open_reservation", operationID},
		{"execution_attempt", operationID}, {"orders", operationID},
		{"close_reservation", closeOperationID},
	} {
		var revision, generation int64
		var digest []byte
		if err := s.DB.QueryRow(`SELECT kernel_policy_revision_id,kernel_policy_generation,kernel_policy_digest
			FROM `+target.table+` WHERE operation_id=$1`, target.operationID).Scan(&revision, &generation, &digest); err != nil {
			t.Fatalf("%s binding: %v", target.table, err)
		}
		if revision != initial.ID || generation != 1 || !bytes.Equal(digest, initial.digestBytes) {
			t.Fatalf("%s binding=%d/%d/%x", target.table, revision, generation, digest)
		}
	}
	if _, err := s.DB.Exec(`UPDATE orders SET max_reprices=max_reprices+1 WHERE id=$1`, orderID); err == nil {
		t.Fatal("bound order envelope was mutable")
	}
	if _, err := s.DB.Exec(`UPDATE execution_attempt SET quote_max_age_sec=quote_max_age_sec+1 WHERE id=$1`, attemptID); err == nil {
		t.Fatal("bound attempt envelope was mutable")
	}
	if _, err := s.DB.Exec(`UPDATE operations SET expires_at=expires_at+interval '1 hour' WHERE id=$1`, operationID); err == nil {
		t.Fatal("bound operation expiry was mutable")
	}
	if _, err := s.DB.Exec(`UPDATE operations SET payload=jsonb_set(payload,'{symbol}',to_jsonb('TAMPERED'::text)) WHERE id=$1`, operationID); err == nil {
		t.Fatal("bound operation payload was mutable")
	}

	wide := base
	wide.ExecutionPolicy.MaxReprices++
	wide.QuoteMaxAgeSec++
	wide.ProposalTTLSec++
	if _, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: wide, ExpectedGeneration: 1, RecordedBy: "test:k1b2", Reason: "later widening",
	}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.GetOrderByAttempt(attemptID)
	if err != nil || unchanged.MaxReprices != base.ExecutionPolicy.MaxReprices ||
		unchanged.QuoteMaxAgeSec != base.QuoteMaxAgeSec ||
		!unchanged.AuthorizationExpiresAt.Equal(operation.ExpiresAt) {
		t.Fatalf("old order envelope widened: order=%+v err=%v", unchanged, err)
	}
}

func TestClaimLeaseUsesPersistedDatabaseExpiryPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	base := testKernelPolicy(t)
	if _, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: base, ExpectedGeneration: 0, RecordedBy: "test:k1b2", Reason: "lease authority",
	}); err != nil {
		t.Fatal(err)
	}
	operationID, attemptID := NewID(), NewID()
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		authority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		if _, err := gate.InsertOperationBound(operationID, "k1b2", "A", "auto_approved",
			map[string]any{"action": "cancel", "broker_order_id": "target"},
			map[string]any{"class": "A"}, nil, authority); err != nil {
			return err
		}
		return gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, Intent: "cancel",
			TargetBrokerOrderID: "target", State: "pending",
		})
	}); err != nil {
		t.Fatal(err)
	}

	first, err := s.ClaimPendingAttempt(attemptID, "slow-instance", 150*time.Millisecond)
	if err != nil || first == nil {
		t.Fatalf("first claim=%+v err=%v", first, err)
	}
	leaseLength := first.LeaseExpiresAt.Sub(first.ClaimedAt)
	if leaseLength < 149*time.Millisecond || leaseLength > 151*time.Millisecond {
		t.Fatalf("database lease length=%s", leaseLength)
	}
	stolen, err := s.ClaimRecoverableAttempt(attemptID, "fast-instance", "claimed", first.Attempt, time.Millisecond)
	if err != nil || stolen != nil {
		t.Fatalf("second instance redefined active lease: claim=%+v err=%v", stolen, err)
	}
	if _, err := s.DB.Exec(`SELECT pg_sleep(0.18)`); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.ClaimRecoverableAttempt(attemptID, "fast-instance", "claimed", first.Attempt, 2*time.Second)
	if err != nil || reclaimed == nil || reclaimed.Attempt != first.Attempt+1 {
		t.Fatalf("expired lease reclaim=%+v err=%v", reclaimed, err)
	}
	if leaseLength := reclaimed.LeaseExpiresAt.Sub(reclaimed.ClaimedAt); leaseLength < 1999*time.Millisecond || leaseLength > 2001*time.Millisecond {
		t.Fatalf("new owner lease length=%s", leaseLength)
	}
}

func TestCurrentRepriceTighteningStopsBoundReplacementPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	base := testKernelPolicy(t)
	base.ExecutionPolicy.MaxReprices = 3
	initial, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: base, ExpectedGeneration: 0, RecordedBy: "test:k1b2", Reason: "working order authority",
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID, reservationID, attemptID, orderID, clientID := NewID(), NewID(), NewID(), NewID(), NewID()
	quantity, priceCap := units.MustQty("1"), units.MustMicros("110")
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		authority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		if _, err := gate.InsertOperationBound(operationID, "k1b2", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": false, "symbol": "K1B2-TIGHT", "kind": "equity",
			"side": "buy", "qty": quantity, "multiplier": 1, "approved_price_cap": priceCap,
		}, map[string]any{"class": "B"}, nil, authority); err != nil {
			return err
		}
		if err := gate.InsertTradeGrant(TradeGrant{
			OperationID: operationID, Ledger: "live", MarketDay: time.Now(),
			AuthorizedRisk: priceCap, RiskSource: "computed",
		}); err != nil {
			return err
		}
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", MarketDay: time.Now(),
			Symbol: "K1B2-TIGHT", Kind: "equity", OriginalQty: quantity, RemainingQty: quantity,
			OriginalRisk: priceCap, RemainingRisk: priceCap, OriginalCash: priceCap,
			RemainingCash: priceCap, ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, OpenReservationID: reservationID,
			Intent: "place", ClientOrderID: clientID, State: "placed", Qty: quantity,
			Limit: units.MustMicros("105"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "K1B2-TIGHT", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: quantity, Limit: units.MustMicros("105"),
			ApprovedPriceBound: priceCap, State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	brokerOrderID := "k1b2-working-order"
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: attemptID, BrokerOrderID: brokerOrderID, State: "submitted",
	}); err != nil {
		t.Fatal(err)
	}
	cancelAttempt, err := s.StageRepriceCancel(orderID)
	if err != nil || cancelAttempt == nil {
		t.Fatalf("stage cancel=%+v err=%v", cancelAttempt, err)
	}
	claimed, err := s.ClaimPendingAttempt(cancelAttempt.ID, "k1b2", time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("claim cancel=%+v err=%v", claimed, err)
	}
	tight := base
	tight.ExecutionPolicy.MaxReprices = 0
	if _, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: tight, ExpectedGeneration: initial.Generation,
		RecordedBy: "test:k1b2", Reason: "stop future replacement",
	}); err != nil {
		t.Fatal(err)
	}
	next, err := s.FinalizeRepriceCancel(claimed.ID, claimed.Attempt, OrderUpdate{
		BrokerOrderID: brokerOrderID, State: "cancelled",
	}, &RepriceReplacement{
		AttemptID: NewID(), OrderID: NewID(), ClientOrderID: NewID(), Limit: units.MustMicros("108"),
	}, "")
	if err != nil || next != nil {
		t.Fatalf("tightened replacement next=%+v err=%v", next, err)
	}
	reservation, err := s.GetOpenReservation(reservationID)
	if err != nil || reservation.ResourceState != "released" || reservation.RemainingRisk != 0 {
		t.Fatalf("reservation=%+v err=%v", reservation, err)
	}
	var replacements, policyExpiries int
	if err := s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM orders WHERE operation_id=$1 AND id<>$2),
		(SELECT count(*) FROM events WHERE kind='order_expired_policy'
		  AND payload->>'operation_id'=$1::text AND payload->>'reason'='max_reprices')`,
		operationID, orderID).Scan(&replacements, &policyExpiries); err != nil {
		t.Fatal(err)
	}
	if replacements != 0 || policyExpiries != 1 {
		t.Fatalf("replacements=%d policy_expiries=%d", replacements, policyExpiries)
	}
}

func TestExecutionPolicyEnvelopeSchemaRemainsVersioned(t *testing.T) {
	// Keep this file linked to the typed policy package so future schema-version
	// changes must deliberately update the envelope migration and its probes.
	if policy.SchemaVersion != 1 {
		t.Fatalf("update K1B-2 envelope probes for policy schema %d", policy.SchemaVersion)
	}
}
