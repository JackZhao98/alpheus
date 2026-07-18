# Agent Ops and Strategy Lab Web Contract

> Status: **FROZEN ARCHITECTURE — Web authority, command/confirmation,
> provenance, diagnostics isolation, and operator-attention boundaries are
> authoritative. Layout, visual design, navigation details, framework, route
> names, and component implementation remain deliberately unfrozen.**

## Purpose

Web is a replaceable observation and command surface. It is not a source of
Kernel, Agent, Evidence, Memory, Strategy, GRACE, or Delegation truth. A future
redesign may replace every screen without changing the backend authority and
audit contracts in this plan.

The product should help the user answer three questions:

1. What needs attention now?
2. What did the system do, and why?
3. What is the system learning, validating, or changing?

The normal experience is not a wall of Agent chat. Structured state, decisions,
Evidence, uncertainty, and actionable tickets are primary; raw A2A messages and
Tool traces are drill-down diagnostics.

## Product workspaces

The initial site has three logical workspaces. They may later be reorganized
without changing their contracts.

### Command Center

Daily operational surface:

- canonical Kernel account, position, order, reservation, breaker, and unknown
  state;
- current Agent Runs and concise material results;
- user Query input and interpreted typed scope;
- attention/confirmation queue;
- Decision Memo, opposing Evidence, unresolved unknowns, and proposal state;
- Provider and required monitoring health;
- Live/Shadow/Simulation mode and freshness.

The existing Cockpit may serve as the first shell. It is not an architectural
dependency.

### Agent Ops

Operational and diagnostic surface:

- Run -> Task -> Session/Attempt state and causal graph;
- bound Role, prompt/model, Skill, Tool, Contract, and Strategy revisions;
- Capability requirements, coverage, assignment, and unavailable/skipped
  reasons;
- budgets, leases, retries, checkpoints, pause/resume/cancel, and blockers;
- Artifact, Evidence, Message, BehaviorEvent, and evaluation references;
- Tool/MCP health, effect/reconciliation state, and audit trail;
- Registry revisions and deployment compatibility.

Raw messages, model output, and Tool payloads are hidden by default and exposed
only under appropriate sensitivity and diagnostic permissions.

### Strategy Lab

Learning and governance surface:

- Playbook/Strategy Champion, Candidate, parent, diff, and rollback revision;
- Hypothesis, Evidence, experiment, replay, Shadow, Challenge, and Validator
  manifests;
- Behavior evaluation maturity and GRACE ScoreSnapshots with components,
  uncertainty, coverage, and limitations;
- Delegation proposals/grants as a separate authority record;
- Strategy activation-authority and model-risk decisions, including explicit
  human decisions where initial/material policy requires them;
- canary, deterioration, requalification, rejection, and rollback history.

Strategy Lab cannot directly activate a Strategy, change a GRACE score, create
a Delegation grant, or call Kernel. Buttons submit typed commands to the owning
deterministic service.

## Attention queue

The default page prioritizes durable actionable objects rather than noisy
notifications. Examples include:

- exact proposal confirmation or Class C review;
- Kernel breaker, unknown broker effect, or reconciliation blocker;
- stale/missing required Evidence or Provider data;
- failed required Challenge/Validator/Position monitoring;
- paused/blocked Run awaiting human input;
- Strategy/GRACE/Delegation promotion or requalification decision;
- expiring grant, confirmation, canary, or position-management requirement.

Every item binds its owner, severity/priority policy, subject, current revision,
reason, expiry, allowed actions, and canonical target. UI sorting cannot change
backend priority or authority.

## Query and intent interaction

```text
user input
  -> deterministic gateway validation
  -> LLM IntentDraft
  -> deterministic Policy Resolver
  -> typed UserRequest / Run
  -> Artifact, Decision, or exact HumanQuestion/ConfirmationTicket
```

The UI may show the interpreted scope, mode, assumptions, missing information,
budget, and planned work before or during execution. Read-only research can run
under policy without trading confirmation. A money or other critical effect
requires exactly one Kernel-recognized structured authority binding: a current
autonomous grant, an exact human confirmation, or a Kernel-proven reduction/
emergency route as frozen in `USER_INPUT.md` and `DELEGATION.md`.
Trading and privileged authority views also follow the distinct effect classes,
ticket/receipt fields, and deadlines in `DELEGATION_POLICY.md`.

Pause, resume, correction, cancel, and supersede are typed commands against a
specific current object. Closing a browser, editing visible text, or sending a
later chat message does not rewrite committed Artifacts or cancel a Kernel
operation.

