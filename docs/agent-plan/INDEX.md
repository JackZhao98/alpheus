# Alpheus Agent Platform Plan Index

> Status: **LEAN V1 REVIEW OPEN — AP0 withheld pending M11,
> K1, B0, amendment freeze, Charter closeout and refreshed audit**
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

`FROZEN ARCHITECTURE` records an agreed ownership or mechanism boundary. It does
not authorize implementation until the module's detailed specification, threat
model, acceptance probes, dependencies, and rollout are separately frozen.

## Current module status

| Module | Status | File |
|---|---|---|
| Lean v1 cross-module amendment | Proposed; owner review/freeze and refreshed audit required; AP0 remains withheld | [`LEAN_V1_AMENDMENT.md`](LEAN_V1_AMENDMENT.md) |
| GRACE architecture | Architecture frozen; no authorization effect | [`GRACE.md`](GRACE.md) |
| GRACE quantitative evaluation | Draft written; independent model-risk review, exact machine schemas, Calibration Pack, and implementation authorization required | [`GRACE_QUANTITATIVE.md`](GRACE_QUANTITATIVE.md) |
| GRACE mixed-control attribution | Architecture frozen; B0/AP8 evidence bindings and AP9 quantitative/model-risk acceptance required | [`GRACE_MIXED_CONTROL.md`](GRACE_MIXED_CONTROL.md) |
| Delegation policy and risk authorization | Architecture and exact v1 specification frozen; autonomous Live disabled pending GRACE/model-risk, machine-schema, security, fault-suite, and rollout acceptance | [`DELEGATION.md`](DELEGATION.md), [`DELEGATION_POLICY.md`](DELEGATION_POLICY.md) |
| Complete module graph and trust boundaries | Architecture frozen; exact transport, persistence roles, cross-module schemas, and probes required | [`SYSTEM_BOUNDARIES.md`](SYSTEM_BOUNDARIES.md) |
| Durable Agent Runtime | Architecture frozen; detailed state machines and implementation specification required | [`RUNTIME.md`](RUNTIME.md) |
| User query, Intent, interruption, and confirmation | Architecture frozen; trading money-confirmation subset frozen by Delegation; remaining schemas and UI transport required | [`USER_INPUT.md`](USER_INPUT.md), [`DELEGATION_POLICY.md`](DELEGATION_POLICY.md) |
| Skills, Tools, and Capability Registry | Architecture frozen; metadata, taxonomy, Gateway, and validators required | [`SKILLS_TOOLS.md`](SKILLS_TOOLS.md) |
| Task planning and typed Agent collaboration | Architecture frozen; state schemas, Scheduler, and limits required | [`COLLABORATION.md`](COLLABORATION.md) |
| Research and data plane | Architecture frozen; Evidence schemas, Providers, source policy, and acceptance required | [`RESEARCH_DATA.md`](RESEARCH_DATA.md) |
| Multi-level memory and context management | Architecture frozen; schemas, retrieval/ranking, retention, and implementation specification required | [`MEMORY.md`](MEMORY.md) |
| Playbook and Strategy evolution | Architecture frozen; schemas, quantitative validation, Strategy Lab, and implementation specification required | [`PLAYBOOK.md`](PLAYBOOK.md) |
| Agent team and role contracts | Architecture frozen; exact Role packages, prompts, schedules, models, budgets, and implementation specification required | [`TEAM.md`](TEAM.md) |
| Agent Ops and Strategy Lab Web | Architecture frozen; exact API, permissions, read models, and implementation specification required | [`WEB.md`](WEB.md) |
| Agent Platform Build Roadmap | Frozen baseline through AP15; Lean v1 proposes a smaller topology and lower ceremony without changing rollout destination; AP0 withheld | [`BUILD_ROADMAP.md`](BUILD_ROADMAP.md), [`LEAN_V1_AMENDMENT.md`](LEAN_V1_AMENDMENT.md) |
| Final cross-module architecture audit | Reopened for Lean v1 and Kernel policy ownership; historical findings remain evidence, protected AP0 release record not approved | [`FINAL_ARCHITECTURE_AUDIT.md`](FINAL_ARCHITECTURE_AUDIT.md) |

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

1. Treat M11 v1.7.1 recovery/Halt commit `0913010` and v1.8.1 K0 database
   canary authority commit `d24b8b9` as landed non-money evidence; complete only
   the separately confirmed target-database bootstrap plus one-share canary and
   its stop/recovery acceptance before marking M11 `LANDED`.
2. Implement K1 Kernel policy ownership and B0 broker coexistence, then
   owner-review/freeze Lean v1. They are separate modules and may proceed
   independently after M11, but both precede AP0.
3. Land the remaining post-M11 Charter amendment and run the digest-pinned
   audit release check. Record owner acceptance plus independent review in a
   machine-verifiable signed/protected AP0 release record; implement AP0 only
   when that exact digest-bound record has decision `authorized`.
4. Independently review `GRACE_QUANTITATIVE.md`; build representative reference
   data and a signed Calibration Pack before AP9 implementation.
5. Continue stage by stage with non-Live foundations first. GRACE and
   Delegation cannot affect autonomous Live until their independent acceptance
   boundaries pass.
