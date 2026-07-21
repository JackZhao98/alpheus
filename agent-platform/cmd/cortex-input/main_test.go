package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"alpheus/agentplatform/contracts"
)

func TestBearerSubjectRequiresOneExactCredential(t *testing.T) {
	subject := contracts.AuditActor{PrincipalID: "owner-1", Kind: contracts.PrincipalUser, Audience: contracts.AudienceControlAPI}
	authenticate := bearerSubject("secret", subject)
	request := httptest.NewRequest(http.MethodPost, "/v1/user-requests", nil)
	request.Header.Set("Authorization", "Bearer secret")
	got, err := authenticate(request)
	if err != nil || got != subject {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	request.Header.Add("Authorization", "Bearer secret")
	if _, err := authenticate(request); err == nil {
		t.Fatal("multiple credentials were accepted")
	}
}
