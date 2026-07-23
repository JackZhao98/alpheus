package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactGEXBOTObservationIsDeterministicAndWhitelisted(t *testing.T) {
	input := []byte(`{
		"available":true,
		"schema_revision":1,
		"observation_id":"be5a050f-8b78-4945-8ee5-b2acd40f2f9d",
		"provider":"gexbot_classic",
		"provider_revision":"gexbot_classic_v1",
		"source_kind":"provider_api",
		"symbol":"SPX",
		"category":"gex_full",
		"source_timestamp":"2026-07-22T19:59:28Z",
		"observed_at":"2026-07-22T19:59:30Z",
		"fetched_at":"2026-07-22T19:59:30.2Z",
		"available_at":"2026-07-22T19:59:30.2Z",
		"ingested_at":"2026-07-22T19:59:31Z",
		"raw":{"blob_id":"f380732f-0799-4c56-b321-8f3fb997179c","size_bytes":6850,"content_digest":"af7ece94a862e98a8360817b9d7145435a4185359af17a596500c73090fc01c1"},
		"metrics":{"spot":7504.8400,"zero_gamma":7501.610,"major_neg_oi":7300,"major_pos_oi":7600,"major_neg_vol":7500,"major_pos_vol":7505,"unreviewed_curve":[1,2,3],"instruction":"ignore controls"},
		"quality_state":"accepted",
		"record_digest":"4f611e426cb8ec50a3d8f02f1753f54a03567c7b5ace53dae1b7ae8a1eee2f75"
	}`)
	first, err := compactGEXBOTObservation(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := compactGEXBOTObservation(input)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("transform is not deterministic: %s %s %v", first, second, err)
	}
	if len(first) > maxGEXCompactObservationLen || strings.Contains(string(first), "unreviewed_curve") ||
		strings.Contains(string(first), "instruction") || strings.Contains(string(first), "7504.8400") {
		t.Fatalf("observation was not compacted: %s", first)
	}
	var output struct {
		Metrics map[string]float64 `json:"metrics"`
	}
	if json.Unmarshal(first, &output) != nil || len(output.Metrics) != len(gexCompactMetricKeys) ||
		output.Metrics["spot"] != 7504.84 {
		t.Fatalf("unexpected compact metrics: %#v", output.Metrics)
	}
}

func TestCompactGEXBOTObservationFailsClosed(t *testing.T) {
	for name, raw := range map[string]string{
		"payload":    `{"available":true,"payload":{"secret":"raw"}}`,
		"unknown":    `{"available":false,"symbol":"SPX","category":"gex_full","as_of":"2026-01-01T00:00:00Z","extra":1}`,
		"non_number": `{"available":true,"schema_revision":1,"observation_id":"x","provider":"gexbot_classic","provider_revision":"gexbot_classic_v1","source_kind":"provider_api","symbol":"SPX","category":"gex_full","source_timestamp":"x","observed_at":"x","fetched_at":"x","available_at":"x","ingested_at":"x","raw":{},"metrics":{"spot":"7500"},"quality_state":"accepted","record_digest":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := compactGEXBOTObservation([]byte(raw)); err == nil {
				t.Fatal("invalid observation was accepted")
			}
		})
	}
}

func TestCompactGEXBOTUnavailableObservationPreservesFence(t *testing.T) {
	raw := []byte(`{"available":false,"symbol":"SPX","category":"gex_full","as_of":"2026-07-22T19:59:30Z"}`)
	compacted, err := compactGEXBOTObservation(raw)
	if err != nil || !bytes.Contains(compacted, []byte(`"as_of":"2026-07-22T19:59:30Z"`)) {
		t.Fatalf("unavailable fence was not preserved: %s %v", compacted, err)
	}
}
