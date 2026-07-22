package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/policy"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

const reconcileBatchSize = 100

type excludingDayStateReader interface {
	DatabaseNow() (time.Time, error)
	CountTradesForDayExcluding(shadow bool, start, end time.Time, operationID string) (int, error)
	LedgerResources(ledger, excludeOperationID string) (store.LedgerResources, error)
	InsertDayOpen(marketDay time.Time, ledger string, equity units.Micros) error
	EvaluateDayRisk(input store.DayRiskInput) (store.DayRiskStats, error)
}

func (s *server) operationPolicyPair(operation *store.OperationRow) (config.Limits, config.Limits, error) {
	current, err := s.store.LoadKernelPolicyAuthority()
	if err != nil {
		return config.Limits{}, config.Limits{}, err
	}
	if operation == nil {
		return config.Limits{}, config.Limits{}, store.ErrKernelPolicyAuthorityInvalid
	}
	if operation.KernelPolicyRevisionID == 0 {
		// Historical pre-K1 rows remain explicit. They may finish only under the
		// old startup snapshot plus any stricter current policy.
		return s.limits, current.Policy, nil
	}
	bound, err := s.store.LoadBoundKernelPolicy(operation)
	if err != nil {
		return config.Limits{}, config.Limits{}, err
	}
	return bound.Policy, current.Policy, nil
}

func validateAttemptPolicyBinding(operation *store.OperationRow, attempt *store.ExecutionAttempt) error {
	if operation == nil || attempt == nil {
		return fmt.Errorf("operation or execution attempt is missing")
	}
	if operation.KernelPolicyRevisionID == 0 && attempt.KernelPolicyRevisionID == 0 {
		return nil
	}
	if attempt.KernelPolicyRevisionID != operation.KernelPolicyRevisionID ||
		attempt.KernelPolicyGeneration != operation.KernelPolicyGeneration ||
		attempt.KernelPolicyDigest != operation.KernelPolicyDigest {
		return fmt.Errorf("execution attempt policy binding differs from operation")
	}
	return nil
}

func (s *server) dayStateAtAccountExcluding(ctx context.Context, gate excludingDayStateReader, shadow bool, account broker.AccountState, window marketWindow, halted bool, haltReason, operationID string, limits config.Limits) (risk.DayState, error) {
	n, err := gate.CountTradesForDayExcluding(shadow, window.start, window.end, operationID)
	if err != nil {
		return risk.DayState{}, err
	}
	ledger := ledgerName(shadow)
	resources, err := gate.LedgerResources(ledger, operationID)
	if err != nil {
		return risk.DayState{}, err
	}
	buyingPower, err := spendableBuyingPower(account.BuyingPower, resources.HeldCash)
	if err != nil {
		return risk.DayState{}, err
	}
	if err := gate.InsertDayOpen(window.day, ledger, account.Equity); err != nil {
		return risk.DayState{}, err
	}
	providerPnL, err := s.providerRealizedPnL(ctx, shadow, window.day)
	if err != nil {
		return risk.DayState{}, err
	}
	if err := s.ensureMarketDay(gate, window); err != nil {
		return risk.DayState{}, err
	}
	stats, err := gate.EvaluateDayRisk(store.DayRiskInput{
		Ledger: ledger, MarketDay: window.day, Start: window.start, End: window.end,
		ObservedAt:              window.asOf,
		ProviderRealizedPnL:     providerPnL,
		MaxDailyLossPct:         limits.HardLimits.MaxDailyLossPct,
		ConsecutiveLossDaysHalt: limits.HardLimits.ConsecutiveLossDaysHalt,
		PnLReconciliationLimit:  limits.PnLReconciliationTolerance,
	})
	if err != nil {
		return risk.DayState{}, err
	}
	if err := s.ensureMarketDay(gate, window); err != nil {
		return risk.DayState{}, err
	}
	if !halted && stats.Halted {
		halted, haltReason = true, stats.Reason
	}
	return risk.DayState{
		TradesToday: n, OpenRisk: resources.OpenRisk, Equity: account.Equity,
		EquityKnown: account.EquityKnown, BuyingPower: buyingPower,
		RealizedPnL: stats.EffectiveRealizedPnL, LocalRealizedPnL: stats.LocalRealizedPnL,
		ProviderRealizedPnL: stats.ProviderRealizedPnL, DailyLossLimit: stats.DailyLossLimit,
		ConsecutiveLossDays: stats.ConsecutiveLossDays,
		Halted:              halted, HaltReason: haltReason,
	}, nil
}

