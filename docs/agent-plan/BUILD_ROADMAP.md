# Alpheus Agent Platform Build Roadmap

> Status: **FROZEN IMPLEMENTATION SEQUENCE — code authorization pending the
> final cross-module audit and each stage gate**
>
> Scope: post-M11 Agent Platform work. This roadmap orders implementation and
> defines its contract artifacts, ownership boundaries, rollout gates, and
> acceptance interface. It does not amend the frozen Kernel plan, authorize a
> GRACE model, activate Delegation, or permit autonomous Live trading.

## 1. Decision

Alpheus will be built from the durable, non-money substrate outward:

1. finish and certify the Kernel;
2. freeze shared machine contracts and database authority;
3. replace the process-local Agent loop with a durable Control Plane;
4. expose User Input and an early read-only Agent Ops surface;
5. add governed Skills, Tools, research evidence, collaboration, memory, and
   Strategy Lab;
6. register behavior before evaluating it;
7. implement GRACE only after independent model-risk prerequisites;
8. implement exact confirmation and Delegation behind observe-only and Shadow
   gates; and
9. consider autonomous Live only as a separately approved, tightly bounded
   canary.

No milestone may use a later milestone's authority as a shortcut. In
particular, an Agent, prompt, Tool, Web command, memory, score, strategy, or
feature flag can never call a broker or manufacture trading permission.

## 2. Landed baseline

The current repository already contains:

- a Kernel with the canonical operation, risk, execution, reconciliation, and
  Provider boundaries;
- a Robinhood read path and an M11 equity-only Live Provider/canary plan;
- an `agent-runtime` process with static YAML roles, a stub or LLM cognition
  adapter, `/wake`, and periodic ticks;
- direct Runtime-to-Kernel operation proposal wiring;
- a Kernel Cockpit on port 8100 and a Runtime listener on port 8200; and
- one PostgreSQL service with Kernel migrations.

That Runtime is a useful prototype, not the target substrate. Its sequence,
dedupe, sessions, retries, budgets, and recovery are process-local. It has no
durable Run/Task/Attempt/Artifact graph and currently sends proposed operations
directly to Kernel. The implementation must retire that path before introducing
a second scheduler or proposer, otherwise one trigger could create two effects.

## 3. Hard preconditions

### 3.1 Before Agent Platform persistence or service changes

All of the following are required:

- M11 is `LANDED`, including its separately confirmed canary and rollback
  evidence;
- the Kernel remains healthy with `LIVE_TRADING_ENABLED=false` by default;
- a reviewed post-M11 Charter amendment authorizes the new schemas, roles,
  services, and Kernel interfaces named here;
- the final cross-module architecture audit has no unresolved authority,
  identity, ordering, or fail-open finding; and
- the implementation stage has an accepted Schema Freeze Pack.

Planning, fixtures, and read-only prototypes may be prepared earlier. They
cannot share production credentials, change Kernel tables, or emit operations.

### 3.2 Before any new-risk Artifact can reach Kernel

The exact Artifact and its BehaviorEvent must commit atomically, the
EvaluationTicket registration must be acknowledged under the same immutable
identity, and the operation path must carry that identity into Kernel. If
registration is missing, stale, late, or ambiguous, new-risk progression stops.
Kernel-native cancel, reconcile, ambiguity containment, and verified reduction
remain available.

### 3.3 Before GRACE implementation

The prerequisites in `GRACE_QUANTITATIVE.md` remain binding:

- independent actuarial/statistical review;
- exact GRACE machine schemas and retention plan;
- representative reference data and complete-stream feasibility analysis;
- a signed Calibration Pack for the baseline candidate;
- golden fixtures and simulation generators;
- separate Engine, Validator, and promotion credentials; and
- explicit implementation approval.

Behavior registration and outcome collection may land earlier as neutral
evidence infrastructure. They must not publish an official authority-bearing
ScoreSnapshot.

### 3.4 Before autonomous Live

Autonomous Live remains disabled until every GRACE and Delegation acceptance
boundary passes, Shadow evidence is sufficient for the exact scope, the
human-owned policy is signed and active, Kernel independently recognizes the
mode, and the owner explicitly activates one canary revision. Options remain
disabled unless a later separately frozen plan adds them.

## 4. Dependency graph

```text
M11 + Charter amendment + final architecture audit
                         |
                        AP0  shared contracts and authority scaffold
                         |
                        AP1  durable Control Plane and Worker
                         |
              +----------+-----------+
              |                      |
             AP2                    AP3
      User Input + Web       Capability + Tool Gateway
              |                      |
              |                     AP4
              |          evidence and research data plane
              |                      |
              +----------+-----------+
                         AP5  typed collaboration and Agent team
                          |
                         AP6  memory, context, checkpoint, compact
                          |
                         AP7  Playbook and Strategy Lab
                          |
                         AP8  behavior and evaluation foundation
                          |
                    +-----+-----+
                    |           |
                   AP9         AP10
            reviewed GRACE   exact confirmation
            and Validator    and reduction proof
                    |           |
                    +-----+-----+
                          |
                        AP11  Delegation observe-only
                          |
                        AP12  end-to-end Shadow
                          |
                        AP13  human-confirmed Live
                          |
                        AP14  optional autonomous Live canary
```

AP2 and AP3 may proceed in parallel after AP1. AP4 requires AP3, and AP5
requires both AP2 and AP4. AP9 and AP10 may proceed in parallel after AP8; AP11
requires both. All other arrows are hard dependencies. A milestone may expose
diagnostic read models before its downstream authority is enabled.

## 5. Initial deployable topology

Start with the smallest topology that preserves authority boundaries:

