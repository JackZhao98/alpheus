# Agent Team and Role Contracts

> Status: **FROZEN ARCHITECTURE — role responsibilities, authority separation,
> core/on-demand topology, independence, workflow ownership, and delayed-
> evaluation obligations are authoritative. Exact prompts, model routing,
> schedules, filenames, budgets, schemas, and deployed instance counts are not
> yet specified or authorized.**

## Purpose

Alpheus Agents are versioned, on-demand Role Packages rather than permanently
running digital people. Stable roles preserve responsibility and lineage;
Workers instantiate only the roles required by a Task and end when the Task is
complete.

The team is designed specifically for evidence-backed trading research,
decision, position management, and learning. It does not copy another system's
team count or hierarchy.

## Role, revision, and session

- `RoleContract` defines what a role is responsible for, may receive, must
  produce, must register for delayed evaluation, and must never do.
- `AgentRevision` pins one version of the Role Contract, prompt, model policy,
  Skills, maximum Tool capabilities, memory/context policy, permissions, and
  behavior/evaluation Contract references.
- `AgentSession` is one disposable execution of an AgentRevision for a Task.

Continuity comes from Role/Agent revisions, Artifacts, Messages, Checkpoints,
and governed Memory. It does not come from a hidden, indefinitely growing chat
owned by a model process.

One AgentRevision may execute multiple isolated Sessions concurrently. A
material prompt, model, Skill, Contract, or permission change creates a new
revision.

A Candidate Agent/Role/prompt/model/config revision is not active deployment
state. An independent Agent Release Validator writes a
`DeploymentValidationAttestation`; a separately credentialed Activator may only
CAS the `ActiveAgentDeploymentHead` from the exact validated manifest and
applicable owner/policy decision. Runtime/Workers cannot attest to or activate
their own release. Kernel may lock the published head through the common scoped
authority-head protocol when a Delegation lease binds it.

## Role Package direction

A role is expected to have a versioned package equivalent to:

```text
agents/<role>/
  agent configuration
  prompt content
  contract references
```

The exact file layout is not frozen. Its registry entry must identify at least:

```text
role id, purpose, revision, and owner
accepted Task/input Contracts
required output Contracts
prompt and model-routing revisions
allowed Skills and maximum Tool capabilities
memory scopes and Context policy
trigger/schedule eligibility
budgets and concurrency class
independence/conflict requirements
permissions, prohibited effects, and failure behavior
scoreable and non-scoreable behavior classes
required prediction, confidence, horizon, benchmark, and invalidation fields
evaluation Contract, counterfactual, maturity, and attribution references
immediate integrity/compliance event obligations
```

Configuration declares a maximum envelope; code enforces it. Writing a Tool or
permission into a role file does not grant it. Effective capability is the
intersection defined in `SKILLS_TOOLS.md` across Role, Task, Skill, user, Run,
deployment, health, and safety policy.

## Not Agent roles

The following are system components even when an LLM assists a narrow step:

- **Intent Interpreter:** produces an `IntentDraft` only;
- **Control Plane:** deterministic state, lease, budget, recovery, and delivery;
- **Capability Resolver/Scheduler:** deterministic eligibility and assignment;
- **Evidence Validator:** deterministic source/schema/time/freshness/lineage;
- **GRACE Engine/Validator:** independent delayed real-outcome credibility
  evaluation;
- **Delegation Policy Engine/Validator:** deterministic mapping from approved
  credibility evidence and human policy to bounded authorization proposals.

They do not own investment opinions. No Desk Agent acts as the Control Plane.

## Temporary planning and synthesis roles

### Task Planner

An ephemeral role invoked per complex Run. It consumes UserRequest/event scope,
the compact Capability Manifest, Role/Skill catalogs, budget, and current
required state. It produces a `TaskGraphDraft`, capability/evidence
requirements, dependencies, Specialist/Challenge needs, and human-input points.

