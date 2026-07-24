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

const (
	cortexDecisionTriggerInterval = time.Second
	cortexDecisionTriggerLimit    = 100
)

type decisionTriggerEvaluationStore interface {
	ListPendingDecisionTriggerOccurrences(
		context.Context, string, int,
	) ([]inputgateway.PendingDecisionTriggerOccurrence, error)
	ListPendingDecisionTriggerWakes(
		context.Context, string, int,
	) ([]inputgateway.PendingDecisionTriggerWake, error)
	ListDecisionTriggers(
		context.Context, string, int,
	) ([]inputgateway.DecisionTrigger, error)
	RecordDecisionTriggerSample(
		context.Context, string, json.Number, time.Time,
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

type cortexMonitorQuote struct {
	SchemaRevision uint16    `json:"schema_revision"`
	TriggerID      string    `json:"trigger_id"`
	Provider       string    `json:"provider"`
	Symbol         string    `json:"symbol"`
	Bid            string    `json:"bid"`
	Ask            string    `json:"ask"`
	Mid            string    `json:"mid"`
	ObservedAt     time.Time `json:"observed_at"`
	AvailableAt    time.Time `json:"available_at"`
}

type cortexMonitorGEX struct {
	Available        bool                   `json:"available"`
	SchemaRevision   uint16                 `json:"schema_revision"`
	ObservationID    string                 `json:"observation_id"`
	Provider         string                 `json:"provider"`
	ProviderRevision string                 `json:"provider_revision"`
	SourceKind       string                 `json:"source_kind"`
	Symbol           string                 `json:"symbol"`
	Category         string                 `json:"category"`
	SourceTimestamp  time.Time              `json:"source_timestamp"`
	ObservedAt       time.Time              `json:"observed_at"`
	FetchedAt        time.Time              `json:"fetched_at"`
	AvailableAt      time.Time              `json:"available_at"`
	IngestedAt       time.Time              `json:"ingested_at"`
	Raw              cortexMonitorGEXRaw    `json:"raw"`
	Metrics          map[string]json.Number `json:"metrics"`
	QualityState     string                 `json:"quality_state"`
	RecordDigest     string                 `json:"record_digest"`
}

type cortexMonitorGEXRaw struct {
	BlobID        string `json:"blob_id"`
	ContentDigest string `json:"content_digest"`
	SizeBytes     int64  `json:"size_bytes"`
}

type decisionTriggerValueFetch func(
	context.Context, inputgateway.DecisionTrigger,
) (json.Number, time.Time, error)

func startCortexDecisionTriggerEvaluator(
	ctx context.Context,
	store decisionTriggerEvaluationStore,
	kernelClient *http.Client,
	kernelURL string,
	serviceToken string,
	researchClient *http.Client,
	researchURL string,
	researchToken string,
	subjectID string,
) {
	fetch := func(
		callCtx context.Context,
		trigger inputgateway.DecisionTrigger,
	) (json.Number, time.Time, error) {
		switch trigger.DataSource {
		case "kernel_quote":
			quote, err := fetchCortexMonitorQuote(
				callCtx, kernelClient, kernelURL, serviceToken, trigger,
			)
			if err != nil {
				return "", time.Time{}, err
			}
			value, err := decisionTriggerQuoteValue(trigger.Metric, quote)
			return value, quote.ObservedAt, err
		case "research_gexbot":
			observation, err := fetchCortexMonitorGEX(
				callCtx, researchClient, researchURL, researchToken, trigger,
			)
			if err != nil {
				return "", time.Time{}, err
			}
			value, err := decisionTriggerGEXValue(
				trigger.Metric, observation,
			)
			return value, observation.AvailableAt, err
		default:
			return "", time.Time{}, fmt.Errorf(
				"unsupported Trigger data source")
		}
	}
	go func() {
		for {
			callCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			_, recoveryErr := recoverCortexDecisionTriggerWakes(
				callCtx, store, subjectID,
			)
			_, evaluationErr := evaluateCortexDecisionTriggers(
				callCtx, store, fetch, subjectID,
			)
			cancel()
			err := combineDecisionTriggerErrors(
				recoveryErr, evaluationErr,
			)
			if err != nil && ctx.Err() == nil {
				log.Printf("Cortex decision Trigger evaluation: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(cortexDecisionTriggerInterval):
			}
		}
	}()
}

func recoverCortexDecisionTriggerWakes(
	ctx context.Context,
	store decisionTriggerEvaluationStore,
	subjectID string,
) (int, error) {
	pendingOccurrences, occurrenceListErr :=
		store.ListPendingDecisionTriggerOccurrences(
			ctx, subjectID, cortexDecisionTriggerLimit,
		)
	materialized := 0
	var failures []string
	if occurrenceListErr != nil {
		failures = append(failures, occurrenceListErr.Error())
	}
	for _, item := range pendingOccurrences {
		if _, occurrenceErr :=
			store.MaterializeDecisionTriggerOccurrence(
				ctx, item.Sample.SampleID,
			); occurrenceErr != nil {
			failures = append(failures,
				fmt.Sprintf("%s materialize: %v",
					item.Sample.SampleID, occurrenceErr))
			continue
		}
		materialized++
	}
	pending, err := store.ListPendingDecisionTriggerWakes(
		ctx, subjectID, cortexDecisionTriggerLimit,
	)
	if err != nil {
		failures = append(failures, err.Error())
		return materialized, fmt.Errorf(
			"pending decision Trigger recovery: %s",
			strings.Join(failures, "; "))
	}
	recovered := materialized
	for _, item := range pending {
		if _, wakeErr := store.AdmitDecisionTriggerWake(
			ctx, subjectID, item.Trigger, item.Sample, item.Occurrence,
		); wakeErr != nil {
			failures = append(failures,
				fmt.Sprintf("%s: %v",
					item.Occurrence.OccurrenceID, wakeErr))
			continue
		}
		recovered++
	}
	if len(failures) != 0 {
		return recovered, fmt.Errorf(
			"pending wakes: %s", strings.Join(failures, "; "))
	}
	return recovered, nil
}

func combineDecisionTriggerErrors(left, right error) error {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return fmt.Errorf("%v; %v", left, right)
}

func evaluateCortexDecisionTriggers(
	ctx context.Context,
	store decisionTriggerEvaluationStore,
	fetch decisionTriggerValueFetch,
	subjectID string,
) (int, error) {
	triggers, err := store.ListDecisionTriggers(
		ctx, subjectID, cortexDecisionTriggerLimit,
	)
	if err != nil {
		return 0, err
	}
	evaluated := 0
	var failures []string
	for _, trigger := range triggers {
		if !trigger.Enabled {
			continue
		}
		if trigger.DataSource == "moody_blues_replay" {
			continue
		}
		value, observedAt, fetchErr := fetch(ctx, trigger)
		if fetchErr != nil {
			failures = append(failures,
				fmt.Sprintf("%s observation: %v",
					trigger.TriggerID, fetchErr))
			continue
		}
		sample, recordErr := store.RecordDecisionTriggerSample(
			ctx, trigger.TriggerID, value, observedAt,
		)
		if recordErr != nil {
			failures = append(failures,
				fmt.Sprintf("%s record: %v", trigger.TriggerID, recordErr))
			continue
		}
		if sample.Fired {
			occurrence, occurrenceErr :=
				store.MaterializeDecisionTriggerOccurrence(
					ctx, sample.SampleID,
				)
			if occurrenceErr != nil {
				failures = append(failures,
					fmt.Sprintf("%s occurrence: %v",
						trigger.TriggerID, occurrenceErr))
				continue
			}
			if _, wakeErr := store.AdmitDecisionTriggerWake(
				ctx, subjectID, trigger, sample, occurrence,
			); wakeErr != nil {
				failures = append(failures,
					fmt.Sprintf("%s wake: %v",
						trigger.TriggerID, wakeErr))
				continue
			}
		}
		evaluated++
	}
	if len(failures) != 0 {
		return evaluated, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return evaluated, nil
}

func decisionTriggerQuoteValue(
	metric string,
	quote cortexMonitorQuote,
) (json.Number, error) {
	var raw string
	switch metric {
	case "bid_price":
		raw = quote.Bid
	case "ask_price":
		raw = quote.Ask
	case "mid_price":
		raw = quote.Mid
	default:
		return "", fmt.Errorf("unsupported quote metric")
	}
	number := json.Number(raw)
	if _, err := number.Float64(); err != nil || raw == "" ||
		strings.ContainsAny(raw, "eE") {
		return "", fmt.Errorf("invalid quote value")
	}
	return number, nil
}

func decisionTriggerGEXValue(
	metric string,
	observation cortexMonitorGEX,
) (json.Number, error) {
	var key string
	switch metric {
	case "gex_call_wall":
		key = "major_pos_oi"
	case "gex_put_wall":
		key = "major_neg_oi"
	case "gex_zero_gamma":
		key = "zero_gamma"
	default:
		return "", fmt.Errorf("unsupported GEX metric")
	}
	number, found := observation.Metrics[key]
	if !found {
		return "", fmt.Errorf("GEX metric unavailable")
	}
	raw := number.String()
	if _, err := number.Float64(); err != nil || raw == "" ||
		strings.ContainsAny(raw, "eE") {
		return "", fmt.Errorf("invalid GEX value")
	}
	return number, nil
}

func fetchCortexMonitorQuote(
	ctx context.Context,
	client *http.Client,
	kernelURL string,
	serviceToken string,
	trigger inputgateway.DecisionTrigger,
) (cortexMonitorQuote, error) {
	if client == nil || kernelURL == "" || serviceToken == "" {
		return cortexMonitorQuote{}, fmt.Errorf("monitor bridge unavailable")
	}
	body, err := json.Marshal(map[string]string{
		"trigger_id": trigger.TriggerID,
		"symbol":     trigger.Symbol,
	})
	if err != nil {
		return cortexMonitorQuote{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		strings.TrimRight(kernelURL, "/")+
			"/internal/v1/cortex-monitor/quote",
		bytes.NewReader(body),
	)
	if err != nil {
		return cortexMonitorQuote{}, err
	}
	request.Header.Set("Authorization", "Bearer "+serviceToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return cortexMonitorQuote{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (8<<10)+1))
	if err != nil || len(raw) > 8<<10 || response.StatusCode != http.StatusOK {
		return cortexMonitorQuote{},
			fmt.Errorf("Kernel monitor bridge rejected")
	}
	var quote cortexMonitorQuote
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&quote) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		quote.SchemaRevision != 1 ||
		quote.TriggerID != trigger.TriggerID ||
		quote.Symbol != trigger.Symbol ||
		quote.Provider == "" ||
		quote.ObservedAt.IsZero() || quote.AvailableAt.IsZero() ||
		quote.ObservedAt.Location() != time.UTC ||
		quote.AvailableAt.Location() != time.UTC ||
		quote.AvailableAt.Before(quote.ObservedAt) {
		return cortexMonitorQuote{}, fmt.Errorf("invalid Kernel monitor quote")
	}
	if _, err := decisionTriggerQuoteValue(trigger.Metric, quote); err != nil {
		return cortexMonitorQuote{}, err
	}
	return quote, nil
}

func fetchCortexMonitorGEX(
	ctx context.Context,
	client *http.Client,
	researchURL string,
	researchToken string,
	trigger inputgateway.DecisionTrigger,
) (cortexMonitorGEX, error) {
	if client == nil || researchURL == "" || researchToken == "" ||
		trigger.Symbol != "SPX" {
		return cortexMonitorGEX{}, fmt.Errorf(
			"Moody Blues monitor bridge unavailable")
	}
	asOf := time.Now().UTC().Truncate(time.Microsecond)
	body, err := json.Marshal(map[string]string{
		"symbol": trigger.Symbol, "category": "gex_full",
		"as_of": asOf.Format(time.RFC3339Nano),
	})
	if err != nil {
		return cortexMonitorGEX{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		strings.TrimRight(researchURL, "/")+
			"/internal/v1/moody-blues/providers/gexbot-classic/as-of",
		bytes.NewReader(body),
	)
	if err != nil {
		return cortexMonitorGEX{}, err
	}
	request.Header.Set("Authorization", "Bearer "+researchToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return cortexMonitorGEX{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
	if err != nil || len(raw) == 0 || len(raw) > 16<<10 ||
		response.StatusCode != http.StatusOK {
		return cortexMonitorGEX{},
			fmt.Errorf("Moody Blues monitor bridge rejected")
	}
	var observation cortexMonitorGEX
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if decoder.Decode(&observation) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF ||
		!validCortexMonitorGEX(observation, asOf) {
		return cortexMonitorGEX{},
			fmt.Errorf("invalid Moody Blues GEX observation")
	}
	if _, err := decisionTriggerGEXValue(
		trigger.Metric, observation,
	); err != nil {
		return cortexMonitorGEX{}, err
	}
	return observation, nil
}

func validCortexMonitorGEX(
	value cortexMonitorGEX,
	asOf time.Time,
) bool {
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
		!value.AvailableAt.After(asOf) &&
		asOf.Sub(value.AvailableAt) <= 2*time.Minute
}

func validCortexMonitorDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') &&
			(char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
