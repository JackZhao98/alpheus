# alpheus — Implementation Plan (work order for a coding agent)

This is a milestone-by-milestone work order against the existing Go repo.
Execute milestones **in order** (later ones depend on earlier ones), one PR
per milestone. Every PR must pass: `gofmt -l .` empty, `go vet ./...`,
`go test ./...`, and `docker compose up --build` still boots cleanly with
`scripts/smoke.sh` passing.

## Context

alpheus is an agentic options-trading system for a small (~$300) Robinhood
account. Two services + postgres:

- `kernel/` — deterministic Go service. Owns broker credentials, hard risk
  limits (`kernel/limits.yaml`), operation approval, order lifecycle,
  persistence. HTTP on :8100. Deps: lib/pq, robfig/cron, yaml.v3.
- `agent-runtime/` — LLM cognition layer. Stateless sessions per role
  (desk_master / scout / position_manager / coach), output schemas enforced
  in `internal/contracts`. Currently runs a rule-based stub cognition.
- `db/init.sql` — schema: events, operations, orders, fills, journal,
  lessons, blackboard. (Becomes migration 0001 in M2.7; see that milestone
  before adding any column.)

Operation approval classes (`kernel/internal/risk`):
- **A** risk-reducing (verified close / cancel / tighten_stop) → execute
  immediately. A `close` is Class A only after the kernel verifies it against
  the live position; M2.8 additionally reserves the quantity durably before any
  asynchronous effect. An unverified close is REJECT, not A.
- **B** opening trade passing the full deterministic checklist → auto-execute.
- **C** checklist failure but no absolute violation → `pending_review`.
- **REJECT** absolute violation → dead. Absolutes include: a halted breaker for
  opens, any `open`+`sell` (single-leg model — see M2.5), unverified close,
  risk-declaration mismatch, and a non-sane quote for any quote-dependent
  broker placement. Cancel/tighten-stop do not invent a quote dependency.

## Invariants — hard constraints on every change

1. Numeric risk rules live ONLY in `kernel/limits.yaml` + `internal/risk`.
   Never in prompts, never in agent-runtime.
2. agent-runtime NEVER talks to the broker or any market/MCP endpoint
   directly. It only calls the kernel HTTP API.
3. Contracts (struct + Validate) are enforced in code regardless of prompts.
4. Shadow operations (`shadow: true`) must NEVER reach the broker.
5. Do not add dependencies beyond the existing three plus, where a milestone
   explicitly says so, `github.com/modelcontextprotocol/go-sdk` and
   `github.com/anthropics/anthropic-sdk-go`. No web frameworks, no ORMs.
6. Do not modify `limits.yaml` VALUES or `roles/*.yaml` prompt slots (prompt
   content is a human task). Adding a new key that a milestone explicitly
   specifies is permitted; changing an existing number is not.
7. All timestamps stored in UTC; market-time logic uses TZ_MARKET.
8. Every JSON write endpoint uses the shared kernel decoder: require
   `application/json`, cap the body at 1 MiB, reject unknown fields for typed
   schemas, and accept exactly one JSON value.
9. Risk gates fail closed on malformed dependency data. In particular, a quote
   is usable only when `ask > bid > 0`, both prices are finite, and it is not
   older than `quote_max_age_sec`; locked, crossed, non-positive, NaN,
   infinite, and stale quotes fail the liquidity check.
10. **Risk facts are computed by the kernel from broker/kernel state, never
    accepted from the proposer.** A payload risk value is a *declaration*: the
    kernel may compare it against its own computed value and REJECT on
    mismatch, but a declaration must never be an input to a gate. Identity is
    the authenticated subject, never a payload field. Kernel-derived fields
    must be structurally unreachable from JSON — put them on the persisted
    struct, never on the request DTO (M2.5). This invariant exists because
    every gate bypass found so far — `side` on close, `max_risk_usd`, `short`,
    `closes_operation_id`, `reviewer` — was the same defect.
11. **No effect without a durable record that precedes it, and no record of an
    effect that did not happen.** Every broker effect is preceded by a committed
    `execution_attempt` carrying **a stable reconciliation key appropriate to
    that effect** — a `client_order_id` we generated for a `place`, the target
    `broker_order_id` for a `cancel`. On any uncertainty (broker timeout, crash
    mid-flight) the system records `unknown` and reconciles by *querying* the
    broker with that key. It never **blindly** re-places to find out. Retrying
    the exact same effect key is permitted only when provider-side deduplication
    has been independently verified; otherwise ambiguity remains `unknown` and
    requires human reconciliation.
    Corollary: **entitlement to act is reserved before acting, not inferred
    afterwards.** Risk, cash and closable quantity are held from the moment a
    gate passes until the effect is conclusively settled or fails; a granted
    daily-trade slot is consumed irreversibly even on failure. Reading any of
    them back only from filled state leaves a window in which the gate is blind
    (M2.8/M3A/M3D).
12. **No float in the money path.** Amounts, prices, risk, exposure and PnL are
    integer micro-dollars, and **quantities are integer micro-units** (M2.5) —
    a float quantity keeps the money path in floating point regardless of the
    price type, because `qty × price` is the money. Every rounding site names
    its direction **in a comment at the site** and rounds against the account's
    interest; a direction that is only implied by the formula has already been
    inverted once in this plan's own history.

---

## Sequence

| # | Milestone | State |
|---|---|---|
| M1 | Class-A fast path (verified close / cancel / tighten_stop) | ✅ landed |
| M2 | Dual ledger + advisory-lock risk gate | ✅ landed |
| M2.4 | Input + market-data boundary hardening (invariants 8, 9) | ✅ landed |
| **M2.5** | **Computed risk in integer micro-dollars + request DTO split** | **next, P0** |
| M2.6 | Run modes, authentication, kill switch | P0 |
| M2.7 | Migrations, DB deadlines, idempotency | P0 |
| M2.8 | Execution attempts, trade grants, close reservation, reconciler | P0 |
| M2.9 | Orders + fills persistence | P0 |
| M3A | Open reservations + exposure ledger + shadow paper book | |
| M8A | Robinhood MCP read-only + capability discovery | |
| M3C | Cost-basis realized PnL, daily stats, breakers | |
| M3D | Provider-confirmed account/settlement model | ⛔ blocked on M8A |
| M4 | Execute approved Class-C (atomic, expiring) | |
| M5B | Safe repricing | |
| M6 | Watchdog → runtime wake channel | |
| M7 | Minimal review console (authenticated) | |
| M9 | Pre-live certification (fault injection) | |
| M10 | cognition/llm.go + context/cost budget | |
| M11 | Robinhood live adapter + canary | last |

**M2.9 precedes M3A**: exposure and partial-fill reservation updates are
written with fills, so durable fill records must exist first. Do not swap them.

M2.5–M2.9 are P0 because M2.5 fixes bugs that are live in shipped code, and
because M3 onward (breaker resume, Class-C approval, real fills) each add a
money-moving surface the current control plane cannot safely carry.

---

## Milestone 1 (P0) — Complete the Class-A fast path ✅ landed

**Problem:** `close` proposals are auto-approved but no order is placed:
kernel only fetches a quote for `action == "open"`, so `close` without an
explicit `limit` resolves to price 0 and skips execution. The one-hop stop
path is the core design promise; finish it.

**Spec:**
- In `kernel/cmd/kernel/main.go` propose():
  - Fetch a quote whenever `action` is `open` OR `close` (symbol fallback:
    `symbol` then `underlying`).
  - For `close` with no explicit limit, default to the **marketable** price
    (closing a long → sell at `bid`; closing a short → buy at `ask`), not mid.
    Rationale: exits prioritize certainty over price.
  - A live `close` becomes Class A only after the kernel reads the matching
    broker position, verifies `qty > 0` and `qty <= abs(position.qty)`, and
    derives the order side and kind from that position. Payload `side` is an
    optional legacy hint and never controls close execution. Serialize live
    broker mutations so concurrent closes cannot both consume one position.
    (M2.8 replaces the process mutex with a durable reservation.)
  - `tighten_stop`: for now record the new stop into the operation payload and
    journal (no broker action until stop orders exist); still Class A.
  - `cancel`: require `broker_order_id` in the payload; call
    `broker.CancelOrder`; record `order_update` event.