| Deployable | Owns | May call | Must never own |
|---|---|---|---|
| `kernel` | operations, risk, reservations, attempts, orders, fills, reconciliation, Provider, Kernel Gate facts | broker Provider; exact reviewed authority reads | prompts, Agent planning, GRACE scoring, grant creation |
| `agent-runtime` | Input Gateway, Control Plane, Scheduler, Workers, low-authority module APIs, Agent Ops API | typed Kernel read/propose API; Research Gateway; model adapters | broker credentials, Kernel writes, official GRACE promotion, Delegation activation |
| `agent-web` | replaceable static UI | Agent Ops and Kernel APIs through authenticated commands | database credentials, business authority |
| `research-gateway` | connector sessions, Tool execution, normalization boundary, untrusted-source quarantine | approved read-only external sources | Robinhood mutation tools, Kernel DB, grant or score writes |
| `grace-engine` | official evaluation computation after AP9 | immutable evaluation inputs | grant, policy, Kernel, or broker writes |
| `grace-validator` | independent reproduction and validation | read-only GRACE candidate data | Engine or promotion credentials |
| `grace-activator` | one fenced Champion head transition | validated signed artifacts | training or Agent credentials |
| `delegation-engine` | deterministic proposals and observe/Shadow policy decisions | official GRACE/Strategy/Kernel facts | Kernel operation or broker writes |
| `delegation-validator` | independent proposal and policy validation | immutable policy inputs | activation credentials |
| `delegation-activator` | fenced policy/grant/lease head transitions | validated signed artifacts | Agent, GRACE Engine, Kernel, or broker credentials |

The low-authority Input, Control Plane, Worker, collaboration, memory, and
Strategy Registry modules may initially ship inside one `agent-runtime`
binary to avoid premature distributed operations. They still use package
interfaces, separate database pools and roles, and cross-owner contracts rather
than cross-schema writes. Official GRACE and Delegation credentials must be
isolated into separate deployables before those records can affect policy. A
single process holding several roles is an operational convenience, not a
security boundary, and must not be used for high-authority activation.

The Web UI may be served early at port 8200, while the existing Kernel Cockpit
remains the canonical execution surface on port 8100. The two surfaces link to
one another; neither scrapes the other's HTML.

## 6. Persistence and database authority

One PostgreSQL cluster is acceptable initially. Co-location does not imply
shared ownership.

| Logical schema | Writer | Canonical records |
|---|---|---|
| existing Kernel schema | Kernel only | operations, events, journal, trade grants, reservations, attempts, orders, fills, reconciliation, tickets, authority bindings, charges, Gate decisions, reduction proofs |
| `agent_input` | Input Gateway | UserRequest, AttachmentRef, IntentDraft, PolicyResolution, HumanQuestion, AnswerReceipt, Interrupt, Supersession |
| `agent_control` | Control Plane | Trigger, Run, Task, Dependency, Session, Attempt, Turn, Artifact, Checkpoint, BudgetLedger, BehaviorEvent, outbox/inbox |
| `capability` | Capability Registry | CapabilityRevision, SkillRevision, SkillResource, ToolRevision, manifests, grants, read receipts, calls, utilization |
| `research` | Research Data Plane | source revisions, raw-document metadata, Evidence, Claim, Fact, Metric, Snapshot, plan/bundle, conflict, universe, signals |
| `memory` | Memory service | candidates, items, validations, relations, retrieval/context manifests, retention and deletion events |
| `strategy` | Strategy Registry/Lab | Playbook, Setup, Strategy Contract, hypothesis, lesson, experiment, validation, decision, active heads, position bindings |
| `grace` | privileged GRACE paths only | EvaluationTicket, Outcome, AtomicEvaluation, ScoreSnapshot, ModelRevision, Calibration Pack refs, model heads and events |
| `delegation` | privileged Delegation paths only | policy/template/pool revisions, source bindings, proposals, validations, grants, heads, leases, state events |
| `agent_ops_view` | projection service only | rebuildable, freshness-stamped Web read models |

Each schema has:

- a migration role that is unavailable to application processes;
- one narrowly scoped writer role;
- read roles granted only to named views, functions, or tables;
- an append role when a cross-owner append is explicitly part of a contract;
  and
- a Web role limited to redacted projections.

Append-only records deny `UPDATE` and `DELETE` to application roles and use
database constraints or triggers where privilege rules alone are insufficient.
Mutable state is represented by immutable bodies plus append-only events and a
fenced head/projection with an expected generation. Cross-owner foreign keys are
not used as a substitute for protocol validation; references carry immutable
identity and digest and are validated by the receiving owner.

Kernel may read and row-lock the exact Delegation heads required by the frozen
Gate protocol. It may write only Kernel-owned bindings, charges, decisions, and
effects. Delegation cannot write those records. GRACE cannot write Delegation
records. Agent code cannot write either authority domain. Cross-owner row
locking uses a narrowly scoped, reviewed database function or equivalent
capability; it does not grant Kernel broad `UPDATE` permission on
Delegation-owned heads merely to obtain `SELECT FOR UPDATE` semantics.

Large raw documents live behind a content-addressed `BlobStore` interface.
The first implementation may use a dedicated local volume; production can move
to object storage without changing document identities. PostgreSQL stores
metadata, content digest, media type, size, origin, access class, and retention
state. Raw blobs are never copied into every prompt or event.

## 7. Schema Freeze Pack

The roadmap freezes the schema inventory and the required artifact format. It
does not duplicate hundreds of field definitions from the module documents.
Before handler or migration code begins for a milestone, that milestone must
land a reviewed contract-only commit at:

```text
contracts/<domain>/v1/
  manifest.yaml
  schema/*.schema.json
  api/openapi.yaml
  events/asyncapi.yaml
  state-machines/*.yaml
  permissions/roles.sql
  retention.yaml
  canonicalization.md
  golden/valid/*.json
  golden/invalid/*.json
```

