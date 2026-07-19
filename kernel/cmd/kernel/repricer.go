package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

var errRepriceIneligible = errors.New("order is not eligible for repricing")

func startRepricer(s *server) error {
	// Production Robinhood remains intentionally read-only pending M11. The absence
	// of an execution capability is a construction-time guarantee that this
	// worker cannot issue a broker effect in read-only deployments.
	if s.executionProvider() == nil {
		return nil
	}
	go func() {
		// Policy owns each order's durable eligibility interval. This short code-
		// owned scan cadence is only a scheduler resolution, never authorization.
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if err := s.repriceOnce(context.Background()); err != nil {
				log.Printf("repricer: %v", err)
			}
		}
	}()
	return nil
}

func (s *server) repriceOnce(ctx context.Context) error {
	if s.executionProvider() == nil {
		return nil
	}
	orders, err := s.store.ListWorkingOrders(reconcileBatchSize)
	if err != nil {
		return err
	}
	var cycleErr error
	for i := range orders {
		if err := s.repriceOrder(ctx, &orders[i]); err != nil {
			cycleErr = errors.Join(cycleErr, fmt.Errorf("order %s: %w", orders[i].ID, err))
		}
	}
	return cycleErr
}

func (s *server) repriceOrder(ctx context.Context, order *store.Order) error {
	row, err := s.store.GetOperation(order.OperationID)
	if err != nil {
		return err
	}
	var op risk.Operation
	if err := json.Unmarshal(row.Payload, &op); err != nil {
		return fmt.Errorf("decode persisted operation: %w", err)
	}
	if err := validateOrderPolicyBinding(row, order); err != nil {
		return err
	}
	if op.Action != "open" && op.Action != "close" {
		return nil
	}
	effective, err := s.effectiveOrderPolicy(order)
	if err != nil {
		return err
	}
	databaseNow, err := s.store.DatabaseNow()
	if err != nil {
		return err
	}
	if !order.WorkingSince.IsZero() && databaseNow.Before(order.WorkingSince.Add(time.Duration(effective.repriceIntervalSec)*time.Second)) {
		return nil
	}

	policyReason, err := s.repricePolicy(ctx, op, order)
	if err != nil {
		return err
	}
	if policyReason == "" {
		// Do not disturb a working order until a fresh, sane quote and a valid
		// tick prove that a bounded replacement can be staged after cancellation.
		if _, err := s.boundedReplacementLimit(ctx, op, order); err != nil {
			if errors.Is(err, errRepriceIneligible) {
				return nil
			}
			return err
		}
	}

	cancelAttempt, err := s.store.StageRepriceCancel(order.ID)
	if err != nil || cancelAttempt == nil {
		return err
	}
	return s.executePendingRepriceCancel(ctx, cancelAttempt, order, op, policyReason)
}

func (s *server) executePendingRepriceCancel(ctx context.Context, cancelAttempt *store.ExecutionAttempt, order *store.Order, op risk.Operation, policyReason string) error {
	claimed, err := s.claimPendingAttempt(cancelAttempt.ID)
	if err != nil || claimed == nil {
		return err
	}
	return s.executeClaimedRepriceCancel(ctx, claimed, order, op, policyReason)
}

func (s *server) executeClaimedRepriceCancel(ctx context.Context, claimed *store.ExecutionAttempt, order *store.Order, op risk.Operation, policyReason string) error {
	bindingCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	bindingErr := s.assertLiveAccountBinding(bindingCtx, order.OperationID)
	cancel()
	if bindingErr != nil {
		_, resolveErr := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
			State: "failed", LastError: "account binding failed",
		})
		return errors.Join(bindingErr, resolveErr)
	}
	if s.tradingMode() == config.ModeLive {
		marked, markErr := s.store.MarkAttemptSent(claimed.ID, claimed.Attempt, false, 0, nil)
		if markErr != nil || !marked {
			return errors.Join(markErr, fmt.Errorf("provider cancel send was not durably marked"))
		}
	}

	brokerCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	_, cancelErr := s.executionProvider().CancelOrder(brokerCtx, order.BrokerOrderID)
	cancel()
	// A cancel acknowledgement is not enough: query the target itself so a
	// broker-side rejection cannot be mistaken for terminal order proof.
	queryCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	result, queryErr := s.executionProvider().GetOrder(queryCtx, order.BrokerOrderID)
	cancel()
	if queryErr != nil || !isTerminalOrderState(result.State) {
		lastError := "cancel target is not proven terminal"
		if cancelErr != nil {
			lastError = "cancel effect is ambiguous"
		}
		_, resolveErr := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
			State: "unknown", LastError: lastError,
		})
		return errors.Join(queryErr, resolveErr)
	}
	return s.finalizeRepriceResult(ctx, claimed, order, op, result, policyReason)
}

