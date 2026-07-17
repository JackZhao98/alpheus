# Phase 1 — Safety foundation and production parity

[Back to Plan Index](INDEX.md)

> Frozen specification v1.1. M2.5–M2.9 are the safety foundation; M8A/M8B
> provide early production-read parity and visibility. Implementation status
> and the current target are tracked only in `INDEX.md`.

<!-- BEGIN FROZEN SPEC -->

## Milestone 2.5 (P0) — Computed risk in integer micro-dollars + request DTO split

**Problem (live, verified against the running system):** `risk.Classify` gates
on `op.MaxRiskUSD` — a number the proposer writes. On a $300 account with
`max_risk_per_trade_pct: 35` (cap $105), an option `open` of qty 1 at limit
3.00 — real cost `1 × 3.00 × 100 = $300` — declaring `"max_risk_usd": 10`
classifies **B**, executes, and drains the account to zero. Two more instances
of the same defect:

- `op.Short` is trusted, and the naked-short absolute only inspects options.
  `open` + `kind: equity` + `side: sell` + `short: false` classifies B and
  opens a short stock position in a cash account.
- `FakeBroker.GetAccount` reports `Equity = cash`, ignoring position market
  value. That short sale therefore *raises* reported equity ($300 → $400), and
  every cap is a percentage of equity — so the two defects multiply.

Money is also `float64` throughout. `/state` already shows `equity: -0.3` and
`293.99400000000014`. Fixing this later means migrating exposure, fills and PnL
after they are built and re-verifying every gate; the derived-risk arithmetic
introduced here is exactly what must not be written in float. Do it now.

**Spec:**

*Money AND quantity types (do this first — everything below is written in them):*
- `type Micros int64` — integer micro-dollars (1e-6 USD), one type for both
  amounts and prices. Cents is too coarse: sub-penny quotes are real. int64
  covers ±$9.2e12.
- `type Qty int64` — integer micro-units (1e-6 of a share/contract). **A float
  quantity keeps the money path in floating point** no matter what type the
  price is, because `qty × price` is where the money is made: `Qty` is not
  optional polish. Options must be whole contracts (`qty % 1_000_000 == 0` →
  otherwise 400); equities may be represented to 1e-6 internally, but the live
  adapter must also enforce the provider/account's discovered minimum increment
  before placement (M8A/M11).
- Dimensionless limits are fixed-point too: `PercentMicros int64` stores
  millionths of one percentage point (`35` in YAML → `35_000_000`), while
  `RatioMicros int64` stores millionths of one whole (`0.15` → `150_000`).
  Percentage caps use
  `floor(equity_micros * percent_micros / (100 * 1_000_000))`; rounding the
  allowed cap down is conservative. Relative spread is compared without a
  division: after quote sanity, compare
  `2*(ask-bid)*1_000_000 <= (ask+bid)*max_relative_spread_ratio_micros` in
  `big.Int`. No percentage or ratio becomes a float on the risk path.
- Replaces `float64` in: quotes, limits, cash/equity/settled, `required_cash`,
  `derived_max_risk`, exposure, orders, fills, PnL, position quantities, and all
  limits.yaml money values.
- `qty × price × multiplier` can exceed int64 at plausible inputs
  (1e9 micro-units × 1e9 micro-dollars = 1e18, and ×100 for options overflows).
  Do that arithmetic in `math/big.Int` — stdlib, no new dependency
  (invariant 5) — and convert back to `Micros` with an explicit range check;
  overflow is a REJECT, never a wrap.
- Every conversion and rounding site is explicit and **names its direction**:
  `required_cash` rounds **up**, `remaining_risk` rounds **up** (so the release
  rounds down — see M3A), realized PnL rounds **against** the account. State the
  direction in a comment at each site; a rounding direction that is only implied
  by the formula is how M3A's release was inverted in an earlier draft of this
  plan.
- No `float64` may appear in `internal/risk` or the exposure/PnL path — enforce
  with a test that greps the package.

*Wire encoding — the internal unit is NOT the API unit:*
- On the wire, `qty: 1` means **one share/contract** and `max_risk_usd: 10`
  means **ten dollars**. Callers, prompts and limits.yaml all speak human units;
  micro-units exist only inside the kernel. Getting this wrong by leaking the
  internal scale into the API would be a 1,000,000× error on every order.
- Decode via `json.Number` (or a decimal string) and convert **exactly**:
  parse sign / integer part / fraction manually, or via `math/big.Rat`. Never
  `float64` as an intermediate — `strconv.ParseFloat("10.00")*1e6` is not
  reliably `10000000`, and that rounding lands directly on the money path.
  Reject more than 6 fractional digits, and reject NaN/Inf/exponent forms
  rather than best-effort parsing them.
- `limits.yaml` money and quantity values decode through the same path:
  yaml.v3 will happily give you a `float64` for `35.5` if the target is one, so
  money keys must unmarshal from the raw scalar string via a custom
  `UnmarshalYAML`. A limit that is off by a float ulp is a limit that is wrong.
- Marshalling back out (responses, journal, console) renders `Micros`/`Qty` as
  decimal in human units, not as raw integers.
- Test: round-trip `0.000001`, `0.1`, `10.00`, `123456.654321` and the
  per-trade cap itself, asserting exact equality both directions; assert
  exponent forms such as `"1e-6"` are rejected; assert `qty: 1` on the wire
  produces `Qty(1_000_000)` internally.

*Request DTO split (invariant 10, structurally):*
- The propose handler decodes into a **request DTO** carrying only
  client-supplied fields. The kernel maps DTO → `risk.Operation`, filling
  derived fields itself. Kernel-derived fields (`DerivedMaxRisk`,
  `RequiredCash`, `VerifiedReduction`, resolved side/kind) live on
  `risk.Operation` only and are never present on the DTO — so
  `DisallowUnknownFields` (invariant 8) rejects any client that tries to send
  them. Adding a derived field to the decoded struct is the `VerifiedReduction`
  mistake waiting to happen; the DTO makes it structurally impossible.
