package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"alpheus/agentplatform/inputgateway"
)

type fakeScoutContinuationStore struct {
	candidates []string
	listErr    error
	results    map[string]inputgateway.ScoutContinuation
	errs       map[string]error
	continued  []string
}

func (store *fakeScoutContinuationStore) ListScoutContinuationCandidates(context.Context, int) ([]string, error) {
	return append([]string(nil), store.candidates...), store.listErr
}

func (store *fakeScoutContinuationStore) ContinueScoutParent(_ context.Context, requestID string) (inputgateway.ScoutContinuation, error) {
	store.continued = append(store.continued, requestID)
	if err := store.errs[requestID]; err != nil {
		return inputgateway.ScoutContinuation{}, err
	}
	return store.results[requestID], nil
}

func TestReconcileCortexScoutContinuationsResumesEveryCandidateOnce(t *testing.T) {
	store := &fakeScoutContinuationStore{
		candidates: []string{"scout-request-1", "scout-request-2"},
		results: map[string]inputgateway.ScoutContinuation{
			"scout-request-1": {Status: "ready", RequestID: "scout-request-1", ParentTaskID: "parent-1", ParentSessionID: "desk-1"},
			"scout-request-2": {Status: "ready", RequestID: "scout-request-2", ParentTaskID: "parent-2", ParentSessionID: "desk-2"},
		},
	}
	completed, err := reconcileCortexScoutContinuations(context.Background(), store)
	if err != nil || completed != 2 || !reflect.DeepEqual(store.continued, store.candidates) {
		t.Fatalf("completed=%d err=%v continued=%v", completed, err, store.continued)
	}
}

func TestReconcileCortexScoutContinuationsFailsClosedBeforeLaterCandidate(t *testing.T) {
	store := &fakeScoutContinuationStore{
		candidates: []string{"scout-request-1", "scout-request-2"},
		errs:       map[string]error{"scout-request-1": errors.New("database unavailable")},
		results: map[string]inputgateway.ScoutContinuation{
			"scout-request-2": {Status: "ready", RequestID: "scout-request-2", ParentTaskID: "parent-2", ParentSessionID: "desk-2"},
		},
	}
	completed, err := reconcileCortexScoutContinuations(context.Background(), store)
	if err == nil || completed != 0 || !reflect.DeepEqual(store.continued, []string{"scout-request-1"}) {
		t.Fatalf("completed=%d err=%v continued=%v", completed, err, store.continued)
	}
}

func TestReconcileCortexScoutContinuationsRejectsIncompleteContinuationIdentity(t *testing.T) {
	store := &fakeScoutContinuationStore{
		candidates: []string{"scout-request-1"},
		results: map[string]inputgateway.ScoutContinuation{
			"scout-request-1": {Status: "ready", RequestID: "scout-request-1", ParentTaskID: "parent-1"},
		},
	}
	completed, err := reconcileCortexScoutContinuations(context.Background(), store)
	if err == nil || completed != 0 || !reflect.DeepEqual(store.continued, []string{"scout-request-1"}) {
		t.Fatalf("completed=%d err=%v continued=%v", completed, err, store.continued)
	}
}
