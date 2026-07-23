package capability

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKernelReadSpecsMatchCatalogAndRejectAccountSelection(t *testing.T) {
	if got := len(KernelReadToolIDs()); got != 33 {
		t.Fatalf("generic Kernel read tools = %d, want 33", got)
	}
	for _, rawID := range KernelReadToolIDs() {
		id := ToolID(rawID)
		spec, ok := KernelReadToolSpecForID(id)
		descriptor, catalogued := LookupTool(id)
		if !ok || !catalogued || descriptor.Provider != "kernel_robinhood_mcp" || descriptor.SourceTool != spec.SourceTool {
			t.Fatalf("Kernel read spec/catalog mismatch for %q", id)
		}
	}
	request := KernelReadRequest{
		ToolID: "kernel_portfolio", SourceTool: "get_portfolio",
		Arguments: map[string]any{"account_number": "attacker-selected"},
	}
	if request.Validate() == nil {
		t.Fatal("model-selected account_number was accepted")
	}
}

func TestKernelReadRequestValidatesKnownAndRequiredArguments(t *testing.T) {
	valid := KernelReadRequest{
		ToolID: "kernel_equity_quotes", SourceTool: "get_equity_quotes",
		Arguments: map[string]any{"symbols": []any{"AAPL"}},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid quote request rejected: %v", err)
	}
	valid.Arguments["unknown"] = true
	if valid.Validate() == nil {
		t.Fatal("unknown Kernel read argument was accepted")
	}
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"query":"Apple","limit":10}`))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatal(err)
	}
	if err := (KernelReadRequest{ToolID: "kernel_search", SourceTool: "search", Arguments: decoded}).Validate(); err != nil {
		t.Fatalf("valid search request rejected: %v", err)
	}
	if (KernelReadRequest{ToolID: "kernel_search", SourceTool: "search", Arguments: map[string]any{}}).Validate() == nil {
		t.Fatal("missing required search query was accepted")
	}
}
