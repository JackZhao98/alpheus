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
`go test ./...`; on a fresh database, explicitly run the `kernel-policy`
bootstrap before `docker compose up --build`; and keep `scripts/smoke.sh`
passing. Normal server startup must never import that file automatically.

## Context

alpheus is an agentic trading system for a small Robinhood account. The landed
Kernel and the frozen Lean v1 Agent architecture use one Kernel, one Agent
Platform distribution with credential-isolated profiles, one Research Gateway,
and PostgreSQL plus bounded blob storage:

- `kernel/` — deterministic Go service. Owns broker credentials, typed
  database policy authority (`kernel/limits.yaml` is bootstrap input only),
  operation approval, order lifecycle,
  persistence. HTTP on :8100. Deps: lib/pq, robfig/cron, yaml.v3.
- `agent-runtime/` — the landed prototype cognition loop. It remains available
  while AP0/AP1 build the durable replacement, but its direct proposer is
  retired before the new Control Plane claims triggers.
- `agent-platform/` — the Lean v1 target distribution. `control-api`, `worker`,
  GRACE, Delegation, Validator and Activator profiles run with separate
  credentials and record-family permissions even when they share code.
- `research-gateway/` — approved read-only external Tool/market-data sessions,
  normalization, quarantine and egress policy. It never holds production broker
  mutation credentials.
- `db/migrations/` plus content-addressed blob storage — relational authority,
  durable workflow/evidence state and bounded referenced bytes.

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
2. Only the Kernel may mutate or reconcile the broker. Agent Workers never
   receive broker credentials or call market/MCP endpoints directly. Approved
   external reads go through the Research Gateway; broker/account truth used by
   a risk or execution gate comes from Kernel/Provider projections.
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

### Amendment v1.8 — policy ownership (supersedes invariants 1 and 6 only)

Human/business risk values remain Kernel-owned and impossible for prompts or
Agents to override, but their runtime authority moves from `limits.yaml` to
typed immutable database policy revisions with an audited active head. Code
continues to own structural invariants and absolute resource/protocol ceilings;
deployment config owns endpoints, secrets, physical account binding, timeouts
and a maximum capability ceiling; Provider/account data remains observed fact.
The effective permission combines those layers, current Kernel/Provider facts
and, where the effect requires it, the operation's scoped grant. The frozen
Class-C route remains: an exact approval may cover only named reviewable
checklist failures; it cannot cover structural or non-overridable absolutes.
Cancel, reconciliation and verified reduction do not depend on an opening grant.

`limits.yaml` becomes a one-time bootstrap/export fixture. Once the
corresponding domain head exists, missing or invalid database policy fails
closed and never falls back to that domain's file values. Existing numeric
values and prompt content remain human-owned: normal implementation work may
migrate them without changing them, while any value or semantic change requires
its own authorized policy revision. Full ownership, binding and transition
rules are in
[`06_POLICY_OWNERSHIP.md`](06_POLICY_OWNERSHIP.md).

### Amendment v1.9.2 — Lean v1 Agent Platform entry

The owner accepted the Lean v1 architecture after K1 policy ownership and B0
broker coexistence landed. The Agent Platform is one distribution with
credential-isolated profiles, not one service per logical Agent or governance
role. The deterministic Control Plane owns durable Run/Task/Attempt/Artifact
state; Workers return untrusted typed Artifacts; Research Gateway owns approved
external reads; Kernel remains the sole broker mutation and hard-risk boundary.

AP0 is authorized only to build common contracts, identity/authority scaffolds,
database roles, outbox/inbox, bounded blob handling and disabled-by-default
effect controls. It cannot emit an operation or activate GRACE, Delegation or
Live. AP0 must implement the digest-bound release manifest and verification
mechanism required before AP1. Repository history plus the explicit owner
decision authorize AP0 entry; no prose status can authorize later stages or
runtime effects. Exact ownership and supersession rules are frozen in
[`../agent-plan/LEAN_V1_AMENDMENT.md`](../agent-plan/LEAN_V1_AMENDMENT.md).

---

## Canonical sequence

See [`INDEX.md`](INDEX.md). It is the only canonical source for milestone
status, dependency gates, and the current implementation target.

---

## Explicitly out of scope for the coding agent

- Authoring any prompt slot content (human task).
- Changing human-owned policy values or approval-class semantics during a
  storage migration. Importing identical values under v1.8 is allowed; any
  value/semantic change requires its own authorized policy revision.
- Robinhood ORDER placement before M11's preconditions are met.
- UI polish, responsive refinement or additional clients before their owning
  Agent Web milestone; early desktop diagnostics may remain deliberately plain.
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
