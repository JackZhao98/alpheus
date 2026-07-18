# GRACE Quantitative Specification

> Status: **DRAFT FOR INDEPENDENT MODEL-RISK REVIEW.** This document proposes
> the data semantics, statistical architecture, non-compensatory rating logic,
> model lifecycle, and acceptance tests intended for the frozen contract. It
> deliberately does not
> authorize implementation, choose empirical priors, or set numerical rating
> thresholds. Those values require the Calibration Pack defined below,
> independent validation, and explicit human approval.

This implementation-directed Draft refines the architecture in
[`GRACE.md`](GRACE.md) for **Grounded Retrospective Agent Credibility
Evaluation**. GRACE evaluates behavior after its predeclared outcome becomes
observable. It combines honest forecast quality with actual economic evidence
and the path of risk, while keeping integrity, data quality, and uncertainty
visible. Exact machine schemas, empirical calibration, and authorization still
remain future reviewed work.

The central decision is intentionally conservative:

> GRACE is a multidimensional evidence system with a non-compensatory joint
> rating. It is not a weighted points account. Profit cannot purchase its way
> past miscalibration, hidden tail risk, incomplete evidence, or an integrity
> violation.

GRACE publishes ratings. It does not create authorization. The Delegation
Policy Engine may later consume compatible published ratings under a separate
human-owned policy, and Kernel still independently enforces every hard limit.

## 1. Authority and unresolved calibration

This document owns:

- the canonical evaluation record shapes;
- the evidence and outcome taxonomy;
- one primary, authority-bearing horizon per behavior;
- Role-specific scoring-rule families;
- the economic outcome and risk-unit definitions;
- the conservative credibility baseline and robust Challenger family;
- correlation, selection, attribution, and counterfactual treatment;
- the non-compensatory rating lattice;
- Champion/Challenger/Validator behavior; and
- deterministic acceptance probes.

It does **not** own:

- trading permissions, products, account limits, or autonomous tiers;
- Kernel risk limits or broker semantics;
- the numerical priors, margins, minimum exposure, coverage thresholds,
  credible-probability cutoffs, tail levels, or rating boundaries;
- an empirical claim that the proposed model fits Alpheus data; or
- permission for GRACE output to affect Live behavior.

The missing numerical choices belong to a versioned **GRACE Calibration Pack**.
No placeholder number in a test fixture, example, or conversation becomes
policy. The Pack must be estimated from representative data, stress tested,
independently reviewed, signed, and bound to one `GRACEModelRevision` before
that revision can become Champion.

## 2. Quantitative invariants

1. A behavior is registered before any future target outcome or post-decision
   path observation becomes available. A frozen start price/baseline already
   present in the point-in-time Evidence Snapshot is permitted and required
   where the Contract uses it.
2. The target, probability semantics, scoring rule, horizon, comparator,
   benchmark, risk unit, data hierarchy, censoring rule, and attribution rule
   are frozen at registration.
3. Every mandatory behavior in the qualified stream is present, including
   WAIT, PASS, denied, expired, superseded, ignored, and untraded decisions.
4. Outcome-dependent inclusion, weighting, horizon selection, or metric
   selection invalidates an authority-bearing evaluation.
5. A Role is scored only against the objective declared by its Role Contract.
6. The same economic PnL is recorded once at the Strategy/decision-pipeline
   scope. It may be referenced by several behaviors but is never summed into
   several personal accounts.
7. `actual_live`, `unrealized_mark`, `execution_shadow`, and
   `observed_counterfactual` provenance remain physically and semantically
   distinct; `valid`, `invalid`, and `censored` are an orthogonal validity
   state, never disguised provenance.
8. Actual broker-reconciled net PnL and hypothetical opportunity cost occupy
   different fields and different ledgers.
9. More capital at risk does not manufacture more information credibility.
10. Small, correlated, stale, selectively observed, or weakly attributable
    samples receive less support, never invented certainty.
11. Integrity and permission facts are immediate, categorical evidence. They
    cannot be averaged away by market performance.
12. No scalar, posterior probability, grade, explanation, or model revision is
    itself an authorization.

## 3. Evaluation graph and cardinality

The canonical graph is:

```text
BehaviorEvent 1
  -> EvaluationTicket 1
       -> HorizonSpec 1..N
            -> MaturedBehaviorOutcome 0..1 current + append-only corrections
                 -> AtomicEvaluation 0..many model revisions
                      -> ScoreSnapshot 0..many scopes/as-of times
```

Rules:

- exactly one horizon is `primary` and authority-bearing;
- secondary horizons are diagnostic unless an approved model explicitly
  models their dependence and declares them authority-bearing;
- each horizon produces a separate outcome record;
- a final trade-close/reconciliation outcome is a separate horizon kind from
  a fixed market-path horizon;
- partial maturity never silently promotes the whole ticket to complete;
- repeated delivery is idempotent by immutable id and digest; and
- corrections append a superseding outcome and new evaluations/snapshots.
  They never overwrite the prior audit history.

## 4. Common record envelope

Every official record uses an immutable envelope:

```text
id
schema_revision
created_at and committed_at from the trusted service clock
effective_at or observed_at where applicable
content_digest
producer_service and producer_revision
causal_parent_ids
supersedes_id, correction_reason, and correction_manifest where applicable
```

Ids are unique, but identity is not the integrity mechanism. The canonical
serialization digest, schema revision, service time, and causal references are
validated together. Agent-provided timestamps are retained as claims, not
trusted as registration time.

## 5. `BehaviorEvent`

`BehaviorEvent` is written by the Agent Control Plane in the same owner
transaction as the qualifying published Artifact and its delivery outbox row.
The outbox delivers the already committed BehaviorEvent to GRACE; it is not an
alternative to BehaviorEvent persistence. GRACE owns validation and ticket
derivation, not the original behavior.

### 5.1 Common fields

```text
behavior_id, behavior_schema_revision, digest
occurred_at_claimed, committed_at_server, market_time_zone
run_origin_ref, run_id, task_id, attempt_id, session_id
conversation_id and user_request_id iff run_origin=user_request
artifact_id and artifact_schema_revision
agent_revision_id and role_contract_revision
optional strategy_version_id or explicit role_policy_scope_id
playbook, prompt, model, Skill, Tool, data, and configuration revisions
parent behavior, opportunity, claim, decision, proposal, operation,
authorization, order, position, and economic_outcome references
behavior_type and disposition
subject, instrument, product, universe, ledger, opportunity, and position scope
primary horizon and bounded secondary horizons
benchmark, comparator, and no-action baseline
point-in-time Evidence, feature, regime, Kernel, policy, and delegation state
known gaps, conflicts, stale inputs, and unresolved unknowns
decision graph: supporting, opposing, accepted, ignored, and superseded claims
evaluation_contract_revision and evaluation_profile_revision
behavior_evidence_class
```

An `economic_outcome` reference may point only to a historical Outcome already
committed and visible at behavior time, such as context for a management
decision. It can never predeclare, backfill, or point forward to this behavior's
future Outcome.

`strategy_version_id` is not required for operational Roles that do not act
under a Strategy. Such events bind an explicit `role_policy_scope_id`; absence
is never represented by an ambiguous empty string.

`run_origin_ref` is the common immutable discriminated origin. Scheduled,
external-event, Kernel-event, maintenance, and recovery behaviors must not
fabricate a Conversation/UserRequest or reuse an interactive credential.
Recovery binds the original causal, idempotency, authority, and effect identity
and cannot create a fresh scoreable decision merely by retrying it.

### 5.2 Role-discriminated payloads

The common envelope does not become a giant nullable financial schema. Exactly
one discriminated payload is required:

| Payload | Producer Roles | Required objective content |
|---|---|---|
| `DataQualityClaim` | Data Desk | source/field universe, expected availability, freshness deadline, conflict or correctness claim |
| `DiscoveryForecast` | Scout | frozen discovery route/universe, candidate-level event probabilities, rank, lead-time target, omissions/exclusions |
| `MarketForecast` | Specialist / Lead Analyst | target event or distribution, horizon, probability/quantiles, scenarios, invalidation |
| `ChallengeForecast` | Challenger | exact challenged claim, failure event, base rate, probability, severity, horizon, falsification |
| `DecisionForecast` | Decision Desk | complete WAIT/PASS/PROPOSE disposition, target distribution, risk intent, comparator and reasons |
| `ManagementForecast` | Position Manager | HOLD/close/cancel/tighten/escalate action, pre-action path forecast, management comparator and constraints |
| `LearningHypothesis` | Coach / Strategy Researcher | falsifiable hypothesis, future qualified universe, frozen OOS protocol; no immediate credibility |

A Contract rejects a missing required field. Prose may explain the structured
payload but cannot replace it.

### 5.3 Scoreability ownership

Human-owned evaluation policy defines mandatory scoreable behavior classes.
A Strategy may define the legitimate qualified opportunity universe and
objective semantics, but it cannot exclude an unfavorable behavior from a
mandatory class. Deterministic Control Plane code resolves scoreability before
publication. The Agent cannot submit `not_scoreable` as an escape hatch.

If mandatory behavior registration fails:

1. the Worker/Attempt result or unpublished Artifact candidate may remain
   durably quarantined, but no canonical published Artifact exists;
