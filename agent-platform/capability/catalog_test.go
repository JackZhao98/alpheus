package capability

import "testing"

func TestCatalogIsCompleteReviewedInventory(t *testing.T) {
	if err := ValidateCatalog(); err != nil {
		t.Fatalf("ValidateCatalog() error = %v", err)
	}

	var active, robinhoodCandidates int
	for _, entry := range Catalog() {
		if entry.State == CatalogStateActive {
			active++
		}
		if entry.Provider == "kernel_robinhood_mcp" && entry.State == CatalogStateCandidate {
			robinhoodCandidates++
			if entry.Effect != "read_only" && entry.Effect != "read_only_preflight" {
				t.Fatalf("candidate %q effect = %q, want read-only", entry.ID, entry.Effect)
			}
		}
	}
	if active != 37 {
		t.Fatalf("active tools = %d, want 37", active)
	}
	if robinhoodCandidates != 0 {
		t.Fatalf("Robinhood candidates = %d, want 0", robinhoodCandidates)
	}
}

func TestCatalogLookupReturnsCopy(t *testing.T) {
	entry, ok := LookupTool(ToolKernelEarningsResults)
	if !ok || entry.State != CatalogStateActive || entry.SourceTool != "get_earnings_results" {
		t.Fatalf("earnings tool = %#v, ok = %t", entry, ok)
	}
	entry.TargetRoles[0] = "changed"
	again, ok := LookupTool(ToolKernelEarningsResults)
	if !ok || again.TargetRoles[0] == "changed" {
		t.Fatalf("lookup exposed mutable catalog roles: %#v", again)
	}
}
