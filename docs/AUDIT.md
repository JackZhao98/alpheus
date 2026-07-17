# alpheus — Audit Charter (work order for an auditing agent)

You are auditing a running system, not reviewing a codebase. alpheus is an
agentic options-trading system; the kernel gates every trade through
deterministic risk rules. Your job is to try to make the DEPLOYED system
violate its invariants, and to report reproducible findings. You are NOT
the builder: do not fix anything, do not refactor, do not open PRs.

Companion plan: [`plan/INDEX.md`](plan/INDEX.md). Read the index first, then
[`plan/00_CHARTER.md`](plan/00_CHARTER.md) and only the phase file containing
the current milestone. `PLAN.md` is a compatibility entrypoint.

## Methodology — behavior first

Primary method: black-box testing against the running stack
(`docker compose up`). Allowed observation/attack surfaces:

- The kernel HTTP API on :8100 (curl / scripts / a small test harness).
- The agent-runtime logs (`docker compose logs`).
- Read-only SQL via `docker compose exec db psql -U alpheus` (events,
  operations, orders, fills, journal — the audit's ground truth).
- Fake-broker sim controls (`POST /sim/quote`, `/sim/advance_day` once it
  exists) to shape market conditions.
- Container lifecycle: restart/pause/kill of kernel, agent-runtime, db.

Reading source code is permitted ONLY to localize and explain a behavioral
finding you already reproduced; cite file:line in the finding. Do not
report code-style observations. If you believe a code-level risk exists
that you cannot reach behaviorally, list it under "Untested concerns" —
clearly separated from findings — with the fault timing that would expose it.

Every finding MUST include: a repro (exact commands or a script committed
under `audit/repro/`), expected behavior, actual behavior, the invariant
violated, and severity.

## Severity scale

- **S0** — money or control loss: broker effect without a record, gate
  bypass, shadow op reaching the broker, execution while halted.
- **S1** — state corruption / inconsistency: counters wrong under
  concurrency, missing event trail, double execution on retry.
- **S2** — availability: crash, hang, unrecoverable state after restart.
- **S3** — correctness, minor: wrong error codes, misleading responses.
- **S4** — hygiene: log noise, unclear messages. Report briefly or not at all.

## Invariants to attack (the core of this audit)

- **I1 No effect without approval.** After any `rejected` or
  `pending_review` proposal, fake-broker cash and positions are unchanged
  (verify via /state before/after).
- **I2 Shadow never reaches the broker.** After any `shadow: true`
  operation of any class, broker cash/positions unchanged.
- **I3 Full audit trail.** Every operation with status
  `auto_approved`/`executed` has: an `operation_proposed` event, an
  operations row, and (for executed) an `order_update` event. No orphans in
  either direction.
- **I4 Counters are race-safe.** With exactly one daily-trade slot
  remaining, fire 20 concurrent compliant live proposals: at most one may be
  auto-approved. **Do not use `xargs -P` or process-per-request curl**: process
  startup jitter can serialize the requests enough to produce a false PASS.
  Use the same-process start-barrier harness in `audit/repro/i4_barrier.go`:
  after a fresh `docker compose up db kernel`, run `go run
  ./audit/repro/i4_barrier.go`, then repeat on another fresh database with
  `-shadow`. To probe multiple kernel instances, pass comma-separated endpoints
  with `-urls http://localhost:8100,http://localhost:8101`. Repeat this barrier
  pattern for total_open_risk near the cap. This harness is a reference
  implementation, not the audit oracle: auditors should design and run an
  independent concurrency probe as well. Through M2.9 the deterministic lock
  probe uses `(ledger,market_day)`; once PLAN M3A lands, use its stable
  per-ledger gate key and add a market-midnight barrier case. Continuing to hold
  the retired day key would produce a false failure of the probe itself.
- **I5 No gate bypass via payload.** Probe with: uppercase/whitespace
  action values ("OPEN", " open"); qty 0, negative, huge (1e18);
  max_risk_usd negative/0/NaN-as-string; limit 0/negative; plan keys
  present but empty/whitespace; unknown extra fields; absurdly long
  strings; unicode symbols; content-type omitted; truncated JSON. Every
  malformed input must land in C/REJECT/400 — never B, never a broker call.
- **I6 Class A survives the breaker (by design).** When halted (once M3
  lands; before that, note as untestable), `close` still executes.
  Conversely `open` while halted must REJECT.
- **I7 Restart safety & idempotency.** Restart kernel mid-traffic; verify
  no state corruption. Then: submit the same proposal twice (simulating a
  client timeout-retry) — today this creates two operations and two orders.
  Assess and report: is double-execution on retry reachable? (Likely S1;
  there is no idempotency key yet.)
- **I8 Dependency-failure honesty.** `docker compose pause db`, then
  propose: the kernel must fail closed (no broker call) and return an
  error, not 200. Unpause and verify recovery without restart. Attempt to
  time a db failure between approval and execution; if you cannot hit the
  window black-box, record it under Untested concerns (suspected ignored
  write errors on the money path).
- **I9 Review flow integrity.** Review a non-pending op → 409; review the
  same op twice → second is 409; reject-then-approve impossible; approve
  with garbage verdict value → rejected input, status unchanged.
- **I10 Input surfaces of the small endpoints.** /blackboard/{day} with a
  non-date, huge doc (>1MB), invalid JSON; /lessons?limit=-1, 1e9,
  non-numeric; /operations/{id} with malformed UUID; /sim/quote with
  negative/crossed bid-ask (ask < bid) — then check whether risk checks
  using RelativeSpread behave sanely on crossed quotes.
- **I11 Loop containment.** Let the stub run: verify the 7th+ live-quota
  consumption degrades to pending_review (it does — this is by design) and
  that the pending queue growing unbounded does not degrade the API
  (propose latency stable with 1,000+ pending rows; seed via script).

## Suppression list — known, tracked, NOT findings

These are scheduled work in the plan index and its phase files; verify they
behave as currently documented, but do not report them as discoveries:

M1 and M2 have landed: Class-A behavior and dual-ledger counters are audit
targets, not suppressed findings.

1. day_state open_risk/pnl are 0; breakers never trip (PLAN M3A/M3C).
2. `orders`/`fills` tables are never written (PLAN M2.9).
3. No auth on any endpoint (PLAN M2.6, P0 — scheduled, not yet landed).
4. Approved Class-C ops are not executed (PLAN M4).

Suppression is about the CURRENT build, not the plan: each item above is still
the live behavior. When its milestone lands, the item leaves this list and
becomes an audit target — item 3 in particular, the moment M2.6 ships.

If any suppressed item's ACTUAL behavior differs from its description here
(e.g. a shadow op somehow places an order), that IS a finding.

## Deliverables

Produce `audit/FINDINGS.md`:

1. **Verdict table** — one row per invariant: PASS / FAIL / PARTIAL /
   UNTESTABLE, with finding IDs.
2. **Findings** — ordered by severity: ID, severity, invariant, repro,
   expected, actual, suspected location (file:line, optional).
3. **Untested concerns** — suspected risks you could not reach
   behaviorally, and what instrumentation would make them testable.
4. **Repro scripts** — `audit/repro/*.sh`, each self-contained against a
   fresh `docker compose up`.

Reset state between destructive test groups with `docker compose down -v`.
Do not modify any file outside `audit/`.
