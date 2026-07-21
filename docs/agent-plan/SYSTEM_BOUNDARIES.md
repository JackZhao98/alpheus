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

- Web/CLI displays read models and submits typed user requests plus
  display/confirmation receipt commands for Kernel-created exact tickets.
- It owns no trading, rating, Strategy, Memory, or workflow truth.
- Authentication proves the user/session; ordinary prose does not prove
  authorization.
- Before exact confirmation reaches Live, its receipt commands use a dedicated
  human-audience User Authority Gateway whose credential is unavailable to
  Agent Runtime/Workers. Ordinary conversational intake may remain co-located;
  human authority may not.
- Platform mode ceilings and effect kill switches are fenced governance records,
  not environment-only flags or Web state. A separate Platform Activator owns
  their transitions from authenticated owner commands and deployment evidence.

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
- Research is a peer **Research Plane**, not an Agent Control/Cortex submodule
  or a Kernel extension. Cortex owns Tool selection, capability binding, budget
  and the durable Tool-call receipt; Research owns connector execution,
  collection, normalization, evidence/archive writes, and point-in-time query.
  See [`CORTEX_RESEARCH_BOUNDARY.md`](CORTEX_RESEARCH_BOUNDARY.md).

### Knowledge and strategy governance

- Memory owns governed episodes and reusable knowledge candidates, not facts
  belonging to another authority.
- Playbook/Strategy Registry owns immutable Champion/Candidate revisions.
- Strategy Lab owns reproducible experiment manifests and results.
- Strategy Activation Controller performs validated atomic revision switches;
  an Agent or Strategy experiment cannot activate itself.

### Evaluation and delegation

- GRACE owns delayed EvaluationTickets and their state/binding events, matured
  outcomes, AtomicEvaluations, Evaluation Profiles/Contracts, rating-model and
  Calibration Pack revisions, and ScoreSnapshots. Privileged human/model-risk
  paths own Profile, Pack, and Champion activation; the Engine cannot approve
  itself.
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

### Named-plane clarification

- **Cortex** is the product name for Agent Control plus cognition: Cortex
  Control owns durable Agent workflow truth; Cortex Workers execute bounded
  Attempts only.
- **Research Plane** owns scheduled external collection and temporal evidence.
  An `as_of` result may include only observations available to the system at or
  before the requested time; source publication, observation, availability and
  ingest times remain distinct.
- **Kernel** owns trading facts and effects only. Kernel-owned Agent Lab jobs
  are a read-only MVP compatibility path and cannot become canonical Cortex
  Run/Task/Attempt/Turn/Artifact state.

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
  -> accepted GRACE intake / EvaluationTicket acknowledgement for new risk
  -> deterministic Proposal Validator
  -> active DelegationGrant -> Kernel autonomous admission
     OR Kernel non-effectful ticket -> exact human receipt -> Kernel admission
  -> Kernel hard-risk/reservation/reconciliation path
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
  -> independent validation + applicable StrategyActivationAuthority
  -> Strategy Activation Controller
