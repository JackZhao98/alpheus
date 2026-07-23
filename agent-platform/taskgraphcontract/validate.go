package taskgraphcontract

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"alpheus/agentplatform/canonical"
	"alpheus/agentplatform/capability"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/runtimecontract"
)

var (
	ErrInvalidTaskGraph = errors.New("invalid task graph contract")
	namePattern         = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

func (value ToolGrantSnapshot) validateForRole(roleID string, maxTools uint16) error {
	if !namePattern.MatchString(value.ToolID) || value.ToolRevision == 0 || value.Effect != "read_only" ||
		maxTools == 0 {
		return ErrInvalidTaskGraph
	}
	tool, ok := capability.LookupTool(capability.ToolID(value.ToolID))
	if !ok || tool.State != capability.CatalogStateActive || tool.Revision != value.ToolRevision ||
		tool.Effect != value.Effect {
		return ErrInvalidTaskGraph
	}
	for _, role := range capability.AgentRolesForTool(tool.ID) {
		if string(role) == roleID {
			return nil
		}
	}
	return ErrInvalidTaskGraph
}

func (value TaskGraphNode) validate(createdAt, graphDeadline time.Time) error {
	if !validID(value.TaskID) || !namePattern.MatchString(value.RoleID) || value.RoleRevision == 0 ||
		value.Depth < 1 || value.Depth > AbsoluteMaxDepthV1 || value.Objective.Validate() != nil ||
		value.Objective.Origin.Owner != contracts.OwnerAgentControl ||
		value.Objective.Origin.RecordType != "task_objective" ||
		value.Objective.CommittedAt.After(createdAt) ||
		len(value.InputRefs) > runtimecontract.AbsoluteMaxReferencesV1 ||
		!validRecordRefs(value.InputRefs) ||
		!namePattern.MatchString(value.OutputContractName) ||
		value.OutputContract.Validate() != nil ||
		value.OutputContract.Owner != contracts.OwnerAgentControl ||
		value.OutputContract.RecordType != "output_contract_revision" ||
		value.Limit.Validate() != nil || value.Limit.MaxModelCalls < 1 ||
		value.Limit.MaxWallTimeMS < 1 || value.Limit.MaxTasks != 1 ||
		value.Limit.MaxDepth != 0 || value.Limit.MaxFanout != 0 ||
		value.Limit.MaxParallelism != 1 || !validUTC(value.DeadlineAt) ||
		!createdAt.Before(value.DeadlineAt) || value.DeadlineAt.After(graphDeadline) {
		return ErrInvalidTaskGraph
	}

	maxTools := uint16(0)
	if value.RoleID == DecisionDeskRole {
		if value.RoleRevision != 1 || value.OutputContractName != "answer_v1" || len(value.ToolGrants) != 0 {
			return ErrInvalidTaskGraph
		}
	} else {
		role, ok := capability.LookupAgentRole(capability.AgentRoleID(value.RoleID))
		if !ok || role.Revision != value.RoleRevision {
			return ErrInvalidTaskGraph
		}
		maxTools = role.MaxToolCalls
		if value.OutputContractName != role.OutputContract {
			return ErrInvalidTaskGraph
		}
	}
	if len(value.ToolGrants) > int(maxTools) || int64(len(value.ToolGrants)) > value.Limit.MaxToolCalls {
		return ErrInvalidTaskGraph
	}
	seenTools := make(map[string]struct{}, len(value.ToolGrants))
	for _, grant := range value.ToolGrants {
		if _, exists := seenTools[grant.ToolID]; exists || grant.validateForRole(value.RoleID, maxTools) != nil {
			return ErrInvalidTaskGraph
		}
		seenTools[grant.ToolID] = struct{}{}
	}
	return nil
}

func (value TaskGraphPlan) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || !validID(value.GraphID) || !validID(value.RunID) ||
		!validID(value.ParentTaskID) || value.SourceResult.Validate() != nil ||
		value.SourceResult.Owner != contracts.OwnerAgentControl ||
		value.SourceResult.RecordType != "model_call_result" ||
		value.RuntimePolicy.Validate() != nil || value.RuntimePolicy.Owner != contracts.OwnerAgentControl ||
		value.RuntimePolicy.RecordType != "runtime_policy" ||
		value.Round < 1 || value.MaxRounds < 1 || value.Round > value.MaxRounds ||
		value.MaxRounds > AbsoluteMaxRoundsV1 || value.AuthorizedLimit.Validate() != nil ||
		value.AuthorizedLimit.MaxTasks < 1 || value.AuthorizedLimit.MaxTasks > AbsoluteMaxTasksV1 ||
		value.AuthorizedLimit.MaxParallelism < 1 ||
		value.AuthorizedLimit.MaxParallelism > AbsoluteMaxParallelismV1 ||
		value.AuthorizedLimit.MaxDepth < 1 || value.AuthorizedLimit.MaxDepth > AbsoluteMaxDepthV1 ||
		len(value.Nodes) < 1 || len(value.Nodes) > AbsoluteMaxTasksV1 ||
		int64(len(value.Nodes)) > value.AuthorizedLimit.MaxTasks ||
		len(value.Edges) > AbsoluteMaxEdgesV1 || len(value.Joins) > AbsoluteMaxJoinsV1 ||
		value.CreatedBy.Validate() != nil || value.CreatedBy.Kind != contracts.PrincipalWorkload ||
		value.CreatedBy.Audience != contracts.AudienceControlAPI ||
		!validUTC(value.CreatedAt) || !validUTC(value.DeadlineAt) ||
		!value.CreatedAt.Before(value.DeadlineAt) {
		return ErrInvalidTaskGraph
	}

	nodes := make(map[string]TaskGraphNode, len(value.Nodes))
	var aggregate runtimecontract.BudgetLimit
	for _, node := range value.Nodes {
		if _, exists := nodes[node.TaskID]; exists || node.validate(value.CreatedAt, value.DeadlineAt) != nil {
			return ErrInvalidTaskGraph
		}
		nodes[node.TaskID] = node
		addBudget(&aggregate, node.Limit)
	}
	if !aggregateWithin(aggregate, value.AuthorizedLimit) {
		return ErrInvalidTaskGraph
	}

	incoming := make(map[string][]string, len(nodes))
	outgoing := make(map[string][]string, len(nodes))
	edgeSet := make(map[string]struct{}, len(value.Edges))
	for _, edge := range value.Edges {
		if !validID(edge.FromTaskID) || !validID(edge.ToTaskID) || edge.FromTaskID == edge.ToTaskID {
			return ErrInvalidTaskGraph
		}
		if _, ok := nodes[edge.FromTaskID]; !ok {
			return ErrInvalidTaskGraph
		}
		if _, ok := nodes[edge.ToTaskID]; !ok {
			return ErrInvalidTaskGraph
		}
		key := edge.FromTaskID + "\x00" + edge.ToTaskID
		if _, exists := edgeSet[key]; exists {
			return ErrInvalidTaskGraph
		}
		edgeSet[key] = struct{}{}
		incoming[edge.ToTaskID] = append(incoming[edge.ToTaskID], edge.FromTaskID)
		outgoing[edge.FromTaskID] = append(outgoing[edge.FromTaskID], edge.ToTaskID)
	}
	for _, targets := range outgoing {
		if int64(len(targets)) > value.AuthorizedLimit.MaxFanout {
			return ErrInvalidTaskGraph
		}
	}
	depths, ok := graphDepths(nodes, incoming, outgoing)
	if !ok {
		return ErrInvalidTaskGraph
	}
	for taskID, depth := range depths {
		if nodes[taskID].Depth != depth || depth > value.AuthorizedLimit.MaxDepth {
			return ErrInvalidTaskGraph
		}
	}

	joins := make(map[string]struct{}, len(value.Joins))
	joinedTasks := make(map[string]struct{}, len(value.Joins))
	for _, join := range value.Joins {
		if !validID(join.JoinID) {
			return ErrInvalidTaskGraph
		}
		if _, exists := joins[join.JoinID]; exists {
			return ErrInvalidTaskGraph
		}
		joins[join.JoinID] = struct{}{}
		if _, exists := joinedTasks[join.DownstreamTaskID]; exists ||
			join.validate(nodes, incoming, value.CreatedAt, value.DeadlineAt) != nil {
			return ErrInvalidTaskGraph
		}
		joinedTasks[join.DownstreamTaskID] = struct{}{}
	}
	for taskID, upstream := range incoming {
		_, joined := joinedTasks[taskID]
		if (len(upstream) > 1) != joined {
			return ErrInvalidTaskGraph
		}
	}
	return nil
}