func (s *server) finalizeRepriceResult(ctx context.Context, cancelAttempt *store.ExecutionAttempt, order *store.Order, op risk.Operation, result broker.OrderResult, policyReason string) error {
	if !isTerminalOrderState(result.State) {
		return fmt.Errorf("cancel target has no terminal proof")
	}
	if result.BrokerOrderID == "" {
		result.BrokerOrderID = order.BrokerOrderID
	}

	var replacement *store.RepriceReplacement
	if result.State != "filled" && policyReason == "" {
		// The breaker may have tripped while the broker was processing cancel.
		// Re-evaluate it before creating another open effect.
		var err error
		policyReason, err = s.repricePolicy(ctx, op, order)
		if err != nil {
			return s.deferTerminalReprice(cancelAttempt, "reprice policy unavailable", err)
		}
		if policyReason == "" {
			limit, err := s.boundedReplacementLimit(ctx, op, order)
			if err != nil {
				return s.deferTerminalReprice(cancelAttempt, "replacement market data unavailable", err)
			}
			replacement = &store.RepriceReplacement{
				AttemptID: store.NewID(), OrderID: store.NewID(),
				ClientOrderID: store.NewID(), Limit: limit,
			}
		}
	}

	update := resolutionForOrder(&store.ExecutionAttempt{
		ID: order.ExecutionAttemptID, Intent: "place",
	}, result).OrderUpdate
	if update == nil {
		return s.deferTerminalReprice(cancelAttempt, "terminal order update unavailable", nil)
	}
	next, err := s.store.FinalizeRepriceCancel(
		cancelAttempt.ID, cancelAttempt.Attempt, *update, replacement, policyReason,
	)
	if err != nil {
		if errors.Is(err, store.ErrFillIntegrity) {
			_ = s.refreshGlobalHalt()
		}
		return err
	}
	if next != nil {
		_, err = s.executePendingAttempt(ctx, next.ID)
	}
	return err
}

func (s *server) deferTerminalReprice(attempt *store.ExecutionAttempt, message string, cause error) error {
	_, resolveErr := s.store.ResolveAttempt(attempt.ID, attempt.Attempt, store.AttemptResolution{
		State: "unknown", LastError: message,
	})
	return errors.Join(cause, resolveErr)
}

func (s *server) repricePolicy(ctx context.Context, op risk.Operation, order *store.Order) (string, error) {
	effective, err := s.effectiveOrderPolicy(order)
	if err != nil {
		return "", err
	}
	if order.Reprices >= effective.maxReprices {
		return "max_reprices", nil
	}
	if op.Action != "open" {
		return "", nil
	}
	halted, err := s.repriceLedgerHalted(ctx, op.Shadow, effective.currentLimits)
	if err != nil {
		return "", err
	}
	if halted {
		return "ledger_halted", nil
	}
	return "", nil
}

func (s *server) repriceLedgerHalted(ctx context.Context, shadow bool, limits config.Limits) (bool, error) {
	if err := s.refreshGlobalHalt(); err != nil {
		return true, err
	}
	globalHalted, globalReason := s.haltSnapshot()
	if globalHalted {
		return true, nil
	}
	var halted bool
	err := s.store.WithLedgerLock(shadow, func(gate store.OperationGate) error {
		var account broker.AccountState
		var err error
		if shadow {
			account, err = s.shadowAccountSnapshotWithLimits(ctx, gate, limits)
		} else {
			accountCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
			account, err = s.accountProvider().Account(accountCtx)
			cancel()
		}
		if err != nil {
			return err
		}
		window, err := s.databaseMarketWindow(gate)
		if err != nil {
			return err
		}
		day, err := s.dayStateAtAccountWithLimits(ctx, gate, shadow, account, window, globalHalted, globalReason, limits)
		if err != nil {
			return err
		}
		halted = day.Halted
		return s.ensureMarketDay(gate, window)
	})
	return halted, err
}

