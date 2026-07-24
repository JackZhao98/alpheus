package main

import (
	"context"
	"log"
	"time"

	"alpheus/agentplatform/inputgateway"
)

const (
	cortexRunCancellationInterval = 2 * time.Second
	cortexRunCancellationLimit    = 32
)

type runCancellationStore interface {
	ReconcileRunCancellations(
		context.Context,
		int,
	) (inputgateway.RunCancellationReconciliation, error)
}

func startCortexRunCancellationRecovery(
	ctx context.Context,
	store runCancellationStore,
) {
	go func() {
		for {
			callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			result, err := reconcileCortexRunCancellations(callCtx, store)
			cancel()
			if err != nil {
				log.Printf("Cortex Run cancellation recovery: %v", err)
			} else if result.Processed > 0 {
				log.Printf(
					"Cortex Run cancellation recovery processed=%d canceled=%d pending=%d",
					result.Processed, result.Canceled, result.Pending,
				)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(cortexRunCancellationInterval):
			}
		}
	}()
}

func reconcileCortexRunCancellations(
	ctx context.Context,
	store runCancellationStore,
) (inputgateway.RunCancellationReconciliation, error) {
	return store.ReconcileRunCancellations(
		ctx, cortexRunCancellationLimit)
}
