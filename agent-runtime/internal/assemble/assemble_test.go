package assemble

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"alpheus/agentruntime/internal/roles"
)

func TestAssembleAuthenticatesKernelReads(t *testing.T) {
	requests := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			return &http.Response{
				StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(`{"error":"unauthorized"}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{}`)),
		}, nil
	})

	client := New("http://kernel.test", "runtime-secret")
	client.HTTP.Transport = transport
	role := roles.Role{InjectedContext: []string{"state", "limits"}}
	context, err := client.Assemble(role)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || context["state"] == nil || context["limits"] == nil {
		t.Fatalf("requests=%d context=%v", requests, context)
	}
}

func TestAssembleQueryAddsMarketContext(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer runtime-secret" {
			t.Fatal("missing runtime authorization")
		}
		body := `{}`
		switch r.URL.Path {
		case "/state":
			body = `{"mode":"read_only"}`
		case "/market/quote/SOFI":
			body = `{"symbol":"SOFI","bid":"22.10","ask":"22.12"}`
		case "/market/bars/SOFI":
			if r.URL.Query().Get("days") != "30" {
				t.Fatalf("bars query=%s", r.URL.RawQuery)
			}
			body = `{"bars":[]}`
		default:
			t.Fatalf("unexpected path %s", r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	client := New("http://kernel.test", "runtime-secret")
	client.HTTP.Transport = transport
	context, err := client.AssembleQuery(roles.Role{InjectedContext: []string{"state"}}, "sofi", "现在值得研究吗？")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"state", "market_quote", "market_bars", "symbol", "user_query"} {
		if context[key] == nil {
			t.Fatalf("missing context key %q: %v", key, context)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