## Confirmation presentation

A confirmation view displays the canonical immutable ticket, including:

```text
account/ledger and Live/Shadow mode
instrument/product, action, side, quantity/risk, and order constraints
maximum loss/risk envelope and expiration
Strategy, Agent/Role, proposal, Evidence, GRACE, and Delegation revisions
material opposing Evidence, unresolved unknowns, and failed gates
ticket digest, current status, and what exact effect confirmation permits
```

Any material backend change invalidates the visible confirmation and requires a
new ticket. A generic `Confirm` action cannot bind multiple pending objects,
activate a general autonomous grant, or approve a different proposal.

Successful rendering asks the dedicated User Authority Gateway
(`user-authority-gateway`) to create the immutable `TicketDisplayReceipt`
defined by `DELEGATION_POLICY.md`; only after Kernel has CAS-attached that
receipt to the current TicketStateHead may the UI submit a confirmation or
rejection through that gateway. Web, ordinary Input Gateway, Agent Runtime,
Workers, and CI have no receipt-write or receipt-signing credential.
Browser-local visibility or button state is not the ticket state.

After autonomous production qualification, this screen is an exceptional one-
operation fallback and governance surface, not an ordinary per-order approval
queue. Normal qualified autonomous orders remain visible and auditable without
waiting for a browser or human receipt.

## Provenance and truth labels

Every material value or conclusion exposes an owner and `as_of`/freshness state.
The visual design may vary, but the semantic classes remain distinct:

- `LIVE / KERNEL`: canonical account, operation, order, position, and execution
  facts;
- `RESEARCH / EVIDENCE`: externally sourced point-in-time facts and Claims;
- `AGENT INTERPRETATION`: thesis, forecast, explanation, or recommendation;
- `SHADOW`: tracked non-Live decision or execution-aware estimate;
- `SIMULATION`: synthetic/test result;
- `STALE / UNKNOWN / UNRECONCILED`: unusable or limited state.

Research market data cannot be rendered as execution truth. Counterfactual
market outcomes cannot be labeled realized PnL. GRACE ScoreSnapshot,
AuthorizationProposal, DelegationGrant, OperationConfirmationTicket,
ConfirmationReceipt, breaker resume, canary revision, and unknown-effect
resolution must appear as different objects and commands.

## Read and command architecture

Web reads owner-built or owner-attributable read models. Every displayed object
keeps its canonical id, owner, revision, digest where material, and freshness.
Cached data remains labeled with its observation time and cannot support a
critical command after its underlying revision changes.

Web commands target an authenticated deterministic API with schema validation,
authorization, idempotency, CSRF/replay protection where applicable, and an
auditable result. The browser never receives broker credentials, production MCP
tokens, database write credentials, or the ability to synthesize an authority
envelope.

Live updates may use SSE, WebSocket, or polling; transport choice is not frozen.
Reconnect performs canonical state reconciliation rather than assuming every
intermediate UI event was delivered.

## Diagnostics isolation

The manual MCP/Provider test surface lives under Agent Ops Diagnostics and is
not a normal trading interface.

- read-only production queries are separately permissioned and audited;
- secrets/tokens are never displayed by default or copied into Agent context;
- mutation tests cannot reuse normal Agent authority and must follow explicit
  test/Kernel confirmation policy;
- test, Simulation, Shadow, and Live results are visually and structurally
  distinct;
- Diagnostics cannot create a Delegation grant or bypass Kernel Provider and
  reconciliation.

## Failure and stale behavior

- Backend owner unavailable: show unavailable/stale, not zero or success.
- Live stream disconnected: show last observation time and reconcile before a
  critical action.
- Command response unknown: query canonical command/object identity; do not
  create a fresh command to discover what happened.
- Multiple pending confirmations: require exact selection/binding.
- Run retry/recovery: preserve the same causal/idempotency identity and show
  Attempt history.
- Web unavailable: Kernel safety and valid pre-authorized backend work continue
  according to policy; no new interactive confirmation is inferred.
- Sensitive raw Artifact/Tool data unavailable to the viewer: show redaction
  and access state, not fabricated absence.

## Required later specification

Before implementation, freeze API/read-model schemas, authentication and role
permissions, command idempotency, confirmation UX acceptance, attention-policy
mapping, sensitive-data redaction, freshness/reconnect behavior, Diagnostics
credentials/effects, audit access, and end-to-end stale/replay/concurrency
probes. Visual layout and styling can remain iterative.
