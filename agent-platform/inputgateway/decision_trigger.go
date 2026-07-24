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
	TriggerID       string       `json:"trigger_id"`
	Generation      int64        `json:"generation"`
	Title           string       `json:"title"`
	StrategyID      string       `json:"strategy_id"`
	DataSource      string       `json:"data_source"`
	Symbol          string       `json:"symbol"`
	Metric          string       `json:"metric"`
	Comparator      string       `json:"comparator"`
	Threshold       json.Number  `json:"threshold"`
	CooldownSeconds int64        `json:"cooldown_seconds"`
	Objective       string       `json:"objective"`
	Enabled         bool         `json:"enabled"`
	State           string       `json:"state"`
	UpdatedAt       string       `json:"updated_at"`
	LastValue       *json.Number `json:"last_value,omitempty"`
	LastObservedAt  string       `json:"last_observed_at,omitempty"`
	LastReasonCode  string       `json:"last_reason_code,omitempty"`
	LastFiredAt     string       `json:"last_fired_at,omitempty"`
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

type DecisionTriggerSample struct {
	SampleID     string       `json:"sample_id"`
	TriggerID    string       `json:"trigger_id"`
	Generation   int64        `json:"generation"`
	Value        json.Number  `json:"value"`
	PriorValue   *json.Number `json:"prior_value,omitempty"`
	ConditionMet bool         `json:"condition_met"`
	Fired        bool         `json:"fired"`
	ReasonCode   string       `json:"reason_code"`
	ObservedAt   string       `json:"observed_at"`
	CommittedAt  string       `json:"committed_at"`
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

func (adapter *PostgresAdapter) RecordDecisionTriggerSample(
	ctx context.Context,
	triggerID string,
	value json.Number,
	observedAt time.Time,
) (DecisionTriggerSample, error) {
	numeric, numericErr := strconv.ParseFloat(value.String(), 64)
	if adapter == nil || adapter.db == nil ||
		!validDecisionTriggerID(triggerID) || numericErr != nil ||
		numeric < -1_000_000_000 || numeric > 1_000_000_000 ||
		observedAt.IsZero() || observedAt.Location() != time.UTC {
		return DecisionTriggerSample{},
			fmt.Errorf("invalid decision Trigger sample")
	}
	var raw []byte
	if err := adapter.withRoleTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT agent_control.record_cortex_decision_trigger_sample(
				$1,$2::NUMERIC,$3
			)::TEXT`,
			triggerID, value.String(), observedAt,
		).Scan(&raw)
	}); err != nil {
		return DecisionTriggerSample{},
			fmt.Errorf("record Cortex decision Trigger sample: %w", err)
	}
	var sample DecisionTriggerSample
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&sample) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		validateDecisionTriggerSample(sample) != nil {
		return DecisionTriggerSample{},
			fmt.Errorf("invalid decision Trigger sample response")
	}
	return sample, nil
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
	if err := validateDecisionTriggerLatest(value); err != nil {
		return err
	}
	return nil
}

func validateDecisionTriggerLatest(value DecisionTrigger) error {
	if value.LastValue == nil {
		if value.LastObservedAt != "" || value.LastReasonCode != "" ||
			value.LastFiredAt != "" {
			return fmt.Errorf("invalid decision Trigger latest sample")
		}
		return nil
	}
	numeric, numericErr := strconv.ParseFloat(value.LastValue.String(), 64)
	observed, observedErr := time.Parse(time.RFC3339Nano, value.LastObservedAt)
	if numericErr != nil || numeric < -1_000_000_000 ||
		numeric > 1_000_000_000 || observedErr != nil ||
		observed.IsZero() || observed.Location() != time.UTC ||
		!validDecisionTriggerReason(value.LastReasonCode) {
		return fmt.Errorf("invalid decision Trigger latest sample")
	}
	if value.LastFiredAt != "" {
		fired, err := time.Parse(time.RFC3339Nano, value.LastFiredAt)
		if err != nil || fired.IsZero() || fired.Location() != time.UTC ||
			fired.After(observed) {
			return fmt.Errorf("invalid decision Trigger latest firing")
		}
	}
	return nil
}

func validateDecisionTriggerSample(value DecisionTriggerSample) error {
	numeric, numericErr := strconv.ParseFloat(value.Value.String(), 64)
	observed, observedErr := time.Parse(time.RFC3339Nano, value.ObservedAt)
	committed, committedErr := time.Parse(time.RFC3339Nano, value.CommittedAt)
	if !validDecisionTriggerID(value.SampleID) ||
		!validDecisionTriggerID(value.TriggerID) || value.Generation < 1 ||
		numericErr != nil || numeric < -1_000_000_000 ||
		numeric > 1_000_000_000 ||
		!validDecisionTriggerReason(value.ReasonCode) ||
		value.Fired && !value.ConditionMet ||
		(value.ReasonCode == "cooldown_suppressed" &&
			(value.Fired || !value.ConditionMet)) ||
		observedErr != nil || committedErr != nil ||
		observed.IsZero() || committed.IsZero() ||
		observed.Location() != time.UTC ||
		committed.Location() != time.UTC || observed.After(committed) {
		return fmt.Errorf("invalid decision Trigger sample")
	}
	if value.PriorValue != nil {
		prior, err := strconv.ParseFloat(value.PriorValue.String(), 64)
		if err != nil || prior < -1_000_000_000 ||
			prior > 1_000_000_000 {
			return fmt.Errorf("invalid decision Trigger prior sample")
		}
	}
	return nil
}

func validDecisionTriggerReason(value string) bool {
	switch value {
	case "threshold_not_met", "threshold_met", "crossed",
		"no_prior_sample", "cooldown_suppressed":
		return true
	default:
		return false
	}
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
