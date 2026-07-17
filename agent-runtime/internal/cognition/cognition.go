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

type Option func(*options)

type options struct {
	telemetry telemetrySink
}

// WithTelemetry sends bounded usage metadata to the kernel. Delivery is best
// effort and never changes a cognition result.
func WithTelemetry(sink func(Telemetry) error) Option {
	return func(cfg *options) { cfg.telemetry = sink }
}

func New(opts ...Option) (Cognition, error) {
	var cfg options
	for _, option := range opts {
		option(&cfg)
	}
	switch os.Getenv("COGNITION") {
	case "", "stub":
		return Stub{}, nil
	case "llm":
		return newLLMFromEnvironment(cfg.telemetry)
	default:
		return nil, fmt.Errorf("unknown COGNITION %q", os.Getenv("COGNITION"))
	}
}
