// Package policy owns the typed, mutable human policy enforced by the Kernel.
//
// Policy values are database authority, not deployment configuration. This
// package deliberately contains no database, HTTP, environment, or broker
// code: it defines the schema, canonical representation, validation, and the
// semantic direction of a revision change. Structural invariants remain in
// code and are not switches in this document.
package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"alpheus/kernel/internal/units"
)

const SchemaVersion = 1

const (
	maxUnderlyings      = 2048
	maxPlanRequirements = 32
	maxPolicyString     = 64
	maxQuoteAgeSec      = 24 * 60 * 60
	maxProposalTTLSec   = 30 * 24 * 60 * 60
	maxRepriceInterval  = 24 * 60 * 60
	maxReprices         = 100
)

type Policy struct {
	HardLimits struct {
		MaxRiskPerTradePct      units.PercentMicros `json:"max_risk_per_trade_pct"`
		MaxTotalOpenRiskPct     units.PercentMicros `json:"max_total_open_risk_pct"`
		MaxNewTradesPerDay      int                 `json:"max_new_trades_per_day"`
		MaxDailyLossPct         units.PercentMicros `json:"max_daily_loss_pct"`
		ConsecutiveLossDaysHalt int                 `json:"consecutive_loss_days_halt"`
	} `json:"hard_limits"`
	Whitelist struct {
		Underlyings []string `json:"underlyings"`
	} `json:"whitelist"`
	InstrumentRules struct {
		MinOpenInterest   int               `json:"min_open_interest"`
		MaxRelativeSpread units.RatioMicros `json:"max_relative_spread"`
	} `json:"instrument_rules"`
	PlanRequirements []string `json:"plan_requirements"`
	ExecutionPolicy  struct {
		StartAt            string       `json:"start_at"`
		RepriceIntervalSec int          `json:"reprice_interval_sec"`
		MaxReprices        int          `json:"max_reprices"`
		FeePerContract     units.Micros `json:"fee_per_contract"`
		FeePerShare        units.Micros `json:"fee_per_share"`
	} `json:"execution_policy"`
	RiskDeclarationTolerance   units.Micros `json:"risk_declaration_tolerance"`
	PnLReconciliationTolerance units.Micros `json:"pnl_reconciliation_tolerance_usd"`
	QuoteMaxAgeSec             int          `json:"quote_max_age_sec"`
	ProposalTTLSec             int          `json:"proposal_ttl_sec"`
}

type canonicalDocument struct {
	SchemaVersion int    `json:"schema_version"`
	Policy        Policy `json:"policy"`
}

// Canonical validates and normalizes a policy, then returns the exact policy
// JSON stored in PostgreSQL and a digest that also commits to the schema
// version. List-valued sets are sorted so cosmetic file order cannot create a
// new revision.
func Canonical(input Policy) (Policy, []byte, [sha256.Size]byte, error) {
	normalized, err := normalize(input)
	if err != nil {
		return Policy{}, nil, [sha256.Size]byte{}, err
	}
	body, err := json.Marshal(normalized)
	if err != nil {
		return Policy{}, nil, [sha256.Size]byte{}, fmt.Errorf("marshal policy: %w", err)
	}
	document, err := json.Marshal(canonicalDocument{SchemaVersion: SchemaVersion, Policy: normalized})
	if err != nil {
		return Policy{}, nil, [sha256.Size]byte{}, fmt.Errorf("marshal policy document: %w", err)
	}
	return normalized, body, sha256.Sum256(document), nil
}

// DecodeCanonical is the fail-closed database read boundary. Unknown fields,
// a non-canonical policy body, a wrong schema version, or a digest mismatch are
// all authority corruption.
func DecodeCanonical(schemaVersion int, body, digest []byte) (Policy, error) {
	if schemaVersion != SchemaVersion || len(digest) != sha256.Size {
		return Policy{}, fmt.Errorf("unsupported or invalid policy schema")
	}
	var decoded Policy
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Policy{}, fmt.Errorf("decode policy: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Policy{}, err
	}
	normalized, _, canonicalDigest, err := Canonical(decoded)
	if err != nil {
		return Policy{}, err
	}
	// PostgreSQL JSONB legitimately rewrites whitespace and key order. Require
	// canonical values, then verify the digest over our canonical encoding.
	if !reflect.DeepEqual(decoded, normalized) {
		return Policy{}, fmt.Errorf("policy body is not canonical")
	}
	if !bytes.Equal(digest, canonicalDigest[:]) {
		return Policy{}, fmt.Errorf("policy digest mismatch")
	}
	return normalized, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("policy contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing policy data: %w", err)
	}
	return nil
}

