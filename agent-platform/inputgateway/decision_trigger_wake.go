package inputgateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"alpheus/agentplatform/blob"
	"alpheus/agentplatform/contracts"
)

type DecisionTriggerWake struct {
	Status       string `json:"status"`
	OccurrenceID string `json:"occurrence_id"`
	RunID        string `json:"run_id"`
	RootTaskID   string `json:"root_task_id"`
	RunState     string `json:"run_state"`
	TaskState    string `json:"task_state"`
}

type PendingDecisionTriggerWake struct {
	Trigger    DecisionTrigger           `json:"trigger"`
	Sample     DecisionTriggerSample     `json:"sample"`
	Occurrence DecisionTriggerOccurrence `json:"occurrence"`
}

type PendingDecisionTriggerOccurrence struct {
	Trigger DecisionTrigger       `json:"trigger"`
	Sample  DecisionTriggerSample `json:"sample"`
}

func (adapter *PostgresAdapter) ListPendingDecisionTriggerOccurrences(
	ctx context.Context,
	subjectID string,
	limit int,
) ([]PendingDecisionTriggerOccurrence, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(subjectID) ||
		limit < 1 || limit > 100 {
		return nil, fmt.Errorf(
			"invalid pending decision Trigger occurrence list")
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.list_pending_cortex_decision_trigger_occurrences(
				$1,$2
			)::TEXT`,
			subjectID, limit,
		).Scan(&raw)
	}); err != nil {
		return nil, fmt.Errorf(
			"list pending Cortex decision Trigger occurrences: %w", err)
	}
	var items []PendingDecisionTriggerOccurrence
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&items) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		len(items) > limit {
		return nil, fmt.Errorf(
			"invalid pending decision Trigger occurrence response")
	}
	for _, item := range items {
		if validateDecisionTrigger(item.Trigger) != nil ||
			validateDecisionTriggerSample(item.Sample) != nil ||
			!item.Sample.Fired ||
			item.Sample.TriggerID != item.Trigger.TriggerID ||
			item.Sample.Generation != item.Trigger.Generation {
			return nil, fmt.Errorf(
				"invalid pending decision Trigger occurrence")
		}
	}
	return items, nil
}

func (adapter *PostgresAdapter) ListPendingDecisionTriggerWakes(
	ctx context.Context,
	subjectID string,
	limit int,
) ([]PendingDecisionTriggerWake, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(subjectID) ||
		limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid pending decision Trigger wake list")
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.list_pending_cortex_decision_trigger_wakes(
				$1,$2
			)::TEXT`,
			subjectID, limit,
		).Scan(&raw)
	}); err != nil {
		return nil, fmt.Errorf(
			"list pending Cortex decision Trigger wakes: %w", err)
	}
	var items []PendingDecisionTriggerWake
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&items) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		len(items) > limit {
		return nil, fmt.Errorf("invalid pending decision Trigger wake response")
	}
	for _, item := range items {
		if validateDecisionTrigger(item.Trigger) != nil ||
			validateDecisionTriggerSample(item.Sample) != nil ||
			validateDecisionTriggerOccurrence(item.Occurrence) != nil ||
			!item.Sample.Fired ||
			item.Sample.TriggerID != item.Trigger.TriggerID ||
			item.Occurrence.TriggerID != item.Trigger.TriggerID ||
			item.Occurrence.SampleID != item.Sample.SampleID {
			return nil, fmt.Errorf("invalid pending decision Trigger wake")
		}
	}
	return items, nil
}

