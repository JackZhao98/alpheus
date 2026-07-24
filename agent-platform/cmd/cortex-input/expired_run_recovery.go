package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"alpheus/agentplatform/inputgateway"
)

const (
	cortexExpiredRunRecoveryInterval = 5 * time.Second
	cortexExpiredRunRecoveryLimit    = 32
)

type expiredRunRecoveryStore interface {
	ReconcileExpiredRuns(
		context.Context, int,
	) (inputgateway.ExpiredRunReconciliation, error)
}

func startCortexExpiredRunRecovery(
	ctx context.Context, store expiredRunRecoveryStore,
) {
	if ctx == nil || store == nil {
		return
	}
	go func() {
		for {
			result, err := reconcileCortexExpiredRuns(ctx, store)
			if err != nil && ctx.Err() == nil {
				log.Printf("Cortex expired Run recovery cycle: %v", err)
			} else if result.RecoveredRuns > 0 {
				log.Printf(
					"Cortex terminal Run recovery: runs=%d expired=%d revoked=%d turns=%d attempts=%d sessions=%d tasks=%d",
					result.RecoveredRuns, result.ExpiredRuns,
					result.RevokedRuns, result.TerminalizedTurns,
					result.TerminalizedAttempts, result.ClosedSessions,
					result.TerminalizedTasks,
				)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(cortexExpiredRunRecoveryInterval):
			}
		}
	}()
}

func reconcileCortexExpiredRuns(
	ctx context.Context, store expiredRunRecoveryStore,
) (inputgateway.ExpiredRunReconciliation, error) {
	if ctx == nil || store == nil {
		return inputgateway.ExpiredRunReconciliation{},
			fmt.Errorf("Cortex expired Run recovery unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return store.ReconcileExpiredRuns(
		callCtx, cortexExpiredRunRecoveryLimit,
	)
}
