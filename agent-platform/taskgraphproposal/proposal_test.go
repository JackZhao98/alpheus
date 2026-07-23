package taskgraphproposal

import (
	"encoding/json"
	"testing"
)

func validProposal() Proposal {
	return Proposal{
		SchemaRevision: SchemaRevisionV1,
		Rationale:      "Independent price and financial evidence can run concurrently.",
		JoinMode:       JoinMinimumSucceeded,
		Branches: []Branch{
			{
				RoleID: "market_scout", Objective: "Inspect the current quote.",
				ToolID: "kernel_equity_quotes",
			},
			{
				RoleID: "fundamental_scout", Objective: "Inspect reported financials.",
				ToolID: "kernel_financials",
			},
		},
	}
}

func TestProposalAcceptsIndependentReviewedBranches(t *testing.T) {
	value := validProposal()
	if err := value.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeStrict(raw)
	if err != nil || len(decoded.Branches) != 2 {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestProposalRejectsAuthorityAndCatalogDrift(t *testing.T) {
	tests := map[string]func(*Proposal){
		"one branch": func(value *Proposal) {
			value.Branches = value.Branches[:1]
		},
		"unknown role": func(value *Proposal) {
			value.Branches[0].RoleID = "uninstalled_agent"
		},
		"wrong tool owner": func(value *Proposal) {
			value.Branches[0].ToolID = "kernel_financials"
		},
		"preflight effect": func(value *Proposal) {
			value.Branches[0].RoleID = "market_scout"
			value.Branches[0].ToolID = "kernel_review_equity_order"
		},
		"duplicate branch": func(value *Proposal) {
			value.Branches[1] = value.Branches[0]
		},
		"blank objective": func(value *Proposal) {
			value.Branches[0].Objective = ""
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validProposal()
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid proposal passed")
			}
		})
	}
}

func TestProposalDecodeIsClosed(t *testing.T) {
	raw := []byte(`{
		"schema_revision":1,
		"rationale":"parallel evidence",
		"join_mode":"all_required",
		"branches":[
			{"role_id":"market_scout","objective":"price","tool_id":""},
			{"role_id":"fundamental_scout","objective":"financials","tool_id":""}
		],
		"max_parallelism":99
	}`)
	if _, err := DecodeStrict(raw); err == nil {
		t.Fatal("unknown authority field passed")
	}
}

func TestOutputSchemaEnumeratesOnlySpecialistReadTools(t *testing.T) {
	raw, err := json.Marshal(OutputSchema())
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{
		"kernel_equity_quotes", "research_gexbot_as_of",
		"market_gexbot_live",
	} {
		if !contains(text, expected) {
			t.Fatalf("schema is missing %s", expected)
		}
	}
	for _, denied := range []string{
		"kernel_review_equity_order", "kernel_review_option_order",
	} {
		if contains(text, denied) {
			t.Fatalf("schema includes non-graph Tool %s", denied)
		}
	}
}

func contains(value, target string) bool {
	for index := 0; index+len(target) <= len(value); index++ {
		if value[index:index+len(target)] == target {
			return true
		}
	}
	return false
}
