package risk

import (
	"os"
	"strings"
	"testing"
	"time"

	"alpheus/kernel/internal/broker"
	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/units"
)

func limitsForTest() config.Limits {
	var limits config.Limits
	limits.HardLimits.MaxRiskPerTradePct = units.MustPercent("35")
	limits.HardLimits.MaxTotalOpenRiskPct = units.MustPercent("80")
	limits.HardLimits.MaxNewTradesPerDay = 6
	limits.InstrumentRules.MinOpenInterest = 300
	limits.InstrumentRules.MaxRelativeSpread = units.MustRatio("0.15")
	limits.RiskDeclarationTolerance = units.MustMicros("0.01")
	limits.PlanRequirements = []string{"stop", "invalidation", "time_stop", "target"}
	return limits
}

func testDay() DayState {
	return DayState{
		Equity: units.MustMicros("300"), EquityKnown: true,
		BuyingPower: units.MustMicros("300"),
	}
}

func testQuote() *broker.Quote {
	return &broker.Quote{
		Symbol: "SPY", Bid: units.MustMicros("4.20"),
		Ask: units.MustMicros("4.40"), OpenInterest: 45_000,
	}
}

func testPlan() map[string]string {
	return map[string]string{
		"stop": "-30%", "invalidation": "x", "time_stop": "15:45", "target": "+50%",
	}
}

func testOpen(riskValue string) Operation {
	value := units.MustMicros(riskValue)
	return Operation{
		Action: "open", Kind: "option", Underlying: "SPY", Symbol: "SPY",
		Side: "buy", Qty: units.MustQty("1"), MaxRiskUSD: &value,
		Plan: testPlan(), DerivedMaxRisk: value, RequiredCash: value,
		ApprovedPriceCap: units.MustMicros("4.40"), WorkingPrice: units.MustMicros("4.30"),
		Multiplier: 100,
	}
}

func TestClassificationPaths(t *testing.T) {
	if verdict := Classify(Operation{Action: "close", Shadow: true}, limitsForTest(), testDay(), nil); verdict.Class != "A" {
		t.Fatalf("shadow close=%s, want A", verdict.Class)
	}
	if verdict := Classify(Operation{Action: "close", VerifiedReduction: true}, limitsForTest(), testDay(), nil); verdict.Class != "A" {
		t.Fatalf("verified close=%s, want A", verdict.Class)
	}
	if verdict := Classify(Operation{Action: "close"}, limitsForTest(), testDay(), nil); verdict.Class != "REJECT" {
		t.Fatalf("unverified close=%s, want REJECT", verdict.Class)
	}
	if verdict := Classify(testOpen("35"), limitsForTest(), testDay(), testQuote()); verdict.Class != "B" {
		t.Fatalf("compliant open=%s reasons=%v", verdict.Class, verdict.Reasons)
	}
	if verdict := Classify(testOpen("200"), limitsForTest(), testDay(), testQuote()); verdict.Class != "C" {
		t.Fatalf("over budget=%s, want C", verdict.Class)
	}

	sell := testOpen("35")
	sell.Kind, sell.Side, sell.Short = "equity", "sell", false
	if verdict := Classify(sell, limitsForTest(), testDay(), testQuote()); verdict.Class != "REJECT" ||
		verdict.Reasons[0] != "uncovered_short" {
		t.Fatalf("sell open=%+v", verdict)
	}
	sell.RejectReason = "market_data_unavailable"
	if verdict := Classify(sell, limitsForTest(), testDay(), nil); verdict.Class != "REJECT" ||
		verdict.Reasons[0] != "uncovered_short" {
		t.Fatalf("sell open dependency priority=%+v", verdict)
	}
	if verdict := Classify(Operation{Action: "cancel", VerifiedReduction: true}, limitsForTest(), testDay(), nil); verdict.Class != "A" {
		t.Fatalf("verified cancel=%+v, want A", verdict)
	}
	if verdict := Classify(Operation{Action: "cancel"}, limitsForTest(), testDay(), nil); verdict.Class != "REJECT" {
		t.Fatalf("unverified cancel=%+v, want REJECT", verdict)
	}
	if verdict := Classify(Operation{Action: "tighten_stop"}, limitsForTest(), testDay(), nil); verdict.Class != "A" {
		t.Fatalf("tighten_stop=%+v, want A", verdict)
	}
	if verdict := Classify(Operation{Action: "OPEN"}, limitsForTest(), testDay(), nil); verdict.Class != "REJECT" ||
		!strings.Contains(verdict.Reasons[0], "unknown action") {
		t.Fatalf("unknown action verdict=%+v", verdict)
	}
}

func TestAbsoluteRiskFactsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Operation, *DayState)
		reason string
	}{
		{"unknown equity", func(_ *Operation, day *DayState) { day.EquityKnown = false }, "equity_unknown"},
		{"zero equity", func(_ *Operation, day *DayState) { day.Equity = 0 }, "nonpositive_equity"},
		{"buying power", func(_ *Operation, day *DayState) { day.BuyingPower = units.MustMicros("34.999999") }, "insufficient_buying_power"},
		{"derived failure", func(op *Operation, _ *DayState) { op.RejectReason = "unsupported_contract" }, "unsupported_contract"},
		{"halted", func(_ *Operation, day *DayState) { day.Halted, day.HaltReason = true, "operator" }, "breaker halted: operator"},
		{"zero derived risk", func(op *Operation, _ *DayState) { op.DerivedMaxRisk = 0 }, "risk_not_computed"},
		{"zero required cash", func(op *Operation, _ *DayState) { op.RequiredCash = 0 }, "risk_not_computed"},
		{"zero multiplier", func(op *Operation, _ *DayState) { op.Multiplier = 0 }, "risk_not_computed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			op, day := testOpen("35"), testDay()
			tc.change(&op, &day)
			verdict := Classify(op, limitsForTest(), day, testQuote())
			if verdict.Class != "REJECT" || verdict.Reasons[0] != tc.reason {
				t.Fatalf("verdict=%+v, want %s", verdict, tc.reason)
			}
		})
	}
}