func (s *server) attemptClaimTimeout() time.Duration {
	if s.claimTimeout > 0 {
		return s.claimTimeout
	}
	return 30 * time.Second
}

func (s *server) attemptStaleAfter() time.Duration {
	if s.attemptStale > 0 {
		return s.attemptStale
	}
	return 3 * time.Second
}

func startAttemptReconciler(s *server) error {
	// Complete the first scan before the HTTP listener opens. New proposals must
	// not race startup recovery of reservations left by the previous process.
	if err := s.reconcileAttempts(context.Background()); err != nil {
		return err
	}
	go func() {
		interval := s.attemptStaleAfter() / 2
		if interval < 100*time.Millisecond {
			interval = 100 * time.Millisecond
		}
		if interval > time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			if err := s.reconcileAttempts(context.Background()); err != nil {
				log.Printf("attempt reconciler: %v", err)
			}
		}
	}()
	return nil
}

func (s *server) reconcileAttempts(ctx context.Context) error {
	now := time.Now().UTC()
	attempts, err := s.store.ListRecoverableAttempts(s.attemptStaleAfter(), reconcileBatchSize)
	if err != nil {
		return err
	}
	for i := range attempts {
		attempt := attempts[i]
		switch attempt.State {
		case "pending":
			if err := s.reconcilePendingAttempt(ctx, &attempt); err != nil {
				log.Printf("reconcile pending attempt %s: %v", attempt.ID, err)
			}
		case "claimed", "unknown":
			if err := s.reconcileUncertainAttempt(ctx, &attempt, now); err != nil {
				log.Printf("reconcile uncertain attempt %s: %v", attempt.ID, err)
			}
		}
	}
	if err := s.reconcileWorkingOrders(ctx); err != nil {
		return err
	}
	return s.reconcileTerminalReservations(ctx)
}

func (s *server) reconcileTerminalReservations(ctx context.Context) error {
	candidates, err := s.store.ListTerminalReservationCandidates(reconcileBatchSize)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		provenFilled, terminalProof := candidate.DurableFilledQty, false
		switch {
		case candidate.SafeWithoutBroker:
			terminalProof = true
		case candidate.Ledger == "shadow":
			terminalProof = isTerminalOrderState(candidate.OrderState)
		case candidate.BrokerOrderID != "" && s.executionProvider() != nil:
			queryCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
			result, queryErr := s.executionProvider().GetOrder(queryCtx, candidate.BrokerOrderID)
			cancel()
			if queryErr != nil || !isTerminalOrderState(result.State) {
				log.Printf("terminal reservation %s remains held: broker terminal proof unavailable", candidate.ReservationID)
				continue
			}
			provenFilled, terminalProof = result.FilledQty, true
		default:
			log.Printf("terminal reservation %s remains held: no safe proof path", candidate.ReservationID)
			continue
		}
		if provenFilled != candidate.DurableFilledQty {
			log.Printf("terminal reservation %s remains held: broker filled=%s durable=%s",
				candidate.ReservationID, provenFilled, candidate.DurableFilledQty)
			continue
		}
		released, err := s.store.ReleaseProvenTerminalReservation(candidate, provenFilled, terminalProof)
		if err != nil {
			log.Printf("reconcile terminal reservation %s: %v", candidate.ReservationID, err)
		} else if !released {
			log.Printf("terminal reservation %s remains held: proof changed before release", candidate.ReservationID)
		}
	}
	return nil
}

func isTerminalOrderState(state string) bool {
	switch state {
	case "filled", "cancelled", "rejected", "expired":
		return true
	default:
		return false
	}
}

