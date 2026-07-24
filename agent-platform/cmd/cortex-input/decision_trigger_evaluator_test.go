package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/inputgateway"
)

type decisionTriggerEvaluationStoreProbe struct {
	triggers           []inputgateway.DecisionTrigger
	samples            []inputgateway.DecisionTriggerSample
	values             []json.Number
	fired              bool
	occurs             []string
	wakes              []string
	pendingOccurrences []inputgateway.PendingDecisionTriggerOccurrence
	pending            []inputgateway.PendingDecisionTriggerWake
}

func (probe *decisionTriggerEvaluationStoreProbe) ListPendingDecisionTriggerOccurrences(
	context.Context, string, int,
) ([]inputgateway.PendingDecisionTriggerOccurrence, error) {
	return probe.pendingOccurrences, nil
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
	) (json.Number, time.Time, error) {
		fetches++
		if trigger.DataSource == "research_gexbot" {
			return "5100", now, nil
		}
		return "638.12", now, nil
	}
	count, err := evaluateCortexDecisionTriggers(
		context.Background(), probe, fetch, "owner-1",
	)
	if err != nil || count != 2 || fetches != 2 ||
		len(probe.values) != 2 ||
		probe.values[0].String() != "638.12" ||
		probe.values[1].String() != "5100" {
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
		_ inputgateway.DecisionTrigger,
	) (json.Number, time.Time, error) {
		return "729", now, nil
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

func TestDecisionTriggerGEXValueUsesDeterministicMetricMapping(t *testing.T) {
	observation := cortexMonitorGEX{Metrics: map[string]json.Number{
		"major_pos_oi": "6125",
		"major_neg_oi": "5875",
		"zero_gamma":   "6010.5",
	}}
	for metric, expected := range map[string]string{
		"gex_call_wall":  "6125",
		"gex_put_wall":   "5875",
		"gex_zero_gamma": "6010.5",
	} {
		value, err := decisionTriggerGEXValue(metric, observation)
		if err != nil || value.String() != expected {
			t.Fatalf("%s=%s err=%v", metric, value, err)
		}
	}
	if _, err := decisionTriggerGEXValue(
		"spot", observation,
	); err == nil {
		t.Fatal("unregistered GEX metric accepted")
	}
}

func TestCortexMonitorGEXRejectsStaleArchiveObservation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	value := cortexMonitorGEX{
		Available: true, SchemaRevision: 1,
		ObservationID: "observation-1", Provider: "gexbot_classic",
		ProviderRevision: "gexbot_classic_v1",
		SourceKind:       "provider_poll",
		Symbol:           "SPX", Category: "gex_full",
		SourceTimestamp: now.Add(-11 * time.Minute),
		ObservedAt:      now.Add(-10*time.Minute - time.Second),
		FetchedAt:       now.Add(-10 * time.Minute),
		AvailableAt:     now.Add(-10 * time.Minute),
		IngestedAt:      now.Add(-10 * time.Minute),
		Raw: cortexMonitorGEXRaw{
			BlobID: "blob-1", ContentDigest: strings.Repeat("a", 64),
			SizeBytes: 512,
		},
		Metrics: map[string]json.Number{
			"zero_gamma": "6010.5",
		},
		QualityState: "accepted",
		RecordDigest: strings.Repeat("b", 64),
	}
	if validCortexMonitorGEX(value, now) {
		t.Fatal("stale Moody Blues observation accepted as a live Trigger sample")
	}
}

func TestFetchCortexMonitorGEXUsesMoodyBluesArchive(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost ||
			request.URL.Path !=
				"/internal/v1/moody-blues/providers/gexbot-classic/as-of" ||
			request.Header.Get("Authorization") != "Bearer research-token" {
			t.Fatalf("request=%s %s auth=%q", request.Method,
				request.URL.Path, request.Header.Get("Authorization"))
		}
		var input map[string]string
		if json.NewDecoder(request.Body).Decode(&input) != nil ||
			input["symbol"] != "SPX" ||
			input["category"] != "gex_full" ||
			input["as_of"] == "" {
			t.Fatalf("input=%v", input)
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"available":         true,
			"schema_revision":   1,
			"observation_id":    "observation-1",
			"provider":          "gexbot_classic",
			"provider_revision": "gexbot_classic_v1",
			"source_kind":       "provider_poll",
			"symbol":            "SPX",
			"category":          "gex_full",
			"source_timestamp":  now.Add(-30 * time.Second),
			"observed_at":       now.Add(-20 * time.Second),
			"fetched_at":        now.Add(-19 * time.Second),
			"available_at":      now.Add(-18 * time.Second),
			"ingested_at":       now.Add(-18 * time.Second),
			"raw": map[string]any{
				"blob_id":        "blob-1",
				"content_digest": strings.Repeat("a", 64),
				"size_bytes":     512,
			},
			"metrics": map[string]any{
				"major_pos_oi": 6125,
				"major_neg_oi": 5875,
				"zero_gamma":   6010.5,
			},
			"quality_state": "accepted",
			"record_digest": strings.Repeat("b", 64),
		})
	}))
	defer server.Close()

	observation, err := fetchCortexMonitorGEX(
		context.Background(), server.Client(), server.URL,
		"research-token",
		inputgateway.DecisionTrigger{
			TriggerID: "gex-trigger", DataSource: "research_gexbot",
			Symbol: "SPX", Metric: "gex_zero_gamma",
		},
	)
	value, valueErr := decisionTriggerGEXValue(
		"gex_zero_gamma", observation,
	)
	if err != nil || valueErr != nil || value.String() != "6010.5" ||
		observation.Provider != "gexbot_classic" {
		t.Fatalf("observation=%+v value=%s err=%v value_err=%v",
			observation, value, err, valueErr)
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

func TestRecoverCortexDecisionTriggerWakesMaterializesMissedFiring(
	t *testing.T,
) {
	trigger := inputgateway.DecisionTrigger{
		TriggerID: "trigger-mid", Generation: 1,
	}
	sample := inputgateway.DecisionTriggerSample{
		SampleID: "sample-missed", TriggerID: "trigger-mid",
		Generation: 1, Fired: true,
	}
	probe := &decisionTriggerEvaluationStoreProbe{
		pendingOccurrences: []inputgateway.PendingDecisionTriggerOccurrence{{
			Trigger: trigger, Sample: sample,
		}},
	}
	count, err := recoverCortexDecisionTriggerWakes(
		context.Background(), probe, "owner-1",
	)
	if err != nil || count != 1 || len(probe.occurs) != 1 ||
		probe.occurs[0] != sample.SampleID {
		t.Fatalf("count=%d occurrences=%v err=%v",
			count, probe.occurs, err)
	}
}
