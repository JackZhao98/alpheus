package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiveAccountBindingFileMustBePrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "live-account-id")
	if err := os.WriteFile(path, []byte("full-account-number\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIVE_ACCOUNT_ID", "")
	t.Setenv("LIVE_ACCOUNT_ID_FILE", path)
	if _, err := loadLiveAccountID(); err == nil {
		t.Fatal("accepted group/world-readable account binding")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := loadLiveAccountID()
	if err != nil || value != "full-account-number" {
		t.Fatalf("value=%q err=%v", value, err)
	}
	t.Setenv("LIVE_ACCOUNT_ID", "another-account")
	if _, err := loadLiveAccountID(); err == nil {
		t.Fatal("accepted ambiguous direct and file account bindings")
	}
}

func TestModeConfigCannotMarshalSecretsOrAccountBinding(t *testing.T) {
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
