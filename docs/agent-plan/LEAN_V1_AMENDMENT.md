# Agent Platform Lean v1 Amendment

[Back to Agent Plan Index](INDEX.md)

> Status: **PROPOSED ARCHITECTURE AMENDMENT — owner review/freeze required**
>
> Scope: runtime topology, internal coordination, capability activation,
> evidence/memory substrate, GRACE intake, Delegation freshness and contract
> ceremony. It does not authorize AP0 or any Live effect.

## 1. Decision

The frozen architecture has the right safety boundaries but turns too many
logical owners into permanent services and makes several ordinary paths pay for
the most elaborate governance flow. Lean v1 keeps the authority model and
removes infrastructure that has no demonstrated need.

Keep unchanged:

- Kernel is the only broker mutation, reservation, hard gate and reconciliation
  boundary; Agent output is untrusted intent.
- Qualified ordinary trades are eventually autonomous inside explicit scope;
  humans are not a per-order dependency.
- Run/Task/Attempt/Artifact identity, durable state, outbox delivery,
  idempotency, interruption and recovery are real platform concerns.
- Roles are versioned configurations with prompts, Skills, Tools, model routing
  and budgets, not hard-coded personalities or services.
- Research evidence has source/time/lineage; memory summaries are not facts.
- GRACE is delayed, outcome-based evidence; Delegation and Kernel remain the
  authorization path.
- Shadow/Live separation, immutable revisions, fenced activation, exact scoped
  grants and fail-closed unknown handling remain mandatory.

Simplify for v1:

- four product/deployment components instead of a service product per logical
  owner, with separate process/credential profiles where authority requires;
- workflow profiles instead of invoking the entire Agent roster every time;
- typed task/artifact exchange instead of a general Agent-to-Agent protocol;
- asynchronous GRACE intake after an atomic BehaviorEvent commit;
- immutable Grant + ScopeHead generation + expiry instead of a rolling three-
  party health lease;
- one typed evidence substrate and PostgreSQL search before a specialized data
  or vector platform; and
- machine contracts only at real process/security/event boundaries, not four
  duplicate specifications for every internal package.

This is not permission to collapse independent authority. One binary is code
reuse, not one address space: normal Control, untrusted Worker, GRACE,
Delegation, Validator and Activator profiles run as separate OS processes/jobs
with non-overlapping secrets and database roles. Separation is enforced with
those credentials, immutable records and fenced transitions.

## 2. Initial runtime topology

Lean v1 has four product/deployment components:

```text
User / schedules / Kernel events
              |
       Agent Platform distribution
  Control/API/Web | Worker | GRACE | Delegation
       separate process/credential profiles
        |              |
        |       Research Gateway ---- approved external data/tools
        |
      Kernel ---- Broker Provider
        |
   PostgreSQL + content-addressed blob volume/object storage
```

| Boundary | Owns | Explicitly does not own |
|---|---|---|
| `kernel` | operation gate, Kernel trade entitlements/reservations, attempts, orders/fills, reconciliation, broker Provider and final consumption of eligible Delegation evidence | prompts, planning, research, GRACE score, Delegation creation/activation |
| `agent-platform` distribution | code for Input/Intent, Control, Worker/Role runtime, Registry, Artifact/Evidence/Memory/Strategy, Behavior, GRACE, Delegation and static Web, executed only through the profiles below | broker credentials/effects, Kernel DB writes, self-activation, shared catch-all credential |
| `research-gateway` | approved connector sessions, read-only external Tool calls, normalization, quarantine and egress controls | production Robinhood mutation credential, grants, score promotion, Kernel DB |
| PostgreSQL + blob storage | durable relational truth, outbox/inbox, immutable revisions, bounded blob bytes | business decisions or executable behavior |

The Web is static content served by the Control/API profile in v1. Blob storage is a
package plus local volume/object-store adapter, not a daemon. Split either only
when independent scaling, availability or security isolation is measured and a
new boundary review proves the benefit.