2. a new-risk Artifact cannot enter a decision graph or Kernel proposal path
   until intake validation and ticket creation are acknowledged;
3. the outbox retries idempotently; and
4. an operational integrity event records the failure.

This publication gate does not sit on Kernel safety paths. User/Kernel-valid
risk reduction, reconcile, cancel, ambiguity containment, and emergency close
remain available during GRACE or outbox failure. If an Agent-originated close
Artifact **and its BehaviorEvent are already durably committed**, a temporary
Ticket/GRACE outage cannot block Kernel from independently validating and
executing the risk-reducing intent; ticket derivation later uses that original
pre-effect event. If the BehaviorEvent itself cannot commit, the Agent Artifact
cannot be used. Only a separate user/Kernel emergency action may proceed, and
it is recorded under its real origin rather than retroactively disguised as the
failed Agent behavior. GRACE can stop new authority evidence; it cannot trap
risk or legalize post-outcome registration.

## 6. `EvaluationTicket`

GRACE derives exactly one immutable ticket per accepted BehaviorEvent.

```text
ticket_id, ticket_schema_revision, digest
behavior_id, registered_at_server
evaluation_contract_revision and evaluation_profile_revision
immutable ModelBindingPlan: primary revision, ordered fallbacks, compatibility,
selection deadline, and invalidation/fallback predicates
horizon_specs[]
market calendar, session, sampling, corporate-action, option-event, and FX rules
frozen market-data source hierarchy and fallback revisions
fee, financing, borrow, slippage, capacity, and fill conventions
behavior_evidence_class and allowed outcome_evidence_classes
counterfactual, propensity, overlap, censoring, missing-data, and reconciliation protocols
eligible metrics and prohibited interpretations
attribution and economic-ledger references
```

Lifecycle is not a mutable field inside the ticket. Append-only
`EvaluationTicketStateEvent` records advance `registered -> pending -> mature ->
evaluated`, or terminate in `censored`/`invalidated`, with reason, service time,
next scheduled action, evidence references, and idempotency key.

Model selection is also not a mutable ticket field. Append-only
`ModelBindingStateEvent` records `selected`, `bootstrap_unassigned`,
`unsupported_profile`, `fallback_selected`, or `model_binding_invalid`, with
the ModelBindingPlan predicate, decision time, and evidence that caused it.

### 6.1 Horizon specification

Each `HorizonSpec` contains:

```text
horizon_id
kind: fixed_market_path | event_resolution | final_trade_reconciliation | oos_validation
authority_use: primary | diagnostic | jointly_modeled
observation_start and observation_end rules
evaluate_not_before
maturity and finality predicates
target, benchmark, comparator, score rule, and score-rule revision
data, price, adjustment, cost, censoring, and invalidation rules
```

Overlapping horizons are never assumed independent. A model either represents
their shared opportunity/event/time cluster or uses only the primary horizon
for authority-bearing evidence.

### 6.2 Model binding across Champion changes

At registration, the immutable ModelBindingPlan is frozen before any future
target observation. Its primary is the active compatible Champion when one
exists, and its ordered fallback/predicates are fixed then. The first
ModelBindingStateEvent is one of:

- `selected`: names the Plan's compatible primary;
- `bootstrap_unassigned`: no first Champion exists; or
- `unsupported_profile`: the active Champion cannot score the Profile.

An unassigned/unsupported ticket still preserves behavior and later Outcome,
but cannot produce a favorable `current_authority` evaluation. First-Champion
validation may use those records as clearly labeled historical training/
diagnostic input;
authority-bearing evidence starts with compatible tickets bound before their
future target observations.

For `selected`, the chosen model never changes in place. If Champion B replaces
Champion A before the ticket matures, A may complete a `historical_bound`
AtomicEvaluation under the frozen Plan; it is auditable but is not current
authority evidence.

`evaluation_profile_revision` is also frozen at registration. It determines
which planes are required, their target direction, and whether economics/tail
are applicable. Neither GRACE nor an Agent may select a more favorable Profile
after the outcome.

B may evaluate the outcome only as a clearly labeled historical/Challenger
diagnostic. It cannot replace A's bound evaluation or silently absorb the
pending ticket into a B authority-bearing Snapshot. New tickets after B's
effective time bind B. Downstream policy remains pinned to a compatible prior
Snapshot or a more conservative state until B accumulates sufficient eligible
evidence.

This intentionally favors an auditable transition over rapid score continuity.
Any future transfer mechanism requires a new reviewed specification; it cannot
be improvised during promotion.

If A is later found invalid, it cannot continue issuing favorable evidence. A
`fallback_selected` event may choose only the next revision and predicate in
the frozen Plan, and only before the Plan's future-target deadline. Otherwise a
`model_binding_invalid` event is appended. The affected AtomicEvaluation is
`INVALID` and excluded from valid aggregation; the Snapshot evidence-adequacy
plane may then become `INSUFFICIENT` because coverage fell. These are distinct
states. Snapshot coverage exposes model cohorts, transition censoring, and
retired-model replay status.

## 7. `MaturedBehaviorOutcome`

An outcome contains observed facts, not a rating:

```text
outcome_id, outcome_schema_revision, ticket_id, horizon_id, revision, as_of
observation window and complete market-path manifest
data sources, Provider revisions, timestamps, quality, and fallback use
target realization, benchmark realization, and paired differences
outcome_evidence_class and outcome_validity_state
realized_live_net_pnl, economic_set_id, LotAccountingRevision, matched-lot/
inventory/basis-transfer manifest, and signed reconciled cash-flow manifest
unrealized_mark_net_pnl only for an `unrealized_mark` Outcome
actual fees, slippage, financing/borrow, tax, distribution, assignment, and FX
benchmark_reference_net_pnl, benchmark_relative_net_pnl,
benchmark_identification_state, u_account, and u_edge
hypothetical_opportunity_cost in a separate counterfactual structure
Kernel reservation/product-risk id/revision, canonical positive risk unit,
base currency, scope, exposure units, and exposure time
MAE, MFE, drawdown, volatility, gap, stop breach, and tail-path measures
fill, partial fill, latency, capacity, and execution divergence
forecast losses, calibration observations, and baseline losses
integrity/operational event references
counterfactual identification state, method, overlap, uncertainty, and bounds
censoring, missingness, invalidation, and reconciliation state
data/code/configuration checksums and replay manifest
```

### 7.1 Outcome evidence taxonomy

`outcome_evidence_class` describes provenance and is exactly one of:

- `actual_live`: broker-reconciled executed economic facts;
- `unrealized_mark`: observable mark on a still-open Live position, never
  realized PnL;
- `execution_shadow`: a versioned simulated execution under frozen quote,
  fill, cost, capacity, and capital rules;
- `observed_counterfactual`: the real later market target for an unexecuted
  forecast or decision, without claiming an executable trade.

`outcome_validity_state` is separately `valid`, `invalid`, or `censored`.
`invalid` means the evaluation meaning or evidence integrity failed;
`censored` means the target cannot be validly/finally observed under the frozen
protocol. The expected provenance remains recorded even when validity is not
`valid`, but no invalid/censored numeric value enters the fitted outcome as if
observed.

`behavior_evidence_class` separately records where the behavior was produced:
`live_context`, `shadow_context`, `simulation_context`, or `research_context`.
All scope keys use the cross-product of these two taxonomies; a single
`live_or_shadow` flag is insufficient.

### 7.2 Append-only correction

Late fees, changed broker reconciliation, busted trades, split/dividend
adjustments, assignments, exercise, delisting proceeds, or corrected market
data create a new Outcome revision. The correction transaction:

1. records the reason and source manifest;
2. CAS-advances one fenced `OutcomeHead` from the exact prior generation;
3. appends the OutcomeRevision and OutcomeStateEvent without deleting the prior
   outcome; and
4. commits an outbox notification without directly writing an evaluation,
   ScoreSnapshot, model head, or grant.

The Engine later consumes that event under its own stable inbox identity and
publishes new AtomicEvaluations/ScoreSnapshots plus supersession references in
an Engine-owned transaction. Concurrent corrections have one current Outcome
tip; losers re-read and create an explicit successor or conflict. No correction
transaction crosses Intake/Outcome and Engine write ownership.

### 7.3 `AtomicEvaluation`

An immutable `AtomicEvaluation` records one model's treatment of one
BehaviorEvent/Horizon Outcome. It is not a rolling score or authorization:

```text
atomic_evaluation_id, schema_revision, digest
behavior_id, ticket_id, horizon_id, outcome_id and outcome revision
GRACEModelRevision and Evaluation Profile revision
mode: bound | challenger | historical_diagnostic
registration-model cohort and model-compatibility result
input target, score-rule loss, reference loss, paired differential
account/edge/risk-path fields eligible for this Role and evidence class
cluster ids, exposure weights, selection/censoring/attribution state
validity, limitations, deterministic integrity-event references
model-ready sufficient statistics and replay manifest
created_at, supersedes_id, and correction lineage
```

Exactly one non-superseded `bound` evaluation exists for each selected
ticket-model/outcome revision. Challenger and diagnostic evaluations coexist
under different model ids and can never overwrite it.

