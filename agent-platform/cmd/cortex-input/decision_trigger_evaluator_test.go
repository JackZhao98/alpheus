package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/inputgateway"
)

type decisionTriggerEvaluationStoreProbe struct {
	triggers []inputgateway.DecisionTrigger
	samples  []inputgateway.DecisionTriggerSample
	values   []json.Number
	fired    bool
	occurs   []string
	wakes    []string
	pending  []inputgateway.PendingDecisionTriggerWake
}

func (probe *decisionTriggerEvaluationStoreProbe) ListPendingDecisionTriggerWakes(
	context.Context, string, int,
) ([]inputgateway.PendingDecisionTriggerWake, error) {
	return probe.pending, nil
}

func (probe *decisionTriggerEvaluationStoreProbe) ListDecisionTriggers(
	context.Context, string, int,
) ([]inputgateway.DecisionTrigger, error) {
	return probe.triggers, nil
}

func (probe *decisionTriggerEvaluationStoreProbe) RecordDecisionTriggerSample(
	_ context.Context,
	triggerID string,
	value json.Number,
	observedAt time.Time,
) (inputgateway.DecisionTriggerSample, error) {
	probe.values = append(probe.values, value)
	reason := "threshold_not_met"
	if probe.fired {
		reason = "crossed"
	}
	sample := inputgateway.DecisionTriggerSample{
		SampleID: "sample-1", TriggerID: triggerID, Generation: 1,
		Value: value, ConditionMet: probe.fired, Fired: probe.fired,
		ReasonCode:  reason,
		ObservedAt:  observedAt.Format(time.RFC3339Nano),
		CommittedAt: observedAt.Add(time.Millisecond).Format(time.RFC3339Nano),
	}
	probe.samples = append(probe.samples, sample)
	return sample, nil
}

func (probe *decisionTriggerEvaluationStoreProbe) MaterializeDecisionTriggerOccurrence(
	_ context.Context,
	sampleID string,
) (inputgateway.DecisionTriggerOccurrence, error) {
	probe.occurs = append(probe.occurs, sampleID)
	return inputgateway.DecisionTriggerOccurrence{
		Status: "materialized", SampleID: sampleID,
		TriggerID: "trigger-mid", OccurrenceID: "occurrence-1",
		OccurrenceDigest:   strings.Repeat("a", 64),
		SourceRecordDigest: strings.Repeat("b", 64),
		OccurredAt:         time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func (probe *decisionTriggerEvaluationStoreProbe) AdmitDecisionTriggerWake(
	_ context.Context,
	_ string,
	_ inputgateway.DecisionTrigger,
	_ inputgateway.DecisionTriggerSample,
	occurrence inputgateway.DecisionTriggerOccurrence,
) (inputgateway.DecisionTriggerWake, error) {
	probe.wakes = append(probe.wakes, occurrence.OccurrenceID)
	return inputgateway.DecisionTriggerWake{
		Status: "admitted", OccurrenceID: occurrence.OccurrenceID,
		RunID: "run-1", RootTaskID: "task-1",
		RunState: "queued", TaskState: "ready",
	}, nil
}

func TestEvaluateCortexDecisionTriggersUsesConfiguredMetric(t *testing.T) {
	now := time.Now().UTC()
	probe := &decisionTriggerEvaluationStoreProbe{
		triggers: []inputgateway.DecisionTrigger{
			{
				TriggerID: "trigger-mid", DataSource: "kernel_quote",
				Symbol: "SPY", Metric: "mid_price", Enabled: true,
			},
			{
				TriggerID: "trigger-paused", DataSource: "kernel_quote",
				Symbol: "SPY", Metric: "ask_price", Enabled: false,
			},
			{
				TriggerID:  "trigger-research",
				DataSource: "research_gexbot", Enabled: true,
			},
		},
	}
	fetches := 0
	fetch := func(
		_ context.Context,
		trigger inputgateway.DecisionTrigger,
	) (cortexMonitorQuote, error) {
		fetches++
		return cortexMonitorQuote{
			TriggerID: trigger.TriggerID, Symbol: trigger.Symbol,
			Bid: "638.10", Ask: "638.14", Mid: "638.12",
			ObservedAt: now, AvailableAt: now,
		}, nil
	}
	count, err := evaluateCortexDecisionTriggers(
		context.Background(), probe, fetch, "owner-1",
	)
	if err != nil || count != 1 || fetches != 1 ||
		len(probe.values) != 1 || probe.values[0].String() != "638.12" {
		t.Fatalf("count=%d fetches=%d values=%v err=%v",
			count, fetches, probe.values, err)
	}
}

func TestDecisionTriggerQuoteValueRejectsUnsupportedOrExponent(t *testing.T) {
	quote := cortexMonitorQuote{Bid: "1e3", Ask: "2", Mid: "1.5"}
	if _, err := decisionTriggerQuoteValue("bid_price", quote); err == nil {
		t.Fatal("exponent quote accepted")
	}
	if _, err := decisionTriggerQuoteValue("last_price", quote); err == nil {
		t.Fatal("unsupported metric accepted")
	}
}

func TestEvaluateCortexDecisionTriggersMaterializesOnlyFiredSample(
	t *testing.T,
) {
	now := time.Now().UTC()
	probe := &decisionTriggerEvaluationStoreProbe{
		fired: true,
		triggers: []inputgateway.DecisionTrigger{{
			TriggerID: "trigger-mid", DataSource: "kernel_quote",
			Symbol: "SPY", Metric: "mid_price", Enabled: true,
		}},
	}
	fetch := func(
		_ context.Context,
		trigger inputgateway.DecisionTrigger,
	) (cortexMonitorQuote, error) {
		return cortexMonitorQuote{
			TriggerID: trigger.TriggerID, Symbol: trigger.Symbol,
			Bid: "728", Ask: "730", Mid: "729",
			ObservedAt: now, AvailableAt: now,
		}, nil
	}
	count, err := evaluateCortexDecisionTriggers(
		context.Background(), probe, fetch, "owner-1",
	)
	if err != nil || count != 1 || len(probe.occurs) != 1 ||
		probe.occurs[0] != "sample-1" ||
		len(probe.wakes) != 1 || probe.wakes[0] != "occurrence-1" {
		t.Fatalf("count=%d occurrences=%v wakes=%v err=%v",
			count, probe.occurs, probe.wakes, err)
	}
}

func TestRecoverCortexDecisionTriggerWakesReplaysPendingOccurrence(
	t *testing.T,
) {
	trigger := inputgateway.DecisionTrigger{TriggerID: "trigger-mid"}
	sample := inputgateway.DecisionTriggerSample{
		SampleID: "sample-1", TriggerID: "trigger-mid", Fired: true,
	}
	occurrence := inputgateway.DecisionTriggerOccurrence{
		OccurrenceID: "occurrence-recovery",
	}
	probe := &decisionTriggerEvaluationStoreProbe{
		pending: []inputgateway.PendingDecisionTriggerWake{{
			Trigger: trigger, Sample: sample, Occurrence: occurrence,
		}},
	}
	count, err := recoverCortexDecisionTriggerWakes(
		context.Background(), probe, "owner-1",
	)
	if err != nil || count != 1 || len(probe.wakes) != 1 ||
		probe.wakes[0] != occurrence.OccurrenceID {
		t.Fatalf("count=%d wakes=%v err=%v", count, probe.wakes, err)
	}
}