The Agent Platform distribution runs these credential-isolated profiles:

| Profile | May write/use | Must not possess |
|---|---|---|
| `control-api` | UserRequest/Run/Task state, accepted Worker Artifact, atomic BehaviorEvent/outbox, effect dispatch credential, static Web | model Tool secrets, GRACE/Delegation/activation/human-confirmation credentials |
| `worker` | claimed Attempt output/candidate Artifact only | Behavior publication, Kernel proposal, GRACE/Delegation, head or human credentials |
| `grace-intake` / `grace-engine` | GRACE intake/cursor/outcome/evaluation record families appropriate to the stage | Worker, Delegation, activation, Kernel or human credentials |
| `delegation-engine` | deterministic proposal/compatibility candidates | activation, Worker, Kernel or human credentials |
| Validator/Activator jobs | their one attestation or fenced-head transition | normal server/Worker/candidate-author credentials |

Profiles are separate processes/containers or one-shot jobs even when built from
the same image. Normal long-running server/Worker processes never load GRACE,
Delegation, Validator, Activator or human trading-authority secrets. Candidate
authors never receive activation credentials. Quantitative GRACE validation may
later use a separately operated distribution because independence is
substantive; no network service is provisioned before that need.

### Frozen-deployable mapping

| Frozen roadmap name | Lean v1 form |
|---|---|
| `agent-runtime`, `agent-web` | one `agent-platform` distribution; separate `control-api` and `worker` processes; Control serves static Web |
| `artifact-store` | content-addressed package/adapter plus blob volume |
| platform/capability/agent/strategy Activators | governance subcommands with distinct credentials and fenced heads |
| capability/agent/strategy Validators | CI/offline validation jobs that append attestations |
| GRACE intake/engine | separate credential profiles of the Agent Platform distribution; never in the Worker process |
| GRACE Validator/Activator | independent AP9 job/credential, not pre-provisioned v1 daemon |
| Delegation engine | separate deterministic Agent Platform job/profile; activation remains separately credentialed |
| Delegation Validator/Activator | offline attestation/activation jobs |
| user-authority gateway | no Agent-plane trading authority: Web displays/links only; exact trade confirmation goes directly to Kernel Admin/human-audience API |

## 3. Control Plane, Planner and roles

The Control Plane is deterministic software, not an Agent. It owns trigger
dedupe, Run/Task state machines, dependency readiness, attempt leases, budgets,
cancellation, outbox delivery, recovery and terminal status. It never decides
whether a stock is attractive.

Input Gateway first persists the raw `UserRequest`. An LLM may produce an
untrusted `IntentDraft`; a deterministic resolver applies identity, policy,
effect tier, ambiguity and confirmation rules before creating a Run. Exact
trade confirmation, breaker resume, emergency action and unknown-effect
resolution go directly to Kernel's separately authenticated human/Admin API;
Agent Platform may display status or link to it but never receives, proxies or
signs that credential. Other platform-governance commands use their one-shot
Activator path. No authority is inferred from conversational prose.

The Task Planner is an LLM role invoked only when a request needs decomposition
or capability choice. It receives a bounded capability catalog containing
stable IDs, descriptions, effect tier, input/output summary, freshness and cost
metadata. It does not receive every full `SKILL.md` or Tool schema.

After the plan names capabilities, deterministic resolution freezes exact
Skill/Tool revisions and injects only their full selected documents/schemas
into the assigned Task. The Control Plane intersects them with code ceilings,
active capability heads, principal/Run policy and remaining budgets. Unknown,
inactive, over-budget or effect-incompatible capabilities are refused; the
Planner cannot invent a capability by naming it.

Data Desk, Scout, Lead, Challenger, Decision, Position Manager and Coach are
Role configurations executed by the same Worker pool. None is a scheduler,
control plane, database owner or service.

