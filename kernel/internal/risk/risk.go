// Package risk is the deterministic gate for every operation.
package risk

import (
	"fmt"
	"strings"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/units"
)

type Operation struct {
	Proposer          string            `json:"proposer"`
	Action            string            `json:"action"`
	Kind              string            `json:"kind,omitempty"`
	Underlying        string            `json:"underlying,omitempty"`
	Symbol            string            `json:"symbol,omitempty"`
	Side              string            `json:"side,omitempty"`
	OrderType         string            `json:"order_type,omitempty"`
	ExecutionStyle    string            `json:"execution_style,omitempty"`
	Qty               units.Qty         `json:"qty,omitempty"`
	Limit             *units.Micros     `json:"limit,omitempty"`
	StopPrice         *units.Micros     `json:"stop_price,omitempty"`
	MaxRiskUSD        *units.Micros     `json:"max_risk_usd,omitempty"`
	Short             bool              `json:"short,omitempty"`
	Plan              map[string]string `json:"plan,omitempty"`
	Thesis            string            `json:"thesis,omitempty"`
	Setup             string            `json:"setup,omitempty"`
	Shadow            bool              `json:"shadow"`
	BrokerOrderID     string            `json:"broker_order_id,omitempty"`
	PositionID        string            `json:"position_id,omitempty"`
	ClosesOperationID string            `json:"closes_operation_id,omitempty"`

	// Kernel-derived execution and risk facts. None of these fields exist on
	// the request DTO decoded by the HTTP handler.
	DerivedMaxRisk                units.Micros `json:"derived_max_risk,omitempty"`
	RequiredCash                  units.Micros `json:"required_cash,omitempty"`
	ApprovedPriceCap              units.Micros `json:"approved_price_cap,omitempty"`
	WorkingPrice                  units.Micros `json:"working_price,omitempty"`
	Multiplier                    int64        `json:"multiplier,omitempty"`
	QtyIncrement                  units.Qty    `json:"qty_increment,omitempty"`
	InstrumentID                  string       `json:"instrument_id,omitempty"`
	DecisionObservationID         string       `json:"decision_observation_id,omitempty"`
	DecisionObservationGeneration int64        `json:"decision_observation_generation,omitempty"`
	DecisionObservationDigest     string       `json:"decision_observation_digest,omitempty"`
	BrokerObjectOrigin            string       `json:"broker_object_origin,omitempty"`
	BrokerPositionID              string       `json:"broker_position_id,omitempty"`
	DecisionPositionQty           units.Qty    `json:"decision_position_qty,omitempty"`
	CancelTargetEffect            string       `json:"cancel_target_effect,omitempty"`
	CancelTargetFingerprint       string       `json:"cancel_target_fingerprint,omitempty"`
	TrackedCloseQty               units.Qty    `json:"tracked_close_qty,omitempty"`
	ExternalCloseQty              units.Qty    `json:"external_close_qty,omitempty"`

	VerifiedReduction bool   `json:"-"`
	RejectReason      string `json:"-"`
}

type DayState struct {
	TradesToday         int           `json:"trades_today"`
	OpenRisk            units.Micros  `json:"open_risk"`
	RealizedPnL         units.Micros  `json:"realized_pnl"`
	LocalRealizedPnL    units.Micros  `json:"local_realized_pnl"`
	ProviderRealizedPnL *units.Micros `json:"provider_realized_pnl,omitempty"`
	DailyLossLimit      units.Micros  `json:"daily_loss_limit"`
	ConsecutiveLossDays int           `json:"consecutive_loss_days"`
	Equity              units.Micros  `json:"equity"`
	EquityKnown         bool          `json:"equity_known"`
	BuyingPower         units.Micros  `json:"buying_power"`
	Halted              bool          `json:"halted"`
	HaltReason          string        `json:"halt_reason,omitempty"`
}

type Verdict struct {
	Class   string          `json:"class"`
	Checks  map[string]bool `json:"checks,omitempty"`
	Reasons []string        `json:"reasons"`
}

func reject(reason string) Verdict {
	return Verdict{Class: "REJECT", Reasons: []string{reason}}
}

func Classify(op Operation, limits config.Limits, day DayState, quote *broker.Quote) Verdict {
	return ClassifyAt(op, limits, day, quote, time.Now().UTC())
}