It may request capabilities but cannot select a favored persona, execute
research, form a thesis, submit an operation, increase budget/permissions, or
skip mandatory independent review. The Control Plane and Scheduler validate and
enact its draft under `COLLABORATION.md`.

### Lead Analyst

An on-demand role bound to one candidate or scoped thesis. It synthesizes Data
Desk and Specialist Artifacts into a `PrimaryThesis` with Claim graph,
supporting/contradicting Evidence, mechanism, horizon, invalidation, gaps, and
uncertainty. It may provide one scoped rebuttal to a Challenge.

It cannot issue an OperationProposal, validate itself, select only favorable
Evidence, or act as final Decision Desk. Separating Primary Thesis from final
decision prevents the deciding role from building and judging the same case
without independent challenge.

When it makes a scoreable forecast Claim, its Artifact must declare the target,
direction/range or approved categorical outcome, confidence semantics, primary
horizon, benchmark, invalidation, and evaluation Contract before the outcome is
observable.

## Stable core roles

### Data Desk

Evidence coordinator for open-ended/multi-source research. It produces
`EvidencePlan`, `EvidenceBundle`, `CoverageReport`, and explicit conflict,
freshness, and gap state. It may request deterministic Research Tools and source
cross-checks.

It cannot decide trades, treat extracted claims as validated facts, increase
its budget, validate its own evidence, write Kernel/GRACE/Delegation, or infer
that missing evidence proves absence. Exact deterministic queries may bypass
its LLM under `RESEARCH_DATA.md`.

Data Desk is not scored on stock direction by default. A Role Contract may
register objectively measurable coverage, timeliness, source-correctness, or
conflict-resolution behaviors, but only against a predeclared evidence-quality
protocol.

### Scout

Discovery coordinator. It consumes market/regime context and multiple
`CandidateSignal` routes, resolves/merges entities, preserves common-source
lineage, applies initial relevance, and produces a bounded `CandidateSet` with
discovery reasons, uncertainty, suggested research needs, and exclusion
reasons.

Every scoreable `CandidateSignal` binds the discovery universe or route,
observation time, rank/bucket, reason type, target, confidence semantics,
horizon, benchmark, and Evidence Snapshot required by its evaluation Contract.
Rejected and excluded candidates remain represented so later GRACE evaluation
cannot grade only favorable discoveries.

It cannot become a universal analyst, treat repeated mentions as independent
confirmation, issue a final decision/proposal, or ignore installed capability
categories because one discovery Tool is familiar.

### Challenger

Independent adversarial review of a Primary Thesis/Decision candidate. It
targets Claim ids and produces `ThesisChallenge` with counterevidence,
confounders, leakage/selection concerns, tail and invalidation paths, surviving
claims, and unresolved material unknowns.

Its first pass uses the original Evidence rather than only a Primary summary;
it runs in a separate Session/AgentRevision and cannot see private scratchpad.
It cannot edit the Primary Artifact, approve/reject a Kernel operation, invent a
permanent veto, or write Active Strategy/GRACE/Delegation. Decision Desk must
address material findings; unresolved findings lead to WAIT/PASS/human review
rather than silent override.

A scoreable Challenge identifies the exact Claim/failure mode, expected
direction, probability or approved severity category, horizon, base-rate or
comparator, and falsification condition. Vague permanent pessimism is not
credited merely because some later loss occurs.

### Decision Desk

The only core Agent role that synthesizes new-risk trading intent. It consumes
the User/Run objective, Active Strategy/Playbook, Candidate and Primary Thesis,
Specialist Artifacts, Challenge, point-in-time Evidence Snapshot, canonical
Kernel portfolio facts, published GRACE ScoreSnapshots, and active Delegation
status.

It produces `WAIT`, `PASS`, a `DecisionMemo`, or a typed new-risk
`OperationProposal`. The memo cites supporting and opposing Evidence, responds
to the Challenge, names unresolved facts, binds the Strategy revision, and
states invalidation/exit intent.

