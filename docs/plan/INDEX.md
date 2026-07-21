# Alpheus Plan Index

> Plan version: **v1.9.6 — max_risk_usd is a declared ceiling**
>
> Semantic baseline: commit `fa5a29e` (`docs: harden roadmap execution invariants`)
>
> Frozen on: 2026-07-16
>
> Current implementation target: **continue the Agent Platform MVP above the
> recertified equity-only Kernel boundary. M11 is `LANDED`; production remains
> read-only with global Halt committed. Option Live and autonomous broker
> effects remain closed, and AP13 still requires its own later-stage gates**

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
- Limits and prompt content remain human-owned. Amendment v1.8 changes their
  storage/activation authority, not their values or Agent override boundary.

## AI reading order

For implementation work, read only:

1. This index.
2. [`00_CHARTER.md`](00_CHARTER.md) for global invariants and Definition of Done.
3. [`06_POLICY_OWNERSHIP.md`](06_POLICY_OWNERSHIP.md) for any Kernel config,
   policy, grant, expiry, lease, canary or Agent activation work.
4. [`07_BROKER_COEXISTENCE.md`](07_BROKER_COEXISTENCE.md) for Provider facts,
   external/manual orders or positions, final pre-effect refresh, and broker
   coexistence work.
5. [`08_DEFERRED_CANARY.md`](08_DEFERRED_CANARY.md) for the M11 deferral,
   non-money continuation boundary, or AP13 Live gate.
6. The **single phase file containing the current milestone**.
7. [`../AUDIT.md`](../AUDIT.md) only when adding acceptance probes or performing
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
| 4 — Pre-live and live | M9, M10, M11 | **LANDED; Agent Live not activated** | [`05_PRELIVE_AND_LIVE.md`](05_PRELIVE_AND_LIVE.md) |
| X — Policy ownership | M11 K0; K1; Agent K2 | **K0 LANDED; K1 LANDED; Agent K2 with owning modules** | [`06_POLICY_OWNERSHIP.md`](06_POLICY_OWNERSHIP.md) |
| Y — Broker coexistence | B0 | **LANDED; required before AP0** | [`07_BROKER_COEXISTENCE.md`](07_BROKER_COEXISTENCE.md) |
| Z — Production evidence | M11 Canary | **LANDED; v1.9.5 recertified equity lifecycle** | [`08_DEFERRED_CANARY.md`](08_DEFERRED_CANARY.md) |

## Milestone tracker