The Pack is the exact machine contract. Its manifest names:

- owner, schema revision, compatibility rule, and lifecycle state;
- every record, command, event, query, projection, and state machine;
- field units, bounds, nullability, enum behavior, and privacy class;
- immutable fields, mutable head fields, unique keys, and idempotency scope;
- source identity, digest, freshness, and temporal semantics;
- authentication audience and required capability;
- producer/consumer compatibility matrices;
- migration, replay, retention, redaction, and deletion behavior; and
- golden canonicalization and digest vectors.

SQL is canonical for database constraints. JSON Schema is canonical for
cross-boundary payload validation. OpenAPI and AsyncAPI are canonical for
synchronous and event transport. Go types must conform to those artifacts and
must not silently widen them.

Every contract change is classified:

- additive and backward compatible;
- reader-first migration;
- writer-first migration;
- breaking revision requiring dual-read or drain; or
- identity/canonicalization migration requiring explicit human review.

No breaking change is deployed by editing a record in place.

## 8. Common machine-contract profile

AP0 freezes one common profile used by every Pack.

### 8.1 Identity and references

Server-generated IDs are opaque and stable. An immutable cross-owner reference
contains:

```text
owner, record_type, record_id, schema_revision, record_digest
```

Where currentness matters it also contains:

```text
head_id, observed_generation, observed_at, freshness_deadline
```

Names, symbols, Role labels, prompt text, explanations, URLs, and model output
are never identity. Unknown owners, record types, revisions, enums, or digest
mismatches fail closed.

### 8.2 Time and market day

- persisted instants use database UTC;
- security deadlines use database time and half-open intervals;
- client or model clocks are evidence only;
- market-day identity is derived through the frozen `TZ_MARKET` rule;
- an `as_of`, evidence cutoff, observation time, and ingestion time are
  distinct fields; and
- future-dated or temporally inverted records are rejected or quarantined.

### 8.3 Exact units

Money, quantity, price, risk, probability, and duration use named unit types.
Authority-bearing arithmetic never uses binary floating point. The common Pack
must freeze decimal scale, rounding direction, overflow behavior, currency,
instrument unit, option multiplier, and aggregation basis before code uses a
field. Missing or unknown units fail closed.

The Agent Platform does not invent `settled_cash`. Account admission uses the
canonical Provider/Kernel buying-power and account facts defined by the Kernel
plan.

### 8.4 Canonical serialization and digest

AP0 must select and publish one versioned canonicalization profile with
cross-language golden vectors. Exact numeric values use representations that
cannot lose precision in JSON. Digests include schema/profile identity and
domain separation. A digest algorithm change creates a reviewed compatibility
revision; it does not silently create a second logical authority partition.

### 8.5 Commands and idempotency

A command envelope contains authenticated actor, audience, command type,
schema revision, idempotency key, request digest, causation, correlation, and
deadline. Idempotency identity is:

```text
(authenticated_actor, command_type, idempotency_key)
```

An exact retry returns the original result. Reuse with a different request
digest returns conflict. Expired, superseded, or unknown commands never become
fresh work automatically.

Actor and origin are derived from the authenticated workload or user session,
not trusted from request JSON. Credentials are audience-specific and are not
shared between Agent, Web, human-confirmation, GRACE, Delegation, diagnostics,
or Kernel paths. An Agent credential cannot claim human origin,
`kernel_verified_reduction`, or an activation class. Kernel binds the
authenticated origin into every accepted operation and authority decision.

### 8.6 Events and delivery

Owner state and its outbox event commit in one transaction. Consumers record an
inbox identity and effect in one transaction. Delivery is at least once;
the receiving durable state transition is applied at most once per declared
consumer identity. There is no claim of exactly-once distributed or external
effects. Those retain owner-specific idempotency, dispatch fences, and
unknown-effect recovery. Events carry owner sequence where ordering is
meaningful, but consumers cannot infer a global database order. A detected
sequence gap stops dependent progression until replay or explicit repair.

Poison events enter a bounded quarantine with an explicit alert and replay
procedure. They are never silently skipped. A retry retains causal identity and
does not re-run an LLM merely to reconstruct an already committed decision.

### 8.7 Input hardening

Every write endpoint enforces authenticated audience, content type, strict
decoding, body and attachment limits, unknown-field rejection, semantic
validation, stable public errors, and redacted internal diagnostics. Prompt,
source, attachment, and Tool content are untrusted data. Database driver,
constraint, connector, model, and secret text are not returned to clients.

### 8.8 Revision and activation

Definitions are immutable revisions. Effective state uses an append-only event
stream plus a single fenced head with monotonically increasing generation.
Activation requires exact expected generation, exact input digests, database
time, authorized credential, and any independent validation artifact.
Concurrent winners collapse to one result.

### 8.9 Context and authority

Context manifests list exact section digests, source revisions, temporal
cutoffs, budgets, exclusions, and MustPreserve facts. Summaries and retrieved
memory never carry executable authority. Permission is resolved from current
canonical policy at the effect boundary, not copied from a prompt.

## 9. Exact schema inventory by milestone

Every named family below receives a Schema Freeze Pack before implementation.
The originating architecture file remains the semantic source.