Workers return validated Artifacts; they do not hold the Kernel proposal
credential. Only the deterministic effect dispatcher may submit an operation,
after it verifies the terminal Decision Artifact, atomic BehaviorEvent identity,
active scope and Run authority. This retires the legacy direct Runtime proposer
before the durable scheduler claims any trigger.

## 4. Workflow profiles, not mandatory full-team ceremony

Choose the smallest workflow that can answer safely:

| Work shape | Default profile |
|---|---|
| exact bounded query | Intent resolution -> one specialist/tool -> response |
| broad opportunity scan | capability routing -> Scout/data tasks -> Decision only for survivors |
| deep trade research | Planner when needed -> Data/Scout -> Lead -> Challenger -> Decision |
| position/market event | Position Manager plus only the required evidence/Decision review |
| scheduled post-trade evaluation | deterministic outcome collection -> Coach/Post Mortem -> GRACE queue |
| material strategy/policy change | Strategy candidate -> independent validation -> governed activation |

The Planner is skipped for a one-step task. Challenger is mandatory only for a
policy-defined material decision, not every lookup. Missing a required reviewer
yields WAIT/PASS, but an unnecessary roster member is not summoned merely to
satisfy an architecture diagram.

## 5. Internal Agent communication

Do not implement a general network A2A chat protocol in v1. Agents exchange a
small set of durable typed records:

- `TaskEnvelope` with objective, causal identity, inputs, capability bindings,
  budgets, deadline and expected output contract;
- `ArtifactRef`/`EvidenceRef` rather than copied source bodies;
- `ReviewRequest` and typed findings/decision;
- `CheckpointRef`, cancellation/supersession and terminal status; and
- a bounded human-readable note for rationale, never an unbounded transcript.

The Control Plane owns inbox/outbox delivery, idempotency and retries. A Worker
cannot create another Worker or grant itself budget; it returns a requested
subtask proposal to the Control Plane. Session recovery reads state,
checkpoints, selected evidence and compact summaries. It does not replay all
Agent dialogue into every context.

This is sufficient for efficient collaboration and leaves a clean external A2A
adapter boundary if interoperability is later demonstrated as a requirement.

## 6. Capability governance without a service mesh

Capability Registry is a database-owned module and read API, not a service per
Skill or Tool. Its code header and contributor documentation must explain how
to register metadata, schemas/effect tier, full `SKILL.md`, tests, ownership,
secrets, budgets, failure semantics and activation evidence.

Use tiers:

1. local/read-only Skills and deterministic transforms: automated validation,
   reviewed revision and fenced active head;
2. external reads or secret-bearing Tools: Gateway isolation, source/data
   policy, rate/size limits and integration evidence; and
3. external writes, money-adjacent actions or authority widening: separately
   frozen effect protocol, durable attempt/reconciliation and privileged
   activation. Broker mutation is never in this tier because it remains Kernel-
   only.

Independent human/validator activation is required for privileged/widening
changes, not for every typo fix to a local read-only Skill. Any material change
creates a new immutable revision; normal Workers cannot advance active heads.

## 7. Evidence, memory and context

Start with one typed `EvidenceRecord` family: kind, source identity, observed/
effective times, subject, normalized facts, confidence/conflict markers,
`BlobRef`, lineage and retention. `kind` is immutable and uses discriminated
payloads plus database constraints. A connector may append quarantined Raw; an
extractor may append a Claim that references Raw; only the validated-data role
may append ValidatedFact; a typed calculator may append DerivedMetric from
declared inputs. No update promotes a row in place, and Worker/external prose
cannot label itself validated. Use indexed columns for real query needs rather
than one table per source.

Start retrieval with PostgreSQL indexes and full-text search. Add embeddings or
a vector database only after measured queries show that typed filters plus FTS
miss relevant evidence at an unacceptable rate.

Memory remains logically multi-level but needs only a small physical model:

- L0: ephemeral Task context;
- L1: durable Run/Task checkpoint and compact summary;
- L2/L3: typed `memory_item` records for episodic and consolidated knowledge,
  with evidence lineage, confidence, invalidation and retention; and