A Snapshot separately declares publication class `current_authority`,
`historical_bound`, `challenger`, or `diagnostic`. Only `current_authority`,
published under the active Champion and compatible Evaluation Profile, is
eligible for downstream Delegation consumption. The other classes remain
visible for audit/comparison but carry no current authority status. Every
Snapshot exposes AtomicEvaluation mode/cohort composition.

## 8. Point-in-time forecast scoring

All forecast evaluation is prequential: prediction at time `t` uses only
information committed by `t`, and its loss is computed only after the declared
outcome matures. Historical predictions are never regenerated with a newer
prompt, model, data set, or scoring rule.

Let `F_i` be the frozen probabilistic forecast and `y_i` the matured target.
Lower loss is better.

### 8.1 Approved scoring-rule families

| Target | Primary proper loss | Notes |
|---|---|---|
| Binary event | Brier: `(p - y)^2` | Default; bounded and auditable |
| Binary event, high information sensitivity | Log loss | Secondary unless the Contract freezes `p` bounds before observation |
| Nominal categorical | Multiclass Brier or log loss | Full probability simplex required |
| Ordered categorical/severity | Ranked Probability Score | Preserves category order |
| Continuous predictive distribution | CRPS | Full CDF and finite first moment required |
| Frozen quantiles/intervals | Pinball loss or Weighted Interval Score | Coverage alone is not a score; width is penalized |

For a finite frozen quantile grid, WIS/pinball is proper for the declared
quantiles. It is not described as strictly proper for an unrestricted full
distribution that the Agent did not provide.

For an interval `[l, u]` with nominal miscoverage `alpha`, the interval score is:

```text
IS_alpha = (u - l)
         + (2 / alpha) * (l - y) * I(y < l)
         + (2 / alpha) * (y - u) * I(y > u)
```

The exact orientation, units, probability bounds, quantile grid, and scoring
rule revision are frozen in the Evaluation Contract.

Accuracy, hit rate, F1, precision-at-K, NDCG, raw Sharpe, or prose confidence
may be useful diagnostics, but none is the authority-bearing forecast score.
A score ratio derived from a proper score is not assumed proper merely because
its numerator was proper.

### 8.2 Paired reference skill

Forecast credibility is measured against a frozen eligible reference on the
same tickets:

```text
d_i = loss(agent_i, y_i) - loss(reference_i, y_i)
```

Negative `d_i` means the Agent beat the reference. A missing reference does not
become zero loss; the Contract must declare the fallback before registration.
Aggregation exposes absolute loss, reference loss, paired difference,
calibration, sharpness, coverage, and uncertainty.

Beating a weak reference is not sufficient for `CREDIBLE`. The Evaluation
Profile also applies frozen absolute-loss, calibration, coverage, and model-fit
criteria appropriate to the target.

### 8.3 Calibration

Binary forecasts expose reliability by probability bins and, where data
supports it, the reviewed hierarchical calibration relation:

```text
logit P(Y_i = 1) = a_g + b_g * logit(p_i)
```

Ideal calibration is `a_g = 0` and `b_g = 1`. Calibration is not replaced by a
high hit rate, and sharpness is rewarded only when calibration is retained.
The Contract freezes treatment of `p=0/1` before observation; a logit
calibration fit cannot clip them after the fact. Sparse calibration cells use
the approved partial pooling or remain `INSUFFICIENT`.

### 8.4 Weights and dependence

Any observation weight must be computable from information frozen before the
outcome. Profit, loss magnitude, realized extremeness, or whether a prediction
was later cited cannot determine its weight.

Overlapping horizons, repeated claims, the same original news event, one
position, one symbol, one market day, and one opportunity create dependence.
Models use explicit shared effects or cluster-robust/time-block inference. The
raw ticket count is never presented as the effective sample size.

## 9. Role-specific observation models

Roles do not share one financial loss function.

### 9.1 Data Desk

Authority-bearing objectives may include source availability, required-field
coverage, freshness deadline compliance, point-in-time correctness, and
conflict detection/resolution against a frozen later reference.

Data Desk is not credited for stock direction unless a separately approved
MarketForecast Contract applies. Operational failure severity remains separate
from market PnL.

### 9.2 Scout

Every DiscoveryForecast binds a route/universe checksum and a candidate-level
probability for the predeclared qualified-opportunity event. The primary score
is the proper loss across the **complete frozen route universe**, including
rejected and unselected names.

Precision@K, recall@K, Average Precision, NDCG, lead time, and coverage are
diagnostics. They cannot replace calibration: identical rankings with `0.60`
and `0.99` probabilities are not equally credible.

If a Tool exposes only a top-20 result, the scope is explicitly "top-20 output
of Tool revision X", not the whole market. Reposts of one original event are
grouped by evidence lineage and do not become independent discoveries.

### 9.3 Specialist and Lead Analyst

Each Claim uses the scoring family matching its target: event probability,
ordered scenario, return/price distribution, or frozen quantiles. Mechanism
and invalidation prose remains supporting evidence; it does not replace the
machine-readable target.

The Analyst receives forecast credibility for that Claim, not the full PnL of
a later team decision.

### 9.4 Challenger

A scoreable Challenge names an exact claim, failure event, probability,
severity, horizon, base-rate reference, and falsification condition. All
qualified warnings enter the denominator. A bundle of correlated warnings is
one scenario family or has a shared cluster effect, not dozens of independent
wins.

Permanent pessimism is compared with the frozen base-rate forecast using a
paired proper loss. A later market decline does not validate vague negative
prose.

### 9.5 Decision Desk

Every qualified WAIT, PASS, and PROPOSE creates a DecisionForecast, not only
the proposals that later trade. Forecast credibility uses the complete
qualified stream.

For an executed and reconciled Live decision, economic evidence is recorded at
the Strategy/decision-pipeline scope and may support a scoped Decision Desk
policy evaluation. Denied, user-overridden, Kernel-rejected, expired, or
unexecuted decisions have no actual Live PnL. Their later observable market
target can still receive a forecast score.

### 9.6 Position Manager

Every scoreable HOLD, close, cancel, tighten, or escalation decision freezes a
path forecast and a comparator before the action. Evaluation uses the
post-decision path, actual execution, rule compliance, and the comparator
defined by the management Contract.

The model never uses the hindsight maximum price as the universal ideal exit.
"Would have held to X" is counterfactual unless the comparator was frozen and
its execution is identified.

### 9.7 Coach and Strategy Researcher

A Post Mortem, lesson, or hypothesis earns no immediate market credibility.
The LearningHypothesis matures only on a later independent OOS qualified
stream frozen before those outcomes. Editorial quality and persuasive prose
are not quantitative evidence.

### 9.8 Operational Roles

Task Planner, Control Plane, and other operational components use ordinary
reliability, latency, cost, recovery, and contract-compliance observability by
default. They enter GRACE only through a separately reviewed objective
Contract; they do not inherit financial credibility merely because they
participated in a profitable run.

## 10. Economic evidence

Economic evidence answers a narrower question than forecast credibility:
under a reconciled Live action, did the Strategy/decision pipeline produce net
value within the risk path it declared and was allowed to take?

### 10.1 Canonical fields

For executed Live outcome `i`, reconciled realized PnL uses the product/account
`LotAccountingRevision` frozen by human policy and reconciled with Kernel/
broker facts:

```text
realized_live_net_pnl_i
  = sum(realized lot PnL for matched closed quantity
        + allocated realized dividends/distributions
        + allocated borrow/financing cash flows
        + allocated fees/taxes
        + realized FX conversion cash flows)

benchmark_relative_net_pnl_i
  = realized_live_net_pnl_i - benchmark_reference_net_pnl_i

u_account_i = realized_live_net_pnl_i / kernel_risk_unit_base_currency_i
u_edge_i    = benchmark_relative_net_pnl_i / kernel_risk_unit_base_currency_i
```

The accounting revision declares broker-authoritative versus canonical lot
matching, long and short signs, scale-in/out, partial-close allocation,
fee/financing allocation, base currency, and rounding. Broker-reported realized
allocation is still reconciled to fills, cash, and inventory.

Exercise or assignment that creates a successor instrument transfers option
premium/basis into the successor lot under the frozen rule; it is not invented
as realized PnL. The `economic_set_id` links option and successor positions
until basis transfer and terminal inventory are resolved. Only when the whole
economic set has zero terminal inventory and no unresolved basis transfer may
the sum of all signed cash flows be used as the equivalent realized identity.
For a still-open or partially closed set, only matched closed quantity is
realized; remaining inventory basis stays open.

The broker/reconciliation manifest proves every component.
`realized_live_net_pnl` is already net; costs are not subtracted twice.
`unrealized_mark_net_pnl` is a separate `unrealized_mark` Outcome, never part
of realized PnL. An optional `live_total_marked_pnl` is explicitly labeled
marked, binds the open inventory/lot basis and mark source/time, and cannot
enter the realized economic plane. A final-trade-reconciliation Outcome does
not mature while required lot/basis/assignment facts remain unresolved.

`benchmark_reference_net_pnl` uses its own frozen cost/execution convention and
matches the evaluated direction, horizon, base currency, financing, capacity,
and risk budget. `benchmark_identification_state` states whether it is an
actually held paired exposure, an execution-aware reference, or an unexecuted
model comparator. Unless it is actually held, benchmark-relative performance
is comparative evidence, not account PnL or causal alpha.

