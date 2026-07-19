package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/units"
)

func TestExternalPositionReductionReconcilesWithoutFictionalFillPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	now := time.Now().UTC()
	_, _, _, openFillID := seedReconciliationExposure(
		t, s, "MANUAL", units.MustQty("2"), units.MustMicros("10"), now.Add(-time.Hour),
	)
	baseline := recordReconciliationObservation(t, s, "account-reconcile", "MANUAL", units.MustQty("2"), now)
	baselineResult, err := s.ReconcileBrokerObservation(baseline.ID)
	if err != nil || !baselineResult.Applied || len(baselineResult.Episodes) != 0 {
		t.Fatalf("baseline result=%+v err=%v", baselineResult, err)
	}

	staleOperationID := NewID()
	if err := s.InsertOperation(staleOperationID, "reconciliation-test", "C", "pending_review",
		map[string]any{"action": "close", "shadow": false, "symbol": "MANUAL", "kind": "equity", "qty": units.MustQty("1")},
		map[string]any{"class": "C"}, nil); err != nil {
		t.Fatal(err)
	}
	changed := recordReconciliationObservation(t, s, "account-reconcile", "MANUAL", units.MustQty("1"), time.Now().UTC().Add(time.Millisecond))
	result, err := s.ReconcileBrokerObservation(changed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || result.Deferred || len(result.Episodes) != 1 ||
		result.Episodes[0].ChangeKind != "external_reduce" ||
		result.Episodes[0].AdjustedTrackedQty != units.MustQty("1") ||
		result.Episodes[0].AttributionStatus != "uncertain" {
		t.Fatalf("result=%+v", result)
	}
	if len(result.InvalidatedOperationIDs) != 1 || result.InvalidatedOperationIDs[0] != staleOperationID {
		t.Fatalf("invalidations=%v", result.InvalidatedOperationIDs)
	}

	var openedQty, closedQty, remainingCost, remainingRisk int64
	if err := s.DB.QueryRow(`SELECT opened_qty,closed_qty,remaining_cost_basis_micros,remaining_risk_micros
		FROM exposure_lot WHERE open_fill_id=$1`, openFillID).Scan(&openedQty, &closedQty, &remainingCost, &remainingRisk); err != nil {
		t.Fatal(err)
	}
	if openedQty != int64(units.MustQty("2")) || closedQty != int64(units.MustQty("1")) ||
		remainingCost != int64(units.MustMicros("10")) || remainingRisk != int64(units.MustMicros("10")) {
		t.Fatalf("lot opened=%s closed=%s cost=%s risk=%s", units.Qty(openedQty), units.Qty(closedQty), units.Micros(remainingCost), units.Micros(remainingRisk))
	}
	var fillCount, closeAllocationCount, externalAllocationCount, episodeCount, invalidationCount int
	if err := s.DB.QueryRow(`SELECT
		(SELECT count(*) FROM fills),(SELECT count(*) FROM exposure_close_allocation),
		(SELECT count(*) FROM broker_external_exposure_allocation),
		(SELECT count(*) FROM broker_external_change_episode),
		(SELECT count(*) FROM broker_operation_invalidation)`).Scan(
		&fillCount, &closeAllocationCount, &externalAllocationCount, &episodeCount, &invalidationCount,
	); err != nil {
		t.Fatal(err)
	}
	if fillCount != 1 || closeAllocationCount != 0 || externalAllocationCount != 1 || episodeCount != 1 || invalidationCount != 1 {
		t.Fatalf("fills=%d close_alloc=%d external_alloc=%d episodes=%d invalidations=%d",
			fillCount, closeAllocationCount, externalAllocationCount, episodeCount, invalidationCount)
	}
	operation, err := s.GetOperation(staleOperationID)
	if err != nil || operation.Status != "expired" {
		t.Fatalf("stale operation=%+v err=%v", operation, err)
	}
	tx, err := s.DB.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pnl, pnlErr := localRealizedPnL(context.Background(), tx, "live", now.Add(-2*time.Hour), time.Now().UTC().Add(time.Hour))
	_ = tx.Rollback()
	if pnlErr != nil || pnl != 0 {
		t.Fatalf("local pnl=%s err=%v", pnl, pnlErr)
	}
	view, err := s.LoadBrokerCoexistenceView("account-reconcile", 50)
	if err != nil {
		t.Fatal(err)
	}
	if view.Reconciliation.State != "current" ||
		view.Reconciliation.ObservedGeneration != changed.Generation ||
		view.Reconciliation.ReconciledGeneration != changed.Generation ||
		len(view.Positions) != 1 || view.Positions[0].ExposureOrigin != "alpheus" ||
		view.Positions[0].ObservedOrigin != "ambiguous" ||
		view.Positions[0].ProviderQty != units.MustQty("1") ||
		view.Positions[0].TrackedQty != units.MustQty("1") || view.Positions[0].ExternalQty != 0 ||
		len(view.ExternalChanges) != 1 || len(view.ExternalChanges[0].Allocations) != 1 ||
		len(view.InvalidatedOperations) != 1 ||
		view.InvalidatedOperations[0].OperationID != staleOperationID ||
		view.InvalidatedOperations[0].Reason != "external_broker_state_changed" {
		t.Fatalf("coexistence view=%+v", view)
	}

	// Same immutable observation and a reconstructed Store both replay as no-ops.
	restarted := &Store{DB: s.DB, timeout: s.timeout, marketTZ: s.marketTZ}
	for attempt, candidate := range []*Store{s, restarted} {
		replayed, err := candidate.ReconcileBrokerObservation(changed.ID)
		if err != nil || replayed.Applied || len(replayed.Episodes) != 0 {
			t.Fatalf("replay %d result=%+v err=%v", attempt, replayed, err)
		}
	}
	var stillClosed int64
	var stillEpisodes int
	if err := s.DB.QueryRow(`SELECT closed_qty,(SELECT count(*) FROM broker_external_change_episode)
		FROM exposure_lot WHERE open_fill_id=$1`, openFillID).Scan(&stillClosed, &stillEpisodes); err != nil {
		t.Fatal(err)
	}
	if stillClosed != int64(units.MustQty("1")) || stillEpisodes != 1 {
		t.Fatalf("replay closed=%s episodes=%d", units.Qty(stillClosed), stillEpisodes)
	}
	restartedView, err := restarted.LoadBrokerCoexistenceView("account-reconcile", 50)
	if err != nil || !reflect.DeepEqual(view, restartedView) {
		t.Fatalf("restarted view=%+v err=%v, want %+v", restartedView, err, view)
	}
}