| Milestone | Owner | Required contract families |
|---|---|---|
| AP0 | common/security | RecordRef, RevisionRef, HeadRef, Scope, Unit types, CommandEnvelope, EventEnvelope, Failure, Freshness, AuditActor, canonicalization |
| AP1 | Runtime | Trigger, Run/RunState, Task/TaskState, Dependency, Session, Attempt/Lease, Turn, Artifact/Section, Checkpoint, BudgetLedger, BehaviorEvent, outbox/inbox, cancellation and recovery |
| AP2 | User Input/Web | Conversation, UserRequest, AttachmentRef, IntentDraft, PolicyResolution, HumanQuestion, AnswerReceipt, Interrupt, Supersession, display/attention receipt, redacted Ops projections |
| AP3 | Capability | CapabilityRevision, SkillRevision/Resource, SkillReadReceipt, ToolRevision, CapabilityManifest, TaskCapabilityGrant, ToolCall/Effect, install/promotion, utilization/coverage |
| AP4 | Research | SourceRevision, RawDocument, Evidence, ExtractedClaim, ValidatedFact, DerivedMetric, Snapshot, EvidencePlan/Bundle, Conflict, TrackedUniverseRevision, CandidateSignal, connector/normalizer |
| AP5 | Collaboration/Team | RoleContractRevision, PromptRevision, ModelBindingRevision, AgentRevision, ScheduleRevision, TaskGraphDraft/Graph, Message, Claim, delivery/wait/cancel/supersession, child work, disagreement, decision graph, CandidateSet, PrimaryThesis, Challenge, DecisionMemo, PositionMonitorReport, PostMortem, independence/substitution |
| AP6 | Memory/Context | MemoryCandidate, MemoryItem, MemoryValidation, relation, retrieval query/manifest, IndexRevision, ContextManifest, MustPreserveManifest, compact, retention, correction/deletion |
| AP7 | Strategy | PlaybookRevision, SetupRevision, StrategyContractRevision, Hypothesis, CandidateLesson, Experiment/Opportunity manifest, run/result, validation, StrategyDecision, ActiveStrategyHead/Event, PositionStrategyBinding |
| AP8 | Evaluation foundation | BehaviorRegistration, EvaluationTicket/ack, OutcomeObservation, maturity/censoring, complete-stream cursor, decision/strategy attribution refs, integrity event, replay manifest |
| AP9 | GRACE | EvaluationProfileRevision, AtomicEvaluation, ScoreSnapshot, GRACEModelRevision, CalibrationPackRevision, validation/promotion artifact, Champion head/event, invalidation and rollback |
| AP10 | User Input/Kernel | OperationConfirmationTicket, TicketDisplayReceipt, ConfirmationReceipt, TicketStateHead/Event, OperationAuthorityBinding, GateDecision, ReductionProof |
| AP11 | Delegation/Kernel | DelegationPolicyRevision, AuthorizationTemplateRevision, ScoreSnapshotBinding, CompatibilityDecision, BudgetPoolRevision/Head, AuthorizationProposal/validation/attestation, DelegationGrant/ScopeHead, AuthorityHealthLease, DelegationCharge |
| AP12-AP14 | Integration | rollout revision, qualification report, Shadow comparison, canary revision, activation/rollback receipt, clean-day and incident evidence |

## 10. Milestone plan

### AP0 — Charter, contracts, and authority scaffold

**Goal:** make future code unable to invent identity, transport, database, or
effect rules locally.

**Deliverables:**

- accepted post-M11 Charter amendment;
- common Schema Freeze Pack and contract validation tool;
- repository layout for contracts, migrations, audit fixtures, and generated
  types;
- schema/role creation plan with no shared writer credential;
- service authentication and secret-loading profile;
- outbox/inbox and canonicalization golden harness;
- feature/effect-mode registry; and
- a migration plan that preserves existing Kernel public tables unless a
  separately reviewed Kernel migration is necessary.

**Acceptance:**

- malformed/unknown revisions, units, enums, digests, and fields fail closed;
- cross-language golden digests are identical;
- changed-body idempotency replay conflicts;
- database grants prove every forbidden cross-owner write fails;
- secrets never appear in contracts, logs, fixtures, or prompts; and
- absent or malformed effect flags resolve to disabled.

**Forbidden effect:** no Runtime behavior change and no operation emission.

**Suggested implementation reasoning:** Max.

### AP1 — Durable Control Plane and Worker

**Goal:** replace the prototype's process-local loop without creating a second
proposer.

**Deliverables:**

- durable Run, Task, Session, Attempt, Turn, Artifact, lease, budget,
  Checkpoint, BehaviorEvent, outbox, and inbox state machines;
- deterministic Scheduler and bounded Worker claims;
- retry, timeout, cancellation, supersession, recovery, and dead-letter paths;
- durable token/tool/time/fan-out budget accounting;
- atomic Artifact plus BehaviorEvent publication;
- model call manifests and replay-safe results; and
- retirement of the legacy direct `runSession -> POST /operations` path.

The first implementation remains one `agent-runtime` deployable with logical
Control Plane and Worker packages.

**Acceptance:**

- kill/restart at every claim, model-call, Artifact, outbox, and acknowledgement
  boundary yields zero duplicate logical Tasks and zero duplicate effects;
- lease expiry cannot let a stale Worker commit over a newer generation;
- cancel and supersede races converge deterministically;
- fan-out and budget exhaustion preserve completed work and refuse new work;
- missing BehaviorEvent registration blocks dependent new-risk work; and
- the same committed cognition result is reused after delivery failure.

**Forbidden effect:** all operation emission is disabled, including Shadow.

**Suggested implementation reasoning:** Ultra.

### AP2 — User Input and early Agent Ops Web

**Goal:** give the user a visible, durable control surface before complex
autonomy exists.

**Deliverables:**

- deterministic Input Gateway, LLM IntentDraft, deterministic Policy Resolver;
- ambiguity, clarification, interrupt, supersession, and answer binding;
- durable Runs/Tasks/Attempts/Artifacts read models;
- early Agent Ops pages for activity, waiting questions, failures, budgets,
  provenance, and context manifests;
- typed commands with idempotency and freshness;
- role/access policy, redaction, attention mapping, reconnect/gap behavior, and
  isolated diagnostic credentials/effects; and
