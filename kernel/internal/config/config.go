package config

import (
	"os"

	"alpheus/kernel/internal/units"

	"gopkg.in/yaml.v3"
)

// Limits is THE CONSTITUTION, loaded from limits.yaml.
// Enforced by code in the risk package, never by prompts.
// Changing the file is a Class-D operation: human only.
type Limits struct {
	Profile    string `yaml:"profile" json:"profile"`
	HardLimits struct {
		MaxRiskPerTradePct      units.PercentMicros `yaml:"max_risk_per_trade_pct" json:"max_risk_per_trade_pct"`
		MaxTotalOpenRiskPct     units.PercentMicros `yaml:"max_total_open_risk_pct" json:"max_total_open_risk_pct"`
		MaxNewTradesPerDay      int                 `yaml:"max_new_trades_per_day" json:"max_new_trades_per_day"`
		MaxDailyLossPct         units.PercentMicros `yaml:"max_daily_loss_pct" json:"max_daily_loss_pct"`
		ConsecutiveLossDaysHalt int                 `yaml:"consecutive_loss_days_halt" json:"consecutive_loss_days_halt"`
	} `yaml:"hard_limits" json:"hard_limits"`
	Whitelist struct {
		Underlyings []string `yaml:"underlyings" json:"underlyings"`
	} `yaml:"whitelist" json:"whitelist"`
	InstrumentRules struct {
		AllowNakedShortOptions bool              `yaml:"allow_naked_short_options" json:"allow_naked_short_options"`
		MinOpenInterest        int               `yaml:"min_open_interest" json:"min_open_interest"`
		MaxRelativeSpread      units.RatioMicros `yaml:"max_relative_spread" json:"max_relative_spread"`
	} `yaml:"instrument_rules" json:"instrument_rules"`
	PlanRequirements []string `yaml:"plan_requirements" json:"plan_requirements"`
	ExecutionPolicy  struct {
		OrderType          string       `yaml:"order_type" json:"order_type"`
		StartAt            string       `yaml:"start_at" json:"start_at"`
		RepriceIntervalSec int          `yaml:"reprice_interval_sec" json:"reprice_interval_sec"`
		MaxReprices        int          `yaml:"max_reprices" json:"max_reprices"`
		FeePerContract     units.Micros `yaml:"fee_per_contract" json:"fee_per_contract"`
		FeePerShare        units.Micros `yaml:"fee_per_share" json:"fee_per_share"`
	} `yaml:"execution_policy" json:"execution_policy"`
	RiskDeclarationTolerance units.Micros `yaml:"risk_declaration_tolerance" json:"risk_declaration_tolerance"`
	QuoteMaxAgeSec           int          `yaml:"quote_max_age_sec" json:"quote_max_age_sec"`
}

func LoadLimits() (Limits, error) {
	path := Env("LIMITS_PATH", "/limits.yaml")
	var l Limits
	b, err := os.ReadFile(path)
	if err != nil {
		return l, err
	}
	return l, yaml.Unmarshal(b, &l)
}

func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
