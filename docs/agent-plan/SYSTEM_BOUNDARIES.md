# Agent Platform Module Graph and Trust Boundaries

> Status: **FROZEN ARCHITECTURE — authority ownership, permitted cross-module
> flows, persistence ownership, and failure isolation are authoritative. Exact
> service topology, schemas, transport, credentials, deployment layout, and
> implementation acceptance remain unspecified and are not authorized.**

## Purpose

This document closes the top-level Agent Platform graph. It prevents a later
implementation from turning an LLM, Web screen, shared database, MCP session,
Memory item, GRACE score, or Strategy experiment into an accidental authority
path.

Logical boundaries apply even when the first implementation uses one process or
one PostgreSQL deployment. Co-location does not grant shared ownership.

## Trust zones

### Presentation and user authority

- Web/CLI displays read models and submits typed user requests or exact
  confirmation tickets.
- It owns no trading, rating, Strategy, Memory, or workflow truth.
- Authentication proves the user/session; ordinary prose does not prove
  authorization.

### Agent control and cognition

- Input Gateway, Policy Resolver, Control Plane, Scheduler, and Capability
  Resolver are deterministic code.
- Intent Interpreter, Task Planner, Role Workers, and optional Advisors are LLM
  cognition components producing drafts and Artifacts only.
- Agent Runtime owns Runs, Tasks, Attempts, Messages, Artifacts, Checkpoints,
  and pre-outcome BehaviorEvents.

### Research and evidence

- Tool Gateway owns credential-scoped calls and side-effect classification.
- Evidence Store owns raw sources, Claims/Facts/Metrics, provenance,
  point-in-time Snapshots, freshness, conflicts, and coverage.
- External documents, websites, attachments, and Tool text are untrusted data.

### Knowledge and strategy governance

- Memory owns governed episodes and reusable knowledge candidates, not facts
  belonging to another authority.
- Playbook/Strategy Registry owns immutable Champion/Candidate revisions.
- Strategy Lab owns reproducible experiment manifests and results.
- Strategy Activation Controller performs validated atomic revision switches;
  an Agent or Strategy experiment cannot activate itself.

### Evaluation and delegation

- GRACE owns delayed EvaluationTickets, matured outcomes, rating-model
  revisions, and ScoreSnapshots.
- Delegation owns deterministic authorization policy, proposals, approvals,
  and scoped active grants.
- A GRACE score is not a grant, and a grant is not a Kernel execution decision.

### Money authority

- Kernel owns canonical account/position/order/operation/reservation state,
  hard risk, reconciliation, breaker state, and every broker mutation.
- Broker Provider and the production Robinhood MCP session live inside the
  Kernel trust zone.
- Agents receive Kernel-published facts and never receive Provider credentials
  or use Robinhood MCP as a normal Agent Tool.

## Logical module graph

```text
User / Web
  -> Input Gateway
  -> Intent Interpreter draft
  -> deterministic Policy Resolver
  -> Control Plane
       -> Task Planner draft
       -> Capability Resolver / Scheduler
       -> bounded Role Workers
            -> Tool Gateway -> Evidence Store
            -> Memory / Strategy references
            -> typed Artifacts + BehaviorEvents

Decision Artifact
  -> deterministic Proposal Validator
  -> exact human ticket OR active DelegationGrant
  -> Kernel hard-risk/reconciliation path
  -> Provider
  -> Robinhood

BehaviorEvent
  -> GRACE EvaluationTicket
  -> delayed real market/Kernel outcome
  -> ScoreSnapshot
  -> optional Delegation policy review

Canonical case + outcome
  -> Coach / Memory Candidate / Strategy Research
  -> Strategy Lab Candidate
  -> independent validation + human Strategy Owner
  -> Strategy Activation Controller
```

GRACE and Strategy learning are asynchronous. Neither belongs on the broker
request/response path. A GRACE or Strategy Lab outage cannot prevent Kernel
from reconciling, cancelling, closing, or enforcing breakers.

## Canonical ownership table

