package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"alpheus/agentplatform/inputgateway"
)

type fakeWebFetchRecoveryStore struct {
	claims     []inputgateway.WebFetchRecoveryClaim
	claimErr   error
	recorded   []inputgateway.CortexWebFetchResult
	recordErr  error
	requeued   []string
	requeueErr error
}

func (store *fakeWebFetchRecoveryStore) ClaimWebFetchRecoveries(context.Context, int, int) ([]inputgateway.WebFetchRecoveryClaim, error) {
	return append([]inputgateway.WebFetchRecoveryClaim(nil), store.claims...), store.claimErr
}

func (store *fakeWebFetchRecoveryStore) RecordWebFetchReceipt(_ context.Context, result inputgateway.CortexWebFetchResult) error {
	store.recorded = append(store.recorded, result)
	return store.recordErr
}

func (store *fakeWebFetchRecoveryStore) RequeueWebFetchRecovery(_ context.Context, claim inputgateway.WebFetchRecoveryClaim, reason string) (bool, error) {
	store.requeued = append(store.requeued, claim.ToolCallID+":"+reason)
	return true, store.requeueErr
}

func TestReconcileCortexWebFetchesAcknowledgesOnlyClaimedIntent(t *testing.T) {
	store := &fakeWebFetchRecoveryStore{claims: []inputgateway.WebFetchRecoveryClaim{{
		ToolCallID: "tool-1", LeaseGeneration: 2, LeaseToken: "lease-token", LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}}}
	called := ""
	completed, err := reconcileCortexWebFetches(context.Background(), store, func(_ context.Context, toolCallID string) (inputgateway.CortexWebFetchResult, error) {
		called = toolCallID
		return inputgateway.CortexWebFetchResult{}, nil
	})
	if err != nil || completed != 1 || called != "tool-1" || len(store.recorded) != 1 || len(store.requeued) != 0 {
		t.Fatalf("completed=%d err=%v called=%q recorded=%d requeued=%v", completed, err, called, len(store.recorded), store.requeued)
	}
}

func TestReconcileCortexWebFetchesRequeuesTransientResearchFailure(t *testing.T) {
	store := &fakeWebFetchRecoveryStore{claims: []inputgateway.WebFetchRecoveryClaim{{
		ToolCallID: "tool-1", LeaseGeneration: 2, LeaseToken: "lease-token", LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}}}
	completed, err := reconcileCortexWebFetches(context.Background(), store, func(context.Context, string) (inputgateway.CortexWebFetchResult, error) {
		return inputgateway.CortexWebFetchResult{}, errors.New("network unavailable")
	})
	if err != nil || completed != 0 || len(store.recorded) != 0 || len(store.requeued) != 1 || store.requeued[0] != "tool-1:research_tool_unavailable" {
		t.Fatalf("completed=%d err=%v recorded=%d requeued=%v", completed, err, len(store.recorded), store.requeued)
	}
}

func TestReconcileCortexWebFetchesRequeuesReceiptAcknowledgementFailure(t *testing.T) {
	store := &fakeWebFetchRecoveryStore{claims: []inputgateway.WebFetchRecoveryClaim{{
		ToolCallID: "tool-1", LeaseGeneration: 2, LeaseToken: "lease-token", LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}}, recordErr: errors.New("database unavailable")}
	completed, err := reconcileCortexWebFetches(context.Background(), store, func(context.Context, string) (inputgateway.CortexWebFetchResult, error) {
		return inputgateway.CortexWebFetchResult{}, nil
	})
	if err != nil || completed != 0 || len(store.recorded) != 1 || len(store.requeued) != 1 || store.requeued[0] != "tool-1:receipt_acknowledgement_failed" {
		t.Fatalf("completed=%d err=%v recorded=%d requeued=%v", completed, err, len(store.recorded), store.requeued)
	}
}
