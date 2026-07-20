// Command ap1_attempt_terminalization_barrier proves that AP1 Attempt
// terminalization linearizes through PostgreSQL. It creates disposable
// non-effect runtime fixtures and invokes no model, Tool, Kernel, Provider,
// operation, broker, GRACE, or Delegation capability.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}$`)

type config struct {
	host      string
	port      int
	database  string
	user      string
	password  string
	principal string
	requests  int
}

type fixture struct {
	runID        string
	taskID       string
	sessionID    string
	attemptID    string
	runLedgerID  string
	taskLedgerID string
	leaseToken   string
	leaseExpires time.Time
	context      json.RawMessage
	outputDigest string
	artifactType string
	stateGen     int64
	leaseGen     int64
	resultID     string
	resultDigest string
	resultOutput json.RawMessage
}

type commandResponse struct {
	Status         string `json:"status"`
	ReasonCode     string `json:"reason_code"`
	ArtifactID     string `json:"artifact_id"`
	ArtifactDigest string `json:"artifact_digest"`
	ManifestDigest string `json:"manifest_digest"`
	ResultID       string `json:"result_id"`
	ResultDigest   string `json:"result_digest"`
	TurnStateGen   int64  `json:"turn_state_generation"`
}

type callResult struct {
	function string
	response commandResponse
	err      error
}

type barrierSummary struct {
	committed int
	denied    int
	winner    string
}

type runtimeSnapshot struct {
	runState, taskState, sessionState, attemptState    string
	runGeneration, taskGeneration                      int64
	sessionGeneration, attemptGeneration               int64
	budgetGeneration, leafBudgetGeneration             int64
	activeTasks, invalidRetries, infrastructureRetries int64
	slotHeld                                           bool
	artifacts, artifactBindings, releaseEvents         int64
	runtimeEvents                                      int64
}

func main() {
	var cfg config
	flag.StringVar(&cfg.host, "host", "127.0.0.1", "PostgreSQL host")
	flag.IntVar(&cfg.port, "port", 5432, "PostgreSQL port")
	flag.StringVar(&cfg.database, "database", "probe", "PostgreSQL database")
	flag.StringVar(&cfg.user, "user", "postgres", "bootstrap database user")
	flag.StringVar(&cfg.password, "password", "", "bootstrap database password")
	flag.StringVar(&cfg.principal, "worker", "worker-1", "authenticated Worker principal")
	flag.IntVar(&cfg.requests, "requests", 20, "simultaneous mixed terminalization commands")
	flag.Parse()
	if flag.NArg() != 0 || cfg.port < 1 || cfg.port > 65535 ||
		cfg.requests < 2 || cfg.requests > 100 || cfg.requests%2 != 0 ||
		!safeIdentifier.MatchString(cfg.principal) {
		fatal(errors.New("invalid barrier arguments"))
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	result, err := run(ctx, cfg)
	if err != nil {
		fatal(err)
	}
	result["wall_ms"] = time.Since(started).Milliseconds()
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fatal(fmt.Errorf("encode result: %w", err))
	}
}