func normalize(input Policy) (Policy, error) {
	p := input
	if err := validatePercent("max_risk_per_trade_pct", p.HardLimits.MaxRiskPerTradePct); err != nil {
		return Policy{}, err
	}
	if err := validatePercent("max_total_open_risk_pct", p.HardLimits.MaxTotalOpenRiskPct); err != nil {
		return Policy{}, err
	}
	if p.HardLimits.MaxNewTradesPerDay <= 0 {
		return Policy{}, fmt.Errorf("max_new_trades_per_day must be positive")
	}
	if err := validatePercent("max_daily_loss_pct", p.HardLimits.MaxDailyLossPct); err != nil {
		return Policy{}, err
	}
	if p.HardLimits.ConsecutiveLossDaysHalt <= 0 {
		return Policy{}, fmt.Errorf("consecutive_loss_days_halt must be positive")
	}

	underlyings, err := normalizedSet(p.Whitelist.Underlyings, true, maxUnderlyings, "underlying")
	if err != nil {
		return Policy{}, err
	}
	p.Whitelist.Underlyings = underlyings
	if p.InstrumentRules.MinOpenInterest < 0 {
		return Policy{}, fmt.Errorf("min_open_interest must not be negative")
	}
	if p.InstrumentRules.MaxRelativeSpread < 0 ||
		p.InstrumentRules.MaxRelativeSpread > units.RatioMicros(units.Scale) {
		return Policy{}, fmt.Errorf("max_relative_spread must be between 0 and 1")
	}
	p.PlanRequirements, err = normalizedSet(p.PlanRequirements, false, maxPlanRequirements, "plan requirement")
	if err != nil {
		return Policy{}, err
	}
	if len(p.PlanRequirements) == 0 {
		return Policy{}, fmt.Errorf("plan_requirements must not be empty")
	}

	p.ExecutionPolicy.StartAt = strings.ToLower(strings.TrimSpace(p.ExecutionPolicy.StartAt))
	if p.ExecutionPolicy.StartAt != "mid" && p.ExecutionPolicy.StartAt != "ask" {
		return Policy{}, fmt.Errorf("start_at must be mid or ask")
	}
	if p.ExecutionPolicy.RepriceIntervalSec <= 0 || p.ExecutionPolicy.RepriceIntervalSec > maxRepriceInterval {
		return Policy{}, fmt.Errorf("reprice_interval_sec is outside the structural range")
	}
	if p.ExecutionPolicy.MaxReprices < 0 || p.ExecutionPolicy.MaxReprices > maxReprices {
		return Policy{}, fmt.Errorf("max_reprices is outside the structural range")
	}
	if p.ExecutionPolicy.FeePerContract < 0 || p.ExecutionPolicy.FeePerShare < 0 {
		return Policy{}, fmt.Errorf("fee assumptions must not be negative")
	}
	if p.RiskDeclarationTolerance < 0 || p.PnLReconciliationTolerance < 0 {
		return Policy{}, fmt.Errorf("reconciliation tolerances must not be negative")
	}
	if p.QuoteMaxAgeSec <= 0 || p.QuoteMaxAgeSec > maxQuoteAgeSec {
		return Policy{}, fmt.Errorf("quote_max_age_sec is outside the structural range")
	}
	if p.ProposalTTLSec <= 0 || p.ProposalTTLSec > maxProposalTTLSec {
		return Policy{}, fmt.Errorf("proposal_ttl_sec is outside the structural range")
	}
	return p, nil
}

func validatePercent(name string, value units.PercentMicros) error {
	if value <= 0 || value > units.PercentMicros(100*units.Scale) {
		return fmt.Errorf("%s must be greater than 0 and no more than 100", name)
	}
	return nil
}

