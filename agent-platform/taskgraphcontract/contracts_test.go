package taskgraphcontract

import (
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/runtimecontract"
)

var testTime = time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)

func testRef(owner contracts.Owner, recordType, recordID string) contracts.RecordRef {
	return contracts.RecordRef{
		Owner: owner, RecordType: recordType, RecordID: recordID,
		SchemaRevision: 1, RecordDigest: strings.Repeat("a", 64),
	}
}

func testRevision(owner contracts.Owner, recordType, recordID string) contracts.RevisionRef {
	return contracts.RevisionRef{RecordRef: testRef(owner, recordType, recordID), Generation: 1}
}

func testBlob(taskID string) blob.BlobRef {
	return blob.BlobRef{
		SchemaRevision: 1, BlobID: "00000000-0000-4000-8000-000000000001",
		ContentDigest: strings.Repeat("b", 64), MediaType: "application/json", SizeBytes: 20,
		Origin:      testRef(contracts.OwnerAgentControl, "task_objective", taskID+"-objective"),
		CommittedAt: testTime.Add(-time.Minute),
	}
}

func taskLimit(tools int64) runtimecontract.BudgetLimit {
	return runtimecontract.BudgetLimit{
		MaxModelCalls: 1, MaxInputTokens: 2000, MaxOutputTokens: 1000,
		MaxToolCalls: tools, MaxWallTimeMS: 30000, MaxIdleTimeMS: 5000,
		MaxTasks: 1, MaxParallelism: 1,
	}
}

func validPlan() TaskGraphPlan {
	node := func(taskID, role, contract string, depth int64, grants []ToolGrantSnapshot) TaskGraphNode {
		return TaskGraphNode{
			TaskID: taskID, RoleID: role, RoleRevision: 1, Depth: depth,
			Objective: testBlob(taskID), InputRefs: []contracts.RecordRef{},
			OutputContractName: contract,
			OutputContract:     testRevision(contracts.OwnerAgentControl, "output_contract_revision", contract),
			ToolGrants:         grants, Limit: taskLimit(int64(len(grants))),
			DeadlineAt: testTime.Add(2 * time.Minute),
		}
	}
	return TaskGraphPlan{
		SchemaRevision: 1, GraphID: "graph-1", RunID: "run-1", ParentTaskID: "root-task-1",
		SourceResult:  testRef(contracts.OwnerAgentControl, "model_call_result", "result-1"),
		RuntimePolicy: testRevision(contracts.OwnerAgentControl, "runtime_policy", "policy-1"),
		Round:         1, MaxRounds: 2,
		AuthorizedLimit: runtimecontract.BudgetLimit{
			MaxModelCalls: 3, MaxInputTokens: 6000, MaxOutputTokens: 3000,
			MaxToolCalls: 2, MaxWallTimeMS: 90000, MaxIdleTimeMS: 15000,
			MaxTasks: 3, MaxDepth: 2, MaxFanout: 1, MaxParallelism: 2,
		},
		Nodes: []TaskGraphNode{
			node("market-task", "market_scout", "specialist_memo_v1", 1, []ToolGrantSnapshot{{
				ToolID: "kernel_equity_quotes", ToolRevision: 1, Effect: "read_only",
			}}),
			node("fundamental-task", "fundamental_scout", "specialist_memo_v1", 1, []ToolGrantSnapshot{{
				ToolID: "kernel_financials", ToolRevision: 1, Effect: "read_only",
			}}),
			node("desk-task", DecisionDeskRole, "answer_v1", 2, nil),
		},
		Edges: []TaskGraphEdge{
			{FromTaskID: "market-task", ToTaskID: "desk-task"},
			{FromTaskID: "fundamental-task", ToTaskID: "desk-task"},
		},
		Joins: []TaskGraphJoin{{
			JoinID: "desk-join", DownstreamTaskID: "desk-task",
			UpstreamTaskIDs: []string{"market-task", "fundamental-task"},
			Policy:          JoinAllRequired, MinimumSuccess: 2, FailurePolicy: JoinFailGraph,
			DeadlineAt: testTime.Add(90 * time.Second),
		}},
		CreatedBy: contracts.AuditActor{
			PrincipalID: "cortex-control-1", Kind: contracts.PrincipalWorkload,
			Audience: contracts.AudienceControlAPI,
		},
		CreatedAt: testTime, DeadlineAt: testTime.Add(3 * time.Minute),
	}
}