- Refactor the execution block of propose() into
  `func (s *server) execute(opID string, op risk.Operation, quote *broker.Quote) (map[string]any, error)`
  — Milestone 4 reuses it.
- Extend `contracts.ProposedOperation` and `risk.Operation` with
  `BrokerOrderID string \`json:"broker_order_id,omitempty"\`` and
  `ClosesOperationID string \`json:"closes_operation_id,omitempty"\``
  (the latter used by M3A).

**Acceptance:**
- smoke path 3 (`close`) returns an `order` object with `state: "filled"`
  against the fake broker.
- Unit test: propose close via httptest against a server wired to FakeBroker;
  assert order placed at bid for a long close.
- Regression tests: no-position, zero/negative qty, over-close, garbage side,
  and concurrent double-close never reach the broker or open reverse exposure.
- New smoke step: cancel of an unknown order returns state `rejected`.

---

## Milestone 2 (P0) — Dual ledger: shadow vs live day-state ✅ landed

**Problem:** shadow operations consume the live `max_new_trades_per_day`
count. After 6 stub shadow trades, everything degrades to Class C. Shadow
must be checked against the SAME checklist (otherwise Phase-3 shadow stats
are inflated by trades live mode would never allow) but must consume a
SEPARATE ledger.

**Spec:**
- `risk.DayState` gains no new fields; instead kernel computes **two**
  day-states (live and shadow) and passes the one matching `op.Shadow` to
  `risk.Classify`.
- Ledger membership: an operation belongs to the shadow ledger iff
  `COALESCE((payload->>'shadow')::bool, false)`; a missing key must fail closed
  into the live ledger. Count within the current `TZ_MARKET` calendar day.
- Count, classify, and insert run in one database transaction protected by a
  transaction-scoped PostgreSQL advisory lock keyed by `(ledger, market_day)`.
  This is correct for M2's per-day counter and with multiple kernel instances;
  M3A deliberately replaces the day-scoped key with a stable per-ledger key
  before adding resources that survive a market-day boundary.
- `GET /state` returns `{"account":…, "positions":…, "day":{"live":…, "shadow":…}}`.
  Update `scripts/smoke.sh` and `agent-runtime/internal/assemble` doc comment
  accordingly (assemble passes raw JSON through; no code change needed there
  beyond none).
- db: no schema change required (shadow lives in payload). Add an expression
  index in `db/init.sql`:
  `CREATE INDEX ops_day_ledger ON operations (ts,
  (COALESCE((payload->>'shadow')::bool, false)));`

**Acceptance:**
- Unit/integration test: submit 6 compliant shadow opens (all B), then one
  compliant LIVE open → still Class B; a 7th shadow open → Class C with
  `daily_trade_count` failed.
- Barrier regression: with either ledger at 5/6, release 20 same-process
  goroutines simultaneously; exactly one is B, 19 are C, and the ledger ends
  at 6. Process-per-request tools are not a valid concurrency test here.
- Deterministic gate probe: hold the `(ledger, market_day)` advisory key in an
  external session; a live propose MUST block until it is released. This, not
  the barrier, is the reliable regression test — see AUDIT.md I4.
- smoke.sh prints both ledgers from /state.

---

## Milestone 2.4 — Input and market-data boundary hardening ✅ landed

Recorded for provenance. Landed as invariants 8 and 9 plus a single
`decodeJSONBody` boundary used by every JSON write endpoint, a `YYYY-MM-DD`
check on `/blackboard/{day}`, verdict validation on review, and quote sanity
in `broker.Quote`. Client input errors return 4xx with kernel-authored
messages; raw driver errors are never echoed.

---

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

## Milestone 3A — Open reservations, exposure ledger, shadow paper book

**Problem:** the previous M3 summed `payload->>'max_risk_usd'` for open risk
and released the whole open as soon as *any* operation carrying
`closes_operation_id` reached `auto_approved`. Every part of that is unsafe:

- `closes_operation_id` is proposer-supplied and unvalidated. Verified: a
  `tighten_stop` carrying `"deadbeef-not-even-a-real-op"` is accepted and
  stored. It can reference another symbol, another ledger, or nothing at all.
- A *partial* close would release the *entire* open's risk.
- A close that is merely `auto_approved` — not filled — would release risk that
  still exists.
- The SQL dropped M2's `COALESCE(...,false)`, so a payload without `shadow`
  belongs to neither ledger — failing open.
- And it summed a declared number, which M2.5 established is fiction.

There is a second gap between authorization and fill. Exposure is correctly
written only on fill, but that means a resting open order is absent from
`open_risk`, settled-cash usage and the daily counter for its entire resting
life. Serializing proposals with the M2 ledger lock does not help when the
state being read changes only after a later fill. M2.9 lands durable fills
first; this milestone can therefore reserve the entitlement before execution
and convert it without a torn handoff.

**Spec:**
- **Upgrade the gate lock before adding cross-day resources.** M2's advisory key
  is `(ledger,market_day)`, which is sufficient for a per-day counter but unsafe
  for open risk and cash that survive midnight: requests on opposite sides of
  the boundary can hold different locks, read the same global resource and both
  pass. At M3A activation replace it everywhere with one stable advisory key per
  ledger (`live` and `shadow` remain different keys). `market_day` remains a
  column/query dimension, never part of the mutex key. After acquiring the
  ledger lock, fetch the bounded-timeout account/position snapshot, then derive
  `market_day` from database time immediately before count/classify/reserve; a
  slow account call that crosses midnight must not stamp the old day.
  Fetching account state before waiting for the lock can apply stale pre-fill
  equity/buying power after a prior gate completes. The deliberate tradeoff is a
  network call while the per-ledger transaction lock is held; it serializes only
  opens for that ledger, and a timeout rolls back with no attempt. Verified
  Class-A exits do not take this lock and remain available. A quote may be
  prefetched, but its `as_of` sanity is rechecked after the lock. All proposal,
  M4 approval and reconciler re-gate paths use the same stable key; lock order
  remains idempotency → ledger → symbol.
- **Open reservation.** New table:
  ```
  open_reservation (
    id UUID PRIMARY KEY,
    operation_id UUID UNIQUE NOT NULL REFERENCES operations(id),
    ledger text NOT NULL,
    market_day date NOT NULL, symbol text NOT NULL, kind text NOT NULL,
    original_qty bigint NOT NULL, remaining_qty bigint NOT NULL,
    original_risk_micros bigint NOT NULL,
    remaining_risk_micros bigint NOT NULL,
    original_cash_micros bigint NOT NULL,
    remaining_cash_micros bigint NOT NULL,
    resource_state text NOT NULL,       -- held | converted | released
    created_at timestamptz NOT NULL, settled_at timestamptz,
    CHECK (original_qty > 0 AND remaining_qty >= 0
           AND remaining_qty <= original_qty),
    CHECK (original_risk_micros >= 0 AND remaining_risk_micros >= 0
           AND remaining_risk_micros <= original_risk_micros),
    CHECK (original_cash_micros >= 0 AND remaining_cash_micros >= 0
           AND remaining_cash_micros <= original_cash_micros),
    CHECK (resource_state IN ('held','converted','released')),
    CHECK (resource_state <> 'held' OR remaining_qty > 0),
    CHECK (resource_state <> 'converted' OR
           (remaining_qty=0 AND remaining_risk_micros=0
                            AND remaining_cash_micros=0)),
    CHECK (resource_state <> 'released' OR
           (remaining_risk_micros=0 AND remaining_cash_micros=0))
  )
  ```
  `execution_attempt` gains nullable `open_reservation_id REFERENCES
  open_reservation(id)`; its existing close FK is named
  `close_reservation_id`. A live Class-B open writes operation + M2.8 trade
  grant + open reservation + execution attempt in the **same** stable-ledger
  gate transaction. A shadow Class-B open writes the
  same four records, but its attempt is `paper_place`. REJECT and
  `pending_review` write none. M4 does the same for an approved Class C. An
  open attempt without both its grant and reservation is invalid.
  The M3A migration also extends the attempt intent constraint with
  `paper_place`: it carries a deterministic `client_order_id` prefixed
  `shadow:` as its internal idempotency key and no `target_broker_order_id`.
  The replacement CHECK is the M2.8 `place` and `cancel` alternatives plus
  `(intent='paper_place' AND client_order_id IS NOT NULL AND
  target_broker_order_id IS NULL AND qty > 0 AND limit_micros > 0)`; dropping
  the old constraint before adding this one is part of the migration, not an
  application-only convention.