func normalizedSet(values []string, uppercase bool, maximum int, label string) ([]string, error) {
	if len(values) > maximum {
		return nil, fmt.Errorf("too many %ss", label)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if uppercase {
			value = strings.ToUpper(value)
		} else {
			value = strings.ToLower(value)
		}
		if value == "" || len(value) > maxPolicyString {
			return nil, fmt.Errorf("invalid %s", label)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	if result == nil {
		result = []string{}
	}
	return result, nil
}

type ChangeClass string

const (
	ChangeInitial ChangeClass = "initial"
	ChangeTighten ChangeClass = "tighten"
	ChangeWiden   ChangeClass = "widen"
	ChangeMixed   ChangeClass = "mixed"
)

// ClassifyChange applies code-owned semantics to every policy field. A mixed
// direction is never mislabeled as a tightening; callers may apply additional
// governance to widening or mixed activations.
func ClassifyChange(oldPolicy, newPolicy Policy) (ChangeClass, error) {
	oldPolicy, _, _, err := Canonical(oldPolicy)
	if err != nil {
		return "", fmt.Errorf("old policy: %w", err)
	}
	newPolicy, _, _, err = Canonical(newPolicy)
	if err != nil {
		return "", fmt.Errorf("new policy: %w", err)
	}
	var direction changeDirection
	compareHigherWidens(&direction, int64(oldPolicy.HardLimits.MaxRiskPerTradePct), int64(newPolicy.HardLimits.MaxRiskPerTradePct))
	compareHigherWidens(&direction, int64(oldPolicy.HardLimits.MaxTotalOpenRiskPct), int64(newPolicy.HardLimits.MaxTotalOpenRiskPct))
	compareHigherWidens(&direction, int64(oldPolicy.HardLimits.MaxNewTradesPerDay), int64(newPolicy.HardLimits.MaxNewTradesPerDay))
	compareHigherWidens(&direction, int64(oldPolicy.HardLimits.MaxDailyLossPct), int64(newPolicy.HardLimits.MaxDailyLossPct))
	compareHigherWidens(&direction, int64(oldPolicy.HardLimits.ConsecutiveLossDaysHalt), int64(newPolicy.HardLimits.ConsecutiveLossDaysHalt))
	compareAllowedSet(&direction, oldPolicy.Whitelist.Underlyings, newPolicy.Whitelist.Underlyings)
	compareHigherTightens(&direction, int64(oldPolicy.InstrumentRules.MinOpenInterest), int64(newPolicy.InstrumentRules.MinOpenInterest))
	compareHigherWidens(&direction, int64(oldPolicy.InstrumentRules.MaxRelativeSpread), int64(newPolicy.InstrumentRules.MaxRelativeSpread))
	compareRequiredSet(&direction, oldPolicy.PlanRequirements, newPolicy.PlanRequirements)
	compareStartAt(&direction, oldPolicy.ExecutionPolicy.StartAt, newPolicy.ExecutionPolicy.StartAt)
	compareHigherTightens(&direction, int64(oldPolicy.ExecutionPolicy.RepriceIntervalSec), int64(newPolicy.ExecutionPolicy.RepriceIntervalSec))
	compareHigherWidens(&direction, int64(oldPolicy.ExecutionPolicy.MaxReprices), int64(newPolicy.ExecutionPolicy.MaxReprices))
	compareHigherTightens(&direction, int64(oldPolicy.ExecutionPolicy.FeePerContract), int64(newPolicy.ExecutionPolicy.FeePerContract))
	compareHigherTightens(&direction, int64(oldPolicy.ExecutionPolicy.FeePerShare), int64(newPolicy.ExecutionPolicy.FeePerShare))
	compareHigherWidens(&direction, int64(oldPolicy.RiskDeclarationTolerance), int64(newPolicy.RiskDeclarationTolerance))
	compareHigherWidens(&direction, int64(oldPolicy.PnLReconciliationTolerance), int64(newPolicy.PnLReconciliationTolerance))
	compareHigherWidens(&direction, int64(oldPolicy.QuoteMaxAgeSec), int64(newPolicy.QuoteMaxAgeSec))
	compareHigherWidens(&direction, int64(oldPolicy.ProposalTTLSec), int64(newPolicy.ProposalTTLSec))
	return direction.class(), nil
}

type changeDirection struct{ tighten, widen bool }

func (d changeDirection) class() ChangeClass {
	switch {
	case d.tighten && d.widen:
		return ChangeMixed
	case d.widen:
		return ChangeWiden
	default:
		// Exact-value retries are handled before revision creation. Treat an
		// otherwise same semantic document as a tightening, never a widening.
		return ChangeTighten
	}
}

func compareHigherWidens(direction *changeDirection, oldValue, newValue int64) {
	if newValue > oldValue {
		direction.widen = true
	} else if newValue < oldValue {
		direction.tighten = true
	}
}

func compareHigherTightens(direction *changeDirection, oldValue, newValue int64) {
	compareHigherWidens(direction, newValue, oldValue)
}

func compareStartAt(direction *changeDirection, oldValue, newValue string) {
	if oldValue == newValue {
		return
	}
	if oldValue == "mid" && newValue == "ask" {
		direction.widen = true
	} else {
		direction.tighten = true
	}
}

func compareAllowedSet(direction *changeDirection, oldValues, newValues []string) {
	if reflect.DeepEqual(oldValues, newValues) {
		return
	}
	// An empty whitelist means every supported symbol is allowed.
	if len(oldValues) == 0 {
		direction.tighten = true
		return
	}
	if len(newValues) == 0 {
		direction.widen = true
		return
	}
	oldSet, newSet := stringSet(oldValues), stringSet(newValues)
	oldContainsNew := setContains(oldSet, newSet)
	newContainsOld := setContains(newSet, oldSet)
	if oldContainsNew {
		direction.tighten = true
	}
	if newContainsOld {
		direction.widen = true
	}
	if !oldContainsNew && !newContainsOld {
		direction.tighten, direction.widen = true, true
	}
}

func compareRequiredSet(direction *changeDirection, oldValues, newValues []string) {
	if reflect.DeepEqual(oldValues, newValues) {
		return
	}
	oldSet, newSet := stringSet(oldValues), stringSet(newValues)
	oldContainsNew := setContains(oldSet, newSet)
	newContainsOld := setContains(newSet, oldSet)
	if newContainsOld {
		direction.tighten = true
	}
	if oldContainsNew {
		direction.widen = true
	}
	if !oldContainsNew && !newContainsOld {
		direction.tighten, direction.widen = true, true
	}
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func setContains(container, candidate map[string]struct{}) bool {
	for value := range candidate {
		if _, exists := container[value]; !exists {
			return false
		}
	}
	return true
}