func (s *server) reconcileWorkingOrders(ctx context.Context) error {
	if halted, reason := s.haltSnapshot(); halted && strings.HasPrefix(reason, store.ErrFillIntegrity.Error()) {
		return nil
	}
	execution := s.executionProvider()
	if execution == nil {
		return nil
	}
	orders, err := s.store.ListWorkingOrders(reconcileBatchSize)
	if err != nil {
		return err
	}
	for _, order := range orders {
		queryCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		result, queryErr := execution.GetOrder(queryCtx, order.BrokerOrderID)
		cancel()
		if queryErr != nil {
			if !errors.Is(queryErr, broker.ErrNotFound) {
				log.Printf("reconcile order %s: %v", order.ID, queryErr)
			}
			continue
		}
		update := resolutionForOrder(&store.ExecutionAttempt{
			ID: order.ExecutionAttemptID, Intent: "place",
		}, result).OrderUpdate
		if update == nil {
			continue
		}
		if err := s.store.ApplyOrderUpdate(*update); err != nil {
			if errors.Is(err, store.ErrFillIntegrity) {
				_ = s.refreshGlobalHalt()
				return err
			}
			log.Printf("reconcile order %s: %v", order.ID, err)
		}
	}
	return nil
}

func (s *server) reconcilePendingAttempt(ctx context.Context, attempt *store.ExecutionAttempt) error {
	if attempt == nil || attempt.ID == "" {
		return fmt.Errorf("recoverable attempt is missing")
	}
	durableAttempt, err := s.store.GetExecutionAttempt(attempt.ID)
	if err != nil {
		return err
	}
	attempt = durableAttempt
	row, err := s.store.GetOperation(attempt.OperationID)
	if err != nil {
		return err
	}
	var op risk.Operation
	if err := json.Unmarshal(row.Payload, &op); err != nil {
		_, failErr := s.store.FailPendingAttempt(attempt.ID, "persisted operation is invalid")
		return errors.Join(err, failErr)
	}
	if err := validateAttemptPolicyBinding(row, attempt); err != nil {
		_, failErr := s.store.FailPendingAttempt(attempt.ID, "execution policy binding mismatch")
		return errors.Join(err, failErr)
	}
	boundLimits, currentLimits, err := s.operationPolicyPair(row)
	if err != nil {
		return err
	}
	if attempt.Intent == "cancel" && (op.Action == "open" || op.Action == "close") {
		return s.reconcilePendingRepriceCancel(ctx, attempt, op)
	}
	if attempt.Intent == "place" && attempt.Seq > 1 && (op.Action == "open" || op.Action == "close") {
		return s.reconcilePendingReplacement(ctx, attempt, op)
	}
	reviewApproved := row.Class == "C" && row.Status == "approved"
	databaseNow, clockErr := s.store.DatabaseNow()
	if clockErr != nil {
		return clockErr
	}
	if !reviewApproved && (row.ExpiresAt.IsZero() || !databaseNow.Before(row.ExpiresAt)) {
		_, failErr := s.store.FailPendingAttempt(attempt.ID, "proposal expired before recovery")
		return failErr
	}

	switch op.Action {
	case "open":
		m3aActive, err := s.store.FeatureActive("m3a")
		if err != nil {
			return err
		}
		if m3aActive {
			if attempt.OpenReservationID == "" {
				_, failErr := s.store.FailPendingAttempt(attempt.ID, "open reservation is missing")
				return failErr
			}
			reservation, err := s.store.GetOpenReservation(attempt.OpenReservationID)
			if err != nil || reservation.OperationID != attempt.OperationID || reservation.ResourceState != "held" {
				_, failErr := s.store.FailPendingAttempt(attempt.ID, "open reservation is not held")
				return errors.Join(err, failErr)
			}
			granted, err := s.store.HasTradeGrant(attempt.OperationID)
			if err != nil || !granted {
				_, failErr := s.store.FailPendingAttempt(attempt.ID, "trade grant is missing")
				return errors.Join(err, failErr)
			}
		}
		quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		quote, err := s.authorityMarketProvider().Quote(quoteCtx, operationSymbol(op))
		cancel()
		if err != nil {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "market data unavailable during recovery")
			return failErr
		}
		boundCandidate := op
		cap := op.ApprovedPriceCap
		boundCandidate.Limit = &cap
		boundCandidate.RejectReason = ""
		boundCandidate = s.deriveOpenOperationWithLimits(ctx, boundCandidate, &quote, boundLimits)
		currentCandidate := op
		currentCandidate.Limit = &cap
		currentCandidate.RejectReason = ""
		currentCandidate = s.deriveOpenOperationWithLimits(ctx, currentCandidate, &quote, currentLimits)
		if boundCandidate.Multiplier != op.Multiplier || boundCandidate.Qty != op.Qty ||
			boundCandidate.ApprovedPriceCap != op.ApprovedPriceCap ||
			boundCandidate.DerivedMaxRisk != op.DerivedMaxRisk ||
			boundCandidate.RequiredCash != op.RequiredCash ||
			currentCandidate.Multiplier != op.Multiplier || currentCandidate.Qty != op.Qty ||
			currentCandidate.ApprovedPriceCap > op.ApprovedPriceCap ||
			currentCandidate.DerivedMaxRisk > op.DerivedMaxRisk ||
			currentCandidate.RequiredCash > op.RequiredCash {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "recovery changed immutable execution facts")
			return failErr
		}
		workingPrice := boundCandidate.WorkingPrice
		if currentCandidate.WorkingPrice < workingPrice {
			workingPrice = currentCandidate.WorkingPrice
		}
		if err := s.refreshGlobalHalt(); err != nil {
			return err
		}
		halted, haltReason := s.haltSnapshot()
		var verdict risk.Verdict
		err = s.store.WithLedgerLock(op.Shadow, func(gate store.OperationGate) error {
			var account broker.AccountState
			var err error
			if op.Shadow {
				markLimits := currentLimits
				markLimits.QuoteMaxAgeSec = minInt(boundLimits.QuoteMaxAgeSec, currentLimits.QuoteMaxAgeSec)
				account, err = s.shadowAccountSnapshotWithLimits(ctx, gate, markLimits)
			} else {
				accountCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
				account, err = s.authorityAccountProvider().Account(accountCtx)
				cancel()
			}
			if err != nil {
				return err
			}
			window, err := s.databaseMarketWindow(gate)
			if err != nil {
				return err
			}
			day, err := s.dayStateAtAccountExcluding(ctx, gate, op.Shadow, account, window, halted, haltReason, attempt.OperationID, currentLimits)
			if err != nil {
				return err
			}
			boundVerdict := risk.Classify(boundCandidate, boundLimits, day, &quote)
			currentVerdict := risk.Classify(currentCandidate, currentLimits, day, &quote)
			verdict = currentVerdict
			if boundVerdict.Class == "REJECT" {
				verdict = boundVerdict
			} else if !reviewApproved && boundVerdict.Class != "B" {
				verdict = boundVerdict
			}
			return s.ensureMarketDay(gate, window)
		})
		if err != nil {
			return err
		}
		if verdict.Class == "REJECT" || (!reviewApproved && verdict.Class != "B") {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "recovery gate failed: "+firstReason(verdict))
			return failErr
		}
		if attempt.Intent != "paper_place" {
			if _, err := s.store.UpdatePendingAttemptLimit(attempt.ID, workingPrice); err != nil {
				return err
			}
		}
	case "close":
		reservation, err := s.store.GetCloseReservation(attempt.CloseReservationID)
		if err != nil || reservation.State != "held" {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "close reservation is not held")
			return errors.Join(err, failErr)
		}
		quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		quote, err := s.authorityMarketProvider().Quote(quoteCtx, reservation.Symbol)
		cancel()
		quoteMaxAge := minInt(boundLimits.QuoteMaxAgeSec, currentLimits.QuoteMaxAgeSec)
		if err != nil || !quote.Usable(quoteMaxAge, time.Now().UTC()) {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "market data unavailable during recovery")
			return failErr
		}
		var positionOK bool
		shadow := reservation.Ledger == "shadow"
		err = s.store.WithProposalLock(nil, shadow, false, func(gate store.OperationGate) error {
			if err := gate.LockLedgerSymbol(reservation.Ledger, reservation.Symbol); err != nil {
				return err
			}
			var quantity units.Qty
			if shadow {
				positions, err := gate.ShadowPositions()
				if err != nil {
					return err
				}
				for i := range positions {
					if positions[i].Symbol == reservation.Symbol && positions[i].Qty > 0 {
						if positions[i].Kind != op.Kind || positions[i].Multiplier != op.Multiplier {
							return nil
						}
						quantity = positions[i].Qty
						op.Side, op.VerifiedReduction = "sell", true
						break
					}
				}
			} else {
				positionCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
				positions, err := s.authorityAccountProvider().Positions(positionCtx)
				cancel()
				if err != nil {
					return err
				}
				quantity, err = closablePositionQuantity(op, positions)
				if err != nil {
					return nil
				}
				normalized, err := normalizeClose(op, positions)
				if err != nil || normalized.Side != op.Side || normalized.Kind != op.Kind || normalized.Multiplier != op.Multiplier {
					return nil
				}
			}
			exposure, err := gate.OpenExposureQuantity(reservation.Ledger, reservation.Symbol, op.Kind)
			if err != nil {
				return err
			}
			if quantity != exposure {
				if err := gate.InsertEvent("position_exposure_mismatch", map[string]any{
					"operation_id": attempt.OperationID,
					"ledger":       reservation.Ledger, "symbol": reservation.Symbol,
					"position_qty": quantity, "exposure_qty": exposure,
					"during": "close_recovery",
				}); err != nil {
					return err
				}
			}
			closable, valid := recoveredCloseCapacity(op, quantity, exposure)
			if !valid {
				return nil
			}
			held, err := gate.HeldCloseQuantity(reservation.Ledger, reservation.Symbol)
			if err != nil {
				return err
			}
			// Revalidation owns this reservation already. Subtract every other
			// held close, then apply this one exactly once.
			if held < reservation.RemainingQty {
				return fmt.Errorf("held close quantity is smaller than its reservation")
			}
			otherHeld := held - reservation.RemainingQty
			if otherHeld > closable || reservation.RemainingQty > closable-otherHeld {
				return nil
			}
			positionOK = true
			return nil
		})
		if err != nil {
			return err
		}
		if !positionOK {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "position no longer covers close reservation")
			return failErr
		}
	case "cancel":
		// A pending cancel has not reached the broker. Live execution still
		// requires the exact decision fingerprint and a fresh pre-effect target.
	default:
		_, failErr := s.store.FailPendingAttempt(attempt.ID, "unsupported recovered operation")
		return failErr
	}

	_, err = s.executePendingAttempt(ctx, attempt.ID)
	return err
}

