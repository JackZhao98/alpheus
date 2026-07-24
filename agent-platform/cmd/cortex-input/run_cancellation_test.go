package main

import (
	"context"
	"testing"

	"alpheus/agentplatform/inputgateway"
)

type runCancellationStoreProbe struct {
	limit int
}

func (probe *runCancellationStoreProbe) ReconcileRunCancellations(
	_ context.Context,
	limit int,
) (inputgateway.RunCancellationReconciliation, error) {
	probe.limit = limit
	return inputgateway.RunCancellationReconciliation{
		Status: "reconciled", Processed: 2, Canceled: 1, Pending: 1,
	}, nil
}

func TestReconcileCortexRunCancellationsUsesBoundedBatch(t *testing.T) {
	probe := &runCancellationStoreProbe{}
	result, err := reconcileCortexRunCancellations(
		context.Background(), probe)
	if err != nil || probe.limit != cortexRunCancellationLimit ||
		result.Processed != 2 || result.Canceled != 1 ||
		result.Pending != 1 {
		t.Fatalf("probe=%+v result=%+v err=%v", probe, result, err)
	}
}
