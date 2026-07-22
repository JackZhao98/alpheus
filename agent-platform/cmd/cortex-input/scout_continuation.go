package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"alpheus/agentplatform/inputgateway"
)

const (
	cortexScoutContinuationInterval = 3 * time.Second
	cortexScoutContinuationBatch    = 8
)

// scoutContinuationStore is intentionally narrower than the main Control
// adapter: reconciliation may only turn an already-completed admitted Scout
// into its exact parent continuation. It cannot create a Scout request, Task,
// Tool intent, or user-facing Artifact.
type scoutContinuationStore interface {
	ListScoutContinuationCandidates(context.Context, int) ([]string, error)
	ContinueScoutParent(context.Context, string) (inputgateway.ScoutContinuation, error)
	ListScoutFailureCandidates(context.Context, int) ([]string, error)
	FailScoutParent(context.Context, string) (inputgateway.ScoutParentFailure, error)
}

func startCortexScoutContinuationRecovery(ctx context.Context, store scoutContinuationStore) {
	if ctx == nil || store == nil {
		return
	}
	go func() {
		for {
			if _, err := reconcileCortexScoutContinuations(ctx, store); err != nil && ctx.Err() == nil {
				log.Printf("Cortex Scout continuation cycle: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(cortexScoutContinuationInterval):
			}
		}
	}()
}

func reconcileCortexScoutContinuations(ctx context.Context, store scoutContinuationStore) (int, error) {
	if ctx == nil || store == nil {
		return 0, fmt.Errorf("Cortex Scout continuation recovery unavailable")
	}
	candidates, err := store.ListScoutContinuationCandidates(ctx, cortexScoutContinuationBatch)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, requestID := range candidates {
		if ctx.Err() != nil {
			return completed, ctx.Err()
		}
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		continuation, err := store.ContinueScoutParent(callCtx, requestID)
		cancel()
		if err != nil {
			return completed, fmt.Errorf("continue %s: %w", requestID, err)
		}
		if continuation.Status != "ready" || continuation.ParentTaskID == "" || continuation.ParentSessionID == "" {
			return completed, fmt.Errorf("continue %s returned an invalid response", requestID)
		}
		completed++
	}
	failures, err := store.ListScoutFailureCandidates(ctx, cortexScoutContinuationBatch)
	if err != nil {
		return completed, err
	}
	for _, requestID := range failures {
		if ctx.Err() != nil {
			return completed, ctx.Err()
		}
		callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		failed, err := store.FailScoutParent(callCtx, requestID)
		cancel()
		if err != nil {
			return completed, fmt.Errorf("fail parent for %s: %w", requestID, err)
		}
		if failed.Status != "failed" || failed.RequestID != requestID || failed.RunID == "" || failed.ParentTaskID == "" || failed.ChildTaskID == "" {
			return completed, fmt.Errorf("fail parent for %s returned an invalid response", requestID)
		}
		completed++
	}
	return completed, nil
}