func TestChecklistFailureMatrix(t *testing.T) {
	limits := limitsForTest()
	limits.Whitelist.Underlyings = []string{"QQQ", "SPY"}
	if verdict := Classify(testOpen("35"), limits, testDay(), testQuote()); verdict.Class != "B" || !verdict.Checks["whitelist"] {
		t.Fatalf("allowlisted verdict=%+v", verdict)
	}

	tests := []struct {
		name   string
		change func(*Operation, *DayState, *broker.Quote, *config.Limits)
		check  string
	}{
		{"whitelist", func(op *Operation, _ *DayState, _ *broker.Quote, _ *config.Limits) { op.Underlying = "IWM" }, "whitelist"},
		{"open risk", func(_ *Operation, day *DayState, _ *broker.Quote, _ *config.Limits) {
			day.OpenRisk = units.MustMicros("230")
		}, "total_open_risk"},
		{"daily count", func(_ *Operation, day *DayState, _ *broker.Quote, limits *config.Limits) {
			day.TradesToday = limits.HardLimits.MaxNewTradesPerDay
		}, "daily_trade_count"},
		{"open interest", func(_ *Operation, _ *DayState, quote *broker.Quote, _ *config.Limits) { quote.OpenInterest = 299 }, "liquidity_oi"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			op, day, quote, candidateLimits := testOpen("35"), testDay(), testQuote(), limits
			tc.change(&op, &day, quote, &candidateLimits)
			verdict := Classify(op, candidateLimits, day, quote)
			if verdict.Class != "C" || verdict.Checks[tc.check] {
				t.Fatalf("verdict=%+v, want C with %s=false", verdict, tc.check)
			}
		})
	}
}

func TestRiskDeclarationMismatchUsesTolerance(t *testing.T) {
	op := testOpen("300")
	declared := units.MustMicros("10")
	op.MaxRiskUSD = &declared
	verdict := Classify(op, limitsForTest(), testDay(), testQuote())
	if verdict.Class != "REJECT" || verdict.Reasons[0] != "risk_declaration_mismatch" {
		t.Fatalf("verdict=%+v", verdict)
	}

	zero := units.Micros(0)
	op.MaxRiskUSD = &zero
	verdict = Classify(op, limitsForTest(), testDay(), testQuote())
	if verdict.Class != "REJECT" || verdict.Reasons[0] != "risk_declaration_mismatch" {
		t.Fatalf("explicit zero verdict=%+v", verdict)
	}
}

func TestExactBudgetBoundary(t *testing.T) {
	day := testDay()
	atCap := testOpen("105")
	atCap.MaxRiskUSD = nil
	if verdict := Classify(atCap, limitsForTest(), day, testQuote()); !verdict.Checks["per_trade_budget"] {
		t.Fatalf("at cap=%+v", verdict)
	}
	above := testOpen("105.000001")
	above.MaxRiskUSD = nil
	if verdict := Classify(above, limitsForTest(), day, testQuote()); verdict.Checks["per_trade_budget"] {
		t.Fatalf("above cap=%+v", verdict)
	}
}

func TestWhitespacePlanAndWideSpreadDowngrade(t *testing.T) {
	op := testOpen("35")
	op.Plan["stop"] = " "
	quote := testQuote()
	quote.Ask = units.MustMicros("6")
	verdict := Classify(op, limitsForTest(), testDay(), quote)
	if verdict.Class != "C" || verdict.Checks["plan_complete"] ||
		verdict.Checks["liquidity_spread"] {
		t.Fatalf("verdict=%+v", verdict)
	}
}

func TestMalformedOrStaleQuoteRejects(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	limits := limitsForTest()
	limits.QuoteMaxAgeSec = 5
	if verdict := ClassifyAt(testOpen("35"), limits, testDay(), nil, now); verdict.Class != "REJECT" ||
		verdict.Reasons[0] != "market_data_unavailable" {
		t.Fatalf("nil quote verdict=%+v", verdict)
	}
	tests := []broker.Quote{
		{Bid: units.MustMicros("100"), Ask: units.MustMicros("50")},
		{Bid: units.MustMicros("100"), Ask: units.MustMicros("100")},
		{Bid: 0, Ask: units.MustMicros("100")},
		{Bid: units.MustMicros("100"), Ask: units.MustMicros("101"), AsOf: now.Add(-6 * time.Second)},
	}
	for _, quote := range tests {
		verdict := ClassifyAt(testOpen("35"), limits, testDay(), &quote, now)
		if verdict.Class != "REJECT" || verdict.Reasons[0] != "market_data_unavailable" {
			t.Fatalf("quote=%+v verdict=%+v", quote, verdict)
		}
	}
}

func TestRiskPackageContainsNoFloatingMoneyType(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "float"+"64") {
			t.Fatalf("%s contains a floating money type", entry.Name())
		}
	}
}