- **Partial fills transfer only the filled slice.** On each open fill of `q`,
  the same transaction inserts the fill, creates exactly one exposure lot for
  `q` at that fill's actual cost, then sets:
  ```
  remaining_qty = original_qty - cumulative_filled_qty
  remaining_risk_micros = ceil(original_risk_micros * remaining_qty / original_qty)
  remaining_cash_micros = ceil(original_cash_micros * remaining_qty / original_qty)
  ```
  Compute in `big.Int`. The fill price must remain within the approved limit;
  actual exposure-lot risk plus the still-held reservation must never fall below
  the exact live obligation. Independent ceiling operations can leave a
  bounded micro-unit surplus; keep that surplus until the order becomes
  terminal rather than rounding down to force equality. At `remaining_qty=0`,
  zero both remaining amounts and mark `converted`. On cancel/failure after a
  partial fill, preserve `remaining_qty` as the audit record of the unfilled
  quantity, zero only `remaining_risk_micros` and `remaining_cash_micros`, and
  mark the row `released`; already-filled exposure lots remain. This avoids both
  failure modes: converting the whole row drops unfilled risk, while leaving
  the whole row held double-counts the filled slice.
- **Trade slots remain independent of resource release.** Risk and cash use
  only `remaining_*` while `resource_state='held'`; the daily counter continues
  to count immutable M2.8 `trade_grant` rows. A failed attempt may release its
  remaining risk/cash reservation but cannot return its grant.
- **Re-gating an existing entitlement is not a second proposal.** Recovery of
  a stale pending open reuses its existing reservation and attempt. Build the
  revalidation day-state with that reservation's remaining risk/cash **and its
  existing trade grant** removed, then apply the operation once; do not allocate
  another grant or reservation. Otherwise recovery counts the operation's own
  entitlement twice. For a close, validate that the current position still
  covers the existing close reservation's remaining quantity; do not reserve it
  again.
- On a **conclusively terminal** failure/cancel, after every broker-reported fill
  is durable, set remaining risk/cash to zero and `resource_state='released'` in
  one transaction while preserving the unfilled `remaining_qty` for audit; keep
  both reservation and separate trade-grant rows. A timeout/`unknown` attempt
  retains the reservation. Extend M2.8's orphan sweeper to repair terminal
  operations whose open resources remain held only when broker cumulative fill
  quantity equals durable fills and no attempt is unresolved; never delete the
  trade grant and never fail open on missing proof.
- **Activation/backfill is part of the migration.** Before the kernel accepts a
  proposal after upgrading, backfill one exposure lot per M2.9 live/FakeBroker
  open fill and its deterministic close allocations from subsequent close fills,
  and create held open reservations for every non-terminal live open order's
  remaining quantity. Run the idempotent backfill in one transaction under an
  exclusive startup advisory lock and commit a durable
  `feature_activation(name PRIMARY KEY, activated_at, cutoff)` marker with it;
  the HTTP server does not start unless the M3A marker exists. A crash before
  commit retries the whole backfill, while a committed marker prevents a second
  rewrite. If a current broker position cannot be reconstructed from durable M2.9 fills,
  startup fails closed with an explicit migration blocker; do not invent cost
  basis from `AvgPrice`. Before M11 there is no real-money adapter, so the sim
  environment may be explicitly flattened/reset and the event recorded.
  Shadow statistics start at the marker's explicit M3A activation timestamp;
  older shadow classifications were journal-only and are not retroactively invented
  as fills.
- **One exposure lot per open fill, not per operation.** One order may fill at
  several prices, and an early fill may be closed before a later fill arrives.
  Aggregating those fills into an operation-level average makes FIFO cost basis
  unrecoverable. New table (migration):
  ```
  exposure_lot (
    open_fill_id UUID PRIMARY KEY REFERENCES fills(id),
    operation_id UUID NOT NULL REFERENCES operations(id),
    ledger text NOT NULL, symbol text NOT NULL, kind text NOT NULL,
    multiplier bigint NOT NULL,
    opened_qty bigint NOT NULL, closed_qty bigint NOT NULL DEFAULT 0,
    entry_cost_micros bigint NOT NULL,
    remaining_cost_basis_micros bigint NOT NULL,
    remaining_risk_micros bigint NOT NULL,
    opened_at timestamptz NOT NULL, closed_at timestamptz,
    CHECK (multiplier > 0 AND opened_qty > 0
           AND closed_qty >= 0 AND closed_qty <= opened_qty),
    CHECK (entry_cost_micros > 0
           AND remaining_cost_basis_micros >= 0
           AND remaining_cost_basis_micros <= entry_cost_micros
           AND remaining_risk_micros >= 0
           AND remaining_risk_micros <= entry_cost_micros)
  )
  ```
  Index open lots by `(ledger,symbol,kind,opened_at,open_fill_id)` where
  `closed_qty < opened_qty`; the fill id is the deterministic FIFO tie-breaker.
- **Exposure is written and updated in the SAME transaction as the fill that
  causes it** (M2.9). A fill without its exposure-lot update, or vice versa, is a
  torn write on the risk path. Never write exposure on approval. An open fill's
  `entry_cost_micros` is its actual fill cost at the persisted order multiplier
  plus fees, never a declaration or operation-level average. Both remaining
  fields start at that exact cost. The open reservation carries M2.5's approved
  `required_cash` upper bound until the corresponding quantity fills.
- A close fill may span more than one FIFO open lot, so persist the allocation
  rather than relying on a later reconstruction:
  ```
  exposure_close_allocation (
    close_fill_id UUID NOT NULL REFERENCES fills(id),
    open_fill_id UUID NOT NULL REFERENCES exposure_lot(open_fill_id),
    qty bigint NOT NULL, matched_cost_micros bigint NOT NULL,
    released_risk_micros bigint NOT NULL,
    PRIMARY KEY (close_fill_id, open_fill_id),
    CHECK (qty > 0 AND matched_cost_micros >= 0
                   AND released_risk_micros >= 0)
  )
  ```
  Under the `(ledger,symbol)` lock, allocate the close fill across eligible
  exposure lots in FIFO order. Insert every allocation and update every touched
  lot in the same transaction as the fill and close-reservation
  decrement. Allocation quantities must be positive and sum exactly to the fill
  quantity. `released_risk_micros` is the conservatively rounded difference
  between the lot's old and new risk remainder; `matched_cost_micros` is the
  separately conservative cost-basis allocation defined below. The primary key
  makes replay of the same fill idempotent.