- `MaxRiskUSD *Micros` — a pointer, so "omitted" and "explicitly 0" are
  distinguishable. Explicit 0 is a declaration of zero risk and must fail the
  mismatch check; only omission skips it.

*New `limits.yaml` keys (adding keys is allowed; see invariant 6):*
`execution_policy.fee_per_contract` (0), `fee_per_share` (0),
`risk_declaration_tolerance` (0.01 USD), `quote_max_age_sec` (used by
invariant 9; 0 disables the age check until M8A supplies timestamps).

*Computed risk:*
- For every `open` the kernel computes:
  - `multiplier` = 1 for equity. For an option it comes from kernel-owned
    instrument metadata (a FakeBroker fixture now, `marketdata.Provider` in
    M8A), never the request. The initial single-leg model accepts only a known
    standard multiplier of 100; unknown or non-standard contracts REJECT
    `unsupported_contract` until their deliverable model is explicitly
    supported. Persist the multiplier used so later PnL cannot change when
    reference data changes.
  - `approved_price_cap` = explicit `limit` when present, else the marketable
    ask from the sanity-checked quote. This is the worst price the kernel may
    pay and the price used for risk/cash reservation. Every open still requires
    a sane current quote for liquidity, marking and execution; an explicit limit
    is not a quote-sanity bypass. No sane quote → REJECT; never fall back to 0.
  - `working_price` is the initial broker limit. Honor the existing
    `execution_policy.start_at`: for `mid`, use the sane quote mid clamped at
    or below `approved_price_cap`. Risk is still computed at the cap, not at
    mid. Persist both values. This gives M5B room to improve a resting order
    without increasing its already-approved risk.
  - With `Qty` stored in micro-units, the scale division is mandatory:
    `required_cash = ceil_div(qty_micro * approved_price_cap_micros * multiplier,
    1_000_000) + fees`. Compute the numerator in `big.Int`, divide once with
    ceiling semantics, then range-check before converting to `Micros`. Omitting
    the division is a 1,000,000× risk error.
  - `derived_max_risk`:
    - long option (buy to open): `= required_cash` — the premium is the max loss.
    - equity long: `= required_cash`. A *planned* stop is not a guaranteed loss
      and MUST NOT reduce derived risk until native stop orders exist (M5B).
      Document this in code; it is the difference between a plan and a broker
      guarantee.
- `checks["per_trade_budget"]` uses `derived_max_risk`;
  `checks["total_open_risk"]` uses `day.OpenRisk + derived_max_risk`. Neither
  may read the payload.
- `required_cash > AccountState.BuyingPower` is an absolute REJECT
  `insufficient_buying_power`, not a human-overridable Class C: an approval
  cannot manufacture broker capacity. M3D may add the stricter settled-cash
  check once the account model is known.
  `AccountState.BuyingPower` has a normalized contract: buying power **before
  subtracting Alpheus's own currently held open-order reservations**, but after
  filled positions and any external activity. FakeBroker can supply that
  directly. A live adapter must add back provider-side holds attributable to the
  same durable Alpheus orders before the kernel subtracts `open_reservation`, or
  otherwise prove equivalent semantics; feeding a provider's already-net
  "available buying power" into the formula would double-subtract every resting
  order. M8A records whether normalization is possible; M11 does not ship if it
  is not.
- Payload `max_risk_usd` is a **declaration**: if present and
  `abs(declared - derived_max_risk) > risk_declaration_tolerance` → REJECT
  `risk_declaration_mismatch`. Rationale: a proposer that misstates its own
  risk is malfunctioning, and there is nothing a human could usefully approve;
  but the declaration is worth keeping as an honesty signal in the journal.

*`open` + `sell` is REJECT, with no coverage exception:*
- In the current **single-leg** model there is no representation for "this
  option short is covered by that stock long" — a covered call is a short call
  covered by a long position in the *underlying*: different symbol, different
  kind. A rule that looks for a covering long in the *same* symbol and kind is
  describing a `close`, not a cover. Any `open` with `side: sell` → REJECT
  `uncovered_short`, both kinds.
- `allow_naked_short_options: false` remains and is subsumed by this.
- `op.Short` becomes advisory: keep the field for the journal, never gate on it.
- Covered/multi-leg strategies need a real position-leg model and are a
  separate future design. Do not approximate one here.

*Equity means equity, or admits it does not know:*
- `broker.AccountState.Equity` = `cash + Σ(liquidation_value(position))`.
  A long marks at sane bid and its positive value rounds down; a legacy short
  liability marks at sane ask and its magnitude rounds up. Mid would overstate
  immediately realizable equity by half the spread and inflate every
  percentage cap. Compute quantity × price × multiplier in `big.Int` with the
  M2.5 scale division. `BuyingPower` / `SettledCash` stay cash-based.
  `broker.Position` persists kernel-derived `Kind` and `Multiplier`; never infer
  either from symbol text or a proposal.
- **There is no `AvgPrice` fallback.** Cost is not market value: a position that
  has fallen 90% still marks at cost, which *overstates* equity, and every cap
  is a percentage of equity — so the fallback inflates the caps exactly when the
  book is in trouble. Instead, if any held position has no usable mark, equity
  is **degraded**: `AccountState.EquityKnown = false`, and every gate that reads
  equity fails closed — `open` → REJECT `equity_unknown`. Class A
  (verified close / cancel / tighten_stop) does not read equity and MUST keep
  working when its own dependencies are sane (AUDIT I6). This bypasses the
  equity/breaker gate, not invariant 9: a close whose own quote is unusable
  cannot invent an executable limit and makes no broker call.
- A known equity at or below zero cannot produce a meaningful positive
  percentage cap: every `open` REJECTs `nonpositive_equity`; verified Class-A
  exits remain available.

*Visibility:*
- The propose response and the persisted operation record `derived_max_risk`
  and `required_cash`, so the console, the journal and the audit all see what
  the kernel actually gated on.

**Acceptance:**
- Option open qty 1 @ 3.00, $300 account, declaring `max_risk_usd: 10` →
  REJECT `risk_declaration_mismatch`, no broker order. Declaring 300 → Class C
  with `per_trade_budget` failed (300 > 105). Declaring 0 explicitly → REJECT
  mismatch (not skipped). Omitting → Class C `per_trade_budget`. In no case B.
