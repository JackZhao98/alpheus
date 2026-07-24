// Package taskgraphproposal defines the deliberately untrusted model output
// that may motivate a Control-owned TaskGraph. A proposal contains no IDs,
// revisions, budgets, deadlines, topology, leases, or executable arguments.
package taskgraphproposal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"alpheus/agentplatform/capability"
)

const (
	SchemaRevisionV1 = 1
	MinBranchesV1    = 2
	MaxBranchesV1    = 4
)

var ErrInvalidProposal = errors.New("invalid TaskGraph proposal")

type JoinMode string

const (
	JoinAllRequired      JoinMode = "all_required"
	JoinMinimumSucceeded JoinMode = "minimum_succeeded"
)

type Branch struct {
	RoleID    string `json:"role_id"`
	Objective string `json:"objective"`
	ToolID    string `json:"tool_id"`
}

// Proposal is model-authored advice only. Deterministic Control separately
// selects immutable catalog revisions, budgets, IDs, deadlines, edges and Join
// policy before calling the canonical TaskGraph admission boundary.
type Proposal struct {
	SchemaRevision int      `json:"schema_revision"`
	Rationale      string   `json:"rationale"`
	JoinMode       JoinMode `json:"join_mode"`
	Branches       []Branch `json:"branches"`
}

func DecodeStrict(raw []byte) (Proposal, error) {
	value, err := decode(raw)
	if err != nil {
		return Proposal{}, err
	}
	if err := value.Validate(); err != nil {
		return Proposal{}, err
	}
	return value, nil
}

// DecodeAdvice accepts only schema-valid, installed read-only planning advice.
// Relationship mistakes such as assigning a reviewed Tool to the wrong
// Specialist are not authority: Control canonicalizes them deterministically.
func DecodeAdvice(raw []byte) (Proposal, error) {
	value, err := decode(raw)
	if err != nil {
		return Proposal{}, err
	}
	if err := value.validateAdvice(); err != nil {
		return Proposal{}, err
	}
	return value, nil
}

func decode(raw []byte) (Proposal, error) {
	var value Proposal
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Proposal{}, fmt.Errorf("%w: %v", ErrInvalidProposal, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Proposal{}, ErrInvalidProposal
	}
	return value, nil
}

// Canonicalize trims bounded text and assigns each Tool to its single reviewed
// owner. It never adds a branch, Tool, permission, budget, or objective.
func (value Proposal) Canonicalize() (Proposal, error) {
	if err := value.validateAdvice(); err != nil {
		return Proposal{}, err
	}
	canonical := value
	canonical.Rationale = strings.TrimSpace(value.Rationale)
	canonical.Branches = append([]Branch(nil), value.Branches...)
	for index := range canonical.Branches {
		branch := &canonical.Branches[index]
		branch.Objective = strings.TrimSpace(branch.Objective)
		if branch.ToolID == "" {
			continue
		}
		owners := capability.AgentRolesForTool(
			capability.ToolID(branch.ToolID),
		)
		if len(owners) != 1 {
			return Proposal{}, ErrInvalidProposal
		}
		branch.RoleID = string(owners[0])
	}
	if err := canonical.Validate(); err != nil {
		return Proposal{}, err
	}
	return canonical, nil
}

func (value Proposal) validateAdvice() error {
	if value.SchemaRevision != SchemaRevisionV1 ||
		!boundedAdviceText(value.Rationale, 1, 4000) ||
		(value.JoinMode != JoinAllRequired &&
			value.JoinMode != JoinMinimumSucceeded) ||
		len(value.Branches) < MinBranchesV1 ||
		len(value.Branches) > MaxBranchesV1 {
		return ErrInvalidProposal
	}
	for _, branch := range value.Branches {
		if !boundedAdviceText(branch.Objective, 1, 4000) {
			return ErrInvalidProposal
		}
		role, found := capability.LookupAgentRole(
			capability.AgentRoleID(branch.RoleID),
		)
		if !found || role.Revision != 1 {
			return ErrInvalidProposal
		}
		if branch.ToolID == "" {
			continue
		}
		tool, found := capability.LookupTool(
			capability.ToolID(branch.ToolID),
		)
		if !found || tool.State != capability.CatalogStateActive ||
			tool.Revision != 1 || tool.Effect != "read_only" ||
			len(capability.AgentRolesForTool(tool.ID)) != 1 {
			return ErrInvalidProposal
		}
	}
	return nil
}

