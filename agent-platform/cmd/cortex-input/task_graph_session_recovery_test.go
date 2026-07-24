package main

import (
	"context"
	"testing"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/inputgateway"
	"alpheus/agentplatform/taskgraphcontract"
)

type taskGraphSessionRecoveryProbe struct {
	pending  []inputgateway.PendingTaskGraphNodeSession
	listed   int
	prepared []string
}

func (probe *taskGraphSessionRecoveryProbe) ListPendingTaskGraphNodeSessions(
	context.Context, int,
) ([]inputgateway.PendingTaskGraphNodeSession, error) {
	probe.listed++
	return probe.pending, nil
}

func (probe *taskGraphSessionRecoveryProbe) PrepareTaskGraphNodeSession(
	_ context.Context, graphID string,
	node taskgraphcontract.TaskGraphNode, _ blob.BlobRef,
) error {
	probe.prepared = append(probe.prepared, graphID+":"+node.TaskID)
	return nil
}

func TestRecoverCortexTaskGraphNodeSessionsPreparesProjectionInOrder(
	t *testing.T,
) {
	probe := &taskGraphSessionRecoveryProbe{
		pending: []inputgateway.PendingTaskGraphNodeSession{
			{GraphID: "graph-1", Node: taskgraphcontract.TaskGraphNode{
				TaskID: "branch-1",
			}},
			{GraphID: "graph-1", Node: taskgraphcontract.TaskGraphNode{
				TaskID: "desk-1",
			}},
		},
	}
	result, err := recoverCortexTaskGraphNodeSessions(
		context.Background(), probe, 32,
	)
	if err != nil || probe.listed != 1 ||
		result.Projected != 2 || result.Prepared != 2 ||
		len(probe.prepared) != 2 ||
		probe.prepared[0] != "graph-1:branch-1" ||
		probe.prepared[1] != "graph-1:desk-1" {
		t.Fatalf(
			"result=%+v listed=%d prepared=%v err=%v",
			result, probe.listed, probe.prepared, err,
		)
	}
}
