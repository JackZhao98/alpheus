package main

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestValidateModelOutputBindsExactSchemaAndInstance(t *testing.T) {
	schema := []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`)
	hash := sha256.Sum256(schema)
	evidence, err := validateModelOutput(schema, hex.EncodeToString(hash[:]), []byte(`{"text":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if evidence.InstanceSHA256 == "" || evidence.SchemaSHA256 == "" {
		t.Fatalf("incomplete evidence: %+v", evidence)
	}
	if _, err := validateModelOutput(schema, strings.Repeat("0", 64), []byte(`{"text":"ok"}`)); err == nil {
		t.Fatal("schema digest mismatch was accepted")
	}
	if _, err := validateModelOutput(schema, hex.EncodeToString(hash[:]), []byte(`{"other":"no"}`)); err == nil {
		t.Fatal("contract-invalid output was accepted")
	}
}