func (value Proposal) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 ||
		!boundedText(value.Rationale, 1, 4000) ||
		(value.JoinMode != JoinAllRequired &&
			value.JoinMode != JoinMinimumSucceeded) ||
		len(value.Branches) < MinBranchesV1 ||
		len(value.Branches) > MaxBranchesV1 {
		return ErrInvalidProposal
	}
	seen := make(map[string]struct{}, len(value.Branches))
	for _, branch := range value.Branches {
		if !boundedText(branch.Objective, 1, 4000) {
			return ErrInvalidProposal
		}
		role, found := capability.LookupAgentRole(
			capability.AgentRoleID(branch.RoleID),
		)
		if !found || role.Revision != 1 {
			return ErrInvalidProposal
		}
		key := branch.RoleID + "\x00" + branch.ToolID
		if _, duplicate := seen[key]; duplicate {
			return ErrInvalidProposal
		}
		seen[key] = struct{}{}
		if branch.ToolID == "" {
			continue
		}
		tool, found := capability.LookupTool(capability.ToolID(branch.ToolID))
		if !found || tool.State != capability.CatalogStateActive ||
			tool.Revision != 1 || tool.Effect != "read_only" ||
			!roleOwnsTool(role.ID, tool.ID) {
			return ErrInvalidProposal
		}
	}
	return nil
}

func roleOwnsTool(
	roleID capability.AgentRoleID,
	toolID capability.ToolID,
) bool {
	for _, candidate := range capability.AgentRolesForTool(toolID) {
		if candidate == roleID {
			return true
		}
	}
	return false
}

func boundedText(value string, minimum, maximum int) bool {
	if strings.TrimSpace(value) != value ||
		len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) &&
			character != '\n' && character != '\t' {
			return false
		}
	}
	return true
}

func boundedAdviceText(value string, minimum, maximum int) bool {
	return boundedText(strings.TrimSpace(value), minimum, maximum)
}

// OutputSchema is the strict local schema sent to the model and committed as
// an immutable OutputContract. Semantic catalog ownership is still checked by
// Validate after schema validation.
func OutputSchema() map[string]any {
	toolIDs := []string{""}
	for _, tool := range capability.Catalog() {
		if tool.State != capability.CatalogStateActive ||
			tool.Revision != 1 || tool.Effect != "read_only" ||
			len(capability.AgentRolesForTool(tool.ID)) == 0 {
			continue
		}
		toolIDs = append(toolIDs, string(tool.ID))
	}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"schema_revision", "rationale", "join_mode", "branches",
		},
		"properties": map[string]any{
			"schema_revision": map[string]any{
				"type": "integer", "enum": []int{SchemaRevisionV1},
			},
			"rationale": map[string]any{
				"type": "string", "minLength": 1, "maxLength": 4000,
			},
			"join_mode": map[string]any{
				"type": "string",
				"enum": []string{
					string(JoinAllRequired),
					string(JoinMinimumSucceeded),
				},
			},
			"branches": map[string]any{
				"type": "array", "minItems": MinBranchesV1,
				"maxItems": MaxBranchesV1,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"role_id", "objective", "tool_id",
					},
					"properties": map[string]any{
						"role_id": map[string]any{
							"type": "string",
							"enum": capability.AgentRoleIDs(),
						},
						"objective": map[string]any{
							"type": "string", "minLength": 1,
							"maxLength": 4000,
						},
						"tool_id": map[string]any{
							"type": "string", "enum": toolIDs,
						},
					},
				},
			},
		},
	}
}