- explicit links to the Kernel Cockpit.

The visual design is intentionally replaceable. The contract, authority,
freshness, and confirmation semantics are not.

**Acceptance:**

- raw input remains authoritative and the IntentDraft cannot overwrite it;
- ambiguous acknowledgements and multiple pending questions require exact
  binding;
- duplicate clicks produce one command;
- stale pages cannot confirm or mutate current state;
- reconnect detects cursor gaps and rebuilds from a freshness-stamped Snapshot;
- attachment injection remains untrusted content;
- 413/400/409/401 failures are stable and contain no database or secret text;
  and
- Web outage does not alter Kernel safety or infer confirmation.

**Forbidden effect:** no trading confirmation and no operation emission.

**Suggested implementation reasoning:** Max.

### AP3 — Capability Registry, Skills, and Tool Gateway

**Goal:** make the Planner aware of the real capability universe and make every
Tool use governed and observable.

**Deliverables:**

- versioned Capability, Skill, Tool, and manifest schemas;
- file-backed `SKILL.md` loading with complete entrypoint reads and
  progressive disclosure;
- deterministic discovery, eligibility, binding, and Task grants;
- source-level registration guide at the capability package entrypoint;
- Tool Gateway with effect classes, schemas, timeouts, quotas, redaction, and
  audit;
- Skill/tool read receipts and utilization/coverage diagnostics; and
- separate read-only Robinhood/research capabilities without mutation Tools.

**Acceptance:**

- Planner tests demonstrate discovery beyond one familiar scanner;
- missing capability becomes an explicit gap;
- unregistered or incompatible Tools cannot execute;
- a Skill cannot grant itself a Tool or widen Task scope;
- prompt injection cannot select a forbidden effect class;
- secrets and private Tool payloads stay outside prompts and Web projections;
  and
- unused relevant capabilities are detectable without forcing irrelevant use.

**Forbidden effect:** no Robinhood mutation Tool exists in Worker credentials.

**Suggested implementation reasoning:** Max.

### AP4 — Evidence Store and Research Data Plane

**Goal:** provide fast, point-in-time, source-backed research without treating
retrieval output as execution truth.

**Deliverables:**

- source registry, connector, normalizer, BlobStore, and evidence schemas;
- RawDocument -> ExtractedClaim -> ValidatedFact -> DerivedMetric ->
  AgentInterpretation separation;
- immutable point-in-time Snapshot and EvidenceBundle construction;
- provenance, freshness, conflict, licensing, retention, and access policies;
- tracked-universe and candidate-signal contracts; and
- Data Desk read APIs and coverage diagnostics.

**Acceptance:**

- future-leakage fixtures are rejected;
- stale, missing, conflicting, and partially normalized inputs remain explicit;
- raw source is preserved when extraction fails;
- malicious source text cannot become system instruction;
- external quote divergence is visible and Kernel execution facts win;
- more than 1 MB content is bounded and stored by reference, not injected into
  every context; and
- Data Plane outage blocks evidence-dependent decisions but not Kernel safety.

**Forbidden effect:** research prices and account facts cannot directly satisfy
Kernel gates.

**Suggested implementation reasoning:** Max.

### AP5 — Typed collaboration and Agent team

**Goal:** run capability-aware research workflows with typed artifacts rather
than chatty Agent-to-Agent transcripts.

**Deliverables:**

- TaskGraphDraft validation and deterministic TaskGraph scheduling;
- typed Task, Message, Artifact, Claim, delivery, wait, and decision graphs;
- frozen Role packages for Data Desk, Scout, Challenger, Decision Desk,
  Position Manager, and Coach;
- on-demand specialist and temporary Planner/Lead contracts;
- exact Prompt/Model/Skill/Tool/memory-scope revisions plus independence,
  substitution, health, trigger, schedule, dedupe, and budget policy;
- bounded child-work, delivery, wait, cancel, supersession, disagreement, and
  unavailable-review state machines;
- required forecast fields and BehaviorEvent mapping; and
- compact references instead of repeated transcript payloads.

Only Decision Desk may emit a new-risk intent Artifact. Only Position Manager
may emit a reducing intent Artifact. Both remain untrusted and non-executable.

**Acceptance:**

- capability coverage is validated before dispatch;
- duplicate work and expired messages do not revive superseded Tasks;
- required challenge cannot be silently substituted or omitted;
- shared-source false consensus remains visible;
- no Role impersonation or permission widening succeeds;
- WAIT, PASS, denied, ignored, and untraded decisions stay in the graph; and
- no collaboration record can approve a Kernel operation, strategy, model, or
  grant.

**Forbidden effect:** research-only; no Kernel operation proposal.

**Suggested implementation reasoning:** Max.

### AP6 — Governed memory, context, checkpoint, and compact

**Goal:** keep long-running Agents bounded without losing risk-relevant facts or
turning memory into authority.

**Deliverables:**

- L0-L4 plus Archive storage and access rules;
- candidate, validation, promotion, conflict, expiry, correction, and deletion;
- temporal retrieval with exact `as_of` and source/revision filtering;
- deterministic context section order and per-section budgets;
- ContextManifest and MustPreserveManifest;
- crash-safe atomic compact with Validator; and
- index revision, rebuild, retention, and bounded-growth jobs.

**Acceptance:**

- compact crash produces either the old or complete new checkpoint;
- every MustPreserve fact survives byte-for-byte identity validation;
- summary drift, future leakage, prompt injection, private-scope escape, stale
  memory, self-validation, and conflicting memory fixtures fail safely;
- index rebuild returns equivalent qualified references;
- context overflow yields explicit degradation or refusal, never silent removal
  of risk facts; and
- retrieved prose cannot carry a Tool grant or trading authorization.

