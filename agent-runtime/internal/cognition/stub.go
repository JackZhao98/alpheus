// Rule-based stand-in. Lets you e2e-test scheduling, kernel gating,
// journaling and blackboard flow with ZERO prompts written. Deliberately
// boring: it mostly PASSes, and periodically emits a tiny well-formed shadow
// proposal so the Class-B path gets exercised.
package cognition

import (
	"encoding/json"

	"alpheus/agentruntime/internal/contracts"
	"alpheus/agentruntime/internal/roles"
)

type Stub struct{}

func (Stub) Run(role roles.Role, _ map[string]json.RawMessage) (contracts.Output, error) {
	switch role.Role {
	case "desk_master":
		return contracts.DeskDecision{
			Action:    "PROPOSE",
			Reasoning: "stub: exercising Class-B path in shadow",
			Proposals: []contracts.ProposedOperation{{
				Action: "open", Kind: "option", Underlying: "SPY", Symbol: "SPY",
				Side: "buy", Qty: json.Number("1"), MaxRiskUSD: number("35"), Shadow: true,
				Thesis: "stub plumbing test", Setup: "stub",
				Plan: &contracts.ExitPlan{Stop: "-30%", Invalidation: "regime flips", TimeStop: "15:45 ET", Target: "+50%"},
			}},
		}, nil
	case "scout":
		return contracts.OpportunityBrief{Action: "PASS"}, nil
	case "position_manager":
		return contracts.ExitAction{Blotter: "stub: flat"}, nil
	case "coach":
		return contracts.JournalReview{
			Lessons: []map[string]any{{"text": "stub lesson", "confidence": 0.1, "applicable_when": "never"}},
		}, nil
	}
	return contracts.JournalReview{}, nil
}

func number(value string) *json.Number {
	number := json.Number(value)
	return &number
}
