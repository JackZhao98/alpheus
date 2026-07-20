# Phase 4 — Pre-live and live

[Back to Plan Index](INDEX.md)

> Frozen specification v1.1. This phase covers M9, M10, and M11. M11 remains the
> final milestone and is gated by every listed precondition. Progress is tracked
> only in `INDEX.md`.

> Amendment v1.5 records an owner-authorized production equity `ref_id` A/B
> probe and defines a bounded same-ref replay plus exact pull-based candidate
> recovery contract. It supersedes the frozen M11 no-live-probe/default-retry
> wording only for equity recovery; the frozen text remains below for audit
> history. See `INDEX.md` and `../M11_PROVIDER_GAP.md`. Option mutations remain
> blocked.

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
  reservation, buying power) **plus** the deterministic advisory-lock probe —
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

**Implementation note:** the frozen acceptance behavior is provider-neutral in
the completed runtime. `LLM_PROVIDER=openai` uses the OpenAI Responses API and
its exact input-token counter; `LLM_PROVIDER=anthropic` retains the original
Anthropic SDK path. Both transports use the same forced single-contract call,
local validation, one-retry ceiling, budget gate, and telemetry. The current
OpenAI model setting is `gpt-5.6-sol` for both model tiers.

---

## Milestone 11 — Robinhood live adapter + canary

**Preconditions, all of them:** M8A's capability snapshot records the Agentic
account's type/buying-power/options level and the *documented* answers to M2.8's
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

## Amendment v1.8.1 — canary authority and widening evidence

K0 supersedes only the frozen canary storage/activation mechanism. Commit
`d24b8b9` removes the Live canary from `limits.yaml`, makes the typed immutable
database revision the startup and admission authority, and binds each new Live
grant to its revision/generation. The $50/five-day human policy value is not
changed by this amendment.

The frozen text allowed a widening after clean days, but implementation review
proved the former `day_open` query was not a durable completed-day
attestation. K0 therefore denies all widening, including a clean-days decrease
or mixed change. K1 must add the typed final-reconciliation attestation defined
in [`06_POLICY_OWNERSHIP.md`](06_POLICY_OWNERSHIP.md) before any widening can be
enabled. This is a fail-closed evidence correction, not removal of the human
policy.

## Amendment v1.5 implementation status

The bounded equity recovery alternative is implemented offline in migration
0009, the execution/reconciliation store, the Robinhood exact-candidate reader,
and the Admin Cockpit adoption flow. It does not enable production execution.
See `../M11_PROVIDER_GAP.md` for the acceptance evidence and remaining
recovery and option blockers as they stood at v1.5.

## Amendment v1.6 implementation status

Under a separately reviewed and owner-confirmed live ticket, one-share equity
limit placement was accepted and canonically observed, then cancelled with zero
fill; the otherwise identical 0.5-share order was rejected before creation.
Read-only boundary reviews established a $0.01 tick above $1 and $0.0001 tick at
or below $1. Commit `319f657` encodes that exact limit-only contract, requires
exact-symbol instrument identity, validates a canonical order-id read after a
successful mutation, and wires the production execution capability only in
explicit live mode. The production constructor is equity-only; option grants
and mutations remain blocked. The deployed Robinhood stack remains read-only,
and the first Alpheus-routed live canary still requires separate human
confirmation. The owner set the initial controls to a $50 daily authorized-risk
cap and five clean days before widening. A fresh-volume, no-agent, no-proposal
live-mode startup certification then passed exact account binding and health;
all operation, grant, attempt, order and fill counts remained zero, and the
isolated stack was removed.

## Amendment v1.7 — bounded replay completion and canary recovery

The v1.5 identity and latch design remains authoritative and supersedes only
the frozen M11 clauses that required `FindOrderByClientID` and prohibited the
separately owner-authorized live dedupe probe. All other M11 safety boundaries
remain in force. This amendment closes three implementation gaps without
introducing another execution mode, retry framework or recovery service.

### Ambiguous placement

- The one permitted equity replay still requires the exact same `ref_id`, bound
  account and byte-identical canonical Provider intent. It may be consumed only
  while database time is strictly before the already-persisted
  `send_window_end`, only after an exact pull of that window returns zero
  candidates, and only while the global Halt is not committed. The comparison
  and irreversible `replay_count` advance occur atomically under the existing
  Live execution gate.
- At or after `send_window_end`, zero candidates never authorizes another
  placement. Non-mutating Provider matching may continue without a time limit,
  while the attempt, grant, reservations and account latch remain held. The
  original window is both the candidate window and replay deadline; do not add
  a second window or a configurable replay duration.

### Halt-to-send serialization

`POST /halt` and every Live open-placement send authorization -- initial,
recovered pending, replacement and same-ref replay -- serialize through the
same database control lock. A send may durably cross the cut only when the
database-backed global Halt is clear, and its sent marker is committed under
that lock before the Provider call. A send already marked before the Halt cut
is in-flight work that must be reconciled; Halt cannot pretend to revoke or
roll it back. No attempt may cross from unsent to send-authorized after the cut.

