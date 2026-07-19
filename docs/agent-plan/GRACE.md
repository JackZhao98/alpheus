# GRACE — Grounded Retrospective Agent Credibility Evaluation

> Status: **FROZEN ARCHITECTURE — delayed evaluation, evidence and attribution
> boundaries, score ownership, and model-promotion lifecycle are authoritative.
> The proposed quantitative design now lives in `GRACE_QUANTITATIVE.md`, but it
> remains a Draft pending independent model-risk review, exact machine schemas,
> a signed Calibration Pack, and implementation authorization. Any downstream
> Live authorization mapping remains separate and unauthorized.**

> Mixed-control note: when a human/external action shares an order, position,
> or economic outcome with an Agent, the additional frozen attribution boundary
> in [`GRACE_MIXED_CONTROL.md`](GRACE_MIXED_CONTROL.md) applies. Actual PnL is
> preserved once; control and behavior evidence are segmented rather than
> multiplied by an arbitrary intervention weight.

GRACE stands for **Grounded Retrospective Agent Credibility Evaluation**. Its
descriptive name is the **real-outcome delayed credibility rating system**.

GRACE is deterministic code and is independent of the Agent system. It records
an attributable behavior before its result is known, waits for a predeclared
evaluation horizon and evidence-finality conditions, then weighs demonstrated
credibility against the real economic outcome and risk path. It publishes a
versioned rating with uncertainty; it does not call a broker, issue a grant, or
treat LLM prose as an authoritative score input.

The system evaluates more than completed trades. Discoveries, forecasts,
challenges, WAIT/PASS decisions, proposals, and position-management decisions
may all become scoreable behaviors when their Role and Strategy Contracts
define an objective evaluation protocol. Outcomes may be executed Live facts
or explicitly labeled real-market counterfactual observations; the two are
never interchangeable.

This document freezes what GRACE may evaluate, which facts must be captured
before an outcome, how delayed outcomes mature, how multi-Agent contribution is
represented, and how a candidate rating model may become active. The proposed
schemas, scoring-rule families, model candidates, and rating lattice are in
`GRACE_QUANTITATIVE.md`; they remain non-authoritative until independent
model-risk review. Numerical policy requires its separately signed Calibration
Pack.

## Non-negotiable boundary

GRACE contains only evaluation components:

| Component | LLM | Responsibility |
|---|---:|---|
| Behavior Intake Validator | No | Validate an immutable scoreable behavior and derive its evaluation ticket from frozen Contracts |
| Maturity Scheduler | No | Open evaluation only after the predeclared horizon and evidence-finality rules are satisfied |
| Outcome and Credibility Engine | No | Reproduce economic outcomes, calibration, risk, severity, credibility, and deterioration measures |
| GRACE Validator | No | Independently replay inputs, challenge assumptions, and approve a model revision for publication |
| GRACE Advisor | Optional | Explain published structured results and suggest research questions; never score, grant, or mutate policy |

The Intake Validator, Scheduler, Engine, and Validator live outside the Kernel
money path and have no broker mutation credentials. Agent Runtime may submit
typed behavior data and read published evaluations, but it cannot write a
rating, alter an evaluation ticket, choose whether an unfavorable behavior is
scoreable, or activate a GRACE model revision.

GRACE does **not** contain the authorization system:

- the separate **Delegation Policy Engine** maps a complete compatible
  `ScoreSnapshotBinding` set plus human-owned policy into an
  `AuthorizationProposal`;
- a privileged approval path creates an active authorization revision;
- the **Kernel Delegation Gate** validates and enforces that revision together
  with all stricter Kernel limits;
- GRACE never signs a grant, changes an account limit, approves an order, or
  decides that a risk-creating operation may execute.

Those downstream responsibilities are frozen separately in `DELEGATION.md`
and `DELEGATION_POLICY.md`. A GRACE rating is evidence for authorization
policy; it is not authority.

## Frozen invariants

1. Market-judgment evaluation is delayed. A behavior, prediction, horizon,
   comparator, and evaluation protocol are committed before its outcome is
   observable.
2. GRACE evaluates attributable behavior and versioned decision policies; it
   never gives an unscoped Agent persona a free-standing pool of capital.
3. The Agent, Role, Strategy, prompt/model, evidence, regime, and policy that
   generated an observation are inseparable from that observation.
4. An Agent cannot opt out of evaluation, revise its intended horizon, lower
   its confidence, change its benchmark, or rename its identity after seeing
   the result.
5. Executed Live profit, unrealized marks, execution-aware Shadow results, and
   observed market counterfactuals are different evidence classes. Invalid or
   censored is an orthogonal validity state, not another provenance class.
