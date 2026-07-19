# Agent Platform Final Cross-Module Architecture Audit

> Audit date: **2026-07-18**
>
> Historical audit input baseline: `d8c977ccdd86b28550da526a56ec010cdf63adac`
> Historical corrected architecture candidate:
> `aa4df069e979a119782224ec2a488f942f0dcff6`
> (current Lean/policy amendments require new digests after freeze)
>
> Result: **HISTORICAL AUDIT — REVIEW REOPENED FOR LEAN V1 AND KERNEL POLICY**
>
> Authorization: **AP0 RELEASE WITHHELD**

## 1. Decision

This document records the previous full-topology audit. The product destination
and closed safety findings remain useful evidence, but its final topology,
synchronous GRACE acknowledgement, rolling lease and release verdict are under
re-review by [`LEAN_V1_AMENDMENT.md`](LEAN_V1_AMENDMENT.md) and Kernel policy
amendment [`../plan/06_POLICY_OWNERSHIP.md`](../plan/06_POLICY_OWNERSHIP.md).
It cannot issue AP0 authorization until that amendment is frozen and this audit
is refreshed against new digests.

The corrected Agent Platform architecture is coherent around one intended
product destination:

> Alpheus is a governed autonomous trading system. Once an exact scope has
> qualified through Strategy validation, GRACE, Delegation, rollout, and Kernel
> gates, ordinary eligible trades execute without per-trade human confirmation.

Human authority remains necessary for absolute limits, initial and material
Strategy/model/policy changes, new product/effect classes, non-preauthorized
scope widening, rollout activation, emergency stop, and exceptional unknown
effect resolution. AP13 exact confirmation is a transitional qualification and
exception route. AP14 is the mandatory first autonomous canary. AP15 is scoped
autonomous production.

The previous audit found no document-level path by which an Agent, prompt,
Skill, Tool, Web client, Memory item, research source, Strategy experiment,
GRACE score, or Delegation proposal can directly mutate a broker or manufacture
Kernel permission. The architecture findings discovered during this audit were
closed in the same document revision.

AP0 is nevertheless not authorized. The Kernel clock blocker is closed, its
complete certification is green, and M11 v1.7.1 recovery/Halt landed in
`0913010`, while v1.8.1 K0 database canary authority landed in `d24b8b9`;
however, the separately confirmed canary stop/recovery evidence, K1, B0 broker
coexistence, Lean v1 freeze and the post-M11 Charter/audit closeout remain
incomplete. These are hard release gates, not optional follow-up work.

## 2. Scope and method

The audit covered:

- every file in this Agent Platform plan;
- the frozen Kernel Charter, plan index, M11 phase, and provider-gap evidence;
- write ownership, service credentials, authenticated origin, and record
  activation;
- identity, revision, digest, freshness, units, time, and canonicalization;
- outbox/inbox delivery, retries, dedupe, cancellation, supersession, crash
  recovery, unknown external effects, and rollback;
- User Input, Web, Capability, Tool, Evidence, collaboration, Memory, Strategy,
  GRACE, Delegation, Kernel, Provider, and BlobStore boundaries;
- AP0-AP15 dependencies, certification, canary isolation, and steady-state
  autonomy; and
- current repository status and the Kernel closure/recertification evidence
  described in section 6.

Three independent review passes challenged authority/security, schema/failure
semantics, and roadmap/release gates. No Live mode, production credential, MCP
mutation, broker call, or real-money operation was used by this audit.

## 3. Human and machine responsibility

| Decision/effect | Normal owner after AP15 | Human interaction |
|---|---|---|
| Research, planning, challenge, WAIT/PASS/PROPOSE | Versioned Agents under deterministic Control Plane | On ambiguity or explicit user query, not every Run |
| Ordinary qualified Class-B order | Delegation grant plus Kernel Gate | No per-trade confirmation |
| Exact Class-C exception | Kernel exact-confirmation ticket | One exact fresh receipt; never a reusable grant |
| Risk reduction | Kernel-proven canonical position/order effect | No Agent-plane dependency; emergency user/Kernel origin remains separate |
| GRACE evaluation | Frozen Champion Engine plus independent validation lifecycle | Model-risk approval for Champion changes, not each score |
| Equal-or-narrower grant replacement | Frozen policy plus independent Validator and AutomaticNarrowingAuthorization | No fresh click when explicitly preauthorized |
| First grant or wider authority | Delegation validation and privileged activation | Human policy-owner decision |
| Initial/material Strategy change | Independent Strategy Validator and fenced Activator | Human Strategy Owner decision |
| Future preauthorized parameter-only non-widening Strategy change | Separately frozen policy, Validator, and Activator | May be automatic; cannot widen authority |
| Absolute limit, new product/effect, production mode increase | Human-owned policy and fenced platform/Kernel activation | Always explicit |
| Unknown broker effect or incident | Kernel latch and canonical reconciliation | Human only where deterministic recovery cannot resolve it |

