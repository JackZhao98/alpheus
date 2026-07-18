# Task Planning and Typed Agent Collaboration

> Status: **FROZEN ARCHITECTURE — deterministic Control Plane, capability-based
> planning, typed Task/Message/Artifact collaboration, and bounded communication
> are authoritative. Detailed state schemas, topology, limits, and
> implementation acceptance are not yet specified or authorized.**

## Control Plane is not an Agent

The Control Plane is deterministic workflow code. It validates and enacts legal
state transitions, leases, budgets, dependencies, deduplication, permissions,
and delivery. It does not understand an investment thesis or decide which
research conclusion is correct.

Semantic planning is proposed by a restricted Task Planner LLM. Assignment is
performed by a deterministic Scheduler. Data Desk coordinates research
evidence. Decision Desk synthesizes an investment decision. No Desk acts as the
Control Plane.

## Planning flow

```text
UserRequest / schedule / event
  -> IntentDraft
  -> Task Planner produces TaskGraphDraft
  -> Capability Resolver and coverage review
  -> Control Plane validates graph and budgets
  -> Scheduler binds eligible AgentRevisions
  -> Workers execute typed Tasks
```

The Task Planner specifies objectives, dependencies, required Skills and
capabilities, output Contracts, evidence requirements, independent review, and
human-input points. It requests capabilities rather than choosing a favored
Agent identity or concrete Provider.

The Control Plane rejects cycles, missing Contracts, unavailable Skills,
permission violations, duplicate work, unbounded fan-out, illegal side effects,
and plans exceeding the Run budget. The Scheduler chooses an AgentRevision from
the intersection of capability, Skill, Tool permission, independence, model,
availability, and cost requirements.

## Task, Message, and Artifact

- `Task` asks for bounded work with typed input/output and acceptance.
- `Message` coordinates delivery, questions, correction, cancellation, or
  readiness.
- `Artifact` is the immutable substantive output: evidence, analysis,
  challenge, decision, strategy, proposal, or Post Mortem.

Agents exchange Artifact references rather than copying reports through chat.
A Message carries only the coordination delta needed by its recipient.

When a Role/Strategy Contract marks an Artifact behavior scoreable, its required
`BehaviorEvent` is committed with the Artifact under `RUNTIME.md` and
`GRACE.md`. Message forwarding, summary, omission, or later supersession cannot
change the original target, confidence, horizon, benchmark, decision graph, or
evaluation identity.

## Typed A2A envelope

The detailed schema remains future work, but every durable message binds:

```text
message_id, thread_id, causation_id, correlation_id
sender AgentRevision, recipient role/queue
message type and schema revision
Task and Artifact references
evidence references
created, expiry, priority
dedupe and delivery/acknowledgement state
```

Allowlisted message types may include research requests, artifact readiness,
challenge requests, clarification requests, human questions, corrections,
cancel/supersede notices, and Kernel status changes. Free-form prose may appear
inside a typed payload but cannot set routing, identity, priority, authority, or
state.

Delivery acknowledgements, Task claims, readiness, completion, retries, and
cancellation are machine state changes. Agents do not spend LLM turns saying
`收到`, `好的`, or `我开始了`.

## Efficient communication

Artifacts contain two layers:

- a structured machine summary of claims, evidence, confidence, freshness,
  conflicts, limitations, and unresolved questions, plus target, horizon,
  benchmark, invalidation, disposition, and evaluation references where the
  behavior is scoreable;
- an addressable narrative body for full analysis, tables, and detailed
  reasoning.

Downstream Tasks receive the summary and references by default and retrieve
only required narrative sections. Updates are deltas: changed Claim ids, new or
superseded Evidence, revised confidence and reason, or an explicit
`NO_MATERIAL_CHANGE`. They do not resend the entire Artifact.

Claims and Evidence have stable ids. Challenges target a specific Claim and
cite supporting or contradicting Evidence rather than restating the full
thesis. Message size, exchange count, and retrieval are budgeted.

## Capability-aware planning

The Task Planner receives the complete compact `CapabilityManifest` from
`SKILLS_TOOLS.md`, not the full text of every Skill and Tool. Planning has two
directions:

- **demand-driven:** determine which capabilities the task requires;
- **supply-aware:** identify available capabilities that could materially
  improve coverage even when the initial plan did not name them.

Planner output first describes capability requirements and evidence goals. A
deterministic `CapabilityResolver` queries the full active registry, returns
diverse candidates across relevant capability categories, and creates a
`CoverageReport` showing covered, missing, unavailable, and considered-but-
skipped capabilities with reasons.

The goal is not to invoke every Tool. It is to prevent a useful installed
capability from remaining invisible while avoiding redundant, costly, or noisy
calls. Concrete research-source selection belongs to Data Desk; Worker-level
optional Tool discovery remains possible inside the approved Task scope.

## Dynamic child work

An Agent cannot create a permanent Agent or directly spawn an unrestricted
conversation. It may submit a typed child-Task request describing the required
capability, input references, output Contract, and reason. The Control Plane
validates it and the Scheduler selects an eligible AgentRevision.

Every child inherits the parent's remaining depth, fan-out, token, tool, cost,
time, and permission bounds. It cannot widen them through a Skill, message, or
new role name. Stable semantic dedupe and causal ancestry prevent repeated and
circular delegation.

## Disagreement and challenge

High-value decisions use a bounded pattern:

```text
Primary analysis
  -> independent challenge
  -> at most one scoped rebuttal
  -> decision synthesis
```

Unresolved material disagreement is preserved as a typed artifact and routed
to the designated decision contract or human. Majority vote, repeated prose,
or a more confident tone does not create truth or validation.

A Planner, primary analyst, Challenger, Data Desk, Decision Desk, Coach, or
GRACE Advisor cannot mark its own output independently validated.

## Human interaction

Agents create a typed `HumanQuestion` when missing information materially
changes the result. The Task enters a durable waiting state. The user's answer
binds to that question and Task under `USER_INPUT.md`; it is not broadcast into
every Agent context.

Corrections supersede affected future work without rewriting completed
Artifacts. A submitted Kernel operation remains governed by its canonical
state regardless of later Agent-plan cancellation.

## Data Desk boundary

Data Desk may coordinate the research/data portion of a Task graph: translate
evidence goals into source capabilities, select data categories/providers,
inspect coverage, and return an Evidence Bundle. It does not allocate all
non-data work, decide whether to trade, grant its own budget, validate its own
evidence, or direct Kernel/GRACE effects.

The Task Planner may be an ephemeral capability invoked per Run; it should not
become a permanently context-heavy, all-powerful manager Agent.

## Failure containment

- Duplicate Task or Message delivery resolves through durable dedupe.
- Worker failure returns the Task to its existing recovery state.
- Collaboration cycles, depth, fan-out, turn, or budget exhaustion stop new
  child work and preserve completed Artifacts.
- Missing recipient/capability produces an explicit gap, not silent rerouting.
- Conflicting Agents preserve the disagreement and route it safely.
- Expired messages cannot resume superseded Tasks.
- No collaboration path can approve a Kernel operation, activate a strategy,
  promote GRACE, create Delegation authority, or grant Tool authority.

## Required later specification

Before implementation, freeze the TaskGraph, Task, Message, Artifact, Claim,
delivery, waiting, cancellation, supersession, Scheduler, capability coverage,
child-work, disagreement, decision-graph, and BehaviorEvent linkage schemas and
state machines together with duplicate, crash, selective-omission, loop, cost,
permission, and adversarial communication probes.
