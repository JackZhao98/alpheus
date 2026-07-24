package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestFetchClassicUsesOfficialCredentialAndNormalizes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/SPX/classic/gex_full" || r.Header.Get("Authorization") != "Bearer official-secret" ||
			r.Header.Get("Accept") != "application/json" {
			t.Fatalf("unexpected official request: %s %#v", r.URL.Path, r.Header)
		}
		_, _ = io.WriteString(w, `{"timestamp":1784743200,"ticker":"SPX","spot":6000.25,"zero_gamma":5995.5,"major_pos_vol":6010,"ignored":[1,2,3]}`)
	}))
	defer upstream.Close()
	observed := time.Date(2026, 7, 22, 18, 0, 1, 123000, time.UTC)
	p := &provider{apiKey: "official-secret", apiBaseURL: upstream.URL, http: upstream.Client()}
	result, err := p.fetchClassic(context.Background(), "SPX", "gex_full", observed)
	if err != nil || result.SourceKind != "provider_poll" || result.Symbol != "SPX" || result.Category != "gex_full" ||
		!result.ObservedAt.Equal(observed) || result.Spot == nil || *result.Spot != 6000.25 ||
		result.ZeroGamma == nil || *result.ZeroGamma != 5995.5 {
		t.Fatalf("unexpected live observation: %#v %v", result, err)
	}
}

func TestLiveFailsClosedWithoutCredential(t *testing.T) {
	p := &provider{readKey: "read-secret"}
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{"symbol":"SPX","category":"gex_full"}`))
	request.Header.Set("Authorization", "Bearer read-secret")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	p.live(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCollectionWindowUsesNewYorkMarketSession(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want bool
	}{
		{"before open", time.Date(2026, 7, 23, 12, 59, 59, 0, time.UTC), false},
		{"at open", time.Date(2026, 7, 23, 13, 0, 0, 0, time.UTC), true},
		{"before close", time.Date(2026, 7, 23, 19, 59, 59, 0, time.UTC), true},
		{"at close", time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC), false},
		{"weekend", time.Date(2026, 7, 25, 16, 0, 0, 0, time.UTC), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := collectionWindow(test.at); got != test.want {
				t.Fatalf("collectionWindow(%s)=%t, want %t",
					test.at, got, test.want)
			}
		})
	}
}
