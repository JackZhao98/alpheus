// Package cognition is the LLM port. A Cognition receives an assembled
// context and MUST return the role's contract type. How it gets there
// (which model, which prompt) is an implementation detail behind this line.
package cognition

import (
	"encoding/json"
	"fmt"
	"os"

	"alpheus/agentruntime/internal/contracts"
	"alpheus/agentruntime/internal/roles"
)

type Cognition interface {
	Run(role roles.Role, ctx map[string]json.RawMessage) (contracts.Output, error)
}

func New() (Cognition, error) {
	switch os.Getenv("COGNITION") {
	case "", "stub":
		return Stub{}, nil
	case "llm":
		return LLM{}, nil
	default:
		return nil, fmt.Errorf("unknown COGNITION %q", os.Getenv("COGNITION"))
	}
}