6. Profit alone is not credibility. Rating must account for calibration,
   exposure, cost, drawdown, adverse path, tail risk, policy compliance, and
   uncertainty.
7. GRACE cannot evaluate, validate, approve, deploy, or authorize itself.
   Production model learning is Champion/Challenger, never in-place mutation.
8. Missing, stale, selectively observed, operationally ambiguous,
   unreconciled, or censored evidence can only preserve uncertainty or weaken
   support; it cannot be filled with invented success.
9. Shadow and Live evidence are distinct ledgers. Any credibility transfer is
   explicit, discounted, versioned, and capped.
10. No live-money randomized exploration is permitted for the purpose of
    improving GRACE.
11. Market-performance evaluation is delayed, but deterministic integrity,
    compliance, fabrication, and permission violations are recorded
    immediately and cannot be offset by later profit.
12. No Agent, Memory item, Playbook prose, Coach narrative, or GRACE score can
    raise a human-owned or Kernel absolute limit.

## Delayed behavior lifecycle

The rating lifecycle is anchored at behavior time rather than trade close:

```text
typed Agent Artifact or deterministic decision
  -> committed BehaviorEvent
  -> derived immutable EvaluationTicket
  -> pending until predeclared horizon and finality conditions
  -> MaturedBehaviorOutcome built from real market and Kernel facts
  -> reproducible GRACE evaluation
  -> scoped ScoreSnapshot
  -> optional downstream Delegation Policy review
```

A qualifying Artifact, its canonical `BehaviorEvent`, and the delivery outbox
row must be committed in one Agent Control Plane owner transaction before the
Artifact can influence another decision or reach Kernel proposal flow. The
outbox delivers the already committed BehaviorEvent to GRACE; it is not a
substitute for BehaviorEvent persistence. Registration after the outcome
becomes observable is invalid.

An `EvaluationTicket` has a state equivalent to:

```text
registered -> pending -> mature -> evaluated
                    \-> censored
                    \-> invalidated
```

Corrections create a superseding behavior before its own outcome is known.
They do not rewrite the prior record. A cancellation, supersession, or changing
market condition may affect eligibility under the frozen protocol, but it
cannot erase the original behavior or its audit trail.

A behavior may have one primary and bounded secondary evaluation horizons.
Every horizon and its purpose are declared at registration. An Agent cannot
add a favorable horizon or discard an unfavorable horizon afterward. If the
Role/Strategy Contract does not define a valid horizon for a purported market
forecast, the output cannot earn market credibility and cannot support an
autonomous proposal.

Evaluation maturity is not merely elapsed wall time. It may require market
calendar completion, final price availability, corporate-action adjustment,
order reconciliation, fee finality, or position-close facts. Market-path
evaluation at a fixed horizon and final realized trade PnL are separate outcome
records when they mature at different times.

## Agent-side behavior contract

The Agent system must preserve enough pre-outcome structure to make delayed
evaluation possible. This architecture summarizes the minimum fields; the
proposed Role-discriminated record semantics are in
`GRACE_QUANTITATIVE.md`. Every scoreable `BehaviorEvent` binds fields
equivalent to the following.

### Identity and causal lineage

```text
behavior_id and behavior_schema_revision
occurred_at, committed_at, and market_time_zone
RunOriginRef, run, task, session, attempt, and artifact ids
conversation and user_request ids only for user_request origin
AgentRevision and RoleContract revision
parent behavior, opportunity, claim, decision, proposal, operation, and
position references where applicable
decision-graph and contributor/opponent references
```

### Decision content

```text
behavior_type and action/stance
subject entity, universe, instrument, product, and ledger scope
machine-readable prediction target
direction, range/distribution, probability, and calibrated confidence scale
primary and secondary horizons
benchmark, comparator, and no-action baseline
expected reward, risk, cost, capacity, and material scenarios
claim, thesis, mechanism, invalidation, and exit/management references
WAIT/PASS/PROPOSE or equivalent disposition and reason codes
```

Confidence is not an unrestricted prose number. Its scale, semantics, and
calibration revision must be defined by the applicable Contract. When a role
cannot make a defensible probability forecast, it records a bounded categorical
claim under an approved scoring rule rather than fabricating precision.

### Point-in-time context

```text
Evidence Snapshot and as-of time
market/regime and feature Snapshot revisions
Strategy, Playbook, prompt, model, Skill, Tool, and configuration revisions
Kernel account/position/order references visible at decision time where needed
EffectiveRunAuthority, deployment, policy, and delegation context;
authenticated user context only iff RunOrigin=user_request
known gaps, conflicts, stale inputs, and unresolved unknowns
Live, Shadow, Simulation, or Research evidence class
```

