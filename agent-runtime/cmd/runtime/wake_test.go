package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"alpheus/agentruntime/internal/roles"
)

func TestWakeRequiresKernelToken(t *testing.T) {
	runs := 0
	handler := newWakeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(roles.Role, string) { runs++ })

	for _, token := range []string{"", "runtime-secret", "wrong"} {
		req := httptest.NewRequest(http.MethodPost, "/wake", bytes.NewBufferString(`{"role":"scout"}`))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("token=%q status=%d body=%s", token, w.Code, w.Body.String())
		}
	}
	if runs != 0 {
		t.Fatalf("unauthorized wake ran %d sessions", runs)
	}
}

func TestWakeAcceptsAuthenticatedKnownRole(t *testing.T) {
	var gotRole, gotTrigger string
	handler := newWakeHandler("kernel-secret", map[string]roles.Role{
		"scout": {Role: "scout"},
	}, func(role roles.Role, trigger string) {
		gotRole, gotTrigger = role.Role, trigger
	})
	req := httptest.NewRequest(http.MethodPost, "/wake", bytes.NewBufferString(`{"role":"scout","trigger":"spine"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer kernel-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted || gotRole != "scout" || gotTrigger != "spine" {
		t.Fatalf("status=%d role=%q trigger=%q body=%s", w.Code, gotRole, gotTrigger, w.Body.String())
	}
}