Account dollars, benchmark-reference dollars, relative dollars, `u_account`,
and `u_edge` stay separate. Economic edge uses `u_edge`; actual loss frequency,
severity, drawdown, VaR, and ES use `u_account`. Running ahead of a worse
benchmark cannot hide a real account loss, and lagging a benchmark cannot turn
a real account profit into a loss event.

A favorable Live-economics plane requires the approved one-sided evidence that
expected `u_account` exceeds its absolute net-value margin. When the Profile
declares a benchmark-relative objective, it must **also** show `u_edge` exceeds
its separate margin. Relative outperformance alone never proves net value.

For a Live economic plane, the risk unit comes only from Kernel's canonical
product-risk/reservation record committed before the broker effect. The ticket
binds its reservation id/revision, strictly positive base-currency value,
product semantic, and operation/position/economic-set scope. Agent risk intent
is never the denominator. If an operation is amended before effect, the last
Kernel reservation revision effective before that effect applies; later
hindsight cannot replace it. A management evaluation uses the frozen entry or
pre-action remaining-risk rule declared by its Profile.

The risk unit is not actual realized loss, post-hoc capital deployed, MFE, or
hindsight drawdown. If Kernel cannot define a finite comparable positive risk
unit, the actual dollar outcome remains in the ledger, but the normalized
economic plane is `INSUFFICIENT` and cannot receive a favorable grade.

Capital size affects economic exposure and loss magnitude but does not multiply
information sample count.

### 10.2 Three exposure measures

Every Snapshot distinguishes:

- **information exposure:** independent qualified opportunity/event clusters;
- **economic exposure:** predeclared risk units and exposure time; and
- **selection coverage:** matured required behaviors divided by the complete
  eligible stream generated under the same policy.

An optional concentration diagnostic is:

```text
n_eff_weight = (sum cluster_weight)^2 / sum(cluster_weight^2)
```

It does not replace the model's explicit correlation structure and is never
called an independent trade count.

### 10.3 Frequency, severity, and path

GRACE never summarizes economic evidence by mean return alone. It separately
models and publishes:

- probability/frequency of negative, zero, and positive `u_account` outcomes;
- actual-account positive and negative severity distributions;
- `u_edge` and its uncertainty as a distinct benchmark-relative target;
- net and benchmark-relative mean/median with uncertainty;
- MAE, drawdown, gap loss, stop breach, loss clustering, and consecutive loss
  behavior;
- fees, slippage, borrow/financing, latency, and capacity divergence; and
- upper-tail VaR/Expected Shortfall of a positive loss variable plus probability
  of breaching approved loss and drawdown conditions.

Define behavior-level positive account loss as
`L_i = max(-u_account_i, 0)`. Expected Shortfall uses the
distribution-general upper-tail definition:

```text
ES_alpha(L) = (1 / (1 - alpha)) * integral_alpha^1 VaR_q(L) dq
```

The tail probability `alpha`, estimation rule, and paired VaR/ES validation
belong to the Calibration Pack. ES is not accepted merely because an in-sample
number can be computed.

Behavior-level ES alone cannot see simultaneous losses. GRACE separately
publishes tail evidence for predeclared non-overlapping Strategy/pipeline and
portfolio time blocks, constructed from their signed account cash flows and
compatible frozen aggregate risk base. Drawdown-path risk remains a separate
path statistic. The product risk-unit registry and aggregation rule are part of
the Calibration Pack; incompatible risk units are not casually summed.

### 10.4 No economic fiction

Unexecuted WAIT/PASS/denied cases may have real later market observations, but
they do not have actual account PnL. `execution_shadow` and
`observed_counterfactual` fields remain hypothetical and cannot improve a Live
economic grade unless a future reviewed cross-class transfer model explicitly
allows it. The first Champion's cross-class transfer is zero.

## 11. Conservative credibility baseline

The first candidate Champion is an auditable hierarchical
Buehlmann-Straub-style credibility baseline, calculated independently for each
compatible target/evidence class after deterministic correlation clustering.

The target is explicit and direction-normalized per plane:

```text
x_pred,c    = cluster aggregate of -paired proper-loss differential
x_edge,c    = cluster aggregate of u_edge
x_account,c = cluster aggregate of u_account
```

Positive values are favorable for these three examples, but they are modeled
separately and never pooled across units. Other Role objectives define their
own bounded `x` in the Evaluation Contract.

For compatible subject cell `g` and approximately independent predeclared
cluster units `c`:

```text
W_g       = sum_c m_c
mean_g    = sum_c(m_c * x_c) / W_g
K         = EPV / VHM
Z_g       = W_g / (W_g + K)
estimate  = Z_g * mean_g + (1 - Z_g) * reference_mean[reference_id(g)]
```

The baseline is applicable only when the declared collective approximately
satisfies the Buehlmann-Straub conditions:

```text
E[X_g,c | Theta_g]   = mu(Theta_g)
Var[X_g,c | Theta_g] = v(Theta_g) / m_c
m_c > 0
```

Subject cells in a reference collective must be exchangeable at the declared
level, cluster observations conditionally independent after the modeled
structure, and required second moments finite. Failure of these diagnostics
makes the baseline inapplicable rather than "approximately credible" by name.

`EPV` is expected within-cell process variance and `VHM` is variance of latent
means between exchangeable cells. They are estimated from the same declared
cluster units with non-negative constrained methods. If `VHM <= 0`, is
numerically unstable, or cannot be separated from residual dependence, set
`Z_g = 0`, publish `STRUCTURAL_VARIANCE_UNRESOLVED`, and prohibit a
subject-specific favorable upgrade.

Uncertainty in EPV, VHM, `K`, the reference mean, and cluster aggregation is
propagated into the one-sided subject interval. A plug-in `Z_g` plus a later
naive standard error is insufficient.

### 11.1 Permitted exposure weight

`m_c` is allowed only where the model demonstrates the assumed conditional
variance relationship and the weighting rule was frozen before the outcome.
Otherwise each independent opportunity/event cluster receives equal
information weight. Capital at risk is never used as `m_c` merely because it
is available.

The ModelRevision defines how correlated raw behaviors collapse into
non-overlapping event/position/market-time cluster aggregates before EPV, VHM,
and `Z_g` are fit. It also defines the compatible time-block bootstrap,
cluster-robust procedure, or other reviewed method used for one-sided
conservative intervals. Adding an error bar after fitting an independence model
does not repair biased structural variance. If adequate clusters cannot be
constructed or modeled, the baseline plane is `INSUFFICIENT`.

### 11.2 Pooling hierarchy

The baseline uses an explicit immutable `ReferenceMap`, not a heuristic
"nearest parent" search:

```text
subject_cell_definition
  -> one compatible reference_id
  -> exact reference population predicate
  -> fallback reference_id or INSUFFICIENT
```

The map keys behavior/Role, target, horizon, product, point-in-time regime,
behavior/outcome evidence class, optional Strategy family, and AgentRevision as
declared by the ModelRevision. Sparse cells shrink toward that documented
compatible reference. A material prompt/model/Role/Strategy/Tool/data change
creates a new cell; it does not silently carry forward the old cell's posterior
as personal history. Cross-classified random effects belong to the robust
Challenger, not this single-reference baseline.

### 11.3 Baseline limitations

The baseline is not assumed to solve non-stationarity, heavy tails, dependence,
or nonlinear frequency/severity structure. It can become Champion only with
visible limitations and conservative rating eligibility. In particular:

- insufficient tail evidence makes a required Live tail plane `INSUFFICIENT`;
  it cannot be ignored or receive a favorable joint grade;
- true catastrophic losses are never winsorized away as contamination;
- cluster/time-block uncertainty accompanies the credibility estimate; and
- model failure or unstable structural variance yields `MODEL_INVALID` or
  `INSUFFICIENT`, not full credibility.

## 12. Robust Challenger family

The required alternative is a versioned dynamic hierarchical Bayesian
frequency-severity model. It is a Challenger until it passes independent
validation; complexity alone is not an improvement.

### 12.1 Cross-classified predictor

Candidate latent predictors may take the frozen form:

```text
eta_g,t = beta_0
        + u_role
        + u_behavior
        + u_strategy
        + u_agent_revision
        + u_regime
        + u_horizon
        + u_product
        + u_evidence_class
        + declared interactions
```

The random effects, interactions, prior families, and identifiability
constraints are enumerated in the ModelRevision. Weakly identified variance
components receive reviewed regularizing priors; convenient near-zero
"noninformative" priors are not assumed safe.

### 12.2 Frequency-severity body

Actual-account outcome and benchmark-relative edge are different targets. For
`u_account_i`, use a three-part hurdle so exact zeros do not enter a continuous
gain distribution:

```text
C_i in {loss, zero, gain}
C_i ~ Categorical(pi_loss, pi_zero, pi_gain)

G_i = u_account_i       conditional on u_account_i > 0
L_i = -u_account_i      conditional on u_account_i < 0

E[u_account_g,t]
  = pi_gain * E[G_g,t] - pi_loss * E[L_g,t]
```

