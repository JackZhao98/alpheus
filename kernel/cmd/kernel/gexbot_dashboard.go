package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"alpheus/kernel/internal/store"
)

// gexbotDashboardStore is intentionally read-only. The public dashboard can
// never configure collection, read a credential, or reach a broker capability.
type gexbotDashboardStore interface {
	ListGEXBotObservationHistory(symbol, category string, start, end time.Time) ([]store.GEXBotObservation, error)
	LoadLatestGEXBotObservation(symbol, category string, start, end time.Time) (*store.GEXBotObservation, error)
}

type gexbotDashboardPoint struct {
	ObservedAt      time.Time `json:"observed_at"`
	SourceTimestamp time.Time `json:"source_timestamp"`
	Spot            *float64  `json:"spot"`
	ZeroGamma       *float64  `json:"zero_gamma"`
	MajorPosVol     *float64  `json:"major_pos_vol"`
	MajorPosOI      *float64  `json:"major_pos_oi"`
	MajorNegVol     *float64  `json:"major_neg_vol"`
	MajorNegOI      *float64  `json:"major_neg_oi"`
}

type gexbotDashboardProfile struct {
	gexbotDashboardPoint
	Strikes [][]float64 `json:"strikes"`
}

func (s *server) getPublicGEXDashboard(w http.ResponseWriter, r *http.Request) {
	data, ok := s.store.(gexbotDashboardStore)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "GEX dashboard unavailable"})
		return
	}
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "gex_full"
	}
	if !validGEXBotClassicCategory(category) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid GEX category"})
		return
	}
	start, end, day, err := gexDashboardDayBounds(r.URL.Query().Get("date"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid dashboard date"})
		return
	}
	history, err := data.ListGEXBotObservationHistory(gexbotCollectionSymbol, category, start, end)
	if err != nil {
		writeStoreError(w, "load GEX history", err)
		return
	}
	latest, err := data.LoadLatestGEXBotObservation(gexbotCollectionSymbol, category, start, end)
	if err != nil {
		writeStoreError(w, "load latest GEX profile", err)
		return
	}
	points := make([]gexbotDashboardPoint, 0, len(history))
	for _, value := range history {
		points = append(points, gexbotDashboardPoint{
			ObservedAt: value.ObservedAt, SourceTimestamp: value.SourceTimestamp, Spot: value.Spot, ZeroGamma: value.ZeroGamma,
			MajorPosVol: value.MajorPosVol, MajorPosOI: value.MajorPosOI, MajorNegVol: value.MajorNegVol, MajorNegOI: value.MajorNegOI,
		})
	}
	var profile *gexbotDashboardProfile
	if latest != nil {
		profile = &gexbotDashboardProfile{gexbotDashboardPoint: gexbotDashboardPoint{
			ObservedAt: latest.ObservedAt, SourceTimestamp: latest.SourceTimestamp, Spot: latest.Spot, ZeroGamma: latest.ZeroGamma,
			MajorPosVol: latest.MajorPosVol, MajorPosOI: latest.MajorPosOI, MajorNegVol: latest.MajorNegVol, MajorNegOI: latest.MajorNegOI,
		}}
		var raw struct {
			Strikes [][]float64 `json:"strikes"`
		}
		if err := json.Unmarshal(latest.Payload, &raw); err == nil {
			profile.Strikes = raw.Strikes
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"symbol": gexbotCollectionSymbol, "category": category, "date": day, "timezone": "America/New_York",
		"schedule": map[string]any{"cadence_seconds": 30, "start": "09:00", "end": "16:00", "weekdays_only": true},
		"history":  historyToJSON(points), "latest": profile,
	})
}

func historyToJSON(values []gexbotDashboardPoint) []gexbotDashboardPoint {
	if values == nil {
		return []gexbotDashboardPoint{}
	}
	return values
}

func gexDashboardDayBounds(raw string) (time.Time, time.Time, string, error) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	day := today
	if strings.TrimSpace(raw) != "" {
		parsed, parseErr := time.ParseInLocation(time.DateOnly, raw, loc)
		if parseErr != nil || parsed.After(today) || parsed.Before(today.AddDate(0, 0, -30)) {
			return time.Time{}, time.Time{}, "", fmt.Errorf("date outside dashboard retention window")
		}
		day = parsed
	}
	return day.UTC(), day.AddDate(0, 0, 1).UTC(), day.Format(time.DateOnly), nil
}
