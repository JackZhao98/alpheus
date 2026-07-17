package rhmcp

import "strings"

// SafeQueryTools is the complete reviewed MCP surface that cannot create,
// update, cancel, remove, or place broker/account state. The two review tools
// only simulate an order and are deliberately separated as "preflight" in the
// cockpit. This list is the server-side authority; the browser cannot expand it.
var SafeQueryTools = []string{
	"get_accounts",
	"get_earnings_calendar",
	"get_earnings_results",
	"get_equity_fundamentals",
	"get_equity_historicals",
	"get_equity_orders",
	"get_equity_positions",
	"get_equity_price_book",
	"get_equity_quotes",
	"get_equity_tax_lots",
	"get_equity_technical_indicators",
	"get_equity_tradability",
	"get_financials",
	"get_index_quotes",
	"get_indexes",
	"get_option_chains",
	"get_option_instruments",
	"get_option_level_upgrade_info",
	"get_option_orders",
	"get_option_positions",
	"get_option_quotes",
	"get_option_watchlist",
	"get_pnl_trade_history",
	"get_popular_watchlists",
	"get_portfolio",
	"get_realized_pnl",
	"get_scanner_filter_specs",
	"get_scans",
	"get_watchlist_items",
	"get_watchlists",
	"review_equity_order",
	"review_option_order",
	"run_scan",
	"search",
}

// MutationTools is intentionally separate from SafeQueryTools. Only
// MutationClient may invoke this surface, without response caching or any
// automatic CallTool retry.
var MutationTools = []string{
	"place_equity_order",
	"place_option_order",
	"cancel_equity_order",
	"cancel_option_order",
}

func IsSafeQueryTool(name string) bool {
	for _, allowed := range SafeQueryTools {
		if name == allowed {
			return true
		}
	}
	return false
}

func IsMutationTool(name string) bool {
	for _, allowed := range MutationTools {
		if name == allowed {
			return true
		}
	}
	return false
}

func SafeQueryCategory(name string) string {
	switch {
	case strings.HasPrefix(name, "review_"):
		return "preflight"
	case strings.Contains(name, "option"):
		return "options"
	case strings.Contains(name, "account"), strings.Contains(name, "portfolio"),
		strings.Contains(name, "position"), strings.Contains(name, "order"),
		strings.Contains(name, "pnl"), strings.Contains(name, "tax_lot"):
		return "account"
	case strings.Contains(name, "scanner"), strings.Contains(name, "scan"):
		return "scanner"
	case strings.Contains(name, "watchlist"):
		return "watchlists"
	case strings.Contains(name, "earnings"), strings.Contains(name, "financial"),
		strings.Contains(name, "fundamental"):
		return "fundamentals"
	default:
		return "market"
	}
}