Status vocabulary: `LANDED`, `IN PROGRESS`, `NEXT`, `PENDING`, `DEFERRED`,
`BLOCKED`, `LAST`.

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
| **M11** | **LANDED** | equity-only production Canary plus deterministic static submit/query/cancel/fill/partial-fill/expiry/restart/read-only lifecycle; options and automatic Robinhood replay remain uncertified | Market fill price-backfill fixed in `2d1b66b`. The later working-close incident had no fill or simultaneous duplicate. `776635a` fixed explicit-close churn and read-only typed-nil construction. `26a93f2` makes all open/close execution static by default, requires explicit managed Limit opt-in, binds that choice into idempotency, and records the real one-call working-sell shape. `4f54331` covers Provider working/partial/filled/cancelled/partial-cancelled/expired/rejected normalization plus exactly-once Equity Cancel and terminal read-only replay. Full/race/vet and a rebuilt `read_only` deployment are green; global Halt remains committed | Phase 4 / Z |
| **K1** | **LANDED** | M11 non-money gate; before AP0; real Canary not required; zero production broker mutation | K1A `229a77b`: typed strict policy authority. K1B-1 `be90658`: operation binding, DB-time expiry and activation barrier. K1B-2 `bb07274`: immutable downstream execution envelopes and DB-time leases. K1C `d696010`: immutable completed Live-day attestations, exact prior-revision evidence, guarded/idempotent widening, deployment CLI and Cockpit evidence. Fresh PostgreSQL/race/vet/Compose, 20-way widening and full M9 certification green; see [`../k1c_certification.md`](../k1c_certification.md) | Phase X |
| **B0** | **LANDED** | M11 non-money gate; K1 policy binding where applicable; before AP0; real Canary not required | `ac07550` immutable shared-account observations and evidence-backed origin; `4b30971` fresh action-specific pre-effect manifests bound before Live sends; `5d81818` current-policy/aggregate Provider-risk authority with stale-proposal rejection; `f4a36db` audited external opening-order cancel plus canonical external/mixed position close, no-reversal capacity, typed control episodes, split fill allocation, and non-fictional local PnL; `1622a6a` fresh automatic manual-change reconciliation with a local-state generation fence, conservative FIFO exposure adjustment, immutable uncertain-attribution episodes, stale-work invalidation, no fictional fill/PnL, signed-exposure classification, and restart/replay idempotence; `82a60ed` durable read-only coexistence projection plus API/Cockpit separation of economic exposure and Provider-origin evidence. Full PostgreSQL/race/fault/Provider-fixture certification green, including exact live/shadow 20-way caps, paused-DB zero-effect 503, PostgreSQL replacement with `unknown=0`, restart/deep-replay equality, and desktop/390px browser acceptance with no horizontal overflow | Phase Y |

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
| 2026-07-18 | v1.7 | Complete the bounded `ref_id` recovery and define canary stop-and-recovery acceptance | Code review found that same-ref replay is not atomically limited to the only persisted candidate window, the account latch permits new Live grants/reservations/pending attempts to accumulate, and the in-memory Halt check is not serialized with every background send. M11 must make the original `send_window_end` the database-time replay deadline, reject new Live effect admission before entitlement creation while the account gate is active or unknown, and serialize every Live open send authorization with the database-backed Halt cut. The existing Live Provider plus durable Halt is the recovery state; do not add a second send window, configurable replay TTL, rollback subsystem or `recovery_only` mode, and do not switch to `read_only` before adoption/cancel/reconciliation finishes. A fill is a real fact, never something software can roll back. |
| 2026-07-18 | v1.7.1 | Correct the replay observability guarantee | Commit `0913010` landed the non-money implementation. A database authorization just before `send_window_end` does not prove that Provider `created_at` remains inside the original candidate window. Replay therefore requires a certified Provider creation-latency guard within that same window, and atomically compares the bound account, canonical intent and fingerprint while consuming its one slot. FakeBroker certifies the test path; Robinhood automatic replay stays disabled until its creation-latency bound is certified. Candidate pulls, unknown latch and Admin adoption remain active. The durable sent marker is the Halt/send linearization point; Halt cleanup preserves prior fills/exposure, rejects only the unsent remainder, and integrity failures enter the same database cut. No second window, TTL setting, service, recovery mode or production order was added. |
| 2026-07-18 | v1.8 | Separate structural ceilings, deployment config, database policy and Provider facts | Runtime inspection proved the existing `live_canary_revision` is not used by the production gate, which still reads `limits.yaml`; lowering `clean_days_before_raise` is also not classified as widening. More generally, proposals and working orders do not bind the policy revision that authorized them, so restart/config widening can expand old work. Human risk/business values move to typed immutable DB revisions/heads, while code retains structural and resource ceilings, deploy config retains secrets/endpoints/timeouts/capability ceilings, and Provider data remains observed fact. K0 fixes only canary authority before the one-share M11 canary; K1 performs the general in-Kernel migration after M11 and before AP0. No Config Service or generic settings table is introduced. |
| 2026-07-18 | v1.8.1 | Make K0 widening evidence explicitly fail closed | Implementation review proved `day_open` records observation/start, not a final broker-reconciled completed day. K0 therefore permits explicit bootstrap and tightening only; `cap increase OR clean_days decrease` is classified as widening and denied. K1 owns a typed durable completed-day attestation before widening can exist. Commit `d24b8b9` lands K0 without a Config Service, generic head table, HTTP mutation path, YAML fallback or production broker call. |
| 2026-07-18 | v1.9 | Add B0 broker coexistence and pre-effect Provider facts | Live preflight found real queued orders created outside Alpheus, and the owner confirmed humans may add, reduce, sell, or cancel on the shared account. Current open-order reads are display-only, internal close exposure cannot manage a purely external position, and an external change can invalidate a previously safe proposal. B0 preserves origin without adoption, accounts aggregate Provider facts fail closed, routes later external cancel/close through Kernel, and refreshes action-specific facts immediately before effects. The controlled clean-account M11 canary may proceed first; B0 is required before AP0/autonomous Agent Live. |
| 2026-07-18 | v1.9.1 | Defer the real M11 Canary without blocking non-money work | The owner cannot run the production order now and explicitly directed development to continue. M11's code, recovery/Halt, K0, target v10 migration and read-only deployment are complete with zero money effects. K1, B0 and AP0–AP12 have no need for a production mutation when their deployment ceiling remains read-only/Shadow, so they may proceed under their existing gates. M11 remains `CANARY DEFERRED`, never `LANDED`; the real order must run against the final post-K1/B0 Kernel and remains a hard prerequisite for AP13–AP15. |
| 2026-07-19 | v1.9.2 | Freeze Lean v1 and authorize the non-money AP0 foundation | Focused implementation-readiness review found no unresolved authority, identity, ordering or fail-open conflict after K1 and B0 landed. The owner accepted Lean v1 and directed a fast closeout rather than another planning phase. The Charter now names the Agent Platform profiles and Research Gateway. A protected/digest-bound release mechanism becomes an AP0 deliverable required before AP1, not a prerequisite subsystem that must exist before AP0 can build it. This authorizes AP0 only and does not authorize Runtime operation emission, GRACE, Delegation, production activation or any Live effect. |
| 2026-07-20 | v1.9.3 | Accept the post-K1/B0 M11 production Canary | Two separately confirmed one-share SOFI tickets were routed through Alpheus under the immutable $50 authority. Fenced Halt cancelled the working limit order unfilled. A true Market order filled once at $17.09; a Provider price-backfill mismatch produced an unknown response without retry, then exact candidate adoption completed one order/fill and all accounting. The final Live gate and warnings are empty; deployment returned to read-only with global Halt committed. M11 lands without certifying options, automatic Robinhood replay or activating Agent Live. |
| 2026-07-20 | v1.9.4 | Reopen M11 after the first owner-directed working close lifecycle | A separately previewed and confirmed one-share SOFI sell limit was accepted once, but the implicit repricer cancelled and replaced it at the same $18 price three times; switching to read-only with the working order then panicked through a typed-nil FakeBroker compatibility interface. There was never more than one active sell and no fill, but v1.9.3 did not certify these interactions. Commit `776635a` stops explicit close-limit repricing and fixes read-only construction. M11 returns to `IN PROGRESS` until the minimal static-by-default execution contract and deterministic lifecycle matrix pass. AP13, options and autonomous Live remain closed. |
| 2026-07-20 | v1.9.6 | Make `max_risk_usd` a declared ceiling instead of an exact figure | The equality rule (`abs(declared - derived) > tolerance → REJECT`) fires only on price-bounded orders, where derived risk is trivial arithmetic (`limit × qty × multiplier`); for market orders derived equals declared by construction, so it never triggers. In live testing it rejected a valid one-share equity limit whose declared cap ($20) merely exceeded the derived risk ($18) — a conservative over-declaration with no safety cost. The rule now rejects only *under*-declaration (`derived - declared > tolerance`, reason `risk_exceeds_declared`), preserving the malfunction tripwire in the only direction that matters while the binding limit stays `per_trade_budget` on derived risk. The declaration remains a journal honesty signal. No change to caps, derivation, market/stop-market behavior, or the acceptance that an over-cap trade still fails `per_trade_budget`. |
| 2026-07-20 | v1.9.5 | Recertify the minimal M11 equity execution lifecycle | `26a93f2` makes static execution the default for every new and historical open/close, permits repricing only through explicit managed Limit intent, rejects managed Market orders, and binds the choice into idempotency. `4f54331` adds deterministic Provider state coverage for working, partial, filled, cancelled, partial-cancelled, expired and rejected orders; Equity Cancel mutates exactly once and a terminal replay performs no mutation. Existing durable partial-fill/fill-idempotency/reservation tests remain green, as do full/race/vet. Image `sha256:967282c46cc915729daf3b6a4bd5724a13eb01ec0d1787d1b401d258fa6012bd` started healthy in `read_only` with Live disabled. M11 relands without another money probe; Option Live, automatic Robinhood replay, AP13 activation and autonomous Live are not certified by this amendment. |
