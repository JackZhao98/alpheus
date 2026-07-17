package rhmcp

import (
	"strings"
	"testing"
)

func TestSafeQueryToolsExcludeMutations(t *testing.T) {
	if len(SafeQueryTools) != 34 {
		t.Fatalf("safe query tool count = %d, want 34", len(SafeQueryTools))
	}
	seen := make(map[string]bool, len(SafeQueryTools))
	for _, name := range SafeQueryTools {
		if name == "" || seen[name] {
			t.Fatalf("invalid safe query tool %q", name)
		}
		seen[name] = true
		for _, forbidden := range []string{"place_", "cancel_", "add_", "remove_", "create_", "update_", "follow_", "unfollow_"} {
			if strings.HasPrefix(name, forbidden) {
				t.Fatalf("mutation tool %q reached the safe query allowlist", name)
			}
		}
	}
	if !IsSafeQueryTool("get_realized_pnl") || !IsSafeQueryTool("review_equity_order") {
		t.Fatal("reviewed safe tools are missing")
	}
	if IsSafeQueryTool("place_equity_order") || IsSafeQueryTool("cancel_option_order") {
		t.Fatal("money-path mutation was allowlisted")
	}
}

func TestSafeQueryCategories(t *testing.T) {
	for name, want := range map[string]string{
		"review_option_order": "preflight",
		"get_option_quotes":   "options",
		"get_realized_pnl":    "account",
		"get_scans":           "scanner",
		"get_watchlists":      "watchlists",
		"get_financials":      "fundamentals",
		"search":              "market",
	} {
		if got := SafeQueryCategory(name); got != want {
			t.Fatalf("category %s = %s, want %s", name, got, want)
		}
	}
}
