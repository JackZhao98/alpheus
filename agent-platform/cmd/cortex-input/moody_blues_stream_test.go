package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/inputgateway"
)

type replayTriggerStoreStub struct {
	t       *testing.T
	trigger inputgateway.DecisionTrigger
}

func (s replayTriggerStoreStub) ListDecisionTriggers(
	_ context.Context, subjectID string, limit int,
) ([]inputgateway.DecisionTrigger, error) {
	if subjectID != "owner-1" || limit != 100 {
		s.t.Fatalf("subject=%s limit=%d", subjectID, limit)
	}
	return []inputgateway.DecisionTrigger{s.trigger}, nil
}

func (s replayTriggerStoreStub) RecordMoodyBluesReplayDecisionTriggerSample(
	_ context.Context,
	triggerID string,
	value json.Number,
	virtualObservedAt time.Time,
	replayID string,
	replayGeneration int64,
	observationID string,
	observationRecordDigest string,
	normalized json.RawMessage,
) (inputgateway.DecisionTriggerSample, error) {
	if triggerID != s.trigger.TriggerID || value.String() != "7519.74" ||
		virtualObservedAt.Format(time.RFC3339Nano) !=
			"2026-07-22T20:00:00Z" ||
		replayID != "11111111-1111-4111-8111-111111111111" ||
		replayGeneration != 2 || observationID !=
		"22222222-2222-4222-8222-222222222222" ||
		observationRecordDigest != strings.Repeat("a", 64) ||
		!strings.Contains(
			string(normalized), `"transform_id":"gex_compact_v1"`,
		) ||
		strings.Contains(string(normalized), `"raw"`) {
		s.t.Fatalf(
			"unexpected normalized replay sample: %s", normalized,
		)
	}
	return inputgateway.DecisionTriggerSample{
		SampleID:     "33333333-3333-4333-8333-333333333333",
		TriggerID:    triggerID,
		Generation:   1,
		Value:        value,
		ConditionMet: true,
		Fired:        true,
		ReasonCode:   "threshold_met",
		ObservedAt: virtualObservedAt.Format(
			time.RFC3339Nano,
		),
		CommittedAt: "2026-07-24T08:00:00Z",
	}, nil
}

func (s replayTriggerStoreStub) MaterializeDecisionTriggerOccurrence(
	_ context.Context, sampleID string,
) (inputgateway.DecisionTriggerOccurrence, error) {
	if sampleID != "33333333-3333-4333-8333-333333333333" {
		s.t.Fatalf("sample=%s", sampleID)
	}
	return inputgateway.DecisionTriggerOccurrence{
		Status:             "materialized",
		SampleID:           sampleID,
		TriggerID:          s.trigger.TriggerID,
		OccurrenceID:       "44444444-4444-4444-8444-444444444444",
		OccurrenceDigest:   strings.Repeat("b", 64),
		SourceRecordDigest: strings.Repeat("c", 64),
		OccurredAt:         "2026-07-22T20:00:00Z",
	}, nil
}

func (s replayTriggerStoreStub) AdmitDecisionTriggerWake(
	_ context.Context,
	subjectID string,
	trigger inputgateway.DecisionTrigger,
	sample inputgateway.DecisionTriggerSample,
	occurrence inputgateway.DecisionTriggerOccurrence,
) (inputgateway.DecisionTriggerWake, error) {
	if subjectID != "owner-1" ||
		trigger.TriggerID != s.trigger.TriggerID ||
		sample.SampleID == "" || occurrence.OccurrenceID == "" {
		s.t.Fatal("invalid replay wake inputs")
	}
	return inputgateway.DecisionTriggerWake{
		Status:       "admitted",
		OccurrenceID: occurrence.OccurrenceID,
		RunID:        "55555555-5555-4555-8555-555555555555",
		RootTaskID:   "66666666-6666-4666-8666-666666666666",
		RunState:     "queued",
		TaskState:    "ready",
	}, nil
}