func (adapter *PostgresAdapter) AdmitDecisionTriggerWake(
	ctx context.Context,
	subjectID string,
	trigger DecisionTrigger,
	sample DecisionTriggerSample,
	occurrence DecisionTriggerOccurrence,
) (DecisionTriggerWake, error) {
	if adapter == nil || adapter.db == nil || adapter.local == nil ||
		!validDecisionTriggerID(subjectID) ||
		validateDecisionTrigger(trigger) != nil ||
		validateDecisionTriggerSample(sample) != nil ||
		validateDecisionTriggerOccurrence(occurrence) != nil ||
		!sample.Fired || sample.TriggerID != trigger.TriggerID ||
		occurrence.TriggerID != trigger.TriggerID ||
		occurrence.SampleID != sample.SampleID {
		return DecisionTriggerWake{},
			fmt.Errorf("invalid decision Trigger wake")
	}
	occurredAt, _ := time.Parse(time.RFC3339Nano, occurrence.OccurredAt)
	deadline := occurredAt.Add(14 * time.Minute)
	if trigger.DataSource == "moody_blues_replay" {
		deadline = time.Now().UTC().Add(14 * time.Minute)
	}
	if time.Until(deadline) <= 90*time.Second {
		return DecisionTriggerWake{},
			fmt.Errorf("decision Trigger wake expired")
	}
	prompt := decisionTriggerWakePrompt(trigger, sample, occurrence)
	rawInput, err := adapter.CommitRawInput(ctx, RawBlobRequest{
		SubjectPrincipalID: subjectID,
		InputID:            occurrence.OccurrenceID,
		Text:               []byte(prompt),
		MediaType:          rawInputMediaType,
	})
	if err != nil {
		return DecisionTriggerWake{},
			fmt.Errorf("commit decision Trigger wake input: %w", err)
	}
	source := contracts.RecordRef{
		Owner: "agent_control", RecordType: "trigger_occurrence",
		RecordID: occurrence.OccurrenceID, SchemaRevision: 1,
		RecordDigest: occurrence.OccurrenceDigest,
	}
	objective, err := adapter.CommitControlJSON(
		ctx, "task_objective", occurrence.OccurrenceID,
		"agent-platform.contract.decision_trigger_wake_objective.v1",
		struct {
			SchemaRevision uint16              `json:"schema_revision"`
			Source         contracts.RecordRef `json:"source"`
			TriggerID      string              `json:"trigger_id"`
			StrategyID     string              `json:"strategy_id"`
			Objective      string              `json:"objective"`
			EffectCeiling  string              `json:"effect_ceiling"`
		}{
			SchemaRevision: 1, Source: source,
			TriggerID: trigger.TriggerID, StrategyID: trigger.StrategyID,
			Objective: trigger.Objective, EffectCeiling: "none",
		},
	)
	if err != nil {
		return DecisionTriggerWake{},
			fmt.Errorf("commit decision Trigger wake objective: %w", err)
	}
	command := struct {
		OccurrenceID string       `json:"occurrence_id"`
		Deadline     time.Time    `json:"deadline"`
		RawInput     blob.BlobRef `json:"raw_input"`
		Objective    blob.BlobRef `json:"objective"`
	}{
		OccurrenceID: occurrence.OccurrenceID,
		Deadline:     deadline.UTC(),
		RawInput:     rawInput,
		Objective:    objective,
	}
	commandRaw, err := json.Marshal(command)
	if err != nil {
		return DecisionTriggerWake{}, err
	}
	var responseRaw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.admit_cortex_decision_trigger_wake(
				$1::JSONB
			)::TEXT`,
			string(commandRaw),
		).Scan(&responseRaw)
	}); err != nil {
		return DecisionTriggerWake{},
			fmt.Errorf("admit decision Trigger wake: %w", err)
	}
	var wake DecisionTriggerWake
	decoder := json.NewDecoder(strings.NewReader(string(responseRaw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wake) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		wake.Status != "admitted" ||
		wake.OccurrenceID != occurrence.OccurrenceID ||
		!validDecisionTriggerID(wake.RunID) ||
		!validDecisionTriggerID(wake.RootTaskID) ||
		wake.RunState != "queued" || wake.TaskState != "ready" {
		return DecisionTriggerWake{},
			fmt.Errorf("invalid decision Trigger wake response")
	}
	if err := adapter.prepareDecisionTriggerWakeSession(
		ctx, wake, occurrence, rawInput,
	); err != nil {
		return DecisionTriggerWake{}, err
	}
	return wake, nil
}

func decisionTriggerWakePrompt(
	trigger DecisionTrigger,
	sample DecisionTriggerSample,
	occurrence DecisionTriggerOccurrence,
) string {
	return fmt.Sprintf(
		"MARKET TRIGGER WAKE — EFFECT CEILING: NONE\n"+
			"This is an automatic, read-only Cortex decision review, not a user order.\n"+
			"Trigger: %s\nStrategy: %s\nInstrument: %s\n"+
			"Condition: %s %s %s\nObserved value: %s\n"+
			"Observed at: %s\nOccurrence: %s\n\n"+
			"Objective: %s\n\n"+
			"Assess the market context and portfolio implications. "+
			"State whether the setup deserves continued monitoring. "+
			"If the active strategy objective requires a decision and "+
			"receipt-backed evidence supports it, the final Decision Desk "+
			"may emit one effect-free equity Paper Candidate. A Candidate "+
			"is not approval or an order. Cite tool receipts for new facts. "+
			"Do not place, propose as approved, or imply a live order.",
		trigger.Title, trigger.StrategyID, trigger.Symbol,
		trigger.Metric, trigger.Comparator, trigger.Threshold.String(),
		sample.Value.String(), sample.ObservedAt, occurrence.OccurrenceID,
		trigger.Objective,
	)
}

func (adapter *PostgresAdapter) prepareDecisionTriggerWakeSession(
	ctx context.Context,
	wake DecisionTriggerWake,
	occurrence DecisionTriggerOccurrence,
	rawInput blob.BlobRef,
) error {
	worker := "cortex-worker-1"
	execution, err := adapter.CommitControlJSON(
		ctx, "execution_binding", wake.RootTaskID,
		"agent-platform.contract.execution_binding.v1",
		map[string]any{
			"schema_revision":     1,
			"task_id":             wake.RootTaskID,
			"worker_principal_id": worker,
			"provider":            "openai",
			"model":               "gpt-5.6-sol",
		},
	)
	if err != nil {
		return fmt.Errorf("commit decision Trigger execution binding: %w", err)
	}
	contextRef, err := adapter.CommitControlJSON(
		ctx, "context_manifest", occurrence.OccurrenceID,
		"agent-platform.contract.context_manifest.v1",
		map[string]any{
			"schema_revision": 1,
			"request_id":      occurrence.OccurrenceID,
			"raw_input":       rawInput,
		},
	)
	if err != nil {
		return fmt.Errorf("commit decision Trigger context: %w", err)
	}
	executionRaw, _ := json.Marshal(execution)
	contextRaw, _ := json.Marshal(contextRef)
	inputRaw, _ := json.Marshal(rawInput)
	return adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		var raw []byte
		if err := tx.QueryRowContext(ctx,
			`SELECT agent_control.prepare_cortex_root_session_v2(
				$1,$2::JSONB,$3::JSONB,$4::JSONB,$5
			)::TEXT`,
			wake.RootTaskID, string(executionRaw), string(contextRaw),
			string(inputRaw), worker,
		).Scan(&raw); err != nil {
			return fmt.Errorf("prepare decision Trigger Session: %w", err)
		}
		var response struct {
			Status    string `json:"status"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal(raw, &response) != nil ||
			response.Status != "ready" ||
			!validDecisionTriggerID(response.SessionID) {
			return fmt.Errorf("decision Trigger Session was not prepared")
		}
		return nil
	})
}
