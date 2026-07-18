# Robinhood MCP production-shape facts

Last reviewed: 2026-07-17. This document is secret-free and may be committed.
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
| Equity/option quantity increments | Options are positive whole contracts. For the equity limit shape Alpheus sends, an owner-authorized live A/B probe proved a one-share increment: one share was accepted and 0.5 share was rejected before order creation. This evidence is not generalized to equity market or dollar-based orders. |
| Price ticks and sub-penny behavior | SPY option chain/instruments expose a fixed 0.01 tick. Owner-authorized equity reviews proved the limit schedule Alpheus uses: $0.01 above $1 and $0.0001 at or below $1. A 0.5001 limit passed precision validation; 0.50001 and 1.001 were rejected. |
| Live `quote_max_age_sec` value | Human approved at 15 seconds for the read-only Robinhood session; startup still refuses zero |
| Option multiplier and non-standard deliverables | Standard SPY chain verified exact `100`, one underlying and null cash component. Anything else is unsupported |
| Buying-power source | `get_portfolio.buying_power.buying_power` is the normalized and authoritative hard-capacity source; `cash` is recorded separately for display only |
| Stable order and fill identifiers | Equity and option order IDs plus execution IDs are UUIDs; option executions are nested under legs |
| Cumulative-fill behavior | Equity uses `cumulative_quantity`; options use `processed_quantity`, with per-fill quantities under executions |
| Client-supplied id query/deduplication | Both place schemas accept UUID `ref_id` and document retry deduplication. An owner-authorized $1 equity A/B probe verified that an identical same-ref replay created no duplicate while a fresh ref created a distinct order. Read-order schemas still do not expose/query `ref_id`; the replay returned an unknown outcome rather than the original order. A separately authorized reviewed option limit attempt plus one same-ref replay both returned unknown and produced zero orders, so option dedupe remains unproven and sanitized provider error capture is required before another probe. |

Additional authenticated findings:

- The committed capability snapshot has 49 tools. A 2026-07-17 live discovery
  returned one additional read tool, `get_option_historicals`; the four order
  schemas were unchanged. Alpheus still registers only its 34 reviewed
  no-state-change tools: 32 data queries plus the two order-review simulations.
  Unreviewed and state-changing tools are absent from the Cockpit allowlist.
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
- Exact-symbol `search` supplies the equity instrument UUID. Alpheus requires
  exactly one exact uppercase-symbol result and rejects missing, duplicate or
  lookalike-only results.
- An accepted equity placement may echo only the broker order ID rather than
  every canonical field. Alpheus immediately reads that order ID and validates
  exact identity, side, whole-share quantity, limit, trigger, GFD session,
  `regular_hours` and `placed_agent=agentic`. Missing canonical visibility is
  post-send uncertainty, never permission to resend with a fresh ref.

The option `Instrument` capability now works for an exact option UUID backed by
the chain and instrument records. Equity `Instrument` now supports only the
certified limit-order contract above. The live execution constructor remains
equity-only; option read metadata does not authorize an option mutation.

## Downstream decisions exposed by M8A

- M3D uses exact provider-authoritative buying power as the sole hard funds
  capacity. Durable local reservations close the pre-broker concurrency window;
  exact provider-hold add-back remains an M11 optimization and must never use
  approximate matching.
- Plan amendment v1.5 permits at most one byte-identical same-ref equity replay
  after an uncertain outcome. Pull-based reconciliation requires an exact,
  unique provider-visible fingerprint and remains human-gated unless an
  audited exclusive-writer mode is active. A fresh ref is always a new order;
  option automatic recovery remains disabled.
- The v1.5 recovery path is implemented: durable account-level
  unknown latch, canonical provider intent/fingerprint/time window, exact
  paged equity candidate matching, and human-only sole-candidate adoption with
  a mandatory re-pull. Commit `319f657` adds the exact v1.6 equity limit
  contract and live-only Provider wiring. The deployed stack remains read-only,
  the first Alpheus canary still requires a separate confirmation, and option
  mutations remain blocked.
