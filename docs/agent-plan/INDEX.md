# Alpheus Agent Platform Plan Index

> Status: **DRAFT — architecture discussion, not implementation authority**
>
> Relationship to the frozen plan: this directory covers post-M11 Agent
> Platform work. It does not amend the M1-M11 trading-kernel specifications in
> `docs/plan/`.

This is the entrypoint for the next Alpheus planning cycle. A module becomes an
implementation target only after its specification, threat model, acceptance
criteria, dependencies, and rollout boundary have been reviewed and frozen.

## AI reading order

1. Read this index.
2. Read only the module file relevant to the current discussion or task.
3. Read `GRACE.md` when work concerns delayed behavior evaluation, credibility,
   real outcomes, attribution, or Post Mortem boundaries. Then read
   `GRACE_QUANTITATIVE.md` for schemas, scoring rules, statistical models,
   ratings, maturity, and model validation.
4. Read `DELEGATION.md` when work concerns autonomous authority, score-to-tier
   mapping, grants, human confirmation interaction, or Kernel authorization.
5. Read `SYSTEM_BOUNDARIES.md` before changing ownership, persistence, events,
   cross-module APIs, Provider access, or failure behavior.
6. Read the frozen Kernel plan only when defining or implementing a Kernel
   interface. Agent architecture cannot silently amend it.

`FROZEN ARCHITECTURE` records an agreed ownership or mechanism boundary. It does
not authorize implementation until the module's detailed specification, threat
model, acceptance probes, dependencies, and rollout are separately frozen.

## Current module status

| Module | Status | File |
|---|---|---|
| GRACE architecture | Architecture frozen; no authorization effect | [`GRACE.md`](GRACE.md) |
| GRACE quantitative evaluation | Draft written; independent model-risk review, exact machine schemas, Calibration Pack, and implementation authorization required | [`GRACE_QUANTITATIVE.md`](GRACE_QUANTITATIVE.md) |
| Delegation policy and risk authorization | Architecture frozen; exact policy, grants, human override, Kernel Gate, and rollout specification required | [`DELEGATION.md`](DELEGATION.md) |
| Complete module graph and trust boundaries | Architecture frozen; exact transport, persistence roles, cross-module schemas, and probes required | [`SYSTEM_BOUNDARIES.md`](SYSTEM_BOUNDARIES.md) |
| Durable Agent Runtime | Architecture frozen; detailed state machines and implementation specification required | [`RUNTIME.md`](RUNTIME.md) |
| User query, Intent, interruption, and confirmation | Architecture frozen; schemas and UI transport required | [`USER_INPUT.md`](USER_INPUT.md) |
| Skills, Tools, and Capability Registry | Architecture frozen; metadata, taxonomy, Gateway, and validators required | [`SKILLS_TOOLS.md`](SKILLS_TOOLS.md) |
| Task planning and typed Agent collaboration | Architecture frozen; state schemas, Scheduler, and limits required | [`COLLABORATION.md`](COLLABORATION.md) |
| Research and data plane | Architecture frozen; Evidence schemas, Providers, source policy, and acceptance required | [`RESEARCH_DATA.md`](RESEARCH_DATA.md) |
| Multi-level memory and context management | Architecture frozen; schemas, retrieval/ranking, retention, and implementation specification required | [`MEMORY.md`](MEMORY.md) |
| Playbook and Strategy evolution | Architecture frozen; schemas, quantitative validation, Strategy Lab, and implementation specification required | [`PLAYBOOK.md`](PLAYBOOK.md) |
| Agent team and role contracts | Architecture frozen; exact Role packages, prompts, schedules, models, budgets, and implementation specification required | [`TEAM.md`](TEAM.md) |
| Agent Ops and Strategy Lab Web | Architecture frozen; exact API, permissions, read models, and implementation specification required | [`WEB.md`](WEB.md) |

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

1. Independently review `GRACE_QUANTITATIVE.md`; build representative reference
   data and a signed Calibration Pack before authorizing implementation.
2. Freeze Delegation's exact policy/grant/Kernel Gate specification against the
   proposed non-compensatory `ScoreSnapshot` contract.
3. Derive detailed state schemas, threat models, milestones, rollout gates, and
   acceptance probes only after the architecture closes.
4. Run one final cross-module architecture audit before authorizing code work.
