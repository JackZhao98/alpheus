# Phase 2 — Ledger and controls

[Back to Plan Index](INDEX.md)

> Frozen specification v1.1. This phase covers M3A, M3C, M3D, M4, and M5B.
> M3D remains blocked until the earlier M8A supplies provider evidence.
> Progress is tracked only in `INDEX.md`.

<!-- BEGIN FROZEN SPEC -->

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


<!-- END FROZEN SPEC -->
