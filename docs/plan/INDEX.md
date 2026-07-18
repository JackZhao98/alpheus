# Alpheus Plan Index

> Plan version: **v1.6 — frozen baseline plus evidence amendments**
>
> Semantic baseline: commit `fa5a29e` (`docs: harden roadmap execution invariants`)
>
> Frozen on: 2026-07-16
>
> Current implementation target: **M11 — Robinhood live adapter + canary (always last)**

This is the canonical entrypoint for implementation progress and plan-file
routing. `docs/PLAN.md` exists only as a compatibility pointer.

## Freeze policy

- The milestone specifications and invariants are frozen at the semantic
  baseline above. Implementation work must not silently rewrite them.
- Normal progress updates may change only status, evidence/commit, and the
  current implementation target in this index.
- A specification amendment requires concrete new evidence: a reproducible
  test, an audit finding, an implementation impossibility, or provider
  capability evidence. Record it in the amendment log before changing a phase
  file.
- Limits and prompt content remain human-owned exactly as stated in the charter.

## AI reading order

For implementation work, read only:

1. This index.
2. [`00_CHARTER.md`](00_CHARTER.md) for global invariants and Definition of Done.
3. The **single phase file containing the current milestone**.
4. [`../AUDIT.md`](../AUDIT.md) only when adding acceptance probes or performing
   an audit.

Do not load every later phase by default. Follow cross-milestone references only
when the current phase explicitly depends on them. Implement one milestone per
PR/commit and update this index only after its acceptance criteria pass.

## Phase routing

| Phase | Milestones | Status | File |
|---|---|---|---|
| 0 — Landed baseline | M1, M2, M2.4 | Landed / historical | [`01_LANDED_BASELINE.md`](01_LANDED_BASELINE.md) |
| 1 — Safety + production parity | M2.5, M2.6, M8A, M8B, M2.7–M2.9 | Landed | [`02_SAFETY_FOUNDATION.md`](02_SAFETY_FOUNDATION.md) |
| 2 — Ledger and controls | M3A, M3C, M3D, M4, M5B | Landed | [`03_LEDGER_AND_CONTROLS.md`](03_LEDGER_AND_CONTROLS.md) |
| 3 — Runtime and review | M6, M7 | Landed | [`04_RUNTIME_AND_REVIEW.md`](04_RUNTIME_AND_REVIEW.md) |
| 4 — Pre-live and live | M9, M10, M11 | **Active: M11 (last)** | [`05_PRELIVE_AND_LIVE.md`](05_PRELIVE_AND_LIVE.md) |

## Milestone tracker

Status vocabulary: `LANDED`, `IN PROGRESS`, `NEXT`, `PENDING`, `BLOCKED`, `LAST`.