- L4: an identity/preference/policy resolver view over authoritative records,
  not another prose store.

Context assembly selects by task, time, authority, freshness and token budget.
It carries references and bounded excerpts, not growing documents. Compaction
can replace narrative context only after preserving decision-critical claims,
conflicts, evidence IDs and unresolved obligations. Summaries never replace
Kernel/Provider facts or GRACE evaluation data.

Worker output enters Memory as a candidate. A separately credentialed,
deterministic consolidation/validation profile appends promotion/invalidation
state; a Worker cannot change its own candidate into trusted L2/L3 memory.

Absolute parser/file/page/result/context ceilings stay in code. Lower per-Run,
role, Tool and token budgets are versioned database policy and freeze on Run
creation, following [`../plan/06_POLICY_OWNERSHIP.md`](../plan/06_POLICY_OWNERSHIP.md).

## 8. Behavior, GRACE and Delegation

The deterministic Control Plane, not the Worker, assigns an immutable
`EvaluationContract` when a Task is created. For every contract-qualified
opportunity, terminal WAIT/PASS/PROPOSE, denied, untraded, expired and failed
outcomes all receive a canonical `BehaviorEvent` and continuous partition
cursor. A qualifying Task cannot publish/terminate until Artifact,
BehaviorEvent and outbox commit atomically. An operation proposal carries that
behavior ID/digest.

PostgreSQL outbox delivery to GRACE is asynchronous and idempotent; a
synchronous `EvaluationTicket` acknowledgement is not permission and is not
required on every trading request. GRACE intake records cursor coverage and a
Snapshot declares its complete cutoff. A cursor gap, selective omission,
overdue outbox backlog or incomplete outcome stream makes the Snapshot
ineligible and suspends new/renewed Delegation through a bound stream-health
source. This preserves complete-stream anti-gaming without putting GRACE
availability on every order's latency path.

GRACE later joins delayed outcomes and produces credibility/performance
evidence under its independently reviewed quantitative model. It neither edits
Kernel policy nor directly grants money authority. New or renewed Delegation
may require a current eligible `ScoreSnapshot`; an already authorized trade
does not wait for a same-trade score.

For v1 authorization freshness, use:

- an immutable scoped `Grant` with exact strategy/Agent/product/effect/budget,
  bound source revisions/freshness and expiry;
- a fenced `ScopeHead` generation selecting the active grant; and
- deterministic suspension when a bound source becomes materially stale,
  invalid, replaced incompatibly or incident-latched.

Do not require Engine + Validator + Activator to renew a separate short-lived
`AuthorityHealthLease` continuously. A policy-defined maximum Grant duration is
mandatory and has no permissive default. `grant.valid_until` is no later than
database creation time plus that maximum and the earliest `fresh_until`/
`valid_until` of every bound source. Renewal recomputes complete current
evidence, receives independent validation and creates/CAS-activates a new Grant;
an equal-or-narrower renewal can run unattended only through an explicitly
preauthorized narrow Activator job. Outage prevents renewal and expiry closes
new risk without waiting for a human.

Before new-risk entitlement and again in the transaction that authorizes a
broker send, Kernel invokes a read-only, non-persisted SQL function/view that
directly joins canonical Platform/effect/kill heads, ScopeHead, Grant/budgets,
complete-stream health and every bound source head. It returns source
generations/reasons and requires database now to precede all freshness/expiry
deadlines. It is never an eligibility table or cache; Live authorization cannot
use a cache.

Both Kernel transactions lock those heads in one documented stable order.
Every source activation/invalidation uses the same lock prefix. Therefore a
source change either commits before the send authorization and denies it, or
the sent marker wins the cut and the effect is explicitly in-flight for forward
reconciliation. Missing/mismatched heads, unavailable reads or absent
compatibility attestations deny. Cancel, reconciliation and verified reduction
remain available independently.

