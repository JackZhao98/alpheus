# Phase 3 — Runtime and review

[Back to Plan Index](INDEX.md)

> Frozen specification v1. This phase covers M6 and M7. Progress is tracked only
> in `INDEX.md`.

<!-- BEGIN FROZEN SPEC -->

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


<!-- END FROZEN SPEC -->
