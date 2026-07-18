# Durable Agent Runtime

> Status: **FROZEN ARCHITECTURE — persistence, recovery, context, and budget
> boundaries are authoritative. Detailed schemas, numerical limits, migration,
> and implementation acceptance are not yet specified or authorized.**

## Purpose

The Agent Runtime turns user requests, schedules, Kernel events, and durable
messages into bounded cognition work. An Agent is a persistent logical identity,
not a permanently running process. Workers and model sessions are disposable;
continuity comes from versioned state, artifacts, checkpoints, and memory.

The landed `agent-runtime` remains a useful cognition harness: it loads role
cards, assembles bounded Kernel context, invokes schema-constrained cognition,
validates output, and submits typed operation proposals. It is not yet a
durable Agent platform: its runs, wake dedupe, sessions, and sequence are
process-local and it has no durable Task, Message, checkpoint, or recovery
state.

## Frozen principles

1. Agent identity is durable and versioned; a Worker process is not identity.
2. Every unit of work is represented by a committed Task before execution.
3. Session recovery reconstructs context from durable references; it does not
   pretend an LLM process remained alive.
4. A retry preserves the same causal and idempotency identities and cannot
   create a second Kernel proposal.
5. Control Plane state changes are deterministic. LLMs propose artifacts; they
   do not mutate workflow state directly.
6. Required facts are never silently truncated to fit a model context.
7. Child work inherits the parent Run's remaining limits and cannot create new
   budget.
8. Every scoreable Artifact commits its required `BehaviorEvent` before the
   result is observable or the Artifact can influence downstream action.
9. Agent-plane credentials cannot write Kernel, GRACE, or Delegation-owned
   records.

## Runtime layers

The first implementation should remain one deployable `agent-runtime` unless
evidence requires a split, but it has two explicit internal layers:

### Control Plane

Owns deterministic orchestration:

- triggers, schedules, Runs, Tasks, dependencies, and cancellation;
- leases, heartbeats, retries, deadlines, and crash recovery;
- role, prompt, Skill, Tool, model, and Contract revision binding;
- token, cost, tool, wall-clock, concurrency, recursion, and fan-out budgets;
- inbox/outbox delivery and artifact lineage;
- admission of work to Workers.

The Control Plane is code, not an Agent. It does not form theses, select trades,
interpret market evidence, or give itself authority.

### Worker

Executes one bounded Attempt:

- accepts a frozen Task and context manifest;
- loads the selected Agent, prompt, Skill, Tool, model, and Contract revisions;
- invokes the LLM and allowlisted tools;
- validates structured output;
- commits an immutable result or an explicit failure;
- releases its lease and ends.

Workers do not own durable state, schedules, credentials, memory, permissions,
or retry policy.

## Durable identities

The detailed schema remains future work, but the model distinguishes:

- `AgentRevision`: logical role plus prompt, model policy, permissions, and
  output Contract revision;
- `Conversation`: the long-lived user-facing thread;
- `UserRequest`: one durable user input and its resolved scope;
- `Run`: one bounded workflow serving a request, schedule, or event;
- `Task`: one unit of work with typed inputs, outputs, dependencies, and budget;
- `Session`: the logical cognition history for an Agent performing a Task;
- `Attempt`: one leased execution of a Session/Task;
- `Turn`: one model or tool interaction;
- `Artifact`: immutable structured output with evidence and revision lineage;
- `BehaviorEvent`: immutable pre-outcome record for an Artifact/decision that a
  Role/Strategy evaluation Contract marks scoreable;
- `Checkpoint`: a restorable, versioned context summary plus required-fact
  manifest.

`EvaluationTicket`, `MaturedBehaviorOutcome`, and `ScoreSnapshot` are distinct
GRACE-owned identities. Runtime may create the Agent-side behavior through an
allowlisted intake/outbox contract and later reference GRACE publications; it
cannot create or mutate a ticket, matured outcome, or official score.

A Run can contain multiple Tasks and Sessions. A Conversation can contain many
UserRequests and Runs. Reusing one unbounded chat transcript for all three is
forbidden.

## Task execution and recovery

A Worker claims a Task through a durable lease and heartbeats while executing.
On process failure, the lease expires and another Worker may create a new
Attempt for the same Task. Recovery follows committed state:

- committed result exists: return or advance from that result;
- no committed result: rerun inside the same Task identity and budgets;
- external Agent-plane tool effect is uncertain: reconcile using that Tool's
  recorded idempotency/reconciliation contract before any retry;
- Kernel operation was submitted: query the canonical operation using the
  original idempotency and operation references; never create a fresh proposal
  to discover what happened.

Process-local dedupe is not sufficient for durable scheduling or delivery.

When a Worker commits a scoreable Artifact, the Artifact and its BehaviorEvent
are written atomically or through a transactional outbox with one causal and
idempotency identity. A crash cannot leave a proposal usable but its required
behavior unregistered. Retry resolves the existing registration; it cannot
create a second behavior or wait until the market outcome is visible.

## Session reconstruction

An LLM Session is reconstructed from bounded, versioned inputs:

```text
AgentRevision
+ UserRequest and Task Contract
+ active Skill revisions
+ tool capability grant
+ latest valid Checkpoint
+ unresolved messages and questions
+ required Artifact and Evidence references
+ current Kernel facts where required
```

Changing a prompt, model, Skill, output Contract, or material policy creates a
new revision boundary. The system must not claim that a materially changed
Session is the same execution context.

## Context and automatic compact

Context uses two tracks:

- **deterministic manifest:** objectives, decisions, constraints, operation and
  evidence ids, versions, unresolved questions, required facts, and current
  state;
- **LLM-compressible narrative:** conversation, working notes, explanations,
  and non-authoritative reasoning.

A Checkpoint preserves at least the current objective, completed work,
confirmed facts with sources, rejected alternatives and reasons, decisions and
decision owners, unresolved questions, active constraints, risk state, and
next Tasks. Original messages and artifacts remain archived and attributable.

Compact may summarize eligible narrative. It cannot replace or delete canonical
Kernel facts, Tool results, Evidence citations, approvals, operation ids,
policy/strategy revisions, or unresolved ambiguity. If required context cannot
fit, the Control Plane splits or stops the Task; it never silently truncates.

After recovery or compact, active `SKILL.md` entrypoints are loaded again from
their pinned revision rather than from an LLM-written summary.

## Bounded execution

Every Run and Task carries configured maximums for:

- model calls and tokens/cost;
- tool calls and external cost;
- wall-clock and idle time;
- Task count, collaboration depth, fan-out, and parallelism;
- invalid-output and infrastructure retries.

Children consume the parent budget. A role, Skill, message, or recursive plan
cannot widen it. Exhaustion produces an explicit terminal or waiting state and
an audit event, never an unbounded loop or hidden fallback.

## Persistence boundary

The initial design may use the existing PostgreSQL deployment, but Agent data
uses an independently owned schema and database role. It cannot write Kernel or
GRACE/Delegation tables. Kernel does not become the storage engine for long
Agent prose.

The frozen M1-M11 charter currently restricts `agent-runtime` application
access to the Kernel HTTP boundary. Direct ownership of an Agent schema and a
separate Research Tool Gateway therefore require an explicit post-M11 charter
amendment before implementation. This architecture records the intended
isolation but does not silently make that amendment; Robinhood, execution
market data, and every money effect remain Kernel-only under any revision.

Use normal state tables for operational queries plus an append-only transition
and audit log. Pure event sourcing is not required. Large immutable source
documents may later move to an object store without changing Artifact identity
or lineage.

## Failure containment

- Runtime unavailable: no new cognition; Kernel risk, reconciliation, exits,
  and breakers continue independently.
- Worker/model failure: fail the Attempt or retry within its existing bounds;
  do not parse unstructured fallback prose.
- Duplicate trigger/delivery: resolve to the existing durable Run or Task.
- Context overflow: compact eligible narrative, split, or stop.
- Loop/fan-out exhaustion: refuse new child Tasks and preserve completed work.
- Unknown Kernel/broker state: Runtime can report and monitor it but cannot
  clear, retry, release, or route around the Kernel latch.
- Missing required BehaviorEvent registration: the scoreable Artifact cannot
  advance to dependent decision/proposal work; recovery resolves the original
  outbox identity rather than registering after observation.

## Required later specification

Before implementation, a follow-on specification must freeze the exact Run,
Task, Session, Attempt, Turn, Artifact, Checkpoint, lease, retry, cancellation,
budget, BehaviorEvent/outbox, and transition schemas together with crash-window,
selective-registration, outcome-aware retry, and concurrency acceptance probes.
