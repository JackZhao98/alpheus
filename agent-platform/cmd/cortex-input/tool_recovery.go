package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"alpheus/agentplatform/inputgateway"
)

const (
	cortexToolRecoveryInterval     = 5 * time.Second
	cortexToolRecoveryLeaseSeconds = 45
	cortexToolRecoveryBatchSize    = 8
)

// webFetchRecoveryStore is deliberately narrower than the normal Control
// adapter.  Recovery can claim a persisted ToolCallID, acknowledge its exact
// Research receipt, or relinquish its own lease.  It cannot authorize a new
// Tool request or alter an Attempt.
type webFetchRecoveryStore interface {
	ClaimWebFetchRecoveries(context.Context, int, int) ([]inputgateway.WebFetchRecoveryClaim, error)
	RecordWebFetchReceipt(context.Context, inputgateway.CortexWebFetchResult) error
	RequeueWebFetchRecovery(context.Context, inputgateway.WebFetchRecoveryClaim, string) (bool, error)
}

type webFetchRecoveryInvoke func(context.Context, string) (inputgateway.CortexWebFetchResult, error)

func startCortexWebFetchRecovery(ctx context.Context, store webFetchRecoveryStore, client *http.Client, researchURL, researchToken string) {
	if ctx == nil || store == nil || client == nil || researchURL == "" || researchToken == "" {
		return
	}
	invoke := func(callCtx context.Context, toolCallID string) (inputgateway.CortexWebFetchResult, error) {
		return invokeResearchWebFetch(callCtx, client, researchURL, researchToken, toolCallID)
	}
	go func() {
		for {
			if _, err := reconcileCortexWebFetches(ctx, store, invoke); err != nil && ctx.Err() == nil {
				log.Printf("Cortex Tool recovery cycle: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(cortexToolRecoveryInterval):
			}
		}
	}()
}

// reconcileCortexWebFetches reuses one immutable ToolCallID.  Research first
// returns any durable receipt already associated with that identity, so a lost
// response is reconciled without an unnecessary second public fetch.
func reconcileCortexWebFetches(ctx context.Context, store webFetchRecoveryStore, invoke webFetchRecoveryInvoke) (int, error) {
	if ctx == nil || store == nil || invoke == nil {
		return 0, fmt.Errorf("Cortex Tool recovery unavailable")
	}
	claims, err := store.ClaimWebFetchRecoveries(ctx, cortexToolRecoveryBatchSize, cortexToolRecoveryLeaseSeconds)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, claim := range claims {
		if ctx.Err() != nil {
			return completed, ctx.Err()
		}
		callCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		result, invokeErr := invoke(callCtx, claim.ToolCallID)
		cancel()
		if invokeErr != nil {
			requeueCortexWebFetchRecovery(ctx, store, claim, "research_tool_unavailable")
			continue
		}
		if err := store.RecordWebFetchReceipt(ctx, result); err != nil {
			requeueCortexWebFetchRecovery(ctx, store, claim, "receipt_acknowledgement_failed")
			continue
		}
		completed++
	}
	return completed, nil
}

func requeueCortexWebFetchRecovery(ctx context.Context, store webFetchRecoveryStore, claim inputgateway.WebFetchRecoveryClaim, reason string) {
	requeued, err := store.RequeueWebFetchRecovery(ctx, claim, reason)
	if err != nil {
		log.Printf("Cortex Tool recovery requeue %s: %v", claim.ToolCallID, err)
		return
	}
	if !requeued {
		// The receipt may have been acknowledged by a concurrent normal request,
		// or a fresh reconciler may own the lease.  Either case is terminal for
		// this process and must not be retried locally.
		log.Printf("Cortex Tool recovery lease lost for %s", claim.ToolCallID)
	}
}