**Forbidden effect:** memory cannot modify current policy, strategy head,
GRACE result, Delegation grant, or Kernel state.

**Suggested implementation reasoning:** Ultra.

### AP7 — Playbook Registry and Strategy Lab

**Goal:** let Agents maintain and test explicit trading knowledge without
self-promoting it into Live policy.

**Deliverables:**

- immutable Narrative Doctrine, Structured Setup, and Executable Strategy
  Contract revisions;
- Hypothesis and CandidateLesson workflows;
- reproducible opportunity and experiment manifests;
- backtest, replay, Shadow, comparison, multiple-testing, and stress protocols;
- candidate/Champion validation and independent activation path;
- position-to-entry-revision binding and reviewed migration; and
- Strategy Lab Web read models and human decisions.

**Acceptance:**

- no look-ahead, survivor, source-selection, or outcome-aware parameter change;
- reruns reproduce exact inputs, code, parameters, and results;
- one favorable trial among many cannot hide the test family;
- Candidate cannot call itself Champion;
- active-head races have one winner and exact rollback;
- open positions retain entry revisions unless explicitly migrated; and
- profitable rule violations remain adverse evidence.

**Forbidden effect:** Strategy activation does not create Kernel or Delegation
authority; Live activation is disabled.

**Suggested implementation reasoning:** Ultra.

### AP8 — Behavior and evaluation foundation

**Goal:** create the complete, pre-outcome behavior stream needed by GRACE
without implementing a scoring model.

**Deliverables:**

- BehaviorEvent registration and EvaluationTicket acknowledgement;
- immutable target, horizon, benchmark, scoring-rule, evidence, and attribution
  binding;
- qualified opportunity-stream cursor including WAIT/PASS/denied/untraded;
- deterministic maturity, censoring, outcome, and reconciliation pipeline;
- decision/strategy/Role revision binding;
- replay manifests and integrity events; and
- diagnostic coverage reports.

**Acceptance:**

- late or outcome-aware registration is rejected;
- Artifact and BehaviorEvent crash matrix yields neither or one matching pair;
- missing acknowledgement blocks new-risk progression;
- selective omission and retry cannot change inclusion;
- economic PnL is recorded once and referenced, not credited repeatedly;
- actual Live, unrealized, Shadow, and counterfactual ledgers stay distinct; and
- outage never blocks Kernel-native safety actions.

**Forbidden effect:** no official ScoreSnapshot and no downstream authority.

**Suggested implementation reasoning:** Ultra.

### AP9 — GRACE Engine, Validator, and observe-only publication

**Goal:** implement the independently reviewed quantitative specification and
publish reproducible ratings with no authority effect.

**Entry gate:** every prerequisite in section 3.3 is satisfied. If not, AP9 is
blocked even when AP8 data exists.

**Deliverables:**

- exact Evaluation Profile, AtomicEvaluation, ScoreSnapshot, ModelRevision, and
  Calibration Pack contracts;
- offline Engine and replay;
- independent Validator, sensitivity, shift, and tail probes;
- fenced Champion promotion and exact rollback;
- official observe-only publication with expiry and invalidation; and
- Coach/Advisor explanations stored separately from score fields.

**Acceptance:**

- calibration, proper scoring, uncertainty, correlation, selection,
  attribution, tail, integrity, and transfer probes from the quantitative spec
  pass;
- Engine and Validator reproduce within frozen tolerance;
- trainer cannot approve its own model and concurrent promotion has one winner;
- one lucky high-variance outcome cannot create material favorable movement;
- stale, missing, invalid, unreconciled, or incompatible evidence cannot yield
  a favorable official score;
- ScoreSnapshot contains no tier, dollar permission, order, or recommendation;
  and
- model rollback restores exact prior revisions and reproducible snapshots.

**Forbidden effect:** Delegation and Kernel ignore GRACE for Live authority.

**Suggested implementation reasoning:** Ultra.

### AP10 — Exact confirmation and Kernel reduction proof

**Goal:** modernize one-operation human confirmation and give Kernel a
fact-derived reduction proof without introducing reusable autonomous authority.

**Deliverables:**

- Kernel-owned immutable confirmation ticket, display receipt, confirmation
  receipt, state head/events, consumption, deadlines, and supersession;
- Kernel-owned exact-confirmation OperationAuthorityBinding and GateDecision;
- Kernel-owned ReductionProof derived from canonical positions, orders,
  reservations, and proposed effect;
- pending-operation -> ticket-head lock order, CAS, deadlines, dispatch fence,
  and unknown-effect recovery;
- authenticated display and decision APIs with separate authority classes; and
- compatibility comparison against the current human-confirmed Kernel path.

Exact confirmation is one-operation authority, not a reusable grant.
Kernel-verified reduction is derived from canonical positions/orders and does
not trust an Agent's `close` label.

**Acceptance:**

- duplicate/stale display, decision, expiry, supersession, and consume races
  linearize exactly once;
- changed operation content requires a new ticket;
- crossed/invalid quote and stale account facts fail closed;
- confirmation/dispatch/working deadlines use database time and cannot invert;
- reject, expiry, cleanup, and confirmation cannot produce two winners;
- cancellation after Kernel submission follows Kernel state and cannot pretend
  an already-sent or unknown effect disappeared;
- dispatch/unknown-effect races preserve the original immutable authority
  binding and never resend blindly;
- malformed `close` intent cannot pass reduction proof or increase exposure;
  and
- compromised Agent/Web credentials cannot forge a ticket, receipt, binding,
  Gate decision, or reduction fact.

**Forbidden effect:** exact-confirmation code remains non-Live until AP13; no
reusable grant and no option mutation.

**Suggested implementation reasoning:** Ultra.

