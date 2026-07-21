package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

type providerSnapshot struct {
	Observation *store.BrokerObservation
	View        *store.BrokerAccountView
	Account     broker.AccountState
	Positions   []broker.Position
	Orders      []broker.ReadOrder
}

type observedAccount struct {
	AccountID     string       `json:"account_id"`
	AccountType   string       `json:"account_type"`
	BuyingPower   units.Micros `json:"buying_power"`
	Equity        units.Micros `json:"equity"`
	EquityKnown   bool         `json:"equity_known"`
	DayTradesUsed int          `json:"day_trades_used"`
	Cash          units.Micros `json:"cash"`
	CashKnown     bool         `json:"cash_known"`
	Source        string       `json:"source"`
	AsOf          time.Time    `json:"as_of"`
}

type preEffectFacts struct {
	SnapshotCompletedAt time.Time            `json:"snapshot_completed_at"`
	Quote               *broker.Quote        `json:"quote,omitempty"`
	Instrument          *broker.Instrument   `json:"instrument,omitempty"`
	TargetOrder         *broker.ReadOrder    `json:"target_order,omitempty"`
	Evaluation          *preEffectEvaluation `json:"evaluation"`
}

type preEffectEvaluation struct {
	ActivePolicyRevisionID int64        `json:"active_policy_revision_id"`
	ActivePolicyGeneration int64        `json:"active_policy_generation"`
	ActivePolicyDigest     string       `json:"active_policy_digest"`
	LocalOpenRisk          units.Micros `json:"local_open_risk"`
	LocalHeldCash          units.Micros `json:"local_held_cash"`
	OtherLocalCloseQty     units.Qty    `json:"other_local_close_qty"`
	ExternalPositionRisk   units.Micros `json:"external_position_risk"`
	ExternalWorkingRisk    units.Micros `json:"external_working_risk"`
	ExternalWorkingClose   units.Qty    `json:"external_working_close_qty"`
	AggregateOpenRisk      units.Micros `json:"aggregate_open_risk"`
	EffectiveBuyingPower   units.Micros `json:"effective_buying_power"`
	FreshnessExpiresAt     time.Time    `json:"freshness_expires_at"`
}

func bindDecisionObservation(op *risk.Operation, snapshot *providerSnapshot) {
	if op == nil || snapshot == nil || snapshot.Observation == nil {
		return
	}
	op.DecisionObservationID = snapshot.Observation.ID
	op.DecisionObservationGeneration = snapshot.Observation.Generation
	op.DecisionObservationDigest = snapshot.Observation.ManifestDigest
}

func observedObjectOrigin(snapshot *providerSnapshot, family, key string) string {
	if snapshot == nil || snapshot.View == nil {
		return ""
	}
	for _, object := range snapshot.View.Objects {
		if object.Family == family && object.ObjectKey == key {
			return object.Origin
		}
	}
	return ""
}

func observedWorkingOrder(snapshot *providerSnapshot, brokerOrderID string) *broker.ReadOrder {
	if snapshot == nil {
		return nil
	}
	for i := range snapshot.Orders {
		if snapshot.Orders[i].BrokerOrderID == brokerOrderID {
			order := snapshot.Orders[i]
			return &order
		}
	}
	return nil
}

func classifyCancelDecision(snapshot *providerSnapshot, op *risk.Operation) {
	bindDecisionObservation(op, snapshot)
	target := observedWorkingOrder(snapshot, op.BrokerOrderID)
	if target == nil {
		op.CancelTargetEffect = "missing"
		return
	}
	op.BrokerObjectOrigin = observedObjectOrigin(snapshot, store.BrokerFamilyOrders, target.BrokerOrderID)
	op.CancelTargetFingerprint = cancelTargetFingerprint(*target)
	mayOpen, mayClose, err := workingOrderEffects(*target, snapshot.Positions)
	switch {
	case err != nil || (mayOpen && mayClose):
		op.CancelTargetEffect = "ambiguous"
	case mayOpen:
		op.CancelTargetEffect = "opening"
		op.VerifiedReduction = true
	case mayClose:
		op.CancelTargetEffect = "closing"
	default:
		op.CancelTargetEffect = "ambiguous"
	}
}

