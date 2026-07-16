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
  lessons, blackboard.

Operation approval classes (`kernel/internal/risk`):
- **A** risk-reducing (close/cancel/tighten_stop) → execute immediately.
- **B** opening trade passing the full deterministic checklist → auto-execute.
- **C** checklist failure but no absolute violation → `pending_review`.
- **REJECT** absolute violation (halted breaker, naked short) → dead.

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
6. Do not modify `limits.yaml` values or `roles/*.yaml` prompt slots
   (prompt content is a human task).
7. All timestamps stored in UTC; market-time logic uses TZ_MARKET.

---

## Milestone 1 (P0) — Complete the Class-A fast path

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
  (the latter used by Milestone 3).

**Acceptance:**
- smoke path 3 (`close`) returns an `order` object with `state: "filled"`
  against the fake broker.
- Unit test: propose close via httptest against a server wired to FakeBroker;
  assert order placed at bid for a long close.
- Regression tests: no-position, zero/negative qty, over-close, garbage side,
  and concurrent double-close never reach the broker or open reverse exposure.
- New smoke step: cancel of an unknown order returns state `rejected`.

---

## Milestone 2 (P0) — Dual ledger: shadow vs live day-state

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
  This is the shared risk-gate primitive for M3 open-risk checks and remains
  correct with multiple kernel instances.
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
- smoke.sh prints both ledgers from /state.

---

## Milestone 3 — Real day-state: open risk, daily PnL, breakers

**Spec (compute-on-read; no caching until it hurts):**

1. **Open risk** per ledger:
   an `open` operation is *live-open* if `status IN ('auto_approved','executed')`
   and no executed operation references it via `closes_operation_id`.
   ```sql
   SELECT COALESCE(SUM((payload->>'max_risk_usd')::numeric),0)
   FROM operations o
   WHERE o.payload->>'action'='open'
     AND o.status IN ('auto_approved','executed')
     AND (o.payload->>'shadow')::bool = $1
     AND NOT EXISTS (SELECT 1 FROM operations c
                     WHERE c.payload->>'closes_operation_id' = o.id::text
                       AND c.status IN ('auto_approved','executed'));
   ```
   position_manager (stub for now) is responsible for setting
   `closes_operation_id` on closes; kernel does not infer it.

2. **Day-open equity:** new table
   `day_open (day date, ledger text, equity numeric, PRIMARY KEY(day, ledger))`.
   On the first `dayState()` call of a calendar day (market tz), insert the
   current equity. Shadow ledger uses the same account equity value.

3. **Realized PnL today (live ledger):** from `fills` joined through
   `orders → operations`, sum of `(sell fills) - (buy fills)` in dollar terms
   (`qty * price * mult`, mult 100 for options — persist `kind` on the order
   row; see Milestone 5 which makes kernel actually write `orders`).
   Until Milestone 5 lands, return 0 with a TODO. For the shadow ledger,
   compute the same from shadow operations' assumed fill prices recorded in
   the `order_update` events (fake broker fills shadow? — NO: shadow ops never
   execute; shadow PnL becomes meaningful only when Phase-3 shadow marking is
   built. Return 0 for shadow, documented).

4. **Breakers:** new table
   `breaker_state (ledger text PRIMARY KEY, halted bool NOT NULL DEFAULT false,
   reason text, updated_at timestamptz DEFAULT now())`.
   - Evaluate inside `dayState()`: if
     `realized_pnl_today <= -(day_open.equity * max_daily_loss_pct/100)`,
     set halted for that ledger with reason `daily_loss`; this auto-clears
     next day (daily-loss halt is day-scoped: when day_open.day advances,
     clear a `daily_loss` halt automatically).
   - Consecutive-loss-days halt (`consecutive_loss_days_halt`): count the
     most recent N market days where the ledger's realized PnL was negative;
     if ≥ limit, set halted with reason `loss_streak`. This one does NOT
     auto-clear: add `POST /breaker/resume {"ledger":"live"}` (also clears
     daily_loss early if a human insists). Log every transition as a
     `breaker` event.
- `risk.DayState.Halted/HaltReason` populated from breaker_state + evaluation.

**Acceptance:**
- Table-driven store tests using a real postgres from docker compose
  (`go test -tags integration`, skipped when DATABASE_URL unset).
- Manual: insert a fake losing fill set; /state shows halted live ledger;
  a live open proposal returns REJECT with `breaker halted: daily_loss`;
  shadow proposals still classify normally; POST /breaker/resume clears it.

---

## Milestone 3b — Cash-account settlement model (T+1 buying power)