func TestExternalAddAndInternalDeltaAreSeparatedPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)

	now := time.Now().UTC()
	_, _, _, openFillID := seedReconciliationExposure(t, s, "ATTR", units.MustQty("1"), units.MustMicros("10"), now.Add(-time.Hour))
	baseline := recordReconciliationObservation(t, s, "account-attribution", "ATTR", units.MustQty("1"), now)
	if _, err := s.ReconcileBrokerObservation(baseline.ID); err != nil {
		t.Fatal(err)
	}
	staleOperationID, staleAttemptID, staleReservationID := seedPendingReconciliationOpen(t, s, "STALE", now)

	manualAdd := recordReconciliationObservation(t, s, "account-attribution", "ATTR", units.MustQty("2"), now.Add(time.Second))
	result, err := s.ReconcileBrokerObservation(manualAdd.ID)
	if err != nil || len(result.Episodes) != 1 || result.Episodes[0].ChangeKind != "external_add" ||
		result.Episodes[0].AdjustedTrackedQty != 0 {
		t.Fatalf("manual add result=%+v err=%v", result, err)
	}
	var staleStatus, attemptState, reservationState, orderState string
	var remainingRisk, remainingCash int64
	if err := s.DB.QueryRow(`SELECT o.status,a.state,r.resource_state,r.remaining_risk_micros,
		r.remaining_cash_micros,ord.state FROM operations o
		JOIN execution_attempt a ON a.operation_id=o.id
		JOIN open_reservation r ON r.operation_id=o.id
		JOIN orders ord ON ord.operation_id=o.id WHERE o.id=$1 AND a.id=$2 AND r.id=$3`,
		staleOperationID, staleAttemptID, staleReservationID).Scan(
		&staleStatus, &attemptState, &reservationState, &remainingRisk, &remainingCash, &orderState,
	); err != nil {
		t.Fatal(err)
	}
	if staleStatus != "failed" || attemptState != "failed" || reservationState != "released" ||
		remainingRisk != 0 || remainingCash != 0 || orderState != "rejected" {
		t.Fatalf("stale status=%s attempt=%s reservation=%s risk=%s cash=%s order=%s",
			staleStatus, attemptState, reservationState, units.Micros(remainingRisk), units.Micros(remainingCash), orderState)
	}

	// A later Alpheus-ledger change matching the Provider delta changes both
	// sides equally. It must not create external causal attribution.
	if _, err := s.DB.Exec(`UPDATE exposure_lot SET closed_qty=opened_qty,
		remaining_cost_basis_micros=0,remaining_risk_micros=0,closed_at=$2 WHERE open_fill_id=$1`,
		openFillID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	internalDelta := recordReconciliationObservation(t, s, "account-attribution", "ATTR", units.MustQty("1"), now.Add(2*time.Second))
	result, err = s.ReconcileBrokerObservation(internalDelta.ID)
	if err != nil || len(result.Episodes) != 0 {
		t.Fatalf("internal delta result=%+v err=%v", result, err)
	}
	var episodes int
	if err := s.DB.QueryRow(`SELECT count(*) FROM broker_external_change_episode`).Scan(&episodes); err != nil {
		t.Fatal(err)
	}
	if episodes != 1 {
		t.Fatalf("episodes=%d want=1", episodes)
	}
	view, err := s.LoadBrokerCoexistenceView("account-attribution", 50)
	if err != nil || len(view.Positions) != 1 || view.Positions[0].ExposureOrigin != "external" ||
		view.Positions[0].TrackedQty != 0 || view.Positions[0].ExternalQty != units.MustQty("1") {
		t.Fatalf("coexistence view=%+v err=%v", view, err)
	}
}

