// Package taskgraphplanner expands an untrusted model proposal into one
// deterministic, bounded, Control-owned TaskGraph command.
package taskgraphplanner

import (
	"fmt"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/capability"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/runtimecontract"
	"alpheus/agentplatform/taskgraphcontract"
	"alpheus/agentplatform/taskgraphproposal"
)

type Context struct {
	RunID                         string
	ParentTaskID                  string
	ExpectedRunStateGeneration    int64
	ExpectedParentStateGeneration int64
	SourceResult                  contracts.RecordRef
	RuntimePolicy                 contracts.RevisionRef
	SpecialistOutputContract      contracts.RevisionRef
	AnswerOutputContract          contracts.RevisionRef
	Actor                         contracts.AuditActor
	Round                         int64
	MaxRounds                     int64
	DeadlineAt                    time.Time
}

// Build accepts objective Blobs already committed by Control. Their stable
// committed times make the canonical command exactly replayable.
func Build(
	proposal taskgraphproposal.Proposal,
	context Context,
	objectives []blob.BlobRef,
) (taskgraphcontract.AdmitTaskGraphCommand, error) {
	if proposal.Validate() != nil || !validContext(context) ||
		len(objectives) != len(proposal.Branches)+1 {
		return taskgraphcontract.AdmitTaskGraphCommand{},
			fmt.Errorf("invalid TaskGraph planning input")
	}
	createdAt := time.Time{}
	for _, objective := range objectives {
		if objective.Validate() != nil ||
			objective.Origin.Owner != contracts.OwnerAgentControl ||
			objective.Origin.RecordType != "task_objective" {
			return taskgraphcontract.AdmitTaskGraphCommand{},
				fmt.Errorf("invalid TaskGraph objective")
		}
		committedAt := objective.CommittedAt.UTC()
		if committedAt.After(createdAt) {
			createdAt = committedAt
		}
	}
	if createdAt.IsZero() || !createdAt.Before(context.DeadlineAt) {
		return taskgraphcontract.AdmitTaskGraphCommand{},
			fmt.Errorf("TaskGraph planning deadline expired")
	}

	graphID := "task-graph-" + context.SourceResult.RecordID
	deskTaskID := graphID + "-decision-desk"
	nodes := make([]taskgraphcontract.TaskGraphNode, 0,
		len(proposal.Branches)+1)
	edges := make([]taskgraphcontract.TaskGraphEdge, 0,
		len(proposal.Branches))
	upstream := make([]string, 0, len(proposal.Branches))
	var authorized runtimecontract.BudgetLimit
	for index, branch := range proposal.Branches {
		role, _ := capability.LookupAgentRole(
			capability.AgentRoleID(branch.RoleID),
		)
		taskID := fmt.Sprintf("%s-branch-%02d", graphID, index+1)
		limit := specialistLimit(branch.ToolID != "")
		grants := []taskgraphcontract.ToolGrantSnapshot{}
		if branch.ToolID != "" {
			tool, _ := capability.LookupTool(capability.ToolID(branch.ToolID))
			grants = []taskgraphcontract.ToolGrantSnapshot{{
				ToolID:       string(tool.ID),
				ToolRevision: tool.Revision,
				Effect:       tool.Effect,
			}}
		}
		nodes = append(nodes, taskgraphcontract.TaskGraphNode{
			TaskID: taskID, RoleID: string(role.ID),
			RoleRevision: role.Revision, Depth: 1,
			Objective: objectives[index], InputRefs: []contracts.RecordRef{},
			OutputContractName: role.OutputContract,
			OutputContract:     context.SpecialistOutputContract,
			ToolGrants:         grants, Limit: limit,
			DeadlineAt: context.DeadlineAt,
		})
		edges = append(edges, taskgraphcontract.TaskGraphEdge{
			FromTaskID: taskID, ToTaskID: deskTaskID,
		})
		upstream = append(upstream, taskID)
		add(&authorized, limit)
	}
	deskLimit := decisionDeskLimit()
	nodes = append(nodes, taskgraphcontract.TaskGraphNode{
		TaskID: deskTaskID, RoleID: taskgraphcontract.DecisionDeskRole,
		RoleRevision: 1, Depth: 2, Objective: objectives[len(objectives)-1],
		InputRefs:          []contracts.RecordRef{},
		OutputContractName: "answer_v1",
		OutputContract:     context.AnswerOutputContract,
		ToolGrants:         []taskgraphcontract.ToolGrantSnapshot{},
		Limit:              deskLimit, DeadlineAt: context.DeadlineAt,
	})
	add(&authorized, deskLimit)
	authorized.MaxDepth = 2
	authorized.MaxFanout = 1
	authorized.MaxParallelism = int64(len(proposal.Branches))

	join := taskgraphcontract.TaskGraphJoin{
		JoinID: graphID + "-join", DownstreamTaskID: deskTaskID,
		UpstreamTaskIDs: upstream, DeadlineAt: context.DeadlineAt,
	}
	switch proposal.JoinMode {
	case taskgraphproposal.JoinAllRequired:
		join.Policy = taskgraphcontract.JoinAllRequired
		join.MinimumSuccess = int64(len(upstream))
		join.FailurePolicy = taskgraphcontract.JoinFailGraph
	case taskgraphproposal.JoinMinimumSucceeded:
		join.Policy = taskgraphcontract.JoinMinimumSucceeded
		join.MinimumSuccess = 1
		join.FailurePolicy = taskgraphcontract.JoinContinueIfThresholdMet
	default:
		return taskgraphcontract.AdmitTaskGraphCommand{},
			fmt.Errorf("unsupported TaskGraph Join mode")
	}

	plan := taskgraphcontract.TaskGraphPlan{
		SchemaRevision: taskgraphcontract.SchemaRevisionV1,
		GraphID:        graphID, RunID: context.RunID,
		ParentTaskID: context.ParentTaskID,
		SourceResult: context.SourceResult, RuntimePolicy: context.RuntimePolicy,
		Round: context.Round, MaxRounds: context.MaxRounds,
		AuthorizedLimit: authorized,
		Nodes:           nodes, Edges: edges, Joins: []taskgraphcontract.TaskGraphJoin{join},
		CreatedBy: context.Actor, CreatedAt: createdAt,
		DeadlineAt: context.DeadlineAt,
	}
	planDigest, err := plan.Digest()
	if err != nil {
		return taskgraphcontract.AdmitTaskGraphCommand{}, err
	}
	command := taskgraphcontract.AdmitTaskGraphCommand{
		SchemaRevision: taskgraphcontract.SchemaRevisionV1,
		Envelope: contracts.CommandEnvelope{
			SchemaRevision: 1,
			CommandID:      "admit-" + graphID,
			Actor:          context.Actor,
			Audience:       contracts.AudienceControlAPI,
			CommandType:    "admit_task_graph",
			IdempotencyKey: graphID,
			RequestDigest:  planDigest,
			CausationID:    context.ParentTaskID,
			CorrelationID:  context.RunID,
			Deadline:       context.DeadlineAt,
		},
		ExpectedRunStateGeneration:    context.ExpectedRunStateGeneration,
		ExpectedParentStateGeneration: context.ExpectedParentStateGeneration,
		Plan:                          plan,
	}
	if err := command.Validate(); err != nil {
		return taskgraphcontract.AdmitTaskGraphCommand{}, err
	}
	return command, nil
}