func (s *server) reconcilePendingRepriceCancel(ctx context.Context, attempt *store.ExecutionAttempt, op risk.Operation) error {
	order, err := s.store.GetOrderByBrokerID(attempt.TargetBrokerOrderID)
	if err != nil {
		return err
	}
	policyReason, err := s.repricePolicy(ctx, op, order)
	if err != nil {
		return err
	}
	if policyReason == "" {
		if _, err := s.boundedReplacementLimit(ctx, op, order); err != nil {
			return err
		}
	}
	return s.executePendingRepriceCancel(ctx, attempt, order, op, policyReason)
}

func (s *server) reconcilePendingReplacement(ctx context.Context, attempt *store.ExecutionAttempt, op risk.Operation) error {
	order, err := s.store.GetOrderByAttempt(attempt.ID)
	if err != nil {
		return err
	}
	if _, err := s.boundedReplacementLimit(ctx, op, order); err != nil {
		return err
	}
	switch op.Action {
	case "open":
		reservation, err := s.store.GetOpenReservation(attempt.OpenReservationID)
		if err != nil || reservation.OperationID != attempt.OperationID ||
			reservation.ResourceState != "held" || reservation.RemainingQty != attempt.Qty {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "replacement open reservation is not held")
			return errors.Join(err, failErr)
		}
		granted, err := s.store.HasTradeGrant(attempt.OperationID)
		if err != nil || !granted {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "replacement trade grant is missing")
			return errors.Join(err, failErr)
		}
		effective, err := s.effectiveOrderPolicy(order)
		if err != nil {
			return err
		}
		halted, err := s.repriceLedgerHalted(ctx, op.Shadow, effective.currentLimits)
		if err != nil {
			return err
		}
		if halted {
			_, err := s.store.FailPendingAttempt(attempt.ID, "order_expired_policy:ledger_halted_before_replacement")
			return err
		}
	case "close":
		covered, err := s.replacementCloseStillCovered(ctx, attempt, op)
		if err != nil {
			return err
		}
		if !covered {
			_, err := s.store.FailPendingAttempt(attempt.ID, "replacement close is no longer covered")
			return err
		}
	}
	_, err = s.executePendingAttempt(ctx, attempt.ID)
	return err
}