**Context (confirmed):** the live account is a Robinhood CASH account,
options Level 2. No PDT applies. Per Robinhood's official docs, cash
accounts CANNOT trade with unsettled funds from stock/options sales
(1-trading-day settlement) — Robinhood excludes unsettled proceeds from
buying power entirely, so GFVs are structurally impossible there. The
kernel must MIRROR this behavior: (a) FakeBroker has to model it or
shadow-mode stats will assume turnover the real account can't execute;
(b) gating in the kernel is cleaner than submitting orders the broker
will reject. Also note: Robinhood cash accounts don't support option
ROLLING (simultaneous close+open); alpheus only ever issues sequential
ops, which is fine — never build an atomic roll.

**Spec:**
- `broker.AccountState` already carries `SettledCash`; make it real:
  FakeBroker models settlement — buys consume settled cash only (reject
  with reason `insufficient settled funds` if exceeded, mirroring RH);
  sell proceeds land in an unsettled bucket and move to settled at the
  next market-day rollover. Add sim control `POST /sim/advance_day`
  (fake broker only) so tests can turn the clock.
- `risk.DayState` gains `SettledCash float64` and `AccountType string`.
- `risk.Classify`: for `open` on a cash account (live ledger), new
  checklist item `settled_funds`: `MaxRiskUSD <= day.SettledCash`.
  Failure → Class C like any other check. Shadow ledger uses the same
  value for now; proper shadow settlement simulation is a Phase-3
  refinement (leave a TODO).
- Note for humans, not code: options Level 2 + small equity means the
  practical playbook universe is LONG calls/puts only (covered calls and
  cash-secured puts are capital-infeasible at this size). No kernel change —
  `allow_naked_short_options: false` already covers the forbidden side.

**Acceptance:** integration test: live open consuming all settled cash
fills; a second live open the same day → Class C with `settled_funds`
failed; `POST /sim/advance_day` → proceeds settle → same proposal →
Class B. FakeBroker rejects a direct over-settled-cash order with
`insufficient settled funds`.

---

## Milestone 4 — Execute approved Class-C operations

**Spec:** in the review handler, on `approved`: reload the operation payload,
unmarshal into `risk.Operation`, re-fetch quote, and run the SAME
`s.execute(...)` from Milestone 1. Set status `executed` on fill. Re-run
`risk.Classify` first with current day-state; if it now REJECTs (breaker
tripped between proposal and approval), refuse with 409 and reason — an
approval must not bypass absolutes.

**Acceptance:** integration test: over-budget open → pending_review →
approve → order placed; same flow with breaker halted → 409.

---

## Milestone 5 — Order lifecycle: persist orders + repricing worker

**Problem:** the `orders` table exists but kernel never writes it, and
non-marketable limit orders sit forever.

**Spec:**
- On placement: insert an `orders` row (id = store.NewID(), operation_id,
  broker_order_id, state, payload = {symbol, side, qty, limit, kind,
  reprices: 0}). Update state via `state.Advance` only; illegal transitions
  are bugs → log + event, never silent.
- Repricing worker (goroutine in kernel, started from main):
  every `execution_policy.reprice_interval_sec`, for each order in state
  `submitted`: cancel at broker, re-place stepped toward the marketable side
  (buy: previous limit + half the remaining distance to ask, ceil to cent;
  sell: mirrored), increment `reprices`. After `max_reprices`, cancel and
  mark `cancelled`, event `order_expired_policy`. Never chase past the
  marketable price.
- FakeBroker: add a way for tests to make a symbol non-marketable then
  marketable (already possible via SetQuote; no change expected).
- Poll fills for live brokers is future work; FakeBroker fills synchronously,
  so the worker only handles the resting case.

**Acceptance:** integration test: set SPY ask far above limit → propose
open (Class B, non-shadow) → order rests; move quote down via /sim/quote →
next reprice cycle fills; orders row walks new→submitted→filled with fills
row written (extend FakeBroker or kernel to insert fills on fill).
Second test: quote never moves → order ends `cancelled` after max_reprices.

---

## Milestone 6 — Watchdog → runtime wake channel

**Spec:**
- agent-runtime: add HTTP server on :8200 with `POST /wake {"role": "...",
  "trigger": "spine"}` → runs one session for that role (reuse runSession).
  Reject unknown roles 404. Keep the tick loop as fallback; `TICK_SECONDS=0`
  disables it (spine becomes the only driver).
- kernel watchdog `fire`: POST to `RUNTIME_URL` (env, default
  `http://agent-runtime:8200`) `/wake`; on error, log + event
  `spine_wake_failed` (the repair job is future work; leave a TODO).
- docker-compose: add RUNTIME_URL to kernel env; expose nothing publicly.

**Acceptance:** `curl -X POST localhost:8200/wake -d '{"role":"scout"}'`
(port-forwarded) triggers exactly one scout session; unknown role → 404;
kernel spine_tick events now paired with runtime session logs.

