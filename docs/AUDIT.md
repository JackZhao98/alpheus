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
- **I6 Class A survives the breaker (by design).** Use the M2.6 Admin-token
  `POST /halt`; after it commits, `close` and `cancel` still execute.
  Conversely `open` while halted must REJECT.
- **I7 Restart safety & idempotency.** Restart kernel mid-traffic; verify
  no state corruption. In live mode, omit `Idempotency-Key`, use whitespace,
  control bytes and 201 characters: each must be 400 before classification.
  Submit the same normalized intent twice with one key: the second response
  must return the original operation id/current status and produce no second
  classification or broker effect. Change every client-writable field in turn
  under the same key: each must return 409 `idempotency_key_reused`. Release 20
  same-key requests from a same-process barrier: exactly one operation and one
  broker effect. Force agent-runtime's first response read to fail and verify
  its retry sends the identical body and key. Separately exercise migrations:
  fresh DB, exact legacy-M2 baseline with retained sentinel data, partial schema
  rejection, applied-checksum mismatch and two concurrent kernel starts.
  For M2.8, independently inject all three crash windows around attempt
  commit/claim/broker acceptance and verify the stable client id produces the
  specified 0/0/1 broker effects. A stale `pending` must re-run the gate and
  obey the 1800-second proposal TTL; `unknown` must query before any retry, and
  a provider without independently verified deduplication must not re-place.
  Hold the close symbol advisory key externally (signed big-endian first 64
  bits of SHA-256 over `symbol\0{ledger}\0{symbol}`): a close must block or
  hit `DB_TIMEOUT_MS` with no broker effect. Through M2.8 a filled close remains
  reserved fail-closed; M2.9 must prove the durable fill before decrementing or
  releasing it.
- **I8 Dependency-failure honesty.** `docker compose pause db`, then
  propose: the kernel must return 503 within `DB_TIMEOUT_MS` (allowing only
  small scheduler/network jitter) and make no broker call. This probe must
  pause the server process, not merely inject `pg_sleep`, because lib/pq's
  cancellation transport is part of the acceptance boundary. Unpause and
  verify recovery without restart. Attempt to
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
- **I12 Mode and identity boundary.** Outside sim, reads without a valid bearer
  return 401; Runtime Token can propose/write journal and blackboard but cannot
  review; reviewer identity is `admin`, never payload text. In `read_only`,
  every write is 405. In `live`, `/sim/*` is 404. Agent-runtime `/wake` rejects
  every token except `KERNEL_TOKEN`. Confirm no token or account id appears in
  logs, events, operation payloads, or API responses.
- **I13 Production-read Provider boundary.** Assert the Robinhood account
  Provider does not satisfy `ExecutionProvider`; `read_only` and `shadow` must
  construct no production object with place/cancel/replace methods. Start with
  a missing, renamed, or schema-mutated required MCP tool: startup must fail
  closed. A missing, duplicate, or different account must fail and emit only a
  sanitized account-binding event. Move/nest a required money field, remove a
  stable identifier, add more than six decimal places, or change the option
  multiplier in golden fixtures; all must fail closed. Feed stale,
  future-dated, locked, crossed, non-positive and incomplete quotes through
  `/market/quote` and an open proposal; neither may approve a price. Very large
  chain/bar/mover requests must reach the Provider only as 15 percentage
  points, 30 days and 10 items; negative/malformed values are 400. A 0644 OAuth
  file, trailing JSON value, expired non-refreshable token, stalled call, and
  dropped transport must respectively fail before connection, respect the
  deadline, and reconnect only once. Scan logs/API for tokens, account numbers
  and raw payloads. Run an independent probe in addition to the env-gated
  loopback reference test.
- **I14 Read-only Trading Cockpit.** Outside sim, load `/` without a token:
  the static shell may render but every data request must be 401 until a valid
  read token is supplied. Inspect storage, cookies, URLs and requests: the
  token exists only in page memory and never appears in a URL, cookie,
  local/session storage or embedded asset. CSP must omit `unsafe-inline`; inject
  `<img src=x onerror=...>` into every displayed stored/provider text field and
  verify no script executes. Search assets and network traffic for mutation
  requests and controls: M8B must expose no approve/reject/halt/resume/place/
  cancel/replace action. Page through `GET /operations?limit=2` while inserting
  newer rows; `(ts,id)` pagination must produce no duplicate or skipped older
  row, invalid status/cursor/limit must be 400, and a huge positive limit must
  be clamped to 100. For the Live MCP Tool Lab, enumerate the server catalog:
  it must contain exactly the reviewed 34 no-state-change tools and reject all
  15 mutation tools before provider invocation. Attempt an account override,
  unknown argument, oversized body and malformed JSON; each must fail before
  the MCP call. Query results must be bounded, decoded, account/secret-redacted
  and re-encoded rather than transport-pass-through; full account ids, tokens,
  raw transport payloads and secret paths must not appear in HTML, API responses
  or browser diagnostics. Every successful lab query emits only tool name and
  authenticated subject to the audit log, never arguments or result data.

## Suppression list — known, tracked, NOT findings

These are scheduled work in the plan index and its phase files; verify they
behave as currently documented, but do not report them as discoveries:

M1 through M2.7 plus M8A/M8B have landed: Class-A behavior, dual-ledger
counters, exact risk, mode/auth, account binding, the kill switch, migrations,
DB deadlines, idempotency and production-read/cockpit boundaries are audit
targets, not suppressed findings.

1. day_state open_risk/pnl are 0; breakers never trip (PLAN M3A/M3C).
2. `orders`/`fills` tables are never written (PLAN M2.9).
3. Approved Class-C ops are not executed (PLAN M4).

Suppression is about the CURRENT build, not the plan: each item above is still
the live behavior. When its milestone lands, the item leaves this list and
becomes an audit target.

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