func run(ctx context.Context, cfg config) (map[string]any, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=disable connect_timeout=5",
		cfg.host, cfg.port, cfg.database, cfg.user, cfg.password,
	)
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open admin database: %w", err)
	}
	defer admin.Close()
	admin.SetMaxOpenConns(8)
	if err := admin.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	raceFixture, err := prepareResultFixture(ctx, admin, dsn, cfg, "race", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	race, err := proveMixedRace(ctx, admin, dsn, cfg, raceFixture)
	if err != nil {
		return nil, err
	}

	failStallFixture, err := seedAttempt(ctx, admin, cfg, "failstall", 2500*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if err := proveFailBudgetWaitFence(ctx, admin, dsn, cfg, failStallFixture); err != nil {
		return nil, err
	}

	commitStallFixture, err := prepareResultFixture(ctx, admin, dsn, cfg, "commitstall", 5*time.Minute)
	if err != nil {
		return nil, err
	}
	if err := proveCommitResultWaitFence(ctx, admin, dsn, cfg, commitStallFixture); err != nil {
		return nil, err
	}

	return map[string]any{
		"status":   "PASS",
		"probe":    "ap1-attempt-terminalization-barrier",
		"requests": cfg.requests,
		"mixed_race": map[string]any{
			"committed": race.committed,
			"denied":    race.denied,
			"winner":    race.winner,
		},
		"fail_budget_wait_fence":   "denied_after_lease_expiry",
		"commit_result_wait_fence": "denied_after_command_deadline",
		"effect_ceiling":           "none",
	}, nil
}

func proveMixedRace(
	ctx context.Context,
	admin *sql.DB,
	dsn string,
	cfg config,
	f fixture,
) (barrierSummary, error) {
	before, err := snapshot(ctx, admin, f)
	if err != nil {
		return barrierSummary{}, err
	}
	deadline := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
	functions := make([]string, cfg.requests)
	bodies := make([]string, cfg.requests)
	prefix := "termbar-race-" + shortToken()
	for i := range bodies {
		if i%2 == 0 {
			functions[i] = "commit_attempt"
			body := map[string]any{
				"schema_revision":                   1,
				"envelope":                          envelope(cfg.principal, "commit_attempt", prefix, i, deadline),
				"attempt_id":                        f.attemptID,
				"expected_attempt_state_generation": f.stateGen,
				"lease_generation":                  f.leaseGen,
				"lease_token":                       f.leaseToken,
				"result": map[string]any{
					"owner": "agent_control", "record_type": "model_call_result",
					"record_id": f.resultID, "schema_revision": 1,
					"record_digest": f.resultDigest,
				},
				"artifact": map[string]any{
					"artifact_type": f.artifactType, "output_contract_digest": f.outputDigest,
					"effect_class": "none",
					"sections": []any{map[string]any{
						"name": "model_output", "required": true, "content": f.resultOutput,
					}},
				},
			}
			bodies[i], err = marshalBody(body)
		} else {
			functions[i] = "fail_attempt"
			body := map[string]any{
				"schema_revision":                   1,
				"envelope":                          envelope(cfg.principal, "fail_attempt", prefix, i, deadline),
				"attempt_id":                        f.attemptID,
				"expected_attempt_state_generation": f.stateGen,
				"lease_generation":                  f.leaseGen,
				"lease_token":                       f.leaseToken,
				"retry_class":                       "none",
				"failure": map[string]any{
					"code": "terminal_race_failure", "message": "terminal race failure", "retryable": false,
				},
			}
			bodies[i], err = marshalBody(body)
		}
		if err != nil {
			return barrierSummary{}, fmt.Errorf("marshal race command %d: %w", i, err)
		}
	}

	summary, err := runMixedBarrier(ctx, dsn, cfg, functions, bodies)
	if err != nil {
		return summary, err
	}
	if summary.committed != 1 || summary.denied != cfg.requests-1 {
		return summary, fmt.Errorf("mixed terminal race split: committed=%d denied=%d", summary.committed, summary.denied)
	}
	after, err := snapshot(ctx, admin, f)
	if err != nil {
		return summary, err
	}
	if after.attemptGeneration != before.attemptGeneration+1 ||
		after.attemptState != map[string]string{"commit_attempt": "result_committed", "fail_attempt": "failed"}[summary.winner] ||
		after.sessionState != "closed" || after.sessionGeneration != before.sessionGeneration+1 ||
		after.runGeneration != before.runGeneration+1 || after.taskState == "running" ||
		after.slotHeld || after.activeTasks != before.activeTasks-1 ||
		after.budgetGeneration != before.budgetGeneration+1 ||
		after.leafBudgetGeneration != before.leafBudgetGeneration ||
		after.invalidRetries != before.invalidRetries ||
		after.infrastructureRetries != before.infrastructureRetries ||
		after.releaseEvents != before.releaseEvents+1 {
		return summary, fmt.Errorf("mixed terminal race mutated runtime more than once: before=%+v after=%+v", before, after)
	}
	if summary.winner == "commit_attempt" {
		if after.taskState != "succeeded" || after.taskGeneration != before.taskGeneration+2 ||
			after.runState != "succeeded" || after.artifacts != before.artifacts+1 ||
			after.artifactBindings != before.artifactBindings+1 ||
			after.runtimeEvents != before.runtimeEvents+5 {
			return summary, fmt.Errorf("commit winner terminal state mismatch: before=%+v after=%+v", before, after)
		}
	} else if summary.winner == "fail_attempt" {
		if after.taskState != "failed" || after.taskGeneration != before.taskGeneration+1 ||
			after.runState != "failed" || after.artifacts != before.artifacts ||
			after.artifactBindings != before.artifactBindings ||
			after.runtimeEvents != before.runtimeEvents+4 {
			return summary, fmt.Errorf("failure winner terminal state mismatch: before=%+v after=%+v", before, after)
		}
	} else {
		return summary, fmt.Errorf("unknown race winner %q", summary.winner)
	}
	var commandCount, processing int
	if err := admin.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE state = 'processing')
		  FROM agent_control.runtime_command
		 WHERE command_id LIKE $1
	`, prefix+"-command-%").Scan(&commandCount, &processing); err != nil {
		return summary, fmt.Errorf("verify mixed race commands: %w", err)
	}
	if commandCount != cfg.requests || processing != 0 {
		return summary, fmt.Errorf("mixed race command durability count=%d processing=%d", commandCount, processing)
	}
	return summary, nil
}

func proveFailBudgetWaitFence(
	ctx context.Context,
	admin *sql.DB,
	dsn string,
	cfg config,
	f fixture,
) error {
	before, err := snapshot(ctx, admin, f)
	if err != nil {
		return err
	}
	blocker, err := admin.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin failure budget blocker: %w", err)
	}
	blockerOpen := true
	defer func() {
		if blockerOpen {
			_ = blocker.Rollback()
		}
	}()
	if _, err := blocker.ExecContext(ctx, `
		SELECT 1 FROM agent_control.runtime_budget_ledger
		 WHERE ledger_id = $1 FOR UPDATE
	`, f.runLedgerID); err != nil {
		return fmt.Errorf("lock failure budget: %w", err)
	}
	prefix := "termbar-failstall-" + shortToken()
	body, err := marshalBody(map[string]any{
		"schema_revision":                   1,
		"envelope":                          envelope(cfg.principal, "fail_attempt", prefix, 0, time.Now().UTC().Add(20*time.Second).Format(time.RFC3339Nano)),
		"attempt_id":                        f.attemptID,
		"expected_attempt_state_generation": f.stateGen,
		"lease_generation":                  f.leaseGen,
		"lease_token":                       f.leaseToken,
		"retry_class":                       "infrastructure",
		"failure": map[string]any{
			"code": "budget_wait_failure", "message": "budget wait failure", "retryable": true,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal failure wait command: %w", err)
	}
	resultCh := make(chan callResult, 1)
	go func() {
		response, callErr := callWorker(ctx, dsn, cfg.principal, "fail_attempt", body)
		resultCh <- callResult{function: "fail_attempt", response: response, err: callErr}
	}()
	if err := observeLockWait(ctx, admin, "fail_attempt", 2*time.Second); err != nil {
		return err
	}
	if err := waitUntil(ctx, f.leaseExpires.Add(150*time.Millisecond)); err != nil {
		return err
	}
	if err := blocker.Rollback(); err != nil {
		return fmt.Errorf("release failure budget blocker: %w", err)
	}
	blockerOpen = false
	result, err := receive(ctx, resultCh)
	if err != nil {
		return fmt.Errorf("budget-stalled fail_attempt: %w", err)
	}
	if result.response.Status != "denied" || result.response.ReasonCode != "fail_fence_expired" {
		return fmt.Errorf("budget-stalled fail_attempt status=%s reason=%s", result.response.Status, result.response.ReasonCode)
	}
	after, err := snapshot(ctx, admin, f)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("budget-stalled fail_attempt mutated runtime: before=%+v after=%+v", before, after)
	}
	return verifyDeniedCommand(ctx, admin, prefix+"-command-00", "fail_fence_expired")
}

func proveCommitResultWaitFence(
	ctx context.Context,
	admin *sql.DB,
	dsn string,
	cfg config,
	f fixture,
) error {
	before, err := snapshot(ctx, admin, f)
	if err != nil {
		return err
	}
	blocker, err := admin.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin commit Result blocker: %w", err)
	}
	blockerOpen := true
	defer func() {
		if blockerOpen {
			_ = blocker.Rollback()
		}
	}()
	if _, err := blocker.ExecContext(ctx, `
		SELECT 1 FROM agent_control.runtime_model_call_result
		 WHERE result_id = $1 FOR UPDATE
	`, f.resultID); err != nil {
		return fmt.Errorf("lock commit Result: %w", err)
	}
	commandDeadline := time.Now().UTC().Add(1200 * time.Millisecond)
	prefix := "termbar-commitstall-" + shortToken()
	body, err := marshalBody(map[string]any{
		"schema_revision":                   1,
		"envelope":                          envelope(cfg.principal, "commit_attempt", prefix, 0, commandDeadline.Format(time.RFC3339Nano)),
		"attempt_id":                        f.attemptID,
		"expected_attempt_state_generation": f.stateGen,
		"lease_generation":                  f.leaseGen,
		"lease_token":                       f.leaseToken,
		"result": map[string]any{
			"owner": "agent_control", "record_type": "model_call_result",
			"record_id": f.resultID, "schema_revision": 1, "record_digest": f.resultDigest,
		},
		"artifact": map[string]any{
			"artifact_type": f.artifactType, "output_contract_digest": f.outputDigest,
			"effect_class": "none",
			"sections": []any{map[string]any{
				"name": "model_output", "required": true, "content": f.resultOutput,
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("marshal commit wait command: %w", err)
	}
	resultCh := make(chan callResult, 1)
	go func() {
		response, callErr := callWorker(ctx, dsn, cfg.principal, "commit_attempt", body)
		resultCh <- callResult{function: "commit_attempt", response: response, err: callErr}
	}()
	if err := observeLockWait(ctx, admin, "commit_attempt", 2*time.Second); err != nil {
		return err
	}
	if err := waitUntil(ctx, commandDeadline.Add(150*time.Millisecond)); err != nil {
		return err
	}
	if err := blocker.Rollback(); err != nil {
		return fmt.Errorf("release commit Result blocker: %w", err)
	}
	blockerOpen = false
	result, err := receive(ctx, resultCh)
	if err != nil {
		return fmt.Errorf("Result-stalled commit_attempt: %w", err)
	}
	if result.response.Status != "denied" || result.response.ReasonCode != "commit_fence_expired" {
		return fmt.Errorf("Result-stalled commit_attempt status=%s reason=%s", result.response.Status, result.response.ReasonCode)
	}
	after, err := snapshot(ctx, admin, f)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("Result-stalled commit_attempt mutated runtime: before=%+v after=%+v", before, after)
	}
	return verifyDeniedCommand(ctx, admin, prefix+"-command-00", "commit_fence_expired")
}

func prepareResultFixture(
	ctx context.Context,
	admin *sql.DB,
	dsn string,
	cfg config,
	label string,
	lease time.Duration,
) (fixture, error) {
	f, err := seedAttempt(ctx, admin, cfg, label, lease)
	if err != nil {
		return fixture{}, err
	}
	deadline := time.Now().UTC().Add(2 * time.Minute).Format(time.RFC3339Nano)
	identity := "termbar-" + label + "-" + shortToken()
	turnID := identity + "-turn"
	callID := identity + "-call"
	requestDigest := digest(identity + "-request")
	dispatchBody, err := marshalBody(map[string]any{
		"schema_revision":                   1,
		"envelope":                          envelope(cfg.principal, "dispatch_model_call", identity+"-dispatch", 0, deadline),
		"attempt_id":                        f.attemptID,
		"expected_attempt_state_generation": f.stateGen,
		"lease_generation":                  f.leaseGen,
		"lease_token":                       f.leaseToken,
		"turn_id":                           turnID,
		"manifest": map[string]any{
			"call_id": callID, "idempotency_key": identity + "-provider-idem",
			"provider": "probe-provider", "model": "probe-model",
			"prompt_digest": digest(identity + "-prompt"), "context_manifest": f.context,
			"output_contract_digest": f.outputDigest, "request_digest": requestDigest,
			"max_output_tokens": 20, "reserved_input_tokens": 10,
			"reserved_external_cost_micro_usd": 30, "timeout_ms": 4000,
			"temperature_micros": 0,
		},
	})
	if err != nil {
		return fixture{}, fmt.Errorf("marshal fixture dispatch: %w", err)
	}
	dispatch, err := callWorker(ctx, dsn, cfg.principal, "dispatch_model_call", dispatchBody)
	if err != nil {
		return fixture{}, fmt.Errorf("dispatch fixture model call: %w", err)
	}
	if dispatch.Status != "committed" || dispatch.ManifestDigest == "" || dispatch.TurnStateGen != 2 {
		return fixture{}, fmt.Errorf("fixture dispatch status=%s reason=%s generation=%d", dispatch.Status, dispatch.ReasonCode, dispatch.TurnStateGen)
	}
	output, err := seedOutputBlob(ctx, admin, cfg.principal, identity, callID, dispatch.ManifestDigest)
	if err != nil {
		return fixture{}, err
	}
	resolveBody, err := marshalBody(map[string]any{
		"schema_revision":                   1,
		"envelope":                          envelope(cfg.principal, "resolve_model_call", identity+"-resolve", 0, deadline),
		"attempt_id":                        f.attemptID,
		"expected_attempt_state_generation": f.stateGen,
		"lease_generation":                  f.leaseGen,
		"lease_token":                       f.leaseToken,
		"turn_id":                           turnID,
		"expected_turn_state_generation":    dispatch.TurnStateGen,
		"outcome":                           "result_committed",
		"result": map[string]any{
			"call_id": callID, "request_digest": requestDigest,
			"provider_request_id": identity + "-provider-request",
			"output":              output, "input_tokens": 3, "output_tokens": 5,
			"external_cost_micro_usd": 7, "wall_time_ms": 11, "finish_reason": "stop",
		},
	})
	if err != nil {
		return fixture{}, fmt.Errorf("marshal fixture resolution: %w", err)
	}
	resolved, err := callWorker(ctx, dsn, cfg.principal, "resolve_model_call", resolveBody)
	if err != nil {
		return fixture{}, fmt.Errorf("resolve fixture model call: %w", err)
	}
	if resolved.Status != "committed" || resolved.ResultID == "" || resolved.ResultDigest == "" {
		return fixture{}, fmt.Errorf("fixture resolution status=%s reason=%s", resolved.Status, resolved.ReasonCode)
	}
	f.resultID = resolved.ResultID
	f.resultDigest = resolved.ResultDigest
	f.resultOutput = append(json.RawMessage(nil), output...)
	return f, nil
}

func seedAttempt(ctx context.Context, admin *sql.DB, cfg config, label string, lease time.Duration) (fixture, error) {
	suffix := "tb-" + label + "-" + shortToken()
	f := fixture{
		runID: "run-" + suffix, taskID: "task-" + suffix,
		sessionID: "session-" + suffix, attemptID: "attempt-" + suffix,
		runLedgerID: "run-ledger-" + suffix, taskLedgerID: "task-ledger-" + suffix,
		leaseToken: uuid(), stateGen: 2, leaseGen: 1,
	}
	occurrenceID := "occurrence-" + suffix
	now := time.Now().UTC().Truncate(time.Microsecond)
	f.leaseExpires = now.Add(lease)
	tx, err := admin.BeginTx(ctx, nil)
	if err != nil {
		return fixture{}, fmt.Errorf("begin fixture %s: %w", label, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SET CONSTRAINTS ALL DEFERRED`); err != nil {
		return fixture{}, fmt.Errorf("defer fixture constraints: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.trigger_occurrence
		SELECT (jsonb_populate_record(NULL::agent_control.trigger_occurrence,
		       to_jsonb(source_row) || jsonb_build_object(
		         'occurrence_id', $1::text, 'record_digest', $2::text,
		         'occurrence_key', $3::text, 'occurred_at', $4::timestamptz - interval '4 seconds',
		         'observed_at', $4::timestamptz - interval '3 seconds',
		         'committed_at', $4::timestamptz - interval '2 seconds'))).*
		  FROM agent_control.trigger_occurrence AS source_row
		 WHERE source_row.occurrence_id = 'occurrence-command-1'
	`, occurrenceID, digest("occurrence:"+suffix), "occurrence-key-"+suffix, now); err != nil {
		return fixture{}, fmt.Errorf("seed occurrence: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.runtime_run
		SELECT (jsonb_populate_record(NULL::agent_control.runtime_run,
		       to_jsonb(source_row) || jsonb_build_object(
		         'run_id', $1::text, 'occurrence_id', occurrence.occurrence_id,
		         'occurrence_digest', occurrence.record_digest,
		         'origin_occurred_at', occurrence.occurred_at,
		         'origin_observed_at', occurrence.observed_at,
		         'origin_committed_at', occurrence.committed_at,
		         'budget_ledger_id', $2::text, 'root_task_id', $3::text,
		         'state', 'queued', 'state_generation', 1,
		         'superseded_by', NULL, 'failure', NULL,
		         'created_at', $4::timestamptz, 'updated_at', $4::timestamptz,
		         'deadline_at', $4::timestamptz + interval '15 minutes', 'terminal_at', NULL))).*
		  FROM agent_control.runtime_run AS source_row
		  JOIN agent_control.trigger_occurrence AS occurrence ON occurrence.occurrence_id = $5
		 WHERE source_row.run_id = 'run-command-1'
	`, f.runID, f.runLedgerID, f.taskID, now, occurrenceID); err != nil {
		return fixture{}, fmt.Errorf("seed Run: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.runtime_budget_ledger
		SELECT (jsonb_populate_record(NULL::agent_control.runtime_budget_ledger,
		       to_jsonb(source_row) || jsonb_build_object(
		         'ledger_id', $1::text, 'scope_id', $2::text, 'parent_ledger_id', NULL,
		         'consumed_model_calls', 0, 'consumed_input_tokens', 0,
		         'consumed_output_tokens', 0, 'consumed_tool_calls', 0,
		         'consumed_external_cost_micro_usd', 0, 'consumed_wall_time_ms', 0,
		         'consumed_tasks', 1, 'consumed_active_tasks', 1,
		         'consumed_invalid_output_retries', 0, 'consumed_infrastructure_retries', 0,
		         'reserved_model_calls', 0, 'reserved_input_tokens', 0,
		         'reserved_output_tokens', 0, 'reserved_tool_calls', 0,
		         'reserved_external_cost_micro_usd', 0, 'reserved_wall_time_ms', 0,
		         'reserved_tasks', 0, 'reserved_active_tasks', 0,
		         'reserved_invalid_output_retries', 0, 'reserved_infrastructure_retries', 0,
		         'generation', 1, 'state', 'open', 'updated_at', $3::timestamptz))).*
		  FROM agent_control.runtime_budget_ledger AS source_row
		 WHERE source_row.ledger_id = 'run-ledger-command-1'
	`, f.runLedgerID, f.runID, now); err != nil {
		return fixture{}, fmt.Errorf("seed Run ledger: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.runtime_budget_ledger
		SELECT (jsonb_populate_record(NULL::agent_control.runtime_budget_ledger,
		       to_jsonb(source_row) || jsonb_build_object(
		         'ledger_id', $1::text, 'scope_id', $2::text, 'parent_ledger_id', $3::text,
		         'consumed_model_calls', 0, 'consumed_input_tokens', 0,
		         'consumed_output_tokens', 0, 'consumed_tool_calls', 0,
		         'consumed_external_cost_micro_usd', 0, 'consumed_wall_time_ms', 0,
		         'consumed_tasks', 0, 'consumed_active_tasks', 0,
		         'consumed_invalid_output_retries', 0, 'consumed_infrastructure_retries', 0,
		         'reserved_model_calls', 0, 'reserved_input_tokens', 0,
		         'reserved_output_tokens', 0, 'reserved_tool_calls', 0,
		         'reserved_external_cost_micro_usd', 0, 'reserved_wall_time_ms', 0,
		         'reserved_tasks', 0, 'reserved_active_tasks', 0,
		         'reserved_invalid_output_retries', 0, 'reserved_infrastructure_retries', 0,
		         'generation', 1, 'state', 'open', 'updated_at', $4::timestamptz))).*
		  FROM agent_control.runtime_budget_ledger AS source_row
		 WHERE source_row.ledger_id = 'task-ledger-command-1'
	`, f.taskLedgerID, f.taskID, f.runLedgerID, now); err != nil {
		return fixture{}, fmt.Errorf("seed Task ledger: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.runtime_task
		SELECT (jsonb_populate_record(NULL::agent_control.runtime_task,
		       to_jsonb(source_row) || jsonb_build_object(
		         'task_id', $1::text, 'run_id', $2::text, 'parent_task_id', NULL,
		         'depth', 0, 'objective', jsonb_set(source_row.objective, '{origin,record_id}', to_jsonb($1::text)),
		         'budget_ledger_id', $3::text, 'session_id', $4::text, 'result_artifact_id', NULL,
		         'state', 'ready', 'state_generation', 1, 'budget_slot_held', false,
		         'failure', NULL, 'created_at', $5::timestamptz, 'updated_at', $5::timestamptz,
		         'deadline_at', $5::timestamptz + interval '14 minutes', 'terminal_at', NULL))).*
		  FROM agent_control.runtime_task AS source_row
		 WHERE source_row.task_id = 'task-command-1'
	`, f.taskID, f.runID, f.taskLedgerID, f.sessionID, now); err != nil {
		return fixture{}, fmt.Errorf("seed Task: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.runtime_session
		SELECT (jsonb_populate_record(NULL::agent_control.runtime_session,
		       to_jsonb(source_row) || jsonb_build_object(
		         'session_id', $1::text, 'run_id', $2::text, 'task_id', $3::text, 'generation', 1,
		         'execution_binding', jsonb_set(source_row.execution_binding, '{origin,record_id}', to_jsonb($1::text)),
		         'latest_checkpoint_id', NULL, 'state', 'open',
		         'created_at', $4::timestamptz, 'closed_at', NULL))).*
		  FROM agent_control.runtime_session AS source_row
		 WHERE source_row.session_id = 'session-command-1'
	`, f.sessionID, f.runID, f.taskID, now); err != nil {
		return fixture{}, fmt.Errorf("seed Session: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.runtime_attempt (
		  attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
		  state, state_generation, lease_generation, lease_token,
		  lease_worker, lease_claimed_at, lease_heartbeat_at, lease_expires_at,
		  created_at, updated_at
		) VALUES ($1, 1, $2, $3, $4, 1, 'leased', 1, 1, $5::uuid,
		  jsonb_build_object('principal_id', $6::text, 'kind', 'workload', 'audience', 'worker'),
		  $7::timestamptz, $7::timestamptz, $8::timestamptz, $7::timestamptz, $7::timestamptz)
	`, f.attemptID, f.runID, f.taskID, f.sessionID, f.leaseToken, cfg.principal, now, f.leaseExpires); err != nil {
		return fixture{}, fmt.Errorf("seed Attempt: %w", err)
	}
	if err := execOne(ctx, tx, `
		INSERT INTO agent_control.runtime_attempt_lease_event (
		  event_id, schema_revision, attempt_id, event_generation, lease_generation,
		  transition, worker_principal_id, lease_token, previous_expires_at, new_expires_at,
		  actor, causation_id, correlation_id, occurred_at
		) VALUES ($1, 1, $2, 1, 1, 'claimed', $3, $4::uuid, NULL, $5::timestamptz,
		  jsonb_build_object('principal_id', $3::text, 'kind', 'workload', 'audience', 'worker'),
		  $6, $7, $8::timestamptz)
	`, uuid(), f.attemptID, cfg.principal, f.leaseToken, f.leaseExpires,
		"cause-seed-"+suffix, "correlation-seed-"+suffix, now); err != nil {
		return fixture{}, fmt.Errorf("seed lease event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_control.runtime_run SET state='running', state_generation=2 WHERE run_id=$1`, f.runID); err != nil {
		return fixture{}, fmt.Errorf("start Run: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_control.runtime_task SET state='running', state_generation=2, budget_slot_held=true WHERE task_id=$1`, f.taskID); err != nil {
		return fixture{}, fmt.Errorf("start Task: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_control.runtime_attempt SET state='executing', state_generation=2 WHERE attempt_id=$1`, f.attemptID); err != nil {
		return fixture{}, fmt.Errorf("start Attempt: %w", err)
	}
	var contextBytes []byte
	if err := tx.QueryRowContext(ctx, `
		SELECT session.context_manifest, task.output_contract_digest::text, contract.artifact_type
		  FROM agent_control.runtime_session AS session
		  JOIN agent_control.runtime_task AS task ON task.task_id = session.task_id
		  JOIN agent_control.output_contract_revision AS contract
		    ON contract.revision_id = task.output_contract_revision_id
		   AND contract.generation = task.output_contract_generation
		   AND contract.record_digest = task.output_contract_digest
		 WHERE session.session_id = $1
	`, f.sessionID).Scan(&contextBytes, &f.outputDigest, &f.artifactType); err != nil {
		return fixture{}, fmt.Errorf("read fixture contract: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fixture{}, fmt.Errorf("commit fixture %s: %w", label, err)
	}
	f.context = append(json.RawMessage(nil), contextBytes...)
	f.outputDigest = strings.TrimSpace(f.outputDigest)
	return f, nil
}

func seedOutputBlob(
	ctx context.Context,
	admin *sql.DB,
	principal, label, callID, manifestDigest string,
) (json.RawMessage, error) {
	blobID := uuid()
	stageID := uuid()
	contentDigest := digest("blob:" + label)
	bindingID := "binding-" + label
	now := time.Now().UTC().Truncate(time.Microsecond)
	var committedText string
	if err := admin.QueryRowContext(ctx, `SELECT agent_control.runtime_utc_text($1::timestamptz)`, now).Scan(&committedText); err != nil {
		return nil, fmt.Errorf("format Blob commit time: %w", err)
	}
	tx, err := admin.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin output Blob fixture: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO blob.blob_stage (
		  stage_id, principal_id, issuer_owner, media_type, max_bytes_snapshot,
		  expected_digest, expected_size_bytes, state, content_digest, size_bytes,
		  blob_id, created_at, expires_at, committed_at
		) VALUES ($1::uuid, $2, 'agent_control', 'application/json', 2, $3, 2,
		  'committed', $3, 2, $4::uuid, $5::timestamptz - interval '1 second',
		  $5::timestamptz + interval '1 hour', $5::timestamptz)
	`, stageID, principal, contentDigest, blobID, now); err != nil {
		return nil, fmt.Errorf("seed Blob stage: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO blob.blob_content (content_digest, size_bytes, state, created_at, updated_at)
		VALUES ($1, 2, 'committed', $2::timestamptz, $2::timestamptz)
	`, contentDigest, now); err != nil {
		return nil, fmt.Errorf("seed Blob content: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO blob.blob_object (
		  blob_id, stage_id, content_digest, media_type, size_bytes,
		  origin_owner, origin_record_type, origin_record_id, origin_record_digest,
		  state, committed_at
		) VALUES ($1::uuid, $2::uuid, $3, 'application/json', 2,
		  'agent_control', 'model_call_manifest', $4, $5, 'committed', $6::timestamptz)
	`, blobID, stageID, contentDigest, callID, manifestDigest, now); err != nil {
		return nil, fmt.Errorf("seed Blob object: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO blob.blob_reference (
		  binding_id, blob_id, reference_owner, reference_record_type,
		  reference_record_id, reference_record_digest, owner_principal,
		  access_class, retention_until, state, generation, bound_at
		) VALUES ($1, $2::uuid, 'agent_control', 'model_call_manifest', $3, $4, $5,
		  'private', $6::timestamptz + interval '1 hour', 'active', 1, $6::timestamptz)
	`, bindingID, blobID, callID, manifestDigest, principal, now); err != nil {
		return nil, fmt.Errorf("seed Blob reference: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit output Blob fixture: %w", err)
	}
	raw, err := json.Marshal(map[string]any{
		"schema_revision": 1, "blob_id": blobID, "content_digest": contentDigest,
		"media_type": "application/json", "size_bytes": 2,
		"origin": map[string]any{
			"owner": "agent_control", "record_type": "model_call_manifest",
			"record_id": callID, "schema_revision": 1, "record_digest": manifestDigest,
		},
		"committed_at": committedText,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal output BlobRef: %w", err)
	}
	return raw, nil
}

func snapshot(ctx context.Context, db *sql.DB, f fixture) (runtimeSnapshot, error) {
	var out runtimeSnapshot
	err := db.QueryRowContext(ctx, `
		SELECT run.state, run.state_generation,
		       task.state, task.state_generation, task.budget_slot_held,
		       session.state, session.generation,
		       attempt.state, attempt.state_generation,
		       run_ledger.generation, run_ledger.consumed_active_tasks,
		       run_ledger.consumed_invalid_output_retries,
		       run_ledger.consumed_infrastructure_retries,
		       task_ledger.generation,
		       (SELECT count(*) FROM agent_control.runtime_artifact WHERE attempt_id = $1),
		       (SELECT count(*) FROM blob.blob_reference WHERE reference_owner = 'agent_control' AND reference_record_type = 'artifact'),
		       (SELECT count(*) FROM agent_control.runtime_attempt_lease_event WHERE attempt_id = $1 AND transition = 'released'),
		       (SELECT count(*) FROM agent_control.runtime_event
		         WHERE subject_id IN ($1, $2, $3, $4, $5, $6))
		  FROM agent_control.runtime_attempt AS attempt
		  JOIN agent_control.runtime_task AS task ON task.task_id = attempt.task_id
		  JOIN agent_control.runtime_run AS run ON run.run_id = attempt.run_id
		  JOIN agent_control.runtime_session AS session ON session.session_id = attempt.session_id
		  JOIN agent_control.runtime_budget_ledger AS run_ledger ON run_ledger.ledger_id = $2
		  JOIN agent_control.runtime_budget_ledger AS task_ledger ON task_ledger.ledger_id = $3
		 WHERE attempt.attempt_id = $1
	`, f.attemptID, f.runLedgerID, f.taskLedgerID, f.runID, f.taskID, f.sessionID).Scan(
		&out.runState, &out.runGeneration,
		&out.taskState, &out.taskGeneration, &out.slotHeld,
		&out.sessionState, &out.sessionGeneration,
		&out.attemptState, &out.attemptGeneration,
		&out.budgetGeneration, &out.activeTasks,
		&out.invalidRetries, &out.infrastructureRetries,
		&out.leafBudgetGeneration, &out.artifacts, &out.artifactBindings, &out.releaseEvents,
		&out.runtimeEvents,
	)
	if err != nil {
		return out, fmt.Errorf("snapshot fixture %s: %w", f.attemptID, err)
	}
	return out, nil
}

func runMixedBarrier(
	ctx context.Context,
	dsn string,
	cfg config,
	functions, bodies []string,
) (barrierSummary, error) {
	if len(functions) != len(bodies) {
		return barrierSummary{}, errors.New("barrier function/body length mismatch")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return barrierSummary{}, fmt.Errorf("open Worker pool: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(len(bodies))
	db.SetMaxIdleConns(0)
	start := make(chan struct{})
	results := make(chan callResult, len(bodies))
	var ready sync.WaitGroup
	ready.Add(len(bodies))
	for i := range bodies {
		go func(i int) {
			conn, connErr := db.Conn(ctx)
			if connErr != nil {
				ready.Done()
				results <- callResult{err: fmt.Errorf("connection %d: %w", i, connErr)}
				return
			}
			defer conn.Close()
			setup := fmt.Sprintf(`SET SESSION AUTHORIZATION "%s"; SET ROLE alpheus_agent_worker`, cfg.principal)
			if _, connErr = conn.ExecContext(ctx, setup); connErr != nil {
				ready.Done()
				results <- callResult{err: fmt.Errorf("Worker setup %d: %w", i, connErr)}
				return
			}
			ready.Done()
			<-start
			query := fmt.Sprintf("SELECT agent_control.%s($1::TEXT)", functions[i])
			var raw []byte
			if connErr = conn.QueryRowContext(ctx, query, bodies[i]).Scan(&raw); connErr != nil {
				results <- callResult{err: fmt.Errorf("%s %d: %w", functions[i], i, connErr)}
				return
			}
			var response commandResponse
			if connErr = json.Unmarshal(raw, &response); connErr != nil {
				results <- callResult{err: fmt.Errorf("decode %s %d: %w", functions[i], i, connErr)}
				return
			}
			results <- callResult{function: functions[i], response: response}
		}(i)
	}
	ready.Wait()
	close(start)
	var summary barrierSummary
	for range bodies {
		result := <-results
		if result.err != nil {
			return summary, result.err
		}
		switch result.response.Status {
		case "committed":
			summary.committed++
			summary.winner = result.function
		case "denied":
			summary.denied++
		default:
			return summary, fmt.Errorf("unknown command status %q", result.response.Status)
		}
	}
	return summary, nil
}

func callWorker(ctx context.Context, dsn, principal, function, body string) (commandResponse, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return commandResponse{}, fmt.Errorf("open Worker connection: %w", err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return commandResponse{}, fmt.Errorf("pin Worker connection: %w", err)
	}
	defer conn.Close()
	setup := fmt.Sprintf(`SET SESSION AUTHORIZATION "%s"; SET ROLE alpheus_agent_worker`, principal)
	if _, err := conn.ExecContext(ctx, setup); err != nil {
		return commandResponse{}, fmt.Errorf("Worker setup: %w", err)
	}
	query := fmt.Sprintf("SELECT agent_control.%s($1::TEXT)", function)
	var raw []byte
	if err := conn.QueryRowContext(ctx, query, body).Scan(&raw); err != nil {
		return commandResponse{}, fmt.Errorf("%s: %w", function, err)
	}
	var response commandResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return commandResponse{}, fmt.Errorf("decode %s: %w", function, err)
	}
	return response, nil
}

func observeLockWait(ctx context.Context, admin *sql.DB, function string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := admin.QueryRowContext(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM pg_stat_activity
			   WHERE pid <> pg_backend_pid() AND datname = current_database()
			     AND state = 'active' AND wait_event_type = 'Lock'
			     AND query LIKE '%' || $1 || '%'
			)
		`, function).Scan(&waiting); err != nil {
			return fmt.Errorf("observe %s lock wait: %w", function, err)
		}
		if waiting {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s never reached the expected lock wait", function)
}

func waitUntil(ctx context.Context, target time.Time) error {
	delay := time.Until(target)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func receive(ctx context.Context, ch <-chan callResult) (callResult, error) {
	select {
	case result := <-ch:
		return result, result.err
	case <-ctx.Done():
		return callResult{}, ctx.Err()
	}
}

func verifyDeniedCommand(ctx context.Context, db *sql.DB, commandID, reason string) error {
	var state, actualReason string
	if err := db.QueryRowContext(ctx, `
		SELECT state, response->>'reason_code'
		  FROM agent_control.runtime_command WHERE command_id = $1
	`, commandID).Scan(&state, &actualReason); err != nil {
		return fmt.Errorf("read denied command %s: %w", commandID, err)
	}
	if state != "denied" || actualReason != reason {
		return fmt.Errorf("command %s state=%s reason=%s", commandID, state, actualReason)
	}
	return nil
}

func envelope(principal, commandType, prefix string, index int, deadline string) map[string]any {
	return map[string]any{
		"schema_revision": 1,
		"command_id":      fmt.Sprintf("%s-command-%02d", prefix, index),
		"actor": map[string]any{
			"principal_id": principal, "kind": "workload", "audience": "worker",
		},
		"audience":        "control_api",
		"command_type":    commandType,
		"idempotency_key": fmt.Sprintf("%s-idem-%02d", prefix, index),
		"request_digest":  digest(fmt.Sprintf("%s-request-%02d", prefix, index)),
		"causation_id":    fmt.Sprintf("cause-%s-%02d", prefix, index),
		"correlation_id":  "correlation-" + prefix,
		"deadline":        deadline,
	}
}

func marshalBody(value any) (string, error) {
	raw, err := json.Marshal(value)
	return string(raw), err
}

func execOne(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("expected one row, got %d", count)
	}
	return nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func shortToken() string {
	var raw [5]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(raw[:])
}

func uuid() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic(err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw[:])
	return strings.Join([]string{encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]}, "-")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