func cancelTargetFingerprint(order broker.ReadOrder) string {
	semantic := struct {
		BrokerOrderID  string       `json:"broker_order_id"`
		InstrumentID   string       `json:"instrument_id"`
		Symbol         string       `json:"symbol"`
		Side           string       `json:"side"`
		Kind           string       `json:"kind"`
		PositionEffect string       `json:"position_effect"`
		State          string       `json:"state"`
		Qty            units.Qty    `json:"qty"`
		FilledQty      units.Qty    `json:"filled_qty"`
		LimitPrice     units.Micros `json:"limit_price"`
		LimitKnown     bool         `json:"limit_price_known"`
		StopPrice      units.Micros `json:"stop_price"`
		StopKnown      bool         `json:"stop_price_known"`
		OrderType      string       `json:"order_type"`
	}{
		BrokerOrderID: order.BrokerOrderID, InstrumentID: order.InstrumentID,
		Symbol: order.Symbol, Side: order.Side, Kind: order.Kind,
		PositionEffect: order.PositionEffect, State: order.State, Qty: order.Qty,
		FilledQty: order.FilledQty, LimitPrice: order.LimitPrice, LimitKnown: order.LimitPriceKnown,
		StopPrice: order.StopPrice, StopKnown: order.StopPriceKnown, OrderType: order.OrderType,
	}
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func externalWorkingCloseQuantity(snapshot *providerSnapshot, op risk.Operation) (units.Qty, error) {
	if snapshot == nil || snapshot.View == nil {
		return 0, fmt.Errorf("broker order observation is unavailable")
	}
	var external units.Qty
	for _, order := range snapshot.Orders {
		if observedObjectOrigin(snapshot, store.BrokerFamilyOrders, order.BrokerOrderID) == "alpheus" {
			continue
		}
		mayOpen, mayClose, err := workingOrderEffects(order, snapshot.Positions)
		if err != nil || (mayOpen && mayClose) {
			return 0, fmt.Errorf("working close order effect is ambiguous")
		}
		if !mayClose || order.Symbol != operationSymbol(op) || order.Kind != op.Kind {
			continue
		}
		remaining := order.Qty - order.FilledQty
		external, err = units.AddQty(external, remaining)
		if err != nil {
			return 0, err
		}
	}
	return external, nil
}

func (s *server) captureProviderSnapshot(ctx context.Context, purpose string) (*providerSnapshot, error) {
	return s.captureProviderSnapshotFrom(ctx, purpose, s.accountProvider())
}

func (s *server) captureProviderSnapshotFrom(ctx context.Context, purpose string, provider broker.AccountProvider) (*providerSnapshot, error) {
	if provider == nil {
		return nil, fmt.Errorf("account provider unavailable")
	}
	localStateGeneration, err := s.store.BrokerLocalStateGeneration()
	if err != nil {
		return nil, fmt.Errorf("local broker state generation unavailable")
	}
	started := time.Now().UTC()
	accountIDCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	accountID, err := provider.AccountID(accountIDCtx)
	cancel()
	accountID = strings.TrimSpace(accountID)
	if err != nil || accountID == "" {
		return nil, fmt.Errorf("provider account identity unavailable")
	}
	if expected := strings.TrimSpace(s.mode.LiveAccountID); expected != "" && accountID != expected {
		return nil, fmt.Errorf("provider account binding mismatch")
	}

	snapshot := &providerSnapshot{}
	families := make([]store.BrokerObservationFamilyInput, 0, 3)
	source := "provider"

	accountCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	account, accountErr := provider.Account(accountCtx)
	cancel()
	accountCompleted := time.Now().UTC()
	accountFamily := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyAccount, Status: "error", ErrorCode: "unavailable", CompletedAt: accountCompleted,
	}
	if accountErr == nil && account.ExternalID == accountID && account.Source != "" &&
		!account.AsOf.IsZero() && !account.AsOf.After(accountCompleted) {
		snapshot.Account = account
		source = account.Source
		accountFamily.Status, accountFamily.ErrorCode = "success", ""
		accountFamily.Items = []store.BrokerObservationItemInput{{
			ObjectKey: accountID, ObservedAt: account.AsOf,
			Canonical: observedAccount{
				AccountID: accountID, AccountType: account.AccountType, BuyingPower: account.BuyingPower,
				Equity: account.Equity, EquityKnown: account.EquityKnown, DayTradesUsed: account.DayTradesUsed,
				Cash:      account.Cash,
				CashKnown: account.CashKnown, Source: account.Source, AsOf: account.AsOf,
			},
		}}
	} else if accountErr == nil {
		accountFamily.ErrorCode = "wrong_account"
	}
	families = append(families, accountFamily)

	positionsCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	positions, positionsErr := provider.Positions(positionsCtx)
	cancel()
	positionsCompleted := time.Now().UTC()
	positionsFamily := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyPositions, Status: "error", ErrorCode: "unavailable", CompletedAt: positionsCompleted,
	}
	if positionsErr == nil && brokerPositionsValid(positions, accountID, source, positionsCompleted) {
		snapshot.Positions = positions
		positionsFamily.Status, positionsFamily.ErrorCode = "success", ""
		for _, position := range positions {
			positionsFamily.Items = append(positionsFamily.Items, store.BrokerObservationItemInput{
				ObjectKey: position.PositionID, ObservedAt: position.AsOf, Canonical: position,
			})
		}
	} else if positionsErr == nil {
		positionsFamily.ErrorCode = "invalid"
	}
	families = append(families, positionsFamily)

	ordersCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	orders, ordersErr := provider.OpenOrders(ordersCtx)
	cancel()
	ordersCompleted := time.Now().UTC()
	ordersFamily := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyOrders, Status: "error", ErrorCode: "unavailable", CompletedAt: ordersCompleted,
	}
	if ordersErr == nil && brokerOrdersValid(orders, source, ordersCompleted) {
		snapshot.Orders = orders
		ordersFamily.Status, ordersFamily.ErrorCode = "success", ""
		for _, order := range orders {
			ordersFamily.Items = append(ordersFamily.Items, store.BrokerObservationItemInput{
				ObjectKey: order.BrokerOrderID, ObservedAt: order.AsOf, Canonical: order,
			})
		}
	} else if ordersErr == nil {
		ordersFamily.ErrorCode = "invalid"
	}
	families = append(families, ordersFamily)

	completed := time.Now().UTC()
	observation, err := s.store.RecordBrokerObservation(store.BrokerObservationInput{
		AccountID: accountID, Source: source, Purpose: purpose, StartedAt: started,
		CompletedAt: completed, LocalStateGeneration: localStateGeneration, Families: families,
	})
	if err != nil {
		return nil, err
	}
	snapshot.Observation = observation
	if observation.Status != "complete" {
		return snapshot, fmt.Errorf("provider snapshot is partial")
	}
	// Bind the returned canonical view to this exact immutable observation.
	// Reading the mutable account head here would allow a concurrent refresh to
	// pair one call's Provider slices with another call's origin evidence.
	view, err := s.store.LoadBrokerObservation(observation.ID)
	if err != nil {
		return nil, err
	}
	snapshot.View = view
	return snapshot, nil
}

