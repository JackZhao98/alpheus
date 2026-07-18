# Playbook and Strategy Evolution

> Status: **FROZEN ARCHITECTURE — knowledge layers, immutable Champion/Candidate
> lifecycle, experiment governance, position revision binding, and GRACE/
> Delegation separation are authoritative. Exact schemas, statistical methods,
> thresholds, parameter spaces, and implementation acceptance are not yet
> specified or authorized.**

## Purpose

Alpheus may capture experience, propose lessons, formalize trading hypotheses,
calibrate approved parameters, and design experiments. It may not mutate an
active production strategy in place or convert one profitable outcome into
authority.

Playbook evolution is versioned, evidence-backed, replayable, reversible, and
separate from GRACE delayed evaluation, Delegation authorization, and Kernel
risk enforcement.

## System boundaries

- **Memory** preserves episodes, candidates, and relevant prior knowledge.
- **Playbook** organizes human-readable mechanisms, setups, experience,
  applicability, and counterexamples.
- **Strategy** defines a versioned decision policy with structured inputs,
  behavior, parameters, and output Contract.
- **Strategy Lab** runs deterministic, reproducible experiments.
- **GRACE** independently performs delayed real-outcome credibility evaluation.
- **Delegation Policy** independently maps approved credibility evidence and
  human policy into bounded authorization.
- **Kernel** recomputes risk, enforces limits, and owns every broker effect.

No layer can silently assume another layer's authority. A lesson is not a
strategy; a strategy is not authorization; authorization is not execution.

## Frozen principles

1. Active Playbook and Strategy revisions are immutable Champions.
2. Learning produces Candidate/Challenger revisions, never in-place production
   mutation.
3. Narrative experience is preserved, but autonomous behavior requires a
   structured and machine-verifiable Strategy Contract.
4. Every change binds its Evidence, data Snapshot, code, configuration, model,
   prompt, Skill, and parent revision.
5. Strategy evaluation is point-in-time and includes selection, repeated
   testing, execution cost, tail risk, regime, and Shadow/Live limitations.
6. A strategy cannot choose its own promotion criteria, validate itself,
   activate itself, or grant itself risk authority.
7. A renamed or slightly modified strategy cannot escape history or reset
   GRACE without an explicit identity/transfer decision.
8. An open position remains bound to its entry-time strategy and management
   revisions unless a separately reviewed migration/action changes it.
9. Profitable rule violations are adverse operational evidence, not positive
   learning or credibility.
10. Playbook history remains bounded in active Context through versioned
    retrieval, not an indefinitely appended document.

## Strategy and Playbook forms

The system supports multiple forms without assuming machine learning:

- narrative mechanisms and practitioner experience;
- structured rule systems and event/setup conditions;
- actuarial, probabilistic, mathematical, or parameterized decision models;
- statistical or machine-learning models when later data and validation justify
  them.

Complexity is not evidence of quality. Every form must identify its inputs,
scope, time horizon, assumptions, failure behavior, and version.

## Three representation layers

### Narrative Doctrine

Human- and Agent-readable descriptions of mechanisms and experience:

- why an opportunity may exist;
- how events, market structure, regime, and company context may interact;
- what experienced patterns or counterpatterns matter;
- common failure modes, exceptions, and unanswered questions.

Narrative supports research and human judgment but cannot by itself authorize
autonomous Live proposals.

### Structured Setup

Formalizes a falsifiable hypothesis with fields equivalent to:

```text
setup identity and revision
eligible universe and instruments
applicable market regime and horizon
required Evidence and freshness
trigger and confirmation conditions
expected mechanism
invalidation and counterexamples
entry, exit, and time-stop intent
known limitations and missing information behavior
```

### Executable Strategy Contract

Required before a strategy can be considered for autonomous Live proposal
generation:

```text
exact input and output schemas
required Evidence/feature revisions and freshness
parameter/model revision
deterministic validation and unknown behavior
eligible instruments and strategy scope
entry, exit, and position-management intent
decision and observability contract
qualified opportunity and complete WAIT/PASS/PROPOSE stream contract
scoreable behavior and GRACE evaluation Contract
prediction target, confidence semantics, horizon, and benchmark/no-action basis
counterfactual, censoring, attribution, and maturity direction
```

Risk declarations remain intent. Kernel, GRACE, and Delegation boundaries
cannot be encoded away inside a Strategy Contract.

## Experience and lesson contract direction

A Candidate Lesson must preserve more than a slogan. It binds:

- observed phenomenon and affected cases;
- proposed mechanism and causal uncertainty;
- applicable entities, industries, regimes, and horizon;
- supporting Evidence and representative cases;
- counterexamples, contradictions, and selection limitations;
- confounders and missing data;
- falsification/invalidation conditions;
- proposed validation experiment;
- current validation and lifecycle state.

One case enters Episodic Memory. It cannot become a durable Playbook rule merely
because it was profitable, memorable, confidently narrated, or repeatedly
summarized by related Agents.

## Evolution lifecycle