For schedule, event, maintenance, or recovery behavior, `RunOriginRef` binds the
registered occurrence, authenticated workload, and owner-policy authority.
Conversation/UserRequest placeholders are forbidden. Recovery retains the
original causal/effect identity and cannot be reclassified as a new prediction
or new-risk decision.

### Evaluation registration

```text
evaluation Contract and protocol revision
scoreable behavior class and required objective fields
evaluation horizons and evaluate-not-before times
market calendar, price/path sampling, benchmark, and adjustment rules
approved evaluation market-data source hierarchy and Provider revisions
transaction-cost, slippage, and capacity treatment
counterfactual eligibility and measurement protocol
censoring, missing-data, invalidation, and reconciliation rules
credit-attribution method and contributor roles
```

The Agent supplies typed semantic content. Deterministic code derives
scoreability and the final ticket from the Role, Strategy, and evaluation
Contracts. An Agent cannot mark an otherwise required behavior `not_scoreable`
or choose its own scoring rule.

## Evaluation ticket contract

The immutable `EvaluationTicket` freezes fields equivalent to:

```text
ticket_id, behavior_id, ticket revision, and registration time
evaluation Contract and Evaluation Profile revisions
primary/secondary horizon ids and maturity criteria
evaluate_not_before and next evaluation time
benchmark/comparator and market-path sampling rules
approved market-data source hierarchy and revision compatibility
price, corporate-action, FX, fee, slippage, and cost conventions
behavior evidence class and allowed outcome evidence classes
counterfactual and censoring protocol
required Kernel reconciliation/finality state
eligible metrics and prohibited interpretations
immutable GRACE ModelBindingPlan: primary/fallback/deadline/compatibility
supersession references
```

Ticket creation must reject a horizon that is already observable, a benchmark
selected after the decision, missing required point-in-time context, or a
behavior/model mismatch. Append-only ticket-state events may advance the
deterministic lifecycle or add evidence references; they cannot change the
frozen ticket or its evaluation meaning.

Append-only binding-state events select, invalidate, or apply a predeclared
fallback under the immutable Plan; binding state is not rewritten inside the
ticket.

The compatible Champion active at registration is bound to the ticket. When no
first Champion or no compatible model exists, an explicit unassigned/
unsupported binding preserves the behavior but cannot produce favorable
`current_authority` evidence. If a new Champion activates before maturity, the
original bound revision may produce a `historical_bound` evaluation/Snapshot,
not current authority evidence. A later model may produce a labeled diagnostic
replay, but cannot choose itself after the outcome, replace the bound result,
or silently absorb the pending ticket into a current-authority Snapshot.
Invalid-model and predeclared-fallback behavior is specified in
`GRACE_QUANTITATIVE.md`.

## Matured outcome contract

A `MaturedBehaviorOutcome` is built from versioned facts and includes fields
equivalent to:

```text
ticket and behavior references
outcome observation window and complete market path manifest
market-data sources, Provider revisions, observation times, and quality state
benchmark and excess outcome
outcome evidence class
realized Live net PnL from reconciled canonical lot/inventory accounting,
matched closed quantity, and signed cash-flow/basis-transfer manifests
benchmark-relative edge and actual-account risk units kept separate
unrealized marked PnL only in its distinct outcome class
hypothetical opportunity cost in a physically separate counterfactual field
return and loss in pre-effect Kernel-authoritative risk/reservation units
MAE, MFE, drawdown, volatility, gap, and tail-path measures
fill quality, latency, capacity, and execution divergence where applicable
forecast/probability calibration result
rule, integrity, stale-data, and operational events
counterfactual method, uncertainty, and limitations
censoring, missingness, invalidation, and reconciliation state
data Snapshot, code, configuration, and checksum manifest
```

For an executed behavior, broker-reconciled Live PnL is a realized fact. For a
WAIT, PASS, denied proposal, or unexecuted forecast, later market movement is a
real observed path but its hypothetical trade result remains counterfactual or
Shadow evidence. GRACE must never label it account PnL or give it full-strength
Live credibility.

Kernel remains authoritative for actual account, order, fill, fee, and execution
facts. An approved versioned evaluation-data source may supply the broader
market path for both traded and untraded cases. Its hierarchy, adjustment rules,
fallback behavior, and revision are frozen in the ticket; GRACE cannot choose a
more favorable data source after observing the result. Missing or materially
conflicting evaluation prices produce an explicit limitation, invalidation, or
censored outcome under the frozen protocol.