Human absence never creates a permissive default. It also is not a normal
per-order dependency for an already valid autonomous scope.

## 4. End-to-end write and authority trace

| Path | Canonical writer/identity | Authority boundary | Fail-closed result |
|---|---|---|---|
| User input | Input Gateway writes raw UserRequest; LLM writes only IntentDraft | Deterministic Policy Resolver; exact human authority uses a separate audience | Ambiguity waits; prose cannot become authority |
| Schedule/event/recovery | Control Plane writes TriggerOccurrence and Run with one RunOrigin | EffectiveRunAuthority derives from registered owner policy and workload identity | No fake UserRequest, human token, or fresh authority on recovery |
| Task and Artifact | Control Plane owns state; Worker returns validated candidate output | Artifact is untrusted; AP8 atomically adds Agent Control-owned BehaviorEvent | Duplicate/retry retains causal identity; no provisional scoreable record |
| Skill/Tool | Candidate, Validator, activation decision, and ActiveCapabilityHead have disjoint writers | Gateway intersects active capability, Skill, Run, principal, mode, and health | AP3 external calls are read-only; no production Robinhood MCP credential |
| Evidence | Connector preserves raw BlobRef; Evidence Store owns transformations and point-in-time facts | Research facts never satisfy Kernel execution gates | Stale/conflicting/missing data stays explicit and blocks dependent decisions |
| Agent release | Candidate author, release Validator, and ActiveAgentDeploymentHead Activator are separate | Delegation lease and Kernel bind the active head | Runtime cannot deploy its own prompt/model/Role revision |
| Strategy | Lab writes Candidate; Validator attests; authority owner decides; Activator CASes ActiveStrategyHead | Activation selects a decision revision but grants no money authority | Self-promotion, material inheritance, and head races fail closed |
| Behavior/Outcome | Agent Control owns BehaviorEvent; GRACE Intake owns Ticket and fenced Outcome revisions | New risk waits for exact accepted Ticket acknowledgement | Late/selective registration and concurrent correction cannot win silently |
| GRACE | Engine, Validator, model-risk, and Activator write disjoint records | ScoreSnapshot is evidence only; immutable model body is separate from head/events | Missing/stale/invalid model or data cannot produce favorable authority evidence |
| Delegation | Engine proposes; Validator attests; applicable authority approves; Activator installs | Grant is scoped evidence-backed permission, not execution | Missing/mixed ActivationAuthority, stale heads, or widening by auto path denies |
| Exact confirmation | User Authority Gateway owns receipt candidates; Kernel owns ticket/head/use | Kernel consumes one exact current receipt after re-gating | Duplicate, stale, changed, forged, or ambiguous receipt is inert/denied |
| Autonomous admission | Decision Artifact plus Ticket acknowledgement plus one current grant | Kernel locks source/scope/pool and its own risk/reservation state | Any mismatch/unknown denies new risk before attempt/send |
| Broker effect | Kernel owns operation, binding, charge, grant, reservation, attempt, order/fill, and Provider | Stable attempt and send fence commit before Provider call | Unknown latches; canonical pull/reconciliation; no blind resend |
| Web/Diagnostics | Web writes no truth; typed commands target owning APIs | Browser has no DB, broker, activation, or production MCP secret | Stale/unknown UI cannot confirm, activate, or infer success |
| BlobStore | Artifact Store owns staged/committed bytes and BlobRef metadata | Every read enforces current principal, owning reference, ACL, and retention | Digest knowledge is not access; authoritative reachable blobs are not GC'd |

## 5. Architecture findings closed in this revision

### A-01 — Autonomous product destination

The roadmap previously ended at an optional one-symbol canary. AP14 is now a
mandatory canary, AP15 defines scoped autonomous production, and
`live_autonomous` is an explicit platform ceiling. AP13 is documented as a
transitional/fallback route rather than the product's steady state.

### A-02 — Truthful Run origin

The common contract now requires a discriminated RunOrigin for user, schedule,
Kernel event, external event, maintenance, or recovery origin. It propagates to
Artifacts, BehaviorEvents, GRACE, Delegation, and Kernel. Scheduled work uses
registered owner policy and workload identity, not a fabricated user session.

### A-03 — Record-level write authority

The invalid rule of one writer for an entire logical schema was replaced by one
write authority per record family/transition. Engine, Validator, owner-decision,
Workflow, and Activator roles may coexist only with mutually non-overlapping
grants and no catch-all schema writer.

