package policy

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"alpheus/kernel/internal/units"

	"gopkg.in/yaml.v3"
)

// bootstrapDocument is the only accepted shape of the legacy limits file.
// Known dead fields are decoded so the current file can be imported once, but
// they are deliberately absent from Policy and therefore never become runtime
// authority.
type bootstrapDocument struct {
	Profile string `yaml:"profile"`
	Account struct {
		BaseCurrency string `yaml:"base_currency"`
	} `yaml:"account"`
	HardLimits struct {
		MaxRiskPerTradePct      units.PercentMicros `yaml:"max_risk_per_trade_pct"`
		MaxTotalOpenRiskPct     units.PercentMicros `yaml:"max_total_open_risk_pct"`
		MaxNewTradesPerDay      int                 `yaml:"max_new_trades_per_day"`
		MaxDailyLossPct         units.PercentMicros `yaml:"max_daily_loss_pct"`
		ConsecutiveLossDaysHalt int                 `yaml:"consecutive_loss_days_halt"`
	} `yaml:"hard_limits"`
	Whitelist struct {
		Underlyings []string `yaml:"underlyings"`
	} `yaml:"whitelist"`
	InstrumentRules struct {
		AllowNakedShortOptions bool              `yaml:"allow_naked_short_options"`
		MinOpenInterest        int               `yaml:"min_open_interest"`
		MaxRelativeSpread      units.RatioMicros `yaml:"max_relative_spread"`
	} `yaml:"instrument_rules"`
	PlanRequirements []string `yaml:"plan_requirements"`
	ExecutionPolicy  struct {
		OrderType          string       `yaml:"order_type"`
		StartAt            string       `yaml:"start_at"`
		RepriceIntervalSec int          `yaml:"reprice_interval_sec"`
		MaxReprices        int          `yaml:"max_reprices"`
		FeePerContract     units.Micros `yaml:"fee_per_contract"`
		FeePerShare        units.Micros `yaml:"fee_per_share"`
	} `yaml:"execution_policy"`
	RiskDeclarationTolerance   units.Micros `yaml:"risk_declaration_tolerance"`
	PnLReconciliationTolerance units.Micros `yaml:"pnl_reconciliation_tolerance_usd"`
	QuoteMaxAgeSec             int          `yaml:"quote_max_age_sec"`
	ProposalTTLSec             int          `yaml:"proposal_ttl_sec"`
}

func LoadBootstrapFile(path string) (Policy, error) {
	if path == "" {
		return Policy{}, fmt.Errorf("policy bootstrap file path is required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, fmt.Errorf("read policy bootstrap file: %w", err)
	}
	return DecodeBootstrapYAML(raw)
}

func DecodeBootstrapYAML(raw []byte) (Policy, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var source bootstrapDocument
	if err := decoder.Decode(&source); err != nil {
		return Policy{}, fmt.Errorf("decode policy bootstrap: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Policy{}, fmt.Errorf("policy bootstrap contains multiple YAML documents")
		}
		return Policy{}, fmt.Errorf("decode trailing policy bootstrap data: %w", err)
	}
	// These are structural invariants, not mutable policy. Accept only the
	// historical representation that agrees with the current code.
	if source.InstrumentRules.AllowNakedShortOptions {
		return Policy{}, fmt.Errorf("allow_naked_short_options cannot enable a structurally unsupported effect")
	}
	if source.ExecutionPolicy.OrderType != "limit" {
		return Policy{}, fmt.Errorf("order_type must match the structural limit-only invariant")
	}

	var destination Policy
	destination.HardLimits.MaxRiskPerTradePct = source.HardLimits.MaxRiskPerTradePct
	destination.HardLimits.MaxTotalOpenRiskPct = source.HardLimits.MaxTotalOpenRiskPct
	destination.HardLimits.MaxNewTradesPerDay = source.HardLimits.MaxNewTradesPerDay
	destination.HardLimits.MaxDailyLossPct = source.HardLimits.MaxDailyLossPct
	destination.HardLimits.ConsecutiveLossDaysHalt = source.HardLimits.ConsecutiveLossDaysHalt
	destination.Whitelist.Underlyings = source.Whitelist.Underlyings
	destination.InstrumentRules.MinOpenInterest = source.InstrumentRules.MinOpenInterest
	destination.InstrumentRules.MaxRelativeSpread = source.InstrumentRules.MaxRelativeSpread
	destination.PlanRequirements = source.PlanRequirements
	destination.ExecutionPolicy.StartAt = source.ExecutionPolicy.StartAt
	destination.ExecutionPolicy.RepriceIntervalSec = source.ExecutionPolicy.RepriceIntervalSec
	destination.ExecutionPolicy.MaxReprices = source.ExecutionPolicy.MaxReprices
	destination.ExecutionPolicy.FeePerContract = source.ExecutionPolicy.FeePerContract
	destination.ExecutionPolicy.FeePerShare = source.ExecutionPolicy.FeePerShare
	destination.RiskDeclarationTolerance = source.RiskDeclarationTolerance
	destination.PnLReconciliationTolerance = source.PnLReconciliationTolerance
	destination.QuoteMaxAgeSec = source.QuoteMaxAgeSec
	destination.ProposalTTLSec = source.ProposalTTLSec
	normalized, _, _, err := Canonical(destination)
	return normalized, err
}