### AP11 — Delegation policy and observe-only Gate

**Goal:** reproduce the frozen Delegation policy and Kernel Gate decisions
without granting autonomous Live authority.

**Deliverables:**

- deterministic policy, template, score binding, compatibility, pool,
  proposal, validation, attestation, grant, ScopeHead, and health-lease
  contracts;
- separate Engine, Validator, and activation credentials;
- exact stable partition and budget-pool identity;
- canonical source-head, ScopeHead, PoolHead, operation, reservation, and
  dispatch lock order;
- Kernel-owned autonomous OperationAuthorityBinding, DelegationCharge, and
  GateDecision contracts;
- admission and dispatch revalidation plus revocation and unknown-effect
  semantics; and
- observe-only comparison records against frozen policy fixtures.

**Acceptance:**

- missing, stale, invalid, mismatched, or `UNKNOWN` required inputs fail
  closed;
- budget partitions cannot reset through policy, template, grant, or pool
  revision churn;
- concurrent proposal, validation, activation, revocation, admission, and
  dispatch produce one fenced winner;
- revoke/admit/dispatch/unknown-effect races preserve irreversible charges and
  never resend blindly;
- incompatible material revisions require explicit requalification;
- compromised Agent, GRACE Engine, Delegation Engine, Validator, or Web
  credentials cannot self-activate or write Kernel effects;
- observe-only decisions replay exactly from immutable inputs; and
- autonomous Live remains impossible at credentials, policy, and Kernel
  layers.

**Forbidden effect:** no autonomous Live grant is effective; no option
mutation.

**Suggested implementation reasoning:** Ultra.

### AP12 — End-to-end Shadow

**Goal:** exercise the complete team -> evidence -> strategy -> behavior ->
GRACE -> Delegation -> Kernel pipeline without broker mutation.

**Deliverables:**

- Shadow operation intents with full provenance;
- Delegation Shadow Gate and budget accounting;
- production-shaped Provider responses in the Shadow ledger;
- fault injection for every service boundary;
- Agent Ops/Strategy Lab comparison and incident views; and
- signed qualification report for the exact scope.

**Acceptance:**

- Shadow and Live evidence, counters, positions, cash, orders, and authority are
  physically and semantically separated;
- 20-way and multi-instance concurrency stays inside exact caps;
- outbox duplicates, crashes, partitions, stale heads, and poison events do not
  create duplicate logical effects;
- unknown Provider status latches and stops new sends until canonical
  reconciliation;
- all forbidden write paths remain denied under credential compromise tests;
  and
- rollback to research-only preserves complete audit history.

**Forbidden effect:** zero broker mutation.

**Suggested implementation reasoning:** Max for integration; Ultra for the
security and concurrency sign-off.

### AP13 — Human-confirmed Live

**Goal:** route only exact, freshly confirmed operations through the new
platform while Delegation stays observe-only.

**Deliverables:**

- authenticated exact-ticket Web flow;
- Kernel re-gating at confirmation and dispatch;
- limited whole-share equity limit-order scope;
- reconciliation, alerts, incident stop, and rollback;
- clean-day evidence and human review; and
- Delegation observe-only comparisons attached to each decision.

**Acceptance:**

- every Live new-risk effect binds an exact valid confirmation;
- changed quote, quantity, side, symbol, account, deadline, or risk fact
  invalidates the old ticket;
- failure before/after broker send follows Kernel unknown-effect recovery;
- cancel/reconcile/reduction stay available during Agent/GRACE/Delegation/Web
  outages;
- daily and cumulative caps cannot be exceeded under concurrency; and
- rollback disables new Live admission without hiding open work.

**Forbidden effect:** `autonomous_grant` is rejected by Kernel.

**Suggested implementation reasoning:** Ultra.

### AP14 — Optional autonomous Live canary

**Goal:** test one narrowly scoped autonomous authority revision only after all
independent gates pass.

**Entry gate:** explicit owner approval, signed current GRACE Calibration Pack
and Champion, accepted Delegation policy/validator/fault suite, successful AP12
and AP13 evidence, and a fresh canary revision.

**Initial scope:**

- one declared account and ledger;
- whole-share equity limit orders only;
- one exact symbol/instrument identity chosen in the human-owned canary
  revision;
- very small non-increasing count and risk envelopes;
- short admission/dispatch/working/governance deadlines;
- no options, crypto, market orders, or dynamic symbol expansion; and
- immediate downgrade/rollback paths.

SOFI is the owner's current preference for a future first autonomous fixture,
not an authorization and not a hard-coded strategy rule. The actual instrument,
price, quantity, limits, and timing require a fresh reviewed canary revision.

**Acceptance:**

- stale/missing/invalid GRACE, policy, grant, lease, source head, quote, account,
  or reconciliation state denies new risk;
- no score or profit can exceed human-owned or Kernel absolute limits;
- revocation and dispatch races pass the frozen Delegation suite;
- policy/model changes requalify rather than silently retune authority;
- one canary cannot expand its own scope;
- incident stop and rollback work while dependencies are degraded; and
- every operation replays from immutable evidence through broker result.

**Suggested implementation reasoning:** Ultra.

## 11. Threat model and mandatory proof

