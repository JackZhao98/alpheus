package main

import (
	"fmt"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

const (
	canaryCapReason    = "live_canary_daily_risk_cap"
	canaryLegacyReason = "live_canary_legacy_unknown"
)

func (s *server) liveCanaryRefusal(gate store.OperationGate, operationID string, marketDay time.Time, proposedRisk units.Micros) (string, store.TradeGrantUsage, error) {
	if s.tradingMode() != config.ModeLive {
		return "", store.TradeGrantUsage{}, nil
	}
	cap := s.limits.LiveCanary.DailyAuthorizedRiskCapUSD
	if cap <= 0 || s.limits.LiveCanary.CleanDaysBeforeRaise <= 0 {
		return "", store.TradeGrantUsage{}, fmt.Errorf("live canary is not configured")
	}
	usage, err := gate.TradeGrantUsage("live", marketDay, operationID)
	if err != nil {
		return "", store.TradeGrantUsage{}, err
	}
	if usage.HasLegacyUnknown {
		return canaryLegacyReason, usage, nil
	}
	// Compare by subtraction so a corrupt/oversized stored aggregate cannot
	// wrap an int64 addition into a permissive result.
	if proposedRisk <= 0 || proposedRisk > cap || usage.AuthorizedRisk > cap-proposedRisk {
		return canaryCapReason, usage, nil
	}
	return "", usage, nil
}

func (s *server) insertCanaryRefusalEvent(gate store.OperationGate, operationID, reason string, marketDay time.Time, proposedRisk units.Micros, usage store.TradeGrantUsage) error {
	return gate.InsertEvent("live_canary_refused", map[string]any{
		"operation_id":  operationID,
		"reason":        reason,
		"market_day":    marketDay,
		"used_risk":     usage.AuthorizedRisk,
		"proposed_risk": proposedRisk,
		"risk_cap":      s.limits.LiveCanary.DailyAuthorizedRiskCapUSD,
	})
}