WAIT, PASS, and PROPOSE are all attributable behaviors when human-owned
evaluation policy defines their qualified behavior class as mandatory. The
active Strategy defines the legitimate qualified opportunity universe and
objective semantics; it cannot remove an unfavorable decision from evaluation.
The Decision Artifact binds the complete qualified opportunity, prediction
distribution or approved categorical forecast, confidence scale, horizon,
benchmark/no-action comparator, expected reward/risk/cost, decision graph, and
evaluation Contract. Not trading does not erase the decision from delayed
evaluation.

It cannot call a broker, declare Kernel-derived risk, modify limits/Strategy/
GRACE/Delegation, skip required Challenge, rewrite Evidence to fit a trade,
approve Class C or Live canary, or perform all primary research itself.

### Position Manager

Owns monitoring intent for existing positions under their entry-time Strategy,
Playbook, Thesis, exit/invalidation, and Evidence revisions. It consumes
canonical Kernel position/order/fill/breaker/unknown state plus relevant current
Evidence and produces `PositionMonitorReport`, WAIT, human attention, or a
risk-reducing close/cancel/tighten intent.

It cannot open/add/reverse, rewrite the original Thesis or historical Exit Plan,
declare safe close side/quantity, retry/release broker ambiguity, or propose
martingale recovery. New risk returns to Decision Desk. Hard prices, absolutes,
verified close normalization, and unknown effects remain Kernel-owned; an LLM
monitor is never the only safety layer.

HOLD/WAIT, human escalation, close/cancel/tighten intent, and material monitor
conclusions are registered under the entry-time management/evaluation Contract
where scoreable. Evaluation preserves the declared objective and risk path; it
does not judge an exit solely by the highest price reached afterward.

### Coach

Runs only after canonical reconciliation. It consumes original decision and
Evidence, Strategy/Agent revisions, fills/PnL, risk units, execution path,
MAE/MFE, compliance, and operational events. It produces `PostMortem`, Candidate
Attribution/Lesson, and bounded Strategy/Skill/Prompt research questions.

It distinguishes strategy, data, execution, rule violation, normal variance,
and unknown attribution. It cannot award authority for writing quality, turn
one outcome into a rule, edit source rationale, write Active Memory/Playbook/
GRACE/Delegation, or force attribution before reconciliation.

Coach cannot edit the pre-outcome decision graph or allocate credit after the
fact. Candidate Attributions and Lessons are hypotheses. They receive no
immediate market-credibility reward and become scoreable only under a future,
independently frozen out-of-sample validation protocol.

## On-demand Specialist roles

- **Market Regime Analyst:** macro, rates, volatility, liquidity, rotation, and
  risk appetite.
- **Fundamental/Valuation Analyst:** financials, guidance, unit economics,
  capital structure, valuation, and peers.
- **Filings/Governance Analyst:** regulatory filings, financing, repurchase,
  insiders, governance, litigation, and regulatory matters.
- **Catalyst/News Analyst:** event timelines, source quality, expectations, and
  company/product/legal/macro catalysts.
- **Industry/Supply-Chain Analyst:** value chain, bottlenecks, competitors,
  suppliers/customers, concentration, and pricing power.
- **Technical/Market-Structure Analyst:** interpretation of deterministic
  price/volume/volatility/liquidity/relative-strength evidence.
- **Options/Volatility Analyst:** chain, Greeks, volatility surface/term
  structure, event pricing, and contract liquidity.
- **Portfolio Scenario Analyst:** qualitative scenario, factor, correlation,
  concentration, and portfolio interaction using exact Kernel facts.
- **Strategy Researcher:** cross-case aggregation, falsifiable Hypothesis,
  Candidate Lesson, Playbook/Strategy Candidate, and experiment design under
  `PLAYBOOK.md`.

Specialists produce scoped Artifacts, not OperationProposals. Task Planner and
Capability Resolver instantiate only relevant roles; every candidate does not
run every Specialist.