- An open whose `required_cash` is one micro-dollar above broker buying power →
  REJECT `insufficient_buying_power`; equality is allowed before other checks.
- A DTO carrying `derived_max_risk` or `verified_reduction` → 400 unknown field.
- `open`+`sell` → REJECT `uncovered_short` for both equity and option, with and
  without any position present.
- Seed a short directly through FakeBroker: sale proceeds no longer raise
  `/state` equity; with a spread, liquidation equity is conservatively below
  the pre-sale value rather than +$100 above it.
- Hold positions in symbols A and B; remove A's quote but keep B sane → `/state`
  reports `equity_known: false`, an `open` is REJECT `equity_unknown`, and a
  verified close of B still executes. Closing unquoted A fails
  `market_data_unavailable` with no broker call rather than fabricating a price.
- Set known equity to 0 or negative → every `open` REJECTs
  `nonpositive_equity`, while a verified close still executes.
- Option `open` with `qty` = 1.5 contracts → 400; equity `open` with
  qty = 0.5 shares → accepted.
- An option with missing or non-standard multiplier metadata → REJECT
  `unsupported_contract`; sending a `multiplier` field in the request → 400
  unknown field.
- Overflow guard: an `open` whose `qty × price × multiplier` would exceed int64
  → REJECT, not a wrapped negative.
- Table-driven risk tests: `derived_max_risk` exactly at the per-trade cap
  passes; one micro-dollar above fails.
- Exact-limit tests cover a fractional equity amount, 35% and 80% caps, the
  `0.15` spread ratio, and a value one micro-unit on either side of each result;
  no test obtains its expected value through floating-point arithmetic.
- No `float64` in `internal/risk` or the exposure/PnL path (enforced by test).

---

## Milestone 2.6 (P0) — Run modes, authentication, kill switch