The HTTP Halt result records the cut and exposes any pre-cut in-flight attempt;
the canary stop procedure drains or latches that work before declaring a clean
stop. In-memory `haltMu` remains a local optimization, not the authority. Do not
add a new service or hold an ordinary database transaction open across a
Provider network call.

### Admission while the account gate is occupied

For Live effects, idempotent lookup of an already committed request happens
first. A genuinely new request then checks the account execution gate inside
the same database transaction that would create its operation entitlement:

- `unknown_attempt_id` present -> refuse `live_execution_suspended`;
- another active attempt present -> refuse retryably as `live_execution_busy`;
- either refusal creates no new trade grant, open/close reservation, execution
  attempt or typed order; and
- Shadow work and reads remain independent.

This complements the Provider-send latch. It prevents a later latch clear from
silently releasing a queue of stale, already charged Live effects.

### Canary stop and recovery, not transaction rollback

The first Alpheus-routed canary uses the existing controls in this order:

1. stop Agent emission and commit the database-fenced `POST /halt`, preventing
   new Live open admission and send authorization while the live execution
   capability remains constructed; reconcile any explicitly reported pre-cut
   in-flight attempt;
2. let the Kernel pull and reconcile the exact attempt; if a unique placement
   candidate exists, complete the existing Admin adoption flow before any
   cancel;
3. if the adopted order is working, cancel by canonical broker order ID and
   pull until terminal; if partially filled, ingest the fills and cancel only
   the remainder; if filled, ingest order, fills, position and PnL;
4. require the live gate to be empty and prove no unresolved unknown attempt,
   unsafe reservation, orphaned grant, duplicate broker order or unexplained
   position/PnL divergence; then
5. and only then restart the deployment as `read_only` if the canary runbook
   calls for it.

If deterministic recovery cannot establish the broker fact, the deployment
stays in `TRADING_MODE=live` with global Halt committed so its non-mutating
Provider reads and Admin adoption capability remain available. Here and in the
v1.5 unknown-latch text, "read-only pulls" describes the effect of a query; it
does not mean switching the deployment to `TRADING_MODE=read_only`, which would
remove the execution adapter and adoption/cancel capability. During ordinary
Halt, canonical cancel and verified reduction remain available; while a
Provider-unknown latch is unresolved, automatic mutations remain blocked until
ownership is canonically resolved. A real fill is not undone by a software
"rollback"; any later reduction is a separately authorized, Kernel-verified
trade.

**Acceptance:** fault-injected tests cover a replay before the original window
ends, refusal at deadline equality and after expiry, a lost replay response, 20
concurrent recovery workers and 20 new admissions while active/unknown. Exactly
one same-ref replay may occur and it remains discoverable in the original
window; all refused admissions create zero entitlements. A process-restart test
preserves the unknown latch and recovery evidence. Halt-race tests cover every
Live open-placement path and prove that no unsent attempt crosses the committed
database cut; pre-cut sent work is explicitly reported and reconciled. A non-
money end-to-end canary harness proves
halt -> adopt/query -> cancel or fill reconciliation -> clean gate -> read-only
ordering. No test intentionally creates a production network ambiguity and no
real order is authorized by this amendment.

### Implementation clarification v1.7.1 — replay observability bound

The strict database predicate `clock_timestamp() < send_window_end` is
necessary but is not, by itself, proof that a Provider-created order will have
a `created_at` inside the original candidate window. Authorization can occur
before the deadline while network or server-side processing creates the order
after it. Amendment v1.7's stronger claim that such a replay necessarily
"remains discoverable" is superseded as follows:

- automatic replay additionally requires enough original-window time for a
  separately certified Provider creation-latency bound; the comparison and
  `replay_count` consumption remain atomic and use database time;
- this is a guard inside the one original window, not a second window, replay
  TTL or runtime policy knob;
- FakeBroker has a synchronous bounded path and may exercise this recovery in
  certification; and
- Robinhood `ref_id` dedupe is empirically verified, but no server-side
  creation-latency bound is currently certified. Robinhood automatic replay
  therefore remains disabled. Exact candidate pulls, the unknown latch and
  Admin adoption remain available and fail closed without sending again.

The durable sent marker is the formal Halt/send linearization point. It and
the Halt cut timestamp are created from advancing database time after the
shared control lock is acquired. A claimed open that loses the cut is
terminally rejected without a Provider call, its unsent typed order and held
reservation are closed, its consumed trade grant is not restored, and any
already-executed replacement source remains recorded as executed.

Commit `0913010` implements this clarification without a production order. The
bound account, canonical Provider intent and SHA-256 fingerprint are compared
in the same database update that consumes `replay_count`. Automatic integrity
Halts use the same Halt/send cut after rolling back the failed fill transaction.
Across ordinary terminal updates, pending cleanup and claimed cleanup, any
durable fill keeps the parent operation `executed`; only the unsent remainder
and its typed order become failed/rejected.