| Milestone | Status | Depends on / gate | Evidence | Phase |
|---|---|---|---|---|
| M1 | LANDED | — | `d398e16` merge | Phase 0 |
| M2 | LANDED | M1 | `b52d281` | Phase 0 |
| M2.4 | LANDED | M2 | `5889771` | Phase 0 |
| M2.5 | LANDED | M2.4 | exact-unit/risk acceptance suite + compose smoke | Phase 1 |
| M2.6 | LANDED | M2.5 | mode/auth/halt suite + container probes | Phase 1 |
| **M8A** | **LANDED** | M2.6; read capabilities only | authenticated 49-tool snapshot, exact Agentic binding, real-shape decoders, 15-second quote age, read-only startup/API and env-gated live contract pass | Phase 1 |
| **M8B** | **LANDED** | M8A offline provider boundary | embedded Cockpit, Live production display, 34-tool safe MCP Lab, race/vet/browser and independent mutation/account-override probes pass | Phase 1 |
| **M2.7** | **LANDED** | M8B | fresh/legacy/partial/checksum/concurrent migration probes; 20-way idempotency barrier; runtime response-read retry; 503 in 0.315s under a 300ms paused-DB deadline with zero FakeBroker effects; race/vet green | Phase 1 |
| **M2.8** | **LANDED** | M2.7 | fresh/legacy migration 3 and exact grant backfill; irreversible live/shadow grant caps; 20-close reservation barrier; lease fencing and three crash-window recovery cases; 1800-second TTL; timeout/unknown no-blind-retry; external symbol-lock deadline with zero broker effects; race/vet/compose smoke and read-only deployment green | Phase 1 |
| **M2.9** | **LANDED** | M2.8 | typed migrations 4–5 with M2.8 attempt backfill; one order per place attempt; stable Fake fill ids; state-machine-only transitions with rejection events; duplicate/collision and partial-close atomicity PostgreSQL probes; 2-order/2-fill zero-orphan smoke; race/vet and read-only deployment green | Phase 1 |
| **M3A** | **LANDED** | M2.9 | migration 6; stable cross-day ledger gate; atomic open reservation/fill/exposure transfer and FIFO close allocation; durable shadow paper book; entitlement and terminal-proof probes; activation backfill/rollback/idempotency; fresh-PostgreSQL suite and isolated compose smoke; Robinhood read-only upgrade with 0 orders/fills/place attempts; race/vet green | Phase 2 |
| **M3C** | **LANDED** | M3A plus M8A provider evidence | migration 7; durable FIFO cost-basis PnL with fees/partial fills/option multipliers; conservative local/provider reconciliation and divergence latch; exact daily-loss and consecutive-loss breakers; day-scoped Admin override; Cockpit breaker facts; fresh PostgreSQL suite, isolated compose smoke, race/vet; Robinhood read-only upgrade with provider PnL and 0 orders/fills/place attempts | Phase 2 |
| **M3D** | **LANDED** | M8A account and buying-power evidence; v1.4 amendment | exact provider-field fixture; provider buying power minus durable local reservations; micro-dollar and negative-capacity boundaries; no secondary funds model in types/API/Cockpit; unit/race/vet and isolated compose smoke green; Robinhood read-only upgrade reports authoritative 401.16 buying power with 0 orders/fills/place attempts | Phase 2 |
| **M4** | **LANDED** | M3D | pending-row `FOR UPDATE` plus stable ledger lock; atomic approval status/grant/open-reservation/attempt/typed-order staging; fresh absolute-gate and TTL handling; approval snapshot event; persisted-cap bound; post-commit/pre-claim recovery preserves the reviewed C entitlement beyond proposal TTL while rechecking absolutes; 20-way memory and PostgreSQL concurrency probes; rollback proof; extended Class-C compose smoke; race/vet; Robinhood read-only upgrade with review 405 and 0 orders/fills/place attempts | Phase 2 |
| **M5B** | **LANDED** | M4 | bounded half-step/tick repricer; durable cancel/query/place effects with fencing; same-reservation partial-fill transfer and one-grant proof; hard open/close price bounds; max-reprice and halt policy expiry; ambiguous-cancel hold; stale-reconciler fence; pending/uncertain crash recovery beyond original proposal TTL; memory and PostgreSQL acceptance suites; isolated Compose reprice probe and full smoke; race/vet green | Phase 2 |
| **M6** | **LANDED** | M5B | scheduled-slot occurrence ids; authenticated private `/wake`; concurrent duplicate suppression; disabled-fallback mode; audited delivery failures; health-gated runtime startup; unit/race/vet plus isolated Compose 202/duplicate/401/404 probes and full smoke green | Phase 3 |
| **M7** | **LANDED** | M6 | exact-origin Admin controls; pending-review risk/cap/quote/check display; two-step Halt and constrained breaker Resume; non-actionable uncertainty warnings; event/operation audit ids; phone-width and inert-XSS browser probes; approval/rejection concurrency state machine; Halt open-block/full-close proof; PostgreSQL race suite and isolated Compose smoke green | Phase 3 |
| **M9** | **LANDED** | M7; full pre-live certification | 96.6% risk coverage; deterministic claimed/accepted/crash/reprice fault seams; live/shadow daily, open-risk, buying-power and close-reservation barriers plus PostgreSQL advisory-lock proof; six-operation full-day idempotent replay; paused DB 503 in 3.005701s with zero effects; PostgreSQL process replacement recovery; final unknown=0 and unsafe-orphan=0; isolated race/vet/smoke green | Phase 4 |
| **M10** | **LANDED** | M9 | official Anthropic Go SDK v1.42.0; role-card-order prompt rendering; forced single-tool handwritten contract schemas; strict local decode/Validate and one retry; exact token-count budget plus per-slot caps; untrusted-context boundary; authenticated bounded telemetry event; mocked transport/startup/injection suites; race/vet, isolated Compose certification and missing-key process probe green | Phase 4 |
| **M11** | **IN PROGRESS** | M10; v1.6 equity limit contract, live-only Provider wiring and isolated no-order live startup certification complete; a separately confirmed one-share Alpheus canary remains; option mutations blocked | `319f657`; exact-symbol equity identity; whole-share limit quantity; $0.01 tick above $1 and $0.0001 at/below $1; canonical post-placement read; production constructor equity-only; fail-closed canary and immutable revision ledger; migration 0009 active/unknown latch; exact candidate matching, single same-ref replay and two-step Admin adoption; candidate fault matrix, 20-way races, PostgreSQL rollback/atomic-adoption proofs; owner-set $50 daily cap and configured five-clean-day raise threshold; fresh-volume live startup healthy with exact binding and zero operations/grants/attempts/orders/fills; historical race/vet evidence recorded, but current market-day clock regression must be repaired before landing; deployed stack remains read-only | Phase 4 |

