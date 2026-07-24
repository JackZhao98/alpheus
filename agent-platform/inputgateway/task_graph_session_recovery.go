package inputgateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
	"alpheus/agentplatform/taskgraphcontract"
)

type PendingTaskGraphNodeSession struct {
	GraphID  string                          `json:"graph_id"`
	Node     taskgraphcontract.TaskGraphNode `json:"node"`
	RawInput blob.BlobRef                    `json:"raw_input"`
}

func decodePendingTaskGraphNodeSession(
	raw []byte,
) (PendingTaskGraphNodeSession, error) {
	var pending PendingTaskGraphNodeSession
	if json.Unmarshal(raw, &pending) != nil ||
		strings.TrimSpace(pending.GraphID) == "" ||
		strings.TrimSpace(pending.Node.TaskID) == "" ||
		strings.TrimSpace(pending.Node.RoleID) == "" ||
		pending.Node.RoleRevision != 1 ||
		pending.Node.Depth < 1 ||
		pending.Node.Objective.Validate() != nil ||
		pending.Node.OutputContract.Validate() != nil ||
		pending.Node.Limit.MaxTasks != 1 ||
		pending.Node.Limit.MaxParallelism != 1 ||
		pending.RawInput.Validate() != nil ||
		pending.RawInput.Origin.Owner != contracts.OwnerAgentControl ||
		pending.RawInput.Origin.RecordType != "input_raw" {
		return PendingTaskGraphNodeSession{},
			fmt.Errorf("invalid pending TaskGraph node Session")
	}
	return pending, nil
}

// ListPendingTaskGraphNodeSessions reads a bounded, origin-aware recovery
// projection. Every authority-bearing node field comes from immutable Control
// storage; the process cannot reconstruct or modify an admitted graph.
func (adapter *PostgresAdapter) ListPendingTaskGraphNodeSessions(
	ctx context.Context, limit int,
) ([]PendingTaskGraphNodeSession, error) {
	if adapter == nil || adapter.db == nil || limit < 1 || limit > 64 {
		return nil, fmt.Errorf("invalid TaskGraph Session recovery request")
	}
	pending := make([]PendingTaskGraphNodeSession, 0, limit)
	err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(
			ctx,
			`SELECT value::TEXT
			 FROM agent_control.list_cortex_task_graph_nodes_pending_session($1)
			      AS value`,
			limit,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			value, err := decodePendingTaskGraphNodeSession(raw)
			if err != nil {
				return err
			}
			pending = append(pending, value)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list pending TaskGraph node Sessions: %w", err)
	}
	return pending, nil
}
