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
	cortexDecisionTriggerInterval = 15 * time.Second
	cortexDecisionTriggerLimit    = 100
)

type decisionTriggerEvaluationStore interface {
	ListDecisionTriggers(
		context.Context, string, int,
	) ([]inputgateway.DecisionTrigger, error)
	RecordDecisionTriggerSample(
		context.Context, string, json.Number, time.Time,
	) (inputgateway.DecisionTriggerSample, error)
	MaterializeDecisionTriggerOccurrence(
		context.Context, string,
	) (inputgateway.DecisionTriggerOccurrence, error)
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

type decisionTriggerQuoteFetch func(
	context.Context, inputgateway.DecisionTrigger,
) (cortexMonitorQuote, error)

func startCortexDecisionTriggerEvaluator(
	ctx context.Context,
	store decisionTriggerEvaluationStore,
	client *http.Client,
	kernelURL string,
	serviceToken string,
	subjectID string,
) {
	fetch := func(
		callCtx context.Context,
		trigger inputgateway.DecisionTrigger,
	) (cortexMonitorQuote, error) {
		return fetchCortexMonitorQuote(
			callCtx, client, kernelURL, serviceToken, trigger,
		)
	}
	go func() {
		for {
			callCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
			_, err := evaluateCortexDecisionTriggers(
				callCtx, store, fetch, subjectID,
			)
			cancel()
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

func evaluateCortexDecisionTriggers(
	ctx context.Context,
	store decisionTriggerEvaluationStore,
	fetch decisionTriggerQuoteFetch,
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
		if !trigger.Enabled || trigger.DataSource != "kernel_quote" {
			continue
		}
		quote, quoteErr := fetch(ctx, trigger)
		if quoteErr != nil {
			failures = append(failures,
				fmt.Sprintf("%s quote: %v", trigger.TriggerID, quoteErr))
			continue
		}
		value, valueErr := decisionTriggerQuoteValue(trigger.Metric, quote)
		if valueErr != nil {
			failures = append(failures,
				fmt.Sprintf("%s value: %v", trigger.TriggerID, valueErr))
			continue
		}
		sample, recordErr := store.RecordDecisionTriggerSample(
			ctx, trigger.TriggerID, value, quote.ObservedAt,
		)
		if recordErr != nil {
			failures = append(failures,
				fmt.Sprintf("%s record: %v", trigger.TriggerID, recordErr))
			continue
		}
		if sample.Fired {
			if _, occurrenceErr := store.MaterializeDecisionTriggerOccurrence(
				ctx, sample.SampleID,
			); occurrenceErr != nil {
				failures = append(failures,
					fmt.Sprintf("%s occurrence: %v",
						trigger.TriggerID, occurrenceErr))
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