---

## Milestone 7 — Minimal review console

**Spec:**
- kernel: `GET /operations?status=pending_review` returns rows (id, ts,
  proposer, payload, verdict).
- `GET /` serves ONE embedded HTML page (`go:embed`, vanilla JS, no build
  step, mobile-friendly): shows both day ledgers + breaker lights + positions
  (poll /state every 5s), lists pending_review operations with the failed
  checks highlighted, Approve / Reject buttons → POST review with
  `reviewer: "human"`, and a "Resume breaker" button when halted.
- No auth (deployment is private); add a TODO comment for a shared-secret
  header.

**Acceptance:** manual: run smoke path 2, open http://localhost:8100/,
approve it from a phone-width viewport; operation executes (M4).

---

## Milestone 8 — Marketdata facade + Robinhood MCP (read-only)

**Spec:**
- New package `kernel/internal/marketdata`:
  ```go
  type Provider interface {
      Quote(symbol string) (broker.Quote, error)
      Chain(underlying, expiry string, windowPct float64) ([]OptionQuote, error)
      Expirations(underlying string) ([]string, error)
      Bars(symbol string, days int) ([]Bar, error)      // daily OHLCV, days<=30
      Movers(direction string, n int) ([]Mover, error)  // n<=10
      Hours() (MarketHours, error)
  }
  ```
  Server-side caps are part of the contract (windowPct<=15, days<=30, n<=10):
  clamp, don't error.
- `fake` provider: backed by the FakeBroker quote map + synthesized chains
  (strikes every $1 within window, OI 1000, spread 5% of mid) and flat bars.
- `robinhoodmcp` provider: uses `github.com/modelcontextprotocol/go-sdk`.
  EXACTLY ONE call site wraps `CallTool` with: 10s timeout, TTL cache
  (quotes 2s, chains 30s, bars/hours 10min), a token-bucket rate limiter
  (default 30 calls/min, env-tunable), and one retry on transport error.
  Wrap only the tools the interface needs; map/normalize to the structs
  above and DISCARD all other fields. Env: `MARKETDATA=fake|robinhood_mcp`,
  `RH_MCP_URL`, `RH_MCP_TOKEN` (or whatever auth the MCP requires — keep it
  in kernel env only).
- Kernel endpoints (agents' only doorway):
  `GET /market/quote/{symbol}`, `GET /market/chain/{underlying}?expiry=&window_pct=`,
  `GET /market/expirations/{underlying}`, `GET /market/bars/{symbol}?days=`,
  `GET /market/movers?dir=&n=`, `GET /market/hours`.
- assemble: inject quotes for all held positions into context under
  `"quotes"`. Exploratory calls stay tool-side for Phase 2 (do NOT build
  runtime tools now; just the kernel endpoints).
- risk.Classify keeps using `broker.GetQuote` OR switch kernel's internal
  quote source to the marketdata provider — choose the provider (single
  source of truth) and make FakeBroker consume the same fake provider's
  quote map to avoid divergence. Document the choice in code.

**Acceptance:** unit tests for clamping + cache TTL (fake clock or short
TTLs); with MARKETDATA=fake, /market/chain returns a filtered window;
smoke.sh extended with a /market/quote/SPY call. robinhoodmcp compiles and
is exercised behind an env-gated integration test that skips without
RH_MCP_URL.

---

## Milestone 9 — Risk test expansion

Table-driven tests in `kernel/internal/risk`: for EVERY checklist item, a
pass case, a fail case, and a boundary case (e.g. max_risk_usd exactly at
per-trade cap passes; one cent above fails; spread exactly at
max_relative_spread passes). Include dual-ledger cases from M2 and breaker
REJECT cases from M3. Target: risk package coverage ≥ 90%.

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
kernel endpoint `POST /telemetry` (kernel writes an event). PROMPT SLOT
CONTENT REMAINS BLANK — do not author prompts.

**Acceptance:** unit test with a mocked transport (interface over the SDK
client) covering: happy path, invalid-then-valid retry, double-invalid error.
`COGNITION=llm` with empty slots + missing API key fails loudly at startup,
not at first session.

---

## Explicitly out of scope for the coding agent

- Authoring any prompt slot content (human task).
- Changing limits.yaml numbers or approval-class semantics.
- Robinhood ORDER placement (Phase 4; needs human-verified account
  type/options level first).
- Any UI beyond Milestone 7's single page.
- Backtest replay tooling (Phase 3, separate plan).

## Definition of done (every milestone)

gofmt clean · go vet clean · go test ./... green · docker compose boots ·
smoke.sh (extended where the milestone says so) passes · new behavior has an
event trail in the `events` table · README "刻意没做的事" list updated.
