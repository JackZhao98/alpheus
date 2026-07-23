package main

import (
	"context"
	"testing"

	"alpheus/agentplatform/inputgateway"
)

type expiredRunRecoveryStoreProbe struct {
	calls int
	limit int
	value inputgateway.ExpiredRunReconciliation
}

func (probe *expiredRunRecoveryStoreProbe) ReconcileExpiredRuns(
	_ context.Context, limit int,
) (inputgateway.ExpiredRunReconciliation, error) {
	probe.calls++
	probe.limit = limit
	return probe.value, nil
}

func TestReconcileCortexExpiredRunsUsesOneBoundedControlCall(
	t *testing.T,
) {
	probe := &expiredRunRecoveryStoreProbe{
		value: inputgateway.ExpiredRunReconciliation{
			Status:               "reconciled",
			RecoveredRuns:        2,
			TerminalizedTurns:    1,
			TerminalizedAttempts: 1,
			ClosedSessions:       2,
			TerminalizedTasks:    4,
		},
	}
	result, err := reconcileCortexExpiredRuns(
		context.Background(), probe,
	)
	if err != nil || probe.calls != 1 ||
		probe.limit != cortexExpiredRunRecoveryLimit ||
		result.RecoveredRuns != 2 || result.TerminalizedTasks != 4 {
		t.Fatalf("expired Run cycle=%+v calls=%d limit=%d err=%v",
			result, probe.calls, probe.limit, err)
	}
}