| Threat | Default response | Mandatory proof stage |
|---|---|---|
| prompt or attachment injection | treat as data; capability and effect checks remain deterministic | AP2-AP6 |
| malicious/stale external source | quarantine, preserve provenance, block evidence-dependent decision | AP4 |
| hallucinated Tool/Skill or undiscovered capability | registry validation or explicit coverage gap | AP3/AP5 |
| duplicate delivery or Worker crash | inbox/outbox dedupe, lease fence, same causal identity | AP1 |
| selective behavior registration | atomic registration, complete-stream cursor, no late backfill | AP8 |
| model self-validation or PnL gaming | independent Validator, non-compensatory integrity, fixed stream | AP9 |
| stale Web or human double click | exact immutable target, generation, deadline, idempotency | AP2/AP10 |
| cross-service partial failure | committed owner state plus replayable event; fail closed for new risk | every stage |
| compromised low-authority service credential | database and API denial outside declared owner/effect class | AP0 onward |
| compromised GRACE or Delegation Engine | cannot self-promote, activate, write Kernel, or call Provider | AP9/AP11 |
| database concurrency | canonical lock order, CAS generation, unique keys, race harness | AP1/AP7/AP9-AP11 |
| unknown broker effect | Kernel latch, canonical pull/reconciliation, no blind resend | Kernel/AP12-AP14 |
| context overflow or summary drift | explicit budget failure; MustPreserve validation | AP6 |
| policy/model distribution feedback | new qualification window and visible shift, not online authority tuning | AP9/AP14 |
| diagnostics/admin misuse | separate credentials, no browser-exposed secret, immutable audit | AP2/AP10/AP11 |

Prompt-level instructions are never accepted as the only mitigation for an
authority or persistence threat.

## 12. Rollout and feature gates

Use one monotonic platform mode:

```text
disabled
research_only
shadow
live_exact_confirmation
live_autonomous_canary
```

Transitions are explicit, audited, and reversible toward a less permissive
mode. Environment configuration alone cannot advance the mode. Live requires
all of:

- Kernel build support;
- Kernel `LIVE_TRADING_ENABLED=true`;
- the exact deployment mode;
- compatible current signed policy/model heads where applicable;
- service health and source freshness;
- an authorized activation receipt; and
- operation-specific Gate success.

Independent kill switches remain:

- Agent operation emission;
- official GRACE publication;
- Delegation proposal/activation;
- Shadow integration;
- exact-confirmation Live;
- autonomous Live; and
- each product/effect class.

Absent, malformed, or disagreeing settings resolve to the least permissive
state. A rollback stops new admission; it does not delete history, abandon
unknown effects, or block cancel/reconcile/reduction.

The legacy Runtime proposer is disabled before AP1 claims any trigger. It is not
kept as a quiet fallback.

## 13. Acceptance command contract

AP0 must add one repository entrypoint:

```sh
./scripts/certify-agent.sh <stage>
```

Valid stages are `ap0` through `ap14` and `all`. A stage command:

- is non-interactive unless a separately documented real-money canary requires
  a fresh confirmation;
- starts from a declared clean fixture;
- prints a concise PASS/FAIL summary plus the seed and artifact directory;
- exits non-zero on failure, skipped mandatory probes, dirty generated files,
  leaked secrets, or missing evidence;
- retains machine-readable JUnit/JSON evidence;
- never performs a Live mutation for AP0-AP12; and
- refuses AP13/AP14 unless the exact signed activation inputs are supplied.

Every stage runs the applicable common checks:

```sh
(cd agent-runtime && test -z "$(gofmt -l .)")
(cd agent-runtime && go vet ./...)
(cd agent-runtime && go test -race ./...)
(cd kernel && test -z "$(gofmt -l .)")
(cd kernel && go vet ./...)
(cd kernel && go test -race ./...)
docker compose config
./scripts/validate-contracts.sh
./scripts/check-db-authority.sh
./scripts/check-secret-leaks.sh
```

Stage-specific harnesses live under `audit/agent/<stage>/` and must include
deterministic unit/contract tests plus black-box crash, concurrency, stale,
duplicate, malformed, and credential probes appropriate to that stage. A test
written by the implementation author is a regression fixture, not independent
audit evidence; the final stage review must include independently designed
probes.

The certification wrapper does not hide failures behind retries. Flake, timeout,
or unavailable mandatory infrastructure is a failure requiring explanation.

## 14. Stage Definition of Done

A milestone is complete only when:

1. its semantic architecture dependencies are frozen;
2. its Schema Freeze Pack lands in a contract-only commit;
3. migrations and least-privilege roles apply and roll back on a fresh database;
4. generated code and docs are reproducible with no dirty diff;
5. state-machine, contract, race, crash, security, retention, and replay tests
   pass;
6. the stage certification command passes from a fresh environment;
7. Web/read-model freshness and failure states are visible where relevant;
8. feature flags remain at the stage's least-permissive mode;
9. rollback is exercised and preserves audit/reconciliation state;
10. the plan index records commit and evidence;
11. changes are committed and pushed as one coherent module before the next
    module begins; and
12. the handoff names the next module and its recommended reasoning mode.

Do not combine unrelated milestones in one implementation commit. Within a
large milestone, use the sequence:

```text
contract pack -> migration/roles -> deterministic core -> transports/UI
-> fault and acceptance suite -> docs/evidence
```

Each coherent unit is committed and pushed before the next unit when it can be
reviewed independently.

## 15. Final audit boundary

This roadmap is not the final code authorization. The next planning module is a
cross-module architecture audit that must:

- trace every write and authority path end to end;
- verify every required-later-specification item has an owner and stage;
- check that record identity, revision, digest, freshness, and temporal
  semantics agree across modules;
- check lock order, outbox/inbox, retry, cancellation, supersession, and outage
  behavior across boundaries;
- prove no Agent/Tool/Web/GRACE/Delegation path bypasses Kernel;
- identify any schema or quantitative pack still blocked by independent review;
- validate the stage dependency graph and rollback order; and
- issue an explicit `AUTHORIZED_FOR_AP0` or a blocking finding list.

Only `AUTHORIZED_FOR_AP0` authorizes the first contract/scaffold code. Later
milestones still require their own entry gates. GRACE, Delegation, and Live
cannot inherit authorization merely because AP0 begins.
