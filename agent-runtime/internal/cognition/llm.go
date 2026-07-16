// FILL POINT #2 — the real LLM call.
//
// Wiring plan:
//  1. Render role.PromptSlots (skip empty slots) + serialized context.
//  2. Call the model via the official SDK (github.com/anthropics/anthropic-sdk-go),
//     requesting structured output against the contract's JSON schema.
//  3. Unmarshal into the contract type, call Validate; on failure retry ONCE
//     with the validation error appended, then give up loudly.
//  4. Route role.ModelTier -> DECIDER_MODEL / MONITOR_MODEL env. Monitors run
//     often; keep them on the cheap tier or the API bill outgrows the account.
//  5. Return the parsed contract; the caller journals role.Version with every
//     trade so prompts are A/B-testable like strategies.
package cognition

import (
	"encoding/json"
	"errors"

	"alpheus/agentruntime/internal/contracts"
	"alpheus/agentruntime/internal/roles"
)

type LLM struct{}

func (LLM) Run(role roles.Role, ctx map[string]json.RawMessage) (contracts.Output, error) {
	return nil, errors.New("fill me: prompt render -> model call -> schema-validated parse")
}
