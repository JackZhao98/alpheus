package main

import (
	"testing"
	"time"
)

func TestExpectedMoodyBluesAvailableAtUsesMarketSession(t *testing.T) {
	tests := []struct {
		name string
		now  string
		want string
	}{
		{
			name: "during session",
			now:  "2026-07-23T15:00:00Z",
			want: "2026-07-23T14:58:00Z",
		},
		{
			name: "after close",
			now:  "2026-07-23T23:00:00Z",
			want: "2026-07-23T20:00:00Z",
		},
		{
			name: "monday before open",
			now:  "2026-07-27T12:00:00Z",
			want: "2026-07-24T20:00:00Z",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, test.now)
			got, err := expectedMoodyBluesAvailableAt(now)
			if err != nil || got.Format(time.RFC3339) != test.want {
				t.Fatalf("got=%s err=%v want=%s", got, err, test.want)
			}
		})
	}
}

func TestNormalizeMoodyBluesResearchHealthDetectsStaleSeries(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-07-23T23:00:00Z")
	source := moodyBluesStatusResponse{
		OK:                  true,
		Provider:            "gexbot_classic",
		CollectorConfigured: true,
	}
	source.Collection.SchemaRevision = 1
	source.Collection.Provider = "gexbot_classic"
	source.Collection.ObservationResolution = "30s"
	source.Collection.CollectionPolicyRevision = "gexbot_spx_30s_et_v1"
	for _, category := range []string{"gex_full", "gex_one", "gex_zero"} {
		source.Collection.Series = append(source.Collection.Series, struct {
			Symbol            string `json:"symbol"`
			Category          string `json:"category"`
			Available         bool   `json:"available"`
			Observations      int64  `json:"observations"`
			LatestObservedAt  string `json:"latest_observed_at"`
			LatestAvailableAt string `json:"latest_available_at"`
		}{
			Symbol:            "SPX",
			Category:          category,
			Available:         true,
			Observations:      10,
			LatestObservedAt:  "2026-07-23T19:59:30Z",
			LatestAvailableAt: "2026-07-23T19:59:31Z",
		})
	}
	result, err := normalizeMoodyBluesResearchHealth(source, now)
	if err != nil || result.Status != "degraded" ||
		result.Series[0].Fresh || result.Series[0].LagSeconds != 29 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
