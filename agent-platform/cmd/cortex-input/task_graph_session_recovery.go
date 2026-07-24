package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/inputgateway"
	"alpheus/agentplatform/taskgraphcontract"
)

const (
	cortexTaskGraphSessionRecoveryInterval = 2 * time.Second
	cortexTaskGraphSessionRecoveryLimit    = 32
)

type taskGraphSessionRecoveryStore interface {
	ListPendingTaskGraphNodeSessions(
		context.Context, int,
	) ([]inputgateway.PendingTaskGraphNodeSession, error)
	PrepareTaskGraphNodeSession(
		context.Context, string,
		taskgraphcontract.TaskGraphNode,
		blob.BlobRef,
	) error
}

type taskGraphSessionRecoveryResult struct {
	Projected int
	Prepared  int
}

func startCortexTaskGraphSessionRecovery(
	ctx context.Context, store taskGraphSessionRecoveryStore,
) {
	if ctx == nil || store == nil {
		return
	}
	go func() {
		for {
			if _, err := recoverCortexTaskGraphNodeSessions(
				ctx, store, cortexTaskGraphSessionRecoveryLimit,
			); err != nil && ctx.Err() == nil {
				log.Printf("Cortex TaskGraph Session recovery cycle: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(cortexTaskGraphSessionRecoveryInterval):
			}
		}
	}()
}

func recoverCortexTaskGraphNodeSessions(
	ctx context.Context,
	store taskGraphSessionRecoveryStore,
	limit int,
) (taskGraphSessionRecoveryResult, error) {
	if ctx == nil || store == nil || limit < 1 || limit > 64 {
		return taskGraphSessionRecoveryResult{},
			fmt.Errorf("TaskGraph Session recovery unavailable")
	}
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pending, err := store.ListPendingTaskGraphNodeSessions(callCtx, limit)
	if err != nil {
		return taskGraphSessionRecoveryResult{}, err
	}
	result := taskGraphSessionRecoveryResult{Projected: len(pending)}
	for _, item := range pending {
		if err := store.PrepareTaskGraphNodeSession(
			callCtx, item.GraphID, item.Node, item.RawInput,
		); err != nil {
			return result, fmt.Errorf(
				"prepare TaskGraph node %s: %w", item.Node.TaskID, err,
			)
		}
		result.Prepared++
	}
	return result, nil
}
