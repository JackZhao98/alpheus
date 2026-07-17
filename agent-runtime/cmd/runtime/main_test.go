package main

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"alpheus/agentruntime/internal/assemble"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestOperationRetryReusesIdempotencyKeyAndBody(t *testing.T) {
	var mu sync.Mutex
	var keys, bodies []string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		bodies = append(bodies, string(body))
		attempt := len(keys)
		mu.Unlock()
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    r,
		}
		if attempt == 1 {
			response.Body = io.NopCloser(strings.NewReader(`{"operation_id":"lost"`))
			return response, nil
		}
		response.Body = io.NopCloser(strings.NewReader(`{"operation_id":"same","status":"executed"}`))
		return response, nil
	})

	client := &assemble.Client{Kernel: "http://kernel.test", HTTP: &http.Client{Transport: transport}}
	result, err := postOperationJSON(client, map[string]any{"action": "open", "qty": "1.000000"})
	if err != nil {
		t.Fatal(err)
	}
	if result["operation_id"] != "same" {
		t.Fatalf("result=%v", result)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("keys=%q, want same non-empty key", keys)
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("bodies differ: %q vs %q", bodies[0], bodies[1])
	}
}

func TestTickSecondsAllowsZeroAndRejectsInvalidValues(t *testing.T) {
	t.Setenv("TICK_SECONDS", "0")
	if value, err := envNonNegativeInt("TICK_SECONDS", 300); err != nil || value != 0 {
		t.Fatalf("zero tick: value=%d err=%v", value, err)
	}
	for _, invalid := range []string{"-1", "banana"} {
		t.Setenv("TICK_SECONDS", invalid)
		if _, err := envNonNegativeInt("TICK_SECONDS", 300); err == nil {
			t.Fatalf("invalid TICK_SECONDS %q accepted", invalid)
		}
	}
}