```

GRACE and Strategy learning are asynchronous. Neither belongs on the broker
request/response path. A GRACE or Strategy Lab outage cannot prevent Kernel
from reconciling, cancelling, closing, or enforcing breakers.

## Canonical ownership table

| Record/fact | Write authority by record family | Other modules |
|---|---|---|
| Conversation/UserRequest | ordinary Input Gateway / Agent Control Plane intake role | Read/reference under policy; this role has no human-authority receipt credential |
| TicketDisplayReceipt/ConfirmationReceipt/conversation-receipt binding | dedicated User Authority Gateway (`user-authority-gateway`) | Web may render/submit through the gateway; ordinary Input Gateway, Agent Runtime, Workers, and CI cannot write or sign receipts |
| PlatformMode/EffectClass/KillSwitch heads, events, and activation receipts | authenticated platform owner plus fenced Platform Activator | Services and Kernel enforce the current ceiling; Web reads only |
| Run/Task/Attempt/Message/Artifact/Checkpoint | Agent Control Plane | Read/reference only |
| AgentDeploymentRevision/validation/ActiveAgentDeploymentHead | Candidate owner / independent Agent Release Validator / fenced Activator on disjoint records | Runtime reads active revision; Kernel may scoped-lock a bound head |
| BehaviorEvent and decision graph | Agent Control Plane through validated Artifact commit | GRACE consumes immutable copy/reference |
| Capability candidate/validation/ActiveCapabilityHead | Candidate owner / independent Capability Validator / fenced Activator on disjoint records | Planner/Workers read active manifest only |
| Tool call/effect/reconciliation | Tool Gateway | Agent references; Control Plane handles task state |
| Evidence/Claim/Fact/Metric/Snapshot | Evidence Store/Validator | Agents and Strategy Lab read |
| Memory item/candidate/promotion | Memory service | Agents retrieve under context policy |
| Playbook/Strategy/experiment candidate, validation, activation authority, and ActiveStrategyHead | Strategy Registry/Lab / independent Validator / policy-owner / fenced Activator on disjoint records | Agents consume active or isolated Candidate revision |
| EvaluationProfile/Contract and Calibration Pack | GRACE privileged human/model-risk path | Engine and Validator consume immutable revision |
| EvaluationTicket/TicketState/ModelBindingState | GRACE | Agent Control Plane receives intake acknowledgement; other modules read |
| MaturedBehaviorOutcome/AtomicEvaluation/ScoreSnapshot | GRACE | Delegation and Agents read permitted publication class |
| Delegation policy/template/budget revisions; proposal/ProposalStateHead; attestations/candidates; grant/ScopeHead/PoolHead/HealthLease | Delegation roles and privileged paths exactly as specified in `DELEGATION_POLICY.md` | Kernel validates; Agents read scoped status |
| OperationConfirmationTicket/TicketStateHead; OperationAuthorityBinding/charge/dispatch/reduction proof | Kernel | dedicated User Authority Gateway submits receipt commands; other modules read scoped publication |
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
2. for Agent new risk, receive accepted GRACE intake/EvaluationTicket
   acknowledgement;
3. validate proposal and authority reference;
4. commit canonical Kernel operation/reservation before broker mutation;
5. reconcile Provider outcome into Kernel truth;
6. publish facts for delayed GRACE/learning consumption.

## New-risk path

Only Decision Desk may produce Agent new-risk intent. Before Kernel receives a
candidate:

- required Evidence, Strategy, Challenge, decision graph, and BehaviorEvent
  references are committed;
- GRACE intake has accepted that BehaviorEvent and acknowledged the immutable
  EvaluationTicket/ModelBindingPlan, even when its model binding is explicitly
  bootstrap-unassigned or unsupported;
- deterministic Proposal Validator checks schema, freshness, revision and
  authority-envelope compatibility;
- proposal and authority references use stable idempotency identities.

The authority routes then diverge:

- autonomous flow presents one current scoped `DelegationGrant` to Kernel's
  admission gate;
- human-review flow presents a non-effectful candidate with
  `authority_mode=exact_confirmation`; Kernel canonicalizes it and creates the
  pending operation plus immutable OperationConfirmationTicket; only a later
  exact User Authority Gateway receipt may be atomically consumed into an
  entitlement; and
- the two routes are exclusive and never stacked or silently substituted.

Before any Provider effect, Kernel independently recomputes account state,
risk, reservations, position effect, prices, limits, fresh canonical Provider
account/buying-power, position, order and reservation facts, and breaker state,
then atomically creates the applicable authority binding, charges,
`trade_grant`, reservation, and attempt. There is no independent
`settled_cash` authority field. It does not trust Agent sizing, GRACE scores,
copied grant limits, or Web state.

Kernel amendment B0 in
[`../plan/07_BROKER_COEXISTENCE.md`](../plan/07_BROKER_COEXISTENCE.md) makes
external/manual broker facts part of that canonical state and binds every
mutation to an action-specific pre-effect observation. External origin is
preserved rather than adopted. GRACE evaluates any later shared-control
outcome under [`GRACE_MIXED_CONTROL.md`](GRACE_MIXED_CONTROL.md); neither Agent
nor GRACE may rewrite Kernel origin or broker facts.

## Risk-reducing path

Position Manager may produce close/cancel/tighten intent only for an existing
canonical position/order. Kernel derives whether the action is actually risk
reducing from current state, normalizes side/quantity, and rejects any open,
add, reverse, ambiguity release, or disguised new risk.

GRACE and autonomous Delegation cannot block a valid Kernel risk reduction.
They also cannot label an invalid action risk reducing. Human policy may still
require review for a non-urgent management action, but no missing Agent-plane
component may disable Kernel emergency safety.

For Agent-originated risk reduction, Artifact and BehaviorEvent still commit
before effect. A Ticket/GRACE outage after that commit may be repaired from the
pre-effect event and cannot trap risk. If the BehaviorEvent itself did not
commit, the Agent Artifact is not used; only a separately originated user or
Kernel emergency path may act, under its real identity and audit record.

## Strategy activation path

Strategy Lab produces Candidate artifacts only. Activation requires:

- immutable Candidate and parent/rollback revision;
- reproducible point-in-time replay and forward Shadow evidence;
- independent Challenge and Validator reproduction;
- applicable StrategyActivationAuthority: a human Strategy Owner for initial or
  material change, or a separately frozen policy-preauthorized non-widening
  parameter transition;
- atomic activation by deterministic Strategy Activation Controller.

Activation makes a revision eligible as the Active Strategy; it does not grant
autonomous Live risk. Delegation separately determines whether that exact
Strategy/Agent/Role scope has an active grant. Existing positions remain bound
to their entry revisions unless an explicit reviewed migration occurs.

## GRACE and Delegation path

Agent Runtime commits BehaviorEvent before outcome. GRACE derives the immutable
ticket and ModelBindingPlan, appends separate lifecycle/binding events, waits
for predeclared maturity/finality, constructs the outcome and AtomicEvaluation,
and publishes a scoped Snapshot class. Only a `current_authority` Snapshot
under the active approved Champion is eligible for Delegation consumption;
historical-bound, Challenger, and diagnostic classes remain non-authoritative.

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
| Required Challenger/Validator unavailable | WAIT or a no-trade PASS; no silent substitution, promotion, or exact-confirmation waiver. A human may supply a typed independent-review Artifact only when the frozen RoleContract explicitly permits that reviewer class; ordinary approval cannot replace mandatory evidence/review |
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
