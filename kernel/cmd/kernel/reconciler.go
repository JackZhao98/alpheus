package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/risk"
	"alpheus/kernel/internal/store"
)

const reconcileBatchSize = 100

type excludingTradeCounter interface {
	CountTradesForDayExcluding(shadow bool, start, end time.Time, operationID string) (int, error)
}

func (s *server) dayStateAtAccountExcluding(gate excludingTradeCounter, shadow bool, account broker.AccountState, window marketWindow, halted bool, haltReason, operationID string) (risk.DayState, error) {
	n, err := gate.CountTradesForDayExcluding(shadow, window.start, window.end, operationID)
	if err != nil {
		return risk.DayState{}, err
	}
	return risk.DayState{
		TradesToday: n, OpenRisk: 0, Equity: account.Equity,
		EquityKnown: account.EquityKnown, BuyingPower: account.BuyingPower,
		Halted: halted, HaltReason: haltReason,
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

func (s *server) proposalLifetime() time.Duration {
	if s.proposalTTL > 0 {
		return s.proposalTTL
	}
	lifetime, _ := proposalLifetime(s.limits.ProposalTTLSec)
	return lifetime
}

func startAttemptReconciler(s *server) error {
	if s.executionProvider() == nil {
		return nil
	}
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
	attempts, err := s.store.ListRecoverableAttempts(
		now.Add(-s.attemptStaleAfter()), now.Add(-s.attemptClaimTimeout()), reconcileBatchSize,
	)
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
	return s.reconcileWorkingOrders(ctx)
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
	row, err := s.store.GetOperation(attempt.OperationID)
	if err != nil {
		return err
	}
	if lifetime := s.proposalLifetime(); lifetime <= 0 || !time.Now().UTC().Before(row.TS.Add(lifetime)) {
		_, failErr := s.store.FailPendingAttempt(attempt.ID, "proposal expired before recovery")
		return failErr
	}
	var op risk.Operation
	if err := json.Unmarshal(row.Payload, &op); err != nil {
		_, failErr := s.store.FailPendingAttempt(attempt.ID, "persisted operation is invalid")
		return errors.Join(err, failErr)
	}

	switch op.Action {
	case "open":
		window, err := currentMarketWindow()
		if err != nil {
			return err
		}
		quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		quote, err := s.marketProvider().Quote(quoteCtx, operationSymbol(op))
		cancel()
		if err != nil {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "market data unavailable during recovery")
			return failErr
		}
		accountCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		account, err := s.accountProvider().Account(accountCtx)
		cancel()
		if err != nil {
			return err
		}
		candidate := op
		cap := op.ApprovedPriceCap
		candidate.Limit = &cap
		candidate.RejectReason = ""
		candidate = s.deriveOpenOperation(ctx, candidate, &quote)
		if candidate.Multiplier != op.Multiplier || candidate.Qty != op.Qty || candidate.ApprovedPriceCap > op.ApprovedPriceCap {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "recovery changed immutable execution facts")
			return failErr
		}
		if err := s.refreshGlobalHalt(); err != nil {
			return err
		}
		halted, haltReason := s.haltSnapshot()
		var verdict risk.Verdict
		err = s.store.WithLedgerLock(op.Shadow, window.day, func(gate store.OperationGate) error {
			day, err := s.dayStateAtAccountExcluding(gate, op.Shadow, account, window, halted, haltReason, attempt.OperationID)
			if err != nil {
				return err
			}
			verdict = risk.Classify(candidate, s.limits, day, &quote)
			return nil
		})
		if err != nil {
			return err
		}
		if verdict.Class != "B" {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "recovery gate failed: "+firstReason(verdict))
			return failErr
		}
		if _, err := s.store.UpdatePendingAttemptLimit(attempt.ID, candidate.WorkingPrice); err != nil {
			return err
		}
	case "close":
		reservation, err := s.store.GetCloseReservation(attempt.CloseReservationID)
		if err != nil || reservation.State != "held" {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "close reservation is not held")
			return errors.Join(err, failErr)
		}
		quoteCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
		quote, err := s.marketProvider().Quote(quoteCtx, reservation.Symbol)
		cancel()
		if err != nil || !quote.Usable(s.limits.QuoteMaxAgeSec, time.Now().UTC()) {
			_, failErr := s.store.FailPendingAttempt(attempt.ID, "market data unavailable during recovery")
			return failErr
		}
		var positionOK bool
		err = s.store.WithProposalLock(nil, false, nil, func(gate store.OperationGate) error {
			if err := gate.LockLedgerSymbol(reservation.Ledger, reservation.Symbol); err != nil {
				return err
			}
			positionCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
			positions, err := s.accountProvider().Positions(positionCtx)
			cancel()
			if err != nil {
				return err
			}
			quantity, err := closablePositionQuantity(reservation.Symbol, positions)
			if err != nil {
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
			if otherHeld > quantity || reservation.RemainingQty > quantity-otherHeld {
				return nil
			}
			normalized, err := normalizeClose(op, positions)
			if err == nil && normalized.Side == op.Side && normalized.Kind == op.Kind && normalized.Multiplier == op.Multiplier {
				positionOK = true
			}
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
		// A pending cancel has not reached the broker; its durable target id is
		// sufficient authority to claim and execute it now.
	default:
		_, failErr := s.store.FailPendingAttempt(attempt.ID, "unsupported recovered operation")
		return failErr
	}

	_, err = s.executePendingAttempt(ctx, attempt.ID)
	return err
}

func (s *server) reconcileUncertainAttempt(ctx context.Context, seen *store.ExecutionAttempt, now time.Time) error {
	claimed, err := s.store.ClaimRecoverableAttempt(
		seen.ID, s.workerID(), seen.State, seen.Attempt, now.Add(-s.attemptClaimTimeout()),
	)
	if err != nil || claimed == nil {
		return err
	}
	execution := s.executionProvider()
	if execution == nil {
		_, err := s.store.ResolveAttempt(claimed.ID, claimed.Attempt, store.AttemptResolution{
			State: "unknown", LastError: "execution capability unavailable",
		})
		return err
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.brokerCallTimeout())
	var result broker.OrderResult
	if claimed.Intent == "place" {
		result, err = execution.FindOrderByClientID(queryCtx, claimed.ClientOrderID)
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

func firstReason(verdict risk.Verdict) string {
	if len(verdict.Reasons) == 0 {
		return fmt.Sprintf("class %s", verdict.Class)
	}
	return verdict.Reasons[0]
}
