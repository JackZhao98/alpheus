// Package contracts: THE ARCHITECTURE IS THESE SCHEMAS, not the prompts.
// Every role's output is validated against its contract regardless of what
// the prompt says. Blank prompts + stub cognition can therefore exercise the
// whole pipeline before a single word is written.
//
// When wiring cognition/llm.go, derive a JSON schema from these structs (or
// hand-write one per contract) and request structured output; unmarshal here
// and call Validate.
package contracts

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

type ExitPlan struct {
	Stop         string `json:"stop"`         // price level or condition
	Invalidation string `json:"invalidation"` // thesis-kill condition
	TimeStop     string `json:"time_stop"`    // e.g. "close by 15:45 ET"
	Target       string `json:"target"`
}

type ProposedOperation struct {
	Action     string `json:"action"` // open | close | cancel | tighten_stop
	Kind       string `json:"kind"`   // option | equity
	Underlying string `json:"underlying"`
	Symbol     string `json:"symbol"`
	// Side is the order side for open. It is optional for close because the
	// kernel derives the only safe order side from the signed live position.
	Side              string       `json:"side"`
	Qty               json.Number  `json:"qty,omitempty"`
	Limit             *json.Number `json:"limit,omitempty"`
	MaxRiskUSD        *json.Number `json:"max_risk_usd,omitempty"`
	Short             bool         `json:"short"`
	Plan              *ExitPlan    `json:"plan,omitempty"`
	Thesis            string       `json:"thesis"` // journaled as hypothesis
	Setup             string       `json:"setup"`  // playbook id, for per-setup stats
	Shadow            bool         `json:"shadow"`
	BrokerOrderID     string       `json:"broker_order_id,omitempty"`
	ClosesOperationID string       `json:"closes_operation_id,omitempty"`
}

func (p ProposedOperation) Validate() error {
	switch p.Action {
	case "open", "close", "cancel", "tighten_stop":
	default:
		return fmt.Errorf("bad action %q", p.Action)
	}
	if p.Action == "open" && p.Side != "buy" && p.Side != "sell" {
		return fmt.Errorf("bad side %q", p.Side)
	}
	if p.Action == "close" && p.Side != "" && p.Side != "buy" && p.Side != "sell" {
		return fmt.Errorf("bad side %q", p.Side)
	}
	if p.Action == "open" && p.Kind != "equity" && p.Kind != "option" {
		return fmt.Errorf("bad kind %q", p.Kind)
	}
	if p.Action == "close" && p.Kind != "" && p.Kind != "equity" && p.Kind != "option" {
		return fmt.Errorf("bad kind %q", p.Kind)
	}
	if p.Action == "open" && strings.TrimSpace(p.Underlying) == "" {
		return fmt.Errorf("open without underlying")
	}
	if (p.Action == "open" || p.Action == "close") && strings.TrimSpace(p.Symbol) == "" && strings.TrimSpace(p.Underlying) == "" {
		return fmt.Errorf("%s without symbol or underlying", p.Action)
	}
	if p.Action == "open" || p.Action == "close" {
		qty, err := exactDecimal(p.Qty, true)
		if err != nil {
			return fmt.Errorf("%s qty: %w", p.Action, err)
		}
		if p.Kind == "option" && qty%1_000_000 != 0 {
			return fmt.Errorf("option qty must be a whole number of contracts")
		}
	}
	if p.Limit != nil {
		if _, err := exactDecimal(*p.Limit, true); err != nil {
			return fmt.Errorf("limit: %w", err)
		}
	}
	if p.MaxRiskUSD != nil {
		if _, err := exactDecimal(*p.MaxRiskUSD, false); err != nil {
			return fmt.Errorf("max_risk_usd: %w", err)
		}
	}
	if p.Action == "open" && p.Plan == nil {
		return fmt.Errorf("open without exit plan")
	}
	if p.Action == "cancel" && strings.TrimSpace(p.BrokerOrderID) == "" {
		return fmt.Errorf("cancel without broker_order_id")
	}
	if p.Action == "tighten_stop" && (p.Plan == nil || strings.TrimSpace(p.Plan.Stop) == "") {
		return fmt.Errorf("tighten_stop without stop")
	}
	if p.Action == "tighten_stop" && strings.TrimSpace(p.Symbol) == "" && strings.TrimSpace(p.Underlying) == "" {
		return fmt.Errorf("tighten_stop without symbol or underlying")
	}
	return nil
}

