package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"alpheus/agentplatform/inputgateway"
)

const maxMoodyBluesStreamResponseBytes = 64 << 10

type moodyBluesReplayCreateRequest struct {
	RequestID string `json:"request_id"`
	Symbol    string `json:"symbol"`
	Category  string `json:"category"`
	Start     string `json:"start_available_at"`
	End       string `json:"end_available_at"`
	AsOf      string `json:"as_of"`
}

type moodyBluesReplayStepRequest struct {
	Generation int64 `json:"generation"`
}

type moodyBluesReplayTriggerStore interface {
	ListDecisionTriggers(
		context.Context, string, int,
	) ([]inputgateway.DecisionTrigger, error)
	RecordMoodyBluesReplayDecisionTriggerSample(
		context.Context, string, json.Number, time.Time,
		string, int64, string, string, json.RawMessage,
	) (inputgateway.DecisionTriggerSample, error)
	MaterializeDecisionTriggerOccurrence(
		context.Context, string,
	) (inputgateway.DecisionTriggerOccurrence, error)
	AdmitDecisionTriggerWake(
		context.Context, string, inputgateway.DecisionTrigger,
		inputgateway.DecisionTriggerSample,
		inputgateway.DecisionTriggerOccurrence,
	) (inputgateway.DecisionTriggerWake, error)
}

type moodyBluesReplayEnvelope struct {
	SchemaRevision uint16            `json:"schema_revision"`
	ReplayID       string            `json:"replay_id"`
	State          string            `json:"state"`
	Generation     int64             `json:"generation"`
	Observation    *cortexMonitorGEX `json:"observation"`
}

type moodyBluesReplayTriggerEvaluation struct {
	TriggerID  string                                  `json:"trigger_id"`
	Metric     string                                  `json:"metric"`
	Sample     inputgateway.DecisionTriggerSample      `json:"sample"`
	Occurrence *inputgateway.DecisionTriggerOccurrence `json:"occurrence,omitempty"`
	Wake       *inputgateway.DecisionTriggerWake       `json:"wake,omitempty"`
}

func registerMoodyBluesStreamHandlers(
	mux *http.ServeMux,
	serviceToken string,
	client *http.Client,
	researchURL string,
	researchToken string,
	triggerStore moodyBluesReplayTriggerStore,
	subjectID string,
) {
	if mux == nil {
		return
	}
	mux.HandleFunc(
		"POST /v1/data-streams/gexbot/replays",
		func(w http.ResponseWriter, r *http.Request) {
			if !validBearer(r, serviceToken) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var input moodyBluesReplayCreateRequest
			if !decodeMoodyBluesStreamJSON(w, r, 8<<10, &input) {
				return
			}
			input.RequestID = strings.TrimSpace(input.RequestID)
			input.Symbol = strings.ToUpper(strings.TrimSpace(input.Symbol))
			input.Category = strings.TrimSpace(input.Category)
			start, startErr := time.Parse(
				time.RFC3339Nano, strings.TrimSpace(input.Start),
			)
			end, endErr := time.Parse(
				time.RFC3339Nano, strings.TrimSpace(input.End),
			)
			asOf, asOfErr := time.Parse(
				time.RFC3339Nano, strings.TrimSpace(input.AsOf),
			)
			if input.RequestID == "" || len(input.RequestID) > 200 ||
				input.Symbol != "SPX" ||
				!validMoodyBluesCategory(input.Category) ||
				startErr != nil || endErr != nil || asOfErr != nil ||
				end.Before(start) || asOf.Before(end) ||
				asOf.After(time.Now().UTC()) {
				writeMoodyBluesStreamError(
					w, http.StatusBadRequest, "moody_blues_replay_invalid",
				)
				return
			}
			input.Start = start.UTC().Format(time.RFC3339Nano)
			input.End = end.UTC().Format(time.RFC3339Nano)
			input.AsOf = asOf.UTC().Format(time.RFC3339Nano)
			proxyMoodyBluesStream(
				w, r, client, researchURL, researchToken,
				"/internal/v1/moody-blues/providers/gexbot-classic/replays",
				input,
			)
		},
	)
	mux.HandleFunc(
		"POST /v1/data-streams/gexbot/replays/{id}/next",
		func(w http.ResponseWriter, r *http.Request) {
			if !validBearer(r, serviceToken) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			replayID := strings.TrimSpace(r.PathValue("id"))
			var input moodyBluesReplayStepRequest
			if !decodeMoodyBluesStreamJSON(w, r, 4<<10, &input) {
				return
			}
			if !validMoodyBluesReplayID(replayID) ||
				input.Generation < 1 {
				writeMoodyBluesStreamError(
					w, http.StatusBadRequest, "moody_blues_cursor_invalid",
				)
				return
			}
			path := "/internal/v1/moody-blues/providers/" +
				"gexbot-classic/replays/" + replayID + "/next"
			raw, status, code := requestMoodyBluesStream(
				r, client, researchURL, researchToken, path, input,
			)
			if code != "" {
				writeMoodyBluesStreamError(w, status, code)
				return
			}
			if triggerStore != nil {
				evaluations, err :=
					evaluateMoodyBluesReplayFrame(
						r.Context(), triggerStore, subjectID,
						replayID, raw,
					)
				if err != nil {
					log.Printf(
						"Moody Blues replay Trigger evaluation: %v",
						err,
					)
					writeMoodyBluesStreamError(
						w, http.StatusBadGateway,
						"moody_blues_replay_evaluation_failed",
					)
					return
				}
				var envelope map[string]json.RawMessage
				if json.Unmarshal(raw, &envelope) != nil {
					writeMoodyBluesStreamError(
						w, http.StatusBadGateway,
						"moody_blues_response_invalid",
					)
					return
				}
				envelope["trigger_evaluations"], err =
					json.Marshal(evaluations)
				if err != nil {
					writeMoodyBluesStreamError(
						w, http.StatusBadGateway,
						"moody_blues_response_invalid",
					)
					return
				}
				raw, err = json.Marshal(envelope)
				if err != nil {
					writeMoodyBluesStreamError(
						w, http.StatusBadGateway,
						"moody_blues_response_invalid",
					)
					return
				}
			}
			raw, code = sanitizeMoodyBluesStreamResponse(raw)
			if code != "" {
				writeMoodyBluesStreamError(
					w, http.StatusBadGateway, code,
				)
				return
			}
			writeMoodyBluesStreamResponse(w, raw)
		},
	)
}

