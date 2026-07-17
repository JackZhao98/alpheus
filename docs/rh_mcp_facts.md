# Robinhood MCP production-shape facts

Last reviewed: 2026-07-16. This document is secret-free and may be committed.
Authenticated discovery was completed against the explicitly selected Agentic
account. OAuth state and raw responses remain outside the repository in 0600
files; the committed capability snapshot and fixtures contain no account data.

## Verified public facts

- Trading MCP resource: `https://agent.robinhood.com/mcp/trading`.
- The resource metadata advertises OAuth scope `internal`, bearer tokens in the
  header, and the Trading MCP resource itself as the authorization-server
  issuer. Its authorization metadata advertises PKCE `S256`, dynamic client
  registration, authorization-code and refresh-token grants, and token auth
  method `none`.
- Robinhood says the MCP can read all connected Robinhood accounts, balances,
  positions and transaction/order history, but it may place trades only in the
  dedicated Agentic account. Alpheus is intentionally narrower: its registered
  tool allowlist contains read tools only and production execution capability
  is absent before M11.
- Robinhood describes an Agentic account as a self-directed individual
  investing account and currently documents long equity and long option orders.
- Relevant documented read tool names are:
  `get_accounts`, `get_equity_positions`, `get_equity_quotes`,
  `get_equity_orders`, `get_equity_historicals`, `get_option_chains`,
  `get_option_instruments`, `get_option_quotes`, `get_option_positions`, and
  `get_option_orders`.
- The current public tool catalog does not document `get_market_hours`,
  `get_movers`, or `get_option_expirations`. Alpheus therefore does not guess
  those tool names; the corresponding production endpoints fail closed until
  authenticated discovery establishes a reviewed implementation.

Primary sources:

- [Robinhood: Agentic Trading overview](https://robinhood.com/us/en/support/articles/agentic-trading-overview/)
- [Robinhood: Trading with your agent](https://robinhood.com/us/en/support/articles/trading-with-your-agent/)
- [OAuth protected-resource metadata](https://agent.robinhood.com/.well-known/oauth-protected-resource/mcp/trading)
- [OAuth authorization-server metadata](https://agent.robinhood.com/.well-known/oauth-authorization-server/mcp/trading)

## Account-specific and schema facts

| Fact required by M8A | Status |
|---|---|
| Exact bound Agentic account and account type | Verified active `cash/individual`; exact ID is private and explicitly bound; no default-account selection |
| Spendable-funds behavior | `get_portfolio.buying_power.buying_power` is Alpheus's sole hard funds capacity under owner-approved amendment v1.4. The gate uses that exact value minus durable local reservations; `cash`, `pending_deposits` and unleveraged buying power are informational only. |
| Options approval level | Verified `option_level_2` |
| Exact supported order types and time-in-force values | Equity and single-leg options: market, limit, stop-market and stop-limit; GFD/GTC, subject to the session restrictions in the committed schemas |
| Equity/option quantity increments | Options are positive whole contracts. Equity decimals are documented only for regular-hours market orders; there is no single proven equity increment across order types, so the equity `Instrument` precision capability fails closed |
| Price ticks and sub-penny behavior | SPY option chain/instruments expose a fixed 0.01 tick. The schema supports above/below-cutoff tick rules; variable schedules fail closed. No exact equity tick field is exposed |
| Live `quote_max_age_sec` value | Human approved at 15 seconds for the read-only Robinhood session; startup still refuses zero |
| Option multiplier and non-standard deliverables | Standard SPY chain verified exact `100`, one underlying and null cash component. Anything else is unsupported |
| Buying-power source | `get_portfolio.buying_power.buying_power` is the normalized and authoritative hard-capacity source; `cash` is recorded separately for display only |
| Stable order and fill identifiers | Equity and option order IDs plus execution IDs are UUIDs; option executions are nested under legs |
| Cumulative-fill behavior | Equity uses `cumulative_quantity`; options use `processed_quantity`, with per-fill quantities under executions |
| Client-supplied id query/deduplication | Both place schemas accept UUID `ref_id` and explicitly document retry deduplication. Read-order schemas do not expose/query it, so live proof and automatic replacement remain disabled |

Additional authenticated findings:

- The current catalog has 49 tools. Alpheus registers 34 reviewed
  no-state-change tools: 32 data queries plus the two order-review simulations.
  The remaining 15 place/cancel/watchlist/scanner mutations are absent from the
  client allowlist and cannot be selected by the Cockpit.
- Equity quotes have independent `venue_bid_time` and `venue_ask_time`; the
  normalized timestamp is the older of the two. Option quotes use
  `updated_at`. Both paths reject zero, locked, crossed and malformed markets.
- Option instruments paginate by `next` cursor. Historical responses use
  `bars`, not the older guessed `historicals` field.
- `get_realized_pnl` is available and returns aggregate buckets/totals. Despite
  its schema describing `asset_classes` as optional, the live service rejects
  omission; Alpheus explicitly sends equity, option and crypto. Per-trade
  records remain available from `get_pnl_trade_history`.
- Provider option prices may contain eight fractional digits with only trailing
  zeros. Alpheus accepts them only when exactly representable at micro-dollar
  scale; non-zero precision beyond six digits is schema drift, never rounded.

The option `Instrument` capability now works for an exact option UUID backed by
the chain and instrument records. Equity `Instrument` precision still fails
closed because the provider does not expose one exact tick/increment contract
for every supported equity order type.

## Downstream decisions exposed by M8A

- M3D uses exact provider-authoritative buying power as the sole hard funds
  capacity. Durable local reservations close the pre-broker concurrency window;
  exact provider-hold add-back remains an M11 optimization and must never use
  approximate matching.
- Keep `ref_id` replacement/recovery disabled until M11 can prove the behavior
  with a human-approved canary. Discovery placed, reviewed, cancelled and
  replaced no orders.