func (value TaskGraphJoin) validate(nodes map[string]TaskGraphNode, incoming map[string][]string, createdAt, graphDeadline time.Time) error {
	downstream, exists := nodes[value.DownstreamTaskID]
	if !exists || len(value.UpstreamTaskIDs) < 2 ||
		len(value.UpstreamTaskIDs) > AbsoluteMaxTasksV1 ||
		!validUTC(value.DeadlineAt) || !createdAt.Before(value.DeadlineAt) ||
		value.DeadlineAt.After(graphDeadline) || value.DeadlineAt.After(downstream.DeadlineAt) {
		return ErrInvalidTaskGraph
	}
	want := make(map[string]struct{}, len(incoming[value.DownstreamTaskID]))
	for _, taskID := range incoming[value.DownstreamTaskID] {
		want[taskID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(value.UpstreamTaskIDs))
	for _, taskID := range value.UpstreamTaskIDs {
		if _, exists := nodes[taskID]; !exists || taskID == value.DownstreamTaskID {
			return ErrInvalidTaskGraph
		}
		if _, exists := seen[taskID]; exists {
			return ErrInvalidTaskGraph
		}
		if _, exists := want[taskID]; !exists {
			return ErrInvalidTaskGraph
		}
		seen[taskID] = struct{}{}
	}
	if len(seen) != len(want) {
		return ErrInvalidTaskGraph
	}
	switch value.Policy {
	case JoinAllRequired:
		if value.MinimumSuccess != int64(len(value.UpstreamTaskIDs)) || value.FailurePolicy != JoinFailGraph {
			return ErrInvalidTaskGraph
		}
	case JoinMinimumSucceeded:
		if value.MinimumSuccess < 1 || value.MinimumSuccess >= int64(len(value.UpstreamTaskIDs)) ||
			value.FailurePolicy != JoinContinueIfThresholdMet {
			return ErrInvalidTaskGraph
		}
	default:
		return ErrInvalidTaskGraph
	}
	return nil
}

func (value TaskGraphPlan) Digest() (string, error) {
	if err := value.Validate(); err != nil {
		return "", err
	}
	return canonical.Digest("agent-platform.task-graph-plan.v1", value)
}

func (value AdmitTaskGraphCommand) Validate() error {
	if value.SchemaRevision != SchemaRevisionV1 || value.Envelope.Validate() != nil ||
		value.Envelope.CommandType != "admit_task_graph" ||
		value.Envelope.Audience != contracts.AudienceControlAPI ||
		value.Envelope.Actor.Kind != contracts.PrincipalWorkload ||
		value.Envelope.Actor.Audience != contracts.AudienceControlAPI ||
		value.ExpectedRunStateGeneration < 1 || value.ExpectedParentStateGeneration < 1 ||
		value.Plan.Validate() != nil || value.Envelope.CorrelationID != value.Plan.RunID ||
		value.Envelope.CausationID != value.Plan.ParentTaskID ||
		value.Envelope.Actor != value.Plan.CreatedBy ||
		value.Envelope.Deadline.After(value.Plan.DeadlineAt) {
		return ErrInvalidTaskGraph
	}
	digest, err := value.Plan.Digest()
	if err != nil || value.Envelope.RequestDigest != digest {
		return ErrInvalidTaskGraph
	}
	return nil
}

func graphDepths(nodes map[string]TaskGraphNode, incoming, outgoing map[string][]string) (map[string]int64, bool) {
	remaining := make(map[string]int, len(nodes))
	depth := make(map[string]int64, len(nodes))
	queue := make([]string, 0, len(nodes))
	for taskID := range nodes {
		remaining[taskID] = len(incoming[taskID])
		if remaining[taskID] == 0 {
			queue = append(queue, taskID)
			depth[taskID] = 1
		}
	}
	visited := 0
	for len(queue) > 0 {
		taskID := queue[0]
		queue = queue[1:]
		visited++
		for _, target := range outgoing[taskID] {
			if candidate := depth[taskID] + 1; candidate > depth[target] {
				depth[target] = candidate
			}
			remaining[target]--
			if remaining[target] == 0 {
				queue = append(queue, target)
			}
		}
	}
	return depth, visited == len(nodes)
}

func addBudget(total *runtimecontract.BudgetLimit, value runtimecontract.BudgetLimit) {
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

func aggregateWithin(total, limit runtimecontract.BudgetLimit) bool {
	return total.MaxModelCalls <= limit.MaxModelCalls &&
		total.MaxInputTokens <= limit.MaxInputTokens &&
		total.MaxOutputTokens <= limit.MaxOutputTokens &&
		total.MaxToolCalls <= limit.MaxToolCalls &&
		total.MaxExternalCostMicroUSD <= limit.MaxExternalCostMicroUSD &&
		total.MaxWallTimeMS <= limit.MaxWallTimeMS &&
		total.MaxIdleTimeMS <= limit.MaxIdleTimeMS &&
		total.MaxTasks <= limit.MaxTasks &&
		total.MaxInvalidOutputRetries <= limit.MaxInvalidOutputRetries &&
		total.MaxInfrastructureRetries <= limit.MaxInfrastructureRetries
}

func validRecordRefs(values []contracts.RecordRef) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value.Validate() != nil {
			return false
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d", value.Owner, value.RecordType, value.RecordID, value.SchemaRevision)
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 200 {
		return false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