```text
canonical decision and reconciled outcome
  -> Episodic Case
  -> Coach Post Mortem
  -> Candidate Lesson
  -> Strategy Researcher aggregation and falsifiable Hypothesis
  -> Playbook/Strategy Candidate revision
  -> independent Challenge
  -> point-in-time historical replay
  -> forward Shadow observation
  -> independent Validator reproduction
  -> StrategyActivationAuthority (initial/material changes: human owner)
  -> GRACE delayed evaluation / ScoreSnapshot
  -> Delegation authorization proposal where autonomous authority is requested
  -> bounded Live canary
```

Stages may reject, narrow, supersede, or return a Candidate to research. No
stage may be skipped merely because historical PnL is attractive.

## Change classes

Every revision identifies its material change class:

1. narrative/documentation clarification with no decision behavior change;
2. Candidate Lesson or Playbook mechanism/applicability change;
3. approved empirical parameter calibration;
4. trigger, feature, decision rule, or model-form change;
5. data source, normalizer, feature code, or freshness change;
6. exit or position-management behavior change;
7. risk, limits, or authorization change.

Classes 2-6 require a new attributable revision and proportionate
requalification. Class 7 is not owned by the Strategy: human policy,
Delegation, and Kernel own those changes. GRACE supplies credibility evidence
but does not grant authority. A Provider or feature change is material even when
the visible formula is unchanged because it can change the induced data and
signals.

## Parameter calibration boundary

Candidate generation may search or estimate parameters only inside a
predeclared experiment protocol that freezes:

- parameters eligible for change and their permitted domain;
- immutable parameters and policy constraints;
- training, validation, replay, and forward-observation windows;
- point-in-time data and feature generation;
- objective and loss/risk measures;
- transaction cost, slippage, latency, fill, and capacity assumptions;
- tail, drawdown, operational, and rule-compliance constraints;
- multiple-testing and selection controls;
- Candidate count, sensitivity/robustness analysis, and promotion gates.

An Agent cannot inspect results and then change the objective, evaluation
window, identity, or acceptance rule until a favorable Candidate appears. A new
protocol is a new experiment revision and does not retroactively validate old
searches.

## Evaluation boundary

Evaluation cannot reduce to total PnL. `GRACE_QUANTITATIVE.md` now proposes the
following separate dimensions, pending independent model-risk review and
calibration:

- return relative to pre-authorized risk and exposure;
- loss frequency/severity, drawdown, clustering, and tail behavior;
- concentration and dependence on a few outcomes;
- regime, entity, horizon, and product coverage;
- transaction cost, slippage, latency, and Live/Shadow execution gap;
- parameter sensitivity and nearby-model stability;
- calibration and missing/unknown data behavior;
- rule and operational compliance;
- opportunity selection, denied/censored cases, repeated testing, and
  backtest/selection overfitting;
- historical replay versus forward Shadow deterioration.

Every scoreable Strategy behavior also freezes its prediction target,
confidence semantics, primary/secondary horizon, benchmark/no-action
comparator, Evidence Snapshot, decision graph, counterfactual/censoring rule,
and GRACE evaluation Contract before the outcome. These obligations apply to
WAIT and PASS as well as traded proposals. Strategy evaluation cannot select
only the decisions whose outcomes later became favorable.

Human-owned evaluation policy determines mandatory behavior classes. A
Strategy defines the qualified opportunity universe and objective meaning for
those classes, but cannot mark a required unfavorable behavior unscoreable.
Delayed behavior registration begins when the behavior occurs; it does not wait
for Strategy promotion or later Coach review.

The proposed model families and non-compensatory rating logic are specified in
`GRACE_QUANTITATIVE.md`. No numerical threshold, prior, sample requirement, or
Calibration Pack value is authorized until independent model-risk review.

## Performative and counterfactual boundary

A strategy changes which opportunities it discovers, qualifies, trades, and
later learns from. Champion and Candidate must therefore be compared on the
same point-in-time qualified opportunity stream where possible, with realized
Live, execution-aware Shadow, denied, and censored outcomes kept distinct.

Denied qualified cases continue under the approved Shadow counterfactual
protocol. Missing counterfactuals remain limitations; they are not assigned
invented returns. A Candidate evaluated only on cases selected by itself or the
Champion cannot establish general promotion evidence.

Delayed rating and performative-feedback controls are frozen in `GRACE.md`;
the downstream authority mapping and Kernel grant boundary are frozen in
`DELEGATION.md`.

## Immutable revision and activation

An Active Strategy/Playbook revision does not update while in use. Candidate
activation creates an atomic, effective, expiring where appropriate, auditable
revision transition with parent and rollback target. Prior revisions and their
input/output Contracts remain replayable.

Decision Desk uses only the Active revision for Live intent. Candidate revisions
may be used in Research and Shadow when explicitly marked and isolated. A
Strategy cannot activate itself through prompt, memory, Skill, or experiment
output.

## Existing-position binding

