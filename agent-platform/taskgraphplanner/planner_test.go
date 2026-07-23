package taskgraphplanner

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/taskgraphproposal"
)

func record(recordType, recordID, digest string) contracts.RecordRef {
	return contracts.RecordRef{
		Owner: contracts.OwnerAgentControl, RecordType: recordType,
		RecordID: recordID, SchemaRevision: 1, RecordDigest: digest,
	}
}

func revision(recordType, recordID, digest string) contracts.RevisionRef {
	return contracts.RevisionRef{
		RecordRef: record(recordType, recordID, digest), Generation: 1,
	}
}

func objective(recordID, blobID string, committedAt time.Time) blob.BlobRef {
	return blob.BlobRef{
		SchemaRevision: 1, BlobID: blobID,
		ContentDigest: strings.Repeat("b", 64),
		MediaType:     "application/json", SizeBytes: 64,
		Origin: record(
			"task_objective", recordID, strings.Repeat("c", 64),
		),
		CommittedAt: committedAt,
	}
}

func planningContext(deadline time.Time) Context {
	return Context{
		RunID: "run-1", ParentTaskID: "root-task-1",
		ExpectedRunStateGeneration:    2,
		ExpectedParentStateGeneration: 3,
		SourceResult: record(
			"model_call_result", "proposal-result-1", strings.Repeat("d", 64),
		),
		RuntimePolicy: revision(
			"runtime_policy", "cortex-mvp", strings.Repeat("e", 64),
		),
		SpecialistOutputContract: revision(
			"output_contract_revision", "cortex-scout-research-memo-v1",
			strings.Repeat("f", 64),
		),
		AnswerOutputContract: revision(
			"output_contract_revision", "cortex-text-output-v1",
			strings.Repeat("1", 64),
		),
		Actor: contracts.AuditActor{
			PrincipalID: "cortex-control-1",
			Kind:        contracts.PrincipalWorkload,
			Audience:    contracts.AudienceControlAPI,
		},
		Round: 1, MaxRounds: 2,
		DeadlineAt: deadline,
	}
}

func proposal() taskgraphproposal.Proposal {
	return taskgraphproposal.Proposal{
		SchemaRevision: 1, Rationale: "two independent evidence lanes",
		JoinMode: taskgraphproposal.JoinMinimumSucceeded,
		Branches: []taskgraphproposal.Branch{
			{
				RoleID: "market_scout", Objective: "Inspect price.",
				ToolID: "kernel_equity_quotes",
			},
			{
				RoleID:    "fundamental_scout",
				Objective: "Assess durable business quality.", ToolID: "",
			},
		},
	}
}

func TestBuildExpandsProposalIntoDeterministicFanoutJoin(t *testing.T) {
	committedAt := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	deadline := committedAt.Add(6 * time.Minute)
	objectives := []blob.BlobRef{
		objective(
			"proposal-result-1-branch-01",
			"00000000-0000-4000-8000-000000000001", committedAt,
		),
		objective(
			"proposal-result-1-branch-02",
			"00000000-0000-4000-8000-000000000002",
			committedAt.Add(time.Second),
		),
		objective(
			"proposal-result-1-decision-desk",
			"00000000-0000-4000-8000-000000000003",
			committedAt.Add(2*time.Second),
		),
	}
	first, err := Build(proposal(), planningContext(deadline), objectives)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(proposal(), planningContext(deadline), objectives)
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, _ := first.Plan.Digest()
	secondDigest, _ := second.Plan.Digest()
	if firstDigest != secondDigest ||
		first.Envelope.RequestDigest != second.Envelope.RequestDigest {
		t.Fatal("TaskGraph expansion is not replay-stable")
	}
	if len(first.Plan.Nodes) != 3 || len(first.Plan.Edges) != 2 ||
		len(first.Plan.Joins) != 1 ||
		first.Plan.AuthorizedLimit.MaxParallelism != 2 ||
		first.Plan.Nodes[0].Limit.MaxModelCalls != 2 ||
		first.Plan.Nodes[0].Limit.MaxToolCalls != 1 ||
		first.Plan.Nodes[1].Limit.MaxModelCalls != 1 ||
		first.Plan.Nodes[2].RoleID != "decision_desk" {
		t.Fatalf("unexpected expanded plan: %+v", first.Plan)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"tool_grants":null`) {
		t.Fatal("empty Tool grants must be encoded as an array")
	}
	join := first.Plan.Joins[0]
	if string(join.Policy) != "minimum_succeeded" ||
		join.MinimumSuccess != 1 ||
		string(join.FailurePolicy) != "continue_if_threshold_met" {
		t.Fatalf("unexpected Join: %+v", join)
	}
}

func TestBuildPreservesControlledRoundFence(t *testing.T) {
	committedAt := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	deadline := committedAt.Add(6 * time.Minute)
	objectives := []blob.BlobRef{
		objective(
			"proposal-result-1-branch-01",
			"00000000-0000-4000-8000-000000000011", committedAt,
		),
		objective(
			"proposal-result-1-branch-02",
			"00000000-0000-4000-8000-000000000012",
			committedAt.Add(time.Second),
		),
		objective(
			"proposal-result-1-decision-desk",
			"00000000-0000-4000-8000-000000000013",
			committedAt.Add(2*time.Second),
		),
	}
	context := planningContext(deadline)
	context.Round = 2
	context.MaxRounds = 2
	command, err := Build(proposal(), context, objectives)
	if err != nil || command.Plan.Round != 2 ||
		command.Plan.MaxRounds != 2 {
		t.Fatalf("round plan=%+v err=%v", command.Plan, err)
	}
	context.Round = 3
	if _, err := Build(proposal(), context, objectives); err == nil {
		t.Fatal("planner accepted a round beyond the frozen maximum")
	}
}

func TestBuildRejectsObjectiveCountAndExpiredDeadline(t *testing.T) {
	committedAt := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	values := []blob.BlobRef{
		objective(
			"one", "00000000-0000-4000-8000-000000000001",
			committedAt,
		),
	}
	if _, err := Build(
		proposal(), planningContext(committedAt.Add(time.Minute)), values,
	); err == nil {
		t.Fatal("wrong objective count passed")
	}
	values = append(values,
		objective(
			"two", "00000000-0000-4000-8000-000000000002",
			committedAt,
		),
		objective(
			"desk", "00000000-0000-4000-8000-000000000003",
			committedAt,
		),
	)
	if _, err := Build(
		proposal(), planningContext(committedAt), values,
	); err == nil {
		t.Fatal("expired deadline passed")
	}
}
