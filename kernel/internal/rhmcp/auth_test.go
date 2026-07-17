package rhmcp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOAuthCallbackIgnoresInvalidRequestAndWaitsForValidCode(t *testing.T) {
	callbacks := make(chan oauthCallback, 1)
	handler := oauthCallbackHandler("expected-state", callbacks)

	for _, target := range []string{
		"http://127.0.0.1:8399/mcp-callback",
		"http://127.0.0.1:8399/mcp-callback?code=probe&state=wrong",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid callback status=%d", response.Code)
		}
		select {
		case callback := <-callbacks:
			t.Fatalf("invalid callback consumed authorization slot: %+v", callback)
		default:
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1:8399/mcp-callback?code=valid-code&state=expected-state",
		nil,
	))
	if response.Code != http.StatusOK {
		t.Fatalf("valid callback status=%d body=%s", response.Code, response.Body.String())
	}
	select {
	case callback := <-callbacks:
		if callback.code != "valid-code" || callback.state != "expected-state" {
			t.Fatalf("callback=%+v", callback)
		}
	default:
		t.Fatal("valid callback did not complete authorization")
	}
}
