# Alpheus Agent Platform Plan Index

> Status: **LEAN V1 FROZEN — AP0 NON-MONEY ACCEPTED; AP1-1 CONTRACTS COMPLETE,
> AP1 NOT ACCEPTED; M11
> CANARY DEFERRED; AP13+ withheld pending M11 landing**
>
> Relationship to the frozen plan: this directory covers post-M11 Agent
> Platform work. It does not amend the M1-M11 trading-kernel specifications in
> `docs/plan/`.

This is the entrypoint for the next Alpheus planning cycle. The intended mature
product is policy-bounded autonomous trading: qualified ordinary orders do not
require per-trade human confirmation, while humans retain absolute-limit,
material-promotion, rollout, and emergency authority. A module becomes an
implementation target only after its specification, threat model, acceptance
criteria, dependencies, and rollout boundary have been reviewed and frozen.

## AI reading order

1. Read this index.
2. Read [`LEAN_V1_AMENDMENT.md`](LEAN_V1_AMENDMENT.md) before topology,
   deployment, Control Plane, collaboration, GRACE intake, Delegation freshness,
   Evidence/Memory persistence, contracts or implementation-roadmap work.
3. Read only the module file relevant to the current discussion or task.
4. Read `GRACE.md` when work concerns delayed behavior evaluation, credibility,
   real outcomes, attribution, or Post Mortem boundaries. Then read
   `GRACE_QUANTITATIVE.md` for schemas, scoring rules, statistical models,
   ratings, maturity, and model validation. Read
   [`GRACE_MIXED_CONTROL.md`](GRACE_MIXED_CONTROL.md) when a human/external
   action shares an order, position, or economic outcome with an Agent.
5. Read `DELEGATION.md` and then `DELEGATION_POLICY.md` when work concerns
   autonomous authority, GRACE eligibility mapping, capability templates,
   grants, human confirmation interaction, budgets, or Kernel authorization.
6. Read `SYSTEM_BOUNDARIES.md` before changing ownership, persistence, events,
   cross-module APIs, Provider access, or failure behavior.
7. Read `BUILD_ROADMAP.md` before planning implementation, schemas, services,
   migrations, rollout, or milestone acceptance.
8. Read `FINAL_ARCHITECTURE_AUDIT.md` before claiming AP0 or any later stage is
   authorized.
9. Read the frozen Kernel plan, including
   [`../plan/06_POLICY_OWNERSHIP.md`](../plan/06_POLICY_OWNERSHIP.md), when
   defining or implementing a Kernel/policy interface. Agent architecture
   cannot silently amend it.
10. Read
    [`../plan/08_DEFERRED_CANARY.md`](../plan/08_DEFERRED_CANARY.md) before
    changing AP0 entry, M11 status, deployment effect ceilings, or AP13+ gates.

`FROZEN ARCHITECTURE` records an agreed ownership or mechanism boundary. It does
not authorize implementation until the module's detailed specification, threat
model, acceptance probes, dependencies, and rollout are separately frozen.

## Current module status

