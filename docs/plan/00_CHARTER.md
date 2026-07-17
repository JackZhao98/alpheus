# Alpheus Plan Charter

[Back to Plan Index](INDEX.md)

> Plan v1.1 is frozen; amendments are recorded in `INDEX.md`.
> This file owns global context, invariants, scope, and the Definition of Done.
> Canonical sequence and progress live only in `INDEX.md`.

## Change control

- Milestone specifications are frozen; progress updates belong in `INDEX.md`.
- A specification change requires evidence and an amendment-log entry in
  `INDEX.md` before this charter or a phase file changes.
- Human-owned limits and prompt content remain outside normal implementation
  edits as specified below.

---

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

## Canonical sequence

See [`INDEX.md`](INDEX.md). It is the only canonical source for milestone
status, dependency gates, and the current implementation target.

---

## Explicitly out of scope for the coding agent

- Authoring any prompt slot content (human task).
- Changing limits.yaml numbers or approval-class semantics. (Adding a key a
  milestone specifies is fine; see invariant 6.)
- Robinhood ORDER placement before M11's preconditions are met.
- Any UI beyond the M8B/M7 single Trading Cockpit.
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