## Role-specific evaluation direction

Not every Agent action is financially scoreable, and roles are not judged by
one shared PnL formula. Each Role Contract declares which outputs register a
behavior and which objective rule applies.

| Role | Delayed scoreable behavior | Ground-truth direction |
|---|---|---|
| Data Desk | Predeclared coverage/freshness or conflict-resolution claims where objectively measurable | Later source availability, correctness, timeliness, and missed required coverage; not stock direction by default |
| Scout | Candidate discovery, rank bucket, reason type, horizon, and exclusion | Subsequent qualified opportunity outcome, lead time, precision/calibration, and the frozen discovery universe |
| Specialist / Lead Analyst | Explicit forecast Claims, mechanisms, scenarios, invalidations, and thesis horizons | Later facts and market path under the Claim's declared target and scoring rule |
| Challenger | Specific counterclaim, failure mode, probability, severity, and horizon | Whether the warned condition or invalidation occurred, with base-rate calibration so permanent pessimism is not rewarded |
| Decision Desk | WAIT, PASS, PROPOSE, sizing/risk intent, probability distribution, and horizon | Realized Live outcome when executed plus the complete qualified stream and separately labeled counterfactual outcomes |
| Position Manager | HOLD/WAIT, close, cancel, tighten, escalation, and management horizon | Post-decision path, avoided/admitted risk, execution, and compliance under the entry-time management Contract |
| Coach / Strategy Researcher | Candidate attribution, lesson, or falsifiable hypothesis | Only later independent out-of-sample validation; writing a plausible Post Mortem earns no immediate market credibility |

Task Planner and Control Plane are not investment forecasters. Their
reliability, cost, recovery, and contract-compliance metrics belong to normal
operational observability unless a separately reviewed objective GRACE protocol
is justified.

A role is not rewarded for taking another role's objective. Scout is not
credited as if it sized the trade; Challenger is not rewarded for vague
permanent negativity; Position Manager is not judged solely by the maximum
price reached after an exit; Coach is not rewarded for persuasive prose.

## Multi-Agent credit attribution

One market outcome may follow contributions from Scout, Data Desk,
Specialists, Lead Analyst, Challenger, Decision Desk, Position Manager, and a
Strategy revision. GRACE does not divide PnL equally or let a Coach assign
credit after the fact.

The committed decision graph preserves:

- direct decision owner and active Strategy/decision-policy revision;
- supporting, opposing, superseded, and unused Artifact/Claim ids;
- each contributor's pre-outcome prediction, confidence, horizon, and stated
  relevance;
- whether Decision Desk accepted, narrowed, rejected, or failed to address a
  material contribution;
- which behavior-specific scoring rule applies to each role;
- the predeclared team/strategy economic-outcome attribution method.

GRACE first scores a behavior against its own declared objective. It then may
aggregate through an approved hierarchical attribution model into scoped
Agent/Role, Strategy, and decision-pipeline credibility. Economic PnL belongs
primarily to the executed strategy/decision pipeline; individual contributors
receive credibility evidence only through their attributable forecast or
decision quality.

Where contribution is not identifiable, the outcome remains team/strategy
evidence or censored for individual credit. Uncertain attribution is not forced
to sum to 100%, and a later narrative cannot manufacture independent credit.

## Performative feedback and selection bias

GRACE is part of a performative system even though it does not itself grant
authority. Downstream delegation policy may use its ratings, changing which
opportunities become trades; those trades later become GRACE evidence. A
decision policy revision `theta` therefore induces an observed outcome
distribution `D(theta)` rather than sampling a fixed distribution.

This creates distinct hazards:

- **selective labels:** realized Live outcomes exist for permitted trades but
  not for the denied Live counterfactual;
- **policy-induced distribution shift:** a larger or smaller envelope changes
  strategy behavior, concentration, execution, and the mix reaching
  evaluation;
- **role-selection bias:** the planner and scheduler change which Agents and
  capabilities contribute to each case;
- **survivorship and reporting bias:** favorable Artifacts may be cited while
  rejected, superseded, or ignored predictions disappear from summaries.

Repeatedly fitting an active model to outcomes selected by a policy consuming
that model is forbidden. Convergence, if observed, would establish only a
self-consistent loop under its induced data, not an optimal or safe rating.
GRACE is tiered and constrained, trading data is sparse and heavy-tailed, and
markets are non-stationary; ordinary performative-prediction convergence
assumptions do not hold by default.

Every evaluation must therefore reconstruct:

- the decision and downstream delegation revisions that generated each case;
- the effective risk envelope and whether it was permitted, denied, expired,
  or never qualified;
