// Package taskgraphround defines the deliberately untrusted Decision Desk
// choice after a TaskGraph Join. The model may either return the user-facing
// answer or request one bounded follow-up round. It cannot choose IDs, budget,
// deadlines, revisions, topology, permissions, or more than four branches.
package taskgraphround

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"alpheus/agentplatform/papercandidate"
	"alpheus/agentplatform/taskgraphproposal"
)

const SchemaRevisionV1 = 1
const SchemaRevisionV2 = 2

type Action string

const (
	ActionAnswer Action = "answer"
	ActionRefine Action = "refine"
)

var ErrInvalidDecision = errors.New("invalid TaskGraph round decision")

type Decision struct {
	SchemaRevision int                        `json:"schema_revision"`
	Action         Action                     `json:"action"`
	Text           string                     `json:"text"`
	Rationale      string                     `json:"rationale"`
	JoinMode       taskgraphproposal.JoinMode `json:"join_mode"`
	Branches       []taskgraphproposal.Branch `json:"branches"`
}

type CandidateDecision struct {
	SchemaRevision int                        `json:"schema_revision"`
	Action         Action                     `json:"action"`
	Text           string                     `json:"text"`
	Rationale      string                     `json:"rationale"`
	JoinMode       taskgraphproposal.JoinMode `json:"join_mode"`
	Branches       []taskgraphproposal.Branch `json:"branches"`
	PaperCandidate *papercandidate.Proposal   `json:"paper_candidate"`
}

func DecodeStrict(raw []byte) (Decision, error) {
	var value Decision
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrInvalidDecision, err)
	}
	if decoder.Decode(&struct{}{}) == nil {
		return Decision{}, ErrInvalidDecision
	}
	if err := value.Validate(); err != nil {
		return Decision{}, err
	}
	return value, nil
}

func DecodeCandidateStrict(raw []byte) (CandidateDecision, error) {
	var value CandidateDecision
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return CandidateDecision{},
			fmt.Errorf("%w: %v", ErrInvalidDecision, err)
	}
	if decoder.Decode(&struct{}{}) == nil ||
		value.SchemaRevision != SchemaRevisionV2 {
		return CandidateDecision{}, ErrInvalidDecision
	}
	base := Decision{
		SchemaRevision: SchemaRevisionV1,
		Action:         value.Action,
		Text:           value.Text,
		Rationale:      value.Rationale,
		JoinMode:       value.JoinMode,
		Branches:       value.Branches,
	}
	if base.Validate() != nil ||
		value.Action == ActionRefine && value.PaperCandidate != nil ||
		value.PaperCandidate != nil &&
			value.PaperCandidate.Validate() != nil {
		return CandidateDecision{}, ErrInvalidDecision
	}
	return value, nil
}

func DecodeRefinement(raw []byte) (Decision, error) {
	if value, err := DecodeStrict(raw); err == nil {
		if value.Action != ActionRefine {
			return Decision{}, ErrInvalidDecision
		}
		return value, nil
	}
	value, err := DecodeCandidateStrict(raw)
	if err != nil || value.Action != ActionRefine ||
		value.PaperCandidate != nil {
		return Decision{}, ErrInvalidDecision
	}
	return Decision{
		SchemaRevision: SchemaRevisionV1,
		Action:         value.Action,
		Text:           value.Text,
		Rationale:      value.Rationale,
		JoinMode:       value.JoinMode,
		Branches:       value.Branches,
	}, nil
}

func (value Decision) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 {
		return ErrInvalidDecision
	}
	switch value.Action {
	case ActionAnswer:
		if strings.TrimSpace(value.Text) == "" ||
			strings.TrimSpace(value.Text) != value.Text ||
			len(value.Text) > 16000 ||
			len(value.Rationale) > 4000 ||
			value.JoinMode != taskgraphproposal.JoinAllRequired ||
			len(value.Branches) != 0 {
			return ErrInvalidDecision
		}
	case ActionRefine:
		if value.Text != "" {
			return ErrInvalidDecision
		}
		proposal := value.Proposal()
		if proposal.Validate() != nil {
			return ErrInvalidDecision
		}
	default:
		return ErrInvalidDecision
	}
	return nil
}

func (value Decision) Proposal() taskgraphproposal.Proposal {
	return taskgraphproposal.Proposal{
		SchemaRevision: taskgraphproposal.SchemaRevisionV1,
		Rationale:      value.Rationale,
		JoinMode:       value.JoinMode,
		Branches:       value.Branches,
	}
}

// OutputSchema keeps one closed root shape for OpenAI strict structured
// output. Semantic action-dependent invariants are rechecked by DecodeStrict.
func OutputSchema() map[string]any {
	proposalSchema := taskgraphproposal.OutputSchema()
	proposalProperties, _ := proposalSchema["properties"].(map[string]any)
	branches, _ := proposalProperties["branches"].(map[string]any)
	branchItems := branches["items"]
	toolJoinMode := proposalProperties["join_mode"]
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"schema_revision", "action", "text", "rationale",
			"join_mode", "branches",
		},
		"properties": map[string]any{
			"schema_revision": map[string]any{
				"type": "integer", "enum": []int{SchemaRevisionV1},
			},
			"action": map[string]any{
				"type": "string",
				"enum": []string{string(ActionAnswer), string(ActionRefine)},
			},
			"text": map[string]any{
				"type": "string", "maxLength": 16000,
			},
			"rationale": map[string]any{
				"type": "string", "maxLength": 4000,
			},
			"join_mode": toolJoinMode,
			"branches": map[string]any{
				"type": "array", "minItems": 0,
				"maxItems": taskgraphproposal.MaxBranchesV1,
				"items":    branchItems,
			},
		},
	}
}

func CandidateOutputSchema() map[string]any {
	schema := OutputSchema()
	schema["required"] = append(
		schema["required"].([]string), "paper_candidate",
	)
	properties := schema["properties"].(map[string]any)
	properties["schema_revision"] = map[string]any{
		"type": "integer", "enum": []int{SchemaRevisionV2},
	}
	properties["paper_candidate"] = map[string]any{
		"anyOf": []any{
			map[string]any{"type": "null"},
			papercandidate.OutputSchema(),
		},
	}
	return schema
}
