# Phase 3 — Runtime and review

[Back to Plan Index](INDEX.md)

> Frozen specification v1.1. This phase covers M6 and M7. Progress is tracked only
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

## Milestone 7 — Authenticated review controls on the Trading Cockpit

**Context:** M8B already serves the authenticated read-only cockpit and the
paginated operations list. This milestone extends that same application; it
does not create a second console or replace the early production-data view.

**Spec:**
- Add a pending-review panel with failed checks highlighted and the kernel's
  `derived_max_risk` shown beside the proposer's declaration. Before approval,
  show quantity, kernel-derived instrument multiplier, persisted
  `approved_price_cap`, latest sane quote and the fact that execution cannot
  exceed that cap.
- Add Approve / Reject controls backed by M4, plus explicit Halt and Resume
  Breaker controls backed by M2.6/M3C. Halt requires a non-empty reason and a
  confirmation step. Do not add direct Place, Cancel or Replace controls: users
  approve operations, while the kernel owns broker effects.
- The page upgrades from a read-capable token to `ADMIN_TOKEN` only when the
  user invokes a control. Hold it in memory only; never use cookies, URLs,
  localStorage or embedded configuration. A read-only token continues to render
  every M8B panel but cannot see or invoke mutation controls.
- Operation plans, symbols, reasons and all stored/provider text remain
  untrusted: render with `textContent`, never `innerHTML`. Preserve M8B's
  restrictive CSP with no `unsafe-inline`.
- Mutating requests require an `Origin` matching the configured console
  origin. Approve/Reject send no `reviewer` field; identity is the authenticated
  token subject. Every action renders its resulting operation/event id so the
  user can follow the audit trail.
- Surface stale/unknown execution attempts and held reservations as
  non-actionable warnings. The console must never offer a button that releases
  an ambiguous reservation or retries an uncertain broker effect.

**Acceptance:** create a pending-review operation, open the M8B cockpit at a
phone-width viewport, and approve it with an admin token: M4 executes exactly
once and the audit ids appear. With no token or a read-only/runtime token,
controls are unavailable and direct mutation requests return 401. A
cross-origin POST is refused. Reject then approve is impossible; a repeated
approve is 409. Halt blocks opens while a verified close remains possible;
Resume clears only the intended breaker state. Stored
`<img src=x onerror=...>` content renders as text, executes no script and
cannot observe either token.

---


<!-- END FROZEN SPEC -->