func (s *server) replacementCloseStillCovered(ctx context.Context, attempt *store.ExecutionAttempt, op risk.Operation) (bool, error) {
	reservation, err := s.store.GetCloseReservation(attempt.CloseReservationID)
	if err != nil || reservation.OperationID != attempt.OperationID ||
		reservation.State != "held" || reservation.RemainingQty != attempt.Qty {
		return false, err
	}
	covered := false
	shadow := reservation.Ledger == "shadow"
	err = s.store.WithProposalLock(nil, shadow, false, func(gate store.OperationGate) error {
		if err := gate.LockLedgerSymbol(reservation.Ledger, reservation.Symbol); err != nil {
			return err
		}
		var quantity units.Qty
		if shadow {
			positions, err := gate.ShadowPositions()
			if err != nil {
				return err
			}
			for i := range positions {
				if positions[i].Symbol == reservation.Symbol && positions[i].Qty > 0 &&
					positions[i].Kind == op.Kind && positions[i].Multiplier == op.Multiplier {
					quantity = positions[i].Qty
					break
				}
			}
		} else {
			positionCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
			positions, err := s.authorityAccountProvider().Positions(positionCtx)
			cancel()
			if err != nil {
				return err
			}
			quantity, err = closablePositionQuantity(op, positions)
			if err != nil {
				return nil
			}
			candidate := op
			candidate.Qty = reservation.RemainingQty
			normalized, err := normalizeClose(candidate, positions)
			if err != nil || normalized.Side != op.Side || normalized.Kind != op.Kind || normalized.Multiplier != op.Multiplier {
				return nil
			}
		}
		exposure, err := gate.OpenExposureQuantity(reservation.Ledger, reservation.Symbol, op.Kind)
		if err != nil {
			return err
		}
		held, err := gate.HeldCloseQuantity(reservation.Ledger, reservation.Symbol)
		if err != nil {
			return err
		}
		closable, valid := recoveredCloseCapacity(op, quantity, exposure)
		if !valid {
			return nil
		}
		if held < reservation.RemainingQty {
			return fmt.Errorf("held close quantity is smaller than its reservation")
		}
		otherHeld := held - reservation.RemainingQty
		covered = otherHeld <= closable && reservation.RemainingQty <= closable-otherHeld
		return nil
	})
	return covered, err
}