`u_edge` has its own compatible model and cannot determine whether an account
loss occurred. Means are published only when the required positive and negative
severity moments are finite under the reviewed criterion; the positive tail is
also checked so a lottery-like windfall cannot dominate expected edge.

The non-extreme body may use a reviewed Student-t or another robust likelihood,
but robust fitting must not reinterpret genuine catastrophic losses as
discardable outliers.

### 12.3 Extreme-loss tail

Loss exceedances above a predeclared, higher-level pooled threshold `q` may use
a generalized Pareto tail:

```text
P(L - q <= y | L > q)
  = 1 - (1 + xi * y / beta)^(-1 / xi)
```

The ModelRevision enforces `beta > 0` and support
`y >= 0` and `1 + xi * y / beta > 0` for every exceedance. `xi = 0` uses the
exponential limit. Finite tail mean requires `xi < 1`; finite tail variance
requires `xi < 1/2`.

Threshold, conditional-scale normalization/covariates, and extreme-event
declustering are selected only inside the frozen training window, never from
validation outcomes or per Agent after observing its result. A single Agent
will usually lack enough tail observations; tail effects therefore pool at a
compatible higher Role/Strategy/product/regime level. Raw heteroscedastic
losses are not assumed identically distributed merely because they exceeded
one dollar threshold.

In this GPD parameterization, `xi >= 1` implies a non-finite tail mean. If
tail-mean existence or ES is materially unresolved under the approved
posterior criterion, the Snapshot reports `TAIL_UNRESOLVED`; a Profile that
requires that tail quantity sets its Risk/Tail plane to `INSUFFICIENT`. If
available conservative evidence already crosses the frozen adverse boundary,
the plane is `ADVERSE` instead. `Q_g` is omitted in either unresolved case; the
model does not truncate the tail until a pleasing answer appears.

### 12.4 Time and regime

The first baseline uses fixed versioned time blocks and regime labels known at
behavior time. A Challenger may use a forward-filtered state transition such
as:

```text
eta_g,t = eta_g,t-1 + epsilon_t
epsilon_t ~ Normal(0, q_g)
```

No authority-bearing evaluation uses full-sample hidden-state smoothing that
leaks future information into the decision-time regime. Ad hoc recency weights
and online Champion weight mutation are forbidden.

## 13. Evidence-class transfer

The transfer key is the exact pair:

```text
(behavior_evidence_class, outcome_evidence_class)

behavior: live_context | shadow_context | simulation_context | research_context
outcome:  actual_live | unrealized_mark | execution_shadow |
          observed_counterfactual
```

The default matrix is identity on an exact pair and zero between different
pairs. Thus `live_context/actual_live` does not transfer to
`shadow_context/execution_shadow`; `unrealized_mark` does not become
`actual_live`; and simulation/research evidence does not become Live. When an
open position later realizes, that fact creates its separate `actual_live`
Outcome rather than converting the earlier marked record.

A future Challenger may estimate a paired Shadow-to-Live bridge only when the
same frozen behavior population has sufficient paired evidence, execution
definitions are comparable, and selection/overlap limitations are explicit.
The bridge is directional, discounted, uncertainty-bearing, capped, and bound
to one ModelRevision. It can never turn hypothetical PnL into account PnL.

Until that bridge is independently accepted, Shadow or research evidence may
support research qualification but contributes zero to a Live upgrade.

## 14. Selection, censoring, and counterfactuals

### 14.1 Complete-stream requirement

The authority-bearing denominator is the complete mandatory behavior and
qualified-opportunity stream generated under the frozen policy. Evaluating
only executed trades, winners, extreme outcomes, cited Claims, top-ranked
candidates, or events that later became news is invalid. Extreme-case views
may exist as diagnostics but cannot recompute the primary score.

Every Snapshot publishes:

- eligible behavior/opportunity count;
- registered, matured, pending, invalid, and censored counts;
- selection and maturity coverage;
- disposition mix, including WAIT/PASS/denied/expired/superseded;
- generating Planner, Strategy, GRACE, Delegation, and policy revisions; and
- missingness/censoring reasons and concentration.

### 14.2 Directly observed forecast outcomes

An unexecuted decision can still have a directly observable forecast target.
For example, a frozen probability that SPY closes above a threshold can be
scored against the later market close whether the system traded or waited.
That is forecast evidence, not avoided PnL.

### 14.3 Execution counterfactual

"The option would have filled at this price and earned this amount" requires a
frozen execution model. A Shadow outcome uses timestamped quotes, spread,
volume/capacity, latency, partial-fill, fees, capital conflicts, exit rules, and
market calendar semantics. It remains `execution_shadow` and never becomes an
account fact.

### 14.4 Off-policy evaluation

Policy-value comparison may use a reviewed off-policy estimator only when:

- the context and action definitions are stable;
- the historical logging propensity is recorded, not inferred from Agent
  confidence;
- consistency/no interference and conditional exchangeability are justified;
- every target action has sufficient support under the logging policy;
- weight tails and effective overlap pass the frozen diagnostics; and
- rewards and censoring use the same point-in-time definition.

For logging policy `mu`, target policy `pi`, outcome `r`, and frozen reward
model `m_hat`, the candidate doubly robust estimator is:

```text
V_DR = mean_i[
  sum_a pi(a | x_i) * m_hat(x_i, a)
  + pi(a_i | x_i) / mu(a_i | x_i)
    * (r_i - m_hat(x_i, a_i))
]
```

This formula is authorized only for a one-step atomic contextual-bandit
decision with a fixed reward horizon. Discrete actions use propensities;
continuous actions require valid logging/target densities and density overlap.
Position management and other sequential/path-dependent policies require a
separately reviewed trajectory or sequential doubly robust specification with
state, cumulative weighting, and horizon semantics; this one-step formula
cannot be reused.

Nuisance models use point-in-time temporal cross-fitting. Any weight clipping,
stabilization, reward bound, or continuous-density treatment is frozen in the
Evaluation Contract and its induced bias is reported. Sensitivity analysis may
widen bounds but cannot turn unjustified exchangeability into identification.

Double robustness means the estimator has protection when one of the two
required nuisance models is correctly specified under its assumptions. It does
not make zero overlap, hidden confounding, or two bad models valid.

When `mu(a | x) = 0` for target-policy mass, that region is unidentified. GRACE
reports unsupported mass, conservative bounds where justified, and
`COUNTERFACTUAL_UNIDENTIFIED`; it cannot publish a favorable point estimate.
Finite value bounds require a defensible frozen bounded-reward domain; unbounded
trading PnL does not acquire finite bounds by assertion.
Live trading is never randomized merely to improve overlap for GRACE.

## 15. Multi-Agent attribution

### 15.1 One economic ledger

The canonical economic record belongs to:

```text
StrategyVersion x DecisionPipeline x Position/EconomicOutcome
```

Scout, Analysts, Challenger, Decision Desk, Position Manager, and Coach may all
reference it. The record is included once in team/Strategy economic aggregate.
It is not copied into each Agent's PnL and later summed.

### 15.2 Individual evidence

Each individual receives evidence for the objective it committed before the
outcome:

- Scout: discovery probabilities and complete-universe diagnostics;
- Analyst: Claim probability/distribution and invalidation;
- Challenger: failure-mode probability/severity versus base rate;
- Decision Desk: full-stream decision forecast and, where declared, scoped
  decision-policy outcome;
- Position Manager: management forecast/action versus frozen comparator; and
- Coach/Researcher: later independent OOS hypothesis result.

Decision-graph references establish use, rejection, or opposition. They do not
alone prove causal contribution.

### 15.3 Unidentified contribution

Official individual economic attribution requires a predeclared identified
design: valid intervention, sufficient policy overlap, or an independently
validated structural causal model with sensitivity analysis. Without it, the
status is `ATTRIBUTION_UNIDENTIFIED`, and economic evidence stays at the
pipeline/Strategy level.

Equal splits and forced 100% allocation are forbidden. A Shapley value computed
from a replay model is labeled `MODEL_ATTRIBUTION_DIAGNOSTIC`; it is evidence
about that model's coalition value, not observed causal PnL, and cannot raise an
authority-bearing individual rating.

## 16. Non-compensatory rating

GRACE publishes a scorecard and a conservative joint grade. It does not publish
an additive points balance.

### 16.1 Required planes

| Plane | Core question | Required output |
|---|---|---|
| Evidence adequacy | Is the full, compatible, mature stream sufficient? | coverage, independent clusters, effective exposure, missing/censored/selection limits |
| Predictive credibility | Did forecasts honestly outperform the frozen reference? | proper loss, paired skill distribution, calibration, sharpness, coverage |
| Live economics | Did the relevant Strategy/pipeline create reconciled net value per predeclared risk unit? | realized `u_account`, benchmark-reference `u_edge`, one-sided uncertainty, loss frequency and severity |
| Risk and tail | Was value achieved without unacceptable adverse path or unresolved catastrophe? | MAE/drawdown/gap, VaR/ES, breach probabilities, tail status |
| Integrity | Was behavior faithful to contracts, evidence, and permissions? | `CLEAR`, `UNRESOLVED`, `BREACHED`, or `REQUALIFYING` plus events |

