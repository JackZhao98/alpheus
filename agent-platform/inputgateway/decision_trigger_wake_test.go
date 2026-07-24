package inputgateway

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionTriggerWakePromptCarriesBoundaryAndExactCondition(
	t *testing.T,
) {
	trigger := validDecisionTriggerFixture()
	sample := DecisionTriggerSample{
		SampleID: "sample-1", TriggerID: trigger.TriggerID, Generation: 1,
		Value: json.Number("800.125"), ConditionMet: true, Fired: true,
		ReasonCode: "threshold_met",
		ObservedAt: "2026-07-24T06:34:07.409Z",
	}
	occurrence := DecisionTriggerOccurrence{
		OccurrenceID: "occurrence-1",
	}
	prompt := decisionTriggerWakePrompt(trigger, sample, occurrence)
	for _, expected := range []string{
		"EFFECT CEILING: NONE", "SPY", "mid_price gte 800.00000000",
		"Observed value: 800.125", "paper-only", "Do not place",
		"occurrence-1",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("wake prompt missing %q: %s", expected, prompt)
		}
	}
}