func recoveredCloseCapacity(op risk.Operation, providerQty, exposureQty units.Qty) (units.Qty, bool) {
	if op.DecisionObservationID == "" {
		return minQty(providerQty, exposureQty), true
	}
	total, err := units.AddQty(op.TrackedCloseQty, op.ExternalCloseQty)
	if err != nil || total != op.Qty || op.TrackedCloseQty > exposureQty {
		return 0, false
	}
	return providerQty, true
}

func (s *server) reconcileUncertainAttempt(ctx context.Context, seen *store.ExecutionAttempt, now time.Time) error {
	claimed, err := s.claimRecoverableAttempt(seen)
	if err != nil || claimed == nil {
		return err
	}
	if claimed.Intent == "paper_place" {
		_, err := s.executeClaimedPaperAttempt(ctx, claimed)
		return err
	}
	if s.tradingMode() == config.ModeLive && claimed.SentAt.IsZero() && claimed.Intent == "place" {
		if reason, err := s.recoveredUnsentPlaceRefusal(claimed); err != nil {
			return err
		} else if reason != "" {
			_, resolveErr := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
				State: "failed", LastError: reason, ProviderErrorCode: "recovery_policy_refused",
				OperationStatus: "failed", ReleaseReservation: true,
				OrderUpdate: &store.OrderUpdate{ExecutionAttemptID: claimed.ID, State: "rejected"},
			})
			return resolveErr
		}
		_, err := s.executeClaimedAttemptWithReplay(ctx, claimed, false)
		return err
	}
	if claimed.Intent == "cancel" {
		row, readErr := s.store.GetOperation(claimed.OperationID)
		if readErr != nil {
			return readErr
		}
		var op risk.Operation
		if decodeErr := json.Unmarshal(row.Payload, &op); decodeErr != nil {
			return decodeErr
		}
		if op.Action == "open" || op.Action == "close" {
			if s.tradingMode() == config.ModeLive && claimed.SentAt.IsZero() {
				order, orderErr := s.store.GetOrderByBrokerID(claimed.TargetBrokerOrderID)
				if orderErr != nil {
					return orderErr
				}
				policyReason, policyErr := s.repricePolicy(ctx, op, order)
				if policyErr != nil {
					return policyErr
				}
				return s.executeClaimedRepriceCancel(ctx, claimed, order, op, policyReason)
			}
			return s.reconcileUncertainReprice(ctx, claimed, op)
		}
		if s.tradingMode() == config.ModeLive && claimed.SentAt.IsZero() {
			_, err := s.executeClaimedAttemptWithReplay(ctx, claimed, false)
			return err
		}
	}
	execution := s.executionProvider()
	if execution == nil {
		_, err := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
			State: "unknown", LastError: "execution capability unavailable",
		})
		return err
	}
	if claimed.Intent == "place" && s.tradingMode() == config.ModeLive {
		return s.reconcileLivePlaceAttempt(ctx, execution, claimed)
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	var result broker.OrderResult
	if claimed.Intent == "place" {
		finder, ok := execution.(broker.ClientOrderFinder)
		if !ok {
			cancel()
			_, resolveErr := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
				State: "unknown", LastError: "provider client-order lookup unavailable",
			})
			return resolveErr
		}
		result, err = finder.FindOrderByClientID(queryCtx, claimed.ClientOrderID)
	} else {
		result, err = execution.GetOrder(queryCtx, claimed.TargetBrokerOrderID)
	}
	cancel()
	if err == nil {
		resolution := resolutionForOrder(claimed, result)
		_, err = s.store.ResolveAttempt(claimed.ID, claimed.Attempt, resolution)
		return err
	}
	if errors.Is(err, broker.ErrNotFound) && claimed.Intent == "place" && s.providerDedupeVerified {
		_, err = s.executeClaimedAttempt(ctx, claimed)
		return err
	}
	_, resolveErr := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
		State: "unknown", LastError: "broker query did not prove the effect",
	})
	return resolveErr
}

