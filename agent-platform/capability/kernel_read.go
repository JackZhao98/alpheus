package capability

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const maxKernelReadArgumentsBytes = 12 << 10

// KernelReadToolSpec is the reviewed Cortex-facing subset of one Robinhood MCP
// input schema. account_number is deliberately absent: Kernel injects the
// permanently bound account after authorization and the model cannot select it.
type KernelReadToolSpec struct {
	ToolID        ToolID
	SourceTool    string
	ArgumentGuide string
	AllowedArgs   []string
	RequiredArgs  []string
}

var kernelReadToolSpecs = map[ToolID]KernelReadToolSpec{
	"kernel_accounts":                    kernelSpec("kernel_accounts", "get_accounts", "no arguments", nil, nil),
	"kernel_earnings_calendar":           kernelSpec("kernel_earnings_calendar", "get_earnings_calendar", "days?:integer, filter?:string, start_date?:YYYY-MM-DD", []string{"days", "filter", "start_date"}, nil),
	"kernel_equity_fundamentals":         kernelSpec("kernel_equity_fundamentals", "get_equity_fundamentals", "symbols:uppercase string array; bounds?:one of regular,trading,extended,24_5 (omit unless a trading session is explicitly needed)", []string{"bounds", "symbols"}, []string{"symbols"}),
	"kernel_equity_historicals":          kernelSpec("kernel_equity_historicals", "get_equity_historicals", "symbols:uppercase string array; start_time:RFC3339; end_time?, interval?, bounds?, adjustment_type?", []string{"adjustment_type", "bounds", "end_time", "interval", "start_time", "symbols"}, []string{"start_time", "symbols"}),
	"kernel_equity_orders":               kernelSpec("kernel_equity_orders", "get_equity_orders", "symbol?, state?, created_at_gte?, order_id?, cursor?, placed_agent?; bound account is automatic", []string{"created_at_gte", "cursor", "order_id", "placed_agent", "state", "symbol"}, nil),
	"kernel_equity_positions":            kernelSpec("kernel_equity_positions", "get_equity_positions", "cursor?; bound account is automatic", []string{"cursor"}, nil),
	"kernel_equity_price_book":           kernelSpec("kernel_equity_price_book", "get_equity_price_book", "symbols:uppercase string array", []string{"symbols"}, []string{"symbols"}),
	"kernel_equity_quotes":               kernelSpec("kernel_equity_quotes", "get_equity_quotes", "symbols:uppercase string array", []string{"symbols"}, []string{"symbols"}),
	"kernel_equity_tax_lots":             kernelSpec("kernel_equity_tax_lots", "get_equity_tax_lots", "symbol:uppercase ticker; cursor?; bound account is automatic", []string{"cursor", "symbol"}, []string{"symbol"}),
	"kernel_equity_technical_indicators": kernelSpec("kernel_equity_technical_indicators", "get_equity_technical_indicators", "symbol, type, interval, start_time required; end_time?, period?, fast_period?, slow_period?, signal_period?, method?, output?, multiplier?, num_std?, bounds?, adjustment_type?", []string{"adjustment_type", "bounds", "end_time", "fast_period", "interval", "method", "multiplier", "num_std", "output", "period", "signal_period", "slow_period", "start_time", "symbol", "type"}, []string{"interval", "start_time", "symbol", "type"}),
	"kernel_equity_tradability":          kernelSpec("kernel_equity_tradability", "get_equity_tradability", "symbols:uppercase string array; bound account is automatic", []string{"symbols"}, []string{"symbols"}),
	"kernel_financials":                  kernelSpec("kernel_financials", "get_financials", "symbols:uppercase string array; period?, limit?", []string{"limit", "period", "symbols"}, []string{"symbols"}),
	"kernel_index_quotes":                kernelSpec("kernel_index_quotes", "get_index_quotes", "instrument_ids:index UUID array", []string{"instrument_ids"}, []string{"instrument_ids"}),
	"kernel_indexes":                     kernelSpec("kernel_indexes", "get_indexes", "symbols?:comma-separated index symbols such as SPX,NDX", []string{"symbols"}, nil),
	"kernel_option_chains":               kernelSpec("kernel_option_chains", "get_option_chains", "underlying_symbol? or ids?", []string{"ids", "underlying_symbol"}, nil),
	"kernel_option_instruments":          kernelSpec("kernel_option_instruments", "get_option_instruments", "chain_symbol? or chain_id?; expiration_dates?, strike_price?, type?, state?, tradability?, ids?, cursor?", []string{"chain_id", "chain_symbol", "cursor", "expiration_dates", "ids", "state", "strike_price", "tradability", "type"}, nil),
	"kernel_option_level_upgrade_info":   kernelSpec("kernel_option_level_upgrade_info", "get_option_level_upgrade_info", "no model arguments; bound account is automatic", nil, nil),
	"kernel_option_orders":               kernelSpec("kernel_option_orders", "get_option_orders", "chain_ids?, state?, created_at_gte?, order_id?, underlying_type?, cursor?, placed_agent?; bound account is automatic", []string{"chain_ids", "created_at_gte", "cursor", "order_id", "placed_agent", "state", "underlying_type"}, nil),
	"kernel_option_positions":            kernelSpec("kernel_option_positions", "get_option_positions", "nonzero?, chain_ids?, option_ids?, option_type?, type?, expiration_date?, expiration_date_gte?, expiration_date_lte?, cursor?; bound account is automatic", []string{"chain_ids", "cursor", "expiration_date", "expiration_date_gte", "expiration_date_lte", "nonzero", "option_ids", "option_type", "type"}, nil),
	"kernel_option_quotes":               kernelSpec("kernel_option_quotes", "get_option_quotes", "instrument_ids:option UUID array", []string{"instrument_ids"}, []string{"instrument_ids"}),
	"kernel_option_watchlist":            kernelSpec("kernel_option_watchlist", "get_option_watchlist", "no arguments", nil, nil),
	"kernel_pnl_trade_history":           kernelSpec("kernel_pnl_trade_history", "get_pnl_trade_history", "span?, symbol?, cursor?; bound account is automatic", []string{"cursor", "span", "symbol"}, nil),
	"kernel_popular_watchlists":          kernelSpec("kernel_popular_watchlists", "get_popular_watchlists", "no arguments", nil, nil),
	"kernel_portfolio":                   kernelSpec("kernel_portfolio", "get_portfolio", "no model arguments; bound account is automatic", nil, nil),
	"kernel_realized_pnl":                kernelSpec("kernel_realized_pnl", "get_realized_pnl", "span?, start_date?, end_date?, timezone?, display_currency?, asset_classes?; bound account is automatic", []string{"asset_classes", "display_currency", "end_date", "span", "start_date", "timezone"}, nil),
	"kernel_scanner_filter_specs":        kernelSpec("kernel_scanner_filter_specs", "get_scanner_filter_specs", "no arguments", nil, nil),
	"kernel_scans":                       kernelSpec("kernel_scans", "get_scans", "no arguments", nil, nil),
	"kernel_watchlist_items":             kernelSpec("kernel_watchlist_items", "get_watchlist_items", "list_id:watchlist UUID", []string{"list_id"}, []string{"list_id"}),
	"kernel_watchlists":                  kernelSpec("kernel_watchlists", "get_watchlists", "no arguments", nil, nil),
	"kernel_review_equity_order":         kernelSpec("kernel_review_equity_order", "review_equity_order", "simulation only: symbol, side, type required; quantity? or dollar_amount?; limit_price?, stop_price?, time_in_force?, market_hours?, tax_lots?; bound account is automatic", []string{"dollar_amount", "limit_price", "market_hours", "quantity", "side", "stop_price", "symbol", "tax_lots", "time_in_force", "type"}, []string{"side", "symbol", "type"}),
	"kernel_review_option_order":         kernelSpec("kernel_review_option_order", "review_option_order", "simulation only: legs and quantity required; chain_symbol?, price?, stop_price?, type?, time_in_force?, market_hours?, underlying_type?; bound account is automatic", []string{"chain_symbol", "legs", "market_hours", "price", "quantity", "stop_price", "time_in_force", "type", "underlying_type"}, []string{"legs", "quantity"}),
	"kernel_run_scan":                    kernelSpec("kernel_run_scan", "run_scan", "scan_id:approved scanner UUID", []string{"scan_id"}, []string{"scan_id"}),
	"kernel_search":                      kernelSpec("kernel_search", "search", "query:string required; asset_type?, limit?", []string{"asset_type", "limit", "query"}, []string{"query"}),
}

