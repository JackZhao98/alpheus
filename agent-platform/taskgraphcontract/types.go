// Package taskgraphcontract defines Cortex's immutable, Control-owned task
// graph admission boundary. A model result may motivate a graph, but it cannot
// authorize nodes, tools, budgets, concurrency, or join behavior.
package taskgraphcontract

import (
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/runtimecontract"
)

const SchemaRevisionV1 uint16 = 1

// Absolute ceilings protect every reader even when a stored policy is corrupt.
// Operational ceilings remain the smaller values frozen in AuthorizedLimit.
const (
	AbsoluteMaxTasksV1       = 64
	AbsoluteMaxEdgesV1       = 256
	AbsoluteMaxJoinsV1       = 64
	AbsoluteMaxParallelismV1 = 16
	AbsoluteMaxDepthV1       = 8
	AbsoluteMaxRoundsV1      = 8
)

const DecisionDeskRole = "decision_desk"

type JoinPolicy string

const (
	JoinAllRequired      JoinPolicy = "all_required"
	JoinMinimumSucceeded JoinPolicy = "minimum_succeeded"
)

type JoinFailurePolicy string

const (
	JoinFailGraph              JoinFailurePolicy = "fail_graph"
	JoinContinueIfThresholdMet JoinFailurePolicy = "continue_if_threshold_met"
)

// ToolGrantSnapshot binds a node to one installed, versioned, read-only Tool.
// It is an exact admission snapshot, not a request for a new grant.
type ToolGrantSnapshot struct {
	ToolID       string `json:"tool_id"`
	ToolRevision uint16 `json:"tool_revision"`
	Effect       string `json:"effect"`
}

type TaskGraphNode struct {
	TaskID             string                      `json:"task_id"`
	RoleID             string                      `json:"role_id"`
	RoleRevision       uint16                      `json:"role_revision"`
	Depth              int64                       `json:"depth"`
	Objective          blob.BlobRef                `json:"objective"`
	InputRefs          []contracts.RecordRef       `json:"input_refs"`
	OutputContractName string                      `json:"output_contract_name"`
	OutputContract     contracts.RevisionRef       `json:"output_contract"`
	ToolGrants         []ToolGrantSnapshot         `json:"tool_grants"`
	Limit              runtimecontract.BudgetLimit `json:"limit"`
	DeadlineAt         time.Time                   `json:"deadline_at"`
}

// TaskGraphEdge is pure topology. Success thresholds and terminal failure
// behavior are defined once, at the downstream Join barrier.
type TaskGraphEdge struct {
	FromTaskID string `json:"from_task_id"`
	ToTaskID   string `json:"to_task_id"`
}

type TaskGraphJoin struct {
	JoinID           string            `json:"join_id"`
	DownstreamTaskID string            `json:"downstream_task_id"`
	UpstreamTaskIDs  []string          `json:"upstream_task_ids"`
	Policy           JoinPolicy        `json:"policy"`
	MinimumSuccess   int64             `json:"minimum_success"`
	FailurePolicy    JoinFailurePolicy `json:"failure_policy"`
	DeadlineAt       time.Time         `json:"deadline_at"`
}

// TaskGraphPlan is the immutable graph accepted by deterministic Control.
// SourceResult preserves the exact untrusted model output that motivated it.
type TaskGraphPlan struct {
	SchemaRevision  uint16                      `json:"schema_revision"`
	GraphID         string                      `json:"graph_id"`
	RunID           string                      `json:"run_id"`
	ParentTaskID    string                      `json:"parent_task_id"`
	SourceResult    contracts.RecordRef         `json:"source_result"`
	RuntimePolicy   contracts.RevisionRef       `json:"runtime_policy"`
	Round           int64                       `json:"round"`
	MaxRounds       int64                       `json:"max_rounds"`
	AuthorizedLimit runtimecontract.BudgetLimit `json:"authorized_limit"`
	Nodes           []TaskGraphNode             `json:"nodes"`
	Edges           []TaskGraphEdge             `json:"edges"`
	Joins           []TaskGraphJoin             `json:"joins"`
	CreatedBy       contracts.AuditActor        `json:"created_by"`
	CreatedAt       time.Time                   `json:"created_at"`
	DeadlineAt      time.Time                   `json:"deadline_at"`
}

// AdmitTaskGraphCommand is Control-to-Control: Workers and model output cannot
// write a graph directly. RequestDigest must equal the canonical Plan digest.
type AdmitTaskGraphCommand struct {
	SchemaRevision                uint16                    `json:"schema_revision"`
	Envelope                      contracts.CommandEnvelope `json:"envelope"`
	ExpectedRunStateGeneration    int64                     `json:"expected_run_state_generation"`
	ExpectedParentStateGeneration int64                     `json:"expected_parent_state_generation"`
	Plan                          TaskGraphPlan             `json:"plan"`
}