Not every Role owns the Live-economics plane. Role profiles declare required
planes. A Scout may earn predictive credibility but cannot independently earn
a Strategy economic grade. A Live-capital policy may later require compatible
Role credibility **and** a separate Live Strategy/pipeline economic score; that
mapping belongs to Delegation, not GRACE.

### 16.2 Plane states

Each market/evidence plane returns one of:

- `INVALID`: schema, lineage, model, leakage, or completeness failure makes the
  result unusable;
- `INSUFFICIENT`: valid evidence exists but does not satisfy the frozen
  exposure, coverage, maturity, or uncertainty burden;
- `ADVERSE`: conservative evidence crosses a frozen adverse boundary;
- `UNCERTAIN`: adequate to estimate, but the conservative interval/joint
  probability does not establish favorable or adverse evidence;
- `CREDIBLE`: passes the approved favorable criterion with uncertainty; or
- `ROBUST`: passes the stronger criterion, tail and stability burden, and
  broader validation coverage.

`IntegrityStatus` remains separately visible and categorical. A profitable
behavior retains its positive economic fact, but integrity gates the published
joint state:

- `CLEAR` applies no additional cap;
- `UNRESOLVED` or `REQUALIFYING` caps the joint state at `INSUFFICIENT`; and
- `BREACHED` makes the joint state `ADVERSE` with an integrity reason until the
  human-owned requalification protocol is actually satisfied, except that a
  simultaneously `INVALID` required plane retains the higher handling priority
  defined below.

No market grade or `Q_g` clears that gate.

### 16.3 Joint grade

For a Role/scope, the applicable Evaluation Profile declares the set of
required planes `R`. Joint state uses deterministic precedence:

1. any `INVALID` required plane makes the joint state `INVALID`;
2. otherwise IntegrityStatus `BREACHED` or any `ADVERSE` required plane makes
   it `ADVERSE`, even if another plane is insufficient;
3. otherwise IntegrityStatus `UNRESOLVED`/`REQUALIFYING` or any
   `INSUFFICIENT` required plane makes it `INSUFFICIENT`;
4. otherwise it is `ROBUST` only if every required plane is robust,
   `CREDIBLE` only if every plane is at least credible, and `UNCERTAIN`
   otherwise.

The joint grade is therefore bounded by the weakest required evidence and
cannot be improved by excess performance elsewhere. Every plane remains
visible, so the precedence label never hides a simultaneous limitation.

The reviewed statistical model may also publish:

```text
Q_g = P(
  forecast_skill exceeds its favorable margin
  AND absolute live u_account exceeds its favorable margin, where applicable
  AND benchmark-relative u_edge exceeds its margin, where applicable
  AND ES/drawdown/breach conditions hold
  | frozen evidence and ModelRevision
)
```

`Q_g` is a posterior functional, not a 0-100 cash score. GRACE reports its
posterior Monte Carlo error, approved-model sensitivity range, and the credible
intervals of the underlying parameters; it does not invent a "credible interval
of a probability" without a separately specified higher-level uncertainty
model. Its conditions and probability cutoffs are human-owned Calibration Pack
parameters. It never bypasses an `INVALID`, `INSUFFICIENT`, adverse-tail, or
integrity state.

`Q_g` exists only when the approved model represents the joint dependence of
its conditions. GRACE cannot multiply marginal pass probabilities or assume
independence for convenience. If a defensible joint distribution is absent,
the field is omitted and the non-compensatory plane states remain sufficient.

### 16.4 No arbitrary utility

The specification rejects formulas such as:

```text
score = w1 * PnL + w2 * calibration - w3 * drawdown
utility = return - lambda * maximum_drawdown
```

No empirical or actuarial meaning follows from choosing such weights, and a
large profit can conceal an unsafe tail. GRACE instead preserves dimensions,
uses model-based uncertainty, and applies conjunction/non-compensation.

## 17. `ScoreSnapshot`

A published Snapshot contains:

```text
score_snapshot_id, schema_revision, as_of, evidence_cutoff, expiry
subject type/id and exact scope dimensions
evaluation_profile_revision
publication_class: current_authority | historical_bound | challenger | diagnostic
GRACEModelRevision, active-Champion compatibility, registration-model cohorts,
binding states,
transition censoring, retired-model replay status, and compatibility
input/outcome/evaluation manifests and replay digest
raw counts, independent clusters, economic exposure, effective exposure
maturity, selection, missing, invalid, and censoring coverage
proper losses, paired reference skill, calibration, sharpness, and coverage
realized signed-cash-flow PnL and unrealized marked PnL kept separate
u_account, benchmark reference/identification, u_edge, and their uncertainty
loss frequency, positive/negative severity, drawdown, gap, VaR/ES, tail state
behavior_evidence_class x outcome_evidence_class composition and orthogonal
valid/invalid/censored coverage
transfer matrix use and discount/cap, if any
attribution state, method, uncertainty, and economic-ledger references
each plane state, joint grade, joint-assurance diagnostic/MC error/sensitivity,
and limitations
IntegrityStatus, its joint-state gate, and immutable event references
regime, concentration, performative-shift, deterioration, stale,
requalification, model-invalid, and supersession flags
machine-readable drivers and human-readable explanation references
```

A Snapshot contains no trading tier, dollar amount, product permission,
suggested order, or authorization recommendation. The optional Advisor may
explain the record but cannot alter any field.

## 18. `GRACEModelRevision`

Every candidate and active evaluation model is immutable and self-contained:

```text
model_revision_id, parent_revision_id
created_at, data_cutoff, proposed rollback target
code, build, runtime, dependency, and configuration digests
Calibration Pack revision and full prior/hyperparameter manifest
supported Role, behavior, target, horizon, product, regime, and evidence classes
Evaluation Contract and schema compatibility matrix
likelihoods, link functions, pooling hierarchy, interactions, and constraints
exposure, clustering, time/regime, transfer, attribution, and uncertainty rules
tail threshold/model, VaR/ES definition, and unresolved-tail behavior
rating plane criteria and output scale
training, validation, forward-observation, and stress-window manifests
predeclared comparison metrics, multiple-testing family, and stopping rule
declared assumptions and candidate limitations
```

Mutable lifecycle and independently owned decisions are separate records:

```text
GRACEValidatorAttestation
ModelRiskDecision
ModelStateEvent: draft | challenger | validated | champion | retired | rolled_back
ActiveGRACEChampionHead(model_revision_id, generation, effective_at)
```

The Trainer/Engine writes only the immutable candidate body; the independent
Validator writes only its attestation; the authenticated model-risk path writes
only its decision; and the Activator may only append the state event and CAS the
Champion head from exact validated digests. Effective/retired times, approvals,
signatures, activation, and current state never appear as mutable fields inside
the ModelRevision. Concurrent promotion has one head-generation winner.

The revision specifies deterministic numeric precision, random seeds where
sampling is used, convergence diagnostics, tolerated replay error, and what
constitutes computational failure. A posterior is conditional on the declared
model and priors; the Snapshot must not describe it as objective certainty.

## 19. Calibration Pack

The Calibration Pack converts this design into an empirical model. It must
declare at least:

- reference populations and point-in-time data cutoffs;
- baseline forecasts and comparator construction;
- product-specific risk-unit registry, aggregate risk-base rules, base
  currency/FX conventions, and benchmark exposure matching;
- LotAccountingRevision registry for long/short lots, scale/partial close,
  fees/financing, exercise/assignment basis transfer, and finality;
- prior families and hyperparameters;
- structural/process variance estimation;
- baseline target definitions, immutable ReferenceMap, constrained variance
  behavior, correlation clustering, and one-sided interval algorithm;
- probability bounds and quantile grids;
- economic favorable/adverse margins in risk units;
- minimum complete-stream coverage and independent exposure;
- credible/confidence levels and uncertainty burden;
- loss-tail probability, EVT threshold protocol, and VaR/ES tests;
- positive/negative moment-existence and unresolved-tail rules;
- drawdown, gap, clustering, and severity conditions;
- grade boundaries and expiry/requalification rules;
- correlation clusters, time blocks, and regime definitions;
- missingness, censoring, transfer, and sensitivity limits;
- integrity classification and deterministic requalification facts;
- plane precedence, favorable-grade logic, and false-promotion error budget;
- model-selection family, trial count, stopping rule, and error budget; and
- compatibility with specific Evaluation Contracts and Delegation policies.

The Pack cannot be learned by maximizing authority, realized PnL, or the count
of high-rated Agents. Calibration optimizes forecast validity and model fit
inside human-owned safety constraints. Any change creates a new ModelRevision.

## 20. Model validation

### 20.1 Required validation layers

1. **Schema and lineage:** completeness, point-in-time semantics, no future
   leakage, immutable digests, and exact replay.
2. **Prior predictive:** priors can generate plausible ordinary, adverse, and
   catastrophic paths without silently ruling out known risk.
3. **Implementation calibration:** simulation-based calibration or equivalent
   verifies the inference implementation, not merely the conceptual model.
4. **Posterior/model checks:** frequency, severity, loss streaks, cluster
   dependence, drawdown, gap, tail, and regime behavior.
5. **Strict temporal validation:** rolling/expanding forward evaluation; random
   shuffled trade cross-validation is prohibited.