| Record/fact | Sole write owner | Other modules |
|---|---|---|
| Conversation/UserRequest/confirmation binding | User Input service | Read/reference under policy |
| Run/Task/Attempt/Message/Artifact/Checkpoint | Agent Control Plane | Read/reference only |
| BehaviorEvent and decision graph | Agent Control Plane through validated Artifact commit | GRACE consumes immutable copy/reference |
| Tool call/effect/reconciliation | Tool Gateway | Agent references; Control Plane handles task state |
| Evidence/Claim/Fact/Metric/Snapshot | Evidence Store/Validator | Agents and Strategy Lab read |
| Memory item/candidate/promotion | Memory service | Agents retrieve under context policy |
| Playbook/Strategy/experiment revision | Strategy Registry/Lab | Agents consume active or isolated Candidate revision |
| EvaluationTicket/MaturedBehaviorOutcome/ScoreSnapshot | GRACE | Delegation and Agents read published records |
| Delegation policy/proposal/grant | Delegation privileged path | Kernel validates; Agents read scoped status |
| Account/operation/reservation/order/fill/position/breaker | Kernel | Other modules read canonical publication |
| Broker request/effect/reconciliation | Kernel Provider | No Agent-plane access |

No module writes another owner's table merely because both use the same
database. Cross-owner changes use a validated API, command, or transactional
outbox/inbox contract.

## Cross-boundary envelope

Every durable cross-module command/event binds fields equivalent to:

```text
message/event id and schema revision
causation id, correlation id, and idempotency/dedupe identity
authenticated service/user identity and source revision
source record id, immutable digest, and owning authority
subject account/ledger/entity/Strategy/Agent scope where applicable
occurred, observed, committed, effective, and expiry times as applicable
point-in-time Snapshot and policy/model/Contract revisions
delivery, acknowledgement, supersession, and reconciliation state
```

Free-form prose may be referenced as an Artifact but cannot set identity,
authority, routing, effect class, priority, effective time, or state.

## Read-model and event direction

Modules publish owner-signed or owner-resolvable immutable records and minimal
events. Consumers build their own read models and retain the canonical owner/id
rather than copying a value into new authority.

Cross-module delivery is at-least-once with durable idempotency unless a later
specification proves a stricter transport. A consumer crash or duplicate event
cannot create a second Behavior, grant, Strategy activation, Kernel operation,
or external effect.

There is no distributed database transaction covering Agent cognition and
broker execution. Safety comes from ordered committed records:

1. commit Agent Artifact and required BehaviorEvent;
2. validate proposal and authority reference;
3. commit canonical Kernel operation/reservation before broker mutation;
4. reconcile Provider outcome into Kernel truth;
5. publish facts for delayed GRACE/learning consumption.

## New-risk path

Only Decision Desk may produce Agent new-risk intent. Before Kernel receives it:

- required Evidence, Strategy, Challenge, decision graph, and BehaviorEvent
  references are committed;
- deterministic Proposal Validator checks schema, freshness, revision and
  authority-envelope compatibility;
- autonomous flow references one current scoped `DelegationGrant`;
- human-review flow references one exact current confirmation ticket;
- proposal and authority references use stable idempotency identities.

Kernel then independently recomputes account state, risk, reservations,
position effect, prices, limits, settled Provider facts, and breaker state. It
does not trust Agent sizing, GRACE scores, copied grant limits, or Web state.

## Risk-reducing path

Position Manager may produce close/cancel/tighten intent only for an existing
canonical position/order. Kernel derives whether the action is actually risk
reducing from current state, normalizes side/quantity, and rejects any open,
add, reverse, ambiguity release, or disguised new risk.

GRACE and autonomous Delegation cannot block a valid Kernel risk reduction.
They also cannot label an invalid action risk reducing. Human policy may still
require review for a non-urgent management action, but no missing Agent-plane
component may disable Kernel emergency safety.

## Strategy activation path

Strategy Lab produces Candidate artifacts only. Activation requires:

