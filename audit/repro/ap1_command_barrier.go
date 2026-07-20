// Command ap1_command_barrier proves that concurrent Worker claims linearize
// through AP1's PostgreSQL transaction boundary. It uses no Agent model,
// Kernel, Provider, operation, or broker capability.
package main

import (
	"context"
	"database/sql"
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

var principalPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,63}$`)

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

type commandResponse struct {
	Status     string `json:"status"`
	ReasonCode string `json:"reason_code"`
}

type workerResult struct {
	response commandResponse
	err      error
}

func main() {
	var cfg config
	flag.StringVar(&cfg.host, "host", "127.0.0.1", "PostgreSQL host")
	flag.IntVar(&cfg.port, "port", 5432, "PostgreSQL port")
	flag.StringVar(&cfg.database, "database", "probe", "PostgreSQL database")
	flag.StringVar(&cfg.user, "user", "postgres", "bootstrap database user")
	flag.StringVar(&cfg.password, "password", "", "bootstrap database password")
	flag.StringVar(&cfg.principal, "worker", "worker-1", "authenticated Worker principal")
	flag.StringVar(&cfg.taskID, "task-id", "task-command-concurrency-1", "ready Task fixture")
	flag.IntVar(&cfg.requests, "requests", 20, "simultaneous unique claims")
	flag.Parse()
	if flag.NArg() != 0 || cfg.port < 1 || cfg.port > 65535 || cfg.requests < 2 ||
		cfg.requests > 100 || !principalPattern.MatchString(cfg.principal) ||
		!principalPattern.MatchString(cfg.taskID) {
		fatal(errors.New("invalid barrier arguments"))
	}

	started := time.Now()
	result, err := run(context.Background(), cfg)
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
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(cfg.requests)
	db.SetMaxIdleConns(0)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	deadline := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339Nano)
	start := make(chan struct{})
	results := make(chan workerResult, cfg.requests)
	var ready sync.WaitGroup
	ready.Add(cfg.requests)
	for index := 0; index < cfg.requests; index++ {
		go func(index int) {
			conn, err := db.Conn(ctx)
			if err != nil {
				ready.Done()
				results <- workerResult{err: fmt.Errorf("connection %d: %w", index, err)}
				return
			}
			defer conn.Close()
			setup := fmt.Sprintf(
				`SET SESSION AUTHORIZATION "%s"; SET ROLE alpheus_agent_worker`,
				cfg.principal,
			)
			if _, err := conn.ExecContext(ctx, setup); err != nil {
				ready.Done()
				results <- workerResult{err: fmt.Errorf("worker setup %d: %w", index, err)}
				return
			}
			defer func() {
				_, _ = conn.ExecContext(context.Background(),
					"RESET ROLE; RESET SESSION AUTHORIZATION")
			}()
			ready.Done()
			<-start

			body, err := json.Marshal(map[string]any{
				"schema_revision": 1,
				"envelope": map[string]any{
					"schema_revision": 1,
					"command_id":      fmt.Sprintf("barrier-command-%02d", index),
					"actor": map[string]any{
						"principal_id": cfg.principal,
						"kind":         "workload",
						"audience":     "worker",
					},
					"audience":        "control_api",
					"command_type":    "claim_task",
					"idempotency_key": fmt.Sprintf("barrier-idem-%02d", index),
					"request_digest":  strings.Repeat("e", 64),
					"causation_id":    "cause-claim-command-concurrency-1",
					"correlation_id":  "correlation-command-concurrency-1",
					"deadline":        deadline,
				},
				"task_id":                        cfg.taskID,
				"expected_task_state_generation": 1,
				"requested_lease_seconds":        5,
			})
			if err != nil {
				results <- workerResult{err: fmt.Errorf("marshal command %d: %w", index, err)}
				return
			}

			var raw []byte
			if err := conn.QueryRowContext(ctx,
				"SELECT agent_control.claim_task($1::TEXT)", string(body),
			).Scan(&raw); err != nil {
				results <- workerResult{err: fmt.Errorf("claim %d: %w", index, err)}
				return
			}
			var response commandResponse
			if err := json.Unmarshal(raw, &response); err != nil {
				results <- workerResult{err: fmt.Errorf("decode claim %d: %w", index, err)}
				return
			}
			results <- workerResult{response: response}
		}(index)
	}
	ready.Wait()
	close(start)

	committed := 0
	denied := 0
	for index := 0; index < cfg.requests; index++ {
		result := <-results
		if result.err != nil {
			_ = db.Close()
			return nil, result.err
		}
		switch result.response.Status {
		case "committed":
			committed++
		case "denied":
			denied++
		default:
			_ = db.Close()
			return nil, fmt.Errorf("unknown claim status %q", result.response.Status)
		}
	}
	if err := db.Close(); err != nil {
		return nil, fmt.Errorf("close Worker pool: %w", err)
	}
	if committed != 1 || denied != cfg.requests-1 {
		return nil, fmt.Errorf("claim split mismatch: committed=%d denied=%d", committed, denied)
	}

	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open admin verification: %w", err)
	}
	defer admin.Close()
	var attempts, commands, processing, runActive, taskActive int
	var taskState string
	var taskGeneration int64
	var slotHeld bool
	err = admin.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM agent_control.runtime_attempt
		    WHERE task_id = $1 AND state IN ('leased', 'executing')),
		  (SELECT count(*) FROM agent_control.runtime_command
		    WHERE causation_id = 'cause-claim-command-concurrency-1'),
		  (SELECT count(*) FROM agent_control.runtime_command
		    WHERE causation_id = 'cause-claim-command-concurrency-1'
		      AND state = 'processing'),
		  (SELECT consumed_active_tasks FROM agent_control.runtime_budget_ledger
		    WHERE ledger_id = 'run-ledger-command-concurrency-1'),
		  (SELECT consumed_active_tasks FROM agent_control.runtime_budget_ledger
		    WHERE ledger_id = 'task-ledger-command-concurrency-1'),
		  (SELECT state FROM agent_control.runtime_task WHERE task_id = $1),
		  (SELECT state_generation FROM agent_control.runtime_task WHERE task_id = $1),
		  (SELECT budget_slot_held FROM agent_control.runtime_task WHERE task_id = $1)
	`, cfg.taskID).Scan(
		&attempts, &commands, &processing, &runActive, &taskActive,
		&taskState, &taskGeneration, &slotHeld,
	)
	if err != nil {
		return nil, fmt.Errorf("verify durable state: %w", err)
	}
	if attempts != 1 || commands != cfg.requests || processing != 0 ||
		runActive != 1 || taskActive != 0 || taskState != "running" ||
		taskGeneration != 2 || !slotHeld {
		return nil, fmt.Errorf(
			"durable state mismatch attempts=%d commands=%d processing=%d run_active=%d task_active=%d task=%s/%d slot=%t",
			attempts, commands, processing, runActive, taskActive,
			taskState, taskGeneration, slotHeld,
		)
	}

	return map[string]any{
		"status":               "PASS",
		"probe":                "ap1-command-barrier",
		"requests":             cfg.requests,
		"committed":            committed,
		"denied":               denied,
		"nonterminal_attempts": attempts,
		"processing_commands":  processing,
		"effect_ceiling":       "none",
	}, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