- the Role/Agent selection and decision propensity when a reviewed policy
  defines one;
- the market, Strategy, prompt/model, Tool, and Evidence versions known at
  behavior time;
- which outcomes are Live facts, unrealized marks, Shadow estimates, observed
  counterfactual paths, with invalid/censored recorded as an orthogonal
  validity state;
- coverage limitations created by the generating policy and missing data.

An evaluation that cannot reconstruct this manifest is invalid and cannot
support a GRACE model promotion or downstream authority increase.

## Canonical inputs

GRACE consumes immutable facts rather than self-reported Agent success:

- the full required `BehaviorEvent` and `EvaluationTicket` stream, including
  WAIT, PASS, denied, expired, superseded, and untraded cases;
- Agent, Role, Strategy, Playbook, prompt/model, Skill, Tool, and evaluation
  Contract revisions;
- opportunity, Claim, decision graph, Artifact, Evidence, and Snapshot lineage;
- operation, authorization, reservation, order, fill, position, and
  reconciliation records;
- authorized exposure and risk known before the outcome;
- realized PnL, fees, slippage, MAE/MFE, holding time, drawdown, and market path;
- rule violations, stale evidence, PnL divergence, fabrication, permission
  violations, and unknown effects;
- market regime and features known at behavior time;
- Shadow follow-up for denied qualified cases under a versioned counterfactual
  protocol;
- the generating decision/delegation record that caused each case to be
  selected, ignored, or censored.

Coach and other LLMs may submit `candidate_attribution`, but it is untrusted.
It cannot alter a behavior, outcome, rating, permission, or limit unless a
separately specified deterministic or independently validated process converts
part of it into a categorical fact.

## Evaluation units and scoped scorecards

GRACE does not maintain one global, context-free "AI score". Atomic evidence is
keyed at least by:

```text
behavior_type × AgentRevision × RoleContract × optional StrategyVersion
× market_regime × horizon
× behavior_evidence_class × outcome_evidence_class
```

The quantitative model may use reviewed hierarchical credibility methods to
pool sparse experience without pretending all dimensions are identical.
Published scorecards may roll up into:

- individual Behavior evaluation;
- AgentRevision-within-Role credibility;
- StrategyVersion-within-Regime credibility;
- decision-pipeline/team credibility;
- carefully defined broader reference classes.

Every roll-up exposes its component scope, effective exposure, uncertainty,
transfer/decay assumptions, and excluded/censored cases. A material prompt,
Strategy, model, Tool/data, Contract, or permission change reduces or resets
inherited credibility under a human-reviewed transfer policy. Renaming an
Agent or Strategy cannot erase history.

Opportunity, decision, behavior, trade, and outcome remain separate records. A
denied opportunity is not a zero-return trade; a forecast evaluated at five
days is not the same record as the eventual closed-position PnL; a Shadow
outcome is not realized Live profit.

## Score output contract

GRACE publishes a structured `ScoreSnapshot`, never an authorization or prose
verdict. It includes fields equivalent to:

```text
score_snapshot_id, as_of, effective evidence cutoff, and expiry/review time
subject type/id and complete segmentation scope
GRACE Champion model revision and input manifest
behavior count, exposure, effective sample size, and maturity coverage
credibility estimate/posterior and uncertainty interval
real economic outcome, risk-unit, frequency, and severity components
calibration/proper-score components
frequency, severity, drawdown, clustering, and tail-risk components
operational integrity/compliance facts kept distinct from market variance
Live/Shadow/counterfactual composition and transfer discounts
censoring, missingness, regime, concentration, and selection limitations
attribution method and contribution uncertainty
deterioration, staleness, invalid-model, and requalification flags
machine-readable drivers plus immutable replay references
```

The quantitative Draft defines a multidimensional scorecard and
non-compensatory joint grade. The output retains every component and its
uncertainty; no scalar may hide a profitable tail-risk profile, small sample,
poor calibration, policy violation, or unidentified counterfactual.

The optional Advisor may translate a published snapshot into concise prose and
propose research or Playbook questions. Advisor prose is never fed back as a
score input or authorization fact.

## Parameter ownership

GRACE separates human choices from estimable quantities:

| Class | Examples | Automatic update |
|---|---|---:|
| Human-owned evaluation policy | eligible behavior classes, prohibited uses, required horizons, model-risk burden | Never |
| Kernel invariants | reservation, reconciliation, ambiguity, and broker mutation rules | Never |
| Downstream delegation policy | capability templates, product permissions, fixed risk envelopes, categorical evidence-to-eligibility mapping | Never by GRACE |
| Approved GRACE model form | likelihood, prior family, hierarchical pooling, attribution, transfer and decay form | No; revision requires model-risk review |
| Empirical parameters | exposure, calibrated frequency/severity, execution and outcome distributions | Challenger only |
| LLM interpretation | narrative attribution, hypotheses, explanations, candidate lessons | Never authoritative |

