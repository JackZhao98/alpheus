package capability

import "fmt"

// CatalogState reports lifecycle, not permission. A candidate is an audited
// inventory record only; it cannot be invoked by a Worker or named in a grant.
type CatalogState string

const (
	CatalogStateActive    CatalogState = "active"
	CatalogStateCandidate CatalogState = "candidate"
)

// ToolDescriptor is the code-owned, reviewable inventory used to keep the
// operator tracker and the implementation in sync. It is deliberately not a
// runtime registry: this catalog grants no access and sends no request.
type ToolDescriptor struct {
	ID          ToolID
	Revision    uint16
	Provider    string
	SourceTool  string
	Category    string
	Description string
	Effect      string
	TargetRoles []string
	State       CatalogState
}

var toolCatalog = []ToolDescriptor{
	{
		ID: ToolResearchWebFetch, Revision: 1, Provider: "research_gateway", SourceTool: "web-fetch",
		Category: "web", Description: "Fetch one explicit public HTTP(S) page as bounded, untrusted evidence.",
		Effect: "read_only", TargetRoles: []string{"discovery_scout", "decision_desk"}, State: CatalogStateActive,
	},
	{
		ID: ToolResearchGEXBOTAsOf, Revision: 1, Provider: "gexbot_provider", SourceTool: "gexbot-as-of",
		Category: "market_options", Description: "Read one bounded archived SPX GEX observation behind an as_of fence.",
		Effect: "read_only", TargetRoles: []string{"options_scout", "decision_desk"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_accounts", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_accounts",
		Category: "portfolio", Description: "Read identity and account facts only for the bound brokerage account.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_earnings_calendar", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_earnings_calendar",
		Category: "catalyst", Description: "Read a bounded set of upcoming earnings dates for an explicit symbol.",
		Effect: "read_only", TargetRoles: []string{"catalyst_scout"}, State: CatalogStateActive,
	},
	{
		ID: ToolKernelEarningsResults, Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_earnings_results",
		Category: "catalyst", Description: "Read normalized published earnings results for one explicit symbol.",
		Effect: "read_only", TargetRoles: []string{"catalyst_scout", "decision_desk"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_fundamentals", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_fundamentals",
		Category: "fundamentals", Description: "Read provider fundamental and valuation fields for one explicit equity.",
		Effect: "read_only", TargetRoles: []string{"fundamental_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_financials", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_financials",
		Category: "fundamentals", Description: "Read bounded financial-statement data for one explicit equity.",
		Effect: "read_only", TargetRoles: []string{"fundamental_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_historicals", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_historicals",
		Category: "market", Description: "Read bounded historical equity-price bars.",
		Effect: "read_only", TargetRoles: []string{"market_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_price_book", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_price_book",
		Category: "market", Description: "Read a point-in-time equity bid, ask, and price-book snapshot.",
		Effect: "read_only", TargetRoles: []string{"market_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_quotes", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_quotes",
		Category: "market", Description: "Read a point-in-time equity quote snapshot.",
		Effect: "read_only", TargetRoles: []string{"market_scout", "decision_desk"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_technical_indicators", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_technical_indicators",
		Category: "market", Description: "Read one explicitly requested technical indicator over a bounded interval.",
		Effect: "read_only", TargetRoles: []string{"market_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_tradability", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_tradability",
		Category: "market", Description: "Read equity tradability and market-status facts.",
		Effect: "read_only", TargetRoles: []string{"decision_desk"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_indexes", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_indexes",
		Category: "market", Description: "Resolve an explicit index symbol to provider index facts.",
		Effect: "read_only", TargetRoles: []string{"market_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_index_quotes", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_index_quotes",
		Category: "market", Description: "Read a point-in-time index quote snapshot.",
		Effect: "read_only", TargetRoles: []string{"market_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_option_chains", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_option_chains",
		Category: "options", Description: "Read bounded option-chain metadata for one explicit underlying.",
		Effect: "read_only", TargetRoles: []string{"options_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_option_instruments", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_option_instruments",
		Category: "options", Description: "Read terms and provider IDs for explicitly bounded option instruments.",
		Effect: "read_only", TargetRoles: []string{"options_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_option_quotes", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_option_quotes",
		Category: "options", Description: "Read a point-in-time quote snapshot for bounded option instruments.",
		Effect: "read_only", TargetRoles: []string{"options_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_option_watchlist", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_option_watchlist",
		Category: "options", Description: "Read an existing option-watchlist snapshot without altering it.",
		Effect: "read_only", TargetRoles: []string{"options_scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_option_level_upgrade_info", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_option_level_upgrade_info",
		Category: "portfolio", Description: "Read option-eligibility facts for the bound brokerage account.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_positions", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_positions",
		Category: "portfolio", Description: "Read equity positions for the bound brokerage account.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_option_positions", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_option_positions",
		Category: "portfolio", Description: "Read option positions for the bound brokerage account.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_orders", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_orders",
		Category: "portfolio", Description: "Read bounded equity-order history and states for the bound account.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_option_orders", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_option_orders",
		Category: "portfolio", Description: "Read bounded option-order history and states for the bound account.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_equity_tax_lots", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_equity_tax_lots",
		Category: "portfolio", Description: "Read equity tax lots for the bound brokerage account.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_portfolio", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_portfolio",
		Category: "portfolio", Description: "Read a portfolio summary for the bound brokerage account.",
		Effect: "read_only", TargetRoles: []string{"position_manager", "decision_desk"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_pnl_trade_history", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_pnl_trade_history",
		Category: "portfolio", Description: "Read a bounded history of realized trade P&L.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_realized_pnl", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_realized_pnl",
		Category: "portfolio", Description: "Read a bounded realized-P&L summary.",
		Effect: "read_only", TargetRoles: []string{"position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_popular_watchlists", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_popular_watchlists",
		Category: "discovery", Description: "Read public popular-watchlist metadata only.",
		Effect: "read_only", TargetRoles: []string{"scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_watchlists", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_watchlists",
		Category: "discovery", Description: "Read public or bound-account watchlist metadata within an explicit scope.",
		Effect: "read_only", TargetRoles: []string{"scout", "position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_watchlist_items", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_watchlist_items",
		Category: "discovery", Description: "Read the contents of one explicit watchlist ID without altering it.",
		Effect: "read_only", TargetRoles: []string{"scout", "position_manager"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_scanner_filter_specs", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_scanner_filter_specs",
		Category: "discovery", Description: "Read supported scanner-filter definitions.",
		Effect: "read_only", TargetRoles: []string{"scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_scans", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "get_scans",
		Category: "discovery", Description: "Read available scanner definitions.",
		Effect: "read_only", TargetRoles: []string{"scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_run_scan", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "run_scan",
		Category: "discovery", Description: "Run one approved scanner ID with bounded filter inputs.",
		Effect: "read_only", TargetRoles: []string{"scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_search", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "search",
		Category: "discovery", Description: "Resolve an asset name or symbol to bounded provider identifiers.",
		Effect: "read_only", TargetRoles: []string{"scout"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_review_equity_order", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "review_equity_order",
		Category: "preflight", Description: "Simulate and validate an explicit equity order; it never creates an order.",
		Effect: "read_only_preflight", TargetRoles: []string{"decision_desk"}, State: CatalogStateActive,
	},
	{
		ID: "kernel_review_option_order", Revision: 1, Provider: "kernel_robinhood_mcp", SourceTool: "review_option_order",
		Category: "preflight", Description: "Simulate and validate an explicit option order; it never creates an order.",
		Effect: "read_only_preflight", TargetRoles: []string{"decision_desk"}, State: CatalogStateActive,
	},
}

// Catalog returns a defensive copy. Callers may render it for operators but
// must not treat it as an authorization decision.
func Catalog() []ToolDescriptor {
	result := make([]ToolDescriptor, len(toolCatalog))
	for index, entry := range toolCatalog {
		result[index] = entry
		result[index].TargetRoles = append([]string(nil), entry.TargetRoles...)
	}
	return result
}

func LookupTool(id ToolID) (ToolDescriptor, bool) {
	for _, entry := range toolCatalog {
		if entry.ID == id {
			entry.TargetRoles = append([]string(nil), entry.TargetRoles...)
			return entry, true
		}
	}
	return ToolDescriptor{}, false
}

// ValidateCatalog is intentionally testable so a new candidate cannot silently
// skip the review metadata that R1 requires.
func ValidateCatalog() error {
	seenID := make(map[ToolID]struct{}, len(toolCatalog))
	seenSource := make(map[string]struct{}, len(toolCatalog))
	for _, entry := range toolCatalog {
		if entry.ID == "" || entry.Revision == 0 || entry.Provider == "" || entry.SourceTool == "" ||
			entry.Category == "" || entry.Description == "" || entry.Effect == "" || len(entry.TargetRoles) == 0 ||
			(entry.State != CatalogStateActive && entry.State != CatalogStateCandidate) {
			return fmt.Errorf("invalid tool catalog entry %q", entry.ID)
		}
		if _, exists := seenID[entry.ID]; exists {
			return fmt.Errorf("duplicate tool id %q", entry.ID)
		}
		if _, exists := seenSource[entry.Provider+":"+entry.SourceTool]; exists {
			return fmt.Errorf("duplicate provider tool %q:%q", entry.Provider, entry.SourceTool)
		}
		seenID[entry.ID] = struct{}{}
		seenSource[entry.Provider+":"+entry.SourceTool] = struct{}{}
	}
	return nil
}