func proxyMoodyBluesStream(
	w http.ResponseWriter,
	incoming *http.Request,
	client *http.Client,
	researchURL string,
	researchToken string,
	path string,
	input any,
) {
	raw, status, code := requestMoodyBluesStream(
		incoming, client, researchURL, researchToken, path, input,
	)
	if code != "" {
		writeMoodyBluesStreamError(w, status, code)
		return
	}
	raw, code = sanitizeMoodyBluesStreamResponse(raw)
	if code != "" {
		writeMoodyBluesStreamError(
			w, http.StatusBadGateway, code,
		)
		return
	}
	writeMoodyBluesStreamResponse(w, raw)
}

func requestMoodyBluesStream(
	incoming *http.Request,
	client *http.Client,
	researchURL string,
	researchToken string,
	path string,
	input any,
) ([]byte, int, string) {
	if client == nil || strings.TrimSpace(researchURL) == "" ||
		strings.TrimSpace(researchToken) == "" {
		return nil, http.StatusServiceUnavailable,
			"moody_blues_unavailable"
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, http.StatusBadRequest,
			"moody_blues_request_invalid"
	}
	request, err := http.NewRequestWithContext(
		incoming.Context(), http.MethodPost,
		strings.TrimRight(researchURL, "/")+path,
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, http.StatusBadGateway, "moody_blues_unavailable"
	}
	request.Header.Set("Authorization", "Bearer "+researchToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, http.StatusBadGateway, "moody_blues_unavailable"
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(
		response.Body, maxMoodyBluesStreamResponseBytes+1,
	))
	if err != nil || len(raw) == 0 ||
		len(raw) > maxMoodyBluesStreamResponseBytes ||
		!json.Valid(raw) {
		return nil, http.StatusBadGateway,
			"moody_blues_response_invalid"
	}
	if response.StatusCode == http.StatusConflict {
		return nil, http.StatusConflict,
			"moody_blues_generation_conflict"
	}
	if response.StatusCode/100 != 2 {
		return nil, http.StatusBadGateway, "moody_blues_unavailable"
	}
	return raw, http.StatusOK, ""
}

func sanitizeMoodyBluesStreamResponse(
	raw []byte,
) ([]byte, string) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil ||
		envelope["payload"] != nil ||
		envelope["raw"] != nil {
		return nil, "moody_blues_response_invalid"
	}
	if observation := envelope["observation"]; observation != nil &&
		string(observation) != "null" {
		var value map[string]json.RawMessage
		if json.Unmarshal(observation, &value) != nil ||
			value["payload"] != nil {
			return nil, "moody_blues_response_invalid"
		}
		delete(value, "raw")
		sanitizedObservation, err := json.Marshal(value)
		if err != nil {
			return nil, "moody_blues_response_invalid"
		}
		envelope["observation"] = sanitizedObservation
		raw, err = json.Marshal(envelope)
		if err != nil {
			return nil, "moody_blues_response_invalid"
		}
	}
	return raw, ""
}