An empirical update may change a Challenger estimate only inside the approved
model revision. It cannot change its objective, prior family, loss function,
evaluation horizon, attribution method, downstream authorization mapping, or
promotion criterion.

## Actuarial and statistical requirements

Implementation must use a reviewed actuarial/statistical model, not an
arbitrary weighted sum. It must:

- use credibility/exposure methods suitable for sparse, heterogeneous
  experience with explicit priors and variance assumptions;
- separate frequency and severity where appropriate;
- use proper scoring/calibration methods for probabilistic forecasts;
- compare economic outcome against the frozen benchmark, cost, exposure, and
  risk budget rather than raw terminal return alone;
- model heavy tails, loss clustering, gaps, and drawdown rather than assuming
  simple normal returns;
- report posterior distributions or confidence intervals, not only point
  estimates;
- evaluate expected shortfall and the probability of breaching drawdown and
  loss constraints;
- shrink small samples toward a conservative documented reference experience;
- separate Live, Shadow, unrealized, and counterfactual credibility and define
  any transfer discount explicitly;
- address hierarchical Agent/Role/Strategy attribution without double-counting
  one economic outcome;
- account for selection, repeated testing, regime change, planner/role choice,
  and backtest overfitting;
- validate calibration, coverage, stability, and outcomes independently;
- preserve the exact data Snapshot, code revision, configuration checksum,
  Contract, and model revision needed to replay every result.

No numerical weight, prior, distribution, sample threshold, horizon, rating
boundary, or tail-risk limit discussed informally is authorized by this
architecture. Each must be derived from data, stress testing, and independent
actuarial/model-risk review before a score can affect downstream Live policy.

## Immediate integrity and delayed market evidence

GRACE keeps two evidence clocks distinct:

- **delayed market evidence** matures under the EvaluationTicket and evaluates
  forecast quality, economic outcome, risk path, and calibration;
- **immediate integrity/operational evidence** records fabrication, permission
  escape, omitted mandatory review, stale-data misuse, contract violation, or
  unresolved broker effects as soon as deterministically established.

Immediate evidence may trigger a downstream safety review without waiting for
market maturity. It does not retroactively rewrite the market outcome. A lucky
profit cannot offset a fabrication or rule violation, while an unlucky outcome
does not by itself prove that a calibrated decision process was dishonest or
invalid.

## Threat model

The quantitative specification and acceptance suite must assume:

- an Agent omits a prediction, delays registration, changes its horizon,
  exaggerates attribution, suppresses bad context, or changes identity to
  escape poor history;
- a role produces vague untestable claims or permanent pessimism to collect
  selective credit;
- multiple Agents claim the same PnL or a Coach reallocates credit after seeing
  the result;
- a Strategy earns money while violating policy or accumulating hidden tail
  exposure;
- downstream authorization censors counterexamples needed to evaluate the
  rating that informed it;
- repeated Strategy, prompt, or GRACE model selection overfits the same
  history;
- Shadow/counterfactual execution is systematically more favorable than Live;
- delayed fills, broker ambiguity, missing prices, corporate actions, or
  reconciliation errors corrupt apparent outcomes;
- regime change invalidates priors, transfer assumptions, or calibration;
- a malformed, stale, mismatched, concurrent, or partially written behavior,
  ticket, outcome, or ScoreSnapshot reaches publication;
- the trainer, evaluator, Advisor, or Agent attempts to promote its own output;
- one profitable run or short sample encourages unsafe downstream authority.

Each threat requires a fail-closed response and an acceptance probe before the
corresponding delivery stage advances.

Candidate quantitative specifications must compare at least a conservative
credibility model and one robust alternative. Model selection cannot rely only
on in-sample PnL. Calibration, coverage, stability, tail behavior, attribution,
decision usefulness, and regime-shift failure are assessed separately.

## Champion, Challenger, and Validator

- **Champion** is the single active, immutable GRACE evaluation-model revision
  used to publish `current_authority` ScoreSnapshots. It does not update while
  active.
- **Challenger** is a candidate model revision evaluated offline and in Shadow
  on the same timestamped behavior/opportunity stream. It has no authority to
  trade, grant, approve, overwrite ratings, or modify the Champion.
