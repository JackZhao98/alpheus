package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"alpheus/agentplatform/inputgateway"
)

const cortexTaskGraphJoinInterval = 2 * time.Second

type taskGraphJoinStore interface {
	ReconcileTaskGraphJoins(
		context.Context,
	) (inputgateway.TaskGraphJoinReconciliation, error)
}

func startCortexTaskGraphJoinRecovery(
	ctx context.Context, store taskGraphJoinStore,
) {
	if ctx == nil || store == nil {
		return
	}
	go func() {
		for {
			if _, err := reconcileCortexTaskGraphJoins(
				ctx, store,
			); err != nil && ctx.Err() == nil {
				log.Printf("Cortex TaskGraph Join cycle: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(cortexTaskGraphJoinInterval):
			}
		}
	}()
}

func reconcileCortexTaskGraphJoins(
	ctx context.Context, store taskGraphJoinStore,
) (inputgateway.TaskGraphJoinReconciliation, error) {
	if ctx == nil || store == nil {
		return inputgateway.TaskGraphJoinReconciliation{},
			fmt.Errorf("Cortex TaskGraph Join recovery unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return store.ReconcileTaskGraphJoins(callCtx)
}
