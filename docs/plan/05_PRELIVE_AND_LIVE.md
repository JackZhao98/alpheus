# Phase 4 — Pre-live and live

[Back to Plan Index](INDEX.md)

> Frozen specification v1.1. This phase covers M9, M10, and M11. M11 remains the
> final milestone and is gated by every listed precondition. Progress is tracked
> only in `INDEX.md`.

<!-- BEGIN FROZEN SPEC -->

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


<!-- END FROZEN SPEC -->
