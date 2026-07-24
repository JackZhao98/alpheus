package inputgateway

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	decisionTriggerStrategyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	decisionTriggerSymbolPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9._^-]{0,15}$`)
)

type DecisionTrigger struct {
	TriggerID       string      `json:"trigger_id"`
	Generation      int64       `json:"generation"`
	Title           string      `json:"title"`
	StrategyID      string      `json:"strategy_id"`
	DataSource      string      `json:"data_source"`
	Symbol          string      `json:"symbol"`
	Metric          string      `json:"metric"`
	Comparator      string      `json:"comparator"`
	Threshold       json.Number `json:"threshold"`
	CooldownSeconds int64       `json:"cooldown_seconds"`
	Objective       string      `json:"objective"`
	Enabled         bool        `json:"enabled"`
	State           string      `json:"state"`
	UpdatedAt       string      `json:"updated_at"`
}

type DecisionTriggerCommand struct {
	TriggerID          string      `json:"trigger_id"`
	ExpectedGeneration int64       `json:"expected_generation"`
	Title              string      `json:"title"`
	StrategyID         string      `json:"strategy_id"`
	DataSource         string      `json:"data_source"`
	Symbol             string      `json:"symbol"`
	Metric             string      `json:"metric"`
	Comparator         string      `json:"comparator"`
	Threshold          json.Number `json:"threshold"`
	CooldownSeconds    int64       `json:"cooldown_seconds"`
	Objective          string      `json:"objective"`
	Enabled            bool        `json:"enabled"`
}

type DecisionTriggerMutation struct {
	Status     string          `json:"status"`
	ReasonCode string          `json:"reason_code,omitempty"`
	Trigger    DecisionTrigger `json:"trigger,omitempty"`
}

func (adapter *PostgresAdapter) ListDecisionTriggers(
	ctx context.Context,
	subjectID string,
	limit int,
) ([]DecisionTrigger, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(subjectID) || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("invalid decision Trigger list")
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.list_cortex_decision_triggers($1,$2)::TEXT`,
			subjectID, limit,
		).Scan(&raw)
	}); err != nil {
		return nil, fmt.Errorf("list Cortex decision Triggers: %w", err)
	}
	var triggers []DecisionTrigger
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&triggers) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF || len(triggers) > limit {
		return nil, fmt.Errorf("invalid decision Trigger list response")
	}
	for _, trigger := range triggers {
		if err := validateDecisionTrigger(trigger); err != nil {
			return nil, err
		}
	}
	return triggers, nil
}

func (adapter *PostgresAdapter) RegisterDecisionTrigger(
	ctx context.Context,
	subjectID string,
	command DecisionTriggerCommand,
) (DecisionTriggerMutation, error) {
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(subjectID) ||
		validateDecisionTriggerCommand(command) != nil {
		return DecisionTriggerMutation{},
			fmt.Errorf("invalid decision Trigger command")
	}
	rawCommand, err := json.Marshal(command)
	if err != nil {
		return DecisionTriggerMutation{}, err
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.register_cortex_decision_trigger(
				$1,$2::JSONB
			)::TEXT`,
			subjectID, string(rawCommand),
		).Scan(&raw)
	}); err != nil {
		return DecisionTriggerMutation{},
			fmt.Errorf("register Cortex decision Trigger: %w", err)
	}
	var result DecisionTriggerMutation
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		(result.Status != "registered" && result.Status != "conflict") {
		return DecisionTriggerMutation{},
			fmt.Errorf("invalid decision Trigger mutation response")
	}
	if result.Status == "registered" {
		if err := validateDecisionTrigger(result.Trigger); err != nil {
			return DecisionTriggerMutation{}, err
		}
	} else if result.ReasonCode == "" {
		return DecisionTriggerMutation{},
			fmt.Errorf("invalid decision Trigger conflict")
	}
	return result, nil
}

func validateDecisionTriggerCommand(value DecisionTriggerCommand) error {
	state := "paused"
	if value.Enabled {
		state = "armed"
	}
	trigger := DecisionTrigger{
		TriggerID: value.TriggerID, Generation: 1, Title: value.Title,
		StrategyID: value.StrategyID, DataSource: value.DataSource,
		Symbol: value.Symbol, Metric: value.Metric,
		Comparator: value.Comparator, Threshold: value.Threshold,
		CooldownSeconds: value.CooldownSeconds, Objective: value.Objective,
		Enabled: value.Enabled, State: state,
		UpdatedAt: "2026-01-01T00:00:00Z",
	}
	if value.ExpectedGeneration < 0 {
		return fmt.Errorf("invalid expected generation")
	}
	return validateDecisionTrigger(trigger)
}

func validateDecisionTrigger(value DecisionTrigger) error {
	updated, updatedErr := time.Parse(time.RFC3339Nano, value.UpdatedAt)
	threshold, thresholdErr := strconv.ParseFloat(value.Threshold.String(), 64)
	validMetric := value.DataSource == "kernel_quote" &&
		(value.Metric == "mid_price" || value.Metric == "bid_price" ||
			value.Metric == "ask_price") ||
		value.DataSource == "research_gexbot" &&
			(value.Metric == "gex_call_wall" ||
				value.Metric == "gex_put_wall" ||
				value.Metric == "gex_zero_gamma")
	if !validDecisionTriggerID(value.TriggerID) || value.Generation < 1 ||
		!validDecisionTriggerTitle(value.Title) ||
		!decisionTriggerStrategyPattern.MatchString(value.StrategyID) ||
		!decisionTriggerSymbolPattern.MatchString(value.Symbol) ||
		!validMetric ||
		(value.Comparator != "gte" && value.Comparator != "lte" &&
			value.Comparator != "crosses_above" &&
			value.Comparator != "crosses_below") ||
		thresholdErr != nil || threshold < -1_000_000_000 ||
		threshold > 1_000_000_000 ||
		value.CooldownSeconds < 5 || value.CooldownSeconds > 86400 ||
		value.Objective == "" || value.Objective != strings.TrimSpace(value.Objective) ||
		len(value.Objective) > 4000 ||
		(value.State != "armed" && value.State != "paused") ||
		value.Enabled != (value.State == "armed") || updatedErr != nil ||
		updated.IsZero() || updated.Location() != time.UTC {
		return fmt.Errorf("invalid decision Trigger")
	}
	return nil
}

func validDecisionTriggerID(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 200 {
		return false
	}
	for _, char := range value {
		if unicode.IsSpace(char) || unicode.IsControl(char) {
			return false
		}
	}
	return true
}

func validDecisionTriggerTitle(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 160 {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}