func validCommand(t *testing.T) AdmitTaskGraphCommand {
	t.Helper()
	plan := validPlan()
	digest, err := plan.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return AdmitTaskGraphCommand{
		SchemaRevision: 1,
		Envelope: contracts.CommandEnvelope{
			SchemaRevision: 1, CommandID: "command-1", Actor: plan.CreatedBy,
			Audience: contracts.AudienceControlAPI, CommandType: "admit_task_graph",
			IdempotencyKey: "run-1:graph-1", RequestDigest: digest,
			CausationID: plan.ParentTaskID, CorrelationID: plan.RunID,
			Deadline: testTime.Add(2 * time.Minute),
		},
		ExpectedRunStateGeneration: 1, ExpectedParentStateGeneration: 1, Plan: plan,
	}
}

func TestTaskGraphPlanAcceptsBoundedParallelJoin(t *testing.T) {
	if err := validPlan().Validate(); err != nil {
		t.Fatalf("valid plan: %v", err)
	}
	if err := validCommand(t).Validate(); err != nil {
		t.Fatalf("valid command: %v", err)
	}
}

func TestTaskGraphPlanRejectsUnsafeGraphs(t *testing.T) {
	tests := map[string]func(*TaskGraphPlan){
		"cycle": func(value *TaskGraphPlan) {
			value.Edges = append(value.Edges, TaskGraphEdge{FromTaskID: "desk-task", ToTaskID: "market-task"})
		},
		"missing join": func(value *TaskGraphPlan) { value.Joins = nil },
		"join omits edge": func(value *TaskGraphPlan) {
			value.Joins[0].UpstreamTaskIDs = []string{"market-task", "market-task"}
		},
		"wrong declared depth": func(value *TaskGraphPlan) { value.Nodes[2].Depth = 1 },
		"wrong role tool": func(value *TaskGraphPlan) {
			value.Nodes[0].ToolGrants[0].ToolID = "kernel_financials"
		},
		"candidate tool": func(value *TaskGraphPlan) {
			value.Nodes[0].ToolGrants[0].ToolID = "unknown_tool"
		},
		"tool revision drift": func(value *TaskGraphPlan) {
			value.Nodes[0].ToolGrants[0].ToolRevision = 2
		},
		"specialist latent tool budget": func(value *TaskGraphPlan) {
			value.Nodes[0].Limit.MaxToolCalls = 2
			value.AuthorizedLimit.MaxToolCalls++
		},
		"desk latent tool budget": func(value *TaskGraphPlan) {
			value.Nodes[2].Limit.MaxToolCalls = 1
			value.AuthorizedLimit.MaxToolCalls++
		},
		"desk tool escalation": func(value *TaskGraphPlan) {
			value.Nodes[2].ToolGrants = []ToolGrantSnapshot{{
				ToolID: "kernel_equity_quotes", ToolRevision: 1, Effect: "read_only",
			}}
			value.Nodes[2].Limit.MaxToolCalls = 1
		},
		"aggregate over budget": func(value *TaskGraphPlan) {
			value.AuthorizedLimit.MaxOutputTokens--
		},
		"fanout over budget": func(value *TaskGraphPlan) {
			value.AuthorizedLimit.MaxFanout = 0
		},
		"parallelism over ceiling": func(value *TaskGraphPlan) {
			value.AuthorizedLimit.MaxParallelism = AbsoluteMaxParallelismV1 + 1
		},
		"unbounded child expansion": func(value *TaskGraphPlan) {
			value.Nodes[0].Limit.MaxTasks = 2
		},
		"partial all join": func(value *TaskGraphPlan) {
			value.Joins[0].MinimumSuccess = 1
		},
		"silent partial continuation": func(value *TaskGraphPlan) {
			value.Joins[0].Policy = JoinMinimumSucceeded
			value.Joins[0].MinimumSuccess = 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validPlan()
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestMinimumSucceededJoinIsExplicit(t *testing.T) {
	value := validPlan()
	value.Joins[0].Policy = JoinMinimumSucceeded
	value.Joins[0].MinimumSuccess = 1
	value.Joins[0].FailurePolicy = JoinContinueIfThresholdMet
	if err := value.Validate(); err != nil {
		t.Fatalf("valid threshold join: %v", err)
	}
}

func TestAdmissionBindsPlanDigestAndControlAuthority(t *testing.T) {
	tests := map[string]func(*AdmitTaskGraphCommand){
		"worker actor": func(value *AdmitTaskGraphCommand) {
			value.Envelope.Actor.Audience = contracts.AudienceWorker
		},
		"changed plan": func(value *AdmitTaskGraphCommand) {
			value.Plan.Round = 2
		},
		"wrong run": func(value *AdmitTaskGraphCommand) {
			value.Envelope.CorrelationID = "run-2"
		},
		"deadline exceeds graph": func(value *AdmitTaskGraphCommand) {
			value.Envelope.Deadline = value.Plan.DeadlineAt.Add(time.Second)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := validCommand(t)
			mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
