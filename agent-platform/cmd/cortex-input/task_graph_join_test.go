package main

import (
	"context"
	"testing"

	"alpheus/agentplatform/inputgateway"
)

type taskGraphJoinStoreProbe struct {
	calls int
	value inputgateway.TaskGraphJoinReconciliation
}

func (probe *taskGraphJoinStoreProbe) ReconcileTaskGraphJoins(
	context.Context,
) (inputgateway.TaskGraphJoinReconciliation, error) {
	probe.calls++
	return probe.value, nil
}

func TestReconcileCortexTaskGraphJoinsUsesOneBoundedControlCall(
	t *testing.T,
) {
	probe := &taskGraphJoinStoreProbe{
		value: inputgateway.TaskGraphJoinReconciliation{
			Status: "reconciled", ResolvedJoins: 2,
			ReadyJoins: 1, FailedJoins: 1, CompletedGraphs: 1,
		},
	}
	result, err := reconcileCortexTaskGraphJoins(
		context.Background(), probe,
	)
	if err != nil || probe.calls != 1 || result.ResolvedJoins != 2 ||
		result.ReadyJoins != 1 || result.FailedJoins != 1 ||
		result.CompletedGraphs != 1 {
		t.Fatalf("TaskGraph Join cycle=%+v calls=%d err=%v",
			result, probe.calls, err)
	}
}