Every operation/position retains the entry-time Strategy, Playbook, Agent,
prompt/model, Evidence Snapshot, exit/invalidation, policy, GRACE model/
ScoreSnapshot, and Delegation revisions.
A later Strategy revision cannot silently modify the old position's thesis,
exit, stop, or management contract.

A new conclusion may produce a separately reviewed position-management
migration or Kernel operation. The original and new revisions, rationale,
Evidence, human/Agent decision, and resulting Kernel state remain linked.
Retiring a Strategy never orphans management of positions opened under it.

## Strategy identity and proliferation

- A new identity explains why the existing Strategy cannot express the new
  hypothesis; small changes normally remain Candidate revisions.
- Similarity, shared data/features, and overlapping eligibility are checked
  before accepting a new Strategy identity.
- Identity transfer, history, and GRACE credibility follow explicit rules; a
  rename cannot erase losses, violations, or uncertainty.
- Unsupported, stale, duplicate, or invalidated strategies may be Deprecated or
  Retired while remaining available for audit and existing-position binding.
- Strategy count, search, parallel experiments, and Candidate generation are
  budgeted to prevent unbounded strategy mining.

## Strategy Lab

Strategy Lab is a deterministic experiment and revision system, not a
free-form Agent opinion. It owns records equivalent to:

- Strategy/Playbook/Hypothesis and experiment revisions;
- immutable point-in-time data and opportunity manifests;
- code, feature, configuration, model, prompt, Skill, and environment checksums;
- cost/slippage/execution assumptions;
- Candidate parameters and selection history;
- scoreable behavior/evaluation Contract and complete decision-stream manifest;
- replay, stress, Shadow, Challenge, and Validator results;
- review/promotion decision and rollback target.

Agents propose and interpret experiments. Versioned code executes calculations
and preserves reproducibility. An unreproducible experiment cannot support
promotion.

## Role separation

- **Coach:** produces Post Mortem and Candidate Lesson tied to canonical cases.
- **Strategy Researcher:** aggregates evidence/cases, proposes mechanisms,
  hypotheses, and Candidate revisions.
- **Challenger:** searches for leakage, confounders, counterexamples,
  overfitting, regime failure, and tail/operational risk.
- **Strategy Validator:** independently reproduces manifests and deterministic
  experiments; it does not train the Candidate.
- **Human Strategy Owner:** approves activation, retirement, and material
  changes. A later separately frozen policy may preauthorize only a parameter-
  only equal-or-narrower transition within an exact domain after independent
  validation; it cannot widen Delegation or Kernel authority.
- **GRACE:** independently performs delayed credibility and real-outcome
  evaluation.
- **Delegation Validator/Policy:** independently maps compatible approved
  ratings and human policy to bounded authorization proposals.
- **Decision Desk:** consumes the Active revision for Live intent and clearly
  separated Candidates for Research/Shadow.

No component grades, validates, promotes, activates, or authorizes its own
output.

The initial Strategy, every model/rule/scope/effect change, and any transition
that can widen behavior or authority remain human-approved. A
`StrategyActivationAuthority` is a discriminated immutable record, never an
optional human id: it is either the required HumanStrategyDecision or a future
policy-preauthorized non-widening parameter transition. Candidate authors,
Validators, and Activator credentials remain disjoint in either branch.

## Context and retrieval

Playbooks do not grow as an endless daily log. Agent context receives the exact
applicable Active revision, structured Setup/Contract, current diff where
needed, a bounded set of representative support and counterexamples, and
Artifact/Evidence references. Historical revisions, full cases, experiments,
and Candidate narratives are retrieved on demand under `MEMORY.md`.

Every update is an immutable revision and machine-readable diff. Context compact
preserves exact Strategy/Playbook/experiment ids and cannot summarize away an
invalidation, open-position binding, disagreement, or pending review.

## Failure and rollback behavior

- Reproduction failure: Candidate cannot advance and failure evidence remains.
- Data/feature leakage or invalid point-in-time manifest: invalidate the
  affected evaluation rather than repair its score in place.
- Historical success but forward Shadow deterioration: narrow or return to
  research; do not compensate by changing gates after observation.
- Regime mismatch or stale assumptions: decay/review applicability, not online
  parameter chasing.
- Live canary deterioration or operational failure: fast downgrade/rollback
  under Delegation/Kernel policy using published GRACE evidence where
  applicable.
- Rule-violating profit: adverse evidence with no credibility improvement.
- Validator/GRACE/Delegation unavailable: no increase in Live authority.
- Existing positions: remain bound to their entry revisions unless an explicit
  reviewed migration occurs.

## Required later specification

Before implementation, freeze the exact Playbook, Setup, Strategy Contract,
Hypothesis, Candidate Lesson, experiment/manifest, revision identity/transfer,
activation/retirement, open-position migration, and Strategy Lab schemas and
state machines. Separately review the quantitative methods, source/opportunity
selection controls, replay/Shadow protocol, parameter search, multiple testing,
promotion gates, threat model, and failure/concurrency/rollback acceptance
suite.
