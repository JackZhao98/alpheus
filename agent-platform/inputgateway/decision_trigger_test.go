package inputgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func validDecisionTriggerFixture() DecisionTrigger {
	return DecisionTrigger{
		TriggerID:       "11111111-1111-4111-8111-111111111111",
		Generation:      1,
		Title:           "SPY reaches 800",
		StrategyID:      "price_monitor",
		DataSource:      "kernel_quote",
		Symbol:          "SPY",
		Metric:          "mid_price",
		Comparator:      "gte",
		Threshold:       json.Number("800.00000000"),
		CooldownSeconds: 60,
		Objective:       "Re-evaluate SPY when the midpoint reaches 800.",
		Enabled:         true,
		State:           "armed",
		UpdatedAt:       "2026-07-24T06:00:00Z",
	}
}

func TestDecisionTriggerValidationAcceptsTypedPriceAndGEXConditions(t *testing.T) {
	price := validDecisionTriggerFixture()
	if err := validateDecisionTrigger(price); err != nil {
		t.Fatal(err)
	}
	gex := price
	gex.DataSource = "research_gexbot"
	gex.Metric = "gex_zero_gamma"
	gex.StrategyID = "gamma"
	gex.Comparator = "crosses_below"
	if err := validateDecisionTrigger(gex); err != nil {
		t.Fatal(err)
	}
	replay := gex
	replay.DataSource = "moody_blues_replay"
	if err := validateDecisionTrigger(replay); err != nil {
		t.Fatal(err)
	}
}

func TestDecisionTriggerValidationRejectsCrossProviderMetricAndFalseState(t *testing.T) {
	value := validDecisionTriggerFixture()
	value.Metric = "gex_call_wall"
	if err := validateDecisionTrigger(value); err == nil {
		t.Fatal("GEX metric was accepted for the Kernel quote source")
	}
	value = validDecisionTriggerFixture()
	value.Enabled = false
	if err := validateDecisionTrigger(value); err == nil {
		t.Fatal("enabled Trigger with paused state mismatch was accepted")
	}
}

func TestDecisionTriggerCommandPreservesNumericThreshold(t *testing.T) {
	command := DecisionTriggerCommand{
		TriggerID: "11111111-1111-4111-8111-111111111111",
		Title:     "SPY reaches 800", StrategyID: "price_monitor",
		DataSource: "kernel_quote", Symbol: "SPY", Metric: "mid_price",
		Comparator: "gte", Threshold: json.Number("800.125"),
		CooldownSeconds: 60,
		Objective:       "Re-evaluate SPY at the threshold.",
		Enabled:         true,
	}
	if err := validateDecisionTriggerCommand(command); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" ||
		!json.Valid(raw) ||
		!containsJSONNumber(raw, `"threshold":800.125`) {
		t.Fatalf("threshold was not encoded as a JSON number: %s", raw)
	}
}

func TestDecisionTriggerValidationAcceptsLatestEvaluation(t *testing.T) {
	value := validDecisionTriggerFixture()
	last := json.Number("738.23000000")
	value.LastValue = &last
	value.LastObservedAt = "2026-07-24T06:34:07.409Z"
	value.LastReasonCode = "threshold_not_met"
	if err := validateDecisionTrigger(value); err != nil {
		t.Fatal(err)
	}
	value.LastReasonCode = "invented"
	if err := validateDecisionTrigger(value); err == nil {
		t.Fatal("invented evaluation reason was accepted")
	}
}

func TestDecisionTriggerSampleValidationEnforcesAuditInvariants(t *testing.T) {
	value := DecisionTriggerSample{
		SampleID:   "22222222-2222-4222-8222-222222222222",
		TriggerID:  "11111111-1111-4111-8111-111111111111",
		Generation: 1, Value: json.Number("729"),
		ConditionMet: true, Fired: true, ReasonCode: "crossed",
		ObservedAt:  "2026-07-24T06:34:07.409Z",
		CommittedAt: "2026-07-24T06:34:07.410Z",
	}
	if err := validateDecisionTriggerSample(value); err != nil {
		t.Fatal(err)
	}
	value.ConditionMet = false
	if err := validateDecisionTriggerSample(value); err == nil {
		t.Fatal("fired sample without a met condition was accepted")
	}
}

func TestDecisionTriggerOccurrenceValidationRequiresFrozenDigests(t *testing.T) {
	value := DecisionTriggerOccurrence{
		Status:             "materialized",
		SampleID:           "22222222-2222-4222-8222-222222222222",
		TriggerID:          "11111111-1111-4111-8111-111111111111",
		OccurrenceID:       "33333333-3333-4333-8333-333333333333",
		OccurrenceDigest:   strings.Repeat("a", 64),
		SourceRecordDigest: strings.Repeat("b", 64),
		OccurredAt:         "2026-07-24T06:34:07.409Z",
	}
	if err := validateDecisionTriggerOccurrence(value); err != nil {
		t.Fatal(err)
	}
	value.OccurrenceDigest = "short"
	if err := validateDecisionTriggerOccurrence(value); err == nil {
		t.Fatal("short occurrence digest was accepted")
	}
}

func containsJSONNumber(raw []byte, expected string) bool {
	for index := 0; index+len(expected) <= len(raw); index++ {
		if string(raw[index:index+len(expected)]) == expected {
			return true
		}
	}
	return false
}
