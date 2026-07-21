package inputgateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"alpheus/agentplatform/contracts"
)

func TestHTTPHandlerAdmitsAuthenticatedInput(t *testing.T) {
	blobs, submitted := &testBlobCommitter{}, &testSubmitter{}
	gateway, err := New(blobs, submitted)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time { return time.Date(2026, 7, 21, 16, 1, 0, 0, time.UTC) }
	handler := NewHandler(gateway,
		contracts.AuditActor{PrincipalID: "control-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI},
		func(*http.Request) (contracts.AuditActor, error) {
			return contracts.AuditActor{PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceControlAPI}, nil
		})
	body := `{"conversation_id":"conversation-1","conversation_created_at":"2026-07-21T15:59:00Z","request_id":"request-1","kind":"new_request","text":"test","idempotency_key":"idem-1","causation_id":"cause-1","correlation_id":"correlation-1","deadline":"2026-07-21T16:05:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/user-requests", strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"request_id":"request-1"`) || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%d %s headers=%v", response.Code, response.Body.String(), response.Header())
	}
	if submitted.command.Request.RawInput.Origin.RecordID != "request-1" {
		t.Fatalf("raw origin must be retry-stable: %+v", submitted.command.Request.RawInput.Origin)
	}
}

func TestHTTPHandlerFailsClosed(t *testing.T) {
	blobs, submitted := &testBlobCommitter{}, &testSubmitter{}
	gateway, err := New(blobs, submitted)
	if err != nil {
		t.Fatal(err)
	}
	gateway.now = func() time.Time { return time.Date(2026, 7, 21, 16, 1, 0, 0, time.UTC) }
	actor := contracts.AuditActor{PrincipalID: "control-1", Kind: contracts.PrincipalWorkload, Audience: contracts.AudienceControlAPI}
	unauthorized := NewHandler(gateway, actor, func(*http.Request) (contracts.AuditActor, error) {
		return contracts.AuditActor{}, errors.New("bad token")
	})
	body := `{"conversation_id":"conversation-1","conversation_created_at":"2026-07-21T15:59:00Z","request_id":"request-1","kind":"new_request","text":"test","idempotency_key":"idem-1","causation_id":"cause-1","correlation_id":"correlation-1","deadline":"2026-07-21T16:05:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/v1/user-requests", strings.NewReader(body))
	response := httptest.NewRecorder()
	unauthorized.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "input_gateway_subject_unverified") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}

	badJSON := httptest.NewRequest(http.MethodPost, "/v1/user-requests", strings.NewReader(`{"text":"x","unknown":true}`))
	response = httptest.NewRecorder()
	NewHandler(gateway, actor, func(*http.Request) (contracts.AuditActor, error) { return validAdmissionRequest().Subject, nil }).ServeHTTP(response, badJSON)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "input_gateway_json_invalid") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}

	blobs.err = errors.New("disk")
	request = httptest.NewRequest(http.MethodPost, "/v1/user-requests", strings.NewReader(body))
	response = httptest.NewRecorder()
	NewHandler(gateway, actor, func(*http.Request) (contracts.AuditActor, error) { return validAdmissionRequest().Subject, nil }).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), CodeBlobCommit) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}