func TestMoodyBluesReplayStreamProxiesWithoutRawBlob(t *testing.T) {
	research := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		if r.URL.Path !=
			"/internal/v1/moody-blues/providers/gexbot-classic/replays/replay-id/next" &&
			r.URL.Path !=
				"/internal/v1/moody-blues/providers/gexbot-classic/replays/11111111-1111-4111-8111-111111111111/next" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer research-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
		  "schema_revision":1,
		  "replay_id":"11111111-1111-4111-8111-111111111111",
		  "state":"active",
		  "generation":2,
		  "observation":{
		    "observation_id":"obs-1",
		    "available_at":"2026-07-23T20:00:00Z",
		    "metrics":{"spot":6400,"zero_gamma":6350},
		    "raw":{"blob_id":"secret-metadata"}
		  }
		}`)
	}))
	defer research.Close()

	mux := http.NewServeMux()
	registerMoodyBluesStreamHandlers(
		mux, "service-secret", research.Client(),
		research.URL, "research-secret",
		nil, "",
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/data-streams/gexbot/replays/11111111-1111-4111-8111-111111111111/next",
		strings.NewReader(`{"generation":1}`),
	)
	request.Header.Set("Authorization", "Bearer service-secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"zero_gamma":6350`) ||
		strings.Contains(response.Body.String(), `"raw"`) ||
		strings.Contains(response.Body.String(), "secret-metadata") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMoodyBluesReplayStreamRejectsInvalidOrStaleRequests(t *testing.T) {
	mux := http.NewServeMux()
	registerMoodyBluesStreamHandlers(
		mux, "service-secret", http.DefaultClient,
		"http://research.invalid", "research-secret",
		nil, "",
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/data-streams/gexbot/replays/not-a-replay/next",
		strings.NewReader(`{"generation":1}`),
	)
	request.Header.Set("Authorization", "Bearer service-secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(),
			`"error_code":"moody_blues_cursor_invalid"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/v1/data-streams/gexbot/replays/11111111-1111-4111-8111-111111111111/next",
		strings.NewReader(`{"generation":0}`),
	)
	request.Header.Set("Authorization", "Bearer service-secret")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMoodyBluesReplayFrameNormalizesAndWakesCortex(t *testing.T) {
	trigger := inputgateway.DecisionTrigger{
		TriggerID:       "77777777-7777-4777-8777-777777777777",
		Generation:      1,
		Title:           "Replay zero gamma review",
		StrategyID:      "gamma_replay",
		DataSource:      "moody_blues_replay",
		Symbol:          "SPX",
		Metric:          "gex_zero_gamma",
		Comparator:      "gte",
		Threshold:       json.Number("7500"),
		CooldownSeconds: 60,
		Objective:       "Review the simulated SPX Gamma frame.",
		Enabled:         true,
		State:           "armed",
		UpdatedAt:       "2026-07-24T08:00:00Z",
	}
	raw := []byte(`{
	  "schema_revision":1,
	  "replay_id":"11111111-1111-4111-8111-111111111111",
	  "state":"active",
	  "generation":2,
	  "observation":{
	    "available":true,
	    "schema_revision":1,
	    "observation_id":"22222222-2222-4222-8222-222222222222",
	    "provider":"gexbot_classic",
	    "provider_revision":"classic-v1",
	    "source_kind":"collector_push",
	    "symbol":"SPX",
	    "category":"gex_full",
	    "source_timestamp":"2026-07-22T20:00:00Z",
	    "observed_at":"2026-07-22T20:00:01Z",
	    "fetched_at":"2026-07-22T20:00:02Z",
	    "available_at":"2026-07-22T20:00:03Z",
	    "ingested_at":"2026-07-22T20:00:03Z",
	    "raw":{
	      "blob_id":"88888888-8888-4888-8888-888888888888",
	      "content_digest":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
	      "size_bytes":512
	    },
	    "metrics":{
	      "spot":7498.48,
	      "zero_gamma":7519.74,
	      "major_pos_oi":7600,
	      "major_neg_oi":7500
	    },
	    "quality_state":"accepted",
	    "record_digest":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	  }
	}`)
	evaluations, err := evaluateMoodyBluesReplayFrame(
		context.Background(),
		replayTriggerStoreStub{t: t, trigger: trigger},
		"owner-1",
		"11111111-1111-4111-8111-111111111111",
		raw,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluations) != 1 ||
		evaluations[0].TriggerID != trigger.TriggerID ||
		evaluations[0].Sample.Value.String() != "7519.74" ||
		evaluations[0].Occurrence == nil ||
		evaluations[0].Wake == nil ||
		evaluations[0].Wake.RunID == "" {
		t.Fatalf("evaluations=%+v", evaluations)
	}
}