// exactDecimal validates the same public decimal grammar as the kernel: no
// exponent, at most six fractional digits, and no floating-point conversion.
func exactDecimal(number json.Number, positive bool) (int64, error) {
	value := string(number)
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "eE") {
		return 0, fmt.Errorf("must be an exact decimal without exponent")
	}
	negative := false
	if value[0] == '-' {
		negative = true
		value = value[1:]
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" ||
		(len(parts) == 2 && (parts[1] == "" || len(parts[1]) > 6)) {
		return 0, fmt.Errorf("must have at most 6 fractional digits")
	}
	for _, part := range parts {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return 0, fmt.Errorf("contains a non-decimal character")
			}
		}
	}
	whole := new(big.Int)
	if _, ok := whole.SetString(parts[0], 10); !ok {
		return 0, fmt.Errorf("invalid decimal")
	}
	scaled := new(big.Int).Mul(whole, big.NewInt(1_000_000))
	if len(parts) == 2 {
		fraction := new(big.Int)
		text := parts[1] + strings.Repeat("0", 6-len(parts[1]))
		if _, ok := fraction.SetString(text, 10); !ok {
			return 0, fmt.Errorf("invalid decimal")
		}
		scaled.Add(scaled, fraction)
	}
	if negative {
		scaled.Neg(scaled)
	}
	if !scaled.IsInt64() {
		return 0, fmt.Errorf("is out of range")
	}
	result := scaled.Int64()
	if positive && result <= 0 {
		return 0, fmt.Errorf("must be greater than zero")
	}
	if !positive && result < 0 {
		return 0, fmt.Errorf("must be non-negative")
	}
	return result, nil
}

// Output is what every cognition run must return.
type Output interface{ Validate() error }

type QueryIntent struct {
	Route                string   `json:"route"` // SCOUT | TEAM | REFUSE
	Objective            string   `json:"objective"`
	RequiredCapabilities []string `json:"required_capabilities"`
	MissingInputs        []string `json:"missing_inputs"`
}

func (q QueryIntent) Validate() error {
	if q.Route != "SCOUT" && q.Route != "TEAM" && q.Route != "REFUSE" {
		return fmt.Errorf("bad query route %q", q.Route)
	}
	if strings.TrimSpace(q.Objective) == "" || len(q.Objective) > 1000 {
		return fmt.Errorf("query objective is required and bounded")
	}
	return nil
}

type DeskDecision struct {
	Action          string              `json:"action"` // PROPOSE | WAIT | PASS
	Reasoning       string              `json:"reasoning"`
	Proposals       []ProposedOperation `json:"proposals"`
	WatchTriggers   []string            `json:"watch_triggers"`
	BlackboardPatch map[string]any      `json:"blackboard_patch"`
}

func (d DeskDecision) Validate() error {
	if d.Action != "PROPOSE" && d.Action != "WAIT" && d.Action != "PASS" {
		return fmt.Errorf("bad action %q", d.Action)
	}
	for i, p := range d.Proposals {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("proposal %d: %w", i, err)
		}
	}
	return nil
}

type OpportunityBrief struct {
	Action          string           `json:"action"`     // DISPATCH | WATCH | PASS
	Candidates      []map[string]any `json:"candidates"` // symbol, direction, catalyst, trigger, invalidation, liquidity
	StructuralNotes []string         `json:"structural_notes"`
}

func (o OpportunityBrief) Validate() error {
	if o.Action != "DISPATCH" && o.Action != "WATCH" && o.Action != "PASS" {
		return fmt.Errorf("bad action %q", o.Action)
	}
	return nil
}

// ExitAction is position_manager output — Class A ops, pre-authorized.
type ExitAction struct {
	Operations []ProposedOperation `json:"operations"`
	Blotter    string              `json:"blotter"`
}

func (e ExitAction) Validate() error {
	for i, op := range e.Operations {
		if err := op.Validate(); err != nil {
			return fmt.Errorf("operation %d: %w", i, err)
		}
	}
	return nil
}

type JournalReview struct {
	Outcomes             []map[string]any `json:"outcomes"` // pnl, slippage, rule_compliance, error_tag
	Lessons              []map[string]any `json:"lessons"`  // text, confidence, applicable_when, expires_at
	ParameterSuggestions []string         `json:"parameter_suggestions"`
}

func (JournalReview) Validate() error { return nil }