- Open risk per ledger is **settled exposure PLUS still-held reservations**
  — filled risk alone is blind for the whole life of a resting order:
  ```sql
  SELECT COALESCE((SELECT SUM(remaining_risk_micros) FROM exposure_lot
                   WHERE ledger=$1 AND closed_qty < opened_qty), 0)
       + COALESCE((SELECT SUM(remaining_risk_micros) FROM open_reservation
                   WHERE ledger=$1 AND resource_state='held'), 0)
  ```
  A partial fill transfers exactly one slice in the same transaction, so the
  two terms neither double-count nor leave a gap. Read both under the
  stable per-ledger lock.
  No payload arithmetic anywhere in the risk path.
- Buying power uses the same entitlement-before-effect rule for every account
  type: under the stable ledger lock require
  `required_cash <= AccountState.BuyingPower -
  SUM(remaining_cash_micros of held open_reservation rows)`. This is the
  reservation-aware form of M2.5's absolute; failing it is still REJECT, not C.
  On a live/Fake fill the broker's buying-power debit must become visible before
  the database reduces the reservation, so any intermediate view is fail-closed.
  Shadow updates paper buying power and the reservation in one transaction.
- `closes_operation_id` rules:
  - Meaningful only on `close`; present on any other action → 400.
  - The kernel validates that the referenced operation exists, is an `open`, is
    in the same ledger, same symbol, same kind, and has remaining quantity.
    Otherwise 400.
  - If omitted, the kernel matches lots **FIFO** itself. The proposer does not
    choose which lot is closed; if it names one, the name must agree with the
    kernel's first eligible FIFO lot. A fill larger than that lot continues
    across later FIFO lots and `exposure_close_allocation`, not this singular
    hint, is the authoritative mapping. (Old plan text: "position_manager is
    responsible for setting closes_operation_id; kernel does not infer it" —
    exactly backwards under invariant 10.)
  - M2.8's reservation still binds the total close quantity.
- Once the exposure ledger is active, closable quantity is the conservative
  `max(0, min(abs(broker_or_paper_position), sum(remaining matching exposure
  lots)) - held_close_reservations)`. A broker fill not yet ingested can increase
  the position but cannot create a closeable kernel lot; an unexplained
  broker/kernel mismatch emits a reconciliation event and makes only the
  unexplained excess unavailable. Activation's backfill requirement is what
  prevents legitimate legacy positions from becoming permanently uncloseable
  here.
- Release is proportional and lags reality: on a close **fill** allocation of
  `q` against one exposure lot, `closed_qty += q` and
  ```
  remaining_qty = opened_qty - closed_qty
  remaining_risk_micros = ceil(entry_cost_micros * remaining_qty / opened_qty) // 0 when remaining_qty == 0
  remaining_cost_basis_micros = floor(entry_cost_micros * remaining_qty / opened_qty)
  matched_cost_micros = old_remaining_cost_basis_micros - remaining_cost_basis_micros
  ```
  **Round `remaining_risk_micros` UP** so the *release* rounds down (invariant
  12). Flooring the remainder — the obvious-looking form — releases more risk
  than the close actually earned and is a fail-open on the risk cap. The
  released amount is never computed directly; it is only ever the residue of a
  conservatively-rounded remainder. Cost basis has the opposite purpose:
  rounding its remainder **down** makes `matched_cost_micros` round up, so an
  interim realized-PnL calculation cannot flatter the account. Both residual
  formulas conserve the original integer total when the lot is fully closed.
- **Shadow paper book.** The shadow ledger gets kernel-owned
  `shadow_positions` plus `orders`/`fills` rows marked `ledger='shadow'`,
  filled at the sanity-checked quote's marketable price at the moment a shadow
  op would have executed. Without this, shadow open-risk and shadow PnL are
  meaningless and every Phase-3 statistic is fiction. Shadow still never touches
  the broker (invariant 4) — this is a simulated book inside the kernel. A
  shadow open uses an `execution_attempt` with intent `paper_place` and a
  deterministic synthetic effect id. Claiming it runs one idempotent database
  transaction that inserts the synthetic order/fill, updates
  `shadow_positions`, transfers reservation to an exposure lot and settles the
  attempt. Unique ids derived from the attempt make retry safe. There is no
  network call and no intermediate committed paper state.
  Shadow closes stop using M1's temporary unconditional
  `VerifiedReduction=true`: verify and derive them from `shadow_positions`,
  reserve quantity with the same `(ledger,symbol)` rule, and in the paper
  transaction update the position, exposure lot and close reservation from the
  synthetic close fill. A missing/over-sized shadow close is REJECT and can
  never create reverse paper exposure.
- `day_open (market_day date, ledger text, equity_micros bigint,
  PRIMARY KEY(market_day, ledger))`, written once per market day per ledger on
  the first dayState of that day. Live uses broker equity (correct as of M2.5);
  shadow uses the shadow paper account's equity.

**Acceptance:** integration tests against a real postgres (`-tags integration`,
skipped without DATABASE_URL):
- Freeze a fake clock around market midnight and barrier two opens assigned to
  opposite market days with only one global-risk slot left: the stable ledger
  lock admits at most one. Both rows use the market day computed after lock
  acquisition; live and shadow still proceed independently.
- Block `GetAccount` for one ledger until its timeout: no open attempt is
  committed from a stale snapshot, while a verified close with a sane quote on
  another symbol still follows the Class-A path.
- **Open side, sequential rather than concurrent.** Suppress fills; with a
  $150 total-risk cap, submit one $100 open and then another. The second is C on
  `total_open_risk`, and remains C for as long as the first order rests.
- Repeat with buying power as the binding resource: the first resting order's
  `remaining_cash_micros` makes a second otherwise-affordable order REJECT
  `insufficient_buying_power`; a partial fill never creates a free or
  double-subtracted window.
- Partially fill 1 of 3 units: one exposure lot contains only the filled unit and
  reservation contains exactly the remaining 2; their sum never falls below
  the still-live risk, and any ceiling surplus is bounded and retained rather
  than released early. Confirm cancellation and complete fill reconciliation;
  then assert only the remaining reservation releases.
- A released open reservation leaves its M2.8 trade grant counted; resource
  release never restores a daily slot.
- Force a placement timeout: its open/close reservation remains held and blocks
  reuse until reconciliation proves the broker terminal state and ingests every
  fill; manually marking the operation failed cannot free the resource early.
- Open two FIFO lots, then fill one close across both: allocation quantities sum
  exactly to the close fill, each touched exposure lot releases only its own
  proportional risk, replaying the same fill creates no second allocation, and
  a proposer naming the second lot while the first is still eligible gets 400.
- Make broker position and exposure-lot quantity disagree in either direction:
  a new close cannot exceed the smaller reconciled quantity, emits a mismatch
  event, and never opens reverse exposure.
- A shadow open produces operation + reservation + attempt + synthetic order +
  fill + exposure lot + shadow position with one atomic paper transaction and zero
  broker calls. Kill/retry at every transaction boundary: no duplicate fill and
  no state where the reservation and exposure lot both omit or both own a slice.
- Upgrade fixture: historical M2.9 fills backfill exposure lots and a resting open
  order receives a held reservation before proposals are accepted. Kill the
  process mid-backfill: restart either retries from zero or observes the committed
  activation marker, never a half-active ledger.
- Open 3 lots → `remaining_risk_micros == entry_cost_micros`; close 1 of 3 →
  `remaining_risk_micros` is **≥** 2/3 of entry cost and never below it, not 0;
  a lot whose entry cost does not divide evenly by `opened_qty` (e.g. entry cost
  100, opened_qty 3) → the sum of released risk across closing every lot one at a
  time never exceeds `entry_cost_micros`, and each partial release is rounded against
  the account; a close that rests without filling → `remaining_risk_micros`
  unchanged; killing the kernel between the fill write and the exposure write
  is impossible to observe (same transaction); `closes_operation_id` naming
  another symbol / another ledger / a non-open / a nonexistent id → 400; on
  `tighten_stop` → 400; six shadow opens → shadow open risk reflects shadow
  paper fills while live open risk stays 0.

