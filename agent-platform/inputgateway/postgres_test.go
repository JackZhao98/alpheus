package inputgateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/taskgraphcontract"
)

func TestCortexTraceEventPreservesSafeFailureFields(t *testing.T) {
	var event CortexTraceEvent
	if err := json.Unmarshal([]byte(`{
		"sequence":7,
		"created_at":"2026-07-24T04:00:00Z",
		"stage":"tool_branch_failed",
		"state":"failed",
		"task_id":"task-1",
		"role_id":"market_scout",
		"tool_id":"kernel_equity_historicals",
		"error_code":"kernel_tool_interval_invalid",
		"retryable":false
	}`), &event); err != nil {
		t.Fatal(err)
	}
	if event.ErrorCode != "kernel_tool_interval_invalid" ||
		event.Retryable == nil || *event.Retryable {
		t.Fatalf("safe failure fields lost: %+v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil || !strings.Contains(string(encoded),
		`"retryable":false`) {
		t.Fatalf("non-retryable failure was omitted: %s err=%v",
			encoded, err)
	}
}

func TestDeterministicStageIDIsStableAndScoped(t *testing.T) {
	first := deterministicStageID("cortex-control-1", "request-1")
	if first != deterministicStageID("cortex-control-1", "request-1") || first == deterministicStageID("cortex-control-1", "request-2") {
		t.Fatalf("stage identity is not stable and scoped: %s", first)
	}
	if len(first) != 36 || first[14] != '5' {
		t.Fatalf("not a deterministic version-5 UUID: %s", first)
	}
}

func TestSameStageGrantComparesExpectedSizeValue(t *testing.T) {
	leftSize, rightSize := int64(4), int64(4)
	issued := time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC)
	left := blob.StageGrant{SchemaRevision: 1, StageID: "11111111-1111-4111-8111-111111111111",
		PrincipalID: "cortex-control-1", MediaType: "text/plain; charset=utf-8", MaxBytes: 4,
		ExpectedDigest: "digest", ExpectedSizeBytes: &leftSize, IssuedAt: issued, ExpiresAt: issued.Add(time.Minute)}
	right := left
	right.ExpectedSizeBytes = &rightSize
	if !sameStageGrant(left, right) {
		t.Fatal("equal stage grants with distinct pointer storage did not compare equal")
	}
	rightSize++
	if sameStageGrant(left, right) {
		t.Fatal("different expected sizes compared equal")
	}
}

func TestTaskGraphSessionDocumentsAreNodeScoped(t *testing.T) {
	committedAt := time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC)
	objective := blob.BlobRef{
		SchemaRevision: 1,
		BlobID:         "11111111-1111-4111-8111-111111111111",
		ContentDigest:  strings.Repeat("a", 64),
		MediaType:      "application/json",
		SizeBytes:      10,
		Origin: contracts.RecordRef{
			Owner:          contracts.OwnerAgentControl,
			RecordType:     "task_objective",
			RecordID:       "task-1",
			SchemaRevision: 1,
			RecordDigest:   strings.Repeat("b", 64),
		},
		CommittedAt: committedAt,
	}
	raw := blob.BlobRef{
		SchemaRevision: 1,
		BlobID:         "22222222-2222-4222-8222-222222222222",
		ContentDigest:  strings.Repeat("c", 64),
		MediaType:      "text/plain; charset=utf-8",
		SizeBytes:      10,
		Origin: contracts.RecordRef{
			Owner:          contracts.OwnerAgentControl,
			RecordType:     "input_raw",
			RecordID:       "request-1",
			SchemaRevision: 1,
			RecordDigest:   strings.Repeat("d", 64),
		},
		CommittedAt: committedAt,
	}
	execution, contextValue, err := taskGraphSessionDocuments(
		"graph-1",
		taskgraphcontract.TaskGraphNode{
			TaskID: "task-1", RoleID: "fundamental_scout",
			RoleRevision: 1, Objective: objective,
		},
		raw, "cortex-worker-1",
	)
	if err != nil || execution.GraphID != "graph-1" ||
		execution.TaskID != "task-1" ||
		execution.RoleID != "fundamental_scout" ||
		execution.WorkerPrincipalID != "cortex-worker-1" ||
		contextValue.RequestID != "request-1" ||
		contextValue.RawInput != raw {
		t.Fatalf(
			"TaskGraph Session documents mismatched: execution=%+v context=%+v err=%v",
			execution, contextValue, err,
		)
	}
	raw.Origin.RecordType = "invented"
	if _, _, err := taskGraphSessionDocuments(
		"graph-1",
		taskgraphcontract.TaskGraphNode{
			TaskID: "task-1", RoleID: "fundamental_scout",
			RoleRevision: 1, Objective: objective,
		},
		raw, "cortex-worker-1",
	); err == nil {
		t.Fatal("non-input raw Blob acquired a TaskGraph Session")
	}
}

