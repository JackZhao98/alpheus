# Phase 0 — Landed baseline

[Back to Plan Index](INDEX.md)

> Frozen specification v1.1. These milestones are landed and retained as the
> historical acceptance contract. Progress is tracked only in `INDEX.md`.

<!-- BEGIN FROZEN SPEC -->

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


<!-- END FROZEN SPEC -->