---

## Milestone 8A — Marketdata facade + Robinhood MCP (read-only) + capability discovery

**Context — verify, do not assume.** As of 2026-07-16, Robinhood's official
[Agentic Trading page](https://robinhood.com/us/en/agentic-trading/) says MCP is
available now, uses a separately funded dedicated **Robinhood Agentic Account**,
and may place trades. Robinhood's
[2026-07-01 announcement](https://robinhood.com/us/en/newsroom/robinhood-accelerates-global-expansion-robinhood-chain-mainnet-stock-tokens-agentic-trading/)
says Agentic Trading launched for US equities and options. Those public pages
do **not** establish this account's approval level, cash-vs-margin type,
settlement semantics, exact tool schema, client-id behavior, fill identity, or
which account data the authenticated MCP session exposes. The old M3b's
"Context (confirmed): the live account is a Robinhood CASH account, options
Level 2" was confirmed about the user's *main* account, which is not the
account an agent can trade. Nothing downstream may assume it carries over.

**Spec:**
- New package `kernel/internal/marketdata`:
  ```go
  type Provider interface {
      Quote(symbol string) (broker.Quote, error)
      Instrument(symbol string) (InstrumentSpec, error) // multiplier, qty increment, tick rules
      Chain(underlying, expiry string, window PercentMicros) ([]OptionQuote, error)
      Expirations(underlying string) ([]string, error)
      Bars(symbol string, days int) ([]Bar, error)      // daily OHLCV, days<=30
      Movers(direction string, n int) ([]Mover, error)  // n<=10
      Hours() (MarketHours, error)
  }
  ```
  Server-side caps are part of the contract (window<=15 percentage points,
  days<=30, n<=10): clamp, don't error. `PercentMicros` is M2.5 fixed-point;
  this interface introduces no new float boundary.
- `fake` provider: backed by the FakeBroker quote map + synthesized chains
  (strikes every $1 within window, OI 1000, spread 5% of mid) and flat bars.
- `robinhoodmcp` provider — **read-only**. Uses
  `github.com/modelcontextprotocol/go-sdk`. EXACTLY ONE call site wraps
  `CallTool` with: 10s timeout, TTL cache (quotes 2s, chains 30s, bars/hours
  10min), a token-bucket rate limiter (default 30 calls/min, env-tunable), and
  one retry on transport error. Wrap only the tools the interface needs;
  normalize to the structs above and DISCARD all other fields. Env:
  `MARKETDATA=fake|robinhood_mcp`, `RH_MCP_URL`, `RH_MCP_TOKEN` — kernel env
  only. Persist the OAuth/session in a kernel-only secret volume/file (0600),
  never in the source tree, database payloads, API responses or logs, so a
  restart does not require a human without widening credential exposure.
- **Capability discovery is a deliverable, not a runtime file write.** An
  explicit authenticated discovery command lists tools and updates
  `docs/rh_mcp_capabilities.json`; production never mutates the source tree.
  Offline CI tests the adapter against the committed snapshot. Live/read-only
  startup lists tools and compares them with the snapshot, failing closed on a
  renamed, withdrawn or incompatible tool. A normal CI job without OAuth cannot
  detect live drift; an optional secret-backed scheduled compatibility job may
  do so, but the startup gate is mandatory.
- Record in that snapshot, as facts rather than assumptions: the Agentic
  account's `account_type` (cash|margin), its settlement behaviour, its options
  level, supported asset/order types, quantity increments, price-tick rules,
  option contract multiplier/deliverable metadata, whether realized-PnL and
  option order tools exist, whether executions expose a stable unique fill id
  plus cumulative filled quantity, whether account/order data can normalize
  buying power and settled cash gross of Alpheus's own provider-side order
  holds without including external holds, and **whether the
  order API (a) accepts a client-supplied id, (b) can be queried by it, and
  (c) deduplicates on it.** These are M2.8's live-trading preconditions and they
  are three separate questions: (a)+(b) make a crash diagnosable; only (c) makes
  recovery from `NotFound` safe, because without it "the broker has never heard
  of this id" is indistinguishable from "it is still in flight".
  **M8A can only answer these from schema and documentation — it is read-only
  and cannot place the duplicate order that would actually prove (c).** Record
  the documented answer and mark it `unverified`. Automatic re-place stays OFF
  unless (c) is demonstrated in a provider-supported sandbox/test environment;
  a documented guarantee is not a tested one, and the failure mode of trusting
  it is a duplicate live order. Until then, ambiguous attempts escalate to a
  human.
  **M3D stays blocked until these are recorded.**
- `LIVE_ACCOUNT_ID` allowlist (M2.6) applies to reads too: refuse to read any
  account not on the allowlist, even if the current or a future MCP session
  happens to expose it.
- Quotes carry `source` and `as_of`. A quote older than `quote_max_age_sec`
  fails invariant 9 exactly like a crossed one — a stale quote is a non-sane
  quote.
- Kernel endpoints (agents' only doorway):
  `GET /market/quote/{symbol}`, `GET /market/chain/{underlying}?expiry=&window_pct=`,
  `GET /market/expirations/{underlying}`, `GET /market/bars/{symbol}?days=`,
  `GET /market/movers?dir=&n=`, `GET /market/hours`.
- assemble: inject quotes for all held positions into context under `"quotes"`.
  Exploratory calls stay tool-side for Phase 2 (do NOT build runtime tools now).
- **Single source of truth:** risk and execution MUST read quotes from the same
  provider instance, and FakeBroker must consume the fake provider's quote map.
  Two quote sources means the gate and the order disagree. Document in code.

**Acceptance:** unit tests for clamping + cache TTL (fake clock or short TTLs);
with `MARKETDATA=fake`, `/market/chain` returns a filtered window; smoke
extended with `/market/quote/SPY`; capability snapshot committed, offline
fixture drift breaks CI, and live startup rejects a renamed/removed tool; a
fixture instrument returns exact multiplier/quantity increment/tick metadata,
and missing required instrument metadata fails closed; a stale quote fails the liquidity check
closed; a read for a non-allowlisted account id is refused. `robinhoodmcp` is
exercised behind an env-gated integration test that skips without `RH_MCP_URL`;
restart reuses the protected session and a log/API scan contains no token.

---

## Milestone 3C — Cost-basis realized PnL, daily stats, breakers

**Problem:** the old spec computed "sum of (sell fills) − (buy fills)" for the
day. That is **cash flow, not PnL**. A position bought today and not yet sold
reads as a total loss; a position bought yesterday and sold today reads as pure
profit. With `max_daily_loss_pct: 40`, the first case halts the ledger on the
first trade of almost every day, and the second hides real losses.

**Spec:**
- Realized PnL is **cost-basis matched**, FIFO per `(ledger, symbol, kind)`,
  computed from M2.9 fills and M3A `exposure_close_allocation` rows. M3A already
  made the FIFO decision at close-fill time; M3C consumes that durable mapping
  and must not re-run a possibly different match later. For each close fill:
  `exit_proceeds_micros = floor(qty_micro * price_micros * persisted_multiplier
  / 1_000_000)`, then
  `realized_pnl = exit_proceeds_micros - fees_micros -
  SUM(matched_cost_micros)`. Proceeds round down and matched cost rounds up, both
  against the account; compute products in `big.Int`. A position opened and not
  closed contributes **0**, not a loss.
- Live reconciliation: local FIFO is always computed. If M8A's snapshot shows
  Robinhood exposes a realized-PnL tool, persist both numbers and use the
  **lower (more loss-making)** value for the breaker; a delayed provider value
  must not mask a locally durable loss, and external/provider-only loss must not
  be ignored either. If their absolute difference exceeds a new exact-decimal
  `limits.yaml` key `pnl_reconciliation_tolerance_usd` (adding the key is allowed
  by invariant 6; decode through M2.5), emit `pnl_divergence` and halt new opens
  for that ledger pending admin reconciliation. Verified closes remain enabled.
  If the tool does not exist, local FIFO is authoritative and that is recorded
  as a known gap in README's "刻意没做的事".
- Shadow PnL comes from the shadow paper fills (M3A). The old "return 0 for
  shadow, documented" is obsolete.
- Breakers: `breaker_state (ledger text PRIMARY KEY, halted bool NOT NULL
  DEFAULT false, reason text, updated_at timestamptz DEFAULT now())`.
  - Evaluate in `dayState()`: compute
    `daily_loss_limit_micros = floor(day_open.equity_micros *
    max_daily_loss_percent_micros / (100 * 1_000_000))` in `big.Int`. If
    `realized_pnl_today <= -daily_loss_limit_micros` → halt that ledger with
    reason `daily_loss`. The allowed loss rounds down, so the breaker never
    trips late. Day-scoped: auto-clears when `day_open.market_day` advances.
  - `consecutive_loss_days_halt`: count the most recent N market days with
    negative realized PnL for that ledger; at or past the limit → halt with
    reason `loss_streak`.
- **Resume must suppress, not just clear.** A naive `POST /breaker/resume` sets
  `halted=false`, and the very next `dayState()` re-evaluates the same
  unchanged PnL and re-halts instantly — the resume button would do nothing.
  Add:
  ```
  breaker_override (ledger text, reason text, market_day date, subject text,
                    ts timestamptz, PRIMARY KEY (ledger, reason, market_day))
  ```
  `dayState()` skips evaluating `reason` for `ledger` while an override exists
  for the current market day. Overrides are **always day-scoped and never
  inherited**: a `loss_streak` override must be re-issued each market day until
  the ledger actually has a non-negative day. That is the intended friction —
  `consecutive_loss_days_halt` is documented as the one gate that requires a
  human, so requiring a human *each day* is the honest reading.
- `POST /breaker/resume {"ledger":"live","reason":"daily_loss"}` — admin auth
  (M2.6), writes the override, emits an event carrying the subject. Every
  breaker transition emits a `breaker` event.
- `risk.DayState.Halted/HaltReason` populated from breaker_state + evaluation.
- A verified Class-A close stays executable while halted (AUDIT I6). Do not put
  dayState on the close path.

**Acceptance:** FIFO unit tests — buy today, no sell → realized **0**, ledger
NOT halted (this is the bug the old formula would have shipped); buy yesterday,
sell today → realized = exit − entry, not exit; partial close realizes
proportionally; fees included; option multiplier applied. Integration: seed
  losing fills → live halted, live open → REJECT `breaker halted: daily_loss`,
  verified close → still Class A and executes, shadow classifies normally;
  make provider PnL lag above local loss and then report an unexplained lower
  value: the breaker uses the lower value in both cases, divergence halts opens,
  and a verified close still executes;
`POST /breaker/resume` with admin token clears it **and the next `/state` does
not re-halt**; the following market day the override is gone; resume without a
token → 401.

---

## Milestone 3D ⛔ BLOCKED — Provider-confirmed account/settlement model

**Do not implement until M8A records the Agentic account's real type,
settlement behaviour, and options level.** The old M3b was written against
"Context (confirmed): the live account is a Robinhood CASH account, options
Level 2" — a fact confirmed for the user's main account, not for the Agentic
account that is the only account an agent may trade. Robinhood's public
documentation does not state the Agentic account's type. If M8A finds it is a
margin account, or that settlement differs, this milestone's premise is void
and it must be rewritten rather than adapted.

**When unblocked**, the shape is the old M3b with two corrections:
- The `settled_funds` checklist item compares `required_cash` (M2.5), **not**
  the declared `max_risk_usd`.
- FakeBroker's settlement model mirrors what M8A actually found — not what the
  main account does.

Sketch (subject to M8A):
- `broker.AccountState.SettledCash` becomes real: buys consume settled cash
  only (reject `insufficient settled funds`); sell proceeds land in an
  unsettled bucket and settle at the next market-day rollover. Sim control
  `POST /sim/advance_day` (fake broker only, admin auth, sim mode only).
- `risk.DayState` gains `SettledCash Micros` and `AccountType string`.
- `risk.Classify`: for `open` on a cash account (live ledger), checklist item
  `settled_funds`: `required_cash <= day.SettledCash −
  Σ(remaining_cash_micros of open_reservation WHERE
  resource_state='held' for that ledger)`. **Cash has the same stale-read hole
  as open risk**: broker settled cash does not drop until the buy settles, so
  without subtracting held reservations several pending buys each see the
  whole balance and all pass. M3A already uses the cash upper bound to reserve
  generic buying power; this milestone additionally applies the same held amount
  to settled cash once the account type is known.
  Shadow uses its own paper settled cash (M3A gave it a real book).
  `SettledCash` follows the same normalized-gross contract as M2.5
  `BuyingPower`: do not subtract a held reservation from a provider number that
  already includes that exact order hold. M8A must provide the fields needed to
  add back only Alpheus-owned holds or this model cannot be enabled live.
  Insufficient settled cash is an absolute REJECT
  `insufficient_settled_funds`, not Class C: human approval cannot settle funds
  or make a broker accept the order.
- Evaluate settled cash under M3A's stable per-ledger gate. For a live fill the
  broker account debit is already authoritative before the fill event reaches
  the database; FakeBroker must likewise debit cash **before** committing the
  fill/reservation transfer, so the only observable intermediate state is more
  conservative (new cash minus old reservation), never old cash minus a released
  reservation. Shadow cash, fill and reservation move in one paper transaction.
- Note for humans: if the account is cash with a small balance, the practical
  playbook is long calls/puts only. Robinhood cash accounts also do not support
  option ROLLING; alpheus only ever issues sequential ops. Never build an
  atomic roll.

**Acceptance (when unblocked):** suppress the first buy's fill so it rests while
reserving all settled cash; a second buy is immediately REJECT
`insufficient_settled_funds`. Partially fill the first: broker settled cash decreases
for the filled slice while `remaining_cash_micros` decreases by the same slice,
with no double subtraction or free window. After sale proceeds settle via
`POST /sim/advance_day`, the same affordable proposal is B. FakeBroker rejects
a direct over-settled-cash order with `insufficient settled funds`.

---

## Milestone 4 — Execute approved Class-C operations (atomic, expiring)

**Problem with the obvious implementation:** CAS the status to `approved`, then
re-check TTL / quote / risk, and 409 if a check fails. That leaves the
operation stuck in `approved` after a failed check — a state that says
"execute me" about a proposal that just failed the gate, and the
`pending_review` state is already consumed so it can never be reviewed again.
Approval re-verification, the status change, and attempt creation must be one
atomic decision.

**Spec:** the review handler, admin-authenticated (M2.6), on `approved`:
- **Before the transaction:** prefetch a fresh quote if useful. Quote freshness
  is enforced by `quote_max_age_sec` (invariant 9) again after lock acquisition.
  Do not fetch the account yet: M3A requires the account/position snapshot after
  the stable ledger lock so approval cannot apply stale pre-fill buying power.
- **In ONE transaction:**
  1. `SELECT … FROM operations WHERE id=$1 AND status='pending_review' FOR
     UPDATE` — 0 rows → 409 (covers both "not pending" and concurrent approve).
  2. TTL: `ts + proposal_ttl_sec < now()` → set status `expired`, commit, 409
     `proposal_expired`. Terminal — it can never pass again, so unlike the
     other failures it does change state.
  3. Take M3A's stable per-ledger gate lock, fetch the bounded-timeout account
     snapshot, and, for a close, take the `(ledger, symbol)` lock (M2.8), in that
     order. Account failure rolls the transaction back; no attempt exists.
  4. Re-run the FULL gate with current day-state and current computed risk
     (M2.5). If it now REJECTs — breaker, uncovered short, declaration
     mismatch, crossed/stale quote — **ROLL BACK** and 409 with the reason. The
     operation stays `pending_review`: the breaker may clear, and the TTL will
     eventually retire it. A human may not override a non-sane or stale quote;
     that is not a judgement call, it is bad data.
     The approval remains bound to the proposal's persisted quantity,
     kernel-derived instrument spec and `approved_price_cap`. The fresh quote
     may lower `working_price` but may never raise the cap or the reservation;
     if the current ask is above the cap, the order may rest at the cap. Any
     change to quantity/spec/cap requires a new proposal. Persist the approval
     snapshot in the event so the reviewer can see exactly what became entitled.
  5. Otherwise set status `approved` **and insert an M2.8 `trade_grant`, an M3A
     `open_reservation(resource_state='held')`, plus an M2.8
     `execution_attempt(seq=1, pending)`** in the same transaction, then commit.
     An attempt without its reservation is an execution entitlement that
     no counter can see: the approved trade would consume risk, cash and a
     daily slot that the gate never charged it for. The reservation's
     `market_day` is the day of **approval**, not of the original proposal.
- Execute the attempt **after** commit, via the M2.8 claim path.
- An approval must never bypass an absolute.

**Acceptance:** over-budget open → pending_review → approve → exactly one open
reservation and one attempt, then order placement; breaker halted → 409 **and
status is still `pending_review`, not
`approved`**; TTL-expired → 409 and status `expired`; 20 concurrent approvals
of one operation under the barrier harness → exactly one execution and exactly
one attempt row; approve without admin token → 401; approve while the quote is
crossed → 409 with the operation still reviewable. Five Class-B opens plus one
approved Class-C open consume all six grants; the next otherwise-compliant B is
C on `daily_trade_count`. Move the ask above the proposal's stored cap before
approval: the approved order and reservation remain bounded by the original
cap; no repricer may cross it.

---

## Milestone 5B — Safe repricing

**Context:** M2.5 reserves open risk/cash at `approved_price_cap` while the
initial `working_price` may start at mid. M2.8 reserves closable quantity. This
milestone may improve execution only inside those already-approved bounds; it
must never invent additional entitlement during a replace.

**Spec:**
- Repricing worker (goroutine in kernel, started from main): every
  `execution_policy.reprice_interval_sec`, for each order in state
  `submitted`: cancel at broker, re-place stepped toward the marketable side
  (buy: previous limit + half the remaining distance to ask, ceil to the
  quote's tick; sell: mirrored), increment `reprices`. Operate only on the
  unfilled remainder. After `max_reprices`, request cancel; mark `cancelled` and
  release the remaining reservation only after the broker confirms a terminal
  state and all cumulative fills are durable. An ambiguous cancel remains
  `unknown` with its reservation held. Emit `order_expired_policy` on confirmed
  cancellation.
- A reprice is **two broker effects**, each its own `execution_attempt` row
  (M2.8) — this is why `execution_attempt.operation_id` is not unique. They are
  not symmetric: the `cancel` attempt names a `target_broker_order_id` and
  recovers via `GetOrder`; the `place` attempt carries a fresh
  `client_order_id` and recovers via `FindOrderByClientID`. A crash between them
  is recoverable by query, never by re-placing.
- A replacement place is created only after the target order is confirmed
  terminal at the broker. `unknown` cancel → no replacement. Ingest any fills
  that raced with cancellation first, then recompute replacement quantity from
  the broker's confirmed unfilled remainder and the still-held reservation under
  the relevant lock. This prevents a late fill plus a full-size replacement from
  over-opening or over-closing.
- **Hard bounds on every reprice:**
  - an open buy never exceeds M2.5's `approved_price_cap`; its M3A remaining
    risk/cash reservation was computed at that cap, so repricing consumes no
    new entitlement;
  - a close sell with an explicit limit never goes below that minimum, and a
    close buy never goes above its explicit maximum; a close without a limit
    started marketable and should not need chasing;
  - never step past the current marketable price or outside valid tick rules.
  Re-check quote sanity each cycle. A halted ledger cancels resting **opens**;
  verified Class-A close orders remain allowed by I6 and are not cancelled
  merely because the breaker is on.
- The M3A open reservation or M2.8 close reservation persists across cancel +
  place and transfers to the replacement; no new trade slot is allocated. The
  old order becomes terminal and the replacement attempt is inserted under the
  relevant advisory lock before another operation can consume the entitlement.

**Acceptance:** with `working_price=mid` and cap at ask, a resting buy walks
toward ask but never above the cap or its reserved cash/risk; an explicit buy
limit below market is never crossed. Reprice only the unfilled remainder after
a partial fill. Quote never moves → `cancelled` after `max_reprices` and only
  remaining risk/cash releases; halting mid-rest cancels an open but leaves a
  verified close eligible. Kill the kernel between cancel and replacement: query
  resolves the cancel, the reservation remains held, no duplicate trade slot is
  created, and the position is never double-exited. Fill the old order while its
  cancel is in flight: the replacement uses only the confirmed remainder; an
  unresolved cancel creates no replacement at all.

---

## Milestone 6 — Watchdog → runtime wake channel

**Spec:**
- agent-runtime: HTTP server on :8200 with
  `POST /wake {"role":…, "trigger":"spine", "occurrence_id":…}` → runs one
  session for that role (reuse runSession). Unknown role → 404. Requires
  `KERNEL_TOKEN` (M2.6) — an unauthenticated wake endpoint lets anyone who can
  reach the runtime burn LLM budget and drive proposals.
- **Deduplicate on `(role, occurrence_id)`**: a retried or double-fired cron
  must not start two sessions for the same slot. The kernel derives
  `occurrence_id` from the cron schedule slot, not from `time.Now()`.
- Keep the tick loop as fallback; `TICK_SECONDS=0` disables it (spine becomes
  the only driver).
- kernel watchdog `fire`: POST to `RUNTIME_URL` (env, default
  `http://agent-runtime:8200`) `/wake`; on error, log + event
  `spine_wake_failed` (the repair job is future work; leave a TODO).
- docker-compose: add RUNTIME_URL and KERNEL_TOKEN to kernel env; expose
  nothing publicly.

**Acceptance:** a wake with a valid token triggers exactly one scout session;
the same `occurrence_id` twice → still one session; no token → 401; unknown
role → 404; kernel spine_tick events pair with runtime session logs.

---

## Milestone 7 — Minimal review console (authenticated)

**Spec:**
- kernel: `GET /operations?status=&limit=&cursor=` — paginated, `limit` clamped
  to 100, cursor over `(ts, id)`. An unbounded list endpoint in front of a
  growing pending queue is its own outage.
- `GET /` serves ONE embedded HTML page (`go:embed`, vanilla JS, no build step,
  mobile-friendly): both day ledgers + breaker lights + positions (poll /state
  every 5s), pending_review operations with failed checks highlighted and the
  kernel's `derived_max_risk` shown next to the proposer's declaration. Before
  approval show quantity, kernel-derived instrument multiplier, persisted
  `approved_price_cap`, latest sane quote and the fact that execution cannot
  exceed that cap. Include Approve / Reject buttons and a "Resume breaker"
  button when halted.
- Auth landed in M2.6 — the old "no auth (deployment is private)" note is void.
  The page takes ADMIN_TOKEN once, holds it in memory, and sends it as a bearer
  header. Not in a cookie, not in the URL, not in localStorage.
- Operation plans, symbols, reasons and all other stored text are untrusted:
  render with `textContent`, never `innerHTML`. Send a restrictive CSP with no
  `unsafe-inline` (a static script hash or separately embedded same-origin JS is
  fine). A stored-XSS bug here would steal the in-memory admin token and turn a
  journal field into trading authority.
- Mutating requests require an `Origin` matching the configured console origin.
- Approve/Reject send **no** `reviewer` field; identity is the token's subject.

**Acceptance:** manual — run smoke path 2, open `http://localhost:8100/`,
approve from a phone-width viewport, operation executes (M4); the same page
with no token cannot approve; a cross-origin POST is refused; an operation field
containing `<img src=x onerror=...>` renders as text, executes no script and
cannot observe the admin token.

---

## Milestone 9 — Pre-live certification (fault injection)

Per-checklist-item table tests belong to the milestone that introduces the item
(M2.5, M3A, M3C), not to a testing milestone at the end — a test written six
milestones after the code is a test written against the bug. M9 is instead the
**certification gate before any real money moves**:

- Fault injection, each ending with the system able to state whether an order
  exists: DB paused mid-propose; DB killed between attempt claim and broker
  call; broker timeout; broker accepting an order whose id never reached us
  (the `FindOrderByClientID` path); kernel SIGKILL mid-execution; crash between
  a M5B cancel and its replace; clock skew across a market-day boundary;
  postgres failover.
- The barrier harness on every gated counter (daily trades, open risk, close
  reservation, settled funds) **plus** the deterministic advisory-lock probe —
  the barrier alone has been measured at ~40% sensitivity against known-racy
  code and cannot be the only gate. See AUDIT.md I4.
- Idempotent replay of a full trading day against FakeBroker: same input, same
  ledger, no duplicate orders.
- Targets: `risk` package coverage ≥ 90%; against FakeBroker or a provider
  sandbox with verified query/dedupe semantics, zero unresolved `unknown`
  attempts after reconciliation; zero unsafe orphan reservations. A live
  provider without verified dedupe is intentionally allowed to remain `unknown`
  for human resolution — M9 must not weaken M2.8 just to make the metric green.

---

## Milestone 10 (Phase 2 prep) — cognition/llm.go

**Spec:** implement `LLM.Run` per the comment block in the file:
`github.com/anthropics/anthropic-sdk-go`; render non-empty prompt slots in
role-card order + compact-serialized context; request schema-constrained
output (tool-use with a single tool whose input_schema mirrors the contract
struct — hand-write the JSON schemas in a `schemas.go`, one per contract);
unmarshal → Validate → on failure retry ONCE appending the validation error;
model routing `role.ModelTier`: decider→`DECIDER_MODEL`,
monitor→`MONITOR_MODEL`; temperature 0.2; max_tokens 2000; log
{role, model, input_tokens, output_tokens, latency_ms} to stdout AND to a new
kernel endpoint `POST /telemetry` (kernel writes an event; RUNTIME_TOKEN per
M2.6).

PROMPT SLOT CONTENT REMAINS BLANK — do not author prompts.

Additionally:
- **Context budget.** Per-slot size caps and a total `SESSION_TOKEN_BUDGET`. On
  overflow, refuse to run and emit an event — never silently truncate, since
  the truncated part is exactly the risk-relevant tail.
- **`blackboard` and `lessons` are UNTRUSTED.** They are written by previous
  LLM turns and reachable by anything holding a runtime token. Render them as
  data in a user slot, never in a system slot, never as instructions. The
  contracts gate the output regardless (invariant 3) and the kernel gates the
  operation regardless (invariant 10) — but do not hand a previous turn's text
  the authority of an instruction.

**Acceptance:** unit test with a mocked transport (interface over the SDK
client) covering: happy path, invalid-then-valid retry, double-invalid error.
`COGNITION=llm` with empty slots + missing API key fails loudly at startup, not
at first session. A context exceeding the budget refuses the session. A
`lessons` row containing instruction-shaped text does not change the operation
the kernel receives.

---

## Milestone 11 — Robinhood live adapter + canary

**Preconditions, all of them:** M8A's capability snapshot records the Agentic
account's type/settlement/options level and the *documented* answers to M2.8's
three client-id questions, plus a stable fill-reconciliation identity; M3D is
either implemented against those facts or explicitly waived in writing; M9
certification passes.

**Spec:**
- `robinhood` broker adapter: order placement restricted to the Agentic account
  id; every mutation asserts the M2.6 account binding; implements
  `FindOrderByClientID` and stable fill reconciliation or M11 does not ship.
  Before placement it revalidates the
  M8A-discovered asset/order support, quantity increment, price tick and option
  contract metadata against the persisted order; mismatch → fail closed, never
  coerce the order to a nearby value. It also implements M2.5's normalized
  buying-power contract; inability to distinguish/add back only Alpheus-owned
  provider holds is a no-ship condition, not permission to double-count or
  ignore reservations.
- **No real-money dedupe probe.** Testing whether duplicate client ids create
  two orders can itself create two orders and violate the one-position canary
  cap. Automatic retry therefore defaults OFF. It may be enabled only by an
  admin-only deployment setting after dedupe is demonstrated in a
  provider-supported sandbox/test environment and the evidence is recorded.
  Documentation or a live experiment whose failure mode is a duplicate order
  is insufficient. With retry off, an ambiguous live attempt remains `unknown`
  and requires human reconciliation.
- Canary: `LIVE_TRADING_ENABLED` plus new `limits.yaml` keys
  `live_canary.daily_authorized_risk_cap_usd` and
  `live_canary.clean_days_before_raise` (adding keys is allowed by invariant 6;
  parse the cap exactly through M2.5). They have no permissive default and live
  startup fails until a human sets a positive cap initially no greater than one
  minimum-size position. Under M3A's stable live-ledger gate, require the sum of
  current-day M2.8 `trade_grant.authorized_risk_micros` plus the proposed risk
  to stay within the cap **before inserting the grant** on every live grant path,
  including M4 approval. Recovery reuses an existing grant and excludes itself
  exactly as M3A specifies. A failed/cancelled
  attempt still burns both the daily trade slot and canary allowance; pending
  orders and retry storms cannot reuse it. Any current-day `legacy_unknown`
  grant fails the canary closed. A human may widen the versioned limit only after
  the configured number of clean days with zero unresolved `unknown` attempts
  and zero PnL divergence events; no runtime/API path edits it.

**Acceptance:** paper-identical behaviour between FakeBroker and the live
adapter on a replayed day; automatic retry is off with only documented dedupe
evidence; an ambiguous lookup creates no second order and is surfaced for human
reconciliation; the first live order is a single minimum-size position; the
authorized-risk cap and retry capability cannot be raised by any agent-reachable
path. Barrier 20 concurrent opens at one remaining canary allowance: exactly one
grant is created, and forcing that attempt to fail does not restore the allowance.

---

## Explicitly out of scope for the coding agent

- Authoring any prompt slot content (human task).
- Changing limits.yaml numbers or approval-class semantics. (Adding a key a
  milestone specifies is fine; see invariant 6.)
- Robinhood ORDER placement before M11's preconditions are met.
- Any UI beyond Milestone 7's single page.
- Backtest replay tooling (Phase 3, separate plan).
- Covered / multi-leg strategies. The single-leg model cannot express them and
  approximating one is how `open`+`sell` became a hole; they need their own
  position-leg design.
- Atomic option rolls — Robinhood cash accounts do not support them and alpheus
  only ever issues sequential ops.

## Definition of done (every milestone)

gofmt clean · go vet clean · go test ./... green · docker compose boots ·
smoke.sh (extended where the milestone says so) passes · new behavior has an
event trail in the `events` table · README "刻意没做的事" list updated · no new
risk decision reads a proposer-supplied value (invariant 10) · no new `float64`
on the money path (invariant 12) · every new write endpoint appears in M2.6's
token table.
