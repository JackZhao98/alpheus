package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMoodyBluesReplayStreamProxiesWithoutRawBlob(t *testing.T) {
	research := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		if r.URL.Path !=
			"/internal/v1/moody-blues/providers/gexbot-classic/replays/replay-id/next" &&
			r.URL.Path !=
				"/internal/v1/moody-blues/providers/gexbot-classic/replays/11111111-1111-4111-8111-111111111111/next" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer research-secret" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
		  "schema_revision":1,
		  "replay_id":"11111111-1111-4111-8111-111111111111",
		  "state":"active",
		  "generation":2,
		  "observation":{
		    "observation_id":"obs-1",
		    "available_at":"2026-07-23T20:00:00Z",
		    "metrics":{"spot":6400,"zero_gamma":6350},
		    "raw":{"blob_id":"secret-metadata"}
		  }
		}`)
	}))
	defer research.Close()

	mux := http.NewServeMux()
	registerMoodyBluesStreamHandlers(
		mux, "service-secret", research.Client(),
		research.URL, "research-secret",
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/data-streams/gexbot/replays/11111111-1111-4111-8111-111111111111/next",
		strings.NewReader(`{"generation":1}`),
	)
	request.Header.Set("Authorization", "Bearer service-secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"zero_gamma":6350`) ||
		strings.Contains(response.Body.String(), `"raw"`) ||
		strings.Contains(response.Body.String(), "secret-metadata") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMoodyBluesReplayStreamRejectsInvalidOrStaleRequests(t *testing.T) {
	mux := http.NewServeMux()
	registerMoodyBluesStreamHandlers(
		mux, "service-secret", http.DefaultClient,
		"http://research.invalid", "research-secret",
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"/v1/data-streams/gexbot/replays/not-a-replay/next",
		strings.NewReader(`{"generation":1}`),
	)
	request.Header.Set("Authorization", "Bearer service-secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(),
			`"error_code":"moody_blues_cursor_invalid"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(
		http.MethodPost,
		"/v1/data-streams/gexbot/replays/11111111-1111-4111-8111-111111111111/next",
		strings.NewReader(`{"generation":0}`),
	)
	request.Header.Set("Authorization", "Bearer service-secret")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