When a Specialist Artifact asserts a forecast rather than an interpretation,
it follows the same target, confidence, horizon, benchmark, invalidation, and
evaluation-registration requirements as other scoreable behaviors. A role
cannot avoid evaluation by hiding a directional assertion in prose.

## Behavior registration and delayed evaluation

GRACE evaluates committed behavior after its predeclared horizon; it does not
grade an Agent's private reasoning or ask Coach to decide who deserves credit.
The Control Plane must atomically commit, or transactionally outbox, every
required `BehaviorEvent` with the Artifact that can influence a downstream
decision.

Role Packages therefore declare:

```text
which human-owned mandatory behavior classes the Role implements
which fields are required for each type
the confidence/probability or categorical scoring semantics
the applicable Strategy/evaluation Contract and allowed horizons
benchmark, no-action, counterfactual, and missing-data direction
credit-attribution role in the decision graph
which deterministic integrity/compliance events are immediate
```

Deterministic code derives the immutable `EvaluationTicket`. An Agent cannot
choose `not_scoreable`, change the horizon/benchmark after observation, remove
a losing WAIT/PASS/forecast, or claim another role's objective. Non-scoreable
operational Tasks remain under ordinary reliability and audit metrics rather
than being forced into financial GRACE.

The architectural boundary is frozen in `GRACE.md`; the proposed Role payloads,
record semantics, scoring rules, and acceptance probes are specified in
`GRACE_QUANTITATIVE.md`. That Draft still requires independent model-risk
review and a signed Calibration Pack before implementation.

## Permission direction

| Role | Research | Memory | Strategy | Operation intent |
|---|---|---|---|---|
| Task Planner | Capability catalog only | None | None | None |
| Data Desk | Evidence planning and scoped data | None | None | None |
| Scout | Discovery scope | Candidate Artifact only | None | None |
| Specialists | Task-scoped read/research | Interpretation Artifact | None | None |
| Lead Analyst | Candidate-scoped research | Thesis Artifact | None | None |
| Challenger | Read/request within challenge | Challenge Artifact | None | None |
| Decision Desk | Read/request | Decision Artifact | None | New-risk proposal only |
| Position Manager | Position-scoped evidence | Monitor Artifact | None | Risk-reducing proposal only |
| Coach | Reconciled outcomes | Candidate Lesson | Suggestion only | None |
| Strategy Researcher | Historical/research | Candidate knowledge | Candidate revision | None |

This table is a maximum direction, not a grant. All runtime permissions remain
intersections with User, Skill, Task, Run, deployment, and policy.

## Opportunity workflow

```text
User/schedule/event
  -> Task Planner
  -> current Regime Snapshot
  -> Data Desk Evidence Plan
  -> diverse discovery capabilities
  -> Scout CandidateSet
  -> candidate Evidence and relevant Specialists
  -> Lead Analyst PrimaryThesis
  -> Challenger
  -> at most one scoped rebuttal
  -> Decision Desk WAIT/PASS/PROPOSE
  -> qualifying Artifact + canonical BehaviorEvent
  -> GRACE intake acknowledgement
  -> Proposal Validator
  -> active DelegationGrant OR exact-confirmation ticket route
  -> Kernel Gate
```

Research uses the staged funnel in `RESEARCH_DATA.md`; deep Specialists run only
for surviving relevant candidates.

## Position workflow

```text
Kernel fill/position/order/breaker/unknown or material event/schedule
  -> Position Manager
  -> current position Evidence
  -> PositionMonitorReport
  -> WAIT / human attention / risk-reducing proposal
  -> Kernel revalidation
```

Adding, reversing, or otherwise creating new risk starts a Decision Desk
workflow. A Strategy requiring unavailable Agent monitoring cannot open new
autonomous risk; existing positions remain protected by Kernel hard safety.

## Learning workflow