Humans normally act at initial policy/strategy/grant definition, material or
wider changes, new product/effect activation, emergency controls, and broker
unknown cases deterministic recovery cannot resolve. Inside an active qualified
scope, ordinary trades do not ask for confirmation.

## 9. Contracts and persistence discipline

Internally, Go types, SQL migrations/constraints and executable tests are the
canonical contract. Publish JSON Schema/OpenAPI only for actual HTTP boundaries
and versioned event schemas only where an independently deployed consumer needs
them. Do not require OpenAPI + AsyncAPI + JSON Schema + state YAML + goldens for
every package.

Money, authority, cross-process and public event contract changes still land in
a small contract-first commit before implementation. Purely internal package
types may evolve inside their module commit with tests. This preserves review
quality without turning every stage into a documentation ceremony.

Use PostgreSQL transactions, advisory/fenced heads and outbox/inbox. Do not add
Kafka, a workflow engine, service mesh, graph database, vector database or
generic Config Service until measured load/failure evidence requires it.

One PostgreSQL cluster is sufficient. Logical record ownership remains explicit
in repositories/functions. Material trust profiles have distinct database
roles: `control-api`, `worker`, Research Gateway, validated-data/Memory
promotion, `grace-intake`, `grace-engine`, `delegation-engine`, Validator,
Activator and Kernel. Internal packages inside one such profile do not each need
their own connection pool.

## 10. Configuration ownership

Agent configuration follows the Kernel policy taxonomy:

- code: structural invariants and absolute resource/protocol ceilings;
- deployment/secret: endpoints, model/API keys, token files, network/pool
  timeouts and the maximum effect ceiling;
- typed DB revision/head: prompts, model routing/parameters, Role contracts,
  active Skill/Tool revisions, schedules, lower budgets, Strategy/GRACE/
  Delegation policy and platform mode; and
- runtime evidence: source health, latency, data freshness, model/tool response
  and Provider/Kernel facts.

No universal configuration document owns all domains. Each domain has one
typed revision/head and one enforcement reader inventory. API keys may remain
fixed deployment secrets initially, but never committed literals or ordinary
policy rows.

## 11. Roadmap interpretation

AP0-AP15 remain useful acceptance layers and rollout gates, but they are not a
service count and do not require fifteen separately operated releases. Apply
them in these implementation bands:

| Band | Existing gates | Lean implementation focus |
|---|---|---|
| L0 | pre-AP0, AP0-AP2 | finish M11/K1; common identities; one durable Agent Platform; Input/Intent; early read-only Web |
| L1 | AP3-AP4 | Registry, selected Skill/Tool injection, Research Gateway and Evidence |
| L2 | AP5-AP7 | typed collaboration profiles, bounded Memory/context, Strategy candidates |
| L3 | AP8-AP10 | atomic Behavior registration, delayed outcomes, independently reviewed GRACE, exact-confirmation fallback |
| L4 | AP11-AP15 | Delegation observe-only -> Shadow -> transitional confirmed Live -> autonomous canary -> scoped autonomous Live |

Each cohesive module is committed and pushed independently. A band does not
authorize its next gate. AP0 remains withheld until M11, K1, the lean amendment,
Charter closeout and a refreshed audit/release record are complete.

AP0 release authority is a machine-verifiable, owner-signed/protected release
record binding exact digests, test evidence, reviewer attestation, decision and
trusted time. CI/startup verifies that record and signature/protected head. A
literal phrase in Markdown is status display only and can never authorize code.

## 12. Supersession map and non-goals

If frozen documents conflict after this amendment is owner-reviewed and frozen,
this file supersedes only the following, including every cross-reference to the
named mechanism:

- **Synchronous GRACE permission:** `BUILD_ROADMAP.md` sections 3.2 and AP8;
  `SYSTEM_BOUNDARIES.md` Logical module graph, Read-model/event direction,
  New-risk path and GRACE/Delegation path; `RUNTIME.md` GRACE intake handoff;
  `DELEGATION_POLICY.md` sections 18 and 21; and the related historical audit
  rows. `EvaluationTicket` may remain asynchronous GRACE workflow state, but its
  acknowledgement is not a same-trade authorization prerequisite. Complete-
  stream cursor health replaces that prerequisite for Snapshot/Grant
  eligibility.