func (s *server) boundedReplacementLimit(ctx context.Context, op risk.Operation, order *store.Order) (units.Micros, error) {
	effective, err := s.effectiveOrderPolicy(order)
	if err != nil {
		return 0, err
	}
	if op.Action == "close" && op.Limit == nil {
		return 0, errRepriceIneligible
	}
	if op.Action == "open" && op.Side != "buy" {
		return 0, fmt.Errorf("unsupported open reprice side %q", op.Side)
	}
	if op.Action == "close" && op.Side != "buy" && op.Side != "sell" {
		return 0, fmt.Errorf("unsupported close reprice side %q", op.Side)
	}
	provider := s.marketProvider()
	if provider == nil {
		return 0, fmt.Errorf("market data provider unavailable")
	}
	quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	quote, err := provider.Quote(quoteCtx, order.Symbol)
	cancel()
	if err != nil || !quote.Usable(effective.quoteMaxAgeSec, time.Now().UTC()) {
		return 0, fmt.Errorf("market data unavailable for repricing")
	}
	instrumentCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	instrument, err := provider.Instrument(instrumentCtx, order.Symbol)
	cancel()
	if err != nil || !instrument.PrecisionSane() ||
		instrument.Kind != order.Kind || instrument.Multiplier != order.Multiplier {
		return 0, fmt.Errorf("instrument tick unavailable for repricing")
	}
	if !instrument.SupportsPrice(quote.Bid) || !instrument.SupportsPrice(quote.Ask) ||
		!instrument.SupportsPrice(order.Limit) ||
		order.Qty%instrument.QtyIncrement != 0 {
		return 0, fmt.Errorf("quote or quantity violates instrument increments")
	}

	if op.Side == "buy" {
		cap := order.ApprovedPriceBound
		if cap <= 0 {
			cap = op.ApprovedPriceCap
			if op.Action == "close" {
				cap = *op.Limit
			}
		}
		return nextBoundedBuyLimitForInstrument(order.Limit, quote.Ask, cap, instrument)
	}
	minimum := order.ApprovedPriceBound
	if minimum <= 0 {
		minimum = *op.Limit
	}
	return nextBoundedSellLimitForInstrument(order.Limit, quote.Bid, minimum, instrument)
}

type effectiveExecutionPolicy struct {
	maxReprices        int
	repriceIntervalSec int
	quoteMaxAgeSec     int
	currentLimits      config.Limits
}

func (s *server) effectiveOrderPolicy(order *store.Order) (effectiveExecutionPolicy, error) {
	if order == nil {
		return effectiveExecutionPolicy{}, fmt.Errorf("order is missing")
	}
	current, err := s.store.LoadKernelPolicyAuthority()
	if err != nil {
		return effectiveExecutionPolicy{}, err
	}
	boundMax, boundInterval, boundQuoteAge := order.MaxReprices, order.RepriceIntervalSec, order.QuoteMaxAgeSec
	if order.KernelPolicyRevisionID == 0 {
		// Explicit compatibility for pre-K1 rows only. Post-head inserts cannot
		// reach this branch because migration 0013 requires their binding.
		boundMax = s.limits.ExecutionPolicy.MaxReprices
		boundInterval = s.limits.ExecutionPolicy.RepriceIntervalSec
		boundQuoteAge = s.limits.QuoteMaxAgeSec
	}
	if boundMax < 0 || boundInterval <= 0 || boundQuoteAge <= 0 {
		return effectiveExecutionPolicy{}, fmt.Errorf("order execution envelope is invalid")
	}
	return effectiveExecutionPolicy{
		maxReprices:        minInt(boundMax, current.Policy.ExecutionPolicy.MaxReprices),
		repriceIntervalSec: maxInt(boundInterval, current.Policy.ExecutionPolicy.RepriceIntervalSec),
		quoteMaxAgeSec:     minInt(boundQuoteAge, current.Policy.QuoteMaxAgeSec),
		currentLimits:      current.Policy,
	}, nil
}

