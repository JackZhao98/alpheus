package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"alpheus/agentplatform/inputgateway"
)

const (
	moodyBluesStatusPath      = "/internal/v1/moody-blues/providers/gexbot-classic/status"
	moodyBluesFreshnessPolicy = "weekday_market_session_2m_v1"
	maxMoodyBluesStatusBytes  = 32 << 10
)

type cortexOperationsOverview struct {
	GeneratedAt string                              `json:"generated_at"`
	Status      string                              `json:"status"`
	Cortex      inputgateway.CortexOperationsHealth `json:"cortex"`
	Research    moodyBluesResearchHealth            `json:"research"`
}

type moodyBluesResearchHealth struct {
	Status              string                   `json:"status"`
	Provider            string                   `json:"provider"`
	CollectorConfigured bool                     `json:"collector_configured"`
	CollectionPolicy    string                   `json:"collection_policy_revision"`
	ObservationCadence  string                   `json:"observation_resolution"`
	FreshnessPolicy     string                   `json:"freshness_policy"`
	ExpectedLatestAt    string                   `json:"expected_latest_at"`
	Series              []moodyBluesSeriesHealth `json:"series"`
}

type moodyBluesSeriesHealth struct {
	Symbol            string `json:"symbol"`
	Category          string `json:"category"`
	Available         bool   `json:"available"`
	Observations      int64  `json:"observations"`
	LatestObservedAt  string `json:"latest_observed_at,omitempty"`
	LatestAvailableAt string `json:"latest_available_at,omitempty"`
	Fresh             bool   `json:"fresh"`
	LagSeconds        int64  `json:"lag_seconds"`
}

type moodyBluesStatusResponse struct {
	OK                  bool   `json:"ok"`
	Provider            string `json:"provider"`
	CollectorConfigured bool   `json:"collector_configured"`
	Collection          struct {
		SchemaRevision           int64  `json:"schema_revision"`
		Provider                 string `json:"provider"`
		ObservationResolution    string `json:"observation_resolution"`
		CollectionPolicyRevision string `json:"collection_policy_revision"`
		Series                   []struct {
			Symbol            string `json:"symbol"`
			Category          string `json:"category"`
			Available         bool   `json:"available"`
			Observations      int64  `json:"observations"`
			LatestObservedAt  string `json:"latest_observed_at"`
			LatestAvailableAt string `json:"latest_available_at"`
		} `json:"series"`
	} `json:"collection"`
}