func (s *server) recoveredUnsentPlaceRefusal(attempt *store.ExecutionAttempt) (string, error) {
	operation, err := s.store.GetOperation(attempt.OperationID)
	if err != nil {
		return "", err
	}
	if err := validateAttemptPolicyBinding(operation, attempt); err != nil {
		return "execution policy binding mismatch", nil
	}
	var op risk.Operation
	if err := json.Unmarshal(operation.Payload, &op); err != nil {
		return "persisted operation is invalid", nil
	}
	if op.Action != "open" {
		return "", nil
	}
	databaseNow, err := s.store.DatabaseNow()
	if err != nil {
		return "", err
	}
	reviewApproved := operation.Class == "C" && operation.Status == "approved"
	if !reviewApproved && (attempt.AuthorizationExpiresAt.IsZero() || !databaseNow.Before(attempt.AuthorizationExpiresAt)) {
		return "proposal expired before unsent recovery", nil
	}
	current, err := s.store.LoadKernelPolicyAuthority()
	if err != nil {
		return "", err
	}
	if current.Generation == operation.KernelPolicyGeneration {
		return "", nil
	}
	bound, err := s.store.LoadBoundKernelPolicy(operation)
	if err != nil {
		return "", err
	}
	change, err := policy.ClassifyChange(bound.Policy, current.Policy)
	if err != nil {
		return "", err
	}
	if change == policy.ChangeTighten || change == policy.ChangeMixed {
		// The B0 pre-effect barrier rechecks the recovered send later. Refuse the
		// unsent recovery here as well so tightened policy never reaches that
		// barrier as presumed authority.
		return "current policy tightened before unsent recovery", nil
	}
	return "", nil
}