### A-04 — Missing validation and activation boundaries

The topology now names Capability, Agent release, Strategy, GRACE, Delegation,
Platform, and human-confirmation validation/activation identities. Candidate
authors and Workers do not hold their Activator credentials. Kernel locks
authority source heads only through scoped per-owner functions/equivalent
capabilities, never broad cross-schema update permission.

### A-05 — Behavior and GRACE Intake ownership

AP1 supplies only the durable Artifact publication/outbox extension and cannot
invent a provisional BehaviorEvent. At AP8, Agent Control atomically commits the
canonical BehaviorEvent with its qualifying Artifact; separately credentialed
GRACE Intake validates it and creates the GRACE-owned Ticket/ack. There is one
Behavior identity and no cross-owner transaction claim.

### A-06 — Delegation activation authority

Grant activation now requires exactly one `ActivationAuthority` variant:
HumanDelegationDecision for first/wider grants, or independently validated
AutomaticNarrowingAuthorization for an explicitly preauthorized equal-or-
narrower replacement. Missing/both/mismatched variants and automatic widening
are rejected.

### A-07 — GRACE immutable model and outcome correction

Mutable lifecycle and approvals were removed from immutable ModelRevision.
ValidatorAttestation, ModelRiskDecision, ModelStateEvent, and the fenced
ChampionHead have distinct writers. Outcome correction advances one OutcomeHead
and publishes an event; the Engine later creates evaluations in its own
transaction.

### A-08 — Robinhood and external Tool isolation

Research Gateway may consume external research and Kernel-published read
projections. It never receives the production Robinhood MCP token/session.
AP3 enables no external mutation Tool. Broker mutation remains exclusively in
Kernel Provider; any future non-broker external write needs its own separately
frozen durable attempt/reconciliation protocol.

### A-09 — BlobStore ownership and early attachment dependency

AP0 now owns the common BlobRef/staging/commit/read protocol because AP2 accepts
attachments before AP4. AP4 extends the same store. Streaming bounds, digest
verification, ACL, retained-reference GC protection, quarantine, and orphan
cleanup are explicit.

### A-10 — Platform mode and scoped rollout

PlatformModeHead, effect heads, kill switches, and ActivationReceipts are AP0
contracts with a fenced owner. The global mode is a maximum ceiling, not
authority for every scope. One canary cannot elevate unrelated scopes or remove
an eligible exact-confirmation fallback.

### A-11 — Deployment, rollback, and certification safety

The roadmap freezes additive deployment and safety-first rollback order. Routine
`certify-agent.sh`, including `all`, is permanently non-money. Any real canary
uses a separate one-shot runner, fresh activation, narrow credential audience,
exact environment/account/commit/operation/cap/expiry binding, and stable replay
identity.

### A-12 — Review outage and durable delivery

A missing required Challenger/Validator yields WAIT or no-trade PASS; ordinary
human approval cannot waive a mandatory review. Event consumer identity remains
stable across deployment, and inbox dedupe/tombstones outlive the producer's
maximum replay horizon.

## 6. Release blockers and closure status

### R-01 — CLOSED: Kernel market-day clock and certification

Closed on 2026-07-18 by production repair `66b0281` and test-only PostgreSQL
query fix `d2605b9`. Security-relevant market-day decisions now use the
advancing PostgreSQL clock under the stable ledger transaction lock; the
configured market timezone is frozen once and shared by Kernel, Store,
watchdog, and provider PnL reads.

Closure evidence includes:

- the original fixed-date breaker regression passes without changing its date;
- database/process disagreement, live/shadow day split, provider-PnL and
  RecentFills midnight crossings fail closed with no broker effect;
- proposal, approval, recovery, repricer, state, and breaker-resume paths
  perform a final in-transaction market-day check;
- canonical New York windows cover winter/summer boundaries and 23/25-hour DST
  days, while malformed date/TZ windows fail before durable writes;
- breaker state, override, daily PnL, event row, and payload share the exact
  authoritative observation time; and
- `./scripts/certify-m9.sh` completed `M9 CERTIFICATION PASS` on an isolated
  PostgreSQL 16/FakeBroker project, including live/shadow barriers, smoke,
  paused-DB honesty, PostgreSQL replacement recovery, `unknown=0`, and
  `unsafe_orphans=0`.

The production Robinhood deployment remained read-only and was neither joined
nor restarted. R-01 is no longer an open release blocker.

### R-02 — M11 canary stop/recovery evidence is incomplete