func writeMoodyBluesStreamResponse(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

func evaluateMoodyBluesReplayFrame(
	ctx context.Context,
	store moodyBluesReplayTriggerStore,
	subjectID string,
	replayID string,
	raw []byte,
) ([]moodyBluesReplayTriggerEvaluation, error) {
	var envelope moodyBluesReplayEnvelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&envelope) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		envelope.SchemaRevision != 1 ||
		envelope.ReplayID != replayID ||
		envelope.Generation < 2 ||
		(envelope.State != "active" && envelope.State != "complete") {
		return nil, fmt.Errorf("invalid Moody Blues replay envelope")
	}
	observation := envelope.Observation
	if observation == nil || observation.Category != "gex_full" {
		return []moodyBluesReplayTriggerEvaluation{}, nil
	}
	if !validCortexReplayGEX(*observation) {
		return nil, fmt.Errorf("invalid Moody Blues replay observation")
	}
	metrics := make(map[string]string, 4)
	for _, key := range []string{
		"spot", "zero_gamma", "major_pos_oi", "major_neg_oi",
	} {
		number, found := observation.Metrics[key]
		if !found || !validMoodyBluesReplayNumber(number) {
			return nil, fmt.Errorf(
				"invalid Moody Blues normalized metric")
		}
		metrics[key] = number.String()
	}
	normalized, err := json.Marshal(map[string]any{
		"schema_revision":           1,
		"transform_id":              "gex_compact_v1",
		"replay_id":                 replayID,
		"replay_generation":         envelope.Generation,
		"observation_id":            observation.ObservationID,
		"observation_record_digest": observation.RecordDigest,
		"symbol":                    observation.Symbol,
		"category":                  observation.Category,
		"virtual_observed_at": observation.SourceTimestamp.UTC().
			Format(time.RFC3339Nano),
		"metrics": metrics,
	})
	if err != nil {
		return nil, err
	}
	triggers, err := store.ListDecisionTriggers(ctx, subjectID, 100)
	if err != nil {
		return nil, err
	}
	evaluations := make([]moodyBluesReplayTriggerEvaluation, 0)
	for _, trigger := range triggers {
		if !trigger.Enabled ||
			trigger.DataSource != "moody_blues_replay" ||
			trigger.Symbol != observation.Symbol {
			continue
		}
		value, valueErr := decisionTriggerGEXValue(
			trigger.Metric, *observation,
		)
		if valueErr != nil {
			return nil, valueErr
		}
		sample, recordErr :=
			store.RecordMoodyBluesReplayDecisionTriggerSample(
				ctx, trigger.TriggerID, value,
				observation.SourceTimestamp.UTC(), replayID,
				envelope.Generation, observation.ObservationID,
				observation.RecordDigest, normalized,
			)
		if recordErr != nil {
			return nil, recordErr
		}
		evaluation := moodyBluesReplayTriggerEvaluation{
			TriggerID: trigger.TriggerID,
			Metric:    trigger.Metric,
			Sample:    sample,
		}
		if sample.Fired {
			occurrence, occurrenceErr :=
				store.MaterializeDecisionTriggerOccurrence(
					ctx, sample.SampleID,
				)
			if occurrenceErr != nil {
				return nil, occurrenceErr
			}
			wake, wakeErr := store.AdmitDecisionTriggerWake(
				ctx, subjectID, trigger, sample, occurrence,
			)
			if wakeErr != nil {
				return nil, wakeErr
			}
			evaluation.Occurrence = &occurrence
			evaluation.Wake = &wake
		}
		evaluations = append(evaluations, evaluation)
	}
	return evaluations, nil
}

func validCortexReplayGEX(value cortexMonitorGEX) bool {
	return value.SchemaRevision == 1 &&
		value.ObservationID != "" &&
		value.Provider == "gexbot_classic" &&
		value.ProviderRevision != "" &&
		value.SourceKind != "" && value.Symbol == "SPX" &&
		value.Category == "gex_full" &&
		value.QualityState == "accepted" &&
		validCortexMonitorDigest(value.RecordDigest) &&
		value.Raw.BlobID != "" &&
		validCortexMonitorDigest(value.Raw.ContentDigest) &&
		value.Raw.SizeBytes > 0 && len(value.Metrics) > 0 &&
		!value.SourceTimestamp.IsZero() &&
		!value.ObservedAt.IsZero() && !value.FetchedAt.IsZero() &&
		!value.AvailableAt.IsZero() && !value.IngestedAt.IsZero() &&
		value.SourceTimestamp.Location() == time.UTC &&
		value.ObservedAt.Location() == time.UTC &&
		value.FetchedAt.Location() == time.UTC &&
		value.AvailableAt.Location() == time.UTC &&
		value.IngestedAt.Location() == time.UTC &&
		!value.FetchedAt.Before(value.ObservedAt) &&
		!value.AvailableAt.Before(value.FetchedAt) &&
		!value.AvailableAt.After(time.Now().UTC())
}

func validMoodyBluesReplayNumber(value json.Number) bool {
	raw := value.String()
	if raw == "" || strings.ContainsAny(raw, "eE") {
		return false
	}
	_, err := value.Float64()
	return err == nil
}

func decodeMoodyBluesStreamJSON(
	w http.ResponseWriter,
	r *http.Request,
	limit int64,
	target any,
) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF {
		writeMoodyBluesStreamError(
			w, http.StatusBadRequest, "moody_blues_request_invalid",
		)
		return false
	}
	return true
}

func validMoodyBluesCategory(value string) bool {
	return value == "gex_full" ||
		value == "gex_zero" ||
		value == "gex_one"
}

func validMoodyBluesReplayID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func writeMoodyBluesStreamError(
	w http.ResponseWriter, status int, code string,
) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":      http.StatusText(status),
		"error_code": code,
	})
}
