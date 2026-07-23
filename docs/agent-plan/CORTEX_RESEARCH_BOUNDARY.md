# Cortex, Research Plane, and Kernel Boundary

> Status: **FROZEN ARCHITECTURE CLARIFICATION — adopted 2026-07-21.** This
> document resolves ownership for Agent Lab, agent collaboration, external
> research Tools, collection, evidence persistence, and point-in-time replay.
> It does not authorize an Agent effect, a Kernel operation, or a new Live
> capability.

## Named planes

Alpheus has three peer logical planes. Co-location in one repository, process,
or PostgreSQL cluster grants no shared writer authority.

```text
Kernel          trading facts, hard risk, Provider and broker effects
Cortex          Agent control, cognition, collaboration and Agent history
Research Plane  external collection, evidence, temporal archive and connectors
```

### Kernel

Kernel owns canonical account, position, order, fill, operation, reservation,
risk, reconciliation, breaker, and Provider state. It is the only broker
mutation boundary. It may publish narrowly scoped account or market facts; it
does not own a Cortex Run, Task, Attempt, Turn, Artifact, conversation,
handoff, Agent memory, or Agent Tool receipt.

### Cortex

**Cortex** is the product name for the Agent system. Its deterministic
**Cortex Control** owns UserRequest intake, Conversations, Runs, Tasks,
Attempts, Turns, Artifacts, checkpoints, handoffs, budgets, recovery,
collaboration logs, and Agent-facing read models. Cortex Workers execute one
bounded Attempt and return untrusted typed output; they do not write Kernel or
Research-owned records directly.

The static `agent-runtime` prototype is no longer deployed. Its Compose service,
Kernel `/wake` dependency and legacy query writer are retired. The source
directory remains historical reference only; production Agent work runs through
the separately deployed Cortex Control and Cortex Worker.

An Agent Lab request is a Cortex UserRequest. Its visible progress must be
derived from canonical Cortex Run/Task/Attempt/Turn/Artifact records, not from
a fabricated UI timeline. A normal research flow is expected to be explicit:

```text
Task: Intent Interpreter  -> handoff_to: Specialist | Scout | Desk | User
Turn: bounded Specialist  -> memo_to: Desk
Task: Scout               -> handoff_to: Desk | User
Task: Desk                -> handoff_to: User
```

Each completed/failed step records its real input manifest, output Artifact,
handoff target, and actual Tool-call receipts. A Worker must not claim a Tool
was used merely because a context field was present.

`agent_query_job` and `agent_query_job_trace` in the Kernel schema are now
historical audit data only. `POST /agent/query` returns 410, Kernel starts no
recovery loop, and the authenticated GET projection is retained for old rows.
They must never grow new Cortex workflow, conversation, handoff, or Tool-log
semantics.

### Research Plane

Research is a peer service consumed by Cortex, not a Cortex submodule and not
a Kernel extension. Its current connector deployable is named
`research-gateway`; the mature Research Plane owns:

- scheduled collectors (for example GEX, news, filings, and derived research
  feeds);
- connector sessions, network safety, credential isolation, normalization and
  untrusted-source quarantine;
- immutable observations/evidence, provenance, source revisions, quality and
  coverage metadata; and
- point-in-time query and replay APIs.

For every archived observation, the Plane preserves at least source publication
time when supplied, `observed_at`/`fetched_at`, `available_at`, ingest time,
source identity, content digest, schema/version, and quality state. An
`as_of=T` query may use only evidence with `available_at <= T`; later fetches
must never be backfilled into an earlier Agent run, replay, evaluation, or
strategy experiment.

GEX collection is the first Research Plane collector. Its collection schedule
is independent of whether Cortex happens to request a query. Cortex consumes a
timestamped Research snapshot or observation reference rather than treating a
live pull as historical truth.

## Tool split

Web Search and Web Fetch are Cortex-visible Agent Tools: Cortex Control binds
the active capability revision, role scope, input/output contract, budget and
the decision to call them. Their external execution belongs to the Research
Plane connector boundary.

```text
Cortex Worker proposes permitted Tool call
 -> Cortex Control validates and records Tool-call intent/receipt
 -> Research Gateway executes Search / Fetch / research connector
 -> Research Plane returns normalized Evidence and archives observation
 -> Cortex Worker consumes Evidence Artifact
```

The same split applies to Robinhood research, GEX historical/as-of lookup, and
future research sources. Research never decides an investment thesis, task
graph, handoff, strategy, score, authority, or broker effect. Cortex never
owns a connector credential, raw Provider payload, collection schedule, or
Research archive writer role.

## Required cross-plane directions

```text
Cortex -> Kernel:     typed, policy-scoped read request or later validated candidate
Kernel -> Cortex:     published canonical fact / narrow read response

Cortex -> Research:   authorized Tool request or as-of evidence query
Research -> Cortex:   normalized Evidence / observation reference / Tool receipt

Kernel <-> Research:  only separately specified read-publication or connector
                      contracts; neither obtains the other's database writer role
```

No browser, Worker, Research connector, or shared database connection may use
co-location to bypass these directions.

## Implementation rule

Before changing Agent Lab, Agent orchestration, Tool registration/execution,
research collection, evidence storage, or historical replay, read this file
alongside `SYSTEM_BOUNDARIES.md`, `RUNTIME.md`, `SKILLS_TOOLS.md`, and
`BUILD_ROADMAP.md`. Where an older MVP implementation conflicts with this
document, retain safety and historical readability but do not extend the old
ownership path.
