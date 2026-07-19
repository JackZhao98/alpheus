package config

import (
	"os"

	"alpheus/kernel/internal/policy"
)

// Limits remains a source-compatible alias for deterministic tests and risk
// helpers. Runtime authority is policy.Policy loaded from PostgreSQL; config
// has no YAML reader or fallback after K1.
type Limits = policy.Policy

func Env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