// captureReconciledProviderDecision is the only automatic entrypoint which
// may turn Provider positions into reconciliation state. Its reads bypass the
// interactive MCP cache; read-model and ordinary cached snapshots remain
// evidence only and can never reduce the internal exposure ledger.
func (s *server) captureReconciledProviderDecision(ctx context.Context) (*providerSnapshot, error) {
	bindingCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	bindingErr := s.assertLiveAccountBinding(bindingCtx, "")
	cancel()
	if bindingErr != nil {
		return nil, bindingErr
	}
	snapshot, err := s.captureProviderSnapshotFrom(ctx, "decision", s.authorityAccountProvider())
	if err != nil {
		return nil, err
	}
	if _, err := s.store.ReconcileBrokerObservation(snapshot.Observation.ID); err != nil {
		return nil, fmt.Errorf("reconcile broker observation: %w", err)
	}
	return snapshot, nil
}

func (s *server) captureProviderFills(ctx context.Context, purpose, accountID, source string, since time.Time) ([]broker.ReadFill, *store.BrokerObservation, []store.BrokerObservedObject, error) {
	localStateGeneration, err := s.store.BrokerLocalStateGeneration()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("local broker state generation unavailable")
	}
	started := time.Now().UTC()
	providerCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	fills, providerErr := s.authorityAccountProvider().RecentFills(providerCtx, since)
	cancel()
	completed := time.Now().UTC()
	family := store.BrokerObservationFamilyInput{
		Family: store.BrokerFamilyFills, Status: "error", ErrorCode: "unavailable", CompletedAt: completed,
	}
	if providerErr == nil && brokerFillsValid(fills, source, completed) {
		family.Status, family.ErrorCode = "success", ""
		for _, fill := range fills {
			family.Items = append(family.Items, store.BrokerObservationItemInput{
				ObjectKey: fill.FillID, ObservedAt: fill.AsOf, Canonical: fill,
			})
		}
	} else if providerErr == nil {
		family.ErrorCode = "invalid"
	}
	observation, err := s.store.RecordBrokerObservation(store.BrokerObservationInput{
		AccountID: accountID, Source: source, Purpose: purpose, StartedAt: started,
		CompletedAt: completed, LocalStateGeneration: localStateGeneration,
		Families: []store.BrokerObservationFamilyInput{family},
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if observation.Status != "complete" {
		return nil, observation, nil, fmt.Errorf("provider fill snapshot is partial")
	}
	view, err := s.store.LoadBrokerObservation(observation.ID)
	if err != nil {
		return nil, nil, nil, err
	}
	return fills, observation, view.Objects, nil
}

func brokerPositionsValid(positions []broker.Position, accountID, source string, completedAt time.Time) bool {
	seen := map[string]bool{}
	for _, position := range positions {
		if position.PositionID == "" || position.Symbol == "" || position.Qty == 0 ||
			(position.Kind != "equity" && position.Kind != "option") || position.Multiplier <= 0 ||
			position.Source != source || position.AsOf.IsZero() || position.AsOf.After(completedAt) || seen[position.PositionID] {
			return false
		}
		seen[position.PositionID] = true
	}
	return accountID != ""
}

func brokerOrdersValid(orders []broker.ReadOrder, source string, completedAt time.Time) bool {
	seen := map[string]bool{}
	for _, order := range orders {
		if order.BrokerOrderID == "" || order.Symbol == "" || (order.Side != "buy" && order.Side != "sell") ||
			(order.Kind != "equity" && order.Kind != "option") ||
			(order.PositionEffect != "open" && order.PositionEffect != "close" && order.PositionEffect != "unknown") ||
			strings.TrimSpace(order.State) == "" || order.Qty <= 0 || order.FilledQty < 0 || order.FilledQty > order.Qty ||
			order.Source != source || order.AsOf.IsZero() || order.AsOf.After(completedAt) || seen[order.BrokerOrderID] {
			return false
		}
		if order.LimitPriceKnown && order.LimitPrice <= 0 {
			return false
		}
		if order.StopPriceKnown && order.StopPrice <= 0 {
			return false
		}
		switch order.OrderType {
		case "":
		case "limit":
			if !order.LimitPriceKnown || order.StopPriceKnown {
				return false
			}
		case "market":
			if order.StopPriceKnown {
				return false
			}
		case "stop_limit":
			if !order.LimitPriceKnown || !order.StopPriceKnown {
				return false
			}
		case "stop_market":
			if order.LimitPriceKnown || !order.StopPriceKnown {
				return false
			}
		default:
			return false
		}
		seen[order.BrokerOrderID] = true
	}
	return true
}

// captureLivePreEffect refreshes the whole shared account and then records the
// action-specific facts required for the imminent Provider call. It performs
// no mutation and returns only a short-lived, immutable manifest.
func (s *server) captureLivePreEffect(ctx context.Context, attempt *store.ExecutionAttempt, op risk.Operation, replay bool) (*store.PreEffectManifest, error) {
	if s.tradingMode() != config.ModeLive {
		return nil, fmt.Errorf("pre-effect manifests are live-only")
	}
	// The authority Provider is structurally non-cacheable; no call-site flag
	// can downgrade this pre-effect evidence into a read-model snapshot.
	bindingCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	bindingErr := s.assertLiveAccountBinding(bindingCtx, attempt.OperationID)
	cancel()
	if bindingErr != nil {
		return nil, bindingErr
	}
	snapshot, err := s.captureProviderSnapshotFrom(ctx, "pre_effect", s.authorityAccountProvider())
	if err != nil {
		s.recordPreEffectRefusal(attempt, "account_snapshot_unavailable")
		return nil, fmt.Errorf("%w: account snapshot", errPreEffectUnavailable)
	}
	effect := ""
	facts := preEffectFacts{SnapshotCompletedAt: snapshot.Observation.CompletedAt}
	switch attempt.Intent {
	case "place":
		if op.Action == "open" {
			effect = "place_open"
		} else if op.Action == "close" {
			effect = "place_close"
		}
		if effect == "" {
			return nil, fmt.Errorf("pre-effect place action is invalid")
		}
		provider := s.authorityMarketProvider()
		if provider == nil {
			s.recordPreEffectRefusal(attempt, "market_provider_unavailable")
			return nil, fmt.Errorf("%w: market provider", errPreEffectUnavailable)
		}
		symbol := operationSymbol(op)
		quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		quote, quoteErr := provider.Quote(quoteCtx, symbol)
		cancel()
		instrumentCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		instrument, instrumentErr := provider.Instrument(instrumentCtx, symbol)
		cancel()
		maxAge := attempt.QuoteMaxAgeSec
		if maxAge <= 0 {
			maxAge = s.limits.QuoteMaxAgeSec
		}
		// Instrument identity/precision (which validates the limit price without a
		// quote) and the account snapshot above always gate the effect. A usable
		// live quote does not: a price-bounded order is capped by its own limit,
		// so after hours (no active/fresh quote) it still proceeds; the aggregate
		// gate below already tolerates a nil quote. Other order types still
		// require the quote for marketability.
		if instrumentErr != nil || !preEffectInstrumentMatches(op, attempt, instrument) {
			s.recordPreEffectRefusal(attempt, "instrument_facts_invalid")
			return nil, fmt.Errorf("%w: instrument facts", errPreEffectUnavailable)
		}
		facts.Instrument = &instrument
		if quoteErr == nil && quote.Usable(maxAge, time.Now().UTC()) {
			if !preEffectQuoteMatches(op, quote, instrument) {
				s.recordPreEffectRefusal(attempt, "market_facts_invalid")
				return nil, fmt.Errorf("%w: market facts", errPreEffectUnavailable)
			}
			facts.Quote = &quote
		} else if !priceBoundedOpen(op) {
			s.recordPreEffectRefusal(attempt, "market_facts_invalid")
			return nil, fmt.Errorf("%w: market facts", errPreEffectUnavailable)
		}
	case "cancel":
		if op.Action == "cancel" {
			effect = "cancel_order"
		} else if op.Action == "open" || op.Action == "close" {
			effect = "replace_cancel"
		}
		if effect == "" {
			return nil, fmt.Errorf("pre-effect cancel action is invalid")
		}
		for i := range snapshot.Orders {
			if snapshot.Orders[i].BrokerOrderID == attempt.TargetBrokerOrderID {
				target := snapshot.Orders[i]
				facts.TargetOrder = &target
				break
			}
		}
		if facts.TargetOrder == nil {
			s.recordPreEffectRefusal(attempt, "cancel_target_not_working")
			return nil, fmt.Errorf("%w: cancel target is not working", errPreEffectRejected)
		}
		if effect == "replace_cancel" {
			provider := s.authorityMarketProvider()
			if provider == nil {
				return nil, fmt.Errorf("%w: replacement market provider", errPreEffectUnavailable)
			}
			quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
			quote, quoteErr := provider.Quote(quoteCtx, operationSymbol(op))
			cancel()
			instrumentCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
			instrument, instrumentErr := provider.Instrument(instrumentCtx, operationSymbol(op))
			cancel()
			maxAge := attempt.QuoteMaxAgeSec
			if maxAge <= 0 {
				maxAge = s.limits.QuoteMaxAgeSec
			}
			if quoteErr != nil || !quote.Usable(maxAge, time.Now().UTC()) || instrumentErr != nil ||
				!preEffectQuoteMatches(op, quote, instrument) || !instrument.PrecisionSane() {
				s.recordPreEffectRefusal(attempt, "replacement_facts_invalid")
				return nil, fmt.Errorf("%w: replacement market facts", errPreEffectUnavailable)
			}
			facts.Quote, facts.Instrument = &quote, &instrument
		}
	default:
		return nil, fmt.Errorf("pre-effect intent is unsupported")
	}
	row, err := s.store.GetOperation(attempt.OperationID)
	if err != nil {
		return nil, err
	}
	evaluation, err := s.evaluateLivePreEffect(
		ctx, snapshot, attempt, row, op, facts.Quote, facts.Instrument, facts.TargetOrder, replay,
	)
	if err != nil {
		// The aggregate gate lumps ~8 distinct sub-checks (snapshot/instrument
		// freshness, proposal TTL, external-aggregate re-classification, funds,
		// exposure) under one category; keep the specific cause in the log so a
		// refusal is diagnosable rather than an opaque proposal_stale.
		log.Printf("pre-effect aggregate gate failed: attempt_id=%s detail=%q", attempt.ID, err.Error())
		s.recordPreEffectRefusal(attempt, "aggregate_gate_failed")
		return nil, err
	}
	facts.Evaluation = evaluation
	manifest, err := s.store.RecordPreEffectManifest(store.PreEffectManifestInput{
		AttemptID: attempt.ID, FencingToken: attempt.Attempt,
		AccountID: snapshot.Observation.AccountID, Effect: effect,
		ObservationID:             snapshot.Observation.ID,
		ObservationGeneration:     snapshot.Observation.Generation,
		ObservationManifestDigest: snapshot.Observation.ManifestDigest,
		TargetBrokerOrderID:       attempt.TargetBrokerOrderID,
		Facts:                     facts, ExpiresAt: evaluation.FreshnessExpiresAt, Ledger: "live",
		ActivePolicyRevisionID: evaluation.ActivePolicyRevisionID,
		ActivePolicyGeneration: evaluation.ActivePolicyGeneration,
		ActivePolicyDigest:     evaluation.ActivePolicyDigest,
		ExpectedLocalOpenRisk:  evaluation.LocalOpenRisk,
		ExpectedLocalHeldCash:  evaluation.LocalHeldCash,
		ExpectedOtherCloseQty:  evaluation.OtherLocalCloseQty,
	})
	if err != nil {
		s.recordPreEffectRefusal(attempt, "manifest_persistence_failed")
		if errors.Is(err, store.ErrPreEffectStale) {
			return nil, fmt.Errorf("%w: %v", errPreEffectRejected, err)
		}
		return nil, err
	}
	return manifest, nil
}

func (s *server) evaluateLivePreEffect(
	ctx context.Context,
	snapshot *providerSnapshot,
	attempt *store.ExecutionAttempt,
	row *store.OperationRow,
	op risk.Operation,
	quote *broker.Quote,
	instrument *broker.Instrument,
	target *broker.ReadOrder,
	replay bool,
) (*preEffectEvaluation, error) {
	var evaluation *preEffectEvaluation
	err := s.store.WithLedgerLock(false, func(gate store.OperationGate) error {
		active, err := gate.KernelPolicyAuthority()
		if err != nil {
			return err
		}
		bound, err := gate.BoundKernelPolicy(row)
		if err != nil {
			return err
		}
		databaseNow, err := gate.DatabaseNow()
		if err != nil {
			return err
		}
		reviewApproved := row.Class == "C" && row.Status == "approved"
		maxAgeSec := minInt(bound.Policy.QuoteMaxAgeSec, active.Policy.QuoteMaxAgeSec)
		if maxAgeSec <= 0 || snapshot.Observation.StartedAt.IsZero() ||
			snapshot.Observation.StartedAt.After(databaseNow) {
			return fmt.Errorf("provider snapshot freshness is invalid")
		}
		freshnessExpiresAt := snapshot.Observation.StartedAt.Add(time.Duration(maxAgeSec) * time.Second)
		if quote != nil {
			if !quote.Usable(maxAgeSec, databaseNow) {
				return fmt.Errorf("quote became stale at pre-effect barrier")
			}
			freshnessExpiresAt = earlierTime(freshnessExpiresAt, quote.AsOf.Add(time.Duration(maxAgeSec)*time.Second))
		}
		if instrument != nil {
			if instrument.AsOf.IsZero() || instrument.AsOf.After(databaseNow) ||
				databaseNow.Sub(instrument.AsOf) > time.Duration(maxAgeSec)*time.Second {
				return fmt.Errorf("instrument became stale at pre-effect barrier")
			}
			freshnessExpiresAt = earlierTime(freshnessExpiresAt, instrument.AsOf.Add(time.Duration(maxAgeSec)*time.Second))
		}
		if !databaseNow.Before(freshnessExpiresAt) {
			return fmt.Errorf("provider snapshot became stale at pre-effect barrier")
		}
		if preEffectRequiresProposalTTL(attempt, op) {
			if !reviewApproved && (row.ExpiresAt.IsZero() || !databaseNow.Before(row.ExpiresAt)) {
				return fmt.Errorf("proposal is stale at pre-effect barrier")
			}
		}
		resources, err := gate.LedgerResources("live", attempt.OperationID)
		if err != nil {
			return err
		}
		otherClose, err := gate.HeldCloseQuantityExcluding("live", operationSymbol(op), attempt.OperationID)
		if err != nil {
			return err
		}
		e := &preEffectEvaluation{
			ActivePolicyRevisionID: active.ID, ActivePolicyGeneration: active.Generation,
			ActivePolicyDigest: active.Digest, LocalOpenRisk: resources.OpenRisk,
			LocalHeldCash: resources.HeldCash, OtherLocalCloseQty: otherClose,
			FreshnessExpiresAt: freshnessExpiresAt,
		}
		if replay && attempt.Intent == "place" && sameReferenceOrderVisible(snapshot.Orders, attempt.ClientOrderID) {
			return fmt.Errorf("same-reference Provider candidate appeared before replay")
		}
		effectOp := op
		if attempt.Intent == "place" && attempt.Seq > 1 {
			effectOp.Qty = attempt.Qty
			if effectOp.Action == "open" {
				remaining, err := gate.HeldOpenResources(attempt.OperationID)
				if err != nil {
					return err
				}
				effectOp.DerivedMaxRisk = remaining.OpenRisk
				effectOp.RequiredCash = remaining.HeldCash
				effectOp.MaxRiskUSD = nil
			}
		}
		switch attempt.Intent {
		case "place":
			if effectOp.Action == "open" {
				positionRisk, workingRisk, err := aggregateExternalOpenRisk(snapshot, gate)
				if err != nil {
					return err
				}
				e.ExternalPositionRisk, e.ExternalWorkingRisk = positionRisk, workingRisk
				e.AggregateOpenRisk, err = addMicros(resources.OpenRisk, positionRisk, workingRisk)
				if err != nil {
					return err
				}
				e.EffectiveBuyingPower, err = spendableBuyingPower(snapshot.Account.BuyingPower, resources.HeldCash)
				if err != nil {
					return err
				}
				window, err := s.databaseMarketWindow(gate)
				if err != nil {
					return err
				}
				trades, err := gate.CountTradesForDayExcluding(false, window.start, window.end, attempt.OperationID)
				if err != nil {
					return err
				}
				day := risk.DayState{
					TradesToday: trades, OpenRisk: e.AggregateOpenRisk,
					Equity: snapshot.Account.Equity, EquityKnown: snapshot.Account.EquityKnown,
					BuyingPower: e.EffectiveBuyingPower,
				}
				boundVerdict := risk.ClassifyAt(effectOp, bound.Policy, day, quote, databaseNow)
				activeVerdict := risk.ClassifyAt(effectOp, active.Policy, day, quote, databaseNow)
				if boundVerdict.Class == "REJECT" || activeVerdict.Class == "REJECT" ||
					(!reviewApproved && (boundVerdict.Class != "B" || activeVerdict.Class != "B")) {
					return fmt.Errorf("open authority became stale: bound=%s active=%s", firstReason(boundVerdict), firstReason(activeVerdict))
				}
			} else if effectOp.Action == "close" {
				externalClose, err := validateFreshCloseCapacity(snapshot, effectOp, otherClose)
				if err != nil {
					return err
				}
				e.ExternalWorkingClose = externalClose
			} else {
				return fmt.Errorf("place operation changed semantics")
			}
		case "cancel":
			if target == nil || target.BrokerOrderID != attempt.TargetBrokerOrderID {
				return fmt.Errorf("cancel target changed at pre-effect barrier")
			}
			if op.Action == "cancel" {
				mayOpen, mayClose, err := workingOrderEffects(*target, snapshot.Positions)
				if err != nil || !mayOpen || mayClose || op.CancelTargetEffect != "opening" ||
					op.CancelTargetFingerprint == "" || cancelTargetFingerprint(*target) != op.CancelTargetFingerprint {
					return fmt.Errorf("cancel target is not proven risk-reducing")
				}
			}
		default:
			return fmt.Errorf("unsupported pre-effect intent")
		}
		evaluation = e
		return nil
	})
	if err == nil || errors.Is(err, store.ErrUnavailable) || errors.Is(err, errPreEffectRejected) {
		return evaluation, err
	}
	return nil, fmt.Errorf("%w: %v", errPreEffectRejected, err)
}

func preEffectRequiresProposalTTL(attempt *store.ExecutionAttempt, op risk.Operation) bool {
	if attempt.Intent == "cancel" && op.Action != "cancel" {
		return false
	}
	// A replacement place is durable recovery of an already-authorized working
	// order and intentionally survives the original proposal TTL. It still
	// crosses every current-policy, aggregate-risk and Provider-fact barrier.
	return attempt.Intent != "place" || attempt.Seq <= 1
}

func aggregateExternalOpenRisk(snapshot *providerSnapshot, gate store.OperationGate) (units.Micros, units.Micros, error) {
	var positionRisk, workingRisk units.Micros
	seenPositions := map[string]bool{}
	for _, position := range snapshot.Positions {
		if position.Kind == "option" {
			return 0, 0, fmt.Errorf("option-position coexistence is not certified")
		}
		key := position.Kind + "\x00" + position.Symbol
		if seenPositions[key] {
			return 0, 0, fmt.Errorf("aggregate position identity is ambiguous")
		}
		seenPositions[key] = true
		if position.Qty < 0 {
			return 0, 0, fmt.Errorf("external short position cannot authorize new risk")
		}
		internalQty, err := gate.OpenExposureQuantity("live", position.Symbol, position.Kind)
		if err != nil {
			return 0, 0, err
		}
		if internalQty < 0 {
			return 0, 0, fmt.Errorf("internal exposure quantity is invalid")
		}
		if position.Qty <= internalQty {
			continue
		}
		extra := position.Qty - internalQty
		if !position.AvgPriceKnown || position.AvgPrice <= 0 {
			return 0, 0, fmt.Errorf("external position risk is unknown")
		}
		riskAmount, err := units.MulQtyPrice(extra, position.AvgPrice, position.Multiplier, true)
		if err != nil {
			return 0, 0, err
		}
		positionRisk, err = units.Add(positionRisk, riskAmount)
		if err != nil {
			return 0, 0, err
		}
	}
	origins := map[string]string{}
	for _, object := range snapshot.View.Objects {
		if object.Family == store.BrokerFamilyOrders {
			origins[object.ObjectKey] = object.Origin
		}
	}
	for _, order := range snapshot.Orders {
		if origins[order.BrokerOrderID] == "alpheus" {
			continue
		}
		if order.Kind == "option" {
			return 0, 0, fmt.Errorf("external option-order coexistence is not certified")
		}
		mayOpen, mayClose, err := workingOrderEffects(order, snapshot.Positions)
		if err != nil || (mayOpen && mayClose) {
			return 0, 0, fmt.Errorf("working order effect is ambiguous")
		}
		if !mayOpen {
			continue
		}
		if order.Side != "buy" || !order.LimitPriceKnown || order.LimitPrice <= 0 {
			return 0, 0, fmt.Errorf("working opening order risk is unbounded")
		}
		remaining := order.Qty - order.FilledQty
		multiplier := int64(1)
		if order.Kind == "option" {
			multiplier = 100
		}
		riskAmount, err := units.MulQtyPrice(remaining, order.LimitPrice, multiplier, true)
		if err != nil {
			return 0, 0, err
		}
		workingRisk, err = units.Add(workingRisk, riskAmount)
		if err != nil {
			return 0, 0, err
		}
	}
	return positionRisk, workingRisk, nil
}

func validateFreshCloseCapacity(snapshot *providerSnapshot, op risk.Operation, otherLocal units.Qty) (units.Qty, error) {
	var matched *broker.Position
	for i := range snapshot.Positions {
		position := &snapshot.Positions[i]
		if position.Symbol != operationSymbol(op) || position.Qty == 0 {
			continue
		}
		if matched != nil {
			return 0, fmt.Errorf("close position identity is ambiguous")
		}
		matched = position
	}
	if matched == nil || matched.PositionID != op.BrokerPositionID || matched.Qty != op.DecisionPositionQty ||
		matched.Kind != op.Kind || matched.Multiplier != op.Multiplier || op.InstrumentID == "" ||
		(matched.Kind == "option" && matched.InstrumentID != op.InstrumentID) ||
		(matched.Kind == "equity" && matched.InstrumentID != "" && matched.InstrumentID != op.InstrumentID) {
		return 0, fmt.Errorf("close position changed at pre-effect barrier")
	}
	wantSide := "sell"
	if matched.Qty < 0 {
		wantSide = "buy"
	}
	if op.Side != wantSide {
		return 0, fmt.Errorf("close direction changed at pre-effect barrier")
	}
	positionQty, err := units.AbsQty(matched.Qty)
	if err != nil {
		return 0, err
	}
	var externalClose units.Qty
	origins := map[string]string{}
	for _, object := range snapshot.View.Objects {
		if object.Family == store.BrokerFamilyOrders {
			origins[object.ObjectKey] = object.Origin
		}
	}
	for _, order := range snapshot.Orders {
		if origins[order.BrokerOrderID] == "alpheus" {
			continue
		}
		mayOpen, mayClose, effectErr := workingOrderEffects(order, snapshot.Positions)
		if effectErr != nil || (mayOpen && mayClose) {
			return 0, fmt.Errorf("working close order effect is ambiguous")
		}
		if mayClose && order.Symbol == matched.Symbol && order.Kind == matched.Kind {
			externalClose, effectErr = units.AddQty(externalClose, order.Qty-order.FilledQty)
			if effectErr != nil {
				return 0, effectErr
			}
		}
	}
	reserved, err := units.AddQty(otherLocal, externalClose)
	if err != nil {
		return 0, err
	}
	if reserved > positionQty || op.Qty > positionQty-reserved {
		return 0, fmt.Errorf("proposal is stale: aggregate closable quantity changed")
	}
	return externalClose, nil
}

func workingOrderEffects(order broker.ReadOrder, positions []broker.Position) (bool, bool, error) {
	remaining := order.Qty - order.FilledQty
	if remaining <= 0 {
		return false, false, fmt.Errorf("working order has no remaining quantity")
	}
	declaredClose := false
	switch order.PositionEffect {
	case "open":
		return true, false, nil
	case "close":
		declaredClose = true
	case "unknown":
	default:
		return false, false, fmt.Errorf("working order effect is invalid")
	}
	var position *broker.Position
	for i := range positions {
		candidate := &positions[i]
		identityMatches := false
		if order.InstrumentID != "" && candidate.InstrumentID != "" {
			identityMatches = candidate.InstrumentID == order.InstrumentID
		} else {
			identityMatches = candidate.Symbol == order.Symbol && candidate.Kind == order.Kind
		}
		if !identityMatches || candidate.Qty == 0 {
			continue
		}
		if position != nil {
			return false, false, fmt.Errorf("working order position match is ambiguous")
		}
		position = candidate
	}
	if position == nil {
		if declaredClose {
			return true, true, nil
		}
		return true, false, nil
	}
	positionQty, err := units.AbsQty(position.Qty)
	if err != nil {
		return false, false, err
	}
	closingSide := "sell"
	if position.Qty < 0 {
		closingSide = "buy"
	}
	if order.Side != closingSide {
		if declaredClose {
			return true, true, nil
		}
		return true, false, nil
	}
	if remaining > positionQty {
		return true, true, nil
	}
	return false, true, nil
}

func sameReferenceOrderVisible(orders []broker.ReadOrder, clientOrderID string) bool {
	if strings.TrimSpace(clientOrderID) == "" {
		return false
	}
	for _, order := range orders {
		if order.ClientOrderID == clientOrderID {
			return true
		}
	}
	return false
}

func addMicros(values ...units.Micros) (units.Micros, error) {
	var total units.Micros
	var err error
	for _, value := range values {
		total, err = units.Add(total, value)
		if err != nil {
			return 0, err
		}
	}
	return total, nil
}

func earlierTime(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

func preEffectInstrumentMatches(op risk.Operation, attempt *store.ExecutionAttempt, instrument broker.Instrument) bool {
	stopMatches := op.StopPrice == nil || instrument.SupportsPrice(*op.StopPrice)
	return instrument.InstrumentID != "" && instrument.InstrumentID == op.InstrumentID &&
		instrument.Kind == op.Kind && instrument.Multiplier == op.Multiplier &&
		instrument.Source != "" && !instrument.AsOf.IsZero() && !instrument.AsOf.After(time.Now().UTC()) &&
		instrument.PrecisionSane() && instrument.SupportsPrice(attempt.Limit) && stopMatches &&
		instrument.QtyIncrement > 0 && attempt.Qty%instrument.QtyIncrement == 0
}

func preEffectQuoteMatches(op risk.Operation, quote broker.Quote, instrument broker.Instrument) bool {
	symbolMatches := quote.Symbol == operationSymbol(op)
	if op.Kind == "option" {
		symbolMatches = symbolMatches || quote.Symbol == instrument.InstrumentID
	}
	return symbolMatches && quote.Source != "" && quote.Source == instrument.Source
}

func (s *server) recordPreEffectRefusal(attempt *store.ExecutionAttempt, reason string) {
	log.Printf("pre-effect refused: attempt_id=%s reason=%s", attempt.ID, reason)
	_ = s.store.InsertEvent("execution_pre_effect_refused", map[string]any{
		"attempt_id": attempt.ID, "fencing_token": attempt.Attempt, "reason": reason,
	})
}

func brokerFillsValid(fills []broker.ReadFill, source string, completedAt time.Time) bool {
	seen := map[string]bool{}
	for _, fill := range fills {
		if fill.FillID == "" || fill.BrokerOrderID == "" || fill.Symbol == "" ||
			(fill.Side != "buy" && fill.Side != "sell") || fill.Qty <= 0 || fill.Price <= 0 || fill.Fees < 0 ||
			fill.Source != source || fill.AsOf.IsZero() || fill.AsOf.After(completedAt) || seen[fill.FillID] {
			return false
		}
		seen[fill.FillID] = true
	}
	return true
}
