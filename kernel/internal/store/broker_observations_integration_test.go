package store

import (
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"alpheus/kernel/internal/units"
)

func TestBrokerObservationOriginAndHeadAreEvidenceBackedPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	policy := testKernelPolicy(t)
	if _, err := s.RecordKernelPolicyRevision(RecordKernelPolicyRevisionInput{
		Policy: policy, ExpectedGeneration: 0, RecordedBy: "test:b0", Reason: "observation authority",
	}); err != nil {
		t.Fatal(err)
	}

	operationID, attemptID, orderID, clientID := NewID(), NewID(), NewID(), NewID()
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		authority, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		if _, err := gate.InsertOperationBound(operationID, "b0", "B", "auto_approved", map[string]any{
			"action": "open", "shadow": false, "symbol": "OWNED", "kind": "equity",
			"side": "buy", "qty": units.MustQty("1"), "multiplier": 1,
			"approved_price_cap": units.MustMicros("10"),
		}, map[string]any{"class": "B"}, nil, authority); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, Intent: "place",
			ClientOrderID: clientID, State: "pending", Qty: units.MustQty("1"), Limit: units.MustMicros("10"),
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: "OWNED", Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: units.MustQty("1"), Limit: units.MustMicros("10"),
			ApprovedPriceBound: units.MustMicros("10"), State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	providerIntent, err := json.Marshal(map[string]any{"position_effect": "open"})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(providerIntent)
	if _, err := s.DB.Exec(`UPDATE execution_attempt SET provider_account_id=$2,
		provider_intent=$3,intent_fingerprint=$4 WHERE id=$1`,
		attemptID, "account-b0", providerIntent, fingerprint[:]); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyOrderUpdate(OrderUpdate{
		ExecutionAttemptID: attemptID, BrokerOrderID: "broker-owned", State: "submitted",
	}); err != nil {
		t.Fatal(err)
	}

	started := time.Now().UTC().Add(-time.Second)
	completed := time.Now().UTC()
	observation, err := s.RecordBrokerObservation(BrokerObservationInput{
		AccountID: "account-b0", Source: "fixture", Purpose: "decision",
		StartedAt: started, CompletedAt: completed,
		Families: []BrokerObservationFamilyInput{
			{
				Family: BrokerFamilyAccount, Status: "success", CompletedAt: completed,
				Items: []BrokerObservationItemInput{{
					ObjectKey: "account-b0", ObservedAt: completed,
					Canonical: map[string]any{"account_id": "account-b0", "buying_power": units.MustMicros("100")},
				}},
			},
			{
				Family: BrokerFamilyPositions, Status: "success", CompletedAt: completed,
				Items: []BrokerObservationItemInput{{
					ObjectKey: "equity:MANUAL", ObservedAt: completed,
					Canonical: observedPositionIdentity{PositionID: "equity:MANUAL", Symbol: "MANUAL", Kind: "equity", Qty: units.MustQty("2")},
				}},
			},
			{
				Family: BrokerFamilyOrders, Status: "success", CompletedAt: completed,
				Items: []BrokerObservationItemInput{
					{
						ObjectKey: "broker-owned", ObservedAt: completed,
						Canonical: observedOrderIdentity{
							BrokerOrderID: "broker-owned", Symbol: "OWNED", Side: "buy", Kind: "equity",
							PositionEffect: "unknown", Qty: units.MustQty("1"),
							LimitPrice: units.MustMicros("10"), LimitPriceKnown: true,
						},
					},
					{
						ObjectKey: "broker-external", ObservedAt: completed,
						Canonical: observedOrderIdentity{
							BrokerOrderID: "broker-external", Symbol: "OWNED", Side: "buy", Kind: "equity",
							PositionEffect: "unknown", Qty: units.MustQty("1"),
							LimitPrice: units.MustMicros("10"), LimitPriceKnown: true,
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if observation.Status != "complete" || observation.Generation <= 0 || len(observation.ManifestDigest) != 64 {
		t.Fatalf("observation=%+v", observation)
	}
	view, err := s.LoadBrokerAccountView("account-b0")
	if err != nil {
		t.Fatal(err)
	}
	if view.Observation.ID != observation.ID || len(view.Objects) != 4 {
		t.Fatalf("view=%+v", view)
	}
	origins := map[string]string{}
	for _, object := range view.Objects {
		origins[object.ObjectKey] = object.Origin + "/" + object.Evidence
	}
	if origins["broker-owned"] != "alpheus/exact_broker_order_id" ||
		origins["broker-external"] != "external/unmatched" ||
		origins["equity:MANUAL"] != "external/unmatched" {
		t.Fatalf("origins=%v", origins)
	}

	partial, err := s.RecordBrokerObservation(BrokerObservationInput{
		AccountID: "account-b0", Source: "fixture", Purpose: "manual_refresh",
		StartedAt: completed.Add(time.Second), CompletedAt: completed.Add(2 * time.Second),
		Families: []BrokerObservationFamilyInput{
			{Family: BrokerFamilyAccount, Status: "success", CompletedAt: completed.Add(time.Second), Items: []BrokerObservationItemInput{{
				ObjectKey: "account-b0", ObservedAt: completed.Add(time.Second), Canonical: map[string]any{"account_id": "account-b0"},
			}}},
			{Family: BrokerFamilyPositions, Status: "error", ErrorCode: "unavailable", CompletedAt: completed.Add(2 * time.Second)},
		},
	})
	if err != nil || partial.Status != "partial" {
		t.Fatalf("partial=%+v err=%v", partial, err)
	}
	stillCurrent, err := s.LoadBrokerAccountView("account-b0")
	if err != nil || stillCurrent.Observation.ID != observation.ID {
		t.Fatalf("partial observation advanced head: view=%+v err=%v", stillCurrent, err)
	}
	if _, err := s.DB.Exec(`UPDATE broker_observation SET purpose='read_model' WHERE id=$1`, observation.ID); err == nil {
		t.Fatal("broker observation evidence was mutable")
	}
}

func TestSimilarOrderCannotAcquireAlpheusOriginPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	now := time.Now().UTC()
	observation, err := s.RecordBrokerObservation(BrokerObservationInput{
		AccountID: "account-similar", Source: "fixture", Purpose: "pre_effect",
		StartedAt: now.Add(-time.Second), CompletedAt: now,
		Families: []BrokerObservationFamilyInput{{
			Family: BrokerFamilyOrders, Status: "success", CompletedAt: now,
			Items: []BrokerObservationItemInput{{
				ObjectKey: "similar-only", ObservedAt: now,
				Canonical: observedOrderIdentity{
					BrokerOrderID: "similar-only", Symbol: "SPY", Side: "buy", Kind: "equity",
					Qty: units.MustQty("1"), LimitPrice: units.MustMicros("1"), LimitPriceKnown: true,
				},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var origin, evidence string
	if err := s.DB.QueryRow(`SELECT origin,evidence FROM broker_object_origin_event
		WHERE observation_id=$1 AND object_key='similar-only'`, observation.ID).Scan(&origin, &evidence); err != nil {
		t.Fatal(err)
	}
	if origin != "external" || evidence != "unmatched" {
		t.Fatalf("origin=%s evidence=%s", origin, evidence)
	}
}