```text
reconciled Kernel outcome
  -> Coach PostMortem
  -> MemoryCandidate
  -> Strategy Researcher
  -> Playbook/Strategy Candidate
  -> independent Challenge and Validator
  -> StrategyActivationAuthority (initial/material: Human Strategy Owner)
  -> GRACE delayed evaluation
  -> Delegation Policy review where autonomous authority is requested
```

Learning is asynchronous and its outage cannot impair Kernel safety or existing
position truth.

The rating path begins at behavior time and runs independently of Coach:

```text
scoreable Artifact/decision
  -> BehaviorEvent + EvaluationTicket
  -> predeclared horizon and evidence maturity
  -> real market/Kernel outcome
  -> GRACE ScoreSnapshot
```

## Trigger and priority direction

Eligible triggers include user requests, policy-configured market schedules,
Kernel position/order/fill/breaker/unknown events, material external events,
Strategy Lab/Memory maintenance, and human requests.

Priority classes are deterministic and human-owned. Existing-position safety,
Kernel unknown/breaker handling, explicit user waiting work, material position
events, scheduled research/discovery, and maintenance are ordered by reviewed
policy rather than Agent prose. Exact frequencies and priorities remain to be
specified.

Each Role Contract later freezes trigger eligibility, occurrence/dedupe identity,
required data freshness, deadline, concurrency, missed-run behavior, and budget.

## Independence and groupthink controls

- Primary, Challenger, and Validator cannot be the same Session/AgentRevision.
- Challenger has original Evidence access and does not rely solely on the
  Primary summary.
- Shared underlying sources remain linked; multiple Agents repeating one source
  do not count as independent confirmation.
- Decision Desk cites opposing Evidence and unresolved findings.
- Different models may improve diversity for high-risk review but do not create
  independent evidence by themselves.
- Facts are not decided by majority vote, confidence, verbosity, or consensus.
- Failed, rejected, denied, and untraded cases remain represented in the
  evidence/learning stream.
- Agent identity changes cannot erase Artifact lineage, failure, or history.

## Failure and substitution

- Unavailable AgentRevision: Scheduler may substitute only another revision
  satisfying the same Role Contract, capability, permission, and independence
  requirements, with substitution recorded.
- Missing required Specialist/capability: surface the gap; do not let an
  unqualified Agent impersonate it.
- Challenger unavailable: WAIT or issue a no-trade PASS where Challenge is
  required. A human may supply the typed independent-review Artifact only when
  the frozen RoleContract explicitly permits that reviewer class; ordinary
  approval or exact trade confirmation cannot waive mandatory Challenge,
  Validator, or Evidence requirements.
- Decision Desk unavailable: produce no new-risk proposal.
- Position Manager unavailable: Kernel safety continues; monitor-dependent new
  autonomous opens are blocked and surfaced.
- Data/source unavailable: fail/wait the Evidence-dependent Task rather than
  treating stale cache as current.
- Coach/Strategy Researcher unavailable: learning is delayed without affecting
  operations or positions.

## No LLM Risk Officer

The architecture intentionally does not add a separate LLM with hard risk veto.
Kernel owns hard risk and the deterministic Delegation Policy/Gate owns scoped
autonomous authorization. GRACE supplies delayed credibility evidence only. A
new LLM Risk Officer would duplicate authority and create false assurance.
Qualitative thesis/portfolio risk is covered by Challenger and Portfolio
Scenario Analyst; hard enforcement remains Kernel-only.

## Required later specification

Before implementation, freeze each Role Package schema and exact input/output
Contracts, prompts and revision process, Skill/Tool/memory scopes, Scheduler
eligibility, independence graph, trigger/schedule/dedupe policy, budgets,
substitution, workflow state machines, Agent health, scoreable behavior map,
required forecast fields, evaluation registration, and attribution links.
Acceptance must cover permission escape, role impersonation, duplicate work,
unavailable required review, shared-source false consensus, self-validation,
outcome-aware registration/horizon changes, selective behavior omission,
double-counted credit, new-risk/position-role separation, prompt injection,
model/role substitution, and Kernel isolation.
