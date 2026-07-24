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

func TestKernelEquityFundamentalsRejectsInventedBounds(t *testing.T) {
	for _, bounds := range []string{"regular", "trading", "extended", "24_5"} {
		request := KernelReadRequest{
			ToolID:     "kernel_equity_fundamentals",
			SourceTool: "get_equity_fundamentals",
			Arguments: map[string]any{
				"symbols": []any{"TSLA"},
				"bounds":  bounds,
			},
		}
		if err := request.Validate(); err != nil {
			t.Fatalf("valid fundamentals bounds %q rejected: %v", bounds, err)
		}
	}
	request := KernelReadRequest{
		ToolID:     "kernel_equity_fundamentals",
		SourceTool: "get_equity_fundamentals",
		Arguments: map[string]any{
			"symbols": []any{"TSLA"},
			"bounds":  "as_of=2026-07-23",
		},
	}
	if request.Validate() == nil {
		t.Fatal("invented fundamentals bounds was accepted")
	}
}

func TestKernelSearchRejectsInventedRobinhoodEnums(t *testing.T) {
	valid := KernelReadRequest{
		ToolID:     "kernel_search",
		SourceTool: "search",
		Arguments: map[string]any{
			"query":      "SPCX",
			"asset_type": "instrument",
			"limit":      json.Number("10"),
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid search rejected: %v", err)
	}
	valid.Arguments["asset_type"] = "equity"
	if issue := valid.ValidationIssue(); issue != "kernel_tool_asset_type_invalid" {
		t.Fatalf("invalid asset type issue = %q", issue)
	}
	valid.Arguments["asset_type"] = "instrument"
	valid.Arguments["limit"] = json.Number("10.5")
	if issue := valid.ValidationIssue(); issue != "kernel_tool_limit_invalid" {
		t.Fatalf("fractional limit issue = %q", issue)
	}
}

func TestKernelHistoricalsRequireExactProviderVocabulary(t *testing.T) {
	valid := KernelReadRequest{
		ToolID:     "kernel_equity_historicals",
		SourceTool: "get_equity_historicals",
		Arguments: map[string]any{
			"symbols":         []any{"TSLA", "SPCX"},
			"start_time":      "2026-07-20T00:00:00Z",
			"end_time":        "2026-07-24T00:00:00Z",
			"interval":        "hour",
			"bounds":          "extended",
			"adjustment_type": "split",
		},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid historical request rejected: %v", err)
	}
	cases := []struct {
		key   string
		value any
		issue string
	}{
		{"interval", "1h", "kernel_tool_interval_invalid"},
		{"interval", "1day", "kernel_tool_interval_invalid"},
		{"bounds", "all", "kernel_tool_bounds_invalid"},
		{"adjustment_type", "everything", "kernel_tool_adjustment_type_invalid"},
		{"start_time", "2026-07-20", "kernel_tool_start_time_invalid"},
	}
	for _, test := range cases {
		original := valid.Arguments[test.key]
		valid.Arguments[test.key] = test.value
		if issue := valid.ValidationIssue(); issue != test.issue {
			t.Fatalf("%s issue = %q, want %q", test.key, issue, test.issue)
		}
		valid.Arguments[test.key] = original
	}
	valid.Arguments["interval"] = "day"
	valid.Arguments["adjustment_type"] = "all"
	if issue := valid.ValidationIssue(); issue != "kernel_tool_adjustment_interval_invalid" {
		t.Fatalf("interday all-adjustment issue = %q", issue)
	}
}