func kernelSpec(id ToolID, source, guide string, allowed, required []string) KernelReadToolSpec {
	return KernelReadToolSpec{ToolID: id, SourceTool: source, ArgumentGuide: guide, AllowedArgs: allowed, RequiredArgs: required}
}

type KernelReadRequest struct {
	ToolID     ToolID         `json:"tool_id"`
	SourceTool string         `json:"source_tool"`
	Arguments  map[string]any `json:"arguments"`
}

func (value KernelReadRequest) Validate() error {
	spec, ok := kernelReadToolSpecs[value.ToolID]
	if !ok || value.SourceTool != spec.SourceTool || value.Arguments == nil {
		return ErrInvalidCapability
	}
	allowed := make(map[string]struct{}, len(spec.AllowedArgs))
	for _, key := range spec.AllowedArgs {
		allowed[key] = struct{}{}
	}
	for key, argument := range value.Arguments {
		if key == "account_number" {
			return ErrInvalidCapability
		}
		if _, ok := allowed[key]; !ok || !validKernelArgumentValue(argument, 0) {
			return ErrInvalidCapability
		}
	}
	for _, key := range spec.RequiredArgs {
		if _, ok := value.Arguments[key]; !ok {
			return ErrInvalidCapability
		}
	}
	if value.ToolID == "kernel_equity_fundamentals" {
		if bounds, supplied := value.Arguments["bounds"]; supplied {
			selected, ok := bounds.(string)
			if !ok || (selected != "regular" && selected != "trading" &&
				selected != "extended" && selected != "24_5") {
				return ErrInvalidCapability
			}
		}
	}
	raw, err := json.Marshal(value.Arguments)
	if err != nil || len(raw) > maxKernelReadArgumentsBytes {
		return ErrInvalidCapability
	}
	return nil
}

