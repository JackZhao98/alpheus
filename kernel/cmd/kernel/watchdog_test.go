package main

import (
	"net/http"
	"testing"
)

type watchdogRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn watchdogRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestSpineTickIsAuditOnlyAfterStaticRuntimeRetirement(t *testing.T) {
	st := newMemoryStore()
	s := &server{store: st}
	s.fireSpineTick("scout", "scout:20260717T164500Z:abc123")
	if !containsEvent(st.events, "spine_tick") || containsEvent(st.events, "spine_wake_failed") {
		t.Fatalf("events=%v, want one durable audit tick and no retired runtime wake", st.events)
	}
}
