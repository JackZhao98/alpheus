// Package risk is the gate. Deterministic classification of every proposal.
//
//	Class A: reduces risk           -> execute immediately, zero review
//	Class B: passes full checklist  -> auto-approved by code, no LLM involved
//	Class C: exception              -> pending_review (LLM reviewer or human)
//	REJECT : violates an absolute   -> dead on arrival
//
// Aggressive profile: sizing caps are PERCENTAGES of live equity, so the
// agent's absolute headroom grows with its own track record.
package risk

import (
	"fmt"
	"strings"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
)

type Operation struct {
	Proposer          string            `json:"proposer"`
	Action            string            `json:"action"` // open | close | cancel | tighten_stop
	Kind              string            `json:"kind"`   // option | equity
	Underlying        string            `json:"underlying"`
	Symbol            string            `json:"symbol"`
	Side              string            `json:"side"`
	Qty               float64           `json:"qty"`
	Limit             *float64          `json:"limit"`
	MaxRiskUSD        float64           `json:"max_risk_usd"`
	Short             bool              `json:"short"`
	Plan              map[string]string `json:"plan"` // stop / invalidation / time_stop / target
	Thesis            string            `json:"thesis"`
	Setup             string            `json:"setup"`
	Shadow            bool              `json:"shadow"`
	BrokerOrderID     string            `json:"broker_order_id,omitempty"`
	ClosesOperationID string            `json:"closes_operation_id,omitempty"`
	// VerifiedReduction is set by the kernel only after it checks the live
	// position. It is never accepted from JSON.
	VerifiedReduction bool `json:"-"`
}

type DayState struct {
	TradesToday int     `json:"trades_today"`
	OpenRisk    float64 `json:"open_risk"`
	Equity      float64 `json:"equity"`
	Halted      bool    `json:"halted"`
	HaltReason  string  `json:"halt_reason,omitempty"`
}

type Verdict struct {
	Class   string          `json:"class"` // A | B | C | REJECT
	Checks  map[string]bool `json:"checks,omitempty"`
	Reasons []string        `json:"reasons"`
}

var reducing = map[string]bool{"cancel": true, "tighten_stop": true}

// Classify decides the fate of a proposal. quote may be nil for non-open actions.
func Classify(op Operation, lim config.Limits, day DayState, quote *broker.Quote) Verdict {
	if op.Action == "close" {
		if op.Shadow || op.VerifiedReduction {
			return Verdict{Class: "A", Reasons: []string{"verified risk reduction"}}
		}
		return Verdict{Class: "REJECT", Reasons: []string{"close is not verified against a position"}}
	}
	if reducing[op.Action] {
		return Verdict{Class: "A", Reasons: []string{"risk-reducing"}}
	}
	if op.Action != "open" {
		return Verdict{Class: "REJECT", Reasons: []string{fmt.Sprintf("unknown action %q", op.Action)}}
	}

	// --- absolutes: any failure here is REJECT, not review ---
	if day.Halted {
		return Verdict{Class: "REJECT", Reasons: []string{"breaker halted: " + day.HaltReason}}
	}
	// In the current single-leg model, an option sell on an open action is a
	// short option. Do not trust callers to declare Short correctly.
	if (op.Short || (op.Kind == "option" && op.Side == "sell")) && !lim.InstrumentRules.AllowNakedShortOptions {
		return Verdict{Class: "REJECT", Reasons: []string{"naked short options forbidden"}}
	}

	// --- checklist: failures downgrade to Class C (review), not reject ---
	checks := map[string]bool{}

	wl := lim.Whitelist.Underlyings
	checks["whitelist"] = len(wl) == 0 || contains(wl, op.Underlying)

	perTradeCap := day.Equity * lim.HardLimits.MaxRiskPerTradePct / 100
	totalCap := day.Equity * lim.HardLimits.MaxTotalOpenRiskPct / 100
	checks["per_trade_budget"] = op.MaxRiskUSD > 0 && op.MaxRiskUSD <= perTradeCap
	checks["total_open_risk"] = day.OpenRisk+op.MaxRiskUSD <= totalCap
	checks["daily_trade_count"] = day.TradesToday < lim.HardLimits.MaxNewTradesPerDay

	planOK := true
	for _, k := range lim.PlanRequirements {
		if strings.TrimSpace(op.Plan[k]) == "" {
			planOK = false
		}
	}
	checks["plan_complete"] = planOK

	if quote != nil {
		checks["liquidity_spread"] = quote.Sane() && quote.RelativeSpread() <= lim.InstrumentRules.MaxRelativeSpread
		checks["liquidity_oi"] = op.Kind != "option" || quote.OpenInterest >= lim.InstrumentRules.MinOpenInterest
	} else {
		checks["liquidity_spread"], checks["liquidity_oi"] = false, false
	}

	var failed []string
	for k, ok := range checks {
		if !ok {
			failed = append(failed, k)
		}
	}
	if len(failed) == 0 {
		return Verdict{Class: "B", Checks: checks, Reasons: []string{"checklist pass"}}
	}
	return Verdict{Class: "C", Checks: checks, Reasons: []string{"needs review: " + strings.Join(failed, ", ")}}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
