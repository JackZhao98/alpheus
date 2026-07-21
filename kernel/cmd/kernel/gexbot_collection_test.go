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

	"alpheus/kernel/internal/config"
	"alpheus/kernel/internal/store"
)

type gexbotRoundTripper func(*http.Request) (*http.Response, error)

func (f gexbotRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestFetchGEXBotClassicReadsOnceWithoutRecording(t *testing.T) {
	st := newMemoryStore()
	key := "gexbot_test_0123456789abcdef"
	ciphertext, err := sealAgentSecret(strings.Repeat("k", 32), gexbotCollectionSecret, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutAgentSecret(gexbotCollectionSecret, ciphertext); err != nil {
		t.Fatal(err)
	}
	called := 0
	s := &server{
		mode: config.ModeConfig{AgentWebSessionKey: strings.Repeat("k", 32)}, store: st,
		gexHTTP: &http.Client{Transport: gexbotRoundTripper(func(request *http.Request) (*http.Response, error) {
			called++
			if request.URL.Path != "/v2/SPX/classic/gex_full" || request.Header.Get("Authorization") != "Bearer "+key {
				t.Fatalf("unexpected request path=%s authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"timestamp":1784592000,"ticker":"SPX","spot":6300.25,"zero_gamma":6250}`)), Header: make(http.Header)}, nil
		})},
	}
	snapshot, err := s.fetchGEXBotClassic(context.Background(), "spx", "gex_full")
	if err != nil {
		t.Fatal(err)
	}
	if called != 1 || snapshot.Symbol != "SPX" || snapshot.Category != "gex_full" || snapshot.Spot == nil || *snapshot.Spot != 6300.25 || snapshot.ZeroGamma == nil || *snapshot.ZeroGamma != 6250 {
		t.Fatalf("called=%d snapshot=%+v", called, snapshot)
	}
}

func TestGEXBotCollectionWindowIsRegularSessionOnly(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"open", time.Date(2026, 7, 21, 9, 0, 0, 0, loc), true},
		{"last interval", time.Date(2026, 7, 21, 15, 59, 59, 0, loc), true},
		{"close", time.Date(2026, 7, 21, 16, 0, 0, 0, loc), false},
		{"weekend", time.Date(2026, 7, 19, 10, 0, 0, 0, loc), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := gexbotCollectionWindow(test.at); got != test.want {
				t.Fatalf("window(%s)=%v want %v", test.at, got, test.want)
			}
		})
	}
}

type dashboardTestStore struct {
	*memoryStore
	history []store.GEXBotObservation
	latest  *store.GEXBotObservation
}

func (s *dashboardTestStore) ListGEXBotObservationHistory(_, _ string, _, _ time.Time) ([]store.GEXBotObservation, error) {
	return append([]store.GEXBotObservation(nil), s.history...), nil
}

func (s *dashboardTestStore) LoadLatestGEXBotObservation(_, _ string, _, _ time.Time) (*store.GEXBotObservation, error) {
	return s.latest, nil
}

func TestPublicGEXDashboardReturnsOnlyChartData(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	spot, zero := 6300.25, 6250.0
	latest := &store.GEXBotObservation{Symbol: "SPX", Category: "gex_full", ObservedAt: now, SourceTimestamp: now,
		Spot: &spot, ZeroGamma: &zero, Payload: json.RawMessage(`{"strikes":[[6250,-25,50],[6300,80,-10]]}`)}
	data := &dashboardTestStore{memoryStore: newMemoryStore(), history: []store.GEXBotObservation{*latest}, latest: latest}
	s := &server{mode: config.ModeConfig{TradingMode: config.ModeSim}, store: data}
	req := httptest.NewRequest(http.MethodGet, "/gex/data?category=gex_full", nil)
	response := httptest.NewRecorder()
	s.routes().ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		History []json.RawMessage `json:"history"`
		Latest  struct {
			Strikes [][]float64 `json:"strikes"`
		} `json:"latest"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.History) != 1 || len(body.Latest.Strikes) != 2 || body.Latest.Strikes[1][0] != 6300 {
		t.Fatalf("dashboard body=%s", response.Body.String())
	}
}