**Problem:** the old plan deferred auth to M7 ("No auth; deployment is
private"). But M3C adds `POST /breaker/resume` and M4 adds approve-then-
execute: both move money, and neither can ship unauthenticated. The review
handler also takes `reviewer` from the request body, so the audit trail records
whatever the caller types — the identity half of invariant 10. And M10 feeds
`blackboard`/`lessons` into the LLM context, so **every unauthenticated write
endpoint is a context-injection entry point**, not merely a data-integrity one.

**Spec:**
- `TRADING_MODE = sim | shadow | read_only | live` (env, default `sim`).
  - `sim` — FakeBroker only; `/sim/*` mounted.
  - `shadow` — real marketdata permitted; every operation forced
    `shadow: true`; broker mutations structurally unreachable.
  - `read_only` — reads only; every write endpoint returns 405, including
    journal/blackboard/review/halt (not just `POST /operations`).
  - `live` — real broker; `/sim/*` NOT mounted (404).
- Three tokens, constant-time compared, required unless `TRADING_MODE=sim`:

  | Token | Grants |
  |---|---|
  | `RUNTIME_TOKEN` | `POST /operations`, `POST /journal`, `PUT /blackboard`, `POST /telemetry` (M10) |
  | `ADMIN_TOKEN` | everything above, plus `POST /operations/{id}/review`, `POST /breaker/resume`, `POST /halt`, `/sim/*` |
  | `KERNEL_TOKEN` | kernel → runtime `POST /wake` (M6), verified by the runtime |

  A leaked runtime token must not be able to approve its own Class-C. 401 on
  miss. **Every write endpoint is on this table** — an endpoint that does not
  appear here does not ship.
- Reads (`/state`, `/limits`, `/operations`, `/lessons`, `/blackboard`,
  `/market/*`) require any valid token outside `sim` mode.
- Reviewer identity = the authenticated subject. Remove `reviewer` from the
  review request body entirely; record the subject.
- `LIVE_TRADING_ENABLED` (default false) and `LIVE_ACCOUNT_ID` (no default).
  Startup **fails loudly** (non-zero exit, not a first-request error) when
  `TRADING_MODE=live` and any of `ADMIN_TOKEN` / `RUNTIME_TOKEN` /
  `LIVE_ACCOUNT_ID` is empty, or `LIVE_TRADING_ENABLED` is false.
- Account binding: every broker mutation asserts the adapter's account id ==
  `LIVE_ACCOUNT_ID`; mismatch → refuse + `account_binding_violation` event.
- Kill switch: `POST /halt {"reason":…}` (admin) sets a global halt that
  REJECTs every `open` in every ledger. Verified Class-A closes and cancels
  still execute (AUDIT I6 — the whole point of the fast path). Every transition
  emits an event.
- Startup logs the mode; `GET /state` reports it.

**Acceptance:** `TRADING_MODE=live` without `ADMIN_TOKEN` → process exits
non-zero at startup; review without a bearer → 401; review with
`RUNTIME_TOKEN` → 401; `POST /journal` and `PUT /blackboard` without a token →
401; runtime `/wake` without `KERNEL_TOKEN` → 401; recorded reviewer is the
auth subject regardless of body content; `POST /sim/quote` in live mode → 404;
after `POST /halt`, open → REJECT and a verified close → still Class A and
executed.

---

## Milestone 8A — Production read providers + Robinhood MCP capability discovery

**Sequence and safety boundary:** land this immediately after M2.6, before
M2.7. M2.5 must land first so production data enters fixed-point domain types;
M2.6 must land first so `read_only` / `shadow`, authentication, secret
handling and exact `LIVE_ACCOUNT_ID` binding already exist. This milestone is
read-only: it must be structurally unable to place, cancel, replace or approve
an order. Real broker mutations remain M11.

**Context — verify, do not assume.** Robinhood MCP uses a separately funded
Agentic account. Public product pages and the user's main-account settings do
not establish the Agentic account's type, settlement semantics, approval level,
tool schemas, fill identity or buying-power semantics. Connect early so the
remaining ledger and execution milestones are designed against production
shapes rather than a late guess.

**Provider capability split:**
- Replace the monolithic venue boundary with explicit capabilities:
  ```go
  type AccountProvider interface {
      Account(ctx context.Context) (AccountState, error)
      Positions(ctx context.Context) ([]Position, error)
      OpenOrders(ctx context.Context) ([]ReadOrder, error)
      RecentFills(ctx context.Context, since time.Time) ([]ReadFill, error)
      AccountID(ctx context.Context) (string, error)
  }

  type ExecutionProvider interface {
      PlaceLimitOrder(ctx context.Context, req PlaceRequest) (OrderResult, error)
      CancelOrder(ctx context.Context, brokerOrderID string) (OrderResult, error)
      GetOrder(ctx context.Context, brokerOrderID string) (OrderResult, error)
      FindOrderByClientID(ctx context.Context, clientOrderID string) (OrderResult, error)
  }
  ```
  FakeBroker may implement both for simulation. Production `read_only` and
  `shadow` construct only `AccountProvider` plus the marketdata provider;
  no production `ExecutionProvider` is registered or reachable before M11.
  A method that returns "disabled" is weaker than absence: construction and
  routing must make writes structurally unreachable.
- New package `kernel/internal/marketdata`:
  ```go
  type Provider interface {
      Quote(ctx context.Context, symbol string) (broker.Quote, error)
      Instrument(ctx context.Context, symbol string) (InstrumentSpec, error)
      Chain(ctx context.Context, underlying, expiry string, window PercentMicros) ([]OptionQuote, error)
      Expirations(ctx context.Context, underlying string) ([]string, error)
      Bars(ctx context.Context, symbol string, days int) ([]Bar, error)
      Movers(ctx context.Context, direction string, n int) ([]Mover, error)
      Hours(ctx context.Context) (MarketHours, error)
  }
  ```
  Server-side caps are part of the contract: window <= 15 percentage points,
  days <= 30, n <= 10. Clamp rather than forwarding unbounded work.
- Every normalized type uses M2.5 `Micros`, `Qty`, exact multipliers and UTC
  timestamps. Provider payload numbers never pass through `float64).
  Account/position/order/fill and instrument responses include `source`,
  `as_of` and stable external identifiers where the provider supplies them.

**Robinhood MCP adapter:**
- Use `github.com/modelcontextprotocol/go-sdk`. There is exactly one
  `CallTool` boundary with per-call deadlines, a persistent kernel-owned OAuth
  session, serialized calls, idle cleanup, one reconnect on transport failure,
  a bounded TTL cache and a token-bucket rate limiter. These lifecycle ideas may
  be informed by the neighboring tofi-core implementation, but Alpheus does not
  import tofi-core packages or its MCP SDK.
- Never auto-select the first/default account. Every read resolves and then
  exactly matches `LIVE_ACCOUNT_ID`; zero or multiple matches, or a different
  account, fail closed and emit an account-binding event.
- Decode versioned response envelopes strictly into provider fixtures. Do not
  recursively search arbitrary JSON for the first field with a familiar name:
  a renamed or relocated money field is schema drift, not permission to guess.
  Discard fields outside the normalized contract after validation.
- Credentials and refreshed OAuth state live only in a kernel secret file or
  volume with mode 0600. They never enter the repository, database payloads,
  API responses, events or logs.

**Capability and production-shape snapshots:**
- An explicit authenticated discovery command lists tool names plus input/output
  schemas and writes a reviewed, secret-free
  `docs/rh_mcp_capabilities.json`. Production never mutates the source tree.
  Live read-only startup compares the server with the committed snapshot and
  fails closed on missing, renamed or incompatible required tools.
- Capture redacted golden fixtures for account, positions, open orders, recent
  fills, quote, instrument and option-chain responses. Preserve structure and
  edge cases while removing account numbers, tokens and user data. Offline CI
  decodes these fixtures through the real adapter and runs the same semantic
  contract suite as FakeProvider.
- Record as facts: account type, settlement behavior, options level, supported
  assets/order types, quantity increments, price ticks, contract
  multiplier/deliverables, buying-power/settled-cash semantics, order and fill
  identifiers, cumulative fill behavior, and whether the order API accepts,
  queries and deduplicates a client-supplied id. Read-only discovery may record
  documentation for dedupe but cannot safely prove it with a live duplicate
  order; mark it unverified and keep automatic replacement disabled.
- M3D remains blocked until the account/settlement facts are recorded. M2.8 and
  M2.9 use the discovered order/fill shapes while implementing their durable
  models rather than discovering them again at M11.

**Kernel integration:**
- `TRADING_MODE=read_only` reads the production account and marketdata but
  mounts no write route. `shadow` uses production reads with the paper ledger
  and still never reaches production execution. `sim` uses Fake providers.
- Kernel endpoints are the only Agent doorway:
  `GET /market/quote/{symbol}`,
  `GET /market/chain/{underlying}?expiry=&window_pct=`,
  `GET /market/expirations/{underlying}`,
  `GET /market/bars/{symbol}?days=`,
  `GET /market/movers?dir=&n=`, `GET /market/hours`, and authenticated
  `GET /provider/status`. The status endpoint exposes connection state,
  snapshot version, last successful read, last sanitized error and schema-drift
  state — never raw provider payloads or secrets.
- Risk, state and execution consume the same normalized quote provider
  instance. FakeBroker consumes FakeProvider's quote map. Two independent quote
  sources would let the gate approve one market and the venue simulate another.
- Quotes carry `source` and `as_of`; stale, locked, crossed, non-positive or
  incomplete data fails invariant 9 closed.

**Acceptance:** offline fixture tests fail on a renamed/nested required money
field, extra precision, unknown multiplier or missing stable identifier;
FakeProvider and Robinhood fixture adapters pass one shared semantic contract
suite. In `read_only`, account/positions/orders/fills/quotes load for exactly
`LIVE_ACCOUNT_ID`, every write endpoint is 405 and no mutation tool is
registered. A non-allowlisted or ambiguous account fails closed. Capability
snapshot drift rejects startup. An env-gated integration test reconnects after
a dropped transport, reuses protected OAuth state across restart, respects
timeouts/rate limits, and a log/API scan contains no token, account number or
raw payload.

---

## Milestone 8B — Read-only Trading Cockpit

**Purpose:** expose the production-shape integration early enough for a human to
inspect it while M2.7 onward is still being built. This is the durable shell of
the later review console, not a throwaway demo and not an early mutation
surface.

**Spec:**
- Kernel serves one embedded, mobile-friendly HTML/JS application with no build
  step or new framework. It works in `sim`, `shadow` and `read_only`.
- Display: trading mode; provider connection and capability-snapshot status;
  masked account id; account type, equity, buying power and settled cash;
  positions with normalized quantity/kind/multiplier and current quote; market
  hours; live/shadow day ledgers; paginated recent operations; and per-panel
  `source`, `as_of`, stale state and last sanitized error.
- Move the read-only list API here:
  `GET /operations?status=&limit=&cursor=`, cursor over `(ts,id)`, limit
  clamped to 100. Provider order/fill diagnostics are clearly labeled as
  external read data until M2.9 creates kernel-owned durable rows.
- Include a contract-diagnostics panel showing the normalized field set,
  capability snapshot version and pass/fail parity checks against committed
  Fake/Robinhood fixtures. Never render raw MCP responses, tokens, full account
  numbers or secret paths.
- Outside `sim`, the page asks once for a read-capable token, holds it only in
  memory and sends it as a bearer header. Never use URL parameters,
  localStorage, cookies or embedded environment values.
- All stored/provider text is untrusted: render with `textContent`, never
  `innerHTML`; send a restrictive CSP with no `unsafe-inline`.
- This milestone adds no mutation control and no new write endpoint. There are
  no Approve, Reject, Halt, Resume, Place, Cancel or Replace buttons. M7 adds
  authenticated review controls only after the underlying execution paths are
  safe.

**Acceptance:** with FakeProvider the cockpit renders every panel and pagination
without console errors; with an env-gated `read_only` Robinhood integration it
shows normalized production data plus provenance and snapshot status. Removing
or renaming a fixture field makes the diagnostics fail visibly while the kernel
also fails closed. A phone-width viewport remains usable. Injected
`<img src=x onerror=...>` text executes no script. Browser/network inspection
finds no token persistence, full account id, raw provider payload or mutation
request.

---

## Milestone 2.7 (P0) — Migrations, DB deadlines, idempotency

**Problem:** `db/init.sql` only runs on a fresh volume, so no schema change
after M2 can reach an existing database — and M2.8 onward is almost entirely
schema. Separately: pausing postgres mid-propose makes the request hang past
the client's timeout and the order is still placed once the DB returns — the
caller believes it failed. And there is no idempotency key, so a client retry
creates a second operation and a second order.

**Spec:**
- **Migrations.** `db/migrations/NNNN_name.sql`, applied by the kernel at
  startup inside a transaction, tracked in `schema_migrations(version,
  checksum, applied_at)`, serialized by an advisory lock so multiple instances
  are safe. `db/init.sql` becomes `0001_init.sql`, the compose bootstrap is
  updated to use the migrator, and an applied migration is never edited:
  checksum mismatch fails startup.
  **Existing M2 volumes need an explicit baseline path:** when
  `schema_migrations` is absent but legacy tables exist, validate their expected
  tables/columns/index definitions against a hard-coded M2 fingerprint, create
  the migrations table and record 0001 as baselined without replaying its CREATE
  statements. An empty database executes 0001 normally. A partial or unknown
  legacy schema fails with a repair message; never guess or stamp it current.
  Plain SQL + lib/pq; no ORM (invariant 5). Every later milestone ships its own
  migration.
- **DB deadlines.** Every DB call carries a context deadline (`DB_TIMEOUT_MS`,
  default 3000). Exceeding it → 503 and **no broker call**. Fail closed; never
  hang past the client and execute later.
- **Idempotency.** Require an `Idempotency-Key` header when
  `TRADING_MODE=live` (client-supplied, 1–200 visible ASCII characters). Persist
  it as `idempotency_key` plus a SHA-256 `request_hash` of the normalized request
  DTO in internal fixed-point units. Derived quote/risk fields are deliberately
  excluded: retries bind to the same client intent, not a later market snapshot.
  Unique index on **`(authenticated_subject, idempotency_key)`** — the subject
  from M2.6, NOT
  `proposer`. `proposer` is a payload field, so keying on it means a caller can
  bypass its own idempotency by changing one string: invariant 10 again, in the
  place where the consequence is a duplicate live order. `proposer` remains as
  role attribution on the operation row and is stored separately; it never
  participates in a uniqueness constraint or a gate.
  Acquire a transaction advisory lock derived from `(authenticated_subject,
  idempotency_key)` before taking the ledger gate lock; this lock order is
  global: **idempotency → ledger gate → ledger/symbol**. The ledger gate is
  day-scoped through M2.9 and becomes a stable per-ledger key in M3A. Under the
  idempotency lock, a repeat with the
  same hash returns the ORIGINAL operation id and current status with 200 — no
  second classification, grant, reservation, attempt or broker call. Reusing
  the key with a different hash returns 409 `idempotency_key_reused`; silently
  returning the first, different trade would be a dishonest success response.
- agent-runtime generates one opaque key per intended proposal and reuses it for
  every HTTP retry of that intent; it does not hash the trade as the key, because
  two intentionally separate identical trades must remain possible. Once M6
  supplies a stable `(role,occurrence_id)`, include it in the key namespace so a
  retried wake reuses the same intent identity.

**Acceptance:** a known M2 volume is baselined without data loss and receives the
next migration; a fresh database executes 0001; a partial legacy schema and an
edited applied migration both fail startup. Concurrent kernel starts apply a
migration exactly once; same `idempotency_key` twice →
one operation, the second returns the first's id; 20 concurrent identical keys
under the barrier harness → exactly one operation; same key with any changed
client-intent field → 409 and no new operation; force agent-runtime's first
response read to time out and verify its retry returns the original operation;
`docker compose pause db`
during propose → 503 within `DB_TIMEOUT_MS`, no broker call, and recovery
without restart.

---

## Milestone 2.8 (P0) — Execution attempts, trade grants, close reservation, reconciler

**Problem:** today the kernel commits the operation row and then calls the
broker. If it dies after the broker accepts but before the id is recorded,
nothing can say whether an order exists — and there is no stable id to ask
about, because the id is invented by the broker. Meanwhile `executionMu` is a
process mutex: it works only because the FakeBroker fill happens *inside* the
critical section, so a second close sees the reduced position. Any move to
multi-instance, or to execution-after-commit, breaks that silently and
concurrent closes go reverse-short. The close reservation must be durable, not
a side effect of holding a mutex during a synchronous fill. M3A applies the
same entitlement-before-effect rule to opens once durable fills and exposure
exist; keeping that work later avoids a milestone that consumes tables its
predecessors have not created.

**Spec:**
- **Stable client id, persisted before the call.** `broker.Adapter` gains:
  ```go
  PlaceLimitOrder(clientOrderID, symbol, side string, qty Qty,
                  limit Micros, kind string) (OrderResult, error)
  FindOrderByClientID(clientOrderID string) (OrderResult, error) // ErrNotFound if not currently visible
  ```
  The kernel generates `client_order_id` and **commits it before any broker
  call**. This is what makes invariant 11 recoverable: after a crash the
  reconciler has something to ask about.
  **Two live-trading capabilities, recorded separately in M8A:**
  (a) the broker can be queried by client id; (b) the broker **deduplicates**
  on it. Without (b), `NotFound` is ambiguous — never-arrived vs in-flight — and
  re-placing is a coin flip on a duplicate order. An adapter with (a) but not
  (b) may only escalate to a human on ambiguity, never re-place. An adapter with
  neither cannot trade live. Because M8A is read-only, a documented dedupe
  claim remains unverified and automatic re-place stays disabled unless a
  provider-supported sandbox or non-money test proves it without risking two
  live orders.
- **One row per broker effect, not per operation.** A single operation causes
  many effects once M5B reprices, so `operation_id` must NOT be unique. There
  are exactly two effect kinds, and they recover differently:
  ```
  execution_attempt (
    id UUID PRIMARY KEY,
    operation_id UUID NOT NULL REFERENCES operations(id), seq int NOT NULL,
    close_reservation_id UUID REFERENCES close_reservation(id), -- null for opens
    intent text NOT NULL,               -- place | cancel   (replace = cancel + place)
    client_order_id text UNIQUE,        -- place only
    target_broker_order_id text,        -- cancel only
    state text NOT NULL,                -- pending|claimed|placed|settled|failed|unknown
    broker_order_id text, qty bigint, limit_micros bigint,
    attempt int NOT NULL DEFAULT 0, claimed_by text,
    created_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz, resolved_at timestamptz, last_error text,
    UNIQUE (operation_id, seq),
    CHECK (seq > 0 AND attempt >= 0),
    CHECK (state IN ('pending','claimed','placed','settled','failed','unknown')),
    CHECK ((intent='place' AND client_order_id IS NOT NULL
                          AND target_broker_order_id IS NULL
                          AND qty > 0 AND limit_micros > 0) OR
           (intent='cancel' AND client_order_id IS NULL
                           AND target_broker_order_id IS NOT NULL
                           AND qty IS NULL AND limit_micros IS NULL))
  )
  ```
  A **cancel has no client_order_id of its own** — it names an existing broker
  order. Its recovery is `GetOrder(target_broker_order_id)` and reading the
  resulting state, not `FindOrderByClientID`. `replace` is not an intent: it is
  a cancel attempt followed by a place attempt, each recoverable on its own.
- **Authorization records are action-specific.** In one gate transaction:
  - every Class-B `open`, live or shadow, writes the operation plus one durable
    daily `trade_grant` (below); a live open also writes
    `attempt(seq=1,pending)`;
  - a verified live close writes operation + close reservation + place attempt;
    live cancel writes its cancel attempt; `tighten_stop` has no broker effect
    and therefore no execution attempt;
  - **Class C writes no grant/reservation/attempt** — a pending attempt would be
    an unapproved order waiting for a worker. REJECT writes none.
  Shadow retains its landed classification/journal-only execution behavior
  until M3A introduces the durable paper executor; it never creates a
  broker-place attempt and never reaches the broker. M3A upgrades both live and
  shadow opens to create an open reservation with the attempt; M4 later creates
  grant + reservation + attempt at approval.
- **Daily trade grant is independent and irreversible.** New table:
  ```
  trade_grant (
    operation_id UUID PRIMARY KEY REFERENCES operations(id),
    ledger text NOT NULL, market_day date NOT NULL,
    authorized_risk_micros bigint,
    risk_source text NOT NULL,             -- computed | legacy_unknown
    granted_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((risk_source='computed' AND authorized_risk_micros > 0) OR
           (risk_source='legacy_unknown' AND authorized_risk_micros IS NULL))
  )
  ```
  Add an index on `(ledger,market_day)` for the gate query.
  New grants always use `risk_source='computed'` and
  `authorized_risk_micros` is M2.5's kernel-derived risk at the persisted cap;
  it is immutable and later powers M11's cumulative live canary cap.
  `CountTradesForDay` counts `trade_grant` rows, never operation class/status.
  Once inserted, a grant is never deleted or released: a broker failure still
  burns the slot because `max_new_trades_per_day` contains runaway loops. A
  proposal that never earns execution entitlement creates no grant. The M2.8
  migration backfills one grant for every previously admitted Class-B `open`,
  including operations that later failed, before switching the query. Derive
  `ledger` with M2's fail-closed rule
  `COALESCE((payload->>'shadow')::bool,false)` and derive `market_day` from the
  operation timestamp in `TZ_MARKET`. Use a persisted M2.5 derived risk when it
  exists; older operations become `legacy_unknown` with NULL risk rather than
  trusting their declared payload or recomputing at a new quote. Never use
  migration-day `now()`. This preserves live/shadow isolation and removes M4's
  future Class-C counting bypass.
- **Claim by CAS**, not by mutex:
  `UPDATE execution_attempt SET state='claimed', attempt=attempt+1,
   claimed_by=$instance, claimed_at=now()
   WHERE id=$1 AND state='pending' RETURNING *` — 0 rows means someone else
  owns it. Delete `executionMu`.
- **A claim is a lease, and `attempt` is its fencing token.** `claimed` does not
  mean abandoned — it is also the state of a worker currently blocked on the
  broker. A reconciler that sweeps `claimed` on sight races the very worker that
  owns it, and both then reason about the same in-flight order. So:
  - the reconciler may only touch `claimed` rows older than `CLAIM_TIMEOUT`, and
    steals them by CAS: `... WHERE id=$1 AND state='claimed' AND claimed_at <
    now() - $timeout AND attempt=$seen` — bumping `attempt`;
  - the original worker's write-back is conditional on still owning the lease:
    `... WHERE id=$1 AND attempt=$mine`. If its claim was stolen, its write is a
    no-op rather than a clobber, and it must re-read before doing anything else.
  - `CLAIM_TIMEOUT` must exceed the broker call timeout, or the sweeper is
    guaranteed to race healthy workers.
  Config: `BROKER_TIMEOUT_MS` default 10000, `CLAIM_TIMEOUT_MS` default 30000,
  and `ATTEMPT_STALE_MS` default 3000; startup rejects non-positive values or
  `CLAIM_TIMEOUT_MS <= BROKER_TIMEOUT_MS`.
- **Close reservation is its own object, not a property of an attempt.**
  ```
  close_reservation (
    id UUID PRIMARY KEY,
    operation_id UUID UNIQUE NOT NULL REFERENCES operations(id),
    ledger text NOT NULL, symbol text NOT NULL,
    original_qty bigint NOT NULL, remaining_qty bigint NOT NULL,
    state text NOT NULL,                       -- held | released
    created_at timestamptz NOT NULL, released_at timestamptz,
    CHECK (original_qty > 0 AND remaining_qty >= 0
           AND remaining_qty <= original_qty),
    CHECK (state IN ('held','released'))
  )
  ```
  An attempt cannot carry the reservation: M5B turns one close into a chain of
  place/cancel/place attempts. Summing non-terminal *attempts* double-counts the
  same lot across a cancel+place pair (fails closed, blocking legitimate exits);
  releasing when an *attempt* goes terminal drops the reservation in the gap
  between the cancel settling and the replacement being accepted (fails open,
  which is the bug this milestone exists to prevent). The reservation is held
  for the lifetime of the **close operation**, not of any attempt. Attempts
  reference it; they do not constitute it.
  Its `remaining_qty` **decrements on each close fill**, in the same transaction
  as the fill (M2.9) — a partial fill of 1 against a reservation of 3 moves the
  position 5→4 and the reservation 3→2, keeping `available` at 2. Holding the
  full 3 until the operation terminates would over-reserve by the filled amount
  and block legitimate exits (fails closed, but wrongly). The row moves to
  `released` when `remaining_qty` reaches 0, or only after every related broker
  effect is conclusively terminal and all of the broker's cumulative filled
  quantity has durable fill rows. `unknown` is not terminal and keeps the
  reservation held; an operator cannot "abandon" ambiguity into free closable
  quantity. Release happens exactly once.
- **Reservation flow.** In one transaction:
  1. take a transaction-scoped advisory lock on `(ledger, symbol)` — narrow, so
     a slow symbol cannot stall the whole kernel the way `executionMu` does;
  2. read the broker position (bounded by a broker timeout; this is the one
     network call inside the lock and the tradeoff is deliberate — the blast
     radius is one symbol);
  3. `available = abs(position.qty) − Σ(remaining_qty of held
     close_reservations for that (ledger, symbol))`;
  4. `op.Qty > available` → 400 `insufficient closable quantity`;
  5. insert `close_reservation(held)` + `attempt(seq=1, pending)` and commit.
  A filled close releases because the position itself has moved; a *resting*
  close keeps its reservation across any number of reprices — which is exactly
  the M1 finding (three resting sells against a one-lot long) fixed here rather
  than in M5B.
- **Reconciler.** A goroutine that resolves, at startup and on a ticker:
  - `pending` attempts older than `ATTEMPT_STALE_MS` — a crash between commit
    and claim leaves these, and nothing else would ever pick them up. They are
    unclaimed and the broker has never heard of them. **Re-run the full gate
    before executing one**, exactly as M4 does at approval: a resurrected
    attempt is an execution at today's prices on a decision made before the
    crash, and the breaker may have tripped, the quote may be stale, the
    position may be gone. When rebuilding day-state, exclude this operation's
    already-owned `trade_grant`; after M3A, also exclude its own open
    reservation before applying the operation once. Revalidation may lower the
    working price but never increases the persisted quantity, instrument
    multiplier or M2.5 `approved_price_cap`; a recovery is not fresh authority.
    Gate passes → claim and
    execute; gate fails or the operation is past `proposal_ttl_sec` →
    `failed`, release any remaining risk/cash/close reservation, retain its
    immutable trade grant, and emit an event. Never execute a stale pending
    unexamined.
  - `claimed` and `unknown` attempts — resolve by **query**: `place` via
    `FindOrderByClientID`, `cancel` via `GetOrder(target_broker_order_id)`.
    Never blind-retry to find out (invariant 11). Found ⇒ adopt the broker's
    state. NotFound does **not** prove the original request never arrived. Only
    when provider-side deduplication has been independently verified may the
    reconciler retry the identical `client_order_id`; otherwise the result is
    ambiguous ⇒ keep `unknown`, escalate, and do not re-place.
  - Reservations whose operation is terminal, has no unresolved attempt, and
    whose broker cumulative fill quantity exactly matches durable fills, but
    whose row is still `held` — release them; an orphan reservation silently
    blocks future exits. If any proof is missing, keep held and escalate rather
    than freeing quantity optimistically.
- Move `dayState()` off any lock it does not need: a verified close does not
  consult day-state, and M3C must keep it that way so a close still works while
  halted (I6).

**Acceptance:**
- Force a Class-B open's broker attempt to fail: its `trade_grant` remains and
  consumes the daily slot. Six failed-but-authorized opens make the seventh C
  on `daily_trade_count`; a REJECT and pending-review operation create no
  grant. Repeat independently for shadow and confirm live remains unchanged.
- Barrier: 20 concurrent closes of qty=N against a position of N → exactly one
  places; the rest 400; the position can never go reverse. Repeat with the
  broker's fill artificially delayed past commit — the reservation, not the
  fill, must be what stops the second close.
- **Claim lease:** a worker holding a `claimed` attempt and blocked on a slow
  broker is NOT swept — the reconciler leaves `claimed` rows younger than
  `CLAIM_TIMEOUT` alone. Force a steal by ageing the claim: the original
  worker's late write-back is a no-op (fencing token), not a clobber, and the
  broker still sees exactly one order.
- **Three crash windows, three different correct answers.** They must be tested
  separately; a single "kill it and see" test cannot distinguish them, and
  asserting one order count for all three would be wrong for two of them:

  | Crash point | Orders at broker | Required outcome |
  |---|---|---|
  | after attempt commit, before claim | **0** | reconciler picks up the stale `pending` and **re-gates it**: pass → execute, fail/expired → `failed` + resource reservation released, trade grant retained. Never stranded, never executed unexamined |
  | after claim, before broker call | **0** | query returns NotFound; with independently verified provider dedupe → retry the identical id; otherwise → `unknown` + escalate |
  | after broker accepts, before `broker_order_id` persisted | **1** | query by client id finds it; adopt; never a second order |

- Broker timeout → attempt `unknown`, reservation stays held, no blind retry;
  reconciler resolves by query.
- Crash between a cancel attempt and its replacement place attempt → the
  reservation is still `held` and no second close can slip into the gap.
- A Class-C operation has zero grants, reservations and attempts until approved;
  M4 creates its trade grant, open reservation and attempt together after M3A
  has introduced that schema.
- An operation reaching a terminal state releases its remaining resource
  reservation exactly once only after fills are complete and no attempt is
  unresolved, but never deletes its trade grant; a forced safe orphan is swept
  by the reconciler, while an `unknown` attempt remains held.
- Deterministic probe: hold the `(ledger, symbol)` advisory key externally; a
  close MUST block, mirroring AUDIT I4's method for the ledger lock.

---

## Milestone 2.9 (P0) — Orders + fills persistence

**Problem:** M3A's exposure and M3C's PnL both need real fill records. The old
plan deferred `orders`/`fills` writes to M5 while M3's own acceptance test
required inserting fills to trip a breaker — the plan contradicted itself. This
is the persistence half of the old M5; the repricing worker stays in M5B.

**Spec:**
- On placement, insert an `orders` row with a durable primary key and a unique
  one-to-one link to the place attempt:
  ```
  orders (
    id UUID PRIMARY KEY, operation_id UUID NOT NULL REFERENCES operations(id),
    execution_attempt_id UUID UNIQUE NOT NULL REFERENCES execution_attempt(id),
    broker_order_id text UNIQUE, client_order_id text UNIQUE NOT NULL,
    ledger text NOT NULL, symbol text NOT NULL, side text NOT NULL,
    kind text NOT NULL, multiplier bigint NOT NULL,
    qty bigint NOT NULL, limit_micros bigint NOT NULL,
    state text NOT NULL, reprices int NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
    CHECK (multiplier > 0 AND qty > 0 AND limit_micros > 0 AND reprices >= 0)
  )
  ```
  Persist the kernel-derived `kind` and `multiplier` used at approval — M3C
  reads that immutable execution fact rather than current reference data. Risk
  logic reads typed columns, never an order payload blob.
- Order state moves only through `state.Advance`; an illegal transition is a
  bug → log + event, never silent, never a bare overwrite.
- On fill, write a row that later ledgers can reference directly:
  ```
  fills (
    id UUID PRIMARY KEY, order_id UUID NOT NULL REFERENCES orders(id),
    broker_fill_id text UNIQUE NOT NULL, ledger text NOT NULL,
    qty bigint NOT NULL, price_micros bigint NOT NULL,
    fees_micros bigint NOT NULL, ts timestamptz NOT NULL,
    CHECK (qty > 0 AND price_micros > 0 AND fees_micros >= 0)
  )
  ```
  The synthetic shadow fill id in M3A occupies `broker_fill_id`; its prefix
  distinguishes it from provider ids while the same uniqueness rule protects
  retries.
- Fill ingestion is idempotent but not blind: insert on the unique
  `broker_fill_id`; on conflict, load the existing row. Byte-for-byte equivalent
  economic fields are an already-applied no-op, while the same id with different
  order/qty/price/fees is an integrity error that halts reconciliation. Never
  apply exposure, reservation or PnL updates twice.
- The schema serves both ledgers. This milestone writes FakeBroker/live-path
  orders and fills; M3A adds shadow paper orders/fills through the same store
  methods once the paper executor exists. No shadow operation reaches a broker.
- Money columns are micro-dollar `bigint` (invariant 12), never `float`/`real`.
- Writes go through the M2.8 attempt so a crash mid-write is reconcilable.
- A close fill takes the same `(ledger,symbol)` advisory lock used to create the
  reservation and decrements its M2.8 `close_reservation.remaining_qty` in the **same
  transaction** as the fill row. Every fill/release path takes that lock;
  otherwise reservation creation is serialized against proposals but not
  against its own mutators. Partial fills decrement only the filled quantity; a
  cancelled remainder releases only what remains reserved.

**Acceptance:** every FakeBroker/live-path place attempt produces exactly one
`orders` row and every distinct fill produces exactly one `fills` row carrying
stable ids; a forced illegal transition logs + events and does not mutate state;
replaying an identical `broker_fill_id` is a no-op, while reusing that id with
different economics is an integrity failure and neither path double-applies
state; a partial close fill and its reservation decrement are atomic. Shadow
order/fill counts are added to smoke by M3A, when the paper executor starts
producing them.

---


<!-- END FROZEN SPEC -->