func validContext(value Context) bool {
	return value.RunID != "" && value.ParentTaskID != "" &&
		value.ExpectedRunStateGeneration > 0 &&
		value.ExpectedParentStateGeneration > 0 &&
		value.SourceResult.Validate() == nil &&
		value.SourceResult.Owner == contracts.OwnerAgentControl &&
		value.SourceResult.RecordType == "model_call_result" &&
		value.RuntimePolicy.Validate() == nil &&
		value.RuntimePolicy.Owner == contracts.OwnerAgentControl &&
		value.RuntimePolicy.RecordType == "runtime_policy" &&
		value.SpecialistOutputContract.Validate() == nil &&
		value.SpecialistOutputContract.Owner == contracts.OwnerAgentControl &&
		value.SpecialistOutputContract.RecordType == "output_contract_revision" &&
		value.AnswerOutputContract.Validate() == nil &&
		value.AnswerOutputContract.Owner == contracts.OwnerAgentControl &&
		value.AnswerOutputContract.RecordType == "output_contract_revision" &&
		value.Actor.Validate() == nil &&
		value.Actor.Kind == contracts.PrincipalWorkload &&
		value.Actor.Audience == contracts.AudienceControlAPI &&
		value.Round >= 1 && value.MaxRounds >= value.Round &&
		value.MaxRounds <= taskgraphcontract.AbsoluteMaxRoundsV1 &&
		!value.DeadlineAt.IsZero() && value.DeadlineAt.Location() == time.UTC
}

func specialistLimit(withTool bool) runtimecontract.BudgetLimit {
	limit := runtimecontract.BudgetLimit{
		MaxModelCalls: 1, MaxInputTokens: 64000, MaxOutputTokens: 3000,
		MaxWallTimeMS: 90000, MaxIdleTimeMS: 30000, MaxTasks: 1,
		MaxParallelism: 1,
	}
	if withTool {
		// Planner + one bounded argument correction + receipt-backed memo.
		limit.MaxModelCalls = 3
		limit.MaxInputTokens = 96000
		limit.MaxOutputTokens = 4500
		limit.MaxToolCalls = 1
		limit.MaxWallTimeMS = 180000
	}
	return limit
}

func decisionDeskLimit() runtimecontract.BudgetLimit {
	return runtimecontract.BudgetLimit{
		MaxModelCalls: 1, MaxInputTokens: 96000, MaxOutputTokens: 8000,
		MaxWallTimeMS: 120000, MaxIdleTimeMS: 30000, MaxTasks: 1,
		MaxParallelism: 1,
	}
}

func add(total *runtimecontract.BudgetLimit, value runtimecontract.BudgetLimit) {
	total.MaxModelCalls += value.MaxModelCalls
	total.MaxInputTokens += value.MaxInputTokens
	total.MaxOutputTokens += value.MaxOutputTokens
	total.MaxToolCalls += value.MaxToolCalls
	total.MaxExternalCostMicroUSD += value.MaxExternalCostMicroUSD
	total.MaxWallTimeMS += value.MaxWallTimeMS
	total.MaxIdleTimeMS += value.MaxIdleTimeMS
	total.MaxTasks += value.MaxTasks
	total.MaxInvalidOutputRetries += value.MaxInvalidOutputRetries
	total.MaxInfrastructureRetries += value.MaxInfrastructureRetries
}