func getMoodyBluesResearchHealth(
	ctx context.Context,
	client *http.Client,
	researchURL string,
	token string,
	now time.Time,
) (moodyBluesResearchHealth, error) {
	if client == nil || strings.TrimSpace(researchURL) == "" ||
		strings.TrimSpace(token) == "" || now.Location() != time.UTC {
		return moodyBluesResearchHealth{}, fmt.Errorf(
			"invalid Moody Blues health request")
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet,
		strings.TrimRight(researchURL, "/")+moodyBluesStatusPath, nil,
	)
	if err != nil {
		return moodyBluesResearchHealth{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return moodyBluesResearchHealth{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(
		response.Body, maxMoodyBluesStatusBytes+1))
	if err != nil || response.StatusCode != http.StatusOK ||
		len(raw) == 0 || len(raw) > maxMoodyBluesStatusBytes {
		return moodyBluesResearchHealth{}, fmt.Errorf(
			"Moody Blues health HTTP %d", response.StatusCode)
	}
	var source moodyBluesStatusResponse
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&source) != nil ||
		decoder.Decode(&struct{}{}) != io.EOF {
		return moodyBluesResearchHealth{}, fmt.Errorf(
			"invalid Moody Blues health response")
	}
	return normalizeMoodyBluesResearchHealth(source, now)
}

func normalizeMoodyBluesResearchHealth(
	source moodyBluesStatusResponse,
	now time.Time,
) (moodyBluesResearchHealth, error) {
	if now.Location() != time.UTC || !source.OK ||
		source.Provider != "gexbot_classic" ||
		source.Collection.SchemaRevision != 1 ||
		source.Collection.Provider != source.Provider ||
		source.Collection.ObservationResolution != "30s" ||
		source.Collection.CollectionPolicyRevision == "" ||
		len(source.Collection.Series) != 3 {
		return moodyBluesResearchHealth{}, fmt.Errorf(
			"invalid Moody Blues collection declaration")
	}
	expected, err := expectedMoodyBluesAvailableAt(now)
	if err != nil {
		return moodyBluesResearchHealth{}, err
	}
	result := moodyBluesResearchHealth{
		Status:              "healthy",
		Provider:            source.Provider,
		CollectorConfigured: source.CollectorConfigured,
		CollectionPolicy:    source.Collection.CollectionPolicyRevision,
		ObservationCadence:  source.Collection.ObservationResolution,
		FreshnessPolicy:     moodyBluesFreshnessPolicy,
		ExpectedLatestAt:    expected.Format(time.RFC3339Nano),
		Series:              make([]moodyBluesSeriesHealth, 0, 3),
	}
	allowed := map[string]bool{
		"gex_full": false,
		"gex_one":  false,
		"gex_zero": false,
	}
	for _, series := range source.Collection.Series {
		if series.Symbol != "SPX" || series.Observations < 0 {
			return moodyBluesResearchHealth{}, fmt.Errorf(
				"invalid Moody Blues series")
		}
		if _, ok := allowed[series.Category]; !ok ||
			allowed[series.Category] {
			return moodyBluesResearchHealth{}, fmt.Errorf(
				"invalid Moody Blues category")
		}
		allowed[series.Category] = true
		item := moodyBluesSeriesHealth{
			Symbol:       series.Symbol,
			Category:     series.Category,
			Available:    series.Available,
			Observations: series.Observations,
		}
		if series.Available {
			observed, observedErr := time.Parse(
				time.RFC3339Nano, series.LatestObservedAt)
			available, availableErr := time.Parse(
				time.RFC3339Nano, series.LatestAvailableAt)
			if observedErr != nil || availableErr != nil ||
				observed.After(available) || available.After(now) {
				return moodyBluesResearchHealth{}, fmt.Errorf(
					"invalid Moody Blues series timestamps")
			}
			item.LatestObservedAt = observed.UTC().Format(time.RFC3339Nano)
			item.LatestAvailableAt =
				available.UTC().Format(time.RFC3339Nano)
			item.Fresh = !available.Before(expected)
			if available.Before(expected) {
				item.LagSeconds = int64(expected.Sub(available).Seconds())
			}
		}
		if !item.Available || !item.Fresh {
			result.Status = "degraded"
		}
		result.Series = append(result.Series, item)
	}
	if !result.CollectorConfigured {
		result.Status = "degraded"
	}
	sort.Slice(result.Series, func(i, j int) bool {
		return result.Series[i].Category < result.Series[j].Category
	})
	return result, nil
}

func expectedMoodyBluesAvailableAt(now time.Time) (time.Time, error) {
	if now.Location() != time.UTC {
		return time.Time{}, fmt.Errorf("Moody Blues clock must be UTC")
	}
	market, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(market)
	date := time.Date(local.Year(), local.Month(), local.Day(),
		0, 0, 0, 0, market)
	for date.Weekday() == time.Saturday ||
		date.Weekday() == time.Sunday {
		date = date.AddDate(0, 0, -1)
	}
	open := date.Add(9 * time.Hour)
	closeAt := date.Add(16 * time.Hour)
	switch {
	case local.Before(open):
		date = date.AddDate(0, 0, -1)
		for date.Weekday() == time.Saturday ||
			date.Weekday() == time.Sunday {
			date = date.AddDate(0, 0, -1)
		}
		closeAt = date.Add(16 * time.Hour)
	case local.Before(closeAt):
		closeAt = local.Add(-2 * time.Minute)
	}
	return closeAt.UTC(), nil
}
