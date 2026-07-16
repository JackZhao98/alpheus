package risk

import (
	"testing"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
)

func lims() config.Limits {
	var l config.Limits
	l.HardLimits.MaxRiskPerTradePct = 35
	l.HardLimits.MaxTotalOpenRiskPct = 80
	l.HardLimits.MaxNewTradesPerDay = 6
	l.InstrumentRules.MinOpenInterest = 300
	l.InstrumentRules.MaxRelativeSpread = 0.15
	l.PlanRequirements = []string{"stop", "invalidation", "time_stop", "target"}
	return l
}

func TestPaths(t *testing.T) {
	day := DayState{Equity: 300}
	q := &broker.Quote{Symbol: "SPY", Bid: 623.10, Ask: 623.14, OpenInterest: 45000}
	plan := map[string]string{"stop": "-30%", "invalidation": "x", "time_stop": "15:45", "target": "+50%"}

	// Class A only after the kernel verifies that close reduces a position.
	if v := Classify(Operation{Action: "close", Side: "sell", VerifiedReduction: true}, lims(), day, nil); v.Class != "A" {
		t.Fatalf("close => %s, want A", v.Class)
	}
	if v := Classify(Operation{Action: "close", Side: "sell"}, lims(), day, nil); v.Class != "REJECT" {
		t.Fatalf("unverified close => %s, want REJECT", v.Class)
	}
	// Class B: compliant open, 35 <= 105 (35% of 300)
	if v := Classify(Operation{Action: "open", Kind: "option", Underlying: "SPY", Symbol: "SPY", Side: "buy", Qty: 1, MaxRiskUSD: 35, Plan: plan}, lims(), day, q); v.Class != "B" {
		t.Fatalf("compliant open => %s (%v), want B", v.Class, v.Reasons)
	}
	// Class C: over budget, 200 > 105
	if v := Classify(Operation{Action: "open", Kind: "option", Underlying: "SPY", Symbol: "SPY", Side: "buy", Qty: 1, MaxRiskUSD: 200, Plan: plan}, lims(), day, q); v.Class != "C" {
		t.Fatalf("over-budget => %s, want C", v.Class)
	}
	// REJECT: naked short
	if v := Classify(Operation{Action: "open", Short: true, Side: "sell", MaxRiskUSD: 10, Plan: plan}, lims(), day, q); v.Class != "REJECT" {
		t.Fatalf("naked short => %s, want REJECT", v.Class)
	}
	if v := Classify(Operation{Action: "open", Kind: "option", Side: "sell", MaxRiskUSD: 10, Plan: plan}, lims(), day, q); v.Class != "REJECT" {
		t.Fatalf("inferred naked short => %s, want REJECT", v.Class)
	}
	// REJECT: halted
	if v := Classify(Operation{Action: "open", Side: "buy", MaxRiskUSD: 10, Plan: plan}, lims(), DayState{Equity: 300, Halted: true}, q); v.Class != "REJECT" {
		t.Fatalf("halted => %s, want REJECT", v.Class)
	}
}

func TestWhitespacePlanDoesNotPassChecklist(t *testing.T) {
	day := DayState{Equity: 300}
	q := &broker.Quote{Symbol: "SPY", Bid: 4.20, Ask: 4.40, OpenInterest: 45000}
	plan := map[string]string{"stop": " ", "invalidation": "x", "time_stop": "15:45", "target": "+50%"}
	v := Classify(Operation{Action: "open", Kind: "option", Underlying: "SPY", Symbol: "SPY", Side: "buy", Qty: 1, MaxRiskUSD: 35, Plan: plan}, lims(), day, q)
	if v.Class != "C" || v.Checks["plan_complete"] {
		t.Fatalf("whitespace plan => class=%s checks=%v, want C with plan_complete=false", v.Class, v.Checks)
	}
}
