package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModeConfigCannotMarshalSecretsOrTestBinding(t *testing.T) {
	raw, err := json.Marshal(ModeConfig{
		TradingMode: "read_only", RuntimeToken: "runtime-secret", AdminToken: "admin-secret",
		KernelToken: "kernel-secret", LiveAccountID: "full-account-number",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"runtime-secret", "admin-secret", "kernel-secret", "full-account-number"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("config JSON leaked %q: %s", secret, raw)
		}
	}
}
