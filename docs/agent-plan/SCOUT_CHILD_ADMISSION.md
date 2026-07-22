# Bounded Scout child-work admission — first implementation slice

Status: **frozen implementation boundary**. This document narrows the already
frozen Runtime and Collaboration contracts; it does not activate generic AP5
roles, scheduling, search, or any Kernel-facing capability.

## Goal

Turn one explicit Cortex handoff into a durable, restart-safe collaboration
path:

```text
root Task / Intent Interpreter
  -> immutable Scout child-work request
  -> Control admission
  -> Scout child Task
  -> immutable Scout Artifact
  -> parent continuation / Decision Desk
  -> user-facing Artifact
```

The first slice exists to make the causal graph real. It must not represent a
Scout as an in-prompt label, create arbitrary children, or let a Worker
silently continue a parent after a process restart.

## Strict scope

- One fixed child capability: `cortex_scout_research_v1`.
- One parent route: an Intent Interpreter result may hand off to `scout`.
- One child per parent Task, maximum depth one, no Scout-to-child delegation.
- The same existing Worker pool executes both roles under distinct immutable
  execution bindings; no new credential is introduced.
- The child has effect ceiling `none`. It can use the already installed,
  exact-URL `research_web_fetch` Tool only under the existing Tool policy.
- The child emits a typed Scout Artifact, never a user-facing final response.
- The resumed parent executes only Decision Desk. It cannot run Intent again
  or re-authorize a second Scout request.

Out of scope: generic planner routing, Agent/Prompt/Model registry revisions,
parallel fan-out, arbitrary capabilities, Search, child cancellation,
cross-Run delegation, Kernel proposals, scoreable behavior, and AP5 release
activation.

## Durable records and ownership

`runtime_child_task_request` remains the Worker-authored immutable request.
It is not a runnable task. Cortex Control owns the following derived records:

| Record | Owner | Purpose |
|---|---|---|
| `cortex_scout_child_admission` | Cortex Control | one exact admission/rejection per child request; binds parent, child Task, request, and reason |
| `runtime_task` / `runtime_session` / task budget ledger | Cortex Control | the admitted Scout Task and its immutable execution/context inputs |
| `cortex_parent_continuation` | Cortex Control | one parent-resume identity bound to the Scout Artifact and original handoff |
| new parent Session context manifest | Cortex Control | immutable Desk-only continuation context containing the Scout Artifact reference |

Research Gateway continues to own normalized Evidence and Tool receipts. The
Worker receives only a Control-bound Artifact/Evidence reference; it never
writes a Research record or reads a Research credential.

## State machine

1. Intent resolves and persists an immutable `handoff_to_scout` tied to its
   committed ModelCall result. Its source Attempt must still hold a live lease.
2. The Worker submits exactly one `runtime_child_task_request`, with the
   source result and handoff as input references. A duplicate returns the same
   request identity.
3. Cortex Control validates the fixed capability, the current owner/runtime
   policy, maximum depth/fan-out, remaining parent budgets, exact source
   lineage, and the absence of an existing conflicting admission.
4. In one Control transaction it records an `admitted` or `rejected` decision.
   Admission creates the Scout ledger, Task, Session, bindings and immutable
   context, then moves the parent `running -> waiting`. A rejection leaves the
   parent executable and gives it one stable reason code.
5. Scout claims and completes its own Task through the ordinary fenced Worker
   path. Its only output is a validated `scout_research_memo` Artifact.
6. A Control reconciler observes a completed admitted Scout Task. In one
   fenced transition it records `cortex_parent_continuation`, closes the old
   parent Session, creates a new Desk-only parent Session/context, and moves
   the parent `waiting -> ready`.
7. The resumed parent Worker reads the exact Scout Artifact from its new
   context and executes Decision Desk only. Its final Artifact completes the
   original Run.

No state is inferred from logs, browser state, a Model answer, or the
existence of an Artifact without its admission/continuation record.

## Crash and duplicate rules

| Interruption | Required recovery |
|---|---|
| before child request commit | original parent Attempt may retry; no child exists |
| after request, before admission | Control idempotently admits or records the same rejection |
| after child admission, before parent parks | admission reconciler parks the exact parent or rejects stale source lease; it never creates another child |
| Scout lease loss | ordinary Task recovery retries the same Scout Task within its frozen budget |
| Scout Artifact committed, before parent resume | continuation reconciler creates exactly one continuation from that Artifact |
| parent continuation lease loss | the same Desk-only parent Task retries; it never reruns Intent or Scout |
| duplicate delivery/concurrent reconcilers | unique request/admission/continuation keys plus row locks return the already committed identity |

Tool reconciliation remains independent: an unresolved Scout Tool intent must
first pass the Tool-specific reconciler before Scout terminalization or a
continuation may treat its Evidence as available.

## Acceptance probes

The implementation must prove all of the following against PostgreSQL and the
restricted Control/Worker/Research roles:

1. one live, policy-valid Scout request creates one child Task, one child
   Session, one child ledger and one immutable admission record;
2. duplicate request/admission/continuation calls create none of those twice;
3. stale parent lease, changed source result, unknown capability, excess depth,
   fan-out, or budget each produce a durable stable denial;
4. direct tables remain denied to Worker and Research roles;
5. a parent cannot reach Desk continuation without a successful admitted
   Scout Artifact, and the Desk context cannot name any other Artifact;
6. a crash at each state boundary recovers the same child/continuation identity
   without duplicate Tool authorization; and
7. the displayed trace derives `handoff_to_scout`, child admission, Scout
   completion, parent continuation and Desk completion from these records.
