package taskgraphround

import (
	"encoding/json"
	"testing"
)

func TestDecisionAcceptsAnswerOrBoundedRefinement(t *testing.T) {
	answer := []byte(`{"schema_revision":1,"action":"answer","text":"final synthesis","rationale":"","join_mode":"all_required","branches":[]}`)
	value, err := DecodeStrict(answer)
	if err != nil || value.Action != ActionAnswer ||
		value.Text != "final synthesis" {
		t.Fatalf("answer=%+v err=%v", value, err)
	}

	refine := []byte(`{"schema_revision":1,"action":"refine","text":"","rationale":"one conflict remains","join_mode":"minimum_succeeded","branches":[{"role_id":"market_scout","objective":"verify the price reaction","tool_id":"kernel_equity_quotes"},{"role_id":"catalyst_scout","objective":"verify the earnings event","tool_id":"kernel_earnings_results"}]}`)
	value, err = DecodeStrict(refine)
	if err != nil || value.Action != ActionRefine ||
		len(value.Proposal().Branches) != 2 {
		t.Fatalf("refinement=%+v err=%v", value, err)
	}
}

func TestDecisionRejectsAuthorityAndActionMismatch(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"schema_revision":1,"action":"answer","text":"","rationale":"","join_mode":"all_required","branches":[]}`),
		[]byte(`{"schema_revision":1,"action":"answer","text":"done","rationale":"again","join_mode":"all_required","branches":[]}`),
		[]byte(`{"schema_revision":1,"action":"refine","text":"premature","rationale":"more work","join_mode":"all_required","branches":[]}`),
		[]byte(`{"schema_revision":1,"action":"answer","text":"done","rationale":"","join_mode":"all_required","branches":[],"max_parallelism":16}`),
	} {
		if _, err := DecodeStrict(raw); err == nil {
			t.Fatalf("invalid round decision accepted: %s", raw)
		}
	}
}

func TestDecisionOutputSchemaIsClosedAndBounded(t *testing.T) {
	raw, err := json.Marshal(OutputSchema())
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if json.Unmarshal(raw, &schema) != nil ||
		schema["additionalProperties"] != false {
		t.Fatalf("round decision schema is not closed: %#v", schema)
	}
	properties := schema["properties"].(map[string]any)
	branches := properties["branches"].(map[string]any)
	if branches["minItems"].(float64) != 0 ||
		branches["maxItems"].(float64) != 4 {
		t.Fatalf("round decision branches are not bounded: %#v", branches)
	}
}