func validKernelArgumentValue(value any, depth int) bool {
	if depth > 6 {
		return false
	}
	switch typed := value.(type) {
	case nil, bool, string, json.Number, float64:
		return !isOversizedKernelString(typed)
	case []any:
		if len(typed) > 100 {
			return false
		}
		for _, item := range typed {
			if !validKernelArgumentValue(item, depth+1) {
				return false
			}
		}
		return true
	case map[string]any:
		if len(typed) > 64 {
			return false
		}
		for key, item := range typed {
			if key == "" || len(key) > 128 || !validKernelArgumentValue(item, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func isOversizedKernelString(value any) bool {
	text, ok := value.(string)
	return ok && len(text) > 4000
}

func KernelReadToolSpecForID(id ToolID) (KernelReadToolSpec, bool) {
	spec, ok := kernelReadToolSpecs[id]
	if !ok {
		return KernelReadToolSpec{}, false
	}
	spec.AllowedArgs = append([]string(nil), spec.AllowedArgs...)
	spec.RequiredArgs = append([]string(nil), spec.RequiredArgs...)
	return spec, true
}

func KernelReadToolIDs() []string {
	ids := make([]string, 0, len(kernelReadToolSpecs))
	for id := range kernelReadToolSpecs {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	return ids
}

func KernelReadPromptCatalog() string {
	ids := KernelReadToolIDs()
	lines := make([]string, 0, len(ids))
	for _, rawID := range ids {
		id := ToolID(rawID)
		spec := kernelReadToolSpecs[id]
		descriptor, ok := LookupTool(id)
		if !ok {
			continue
		}
		route := "decision_desk"
		if role, found := SpecialistRoleForTool(id); found {
			route = string(role)
		}
		lines = append(lines, fmt.Sprintf("%s: %s Route: %s. Args: %s.", id, descriptor.Description, route, spec.ArgumentGuide))
	}
	return strings.Join(lines, " ")
}
