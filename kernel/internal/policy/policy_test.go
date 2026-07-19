package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"alpheus/kernel/internal/units"
)

const validBootstrap = `
profile: aggressive
account:
  base_currency: USD
hard_limits:
  max_risk_per_trade_pct: 35
  max_total_open_risk_pct: 80
  max_new_trades_per_day: 6
  max_daily_loss_pct: 40
  consecutive_loss_days_halt: 5
whitelist:
  underlyings: [spy, QQQ]
instrument_rules:
  allow_naked_short_options: false
  min_open_interest: 300
  max_relative_spread: 0.15
plan_requirements: [target, stop, invalidation, time_stop]
execution_policy:
  order_type: limit
  start_at: mid
  reprice_interval_sec: 20
  max_reprices: 3
  fee_per_contract: 0
  fee_per_share: 0
risk_declaration_tolerance: 0.01
pnl_reconciliation_tolerance_usd: 0.01
quote_max_age_sec: 15
proposal_ttl_sec: 1800
`

func TestBootstrapIsStrictAndDropsDeadFields(t *testing.T) {
	p, err := DecodeBootstrapYAML([]byte(validBootstrap))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Whitelist.Underlyings, ","); got != "QQQ,SPY" {
		t.Fatalf("normalized underlyings=%q", got)
	}
	if got := strings.Join(p.PlanRequirements, ","); got != "invalidation,stop,target,time_stop" {
		t.Fatalf("normalized requirements=%q", got)
	}
	_, body, _, err := Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, dead := range []string{"profile", "base_currency", "order_type", "allow_naked_short_options"} {
		if bytes.Contains(body, []byte(dead)) {
			t.Fatalf("dead field %q entered canonical policy: %s", dead, body)
		}
	}

	for name, mutation := range map[string]string{
		"unknown":       strings.Replace(validBootstrap, "profile: aggressive", "profile: aggressive\nunknown_policy: true", 1),
		"naked short":   strings.Replace(validBootstrap, "allow_naked_short_options: false", "allow_naked_short_options: true", 1),
		"market order":  strings.Replace(validBootstrap, "order_type: limit", "order_type: market", 1),
		"duplicate set": strings.Replace(validBootstrap, "underlyings: [spy, QQQ]", "underlyings: [SPY, spy]", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBootstrapYAML([]byte(mutation)); err == nil {
				t.Fatal("invalid bootstrap was accepted")
			}
		})
	}
}

func TestCanonicalDigestCommitsToVersionAndExactBody(t *testing.T) {
	p, err := DecodeBootstrapYAML([]byte(validBootstrap))
	if err != nil {
		t.Fatal(err)
	}
	normalized, body, digest, err := Canonical(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCanonical(SchemaVersion, body, digest[:])
	if err != nil {
		t.Fatalf("decode canonical: %v", err)
	}
	if !reflect.DeepEqual(decoded, normalized) {
		t.Fatalf("decoded policy changed: %+v != %+v", decoded, normalized)
	}
	_, decodedJSON, _, _ := Canonical(decoded)
	if !bytes.Equal(decodedJSON, body) {
		t.Fatalf("decoded body changed: %s != %s", decodedJSON, body)
	}

	wrong := sha256.Sum256([]byte("wrong"))
	if _, err := DecodeCanonical(SchemaVersion, body, wrong[:]); err == nil {
		t.Fatal("wrong digest accepted")
	}
	if _, err := DecodeCanonical(SchemaVersion+1, body, digest[:]); err == nil {
		t.Fatal("wrong schema accepted")
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	unknown, _ := json.Marshal(raw)
	if _, err := DecodeCanonical(SchemaVersion, unknown, digest[:]); err == nil {
		t.Fatal("unknown JSON field accepted")
	}
}

func TestClassifyChangeCoversTightenWidenAndMixed(t *testing.T) {
	base, err := DecodeBootstrapYAML([]byte(validBootstrap))
	if err != nil {
		t.Fatal(err)
	}
	tight := base
	tight.HardLimits.MaxRiskPerTradePct = units.MustPercent("20")
	tight.InstrumentRules.MinOpenInterest = 500
	if got, err := ClassifyChange(base, tight); err != nil || got != ChangeTighten {
		t.Fatalf("tighten=%q err=%v", got, err)
	}

	wide := base
	wide.ProposalTTLSec = 3600
	wide.ExecutionPolicy.MaxReprices = 4
	if got, err := ClassifyChange(base, wide); err != nil || got != ChangeWiden {
		t.Fatalf("widen=%q err=%v", got, err)
	}

	mixed := base
	mixed.HardLimits.MaxRiskPerTradePct = units.MustPercent("20")
	mixed.ProposalTTLSec = 3600
	if got, err := ClassifyChange(base, mixed); err != nil || got != ChangeMixed {
		t.Fatalf("mixed=%q err=%v", got, err)
	}

	restrictSymbols := base
	restrictSymbols.Whitelist.Underlyings = []string{"SPY"}
	if got, err := ClassifyChange(base, restrictSymbols); err != nil || got != ChangeTighten {
		t.Fatalf("whitelist tighten=%q err=%v", got, err)
	}
	moreRequirements := base
	moreRequirements.PlanRequirements = append(append([]string{}, base.PlanRequirements...), "entry_reason")
	if got, err := ClassifyChange(base, moreRequirements); err != nil || got != ChangeTighten {
		t.Fatalf("requirements tighten=%q err=%v", got, err)
	}
}
