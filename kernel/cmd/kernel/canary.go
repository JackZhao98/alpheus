package main

import (
	"errors"
	"fmt"
	"time"

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
	"alpheus/kernel/internal/units"
)

const (
	canaryCapReason       = "live_canary_daily_risk_cap"
	canaryLegacyReason    = "live_canary_legacy_unknown"
	canaryUnboundReason   = "live_canary_unbound_grant"
	canaryFirstSizeReason = "live_canary_first_position_not_minimum_size"
)

type liveCanaryAuthorityLoader interface {
	LoadLiveCanaryAuthority() (*store.LiveCanaryRevision, error)
}

type liveCanaryAuthorityView struct {
	Status    string                    `json:"status"`
	Authority *store.LiveCanaryRevision `json:"authority,omitempty"`
}

func requireLiveCanaryAuthority(mode string, loader liveCanaryAuthorityLoader) (*store.LiveCanaryRevision, error) {
	if mode != config.ModeLive {
		return nil, nil
	}
	revision, err := loader.LoadLiveCanaryAuthority()
	if err != nil {
		return nil, err
	}
	if revision == nil || revision.ID <= 0 || revision.Generation != revision.ID {
		return nil, store.ErrLiveCanaryAuthorityInvalid
	}
	return revision, nil
}

func (s *server) liveCanaryAuthorityView() (liveCanaryAuthorityView, error) {
	revision, err := s.store.LoadLiveCanaryAuthority()
	if s.tradingMode() != config.ModeLive {
		switch {
		case errors.Is(err, store.ErrLiveCanaryAuthorityMissing):
			return liveCanaryAuthorityView{Status: "missing"}, nil
		case errors.Is(err, store.ErrLiveCanaryAuthorityInvalid):
			return liveCanaryAuthorityView{Status: "invalid"}, nil
		}
	}
	if err != nil {
		return liveCanaryAuthorityView{}, err
	}
	return liveCanaryAuthorityView{Status: "active", Authority: revision}, nil
}

func (s *server) liveCanaryRefusal(gate store.OperationGate, operationID string, marketDay time.Time, proposedRisk units.Micros, quantity, quantityIncrement units.Qty) (string, store.TradeGrantUsage, *store.LiveCanaryRevision, error) {
	if s.tradingMode() != config.ModeLive {
		return "", store.TradeGrantUsage{}, nil, nil
	}
	authority, err := gate.LiveCanaryAuthority(marketDay)
	if err != nil {
		return "", store.TradeGrantUsage{}, nil, err
	}
	if authority == nil || authority.DailyAuthorizedRiskCapUSD <= 0 {
		return "", store.TradeGrantUsage{}, nil, store.ErrLiveCanaryAuthorityInvalid
	}
	usage, err := gate.TradeGrantUsage("live", marketDay, operationID)
	if err != nil {
		return "", store.TradeGrantUsage{}, nil, err
	}
	if usage.HasLegacyUnknown {
		return canaryLegacyReason, usage, authority, nil
	}
	if usage.HasUnboundCanary {
		return canaryUnboundReason, usage, authority, nil
	}
	if usage.GrantCount == 0 && (quantityIncrement <= 0 || quantity != quantityIncrement) {
		return canaryFirstSizeReason, usage, authority, nil
	}
	cap := authority.DailyAuthorizedRiskCapUSD
	// Compare by subtraction so a corrupt/oversized stored aggregate cannot
	// wrap an int64 addition into a permissive result.
	if proposedRisk <= 0 || proposedRisk > cap || usage.AuthorizedRisk > cap-proposedRisk {
		return canaryCapReason, usage, authority, nil
	}
	return "", usage, authority, nil
}

func (s *server) insertCanaryRefusalEvent(gate store.OperationGate, operationID, reason string, marketDay time.Time, proposedRisk units.Micros, quantity, quantityIncrement units.Qty, usage store.TradeGrantUsage, authority *store.LiveCanaryRevision) error {
	if authority == nil {
		return fmt.Errorf("%w: refusal has no bound revision", store.ErrLiveCanaryAuthorityInvalid)
	}
	return gate.InsertEvent("live_canary_refused", map[string]any{
		"operation_id":  operationID,
		"reason":        reason,
		"market_day":    marketDay,
		"used_risk":     usage.AuthorizedRisk,
		"proposed_risk": proposedRisk,
		"risk_cap":      authority.DailyAuthorizedRiskCapUSD,
		"grant_count":   usage.GrantCount,
		"quantity":      quantity,
		"qty_increment": quantityIncrement,
		"revision_id":   authority.ID,
		"generation":    authority.Generation,
	})
}

func liveCanaryEventBinding(authority *store.LiveCanaryRevision) map[string]any {
	if authority == nil {
		return nil
	}
	return map[string]any{
		"revision_id": authority.ID,
		"generation":  authority.Generation,
		"risk_cap":    authority.DailyAuthorizedRiskCapUSD,
	}
}