func TestUnappliedAlpheusOrderEffectDefersBrokerReconciliationPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	now := time.Now().UTC()
	operationID, attemptID, orderID, _ := seedReconciliationExposure(t, s, "DEFER", units.MustQty("1"), units.MustMicros("10"), now.Add(-time.Hour))
	if _, err := s.DB.Exec(`UPDATE execution_attempt SET state='placed',broker_order_id='broker-defer' WHERE id=$1`, attemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE orders SET state='submitted',broker_order_id='broker-defer' WHERE id=$1`, orderID); err != nil {
		t.Fatal(err)
	}
	_ = operationID
	observation := recordReconciliationObservation(t, s, "account-defer", "DEFER", units.MustQty("1"), now)
	result, err := s.ReconcileBrokerObservation(observation.ID)
	if err != nil || !result.Deferred || result.DeferredReason != "alpheus_order_effect_not_yet_applied" || result.Applied {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var heads, episodes int
	if err := s.DB.QueryRow(`SELECT (SELECT count(*) FROM broker_reconciliation_head),
		(SELECT count(*) FROM broker_external_change_episode)`).Scan(&heads, &episodes); err != nil {
		t.Fatal(err)
	}
	if heads != 0 || episodes != 0 {
		t.Fatalf("deferred reconciliation mutated heads=%d episodes=%d", heads, episodes)
	}
}

func TestLocalOrderMutationAfterSnapshotDefersBrokerReconciliationPostgres(t *testing.T) {
	s := openKernelPolicyIntegrationStore(t)
	defer s.DB.Close()
	resetM3AIntegrationData(t, s)
	now := time.Now().UTC()
	seedReconciliationExposure(t, s, "FENCE", units.MustQty("1"), units.MustMicros("10"), now.Add(-time.Hour))
	observation := recordReconciliationObservation(t, s, "account-fence", "FENCE", units.MustQty("1"), time.Now().UTC())
	seedPendingReconciliationOpen(t, s, "AFTER", time.Now().UTC())

	result, err := s.ReconcileBrokerObservation(observation.ID)
	if err != nil || !result.Deferred || result.DeferredReason != "local_order_changed_after_observation" || result.Applied {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var heads, episodes, invalidations int
	if err := s.DB.QueryRow(`SELECT (SELECT count(*) FROM broker_reconciliation_head),
		(SELECT count(*) FROM broker_external_change_episode),
		(SELECT count(*) FROM broker_operation_invalidation)`).Scan(&heads, &episodes, &invalidations); err != nil {
		t.Fatal(err)
	}
	if heads != 0 || episodes != 0 || invalidations != 0 {
		t.Fatalf("deferred reconciliation mutated heads=%d episodes=%d invalidations=%d", heads, episodes, invalidations)
	}
}

func TestExternalPositionChangeKindUsesExposureMagnitude(t *testing.T) {
	tests := []struct {
		name                    string
		hasPrior                bool
		before, after, adjusted units.Qty
		want                    string
	}{
		{name: "baseline", before: 0, after: -2, want: "baseline"},
		{name: "long add", hasPrior: true, before: 1, after: 2, want: "external_add"},
		{name: "long reduce", hasPrior: true, before: 2, after: 1, want: "external_reduce"},
		{name: "short add", hasPrior: true, before: -1, after: -2, want: "external_add"},
		{name: "short reduce", hasPrior: true, before: -2, after: -1, want: "external_reduce"},
		{name: "flat", hasPrior: true, before: -1, after: 0, want: "external_reduce"},
		{name: "reversal", hasPrior: true, before: -1, after: 1, want: "external_reversal"},
		{name: "tracked reduction", hasPrior: true, before: 0, after: 0, adjusted: 1, want: "external_reduce"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := externalPositionChangeKind(test.hasPrior, test.before, test.after, test.adjusted)
			if got != test.want {
				t.Fatalf("kind=%s want=%s", got, test.want)
			}
		})
	}
}

func TestBrokerExposureOriginDoesNotAdoptMixedOrAmbiguousPositions(t *testing.T) {
	tests := []struct {
		name              string
		tracked, external units.Qty
		positionKeys      int
		want              string
	}{
		{name: "fully tracked", tracked: 1, positionKeys: 1, want: "alpheus"},
		{name: "external", external: 1, positionKeys: 1, want: "external"},
		{name: "external short", external: -1, positionKeys: 1, want: "external"},
		{name: "mixed", tracked: 1, external: 1, positionKeys: 1, want: "mixed"},
		{name: "ambiguous provider identities", tracked: 1, external: 1, positionKeys: 2, want: "ambiguous"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := brokerExposureOrigin(test.tracked, test.external, test.positionKeys); got != test.want {
				t.Fatalf("origin=%s want=%s", got, test.want)
			}
		})
	}
}

func seedReconciliationExposure(t *testing.T, s *Store, symbol string, qty units.Qty, price units.Micros, ts time.Time) (string, string, string, string) {
	t.Helper()
	operationID, attemptID, orderID, fillID := NewID(), NewID(), NewID(), NewID()
	if err := s.InsertOperation(operationID, "reconciliation-seed", "B", "executed",
		map[string]any{"action": "open", "shadow": false, "symbol": symbol, "kind": "equity"},
		map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, Intent: "place",
			ClientOrderID: NewID(), State: "settled", Qty: qty, Limit: price,
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: NewID(), Ledger: "live", Symbol: symbol, Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: qty, Limit: price, State: "filled",
		})
	}); err != nil {
		t.Fatal(err)
	}
	cost, err := units.MulQtyPrice(qty, price, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO fills
		(id,order_id,broker_fill_id,ledger,qty,price_micros,fees_micros,ts)
		VALUES ($1,$2,$3,'live',$4,$5,0,$6)`, fillID, orderID, "reconcile-open-"+fillID,
		int64(qty), int64(price), ts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO exposure_lot
		(open_fill_id,operation_id,ledger,symbol,kind,multiplier,opened_qty,closed_qty,
		 entry_cost_micros,remaining_cost_basis_micros,remaining_risk_micros,opened_at)
		VALUES ($1,$2,'live',$3,'equity',1,$4,0,$5,$5,$5,$6)`, fillID, operationID,
		symbol, int64(qty), int64(cost), ts); err != nil {
		t.Fatal(err)
	}
	return operationID, attemptID, orderID, fillID
}

func seedPendingReconciliationOpen(t *testing.T, s *Store, symbol string, ts time.Time) (string, string, string) {
	t.Helper()
	operationID, attemptID, reservationID, orderID, clientID := NewID(), NewID(), NewID(), NewID(), NewID()
	qty, price := units.MustQty("1"), units.MustMicros("5")
	if err := s.InsertOperation(operationID, "reconciliation-stale", "B", "auto_approved",
		map[string]any{"action": "open", "shadow": false, "symbol": symbol, "kind": "equity"},
		map[string]any{"class": "B"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.WithProposalLock(nil, false, false, func(gate OperationGate) error {
		if err := gate.InsertOpenReservation(OpenReservation{
			ID: reservationID, OperationID: operationID, Ledger: "live", MarketDay: ts,
			Symbol: symbol, Kind: "equity", OriginalQty: qty, RemainingQty: qty,
			OriginalRisk: price, RemainingRisk: price, OriginalCash: price, RemainingCash: price,
			ResourceState: "held",
		}); err != nil {
			return err
		}
		if err := gate.InsertExecutionAttempt(ExecutionAttempt{
			ID: attemptID, OperationID: operationID, Seq: 1, OpenReservationID: reservationID,
			Intent: "place", ClientOrderID: clientID, State: "pending", Qty: qty, Limit: price,
		}); err != nil {
			return err
		}
		return gate.InsertOrder(Order{
			ID: orderID, OperationID: operationID, ExecutionAttemptID: attemptID,
			ClientOrderID: clientID, Ledger: "live", Symbol: symbol, Side: "buy",
			Kind: "equity", Multiplier: 1, Qty: qty, Limit: price, State: "new",
		})
	}); err != nil {
		t.Fatal(err)
	}
	return operationID, attemptID, reservationID
}

func recordReconciliationObservation(t *testing.T, s *Store, accountID, symbol string, qty units.Qty, completed time.Time) *BrokerObservation {
	t.Helper()
	localStateGeneration, err := s.BrokerLocalStateGeneration()
	if err != nil {
		t.Fatal(err)
	}
	started := completed.Add(-time.Millisecond)
	positionItems := []BrokerObservationItemInput{}
	if qty != 0 {
		positionItems = append(positionItems, BrokerObservationItemInput{
			ObjectKey: "equity:" + symbol, ObservedAt: completed,
			Canonical: broker.Position{
				PositionID: "equity:" + symbol, InstrumentID: "instrument-" + symbol,
				Symbol: symbol, Qty: qty, AvgPrice: units.MustMicros("10"), AvgPriceKnown: true,
				Kind: "equity", Multiplier: 1, Source: "fixture", AsOf: completed,
			},
		})
	}
	observation, err := s.RecordBrokerObservation(BrokerObservationInput{
		AccountID: accountID, Source: "fixture", Purpose: "reconciliation",
		StartedAt: started, CompletedAt: completed, LocalStateGeneration: localStateGeneration,
		Families: []BrokerObservationFamilyInput{
			{Family: BrokerFamilyAccount, Status: "success", CompletedAt: completed, Items: []BrokerObservationItemInput{{
				ObjectKey: accountID, ObservedAt: completed, Canonical: map[string]any{"account_id": accountID},
			}}},
			{Family: BrokerFamilyPositions, Status: "success", CompletedAt: completed, Items: positionItems},
			{Family: BrokerFamilyOrders, Status: "success", CompletedAt: completed},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return observation
}