## Amendment v1.9.1 — real Canary deferred, Live gates preserved

The owner explicitly deferred the one-share production Canary. M11 remains
non-landed as `CANARY DEFERRED`; production stays read-only and this phase
authorizes no order. K1, B0 and non-money AP0–AP12 work may continue under
[`08_DEFERRED_CANARY.md`](08_DEFERRED_CANARY.md), because those stages require
zero production broker mutations. The exact M11 Canary and this phase's full
stop/recovery acceptance remain mandatory before AP13 or any later Agent Live
stage and must run against the final applicable post-K1/B0 Kernel.

## Amendment v1.9.3 — real Canary accepted

On 2026-07-20 the owner separately confirmed two one-share SOFI tickets routed
through the post-K1/B0 Alpheus Kernel. The first was a working limit order that
the fenced Halt procedure cancelled with zero fill. The second was a true
Market order that Robinhood filled once at `$17.09`. A canonical response-shape
gap initially left the second attempt `unknown`; no retry occurred, Halt stayed
committed, an exact bounded pull returned one candidate, and the existing Admin
flow adopted it into one durable order, one fill, converted exposure and an
empty Live gate. The adapter correction is `2d1b66b`; guarded Market support is
`65492f1`; fenced Halt resume is `23a1a13`.

Both irreversible grants total exactly the active `$50` daily Canary cap. The
final deployment is `read_only` with global Halt committed, mutations disabled
and no control warnings. The account intentionally retains the one filled SOFI
share; a later sale would be a new separately authorized operation, not Canary
rollback. The full evidence is recorded in `../M11_PROVIDER_GAP.md`.

M11 is therefore `LANDED`. This supersedes only the historical deferral status
in v1.9.1. Option mutation and automatic Robinhood replay remain uncertified,
and landing M11 does not itself activate AP13 or waive its other prerequisites.

## Amendment v1.9.4 — execution-core acceptance reopened

The v1.9.3 Market canary and recovery facts remain accurate, but the subsequent
first owner-directed working close lifecycle exposed two interactions outside
that acceptance. Kernel operation `552b1515-c1a1-48bb-9d32-f2b03d59461b`
placed one SOFI sell limit at `$18`; the implicit repricer then cancelled and
replaced the same-price order three times. At no point were two sells active and
no fill occurred, but a user-selected static price must not authorize hidden
cancel/replace effects. A later return to `read_only` with the working order
panicked because a nil FakeBroker was boxed into a non-nil execution interface.

Commit `776635a` makes explicit close limits static and prevents the typed-nil
production compatibility adapter. Those repairs are necessary but not by
themselves new production acceptance. M11 returns to `IN PROGRESS` until one
deterministic matrix covers static Limit buy/sell submission, query, cancel,
fill, partial fill, expiry, working-order restart, and read-only observation,
with exactly one broker effect per authorized request. Market response-shape
fixtures must cover working and terminal price forms. Repricing becomes
explicit opt-in rather than an implicit property of every eligible Limit.

The first reopened-acceptance slice landed in `26a93f2`. Every new open and
close now persists `execution_style=static` unless the caller explicitly asks
for `managed`; historical operations without the field also remain static.
Only managed Limit operations may reach the repricer, managed Market orders are
rejected, and the execution style is part of the idempotent client intent. A
recorded Robinhood working-sell fixture proves the accepted close shape is
normalized after exactly one placement call. This closes the implicit-reprice
default but does not complete the remaining lifecycle matrix or reland M11.

Production remains `read_only` with global Halt committed. AP13, autonomous
Live, Option mutation and additional real-money probes remain closed pending a
new owner decision after non-money certification.

## Amendment v1.9.5 — minimal equity lifecycle recertified

Commits `26a93f2` and `4f54331` complete the reopened non-money acceptance.
Static execution is now the default for every open/close, including historical
payloads without the new field. Managed repricing is explicit Limit-only
intent and participates in the idempotency hash. Recorded and deterministic
Provider fixtures cover working, partial fill, fill, cancel, partial-cancel,
expiry and rejection. Equity Cancel produces exactly one mutation and a repeat
against its terminal state is read-only. Existing durable fill, reservation and
illegal-transition suites remain green.

The full suite, race suite and vet pass. The current image was rebuilt and
started healthy with `TRADING_MODE=read_only` and
`LIVE_TRADING_ENABLED=false`; the authenticated Kernel API passed. The prior
production Canary and incident facts remain unchanged, so no additional money
probe is required. M11 returns to `LANDED` for the equity-only lifecycle.

This does not certify Option mutation, high-frequency execution, automatic
Robinhood replay, AP13 activation or autonomous Live. Global Halt remains
committed and production remains read-only.