| Module | Status | File |
|---|---|---|
| Agent Platform implementation tracker | AP0-1 through AP0-6 accepted; AP1-2 durable foundations are in place; the read-only Cockpit -> Kernel -> Scout preview landed at `dd55a30`, while durable AP1 Worker wiring remains next and all effects remain closed | [`IMPLEMENTATION_STATUS.md`](IMPLEMENTATION_STATUS.md) |
| Lean v1 cross-module amendment | Frozen; owner accepted 2026-07-19; authorizes non-money AP0 only | [`LEAN_V1_AMENDMENT.md`](LEAN_V1_AMENDMENT.md) |
| GRACE architecture | Architecture frozen; no authorization effect | [`GRACE.md`](GRACE.md) |
| GRACE quantitative evaluation | Draft written; independent model-risk review, exact machine schemas, Calibration Pack, and implementation authorization required | [`GRACE_QUANTITATIVE.md`](GRACE_QUANTITATIVE.md) |
| GRACE mixed-control attribution | Architecture frozen; B0/AP8 evidence bindings and AP9 quantitative/model-risk acceptance required | [`GRACE_MIXED_CONTROL.md`](GRACE_MIXED_CONTROL.md) |
| Delegation policy and risk authorization | Architecture and exact v1 specification frozen; autonomous Live disabled pending GRACE/model-risk, machine-schema, security, fault-suite, and rollout acceptance | [`DELEGATION.md`](DELEGATION.md), [`DELEGATION_POLICY.md`](DELEGATION_POLICY.md) |
| Complete module graph and trust boundaries | Architecture frozen; exact transport, persistence roles, cross-module schemas, and probes required | [`SYSTEM_BOUNDARIES.md`](SYSTEM_BOUNDARIES.md) |
| Durable Agent Runtime | Architecture frozen; AP1-1 machine contracts and state machines complete; persistence, execution and acceptance remain | [`RUNTIME.md`](RUNTIME.md) |
| User query, Intent, interruption, and confirmation | Architecture frozen; trading money-confirmation subset frozen by Delegation; remaining schemas and UI transport required | [`USER_INPUT.md`](USER_INPUT.md), [`DELEGATION_POLICY.md`](DELEGATION_POLICY.md) |
| Skills, Tools, and Capability Registry | Architecture frozen; metadata, taxonomy, Gateway, and validators required | [`SKILLS_TOOLS.md`](SKILLS_TOOLS.md) |
| Task planning and typed Agent collaboration | Architecture frozen; state schemas, Scheduler, and limits required | [`COLLABORATION.md`](COLLABORATION.md) |
| Research and data plane | Architecture frozen; Evidence schemas, Providers, source policy, and acceptance required | [`RESEARCH_DATA.md`](RESEARCH_DATA.md) |
| Multi-level memory and context management | Architecture frozen; schemas, retrieval/ranking, retention, and implementation specification required | [`MEMORY.md`](MEMORY.md) |
| Playbook and Strategy evolution | Architecture frozen; schemas, quantitative validation, Strategy Lab, and implementation specification required | [`PLAYBOOK.md`](PLAYBOOK.md) |
| Agent team and role contracts | Architecture frozen; exact Role packages, prompts, schedules, models, budgets, and implementation specification required | [`TEAM.md`](TEAM.md) |
| Agent Ops and Strategy Lab Web | Architecture frozen; exact API, permissions, read models, and implementation specification required | [`WEB.md`](WEB.md) |
| Agent Platform Build Roadmap | Frozen Lean v1 baseline through AP15; AP0 accepted with effect ceiling `none`; AP1-1 corrected contract freeze complete, AP1 not accepted | [`BUILD_ROADMAP.md`](BUILD_ROADMAP.md), [`LEAN_V1_AMENDMENT.md`](LEAN_V1_AMENDMENT.md) |
| Final cross-module architecture audit | Current for AP0; no unresolved authority, identity, ordering or fail-open finding; later gates remain closed | [`FINAL_ARCHITECTURE_AUDIT.md`](FINAL_ARCHITECTURE_AUDIT.md) |

## Planning rules

- Do not copy Tofi's team topology. Reuse only mechanisms that survive an
  Alpheus-specific review, such as durable delivery, bounded context, session
  recovery, and pull-based memory.
- The Kernel remains the only broker mutation and hard-risk enforcement
  boundary. Agent output is always untrusted intent.
- Do not promote illustrative formulas, weights, thresholds, or role counts
  from design discussion into implementation requirements.
- Agent, Role, behavior, strategy, prompt, memory, GRACE, and Delegation
  revisions are versioned and attributable. No component grades, validates, or
  authorizes itself.
- Shadow evidence and Live evidence remain separate. A human-owned absolute
  limit is never increased by Agent-, GRACE-, or Delegation-reachable paths.
- Each module must define context-growth behavior and failure behavior before
  implementation. Risk-relevant context is never silently truncated.

## Next planning work

1. Keep M11 `CANARY DEFERRED`, production read-only, and AP13+ closed under
   plan amendment v1.9.1. The real Canary will run later against the final
   applicable post-K1/B0 Kernel; no non-money artifact substitutes for it.
2. K1, B0, Lean v1, the Charter closeout, refreshed AP0 audit, and AP0-1
   through AP0-6 are complete. The accepted AP0 release is bound to source
   `6c276e9d740bf6c20b7e22b53f6ee06c131ecb7d` and manifest digest
   `0614bf7764e9d684e05e63c587f3fdf6c77c91589ce87e7d6ea5295c932d1da2`.
   AP1-1's detailed Runtime contract pack was frozen at `df73161`, its
   persistence-blocking seams were corrected at `006e623`, the exact
   OwnerPolicy/OutputContract canonical sources landed at `fef99de`, and
   reclaimed Attempt lease chronology was corrected at `d23215c`; failed
   Attempt retry budget classification was made explicit at `ce0da6e`.
   Historical AP0 certification was isolated from later-stage files at
   `714bee2`, and AP1-2's immutable/default-deny definition storage landed at
   `bce88cc`. Default-deny durable Runtime state landed at `7671762`. The first
   role-derived claim/start/heartbeat transaction slice landed at `95a1af2`.
   Durable model-call dispatch, unknown containment, reconciliation, budget
   settlement, and same-Attempt crash recovery landed at `4f3a082`; the
   atomic completion/failure and non-effect Artifact transaction landed at
   `9ea1c04`. Trusted OutputContract byte-validation receipts must land before
   downstream Artifact consumption; child-task, cancellation, and recovery
   commands remain in progress. No deployed Worker or model adapter is enabled.
3. Keep every later milestone behind its own entry gate. AP0 authorizes no
   Runtime operation emission, GRACE model, Delegation grant or Live effect.
4. Independently review `GRACE_QUANTITATIVE.md`; build representative reference
   data and a signed Calibration Pack before AP9 implementation.
5. Continue stage by stage with non-Live foundations first. GRACE and
   Delegation cannot affect autonomous Live until their independent acceptance
   boundaries pass.