Ordering constraints: M8A/M8B land after M2.6 so production reads inherit
fixed-point types, authentication and account binding, while all production
mutations remain M11. M2.9 must precede M3A because exposure and partial-fill
reservation updates consume durable fill records. M2.5–M2.9 remain P0.

## Progress update protocol

When a milestone lands:

1. Run every acceptance item and the charter Definition of Done.
2. Change that milestone to `LANDED` and record its commit/evidence.
3. Promote only the immediate unblocked successor to `NEXT`.
4. Update the phase status, without rewriting frozen specification text.
5. If evidence requires a plan change, add an amendment entry first.

## Amendment log

| Date | Version | Scope | Reason / evidence |
|---|---|---|---|
| 2026-07-16 | v1 | Freeze and file split only | Semantic baseline `fa5a29e`; no milestone behavior changed |
| 2026-07-16 | v1.1 | Move M8A after M2.6; add M8B | Validate provider shapes and a read-only cockpit early; production writes remain M11 |
| 2026-07-16 | v1.2 | Add M8B Live MCP Tool Lab | Human-requested inspection surface for all 34 reviewed no-state-change tools; 15 mutations remain structurally absent |
| 2026-07-17 | v1.3 | M2.8 proposal TTL and fill-dependent close release boundary | Human approved `proposal_ttl_sec: 1800`. M2.8 has no durable fill identity/order linkage because M2.9 introduces it; therefore M2.8 releases only conclusively zero-fill terminal closes, keeps any filled quantity reserved fail-closed, and defers fill decrement plus safe-orphan proof to M2.9. This removes a circular acceptance dependency without weakening the reservation invariant. |
| 2026-07-17 | v1.4 | Make provider-authoritative buying power the sole hard funds capacity | Authenticated M8A evidence on the exact bound `cash/individual`, Level-2 Agentic account shows `get_portfolio.buying_power.buying_power` as the provider's authoritative spendable amount. The human owner confirmed this is the only funds gate Alpheus needs. M3D therefore removes the redundant secondary funds model and gates required cash against provider buying power minus durable local reservations; `cash` remains informational only. |
| 2026-07-17 | v1.5 | Verify equity `ref_id` dedupe and define bounded pull-based recovery | Under explicit owner authorization, a fresh-ref $1 SPY market order queued once, an exact same-ref replay returned unknown but created no duplicate, and a fresh ref created a distinct second order. Equity recovery may therefore replay the byte-identical intent at most once, then pull a narrow order window and require an exact unique provider-visible fingerprint. A genuine post-send unknown durably latches the bound live account: all automatic mutations stop, grants/reservations remain held, and read-only pulls continue until unique transactional adoption or audited human resolution; zero exact candidates on one pull or a timeout never clears it, regardless of unrelated account order history. A sole candidate is human-gated unless audited exclusive-writer mode is active. Fresh refs are never recovery, and option mutations remain blocked pending separate evidence. |
| 2026-07-17 | v1.6 | Certify the exact equity limit precision contract and wire the equity-only live Provider | Under a separately reviewed and owner-confirmed ticket, one F share at a $13.50 GFD regular-hours limit queued once, was canonically read, then cancelled with zero fill; the identical 0.5-share ticket was definitively rejected before order creation. Provider reviews rejected $1.001 and $0.50001 while accepting $0.5001 precision, establishing a $0.01 tick above $1 and $0.0001 tick at/below $1 for Alpheus's limit shape. Exact-symbol search supplies the instrument UUID. The live adapter may therefore support only whole-share equity limits, must re-read accepted orders canonically, and must keep option mutations closed. Current production remains read-only; isolated startup certification and the first separately confirmed Alpheus-routed canary are still required before M11 lands. |
