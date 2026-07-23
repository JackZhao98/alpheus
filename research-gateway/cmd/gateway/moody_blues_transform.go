package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

const (
	gexCompactTransformID       = "gex_compact_v1"
	maxGEXCompactObservationLen = 16 << 10
)

var gexCompactMetricKeys = []string{
	"major_neg_oi",
	"major_neg_vol",
	"major_pos_oi",
	"major_pos_vol",
	"spot",
	"zero_gamma",
}

func gexCompactTransformDescriptor() moodyBluesTransform {
	return moodyBluesTransform{
		ID:                 gexCompactTransformID,
		Revision:           1,
		InputDataClass:     "options_gamma_snapshot",
		OutputDataClass:    "options_gamma_compact",
		Deterministic:      true,
		MaxOutputBytes:     maxGEXCompactObservationLen,
		SelectedMetricKeys: append([]string(nil), gexCompactMetricKeys...),
	}
}

// compactGEXBOTObservation is the first deterministic Moody Blues transform.
// It keeps the immutable temporal/provenance envelope, selects a small reviewed
// metric set, canonicalizes finite numbers, and rejects raw payload bytes or
// unexpected top-level fields. It performs no market interpretation.
func compactGEXBOTObservation(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > maxGEXBOTProviderResponseBytes {
		return nil, fmt.Errorf("GEX observation size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var observation map[string]any
	if decoder.Decode(&observation) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("GEX observation is not one JSON object")
	}
	if payload, exists := observation["payload"]; exists || payload != nil {
		return nil, fmt.Errorf("raw GEX payload is forbidden")
	}
	available, ok := observation["available"].(bool)
	if !ok {
		return nil, fmt.Errorf("GEX availability is missing")
	}
	if !available {
		if !exactKeys(observation, "available", "symbol", "category", "as_of") {
			return nil, fmt.Errorf("unavailable GEX observation is not compact")
		}
		return marshalCompactObservation(observation)
	}
	if !exactKeys(observation,
		"available", "schema_revision", "observation_id", "provider", "provider_revision", "source_kind",
		"symbol", "category", "source_timestamp", "observed_at", "fetched_at", "available_at", "ingested_at",
		"raw", "metrics", "quality_state", "record_digest") {
		return nil, fmt.Errorf("available GEX observation has unexpected fields")
	}
	metrics, ok := observation["metrics"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("GEX metrics are invalid")
	}
	compacted := make(map[string]any, len(gexCompactMetricKeys))
	for _, key := range gexCompactMetricKeys {
		value, found := metrics[key]
		if !found {
			continue
		}
		number, ok := value.(json.Number)
		if !ok {
			return nil, fmt.Errorf("GEX metric %s is not numeric", key)
		}
		parsed, err := number.Float64()
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, fmt.Errorf("GEX metric %s is invalid", key)
		}
		compacted[key] = parsed
	}
	observation["metrics"] = compacted
	return marshalCompactObservation(observation)
}

func exactKeys(value map[string]any, keys ...string) bool {
	if len(value) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, found := value[key]; !found {
			return false
		}
	}
	return true
}

func marshalCompactObservation(value map[string]any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > maxGEXCompactObservationLen {
		return nil, fmt.Errorf("compact GEX observation is invalid")
	}
	return raw, nil
}