- **Rolling authority lease:** every `AuthorityHealthLease`/
  `AuthorityHealthLeaseCandidate` requirement and reference in
  `DELEGATION_POLICY.md` (especially sections 10, 13, 15-17, 21-23, 26-30),
  `BUILD_ROADMAP.md` AP11/integration inventories, `SYSTEM_BOUNDARIES.md` and the
  historical audit. Grant maximum duration, direct canonical-head/freshness
  reads at admission/send, shared lock order, invalidation and renewal rules in
  section 8 replace it. All other grant/budget/activation constraints remain.
- **Human gateway placement:** `SYSTEM_BOUNDARIES.md` Presentation/user-
  authority topology and ownership rows, `USER_INPUT.md` Confirmation boundary,
  `WEB.md` Confirmation presentation/read architecture,
  `DELEGATION_POLICY.md` exact-confirmation gateway references,
  `BUILD_ROADMAP.md` topology/AP10 and historical audit service mapping are
  superseded only as physical placement. The dedicated human audience and
  receipt ownership move into Kernel Admin; they are not merged with Agent
  Control/Worker credentials.
- **Physical topology/contracts:** `BUILD_ROADMAP.md` sections 5-7 and matching
  AP deployable/contract inventories are interpreted by sections 2 and 9 here;
  requirements that every internal domain have every interface-description
  format, daemon or database pool are removed.
- **Workflow/storage shape:** mandatory full-roster Agent execution and physical
  table/service proliferation for Evidence/Memory are replaced by sections 4
  and 7, without changing logical review or transition ownership.
- **AP0 magic string:** roadmap/audit language treating the literal
  `AUTHORIZED_FOR_AP0` text as the gate is replaced by the signed/protected
  release record above. The release decision remains mandatory.

It does not supersede Kernel-only mutation, frozen approval classes, immutable
identity/revision/audit requirements, independent high-authority activation,
Shadow/Live separation, quantitative GRACE review, scoped Delegation, rollout
gates or autonomous-product intent.

Explicit v1 non-goals:

- no generic external A2A protocol;
- no service per role, validator, activator, Skill, Tool or memory level;
- no synchronous GRACE permission check per order;
- no generic settings/configuration platform;
- no production research credential with broker mutation capability; and
- no automatic strategy/authority widening from GRACE score or profit alone.

## 13. Freeze acceptance

Before freezing this amendment, a final review must prove:

- every affected record/transition still has one logical writer, and each
  Control/Worker/GRACE/Delegation/activation/human credential is inaccessible
  to every other process profile;
- every user/schedule/event request reaches one durable Run with no duplicate
  proposer;
- capability summaries and selected full-doc injection cover available Skills/
  Tools without exposing all secrets or overflowing context;
- required deep-research/Challenger profiles still fail closed, while exact
  queries do not invoke an unnecessary team;
- complete Behavior cursors cover WAIT/PASS/PROPOSE and non-traded terminal
  paths; gaps/backlog make Snapshot/Grant ineligible despite asynchronous GRACE;
- Grant + ScopeHead direct reads, fixed maximum duration, unattended narrow
  renewal, source freshness and shared admission/send lock order are at least as
  strict as the removed rolling lease;
- Agent Web cannot receive or proxy Kernel human-authority credentials;
- Raw/Claim/ValidatedFact/DerivedMetric and candidate/promoted Memory transitions
  cannot be self-promoted by a Worker;
- bounded context/compaction preserves evidence and unresolved obligations;
- the policy-ownership matrix has no duplicate or fallback authority; and
- the refreshed architecture audit lists M11/K1/GRACE/Delegation gates without
  treating application rollback as reversal of a real broker fill; and
- AP0 authorization verifies a signed/protected release record, not a magic
  Markdown string.