- **Validator** is an independent deterministic service and review process. It
  reproduces evaluation, challenges assumptions, applies model-promotion
  gates, and prepares a signed model-promotion decision. It does not train the
  Challenger.

The three use distinct credentials and write paths. The Engine may produce
Challenger artifacts, but only the privileged promotion path creates a new
active model revision. The prior Champion remains replayable and available for
immediate rollback. Model promotion never directly changes a delegation grant.

The immutable `GRACEModelRevision` body contains no mutable state, effective/
retired time, approval, signature, or activation field. The independent
Validator writes `GRACEValidatorAttestation`; the authenticated model-risk path
writes `ModelRiskDecision`; the Activator appends `ModelStateEvent` and CAS-
advances the fenced `ActiveGRACEChampionHead` from their exact digests. These
roles have disjoint credentials and concurrent promotion has one winner.

## Challenger evaluation protocol

1. Freeze the evaluation window, input manifest, generating behavior/delegation
   revisions, Champion, and Challenger before scoring.
2. Replay Champion and Challenger on the same eligible behavior and qualified
   opportunity streams.
3. Keep realized Live, unrealized, execution-aware Shadow, and observed
   counterfactual provenance distinct, with invalid/censored validity tracked
   orthogonally.
4. Use predeclared metrics and model-promotion gates. Report uncertainty,
   exposure, regime/role coverage, selection limitations, and tail outcomes.
5. Test sensitivity to attribution, missing counterfactuals, transaction costs,
   delayed data, extreme losses, regime labels, horizon choices, and pooling.
6. Run historical replay and forward observe-only comparison before any
   downstream policy consumes a Challenger.
7. Require Validator reproduction and human model-risk approval for every
   Champion change.
8. Activate a promoted model atomically with effective time, compatibility
   scope, rollback target, and complete audit manifest.

GRACE must not use real-money A/B randomization to manufacture counterfactual
evidence. Where causal comparison or individual contribution is not
identified, the result says so and remains conservative.

## Model update and feedback monitoring

An active Champion never tunes itself in place. New matured evidence updates
ScoreSnapshots under the same frozen model; it does not change model weights or
form. A proposed model/parameter change creates a Challenger revision.

Promotion is a bounded, independently reviewed model transition. A newly
promoted model first publishes parallel observe-only snapshots. Downstream
Delegation Policy remains pinned to its approved compatible GRACE model until a
separate human-owned policy transition accepts the new revision.

Tickets registered before the new Champion's effective time retain their
original model binding. This may slow evidence continuity across promotion, but
prevents outcome-time model selection and preserves every prior Snapshot.

The Validator monitors whether downstream use of GRACE changes:

- opportunity acceptance and censoring;
- Strategy, Role/Agent, holding-time, concentration, and execution mix;
- regime and product coverage;
- frequency, severity, clustering, and tails;
- calibration and Live/Shadow/counterfactual disagreement;
- behavior registration, missingness, and attribution patterns.

Material policy-induced shift invalidates naive before/after comparison. It
triggers requalification, narrower applicability, or new model review rather
than automatic compensating weight changes.

## Downstream delegation boundary

`DELEGATION.md` and `DELEGATION_POLICY.md` own the policy that uses a complete
compatible ScoreSnapshotBinding set to determine eligibility for a bounded
human-owned capability template. That policy may define research-only, Shadow,
human-review, and autonomous Live display stages, but GRACE does not.

GRACE may publish deterioration, staleness, invalid-model, or requalification
flags. The Delegation Policy Engine determines their policy consequence under
a frozen rule. Kernel enforces the resulting active grant plus all stricter
limits. Human-confirmed trading authority and risk-reducing operations remain
separate paths defined by delegation and Kernel policy.

If GRACE is unavailable, stale, incompatible, or unable to mature necessary
evidence, downstream policy cannot infer a favorable score. It may preserve a
still-valid conservative grant or reduce/review it according to frozen policy;
it cannot increase authority.

## Post Mortem boundary

Coach creates a short structured Post Mortem linked to canonical evidence:

- result in pre-authorized risk units;
- what worked and failed;
- proposed root-cause category;
- Strategy versus execution versus variance attribution;
- one Candidate Lesson or Playbook action;
- confidence and Evidence references.

Completing prose earns no credibility. A Candidate Lesson receives learning
support only after independent validation and later out-of-sample evidence show
explanatory or predictive value. GRACE never grades writing quality with an LLM
or lets Coach rewrite the committed decision graph.

## Persistence and audit

The eventual schemas must provide immutable or append-only records equivalent
to:

- BehaviorEvents and EvaluationTickets;
- matured outcomes and complete market/reconciliation manifests;
- scoped behavior, Agent/Role, Strategy, and pipeline score events;
- GRACE model revisions and compatibility declarations;
- evaluations, ScoreSnapshots, and their input manifests;
- candidate attribution, Post Mortem, Playbook, and downstream delegation
  references.

An official ScoreSnapshot is created only through a privileged auditable path.
Agents cannot update, delete, supersede, backdate, or selectively suppress it.

Records preserve the qualified opportunity stream, decision graph, generating
delegation state, ignored/denied/censored reason, counterfactual method, and
Champion/Challenger comparison manifest. Retention and compact may summarize
LLM prose but never facts required to replay a rating.

## Delivery sequence

GRACE is delivered as independently reviewable stages:

1. **Behavior foundation:** BehaviorEvent, EvaluationTicket, decision graph,
   revision binding, complete opportunity stream, censoring, and Shadow/
   counterfactual contracts.
2. **Quantitative specification:** actuarial candidates, role-specific scoring,
   attribution, assumptions, estimation, calibration, tail model, transfer/
   decay, maturity, and validation.
3. **Offline intake and maturity:** deterministic registration, scheduler,
   outcome construction, and replay with no Kernel/broker write capability.
4. **Offline Engine:** Behavior and scoped credibility evaluation with no
   downstream authority effect.
5. **Validator:** independent reproduction, sensitivity/shift probes, signed
   model-promotion artifact, and rollback proof.
6. **Published observe-only scores:** official ScoreSnapshots visible to humans
   and Agents but ignored by Live delegation.
7. **Delegation Shadow integration:** separate policy consumes scores without
   affecting Live authority.
8. **Live delegation canary:** separately reviewed, human-approved, tightly
   capped use only after both GRACE and Delegation acceptance pass.

Stages 1-2 must be frozen before Engine implementation. Agent Platform work may
produce BehaviorEvents and consume ScoreSnapshots, but it cannot block Kernel
safety or create a parallel rating/authorization mechanism.

## Acceptance boundary before downstream Live use

GRACE cannot influence Live delegation until acceptance proves:

- every required scoreable Agent behavior is registered before its outcome;
- horizon, benchmark, confidence semantics, scoring rule, and attribution
  cannot be changed after registration;
- deterministic ticket maturity and replay from the recorded manifest;
- no Agent/LLM path can alter a behavior, outcome, rating, model revision, or
  official ScoreSnapshot;
- role-specific scoring prevents vague or wrong-objective credit;
- multi-Agent attribution neither double-counts PnL nor forces unidentified
  individual credit;
- sparse evidence remains conservatively pooled/shrunk with visible
  uncertainty;
- one lucky high-variance outcome cannot create a material rating increase;
- Champion and Challenger use the same qualified behavior/opportunity stream
  without conflating denied, censored, Shadow, counterfactual, and Live facts;
- a model trained only on policy-selected outcomes cannot be promoted;
- every observation binds its generating Agent, Role, Strategy, evaluation,
  and delegation revisions;
- rule-breaking profit cannot improve integrity or erase a violation;
- Shadow evidence cannot silently become full-strength Live evidence;
- concurrent evaluation/model promotion cannot create multiple Champions;
- stale, absent, invalid, mismatched, or unreconciled evidence cannot yield a
  favorable official score;
- GRACE cannot issue or mutate an authorization and never raises a human-owned
  or Kernel limit;
- policy-induced distribution shift invalidates naive before/after evidence;
- Champion rollback restores the exact prior model and reproducible snapshots;
- independent actuarial/model-risk review approves the implemented model,
  attribution, calibration, stress tests, and limitations.

## Required quantitative review

`GRACE_QUANTITATIVE.md` now proposes the Role payloads, maturity semantics,
forecast Contracts, benchmark/counterfactual protocols, attribution boundary,
actuarial candidates, rating lattice, and Validator probes requested by this
architecture. It remains a Draft: independent model-risk review, exact machine
schemas, representative reference data, and a signed Calibration Pack are
still required before implementation is authorized.

Until those requirements are accepted, GRACE remains documentation-only.
Until `DELEGATION.md` is separately implemented and accepted, no GRACE result
can alter Live authority.

## Research basis

The feedback boundary follows the performative-prediction distinction between
a fixed data distribution and one induced by deployment. See Juan C. Perdomo,
Tijana Zrnic, Celestine Mendler-Duenner, and Moritz Hardt, *Performative
Prediction*, ICML 2020. The paper motivates the hazard; it does not establish
that GRACE's constrained, non-stationary trading setting satisfies its
convergence assumptions.