func ClassifyAt(op Operation, limits config.Limits, day DayState, quote *broker.Quote, now time.Time) Verdict {
	if op.Action == "close" {
		if op.Shadow || op.VerifiedReduction {
			return Verdict{Class: "A", Reasons: []string{"verified risk reduction"}}
		}
		return reject("close is not verified against a position")
	}
	if op.Action == "cancel" {
		if op.VerifiedReduction {
			return Verdict{Class: "A", Reasons: []string{"verified risk reduction"}}
		}
		return reject("cancel target is not verified risk-reducing")
	}
	if op.Action == "tighten_stop" {
		return Verdict{Class: "A", Reasons: []string{"risk-reducing"}}
	}
	if op.Action != "open" {
		return reject(fmt.Sprintf("unknown action %q", op.Action))
	}

	// Absolutes: a human cannot override a missing dependency, invented buying
	// power, a short leg the model cannot represent, or a lying declaration.
	if day.Halted {
		return reject("breaker halted: " + day.HaltReason)
	}
	if op.Side == "sell" {
		return reject("uncovered_short")
	}
	if op.RejectReason != "" {
		return reject(op.RejectReason)
	}
	if !day.EquityKnown {
		return reject("equity_unknown")
	}
	if day.Equity <= 0 {
		return reject("nonpositive_equity")
	}
	if quote == nil || !quote.Usable(limits.QuoteMaxAgeSec, now) {
		return reject("market_data_unavailable")
	}
	if op.DerivedMaxRisk <= 0 || op.RequiredCash <= 0 || op.Multiplier <= 0 {
		return reject("risk_not_computed")
	}
	if op.RequiredCash > day.BuyingPower {
		return reject("insufficient_buying_power")
	}
	if op.MaxRiskUSD != nil &&
		units.DifferenceExceeds(*op.MaxRiskUSD, op.DerivedMaxRisk, limits.RiskDeclarationTolerance) {
		return reject("risk_declaration_mismatch")
	}

	// Percentage caps round down, against the account: a fractional micro-dollar
	// of capacity is never granted.
	perTradeCap, err := units.PercentFloor(day.Equity, limits.HardLimits.MaxRiskPerTradePct)
	if err != nil {
		return reject("risk_overflow")
	}
	totalCap, err := units.PercentFloor(day.Equity, limits.HardLimits.MaxTotalOpenRiskPct)
	if err != nil {
		return reject("risk_overflow")
	}

	checks := map[string]bool{}
	checks["whitelist"] = len(limits.Whitelist.Underlyings) == 0 ||
		contains(limits.Whitelist.Underlyings, op.Underlying)
	checks["per_trade_budget"] = op.DerivedMaxRisk <= perTradeCap
	checks["total_open_risk"] = units.SumLessOrEqual(day.OpenRisk, op.DerivedMaxRisk, totalCap)
	checks["daily_trade_count"] = day.TradesToday < limits.HardLimits.MaxNewTradesPerDay

	planOK := true
	for _, key := range limits.PlanRequirements {
		if strings.TrimSpace(op.Plan[key]) == "" {
			planOK = false
		}
	}
	checks["plan_complete"] = planOK
	checks["liquidity_spread"] = units.SpreadWithin(
		quote.Bid, quote.Ask, limits.InstrumentRules.MaxRelativeSpread,
	)
	checks["liquidity_oi"] = op.Kind != "option" ||
		quote.OpenInterest >= limits.InstrumentRules.MinOpenInterest

	order := []string{
		"whitelist", "per_trade_budget", "total_open_risk",
		"daily_trade_count", "plan_complete", "liquidity_spread", "liquidity_oi",
	}
	failed := make([]string, 0, len(order))
	for _, key := range order {
		if !checks[key] {
			failed = append(failed, key)
		}
	}
	if len(failed) == 0 {
		return Verdict{Class: "B", Checks: checks, Reasons: []string{"checklist pass"}}
	}
	return Verdict{
		Class: "C", Checks: checks,
		Reasons: []string{"needs review: " + strings.Join(failed, ", ")},
	}
}

func contains(list []string, value string) bool {
	for _, candidate := range list {
		if candidate == value {
			return true
		}
	}
	return false
}