- immutable Candidate and parent/rollback revision;
- reproducible point-in-time replay and forward Shadow evidence;
- independent Challenge and Validator reproduction;
- human Strategy Owner decision;
- atomic activation by deterministic Strategy Activation Controller.

Activation makes a revision eligible as the Active Strategy; it does not grant
autonomous Live risk. Delegation separately determines whether that exact
Strategy/Agent/Role scope has an active grant. Existing positions remain bound
to their entry revisions unless an explicit reviewed migration occurs.

## GRACE and Delegation path

Agent Runtime commits BehaviorEvent before outcome. GRACE derives the immutable
ticket, waits for its predeclared maturity/finality conditions, constructs a
real-outcome record, and publishes a scoped ScoreSnapshot under an approved
Champion model.

Delegation may consume the snapshot together with human-owned policy, current
grant, deployment/canary state, and Kernel-published health facts. It produces
an authorization proposal, not an effect. A privileged validated transition
creates/revokes an active grant. Kernel validates that grant on every
autonomous risk-creating path.

GRACE cannot write Delegation. Delegation cannot write GRACE. Neither writes
Kernel operation/risk facts.

## External side effects and secrets

Tool Registry classifies effects at least as read-only, Agent-internal write,
external write, and forbidden/money effect. Credentials and refresh tokens
remain inside Tool Gateway/Provider boundaries and are exposed to Workers only
as opaque capability bindings.

Broker/money effects are unavailable through the Agent Tool Gateway. Any other
external write, such as sending a message or mutating a third-party record,
requires its own typed command, idempotency/reconciliation contract, and
human/policy authorization. Installing a Skill or mentioning a Tool in a prompt
cannot widen effects.

MCP Diagnostics is an isolated operational surface. It does not reuse normal
Agent Task authority to submit production orders and cannot become a bypass
around Provider, Delegation, or Kernel.

## Failure matrix

| Failure | Required behavior |
|---|---|
| Web unavailable | No new interactive confirmation; valid pre-authorized work follows policy; Kernel safety continues |
| Agent Runtime unavailable | No new cognition/proposals; Kernel reconciliation, breaker, and risk reduction continue |
| Research/Evidence unavailable | No new evidence-dependent decision; existing Kernel truth remains available |
| Required Challenger/Validator unavailable | Wait, PASS, or human route; no silent substitution or promotion |
| GRACE unavailable/stale | No favorable new rating or authority increase; Delegation preserves/reduces only under frozen policy |
| Delegation unavailable/ambiguous | No new/expanded autonomous grant; exact human and risk-reducing paths follow separate policy |
| Strategy Lab unavailable | No learning/promotion; active immutable revision and existing-position binding remain |
| Kernel unavailable | No broker mutation, regardless of Agent/grant state |
| Provider result unknown | Kernel reconciliation latch; no speculative Agent resend or Delegation release |
| Tool external effect unknown | Tool-specific reconciliation before retry; Task cannot assume success/failure |

## Forbidden paths

- Web or browser state -> Robinhood
- Agent Worker/Skill/Tool Gateway -> Broker Provider mutation
- Robinhood MCP Diagnostics -> normal production order path
- LLM Intent/Planner/Advisor prose -> authority or state transition
- Research price -> Kernel execution truth
- Memory -> active Strategy, GRACE score, Delegation grant, or Kernel fact
- Coach/Post Mortem -> retroactive behavior/credit rewrite
- GRACE -> grant or Kernel operation
- Delegation -> GRACE score or broker effect
- Strategy Lab -> direct Live activation or grant
- shared database credentials -> cross-owner writes

## Required later specification

Before implementation, freeze service/database-role topology, cross-boundary
schemas, outbox/inbox state machines, service authentication, revision/digest
validation, read-model freshness, retention, event ordering, concurrency,
replay, and failure probes. Acceptance must demonstrate that every forbidden
path remains impossible under prompt injection, stale UI state, duplicate
delivery, worker crash, database concurrency, credential compromise, malformed
revisions, and partial service outage.
