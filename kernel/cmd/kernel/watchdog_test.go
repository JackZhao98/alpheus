package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"alpheus/kernel/internal/config"
)

type watchdogRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn watchdogRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSpineTickPostsAuthenticatedOccurrenceToRuntime(t *testing.T) {
	var request struct {
		Role         string `json:"role"`
		Trigger      string `json:"trigger"`
		OccurrenceID string `json:"occurrence_id"`
	}
	client := &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/wake" {
			t.Errorf("request=%s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer kernel-secret" {
			t.Errorf("authorization header is missing")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type=%q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode wake: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusAccepted,
			Body:       io.NopCloser(strings.NewReader(`{"accepted":true}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})}
	st := newMemoryStore()
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeSim, KernelToken: "kernel-secret"},
		store: st, runtimeURL: "http://runtime.test", runtimeHTTP: client,
	}
	s.fireSpineTick("scout", "scout:20260717T164500Z:abc123")
	if request.Role != "scout" || request.Trigger != "spine" ||
		request.OccurrenceID != "scout:20260717T164500Z:abc123" {
		t.Fatalf("wake request=%+v", request)
	}
	if !containsEvent(st.events, "spine_tick") || containsEvent(st.events, "spine_wake_failed") {
		t.Fatalf("events=%v", st.events)
	}
}

func TestSpineWakeFailureIsAudited(t *testing.T) {
	client := &http.Client{Transport: watchdogRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(strings.NewReader(`{"error":"down"}`)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}, nil
	})}
	st := newMemoryStore()
	s := &server{
		mode:  config.ModeConfig{TradingMode: config.ModeSim, KernelToken: "kernel-secret"},
		store: st, runtimeURL: "http://runtime.test", runtimeHTTP: client,
	}
	s.fireSpineTick("coach", "coach:20260717T203500Z:def456")
	if !containsEvent(st.events, "spine_tick") || !containsEvent(st.events, "spine_wake_failed") {
		t.Fatalf("events=%v, want tick and failure", st.events)
	}
}
