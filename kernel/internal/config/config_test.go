package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alpheus/kernel/internal/units"

	"gopkg.in/yaml.v3"
)

func TestLiveAccountBindingFileMustBePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live-account-id")
	if err := os.WriteFile(path, []byte("full-account-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVE_ACCOUNT_ID", "")
	t.Setenv("LIVE_ACCOUNT_ID_FILE", path)
	if _, err := loadLiveAccountID(); err == nil {
		t.Fatal("accepted group/world-readable account binding")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := loadLiveAccountID()
	if err != nil || value != "full-account-number" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	t.Setenv("LIVE_ACCOUNT_ID", "another-account")
	if _, err := loadLiveAccountID(); err == nil {
		t.Fatal("accepted ambiguous direct and file account bindings")
	}
}

func TestModeConfigCannotMarshalSecretsOrAccountBinding(t *testing.T) {
	raw, err := json.Marshal(ModeConfig{
		TradingMode: "read_only", RuntimeToken: "runtime-secret", AdminToken: "admin-secret",
		KernelToken: "kernel-secret", LiveAccountID: "full-account-number",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"runtime-secret", "admin-secret", "kernel-secret", "full-account-number"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("config JSON leaked %q: %s", secret, raw)
		}
	}
}

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
pnl_reconciliation_tolerance_usd: 0.01
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
		limits.RiskDeclarationTolerance != units.MustMicros("0.01") ||
		limits.PnLReconciliationTolerance != units.MustMicros("0.01") {
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
