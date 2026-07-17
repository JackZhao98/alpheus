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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