func TestValidateOperationsHealthEnforcesBoundedConsistentProjection(t *testing.T) {
	now := time.Date(2026, 7, 23, 23, 0, 0, 0, time.UTC)
	value := CortexOperationsHealth{
		GeneratedAt: now.Format(time.RFC3339Nano),
		Status:      "healthy",
		WindowHours: 24,
		ActiveRuns: []CortexOperationsActiveRun{{
			RunID:      "run-1",
			State:      "running",
			UpdatedAt:  now.Add(-time.Second).Format(time.RFC3339Nano),
			DeadlineAt: now.Add(time.Minute).Format(time.RFC3339Nano),
		}},
		RecentFailures: []CortexOperationsFailure{{
			RunID:      "run-0",
			State:      "dead_lettered",
			TerminalAt: now.Add(-time.Minute).Format(time.RFC3339Nano),
			ReasonCode: "runtime_deadline_expired",
		}},
	}
	if err := validateOperationsHealth(value); err != nil {
		t.Fatal(err)
	}
	value.Tools.OverdueUnacknowledged = 1
	if err := validateOperationsHealth(value); err == nil {
		t.Fatal("mismatched Tool risk count was accepted")
	}
	value.Risks.UnacknowledgedToolCalls = 1
	value.ActiveRuns[0].State = "succeeded"
	if err := validateOperationsHealth(value); err == nil {
		t.Fatal("terminal Run was accepted in the active projection")
	}
}

func TestDecodeRunCancellationResultRejectsMismatchedIdentity(t *testing.T) {
	result, err := decodeRunCancellationResult([]byte(`{
		"status":"canceled",
		"run_id":"run-1",
		"run_state":"canceled",
		"request_id":"cancel-1",
		"reason_code":"user_cancel",
		"canceled_at":"2026-07-24T00:00:00Z",
		"terminalized_turns":1,
		"terminalized_attempts":1,
		"closed_sessions":1,
		"terminalized_tasks":1
	}`), "cancel-1")
	if err != nil || result.Status != "canceled" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := decodeRunCancellationResult([]byte(`{
		"status":"canceled",
		"run_state":"canceled",
		"request_id":"cancel-other",
		"reason_code":"user_cancel"
	}`), "cancel-1"); err == nil {
		t.Fatal("mismatched cancellation request identity was accepted")
	}
}

func TestDecodeAgentRoomMutation(t *testing.T) {
	result, err := decodeAgentRoomMutation([]byte(`{
		"status":"recorded",
		"room":{
			"conversation_id":"room-1",
			"conversation_created_at":"2026-07-23T20:00:00Z",
			"mode":"research",
			"title":"Analyze SPX structure",
			"state":"active",
			"generation":1,
			"last_run_id":"run-1",
			"last_run_state":"running",
			"last_activity_at":"2026-07-23T20:00:01Z",
			"updated_at":"2026-07-23T20:00:02Z",
			"message_count":1
		}
	}`), "recorded")
	if err != nil || result.Room.ConversationID != "room-1" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := decodeAgentRoomMutation([]byte(`{
		"status":"recorded",
		"room":{
			"conversation_id":"room-1",
			"conversation_created_at":"2026-07-23T20:00:00Z",
			"mode":"research",
			"title":"Analyze SPX structure",
			"state":"active",
			"generation":1,
			"last_activity_at":"2026-07-23T20:00:01Z",
			"updated_at":"2026-07-23T20:00:02Z",
			"message_count":0
		}
	}`), "recorded"); err == nil {
		t.Fatal("empty persisted Agent Room was accepted")
	}
}

func TestDecodeAgentRoomArchivedMutation(t *testing.T) {
	raw := []byte(`{
		"status":"updated",
		"room":{
			"conversation_id":"room-1",
			"conversation_created_at":"2026-07-24T01:00:00Z",
			"mode":"research",
			"title":"Finished room",
			"state":"archived",
			"generation":3,
			"last_activity_at":"2026-07-24T01:01:00Z",
			"updated_at":"2026-07-24T01:02:00Z",
			"message_count":1
		}
	}`)
	result, err := decodeAgentRoomMutation(raw, "updated")
	if err != nil || result.Room.State != "archived" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