6. **Forecast validation:** proper losses, paired skill, calibration intercept/
   slope, reliability, PIT/coverage, and sharpness.
7. **Risk validation:** VaR and ES are assessed jointly, with exception
   magnitude, clustering, and model misspecification.
8. **Economic validation:** net and benchmark-relative outcomes under fees,
   slippage, financing, capacity, partial fills, and risk normalization.
9. **Sensitivity:** priors, tail threshold/family, clustering, attribution,
   regime, missingness, costs, transfer, and counterfactual assumptions.
10. **Stress:** lottery-like windfall, many small wins then catastrophe,
    correlated losses, regime break, Shadow optimism, selected labels, and
    performative policy shift.

### 20.2 Multiple testing and optional stopping

Strategy Lab remains the canonical owner of append-only experiment manifests
and results under `SYSTEM_BOUNDARIES.md`; GRACE does not create a competing
registry. A GRACE ModelRevision binds the relevant Strategy Lab Trial Registry
entries plus its model-specific promotion manifest.

"Attempted" here means a candidate revision/parameterization was allocated a
trial id and fit, replayed, or scored against data inside the declared promotion
family. Mere brainstorming text is not a statistical trial. Every such formal
Strategy, prompt, parameter, horizon, feature set, transfer rule, and model
trial remains recorded when renamed, abandoned, failed, or unfavorable.

Promotion comparison freezes candidate family, benchmark, window, metrics, and
stopping rule before results. Time-block variants of Reality Check/SPA or an
equally reviewed family-wise procedure address repeated candidate selection.
Deflated Sharpe and probability-of-backtest-overfitting may be diagnostics, but
neither replaces proper-score, economic, and tail validation.

Repeatedly inspecting a fixed-horizon p-value and stopping when favorable is
invalid. Continuous monitoring requires a predeclared anytime-valid method or
confidence sequence compatible with clustered time data.

### 20.3 Promotion gate

A Challenger cannot become Champion unless the independent Validator proves:

- deterministic replay on the frozen manifest;
- no material calibration, coverage, stability, or tail regression;
- either improved predeclared decision-relevant criteria out of sample, or a
  necessary correctness/safety repair or materially simpler/stabler model that
  passes predeclared non-inferiority burdens on every protected criterion;
- no weakened treatment of missing, censored, integrity, or unsupported data;
- acceptable prior/model sensitivity and computation diagnostics;
- sufficient Role/product/regime/evidence-class coverage;
- Trial Registry completeness and multiple-testing control;
- forward observe-only performance after the final model freeze;
- exact rollback and one-Champion concurrency behavior; and
- human model-risk approval with limitations and expiry.

"More complex", "higher in-sample PnL", and "rates more Agents highly" are not
promotion criteria.

## 21. Performative feedback monitoring

GRACE output may influence Delegation, which changes what is traded and later
observed. The evaluation distribution is therefore policy-induced. Every case
binds the active GRACE, Delegation, Strategy, Planner/Role-selection, and Kernel
policy revisions that generated it.

The Validator monitors at least:

- opportunity eligibility and acceptance rates;
- WAIT/PASS/PROPOSE/deny/expire/censor mix;
- Role/Agent/Tool selection and capability coverage;
- product, symbol, regime, holding-time, concentration, and risk-unit mix;
- fill, cost, capacity, and execution quality;
- Live/Shadow/counterfactual disagreement;
- registration, maturity, missingness, and attribution coverage; and
- loss frequency, severity, clustering, drawdown, and tail behavior.

A material change invalidates naive before/after comparison. It causes a new
qualification window, narrower scope, or ModelRevision review, not an online
weight adjustment. Performative stability, if observed, is not proof of market
optimality or safety.

## 22. Failure and publication semantics

- GRACE outage stops new official evaluation/publication and never blocks
  Kernel reconcile, cancel, close, or safety actions.
- Invalid or unreconciled source data cannot produce favorable evidence.
- A stale Snapshot retains its immutable historic value but is marked stale;
  downstream policy determines the conservative consequence.
- A model-invalid event marks affected Snapshots and produces a notification;
  GRACE does not mutate a grant.
- Publication is transactionally bound to the input manifest and replay digest.
- Only the privileged evaluation service writes AtomicEvaluations and
  ScoreSnapshots. Agents, Coach, Advisor, and Delegation have read-only access.
- Intake/Outcome, Engine, Validator, model-risk, and Activator write disjoint
  record families with no catch-all GRACE writer.
- Only the promotion path can append a ModelStateEvent and CAS one Champion;
  Engine and Validator use separate credentials, and Trainer cannot approve its
  own model.

## 23. Acceptance suite

### 23.1 Registration and lineage

- **Late registration race:** a BehaviorEvent committed at or after the first
  future target/path fact is observable is rejected; the pre-decision start
  price in its frozen Evidence Snapshot does not itself make registration late.
- **Immutable meaning:** post-registration changes to probability, target,
  horizon, benchmark, confidence semantics, score rule, data hierarchy, or
  attribution fail; so do changes to Evaluation Profile/required planes, risk
  unit binding, evidence classes, and ModelBindingPlan.
- **Outbox crash matrix:** every crash point around Artifact/Behavior commit
  yields either neither published or exactly one matching pair; no Artifact
  reaches a decision graph first.
- **New-risk acknowledgement:** a new-risk Agent Artifact cannot reach Kernel
  before accepted BehaviorEvent and ticket acknowledgement.
- **Safety bypass:** after Artifact+BehaviorEvent commit, a Ticket/GRACE outage
  cannot block Kernel-valid reconcile, cancel, ambiguity containment, or
  risk-reducing close; before BehaviorEvent commit, the Agent Artifact is not
  used and only an independently originated user/Kernel emergency path acts.
- **Duplicate delivery:** retries create one ticket, one horizon outcome per
  revision, and one idempotent evaluation per model.
- **Completeness reconciliation:** the qualified stream reconciles with WAIT,
  PASS, denied, expired, superseded, ignored, and executed dispositions.
- **Revision identity:** renaming an Agent/Strategy or changing a material
  revision cannot erase or silently inherit personal history.

### 23.2 Proper scoring and Role behavior

- **Honesty:** analytic expectation or repeated seeded Monte Carlo with a
  frozen tolerance shows that data generated with known Bernoulli probability
  gives the true probability lower expected Brier/log loss than exaggerated,
  shrunk, or reversed forecasts; no single finite sample is required to order
  them perfectly.
- **Metric switch:** outcome-time selection among Brier/log/CRPS/WIS, horizons,
  or benchmarks is rejected.
- **Secondary horizon:** a diagnostic secondary horizon cannot affect an
  authority-bearing grade unless the precommitted model explicitly marks and
  jointly models it.
- **Overconfidence:** an overconfident wrong forecast is penalized under the
  frozen proper rule.
- **Scout omission:** removing an unselected later winner changes the universe
  digest and invalidates the score.
- **Rank/calibration:** equal rankings with differently calibrated probabilities
  have equal rank diagnostics but different primary proper loss.
- **Permanent pessimist:** a Challenger that assigns high failure probability
  to everything cannot beat the base rate by selective hits.
- **Coach prose:** an excellent Post Mortem produces no immediate credibility.
- **Role isolation:** Data Desk cannot earn market-direction credit from an
  availability Claim, and Scout cannot earn sizing PnL.

### 23.3 Economics, tails, and dependence

- **Lucky windfall:** one high-variance profitable outcome cannot yield a
  material high grade under small-sample shrinkage and joint gates.
- **Loss despite relative win:** account `-5R` against benchmark `-7R` records
  `u_account=-5` and `u_edge=+2`; the actual loss remains in severity/tail.
- **Profit despite relative loss:** account `+2R` against benchmark `+3R`
  remains an account gain while its edge is `-1R`.
- **Marked reversal:** an unrealized gain that later reverses cannot enter
  realized economic evidence before reconciliation.
- **Partial close:** buy 10/sell 1 realizes only the matched one-unit lot and
  allocated costs; the other nine units remain open basis, not a cash-flow
  loss.
- **Assignment successor:** option assignment/exercise that creates stock
  transfers basis to the successor position and cannot mature final realized
  PnL while that basis remains open.
- **Scale/short accounting:** scale-in/out and short-cover golden cases match
  the frozen lot rule and broker reconciliation without sign reversal.
- **No finite risk unit:** dollar PnL remains recorded, while normalized
  economics is `INSUFFICIENT` and cannot receive a favorable grade.
- **Agent denominator attack:** inflating Agent-declared risk intent cannot
  dilute loss; only the pre-effect Kernel reservation/product-risk record is
  used.
- **Tail non-compensation:** two samples with equal mean but different severe
  lower tails receive different risk-plane states; high mean cannot clear the
  worse tail.
- **Unresolved tail:** unresolved required ES/tail mean makes Risk/Tail
  `INSUFFICIENT` (or `ADVERSE` on conservative breach), omits `Q_g`, and cannot
  yield a favorable joint grade.
- **Portfolio tail:** individually ordinary but correlated simultaneous losses
  appear in Strategy/pipeline and portfolio time-block tail evidence.
- **Joint dependence:** correlated favorable plane probabilities cannot be
  multiplied as if independent to manufacture a high `Q_g`.
