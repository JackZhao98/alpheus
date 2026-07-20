// Command ap1_model_call_barrier proves that AP1 model-call dispatch and
// outcome commands linearize through PostgreSQL. It invokes no model, Kernel,
// Provider, operation, or broker capability.
package main

import (
	"context"
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

	"github.com/lib/pq"
)

var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}$`)

const (
	reservedInput  int64 = 10
	reservedOutput int64 = 20
	reservedCost   int64 = 30
	reservedWall   int64 = 40
)

type config struct {
	host      string
	port      int
	database  string
	user      string
	password  string
	principal string
	taskID    string
	requests  int
}

type attemptFence struct {
	attemptID       string
	stateGeneration int64
	leaseGeneration int64
	leaseToken      string
	contextManifest json.RawMessage
	outputDigest    string
	taskLedgerID    string
	leaseExpiresAt  time.Time
}

type commandResponse struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}

type callResult struct {
	response commandResponse
	err      error
}

type barrierSummary struct {
	committed int
	denied    int
}

type budgetCounters struct {
	ledgerID                                                                 string
	depth                                                                    int
	generation                                                               int64
	consumedModel, consumedInput, consumedOutput, consumedCost, consumedWall int64
	reservedModel, reservedInput, reservedOutput, reservedCost, reservedWall int64
}

type modelCall struct {
	turnID          string
	callID          string
	manifestDigest  string
	state           string
	stateGeneration int64
	reservationHeld bool
}

func main() {
	var cfg config
	flag.StringVar(&cfg.host, "host", "127.0.0.1", "PostgreSQL host")
	flag.IntVar(&cfg.port, "port", 5432, "PostgreSQL port")
	flag.StringVar(&cfg.database, "database", "probe", "PostgreSQL database")
	flag.StringVar(&cfg.user, "user", "postgres", "bootstrap database user")
	flag.StringVar(&cfg.password, "password", "", "bootstrap database password")
	flag.StringVar(&cfg.principal, "worker", "worker-1", "authenticated Worker principal")
	flag.StringVar(&cfg.taskID, "task-id", "task-command-1", "executing Task fixture")
	flag.IntVar(&cfg.requests, "requests", 20, "simultaneous commands per phase")
	flag.Parse()
	if flag.NArg() != 0 || cfg.port < 1 || cfg.port > 65535 || cfg.requests < 2 ||
		cfg.requests > 100 || cfg.requests%2 != 0 ||
		!safeIdentifier.MatchString(cfg.principal) || !safeIdentifier.MatchString(cfg.taskID) {
		fatal(errors.New("invalid barrier arguments"))
	}

	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
	if err := admin.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	fence, err := discoverFence(ctx, admin, cfg)
	if err != nil {
		return nil, err
	}
	if time.Until(fence.leaseExpiresAt) < 10*time.Second {
		return nil, fmt.Errorf("fixture lease has less than 10 seconds remaining")
	}
	baselineBudgets, err := loadBudgetChain(ctx, admin, fence.taskLedgerID)
	if err != nil {
		return nil, err
	}
	if len(baselineBudgets) < 1 {
		return nil, fmt.Errorf("budget ancestry too short: %d", len(baselineBudgets))
	}
	baselineTurns, baselineManifests, baselineArtifacts, err := durableCounts(ctx, admin, fence.attemptID)
	if err != nil {
		return nil, err
	}
	if err := proveIdentityWaitFence(
		ctx, admin, dsn, cfg, fence, baselineBudgets,
		baselineTurns, baselineManifests,
	); err != nil {
		return nil, err
	}

	deadline := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
	dispatchBodies := make([]string, cfg.requests)
	turnIDs := make([]string, cfg.requests)
	for i := range dispatchBodies {
		turnIDs[i] = fmt.Sprintf("model-barrier-turn-%02d", i)
		body, err := json.Marshal(map[string]any{
			"schema_revision":                   1,
			"envelope":                          envelope(cfg.principal, "dispatch_model_call", "dispatch", i, deadline),
			"attempt_id":                        fence.attemptID,
			"expected_attempt_state_generation": fence.stateGeneration,
			"lease_generation":                  fence.leaseGeneration,
			"lease_token":                       fence.leaseToken,
			"turn_id":                           turnIDs[i],
			"manifest": map[string]any{
				"call_id":                          fmt.Sprintf("model-barrier-call-%02d", i),
				"idempotency_key":                  fmt.Sprintf("model-barrier-provider-idem-%02d", i),
				"provider":                         "probe-provider",
				"model":                            "probe-model",
				"prompt_digest":                    digest("model-barrier-prompt", i),
				"context_manifest":                 fence.contextManifest,
				"output_contract_digest":           fence.outputDigest,
				"request_digest":                   digest("model-barrier-request", i),
				"max_output_tokens":                reservedOutput,
				"reserved_input_tokens":            reservedInput,
				"reserved_external_cost_micro_usd": reservedCost,
				"timeout_ms":                       reservedWall,
				"temperature_micros":               0,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal dispatch %d: %w", i, err)
		}
		dispatchBodies[i] = string(body)
	}

	dispatchSummary, err := runBarrier(ctx, dsn, cfg, "dispatch_model_call", dispatchBodies)
	if err != nil {
		return nil, err
	}
	if dispatchSummary.committed != 1 || dispatchSummary.denied != cfg.requests-1 {
		return nil, fmt.Errorf("dispatch split mismatch: committed=%d denied=%d",
			dispatchSummary.committed, dispatchSummary.denied)
	}
	winner, err := findWinner(ctx, admin, fence.attemptID, turnIDs)
	if err != nil {
		return nil, err
	}
	if winner.state != "dispatched" || winner.stateGeneration != 2 || !winner.reservationHeld {
		return nil, fmt.Errorf("dispatch winner mismatch: state=%s generation=%d held=%t",
			winner.state, winner.stateGeneration, winner.reservationHeld)
	}
	afterDispatch, err := loadBudgetChain(ctx, admin, fence.taskLedgerID)
	if err != nil {
		return nil, err
	}
	if err := verifyDispatchBudgets(baselineBudgets, afterDispatch); err != nil {
		return nil, err
	}

	outcomeBodies := make([]string, cfg.requests)
	for i := range outcomeBodies {
		kind := "resolve_model_call"
		body := map[string]any{
			"schema_revision":                   1,
			"attempt_id":                        fence.attemptID,
			"expected_attempt_state_generation": fence.stateGeneration,
			"lease_generation":                  fence.leaseGeneration,
			"lease_token":                       fence.leaseToken,
			"turn_id":                           winner.turnID,
			"expected_turn_state_generation":    winner.stateGeneration,
		}
		if i%2 == 0 {
			kind = "mark_model_call_unknown"
			body["failure"] = map[string]any{
				"code": "provider_unknown", "message": "provider outcome unknown", "retryable": true,
			}
		} else {
			body["outcome"] = "failed"
			body["failure"] = map[string]any{
				"code": "provider_failed", "message": "provider call failed", "retryable": false,
			}
		}
		body["envelope"] = envelope(cfg.principal, kind, "outcome", i, deadline)
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal outcome %d: %w", i, err)
		}
		outcomeBodies[i] = string(raw)
	}

	outcomeSummary, err := runMixedOutcomeBarrier(ctx, dsn, cfg, outcomeBodies)
	if err != nil {
		return nil, err
	}
	if outcomeSummary.committed != 1 || outcomeSummary.denied != cfg.requests-1 {
		return nil, fmt.Errorf("outcome split mismatch: committed=%d denied=%d",
			outcomeSummary.committed, outcomeSummary.denied)
	}

	state, generation, held, err := turnState(ctx, admin, winner.turnID)
	if err != nil {
		return nil, err
	}
	unknownWon := state == "unknown"
	expectedFinalGeneration := int64(3)
	if unknownWon {
		expectedFinalGeneration = 4
		body, err := json.Marshal(map[string]any{
			"schema_revision":                   1,
			"envelope":                          envelope(cfg.principal, "resolve_model_call", "resolve-unknown", 0, deadline),
			"attempt_id":                        fence.attemptID,
			"expected_attempt_state_generation": fence.stateGeneration,
			"lease_generation":                  fence.leaseGeneration,
			"lease_token":                       fence.leaseToken,
			"turn_id":                           winner.turnID,
			"expected_turn_state_generation":    generation,
			"outcome":                           "failed",
			"failure": map[string]any{
				"code": "provider_failed", "message": "unknown reconciled as failed", "retryable": false,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal unknown resolution: %w", err)
		}
		response, err := callWorker(ctx, dsn, cfg.principal, "resolve_model_call", string(body))
		if err != nil {
			return nil, err
		}
		if response.Status != "committed" {
			return nil, fmt.Errorf("unknown resolution status=%s reason=%s", response.Status, response.ReasonCode)
		}
	} else if state != "failed" || held {
		return nil, fmt.Errorf("outcome winner mismatch: state=%s generation=%d held=%t", state, generation, held)
	}

	finalState, finalGeneration, finalHeld, err := turnState(ctx, admin, winner.turnID)
	if err != nil {
		return nil, err
	}
	finalBudgets, err := loadBudgetChain(ctx, admin, fence.taskLedgerID)
	if err != nil {
		return nil, err
	}
	if err := verifyFinalBudgets(baselineBudgets, finalBudgets); err != nil {
		return nil, err
	}
	turns, manifests, artifacts, err := durableCounts(ctx, admin, fence.attemptID)
	if err != nil {
		return nil, err
	}
	var commandCount, processing int
	if err := admin.QueryRowContext(ctx, `
		SELECT count(*), count(*) FILTER (WHERE state = 'processing')
		FROM agent_control.runtime_command
		WHERE causation_id LIKE 'cause-model-barrier-%'
	`).Scan(&commandCount, &processing); err != nil {
		return nil, fmt.Errorf("verify command completion: %w", err)
	}
	expectedCommands := cfg.requests * 2
	if unknownWon {
		expectedCommands++
	}
	if finalState != "failed" || finalGeneration != expectedFinalGeneration || finalHeld ||
		turns != baselineTurns+1 || manifests != baselineManifests+1 || artifacts != baselineArtifacts ||
		commandCount != expectedCommands || processing != 0 {
		return nil, fmt.Errorf(
			"final state mismatch turn=%s/%d held=%t turns=%d manifests=%d artifacts=%d commands=%d processing=%d",
			finalState, finalGeneration, finalHeld, turns, manifests, artifacts, commandCount, processing,
		)
	}

	return map[string]any{
		"status":             "PASS",
		"probe":              "ap1-model-call-barrier",
		"requests_per_phase": cfg.requests,
		"dispatch":           map[string]any{"committed": dispatchSummary.committed, "denied": dispatchSummary.denied},
		"outcome":            map[string]any{"committed": outcomeSummary.committed, "denied": outcomeSummary.denied},
		"unknown_won":        unknownWon,
		"winner": map[string]any{
			"turn_id": winner.turnID, "call_id": winner.callID,
			"manifest_digest": winner.manifestDigest,
		},
		"terminal_state":      finalState,
		"identity_wait_fence": "denied_after_registry_lock_wait",
		"processing_commands": processing,
		"effect_ceiling":      "none",
	}, nil
}

// proveIdentityWaitFence holds one global Turn identity in a separate
// transaction until the command deadline expires. Dispatch must wait before
// its final fence, then durably deny without creating a Turn or reservation.
func proveIdentityWaitFence(
	ctx context.Context,
	admin *sql.DB,
	dsn string,
	cfg config,
	fence attemptFence,
	baselineBudgets map[string]budgetCounters,
	baselineTurns int,
	baselineManifests int,
) error {
	const (
		turnID = "model-identity-stall-turn-1"
		callID = "model-identity-stall-call-1"
	)
	blocker, err := admin.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin identity blocker: %w", err)
	}
	blockerOpen := true
	defer func() {
		if blockerOpen {
			_ = blocker.Rollback()
		}
	}()
	identityKey := "turn:" + turnID
	if _, err := admin.ExecContext(ctx, `
		INSERT INTO agent_control.runtime_model_identity_lock (identity_key)
		VALUES ($1) ON CONFLICT (identity_key) DO NOTHING
	`, identityKey); err != nil {
		return fmt.Errorf("seed model identity blocker: %w", err)
	}
	var lockedIdentity string
	if err := blocker.QueryRowContext(ctx, `
		SELECT identity_key
		FROM agent_control.runtime_model_identity_lock
		WHERE identity_key = $1
		FOR UPDATE
	`, identityKey).Scan(&lockedIdentity); err != nil {
		return fmt.Errorf("lock model identity blocker: %w", err)
	}

	commandDeadline := time.Now().UTC().Add(time.Second)
	body, err := json.Marshal(map[string]any{
		"schema_revision": 1,
		"envelope": map[string]any{
			"schema_revision": 1,
			"command_id":      "model-identity-stall-command-1",
			"actor": map[string]any{
				"principal_id": cfg.principal, "kind": "workload", "audience": "worker",
			},
			"audience":        "control_api",
			"command_type":    "dispatch_model_call",
			"idempotency_key": "model-identity-stall-command-idem-1",
			"request_digest":  digest("model-identity-stall-command", 1),
			"causation_id":    "cause-model-identity-stall-1",
			"correlation_id":  "correlation-model-identity-stall-1",
			"deadline":        commandDeadline.Format(time.RFC3339Nano),
		},
		"attempt_id":                        fence.attemptID,
		"expected_attempt_state_generation": fence.stateGeneration,
		"lease_generation":                  fence.leaseGeneration,
		"lease_token":                       fence.leaseToken,
		"turn_id":                           turnID,
		"manifest": map[string]any{
			"call_id":                          callID,
			"idempotency_key":                  strings.Repeat("i", 200),
			"provider":                         strings.Repeat("p", 200),
			"model":                            "probe-model",
			"prompt_digest":                    digest("model-identity-stall-prompt", 1),
			"context_manifest":                 fence.contextManifest,
			"output_contract_digest":           fence.outputDigest,
			"request_digest":                   digest("model-identity-stall-request", 1),
			"max_output_tokens":                reservedOutput,
			"reserved_input_tokens":            reservedInput,
			"reserved_external_cost_micro_usd": reservedCost,
			"timeout_ms":                       reservedWall,
			"temperature_micros":               0,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal identity stall dispatch: %w", err)
	}

	resultCh := make(chan callResult, 1)
	go func() {
		response, callErr := callWorker(
			ctx, dsn, cfg.principal, "dispatch_model_call", string(body),
		)
		resultCh <- callResult{response: response, err: callErr}
	}()

	waitObserved := false
	observationDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(observationDeadline) {
		var waiting bool
		if err := admin.QueryRowContext(ctx, `
			SELECT EXISTS (
			  SELECT 1 FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND state = 'active'
			    AND wait_event_type = 'Lock'
			    AND query LIKE '%dispatch_model_call%'
			)
		`).Scan(&waiting); err != nil {
			return fmt.Errorf("observe identity registry wait: %w", err)
		}
		if waiting {
			waitObserved = true
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !waitObserved {
		return errors.New("dispatch never waited on the pre-fence identity registry lock")
	}
	if remaining := time.Until(commandDeadline) + 100*time.Millisecond; remaining > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(remaining):
		}
	}
	if err := blocker.Rollback(); err != nil {
		return fmt.Errorf("release identity blocker: %w", err)
	}
	blockerOpen = false

	var result callResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	if result.err != nil {
		return fmt.Errorf("identity-stalled dispatch: %w", result.err)
	}
	if result.response.Status != "denied" || result.response.ReasonCode != "dispatch_fence_expired" {
		return fmt.Errorf("identity-stalled dispatch status=%s reason=%s",
			result.response.Status, result.response.ReasonCode)
	}

	afterBudgets, err := loadBudgetChain(ctx, admin, fence.taskLedgerID)
	if err != nil {
		return err
	}
	if err := verifyBudgetsUnchanged(baselineBudgets, afterBudgets); err != nil {
		return fmt.Errorf("identity-stalled dispatch: %w", err)
	}
	turns, manifests, _, err := durableCounts(ctx, admin, fence.attemptID)
	if err != nil {
		return err
	}
	if turns != baselineTurns || manifests != baselineManifests {
		return fmt.Errorf("identity-stalled dispatch created state: turns=%d manifests=%d", turns, manifests)
	}
	var commandState, reason string
	if err := admin.QueryRowContext(ctx, `
		SELECT state, response->>'reason_code'
		FROM agent_control.runtime_command
		WHERE command_id = 'model-identity-stall-command-1'
	`).Scan(&commandState, &reason); err != nil {
		return fmt.Errorf("load identity-stalled command: %w", err)
	}
	if commandState != "denied" || reason != "dispatch_fence_expired" {
		return fmt.Errorf("identity-stalled command not durably denied: state=%s reason=%s", commandState, reason)
	}
	return nil
}

func envelope(principal, commandType, phase string, index int, deadline string) map[string]any {
	return map[string]any{
		"schema_revision": 1,
		"command_id":      fmt.Sprintf("model-barrier-%s-command-%02d", phase, index),
		"actor":           map[string]any{"principal_id": principal, "kind": "workload", "audience": "worker"},
		"audience":        "control_api",
		"command_type":    commandType,
		"idempotency_key": fmt.Sprintf("model-barrier-%s-idem-%02d", phase, index),
		"request_digest":  digest("model-barrier-"+phase, index),
		"causation_id":    fmt.Sprintf("cause-model-barrier-%s-%02d", phase, index),
		"correlation_id":  "correlation-model-barrier-1",
		"deadline":        deadline,
	}
}

func digest(label string, index int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s-%d", label, index)))
	return hex.EncodeToString(sum[:])
}

func discoverFence(ctx context.Context, db *sql.DB, cfg config) (attemptFence, error) {
	var out attemptFence
	var contextBytes []byte
	err := db.QueryRowContext(ctx, `
		SELECT attempt.attempt_id, attempt.state_generation,
		       attempt.lease_generation, attempt.lease_token::TEXT,
		       session.context_manifest, task.output_contract_digest::TEXT,
		       task.budget_ledger_id, attempt.lease_expires_at
		FROM agent_control.runtime_task AS task
		JOIN agent_control.runtime_session AS session
		  ON session.session_id = task.session_id
		JOIN agent_control.runtime_attempt AS attempt
		  ON attempt.task_id = task.task_id AND attempt.session_id = session.session_id
		WHERE task.task_id = $1 AND task.state = 'running'
		  AND attempt.state = 'executing'
		  AND attempt.lease_worker->>'principal_id' = $2
		ORDER BY attempt.ordinal DESC
		LIMIT 1
	`, cfg.taskID, cfg.principal).Scan(
		&out.attemptID, &out.stateGeneration, &out.leaseGeneration,
		&out.leaseToken, &contextBytes, &out.outputDigest,
		&out.taskLedgerID, &out.leaseExpiresAt,
	)
	if err != nil {
		return out, fmt.Errorf("discover executing fixture: %w", err)
	}
	out.contextManifest = append(json.RawMessage(nil), contextBytes...)
	out.outputDigest = strings.TrimSpace(out.outputDigest)
	return out, nil
}

func loadBudgetChain(ctx context.Context, db *sql.DB, taskLedgerID string) (map[string]budgetCounters, error) {
	rows, err := db.QueryContext(ctx, `
		WITH RECURSIVE chain AS (
		  SELECT ledger.*, 0 AS depth
		  FROM agent_control.runtime_budget_ledger AS ledger
		  WHERE ledger.ledger_id = (
		    SELECT task_ledger.parent_ledger_id
		    FROM agent_control.runtime_budget_ledger AS task_ledger
		    WHERE task_ledger.ledger_id = $1 AND task_ledger.scope = 'task'
		  )
		  UNION ALL
		  SELECT parent.*, child.depth + 1
		  FROM chain AS child
		  JOIN agent_control.runtime_budget_ledger AS parent
		    ON parent.ledger_id = child.parent_ledger_id
		  WHERE child.depth < 4096
		)
		SELECT ledger_id, depth, generation,
		       consumed_model_calls, consumed_input_tokens, consumed_output_tokens,
		       consumed_external_cost_micro_usd, consumed_wall_time_ms,
		       reserved_model_calls, reserved_input_tokens, reserved_output_tokens,
		       reserved_external_cost_micro_usd, reserved_wall_time_ms
		FROM chain ORDER BY depth DESC, ledger_id
	`, taskLedgerID)
	if err != nil {
		return nil, fmt.Errorf("query budget ancestry: %w", err)
	}
	defer rows.Close()
	out := make(map[string]budgetCounters)
	for rows.Next() {
		var b budgetCounters
		if err := rows.Scan(&b.ledgerID, &b.depth, &b.generation,
			&b.consumedModel, &b.consumedInput, &b.consumedOutput, &b.consumedCost, &b.consumedWall,
			&b.reservedModel, &b.reservedInput, &b.reservedOutput, &b.reservedCost, &b.reservedWall,
		); err != nil {
			return nil, fmt.Errorf("scan budget ancestry: %w", err)
		}
		out[b.ledgerID] = b
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate budget ancestry: %w", err)
	}
	return out, nil
}

func verifyDispatchBudgets(before, after map[string]budgetCounters) error {
	if len(before) != len(after) {
		return errors.New("budget ancestry changed during dispatch")
	}
	for id, a := range before {
		b, ok := after[id]
		if !ok || b.generation != a.generation+1 ||
			b.consumedModel != a.consumedModel || b.consumedInput != a.consumedInput ||
			b.consumedOutput != a.consumedOutput || b.consumedCost != a.consumedCost || b.consumedWall != a.consumedWall ||
			b.reservedModel != a.reservedModel+1 || b.reservedInput != a.reservedInput+reservedInput ||
			b.reservedOutput != a.reservedOutput+reservedOutput || b.reservedCost != a.reservedCost+reservedCost ||
			b.reservedWall != a.reservedWall+reservedWall {
			return fmt.Errorf("dispatch budget mismatch at %s", id)
		}
	}
	return nil
}

func verifyBudgetsUnchanged(before, after map[string]budgetCounters) error {
	if len(before) != len(after) {
		return errors.New("budget ancestry changed during denied command")
	}
	for id, a := range before {
		b, ok := after[id]
		if !ok || b != a {
			return fmt.Errorf("budget changed during denied command at %s", id)
		}
	}
	return nil
}

func verifyFinalBudgets(before, after map[string]budgetCounters) error {
	if len(before) != len(after) {
		return errors.New("budget ancestry changed before final verification")
	}
	for id, a := range before {
		b, ok := after[id]
		if !ok || b.generation != a.generation+2 ||
			b.consumedModel != a.consumedModel+1 || b.consumedInput != a.consumedInput+reservedInput ||
			b.consumedOutput != a.consumedOutput+reservedOutput || b.consumedCost != a.consumedCost+reservedCost ||
			b.consumedWall != a.consumedWall+reservedWall ||
			b.reservedModel != a.reservedModel || b.reservedInput != a.reservedInput ||
			b.reservedOutput != a.reservedOutput || b.reservedCost != a.reservedCost || b.reservedWall != a.reservedWall {
			return fmt.Errorf("final budget mismatch at %s", id)
		}
	}
	return nil
}

func durableCounts(ctx context.Context, db *sql.DB, attemptID string) (int, int, int, error) {
	var turns, manifests, artifacts int
	err := db.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM agent_control.runtime_turn WHERE attempt_id = $1),
		  (SELECT count(*) FROM agent_control.runtime_model_call_manifest WHERE attempt_id = $1),
		  (SELECT count(*) FROM agent_control.runtime_artifact WHERE attempt_id = $1)
	`, attemptID).Scan(&turns, &manifests, &artifacts)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("query durable counts: %w", err)
	}
	return turns, manifests, artifacts, nil
}

