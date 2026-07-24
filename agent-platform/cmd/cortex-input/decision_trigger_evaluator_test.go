package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"alpheus/agentplatform/inputgateway"
)

type decisionTriggerEvaluationStoreProbe struct {
	triggers []inputgateway.DecisionTrigger
	samples  []inputgateway.DecisionTriggerSample
	values   []json.Number
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
	sample := inputgateway.DecisionTriggerSample{
		SampleID: "sample-1", TriggerID: triggerID, Generation: 1,
		Value: value, ReasonCode: "threshold_not_met",
		ObservedAt:  observedAt.Format(time.RFC3339Nano),
		CommittedAt: observedAt.Add(time.Millisecond).Format(time.RFC3339Nano),
	}
	probe.samples = append(probe.samples, sample)
	return sample, nil
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