func (s *server) reconcileLivePlaceAttempt(ctx context.Context, execution broker.ExecutionProvider, claimed *store.ExecutionAttempt) error {
	candidates, intent, queryErr := s.exactPlaceCandidatesForAttempt(ctx, execution, claimed)
	if queryErr != nil {
		return s.keepAttemptUnknown(claimed, "exact broker candidate query failed", "candidate_query_failed", "")
	}
	switch len(candidates) {
	case 1:
		return s.keepAttemptUnknown(claimed, "one exact broker candidate awaits human approval", "candidate_pending", candidates[0].BrokerOrderID)
	case 0:
		if claimed.ReplayCount == 0 && s.providerDedupeVerified &&
			s.providerReplayWindowBoundVerified && intent.Kind == "equity" {
			_, err := s.executeClaimedAttemptWithReplay(ctx, claimed, true)
			return err
		}
		return s.keepAttemptUnknown(claimed, "zero exact broker candidates; account remains latched", "candidate_zero", "")
	default:
		return s.keepAttemptUnknown(claimed, "multiple exact broker candidates; account remains latched", "candidate_ambiguous", "")
	}
}

func (s *server) exactPlaceCandidatesForAttempt(ctx context.Context, execution broker.ExecutionProvider, attempt *store.ExecutionAttempt) ([]broker.OrderResult, broker.ProviderPlaceIntent, error) {
	provider, ok := execution.(broker.ExactPlaceCandidateProvider)
	if !ok {
		return nil, broker.ProviderPlaceIntent{}, fmt.Errorf("exact provider candidate query unavailable")
	}
	var intent broker.ProviderPlaceIntent
	if err := json.Unmarshal(attempt.ProviderIntent, &intent); err != nil {
		return nil, broker.ProviderPlaceIntent{}, fmt.Errorf("durable provider intent is invalid")
	}
	canonical, err := json.Marshal(intent)
	if err != nil {
		return nil, broker.ProviderPlaceIntent{}, fmt.Errorf("durable provider intent is invalid")
	}
	digest := sha256.Sum256(canonical)
	if attempt.ProviderAccountID != s.boundRobinhoodAccountID() || !bytes.Equal(digest[:], attempt.IntentFingerprint) ||
		attempt.SendWindowStart.IsZero() || attempt.SendWindowEnd.IsZero() {
		return nil, broker.ProviderPlaceIntent{}, fmt.Errorf("durable provider intent evidence mismatch")
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	defer cancel()
	candidates, err := provider.FindExactPlaceCandidates(queryCtx, broker.ExactPlaceCandidateQuery{
		AccountID: attempt.ProviderAccountID, ClientOrderID: attempt.ClientOrderID,
		Intent: intent, WindowStart: attempt.SendWindowStart, WindowEnd: attempt.SendWindowEnd,
	})
	return candidates, intent, err
}

func (s *server) keepAttemptUnknown(claimed *store.ExecutionAttempt, message, code, candidateID string) error {
	_, err := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
		State: "unknown", LastError: message, ProviderErrorCode: code,
		CandidateBrokerOrderID: candidateID,
	})
	return err
}

func (s *server) reconcileUncertainReprice(ctx context.Context, claimed *store.ExecutionAttempt, op risk.Operation) error {
	execution := s.executionProvider()
	if execution == nil {
		_, err := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
			State: "unknown", LastError: "execution capability unavailable",
		})
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	result, queryErr := execution.GetOrder(queryCtx, claimed.TargetBrokerOrderID)
	cancel()
	if queryErr != nil || !isTerminalOrderState(result.State) {
		_, resolveErr := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
			State: "unknown", LastError: "cancel target is not proven terminal",
		})
		return errors.Join(queryErr, resolveErr)
	}
	order, err := s.store.GetOrderByBrokerID(claimed.TargetBrokerOrderID)
	if err != nil {
		return s.deferTerminalReprice(claimed, "durable cancel target unavailable", err)
	}
	return s.finalizeRepriceResult(ctx, claimed, order, op, result, "")
}

func firstReason(verdict risk.Verdict) string {
	if len(verdict.Reasons) == 0 {
		return fmt.Sprintf("class %s", verdict.Class)
	}
	return verdict.Reasons[0]
}