func findWinner(ctx context.Context, db *sql.DB, attemptID string, turnIDs []string) (modelCall, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT turn.turn_id, manifest.call_id, manifest.record_digest::TEXT,
		       turn.state, turn.state_generation, turn.reservation_held
		FROM agent_control.runtime_turn AS turn
		JOIN agent_control.runtime_model_call_manifest AS manifest
		  ON manifest.turn_id = turn.turn_id AND manifest.attempt_id = turn.attempt_id
		WHERE turn.attempt_id = $1 AND turn.turn_id = ANY($2::TEXT[])
	`, attemptID, pq.Array(turnIDs))
	if err != nil {
		return modelCall{}, fmt.Errorf("query dispatch winner: %w", err)
	}
	defer rows.Close()
	var winner modelCall
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&winner.turnID, &winner.callID, &winner.manifestDigest,
			&winner.state, &winner.stateGeneration, &winner.reservationHeld); err != nil {
			return modelCall{}, fmt.Errorf("scan dispatch winner: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return modelCall{}, fmt.Errorf("iterate dispatch winner: %w", err)
	}
	winner.manifestDigest = strings.TrimSpace(winner.manifestDigest)
	if count != 1 {
		return modelCall{}, fmt.Errorf("dispatch winner count=%d", count)
	}
	return winner, nil
}

func turnState(ctx context.Context, db *sql.DB, turnID string) (string, int64, bool, error) {
	var state string
	var generation int64
	var held bool
	if err := db.QueryRowContext(ctx, `
		SELECT state, state_generation, reservation_held
		FROM agent_control.runtime_turn WHERE turn_id = $1
	`, turnID).Scan(&state, &generation, &held); err != nil {
		return "", 0, false, fmt.Errorf("query Turn state: %w", err)
	}
	return state, generation, held, nil
}

func runBarrier(ctx context.Context, dsn string, cfg config, function string, bodies []string) (barrierSummary, error) {
	functions := make([]string, len(bodies))
	for i := range functions {
		functions[i] = function
	}
	return runMixedBarrier(ctx, dsn, cfg, functions, bodies)
}

func runMixedOutcomeBarrier(ctx context.Context, dsn string, cfg config, bodies []string) (barrierSummary, error) {
	functions := make([]string, len(bodies))
	for i := range functions {
		if i%2 == 0 {
			functions[i] = "mark_model_call_unknown"
		} else {
			functions[i] = "resolve_model_call"
		}
	}
	return runMixedBarrier(ctx, dsn, cfg, functions, bodies)
}

func runMixedBarrier(ctx context.Context, dsn string, cfg config, functions, bodies []string) (barrierSummary, error) {
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
			conn, err := db.Conn(ctx)
			if err != nil {
				ready.Done()
				results <- callResult{err: fmt.Errorf("connection %d: %w", i, err)}
				return
			}
			defer conn.Close()
			setup := fmt.Sprintf(`SET SESSION AUTHORIZATION "%s"; SET ROLE alpheus_agent_worker`, cfg.principal)
			if _, err := conn.ExecContext(ctx, setup); err != nil {
				ready.Done()
				results <- callResult{err: fmt.Errorf("worker setup %d: %w", i, err)}
				return
			}
			ready.Done()
			<-start
			query := fmt.Sprintf("SELECT agent_control.%s($1::TEXT)", functions[i])
			var raw []byte
			if err := conn.QueryRowContext(ctx, query, bodies[i]).Scan(&raw); err != nil {
				results <- callResult{err: fmt.Errorf("%s %d: %w", functions[i], i, err)}
				return
			}
			var response commandResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				results <- callResult{err: fmt.Errorf("decode %s %d: %w", functions[i], i, err)}
				return
			}
			results <- callResult{response: response}
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

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
