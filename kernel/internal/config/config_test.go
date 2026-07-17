package config

import (
	"encoding/json"
	"testing"

	"alpheus/kernel/internal/units"

	"gopkg.in/yaml.v3"
)

func TestLimitsDecodeFixedPointExactly(t *testing.T) {
	var limits Limits
	doc := []byte(`
hard_limits:
  max_risk_per_trade_pct: 35
  max_total_open_risk_pct: 80
  max_daily_loss_pct: 40
instrument_rules:
  max_relative_spread: 0.15
execution_policy:
  fee_per_contract: 0.01
risk_declaration_tolerance: 0.01
`)
	if err := yaml.Unmarshal(doc, &limits); err != nil {
		t.Fatal(err)
	}
	if limits.HardLimits.MaxRiskPerTradePct != units.MustPercent("35") {
		t.Fatalf("per-trade percent=%s", limits.HardLimits.MaxRiskPerTradePct)
	}
	if limits.InstrumentRules.MaxRelativeSpread != units.MustRatio("0.15") {
		t.Fatalf("spread=%s", limits.InstrumentRules.MaxRelativeSpread)
	}
	if limits.ExecutionPolicy.FeePerContract != units.MustMicros("0.01") ||
		limits.RiskDeclarationTolerance != units.MustMicros("0.01") {
		t.Fatalf("money keys decoded incorrectly: %+v", limits)
	}

	encoded, err := json.Marshal(limits)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["risk_declaration_tolerance"] != 0.01 {
		t.Fatalf("wire limit=%v, want 0.01", raw["risk_declaration_tolerance"])
	}
}