- **Catastrophe retention:** a true extreme loss remains in severity/tail data
  and is not discarded by robust body fitting.
- **Risk-unit immutability:** increasing position size or using hindsight loss
  cannot increase information exposure.
- **Overlapping horizons:** one hundred overlapping 20-day targets do not
  report 100 independent observations.
- **Cluster duplication:** repeated news, same opportunity, same position, and
  same market day share the declared dependence cluster.
- **One PnL:** several Agents referencing one position still produce one
  Strategy/pipeline economic record.
- **Profitable breach:** a rule-breaking profitable trade retains its signed
  economic fact, while IntegrityStatus is `BREACHED` and joint state remains
  `ADVERSE`; profit cannot clear it.

### 23.4 Evidence classes and counterfactuals

- **WAIT/PASS:** later market path produces forecast loss; actual Live PnL is
  null and any simulated execution is separately typed.
- **Class separation:** identical numeric results in Live, Shadow, unrealized,
  and counterfactual records remain different scope/weight and never coerce to
  Live.
- **Default transfer:** Shadow-only history cannot improve a Live grade under
  the first Champion.
- **Zero support:** target-policy action with zero logging propensity produces
  unsupported mass/bounds, never an off-policy point estimate or upgrade.
- **DR misuse:** a synthetic zero-overlap or hidden-confounding case remains
  unidentified despite invoking a doubly robust estimator.
- **No causal fiction:** Position Manager hindsight and model Shapley output
  remain counterfactual/model diagnostic, not observed individual PnL.

### 23.5 Data finality and correction

- **Market edge cases:** halt, delisting, split, dividend, symbol change,
  missing/conflicting prices, option expiry/exercise/assignment, partial fill,
  busted trade, late fee, FX, and broker unknown each follow the frozen maturity
  or censoring rule.
- **Source hierarchy:** fallback choice is determined by the ticket, not by
  whichever source gives a favorable result.
- **No leakage:** point-in-time replay cannot read later data revisions,
  adjusted features, or smoothed future regime state.
- **Append-only correction:** a later reconciliation creates a superseding
  Outcome; concurrent corrections have one OutcomeHead winner. The separate
  Engine event later creates a superseding Snapshot. Old records remain
  replayable and marked superseded, and duplicate delivery cannot apply the
  correction twice.
- **Missing evidence:** absence/conflict expands uncertainty, censors, or
  invalidates; it never raises a grade.

### 23.6 Model governance and boundary

- **Trial registry:** hiding, deleting, or renaming a failed candidate causes
  promotion-manifest failure.
- **Optional peeking:** repeatedly viewed fixed-horizon tests cannot promote;
  only the frozen stopping rule may do so.
- **Selected winner:** a Challenger trained/evaluated only on policy-selected
  winners cannot be promoted.
- **Performative shift:** changing only the Delegation threshold and case mix
  blocks naive before/after evidence.
- **Pending-ticket transition:** a ticket registered under A and maturing after
  B activates may produce only `historical_bound` under A, never a B or current
  authority evaluation chosen after the outcome.
- **Invalid bound model:** if A is invalidated after the target begins and no
  fallback was planned/selected beforehand, append `model_binding_invalid`,
  make the AtomicEvaluation `INVALID`, and let Snapshot coverage separately
  determine whether evidence adequacy is `INSUFFICIENT`.
- **Bootstrap/unsupported:** no-Champion and unsupported-Profile tickets retain
  behavior/outcomes but cannot silently become favorable official history.
- **One Champion:** concurrent promotion yields one active revision; rollback
  restores the exact prior revision.
- **Body/state separation:** attempts to place lifecycle, effective/retired,
  approval, signature, or activation fields in ModelRevision fail schema
  validation; Trainer/Engine, Validator, model-risk, and Activator credentials
  cannot write one another's record families.
- **Publication class:** Challenger, diagnostic, and historical-bound
  AtomicEvaluations/Snapshots cannot enter a `current_authority` Snapshot or be
  consumed by Delegation.
- **Integrity cap:** `UNRESOLVED`/`REQUALIFYING` caps joint state at
  `INSUFFICIENT`, while `BREACHED` is `ADVERSE` unless a required plane is
  `INVALID`.
- **Determinism:** the same manifest reproduces bitwise output or the explicitly
  approved numeric tolerance.
- **Write boundaries:** Agent/Coach cannot write Outcome/Score, GRACE cannot
  write Grant, and Delegation cannot write Score.
- **No authority side effect:** publishing or correcting a ScoreSnapshot alone
  cannot change an authorization.
- **Outage isolation:** GRACE failure does not block Kernel safety and cannot
  make Kernel accept missing authority.

## 24. Known limitations

- Sparse personal trading history may remain `INSUFFICIENT` for a long time.
- Tail estimation will usually require higher-level pooling and still carry
  material uncertainty.
- Bayesian credible probability is conditional on model and prior; robust
  sensitivity is mandatory.
- Buehlmann-Straub assumptions are an auditable baseline, not a claim that
  trading observations are homogeneous, independent, stationary, or thin-tail.
- Observed market paths are easier to identify than executable counterfactual
  PnL, and neither identifies every decision's causal value.
- Full opportunity coverage depends on the frozen capability/Tool universe; a
  scanner cannot prove whole-market recall when it never saw the whole market.
- Model/policy feedback prevents simple causal claims from before/after Live
  performance.
- No quantitative design can make a dishonest or incomplete BehaviorEvent
  truthful; prevention and audit remain necessary.

These limitations are reasons to publish uncertainty and narrow scopes, not
reasons to invent a more responsive score.

## 25. Implementation authorization boundary

Before any GRACE Engine code begins, the project still needs:

1. independent actuarial/statistical review of this Draft;
2. exact machine schemas and migration/retention plan;
3. representative reference data and complete-stream feasibility analysis;
4. a signed Calibration Pack for the baseline candidate;
5. golden fixtures and simulation generators for the acceptance suite;
6. security/write-path design for official records and promotion; and
7. an explicit implementation approval.

Before any GRACE result affects Live policy, GRACE must additionally complete
offline, observe-only, Validator, and Shadow-delegation stages in `GRACE.md`,
and `DELEGATION.md` must independently pass its own acceptance boundary.

## 26. Research basis and limits of transfer

This design draws on the following primary sources; each motivates a method or
hazard but does not prove that Alpheus satisfies its assumptions:

- Gneiting and Raftery, [*Strictly Proper Scoring Rules, Prediction, and
  Estimation*](https://doi.org/10.1198/016214506000001437), for honest
  probabilistic evaluation and Brier/log/CRPS families.
- Dawid, [*Statistical Theory: The Prequential
  Approach*](https://doi.org/10.2307/2981683), for evaluating sequential
  forecasts from information available at prediction time.
- Buehlmann, [*Experience Rating and
  Credibility*](https://doi.org/10.1017/S0515036100008989), and Buehlmann and
  Straub's [exposure-credibility
  model](https://doi.org/10.5169/seals-967024), for conservative partial
  pooling.
- Dudik, Langford, and Li, [*Doubly Robust Policy Evaluation and
  Learning*](https://icml.cc/2011/papers/554_icmlpaper.pdf), for identified
  off-policy evaluation under explicit propensity/support assumptions.
- Sachdeva, Su, and Joachims, [*Off-policy Bandits with Deficient
  Support*](https://www.cs.cornell.edu/people/tj/publications/sachdeva_etal_20a.pdf),
  for the non-identification created by zero logging support.
- Acerbi and Tasche, [*On the Coherence of Expected
  Shortfall*](https://arxiv.org/abs/cond-mat/0104295), and Fissler and Ziegel,
  [*Higher Order Elicitability and Osband's
  Principle*](https://arxiv.org/abs/1503.08123), for tail-risk definition and
  joint VaR/ES evaluation.
- McNeil and Frey, [*Estimation of Tail-Related Risk Measures for
  Heteroscedastic Financial Time
  Series*](https://doi.org/10.1016/S0927-5398(00)00012-8), for conditional
  volatility plus EVT tail modeling.
- Talts et al., [*Validating Bayesian Inference Algorithms with
  Simulation-Based Calibration*](https://arxiv.org/abs/1804.06788), for
  implementation-level Bayesian validation.
- White, [*A Reality Check for Data
  Snooping*](https://doi.org/10.1111/1468-0262.00152), for repeated model and
  strategy selection.
- Hansen, [*A Test for Superior Predictive
  Ability*](https://doi.org/10.1198/073500105000000063), for a less-relevant-
  alternative-sensitive repeated-model comparison.
- Lerch et al., [*Forecaster's Dilemma: Extreme Events and Forecast
  Evaluation*](https://doi.org/10.1214/16-STS588), for why outcome-selected
  forecast evaluation destroys honest incentives.
- Perdomo et al., [*Performative
  Prediction*](https://proceedings.mlr.press/v119/perdomo20a.html), for the
  fact that deployed policy changes the observed distribution.

The Perdomo paper is especially a warning, not a convergence guarantee. Its
formal results require conditions such as smooth/strongly convex loss and a
sufficiently regular distribution map. Sparse, constrained, non-stationary,
heavy-tail trading behavior does not satisfy those assumptions by default.