func validateOrderPolicyBinding(operation *store.OperationRow, order *store.Order) error {
	if operation == nil || order == nil {
		return fmt.Errorf("operation or order is missing")
	}
	if operation.KernelPolicyRevisionID == 0 && order.KernelPolicyRevisionID == 0 {
		return nil
	}
	if order.KernelPolicyRevisionID != operation.KernelPolicyRevisionID ||
		order.KernelPolicyGeneration != operation.KernelPolicyGeneration ||
		order.KernelPolicyDigest != operation.KernelPolicyDigest {
		return fmt.Errorf("order policy binding differs from operation")
	}
	return nil
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func nextBoundedBuyLimit(previous, ask, cap, tick units.Micros) (units.Micros, error) {
	return nextBoundedBuyLimitForInstrument(previous, ask, cap, broker.Instrument{
		PriceTick: tick, QtyIncrement: units.MustQty("1"),
	})
}

func nextBoundedBuyLimitForInstrument(previous, ask, cap units.Micros, instrument broker.Instrument) (units.Micros, error) {
	if previous <= 0 || ask <= 0 || cap <= 0 || !instrument.PrecisionSane() {
		return 0, fmt.Errorf("invalid buy reprice inputs")
	}
	marketTarget, err := ceilPriceForInstrument(ask, instrument)
	if err != nil {
		return 0, err
	}
	allowableCap := floorPriceForInstrument(cap, instrument)
	if allowableCap <= 0 {
		return 0, fmt.Errorf("buy cap has no valid price tick")
	}
	target := marketTarget
	if target > allowableCap {
		target = allowableCap
	}
	if target <= previous {
		return target, nil
	}
	distance := target - previous
	raw := previous + distance/2 + distance%2
	next, err := ceilPriceForInstrument(raw, instrument)
	if err != nil {
		return 0, err
	}
	if next > target {
		next = target
	}
	return next, nil
}

func nextBoundedSellLimit(previous, bid, minimum, tick units.Micros) (units.Micros, error) {
	return nextBoundedSellLimitForInstrument(previous, bid, minimum, broker.Instrument{
		PriceTick: tick, QtyIncrement: units.MustQty("1"),
	})
}

func nextBoundedSellLimitForInstrument(previous, bid, minimum units.Micros, instrument broker.Instrument) (units.Micros, error) {
	if previous <= 0 || bid <= 0 || minimum <= 0 || !instrument.PrecisionSane() {
		return 0, fmt.Errorf("invalid sell reprice inputs")
	}
	marketTarget := floorPriceForInstrument(bid, instrument)
	allowableMinimum, err := ceilPriceForInstrument(minimum, instrument)
	if err != nil {
		return 0, err
	}
	target := marketTarget
	if target < allowableMinimum {
		target = allowableMinimum
	}
	if target >= previous {
		return target, nil
	}
	distance := previous - target
	raw := previous - distance/2 - distance%2
	next := floorPriceForInstrument(raw, instrument)
	if next < target {
		next = target
	}
	return next, nil
}

func ceilPriceForInstrument(value units.Micros, instrument broker.Instrument) (units.Micros, error) {
	if !instrument.PrecisionSane() {
		return 0, fmt.Errorf("instrument precision is invalid")
	}
	result, err := ceilPriceToTick(value, instrument.TickForPrice(value))
	if err != nil || !instrument.SupportsPrice(result) {
		return 0, fmt.Errorf("price is outside the instrument tick schedule")
	}
	return result, nil
}

func floorPriceForInstrument(value units.Micros, instrument broker.Instrument) units.Micros {
	if !instrument.PrecisionSane() {
		return 0
	}
	result := floorPriceToTick(value, instrument.TickForPrice(value))
	if !instrument.SupportsPrice(result) {
		return 0
	}
	return result
}

func ceilPriceToTick(value, tick units.Micros) (units.Micros, error) {
	if value <= 0 || tick <= 0 {
		return 0, fmt.Errorf("price and tick must be positive")
	}
	remainder := value % tick
	if remainder == 0 {
		return value, nil
	}
	return units.Add(value, tick-remainder)
}

func floorPriceToTick(value, tick units.Micros) units.Micros {
	if value <= 0 || tick <= 0 {
		return 0
	}
	return value - value%tick
}