The Kernel plan index still marks M11 `IN PROGRESS`. The production deployment
remains read-only, and the first Alpheus-routed one-share Live canary still needs
its separately confirmed exact ticket. Plan amendments v1.7 (`5df440c`) and
v1.8 (`4328327`) now define the missing pre-canary code gates: bounded same-ref
recovery, transactional Live admission/Halt serialization and database-
authoritative canary policy. Recovery/Halt and its non-money acceptance landed
in `0913010`; K0 database canary authority and its non-money acceptance landed
in `d24b8b9` without a production Provider call.

The target database bootstrap and read-only deployment were subsequently
completed under separate explicit owner authorization: version 10, authority
revision/generation `1/1`, `$50`/five days, no broker mutation, and zero
attempt/order/fill/current-day grant/open-risk/unknown effect. Next, under a
fresh confirmation, execute only the already specified one-share equity
canary. Halt new risk, preserve the Live recovery adapter,
reconcile/adopt/cancel or ingest every real order/fill/position/PnL fact, prove
the gate/accounting clean, and only then return deployment to `read_only`. A
real fill is never rolled back; any reduction is a new Kernel-verified effect.
Mark M11 `LANDED` only if every frozen acceptance item passes. This audit did
not authorize the target-database mutation; the later owner instruction did.
It still authorizes no real-money order.

### R-03 — Post-M11 Charter amendment is not landed

The current Charter still excludes the new Agent schemas/process profiles,
Research Gateway, Strategy Lab and additional Web scope. After M11/K1/B0 land
and Lean v1 freezes, a dedicated pre-AP0 governance commit must amend the Charter
and pin the exact deployment, credential, database, Provider and authority
boundaries from the lean plan.

### R-04 — AP0 protected release record is not approved

After R-02 and R-03 close, a narrow release check must verify the M11
evidence, Charter, roadmap, corrected architecture commit, and audit digests and
confirm no new authority/fail-open finding. An owner-signed/protected release
record plus independent architecture-review attestation must bind every verified
digest, reviewer identity, owner decision and trusted release time. CI/startup
verifies that protection and exact content; a string in Markdown has no
authority.

The implementation Agent, Worker, CI job and ordinary maintainer cannot create
the owner decision. The record opens AP0 only. After Lean v1 freeze, AP0 uses a
contract-first commit for money, authority, cross-process and public-event
boundaries; internal types may evolve in their cohesive module with tests. The
record does not authorize AP1 or later work, production activation, runtime
effects, GRACE implementation, Delegation activation or Live trading.

## 7. Intentional later gates

These are expected stage gates and do not need to block AP0 after section 6
closes:

- GRACE quantitative implementation remains blocked at AP9 pending independent
  actuarial/model-risk review, exact machine schemas, representative reference
  data, a signed Calibration Pack, and explicit approval.
- Delegation remains observe-only until AP11 acceptance and cannot affect Live
  before AP12/AP14 gates.
- AP13/AP14/AP15 each require their own non-money certification plus separately
  controlled production evidence.
- Options and every uncertified product/effect class remain disabled until a
  separate frozen Kernel/Provider plan and canary certify them.

## 8. Dependency and rollback verdict

The previous sequence remains a candidate rollout order, but Lean v1 must be
frozen and re-audited before this verdict becomes current:

```text
M11 closeout -> K1 + B0 -> Lean v1 freeze
-> pre-AP0 Charter closeout and refreshed audit/release check
-> AP0 -> AP1
-> AP2 || AP3 -> AP4 -> AP5 -> AP6 -> AP7 -> AP8
-> AP9 || AP10 -> AP11 -> AP12 -> AP13 -> AP14 -> AP15
```

AP2/AP3 and AP9/AP10 are the only declared parallel branches. No later authority
is needed to implement an earlier stage. The legacy direct Runtime proposer is
disabled before AP1 claims triggers, avoiding two schedulers/proposers.

Application/deployment rollback order is also valid after the correction: deny
new admission and send first, freeze upward activation/lease advance, preserve
reconciliation/cancel/verified reduction, drain or latch in-flight effects,
stop writers, roll back
compatible applications, and retain forward-compatible schema plus immutable
authority/audit history. It never deletes, reverses or relabels a real broker
effect; those facts are reconciled forward.

## 9. Final authorization statement

```text
ARCHITECTURE_REVIEW_REOPENED
AP0_RELEASE_STATUS: WITHHELD
```

No Agent Platform implementation should begin from this audit alone. M11
v1.7.1 recovery/Halt and v1.8.1 K0 database canary authority are committed and
pushed, and the target K0/read-only deployment is separately certified. The
one-share canary and M11 landing are next; neither this audit nor the Lean
amendment authorizes that order. K1, B0, Lean v1 freeze, Charter closeout and a
refreshed audit/release record precede AP0.
