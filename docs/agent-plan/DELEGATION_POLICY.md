# Delegation Exact Policy, Grants, and Kernel Gate

> Status: **FROZEN SPECIFICATION — the v1 authority modes, capability-template
> lattice, GRACE eligibility mapping, policy/grant/ticket/use contracts,
> human-authority separation, linearizable Kernel Gate, failure behavior, and
> rollout order are authoritative. Numerical envelopes are signed
> human-owned policy data with no permissive defaults. The initial production
> policy keeps autonomous Live disabled; this document does not itself enable
> a broker mutation.**

## 1. Purpose and decision

This specification closes the implementation gap left by `DELEGATION.md`.
It answers four exact questions:

1. which authority path a proposed effect must use;
2. how compatible GRACE evidence makes a subject eligible for a pre-approved
   capability template without turning a score into money;
3. what an immutable grant, exact confirmation, and Kernel authorization use
   contain and how they change state; and
4. where authorization linearizes against concurrent operations, revocation,
   expiry, recovery, and existing Kernel limits.

The core rule is:

```text
GRACE determines evidence state.
Delegation determines eligibility for a human-owned capability template.
Kernel determines whether one canonical operation may consume that authority.
Provider execution remains exclusively inside Kernel.
```

Delegation never calculates an order quantity from a GRACE score. It never
converts `Q_g`, PnL, a grade, or Agent confidence into dollars. A profitable
Agent may become eligible for review of a larger pre-approved template, but
the template's limits remain an independent human risk decision.

## 2. Relationship to existing authority

This specification narrows and implements the architecture in:

- `DELEGATION.md` for ownership and high-level invariants;
- `GRACE.md` and `GRACE_QUANTITATIVE.md` for evidence semantics;
- `USER_INPUT.md` for user identity and confirmation binding;
- `SYSTEM_BOUNDARIES.md` for module ownership; and
- the frozen Kernel plan for operations, reservations, execution attempts,
  canary limits, reconciliation, and broker effects.

It does not rename or reinterpret existing Kernel facts. In particular:

- a `DelegationGrant` is a reusable, scoped authority lease;
- the existing Kernel `trade_grant` row is a one-operation, irreversible daily
  execution entitlement and runaway-loop charge;
- an autonomous open requires both records;
- an exact-confirmed open requires a Kernel `trade_grant` but no
  `DelegationGrant`; and
- a Kernel-verified reduction requires neither, although it still requires its
  canonical operation/reservation/attempt records where applicable.

The current Kernel configuration key `hard_limits` is historical naming, not
an authority classification. Checks that yield Class C are review thresholds
under the frozen Kernel plan. Kernel `REJECT` conditions, mode/capability
gates, the Live canary, account binding, buying power, quote sanity/freshness,
unknown/reconciliation latches, and other Kernel invariants remain
non-overridable except through their own separately specified Kernel state
transition, if one exists.

## 3. Canonical authority mode

Every proposed critical effect has exactly one `authority_mode`:

```text
autonomous_grant
exact_confirmation
kernel_verified_reduction
user_or_kernel_emergency
```

The deterministic Policy Resolver selects the route before effect admission.
An LLM may suggest a route but cannot set it. More than one route, no route, or
a route inconsistent with origin/effect class is invalid.

### 3.1 `autonomous_grant`

Used only for an Agent-originated, risk-creating operation that references one
current compatible `DelegationGrant`. In v1, an autonomous Live open must also
be Class B after the full current Kernel gate. Class C never becomes autonomous
merely because a grant exists.

### 3.2 `exact_confirmation`

Used for one immutable Kernel-owned operation review ticket. It substitutes
for an autonomous grant for that operation; it is not intersected with one and
does not upgrade one. The operation still intersects every applicable Kernel,
human absolute, canary, reservation, Provider, and account rule.

There is no silent fallback from `autonomous_grant` to exact confirmation. A
denied autonomous candidate may be explicitly rerouted only by creating a new
proposal revision and a new Kernel ticket. The original authority decision
remains immutable.

### 3.3 `kernel_verified_reduction`

Used only after Kernel proves from canonical position/order state that the
normalized action cannot add, reverse, reopen, or expand risk. Agent labels,
grant fields, and human prose are not reduction proof.

### 3.4 `user_or_kernel_emergency`

Used for a separately originated authenticated user or deterministic Kernel
safety action. It cannot borrow an Agent Artifact after the Agent path failed
to commit its required BehaviorEvent. It does not authorize new risk and is
fully distinct from Delegation activation, breaker resume, canary widening,
or unknown-order adoption.

## 4. Human authority classes are separate

The system does not have one generic `admin approved` effect. At minimum it
distinguishes:

| Effect class | Object confirmed | Consequence |
|---|---|---|
| `trade_exact_confirmation` | one Kernel operation ticket | one bounded new-risk entitlement after re-gating |
| `delegation_grant_activation` | one validated AuthorizationProposal digest | activate/replace one scoped grant |
| `delegation_policy_activation` | one validated PolicyRevision | change future policy; never reinterpret an old grant |
| `kernel_breaker_resume` | one reason/day/ledger transition | re-evaluate Kernel breaker state; does not activate a grant |
| `live_canary_revision` | one immutable canary revision | tighten or conditionally widen the Kernel canary |
| `unknown_effect_resolution` | one attempt and canonical broker fact | reconcile/adopt/deny one ambiguous effect |
| `emergency_risk_reduction` | one canonical position/order action | reduce risk only |

Each class has a distinct command schema, credential permission, reason
contract, expiry, idempotency key, and audit event. A token or UI button
authorized for one class cannot be replayed as another. V1 may have one human
owner, but privileged functions still require independent deterministic
Validator artifacts where specified and separate service credentials/write
paths. A second human quorum is not invented where none exists.

## 5. Capability lattice, not a score ladder

Machine policy uses immutable `AuthorizationTemplateRevision` objects. A
template is a point in a capability lattice:

```text
account/ledger/mode
  x action and position effect
  x product and instrument universe
  x Strategy/decision pipeline/Role bindings
  x session/regime
  x order and execution-management shapes
  x risk, cash, concentration, frequency, and duration envelope
  x monitoring and Provider requirements
```

Two templates have an upgrade relationship only when the policy declares an
edge and the target is a proven superset or otherwise explicitly reviewed
transition. Strong equity evidence cannot unlock options. A wider symbol
universe cannot silently unlock a wider risk cap. A new action cannot be
obtained by moving a UI tier label.

For human readability, the Web may show these stages:

| Display stage | Machine consequence |
|---|---|
| `RESEARCH_ONLY` | no operation submission |
| `SHADOW` | bounded Shadow operations; zero Provider mutation |
| `LIVE_CONFIRM` | may submit a candidate for exact human confirmation; no autonomous Live authority |
| `LIVE_AUTONOMOUS_CANARY` | narrowly scoped autonomous Class-B Live capability |
| `LIVE_AUTONOMOUS_BOUNDED` | a reviewed broader template that remains below human/Kernel ceilings |

These labels do not define ordering across template families. The template
graph does.

## 6. Cold-start and initial production policy

The required evidence path is:

```text
Research
  -> Shadow
  -> exact-confirmed Live observations
  -> CREDIBLE and compatible evidence
  -> human-reviewed autonomous Live canary template
  -> new post-expansion evidence
  -> ROBUST and compatible evidence
  -> next explicitly connected bounded template
```

Shadow-only evidence never becomes Live economics. Sparse evidence may keep a
subject at `LIVE_CONFIRM` indefinitely. That is a valid result, not a reason to
weaken GRACE.

The first production `DelegationPolicyRevision` must set:

```text
live_autonomy_enabled = false
maximum_live_display_stage = LIVE_CONFIRM
```

The schemas and observe-only decisions for Live autonomous templates may be
implemented and tested, but no Live `DelegationGrant` may activate until the
rollout gates in this document pass and a later signed PolicyRevision enables
the exact template. Existing M11 requirements remain: production stays
read-only until separately changed, the first Alpheus-routed canary is an exact
human-confirmed whole-share equity limit ticket, option mutation is disabled,
and the Kernel's current $50 daily authorized-risk canary and five-clean-day
widening rule remain independent ceilings.

## 7. `DelegationPolicyRevision`

A policy revision is immutable and contains fields equivalent to:

```text
delegation_policy_revision_id, schema_revision, parent_revision_id
created_at, data_cutoff, proposed_effective_at, expiry
intended human policy owner, canonical digest, rollback-semantics target

supported account/ledger/deployment scopes
live_autonomy_enabled and maximum enabled template families
AuthorizationTemplateRevision registry and transition graph
BudgetPoolRevision registry and scope bindings
exact-confirmation policy and Kernel Class-B/Class-C routing policy

compatible GRACEModelRevision, CalibrationPackRevision,
EvaluationProfileRevision, and ScoreSnapshot schema matrix
required ScoreSnapshotBinding predicates per template
material revision classes and directional CompatibilityDecision rules

promotion, renewal, cooldown, dwell, downgrade, suspension, revocation,
expiry, and reason-specific requalification rules
source-of-truth, head/cursor, freshness, and health-lease requirements
Provider, monitor, Strategy, Agent deployment, and Kernel capability gates

envelope algebra, units, rounding, market timezone, session calendar,
null/zero handling, and fail-closed reason precedence
rollout ceiling, acceptance manifest, limitations, and signatures
```

Lifecycle does not live inside that immutable body. Validation artifacts,
`HumanPolicyDecision`, and append-only `DelegationPolicyStateEvent`s bind the
revision digest. One fenced `ActiveDelegationPolicyHead` per declared policy
scope contains the current active revision id/digest and monotonic generation.
The privileged policy Activation Path may CAS that head only from a matching
Engine result, independent Validator attestation, human decision, and fresh
activation-time revalidation. Retirement and rollback append events and move
the head; they never edit `state`, `retired_at`, approval, or activation fields
inside the revision. Rollback semantics are published as a new revision and
activation, not a pointer resurrection of an old authority object.

V1 has no timer-only policy activation. A desired future effective time is
proposal metadata, not authority: at or after that time the privileged path
must rerun freshness/compatibility checks and explicitly win the head CAS.
Scheduler availability, clock passage, or a pre-existing approval cannot make
a stale policy active by itself.

No required field has a permissive default. Missing caps do not mean unlimited;
they make the template invalid. Unknown gate results cannot support activation,
renewal, widening, or continued autonomous admission.

Policy activation is linearizable per account/ledger policy scope. In v1 a
tightening transaction may only atomically suspend affected active grants
before the ActiveDelegationPolicyHead moves to the new policy; it never creates
or installs a replacement grant while holding policy/source heads. Any
conservative replacement follows later through the ordinary proposal-first
grant-install transaction under the new policy. Loosening never enlarges an
existing grant; it only permits a new proposal. Rollback creates a new
effective transition and never revives an expired, revoked, stale, or currently
incompatible grant.

## 8. `AuthorizationTemplateRevision`

A template contains fields equivalent to:

```text
authorization_template_revision_id, family_id, parent_revision_id
human display stage and exact predecessor/successor transition ids
account/ledger/deployment selectors
subject and material-revision selectors
allowed actions and position effects
allowed product kinds, instrument universe revision, and regime selectors
allowed order types, price constraints, TIF, sessions, and market calendar
execution-management permissions and effect expiry
fixed envelope and required BudgetPoolRevision bindings
required ScoreSnapshotBinding set
Provider/monitor/Kernel capability and freshness predicates
grant maximum duration, renewal mode, cooldown, and review epoch
deterioration actions and reason-specific restart ceiling
```

V1 autonomous Live templates permit only Kernel-supported long equity `open`
through the certified whole-share limit shape. `open sell`, options, fractional
shares, multi-leg structures, dollar orders, market orders, extended-hours
sessions, uncovered exposure, and any unsupported Provider shape are absent and
therefore prohibited. A future capability requires a new template family and
its own Provider and Kernel certification; a high GRACE grade cannot create it.

## 9. GRACE eligibility is categorical and non-compensatory

For a template `T`, the Delegation Policy Engine evaluates:

```text
Eligible(T) =
  active policy permits T
  AND exact scope and revision compatibility
  AND every required ScoreSnapshotBinding resolves exactly once
  AND every required publication_class == current_authority
  AND every required Champion/Profile/Calibration compatibility passes
  AND every required plane satisfies its categorical floor
  AND every required IntegrityStatus == CLEAR
  AND no blocking limitation or transition flag is present
  AND transition/cooldown/new-cohort requirements pass
  AND Strategy, Agent, Provider, monitor, ledger, and Kernel health pass
```

Every gate produces `PASS`, `FAIL`, `UNKNOWN`, or `N/A` plus a stable reason
code. `UNKNOWN` is never favorable.

Forbidden mappings include:

```text
risk_cap = f(Q_g)
risk_cap = f(realized PnL)
stage = floor(score * N)
larger account equity -> mutate current grant cap upward
loss -> recovery or martingale envelope
```

`Q_g` is diagnostic in v1 and has no policy effect. A future reviewed policy
may use it only as an additional conjunctive requirement. It can never replace
a plane, clear Integrity, or determine an amount.

### 9.1 Required Snapshot constellation

The target `DecisionPipelineRevision` declares the complete material Role and
behavior graph. Policy derives the required Snapshot set from that immutable
manifest; the proposer cannot select a favorable subset.

An autonomous Live new-risk template requires, at minimum:

- a StrategyVersion x DecisionPipeline x applicable product/regime economic
  Snapshot whose Live economics and Risk/Tail planes are required;
- the Decision Desk's applicable behavior/predictive Snapshot;
- every upstream Role Snapshot declared material by the active pipeline;
- a Position Manager Snapshot if the template permits autonomous management
  beyond Kernel-native risk reduction/cancel safety; and
- the portfolio/Strategy concentration or tail scope required by the active
  Evaluation Profile.

Role-local credibility does not receive duplicated Strategy PnL. Strategy
profit cannot compensate for an unreliable Role, and a strong Role cannot
compensate for insufficient or adverse Strategy economics.

`ScoreSnapshotBinding` contains fields equivalent to:

```text
binding_id and required subject type/id/scope
score_snapshot_id and immutable digest
required EvaluationProfileRevision
required publication class: current_authority
GRACEModelRevision, CalibrationPackRevision, Champion activation reference
required plane set and minimum categorical state per plane
required joint grade and IntegrityStatus
allowed behavior/outcome evidence classes and transfer restrictions
regime/product/Strategy/Agent/Role/decision-pipeline joins
as_of, evidence_cutoff, expiry, source head event, and supersession state
required/forbidden limitation, deterioration, tail, selection, and shift flags
```

Only exact scope-compatible actual-Live evidence may satisfy a required Live
economic binding. Cross-class transfer is zero unless the active reviewed GRACE
contract says otherwise, and no transfer may manufacture Live authority.

### 9.2 Categorical outcomes

| Required evidence state | Autonomous consequence |
|---|---|
| any `INVALID`, Integrity `BREACHED`, or required `ADVERSE` | suspend/revoke autonomous new risk and enter reason-specific review |
| any `INSUFFICIENT`, `UNCERTAIN`, `UNRESOLVED`, `REQUALIFYING`, stale, incompatible, or unresolved required tail | no autonomous eligibility; Research, Shadow, or exact-confirmation route only |
| every required binding at least `CREDIBLE`, Integrity `CLEAR` | eligible to propose the explicitly connected Live canary template; not automatically active |
| every required binding `ROBUST`, Integrity `CLEAR`, transition and post-expansion evidence complete | eligible to propose one explicitly connected broader template; not automatically active |

Delegation does not add a second raw sample-count threshold. Evidence adequacy,
independent clusters, coverage, tail support, and uncertainty burdens belong to
the referenced GRACE Profile/Model/Calibration Pack. A higher template demands
a stronger reviewed Profile or categorical floor rather than an invented
`trades >= N` shortcut.

## 10. Material revisions and compatibility

Every proposal and grant binds a `MaterialRevisionManifest` containing entries
equivalent to:

```text
revision class, owner, id, digest, effective interval
AgentRevision and deployed model/config/prompt package
RoleContractRevision and DecisionPipelineRevision
StrategyVersion and InstrumentUniverseRevision
Tool/CapabilityRegistryRevision and relevant Data/EvidenceContract revisions
GRACE EvaluationProfile/Model/CalibrationPack revisions
Delegation policy/template/budget revisions
Kernel limits/capability/live-canary revisions
Provider contract and bound-account capability revision
monitoring contract revision
```

Rolling Evidence snapshots remain operation inputs; the manifest binds their
contract and immutable references rather than pretending one market snapshot
lasts for the grant lifetime.

Any material change is incompatible by default. An immutable, directional
`CompatibilityDecision` may declare only:

```text
exact
narrowing_only
approved_evidence_transfer
incompatible
```

It names `from`, `to`, exact scope, evidence, approver, effective/expiry times,
and limitations. Compatibility is not symmetric and cannot be inferred from a
shared name, parent, code similarity, or LLM explanation. `narrowing_only` may
preserve or replace with less authority; it cannot widen. `approved_evidence_transfer`
may permit GRACE evidence use under the GRACE transfer contract but still
requires a new authorization proposal and grant.

Champion promotion, rollback, Snapshot correction, Strategy activation, Agent
deployment, prompt/model change, Tool/Data contract change, Provider contract
change, or policy revision never mutates an old grant. A current compatible
proposal must be recomputed.

## 11. Canonical grant partition, scope, and matching

Policy defines two different identities that must not be collapsed:

- `stable_partition_key` identifies the one replaceable authority slot. Its
  canonical preimage is exactly
  `(immutable_partition_namespace_id, internal_account_key, ledger, deployment_mode,
  delegation_subject_slot_id, authorization_template_family_id)`. The
  human-owned `delegation_subject_slot_id` is an immutable registry id for the
  policy subject lineage, not a mutable name. The key excludes changing Agent,
  Strategy, model, prompt, and other material revision ids.
- `grant_scope_digest` binds the exact current account, ledger, deployment,
  decision pipeline, Strategy, grantee/Role, material revisions, capability,
  product/action, instrument-universe, session, and regime selectors.

If changing revisions were placed in the partition key, the old and new
revisions could each retain an active head. Separating the stable replacement
slot from the exact immutable grant scope makes a revision switch atomic.
Canonical serialization and digest algorithms are schema-versioned.
Changing the serialization schema does not create a new logical partition;
an identity migration preserves the same registered stable key and cannot run
while an old/new head pair coexist.

One `DelegationScopeHead` exists per `stable_partition_key` and contains a
monotonically increasing `generation`, current grant id/digest or none, derived
status, current AuthorityHealthLease id/digest or none, policy revision, and
owner-published source-head bindings. An `active` head must contain both a grant
and matching lease; neither may be installed alone.

Rules:

- one autonomous operation resolves exactly one stable partition, grant id,
  grant-scope digest, current admission ScopeHead generation, and current
  AuthorityHealthLease id/digest;
- Kernel resolves it canonically and never accepts an Agent-supplied copy;
- zero matching active grants fails closed;
- more than one matching active grant is an integrity fault and fails closed;
- grants are never unioned, intersected opportunistically, or ranked by
  generosity;
- an Agent cannot omit dimensions to match a broader grant;
- a grant cannot be shopped across accounts, ledgers, Strategies, products,
  regimes, sessions, or revisions; and
- existing positions retain their entry provenance after a grant changes, but
  risk reduction remains available under current canonical state.

The deterministic Policy Resolver derives the partition from the operation's
authenticated pipeline/capability binding. The Agent does not select from a
list of grants. If policy selectors make two partitions applicable to the same
operation, the policy revision is invalid; Kernel does not choose the more
generous one. Distinct partitions still charge every applicable account- and
ledger-wide Budget Pool.

Renaming an Agent/Strategy/pipeline or deploying a new revision does not create
a new subject slot. Subject-slot split, merge, or retirement is a privileged
identity-governance decision with explicit predecessor/successor mappings; it
cannot discard same-day budget lineage, open exposure, adverse history, or
requalification obligations.

## 12. Envelope algebra and units

Every numeric amount uses the Kernel's exact integer unit contract. Currency
amounts use base-currency micro-units; quantity uses exact Kernel quantity
units; counts are non-negative integers; times use database UTC timestamps;
market-day windows use the configured `TZ_MARKET` and frozen calendar revision.
Floating-point arithmetic is forbidden on the gate path.

Envelope intersection is dimension-specific:

- **sets** (`actions`, products, instruments, sessions, order shapes): exact
  set intersection; empty means prohibited;
- **upper numeric bounds**: minimum after unit/currency compatibility;
- **lower quality/freshness requirements**: strictest requirement;
- **time intervals**: interval intersection; empty means inactive;
- **frequency windows**: every applicable counter must pass independently;
- **concentration/portfolio predicates**: logical conjunction using canonical
  Kernel exposure; never average percentages;
- **boolean capabilities**: logical AND;
- **revision requirements**: exact/directionally approved compatibility only;
- **unknown, null, overflow, unit mismatch, missing FX, or ambiguous calendar**:
  fail closed.

When converting or allocating integer risk, required capacity rounds up and
remaining capacity rounds down. A release occurs only from reconciled Kernel
facts and never from an Agent estimate.

Each Live template must explicitly bound at least:

```text
per-operation authorized risk and required cash
subject concurrent reserved-plus-open risk
total account/ledger reserved-plus-open risk ceiling
per-symbol and declared concentration ceilings
daily new-risk operation count
daily irreversibly authorized risk
maximum working new-risk orders and open positions
allowed sessions and grant/effect expiry
```

Additional product-specific dimensions are mandatory when the Kernel can
represent them. Absence is prohibition, not infinity.

Caps stored in a grant are fixed integers. Profit or account-equity growth does
not enlarge them during the grant. Current Kernel percentage limits may shrink
the effective amount when equity falls. A larger fixed cap requires a new
human-approved template/grant; it is not a GRACE side effect.

## 13. Budget pools and usage

Template envelopes bind one or more immutable `BudgetPoolRevision`s. Pools may
be account-wide, Strategy-wide, template-family-wide, or grant-lineage-specific.
Every autonomous operation receives the complete policy-derived pool set; the
Agent cannot remove one.

As with grants, pool replacement identity and exact revision identity are
separate:

```text
stable_budget_pool_partition_key = canonical_digest(
  immutable_budget_partition_namespace_id,
  internal_account_key,
  ledger,
  human_owned_budget_subject_slot_id,
  budget_dimension_family_id,
  window_specification_id
)
```

The stable key excludes Policy, Template, Grant, and Pool revision ids. One
`BudgetPoolHead` per stable key contains a monotonic generation and the current
`BudgetPoolRevision` id/digest. A revision contains the exact cap dimensions,
scope selector, window/calendar/timezone, effective interval, predecessor,
and human policy approval. Gate transactions lock heads, not a revision-specific
counter that can be replaced to reset usage.

BudgetPoolHead is an authoritative money-path projection owned by the
privileged Delegation policy/activation path. Every transition supplies an
expected generation and atomically appends its pool-transition event. Engine,
Validator, Agent, Web, GRACE, and Kernel credentials cannot update it; Kernel
may row-lock/read it and write only its own charges. Concurrent pool activation
or tightening has one generation winner.

Usage is exact:

- irreversible count/risk usage is the sum of `DelegationCharge`s for the
  stable pool key and canonical window across every old/current revision;
- dynamic use is current held reservation plus remaining exposure joined
  through authority bindings/charges for that stable pool key, including old
  grants and working/unknown effects; and
- account/ledger global pools aggregate every applicable grant partition, not
  merely one grant's `budget_lineage_id`.

Replacement, renewal, promotion, rollback, PoolRevision activation, or process
restart does not reset a market-day budget. Related grants retain the
policy-assigned `budget_lineage_id` as an additional provenance dimension, but
the stable pool key is the charge boundary. A new grant, policy, or pool
revision cannot spend the same allowance again.

A midday tightening compares the new cap with all already charged/dynamic use.
If use meets or exceeds the cap, the pool becomes `draining_no_new_risk`; no
usage is deleted or made negative. Widening applies only after its privileged
policy transition and retains all prior charges. Pool re-key/split/merge is
forbidden inside an active market-day window in v1. It may become effective at
a new window only after unresolved effects are zero and a reviewed carry-forward
maps every still-open exposure conservatively; missing or ambiguous mapping
blocks new risk.

An active grant binds each stable pool key plus the PoolRevision in force at
issuance. Kernel also resolves the current PoolHead on every admission. A
directionally approved `narrowing_only` PoolRevision applies immediately as the
dimension-wise intersection of issuance and current caps. A widened current
PoolRevision cannot enlarge the old grant; the issuance cap remains until a new
grant activates. Any other revision mismatch blocks new risk. Each charge
records both the issuance binding and current PoolHead generation used by the
Gate.

At autonomous new-risk admission, Kernel creates an immutable
`OperationAuthorityBinding` and one or more `DelegationCharge` rows. The
binding contains fields equivalent to:

```text
authority_binding_id, schema_revision, operation_id, authority_mode
authority source oneof:
  autonomous: delegation_grant_id/digest, stable partition key,
              grant-scope digest, admission ScopeHead generation,
              AuthorityHealthLease id/digest
  exact confirmation: ticket/display/confirmation receipt ids/digests and
                      consumed TicketStateHead generation
applicable Delegation template/policy/BudgetPool bindings, or exact-confirmation
Kernel policy/canary bindings
account/ledger/market_day and canonical operation digest
Kernel-derived risk, cash, quantity, product, instrument, concentration facts
gate decision/reason manifest and database decision time
idempotency, causation, correlation, Artifact, BehaviorEvent, ticket-ack refs
admission-time reservation id, initial execution-attempt id/fencing lineage root
```

Each `DelegationCharge` binds one stable Budget Pool key, PoolHead generation,
PoolRevision/digest, lineage, canonical window, irreversible count/risk charge,
admission-time reservation id and dynamic-usage lineage root, and the same
authority binding. A later `DispatchAuthorization` binds one exact execution
attempt/fencing token to the authority state at the durable send boundary.
Later attempts, orders, fills, exposures, releases, and reconciliation records
refer back to the authority-binding/charge/lineage ids. They never backfill a
future reference into the immutable binding or charge.

Only the autonomous route creates DelegationCharges. Exact confirmation still
creates its authority binding and consumes the existing Kernel-global
`trade_grant`, canary, reservation, and counter capacity, but it cannot borrow
or reset a Delegation Budget Pool.

All three records are Kernel-owned. Delegation cannot insert, edit, release,
or delete them. The same admission transaction inserts the operation,
authority binding, charges, existing Kernel `trade_grant`, open reservation,
and execution attempt before any broker effect. A duplicate idempotency key
returns the original binding; changed content, including changed authority
mode/grant/ticket/revision, conflicts and never re-matches a new grant.

Daily count and daily authorized-risk charges are irreversible once admission
commits, even if placement fails, expires, is cancelled, or never fills.
Concurrent capacity remains held by open reservations/exposure and releases
only through the existing conservative reconciliation path. `unknown` retains
all charges and reservations.

Partial fills transfer risk from reservation to exposure without creating new
capacity. Cancel plus replacement does not consume a second new-risk use when
it stays inside the original operation, quantity, risk, cash, price cap, and
effect window. Any expansion is a new proposal and authority decision.

## 14. Authorization proposal and validation

The deterministic Policy Engine produces an immutable
`AuthorizationProposal` containing fields equivalent to:

```text
authorization_proposal_id, schema revision, canonical digest
evaluation time, database time, expiry, dedupe and causation ids
current scope head/generation and current grant reference
target policy/template/scope/material revision manifest
complete ScoreSnapshotBinding set and source-head manifest
current and proposed capability/envelope/budget-pool comparison
every gate result: PASS | FAIL | UNKNOWN | N/A plus reason code
transition edge, post-expansion cohort, cooldown/dwell/review facts
activation-authority class and proposed effective/expiry times
deterioration/requalification/rollback metadata
```

A proposal has no authority effect. It is reproducible from the immutable input
manifest. Its lifecycle fields are not edited into that body. A fenced
`AuthorizationProposalStateHead(proposal_id, generation, effective_state,
attestation_id, activation_authority_type, activation_authority_id,
activated_grant_id,
superseding_proposal_id)` plus append-only `AuthorizationProposalStateEvent`s
tracks workflow state. A deterministic Delegation Workflow Coordinator is the
sole writer of that projection; it cannot create or edit a proposal,
attestation, activation-authority record, grant, policy/Scope/Pool head, or
Kernel record.
Every transition validates the referenced immutable artifacts and uses
expected-generation CAS.

An independently deployed deterministic Validator recomputes the result and
publishes `ValidatorAttestation`:

```text
attestation id, proposal id/digest, Validator build/policy digest
reproduced input manifest and output digest
match/mismatch, limitations, decision time, expiry, signer identity
```

The Validator cannot activate a grant and the Engine cannot attest to itself.
Mismatch, missing input, stale source head, or `UNKNOWN` required gate blocks
activation.

For any proposal that could install a grant, the deterministic Engine also
produces an immutable `GrantActivationCandidate` containing fields equivalent
to:

```text
candidate id/digest, target grant id, derivation schema
proposal and proposal-attestation ids/digests
stable partition and expected ScopeHead generation
complete policy/template/material/ScoreSnapshotBinding manifests
grant scope, subject/grantee, capability, fixed envelope, Pool bindings/lineage
activation_not_before, admission/dispatch/working/governance deadlines
source/lease requirements, parent/replacement/cohort/reason manifests
activation derivation contract permitting only database effective_at, winning
ScopeHead generation, activation event, and final grant digest to be derived
```

The independently deployed Validator publishes a
`GrantActivationValidatorAttestation` over that candidate and its exact
derivation rule. All authority-bearing content and upper deadlines are fixed;
activation-time fields are derived only from locked database facts and can
shorten, never widen, the candidate. Engine cannot attest the candidate and
Activation cannot edit it.

`HumanDelegationDecision` binds exactly the proposal/attestation digests,
GrantActivationCandidate/attestation digests where a grant is proposed,
authenticated policy owner, decision, rationale, displayed envelope diff,
display receipt, timestamp, and expiry. It cannot edit proposal/candidate
fields. A different cap or scope requires a new Policy/Template revision and
proposal.

Every grant installation carries exactly one immutable discriminated
`ActivationAuthority`:

```text
human_decision(HumanDelegationDecisionRef)
automatic_narrowing(AutomaticNarrowingAuthorizationRef)
```

`human_decision` is required for the first grant, every widening/promotion, and
any transition the active PolicyRevision does not explicitly preauthorize.
`automatic_narrowing` is permitted only for an equal-or-narrower replacement
whose exact transition edge, old/new envelope comparison, current policy/head
generation, proposal/Validator digests, reason, cooldown, and expiry are fixed by
the active policy. The deterministic Engine creates the candidate; the
independent Validator attests the dimension-wise non-widening proof. Missing,
both, unknown, stale, mismatched, or a purported automatic first/wider grant
fails closed.

## 15. `DelegationGrant`

The privileged Activation Path may create an immutable grant only from one
valid proposal, matching proposal and GrantActivationCandidate Validator
attestations, and exactly one valid ActivationAuthority. Creation of the final
grant and its matching initial AuthorityHealthLease, initial `active` StateEvent, proposal
`-> activated` event, and installation into the ScopeHead are one transaction
after activation-time revalidation; an uninstalled grant body has no authority.
It contains fields equivalent to:

```text
delegation_grant_id, schema revision, canonical digest
GrantActivationCandidate and candidate-attestation references
authorization proposal, Validator attestation, ActivationAuthority reference
policy/template/material revision and ScoreSnapshotBinding manifests
stable partition key, grant-scope digest,
activated_at_scope_head_generation, subject/grantee and capability set
fixed envelope, BudgetPoolRevision bindings, and budget lineage
effective_at, admission_not_after, dispatch_not_after,
working_order_not_after, governance_expires_at, next_review_at
source-head and health-lease requirements
parent, replaces_prior_grant_id, rollback-semantics target, and qualification-
cohort references known at activation
activation record and immutable reason manifest
```

The final grant is the deterministic candidate plus only the declared
activation-derived database time, winning ScopeHead generation, and activation
event. Those values and the final digest are computed inside the locked install
transaction. The grant digest intentionally does not include a future or
current lease id/digest; the ScopeHead atomically binds the independently
digested grant/lease pair. The final lease binds the resulting grant id/digest
and its target GrantActivationCandidate, so there is no circular digest.
Activation cannot choose a different cap, deadline, revision, scope, or parent.
The target grant id was already fixed by the candidate.

All grant lineage fields point backward to objects known at activation. A
future `replaced_by_grant_id` exists only in the old grant's append-only
GrantStateEvent and current ScopeHead projection; it is never backfilled into
the immutable old grant.

The grant field is specifically `activated_at_scope_head_generation`: immutable
activation provenance, not a promise that the head can never advance. Kernel
does not compare this historical generation with the current head when a later
compatible AuthorityHealthLease is activated for the same grant. New
admissions instead bind the then-current ScopeHead generation and lease in the
Kernel-owned `OperationAuthorityBinding`.

A grant contains no Agent-editable limit and no copied GRACE value that Kernel
trusts without resolving its canonical record. `admission_not_after` is no
later than the earliest immutable applicable Snapshot, policy, compatibility,
canary, deployment-contract, or template validity boundary. It is the fixed
grant ceiling. The shorter rolling monitoring/source-health window belongs to
the current `AuthorityHealthLease`, not to this immutable deadline.

The three effect deadlines are distinct:

- `admission_not_after`: last database time a new operation may consume the
  grant;
- `dispatch_not_after`: last time a committed but unsent attempt may receive a
  durable Provider-send fence; and
- `working_order_not_after`: last time Kernel may intentionally leave the
  unfilled new-risk remainder working before issuing a safety cancel.

All authority intervals are half-open under database time: a check passes only
when `effective_at <= database_now < applicable_not_after`. Equality at a
`not_after` boundary is expired.

Grant construction requires:

```text
effective_at = activation database time
effective_at < admission_not_after
admission_not_after <= dispatch_not_after
dispatch_not_after <= working_order_not_after
working_order_not_after <= governance_expires_at
```

Every timestamp is finite. Missing, equal first interval, inverted, overflowed,
or timezone-ambiguous deadlines invalidate the proposal/grant. A tighter source
validity may shorten any later deadline while preserving this order; it never
extends an earlier source boundary.

V1 forbids future-scheduled grants. Clock passage never changes a grant from
non-authoritative to authoritative. A proposal may wait, but at the eventual
activation transaction the Engine result, independent attestation, human
decision or automatic-narrowing authorization, source heads,
policy/template/material revisions, budgets, and every
categorical gate are checked again; stale or failed input produces no grant and
no ScopeHead change. Future scheduling requires a separately reviewed state
machine and is not implemented by overloading `effective_at`.

Autonomous admission therefore requires
`current_lease.valid_from <= database_now <
min(grant.admission_not_after, current_lease.valid_until)` in addition to every
other gate. Advancing a compatible lease may extend only the rolling lease
sub-window; it cannot move any fixed grant deadline or extend the grant ceiling.

`governance_expires_at` ends the grant record's effective governance lease; it
does not erase already sent orders, fills, positions, or accounting facts.

Grant bodies never change state in place. Append-only `GrantStateEvent`s and
the linearized ScopeHead determine effective state.

## 16. Proposal, grant, and head state machines

### 16.1 Authorization proposal

```text
draft -> validation_failed | validated
validated -> awaiting_human | automatically_eligible_narrowing
awaiting_human -> approved | rejected | expired | superseded
approved -> activated | expired | superseded
automatically_eligible_narrowing -> activated | expired | superseded
```

Automatic activation is limited to an equal-or-narrower replacement explicitly
pre-authorized by the active policy. V1 promotion/widening always requires the
human-decision ActivationAuthority; automatic replacement requires the exact
AutomaticNarrowingAuthorization. A proposal state transition uses idempotency plus expected
generation on `AuthorizationProposalStateHead`; concurrent winners collapse to
one. The immutable AuthorizationProposal body never receives a `state`,
decision, attestation, activation, expiry, or supersession field after creation.

Grant installation first locks the ProposalStateHead at the exact expected
`approved` or `automatically_eligible_narrowing` generation. Its one permitted
activation transition, grant/lease creation, activation events, and ScopeHead
CAS commit atomically. Expiry or supersession racing that transaction either
wins first and prevents installation or loses to `activated`; there is no grant
from a no-longer-approved proposal.

### 16.2 Grant effective state

```text
active -> suspended | replaced | expired | revoked
suspended -> replaced | expired | revoked | requalifying
requalifying -> new proposal only
```

`active` exists only after the creation/installation transaction wins the
ScopeHead CAS. There is no `scheduled` grant state in v1. An old grant is never
resumed in place. Recovery creates a new proposal and grant with a new
generation. `eligible_for_review` is an Engine result, not a grant state.

### 16.3 Non-symmetric transitions

- widening moves across at most one declared template edge;
- the evidence cohort used to enter one template cannot alone cross the next;
- widening is cooldown/dwell bound and initially human-approved;
- tightening, suspension, or revocation may cross multiple edges immediately;
- no transition creates a martingale or profit-following cap;
- rollback re-evaluates current facts and never resurrects an old grant; and
- database time, not Agent/Web time, determines effective and expiry ordering.

## 17. Deterioration, decay, and requalification

### 17.1 Immediate safety events

Integrity `BREACHED`, required `ADVERSE`, confirmed model invalidation, Provider
unknown, reconciliation barrier, missing mandatory monitoring, or a Kernel
breaker suspends autonomous new-risk admission. GRACE or Delegation state alone
never removes the authority to reduce risk. However, Provider `unknown` retains
the frozen M11 account-wide execution latch: every **automatic** Provider
mutation, including close, cancel, and reprice, remains blocked while read-only
pull/reconciliation continues. Only canonical resolution of the uncertain
effect, or a separately authenticated emergency action after a fresh broker
pull, may pass that Kernel latch.

### 17.2 Qualification expiry

Stale/expired Snapshot, incompatible regime/scope/revision, or lapsed model/
policy compatibility prevents renewal or widening and expires no later than the
current lease boundary. Historical evidence remains immutable; it is not
arithmetically decayed by subtracting points each day.

### 17.3 Statistical deterioration

Delegation consumes only a deterioration state published by the frozen GRACE
model. Continuous per-trade downgrade tests require a separately validated
sequential boundary/confidence-sequence method. Otherwise policy evaluates only
at fixed review epochs; repeated peeking cannot manufacture significance.

### 17.4 Reason-specific requalification

- **Integrity:** root-cause repair, material revision, deterministic tests,
  Shadow/observe-only no-recurrence evidence, and human requalification;
  later profit is not repair.
- **Statistical adverse:** new independent out-of-sample compatible evidence
  after the remediation cutoff; the triggering window cannot grade its own
  repair.
- **Model/scope incompatibility:** explicit compatibility review or restart
  from the conservative template required by policy.
- **Provider ambiguity:** canonical reconciliation only; neither GRACE nor
  human explanation clears it.
- **Operational/monitoring failure:** service restoration plus health/replay
  acceptance before a new proposal.

Each reason has a policy-declared restart ceiling. No reason automatically
restores the prior template.

## 18. Exact operation confirmation contract

The Agent path first commits its Artifact, BehaviorEvent, and accepted GRACE
intake/EvaluationTicket acknowledgement. The deterministic Proposal Validator
then sends a non-effectful candidate to Kernel with
`authority_mode=exact_confirmation`.

Kernel canonicalizes and validates the candidate without creating a
reservation, `trade_grant`, or execution attempt. If it is reviewable, Kernel
creates the immutable operation in `pending_review` plus a Kernel-owned
`OperationConfirmationTicket` containing fields equivalent to:

```text
confirmation_ticket_id, schema revision, nonce, canonical digest
effect_class=trade_exact_confirmation
operation id/revision/digest and authority mode
authenticated account owner, account, ledger, deployment mode
Agent/Role/Strategy/pipeline/Artifact/Behavior/Ticket references
canonical product, instrument id, symbol, action, position effect and side
exact quantity, order type, limit/price cap, TIF, and session
Kernel-derived max risk, required cash, maximum loss, and exit-plan digest
Kernel limits, live-canary, Provider contract, and capability revisions
ReviewExceptionVector: each current Class-C failure, observed value, threshold,
unit, conservative comparator, and severity
all non-overridable gate facts
issued_at, confirmation_not_after, dispatch_not_after,
working_order_not_after
supersedes_prior_ticket_id and issuance reason, if this is a replacement
```

The immutable Kernel ticket cannot contain a future display fact. When Web
actually renders it, the dedicated User Authority Gateway
(`user-authority-gateway`) creates an immutable
`TicketDisplayReceipt` containing fields equivalent to:

```text
display_receipt_id, schema revision, canonical digest
confirmation ticket id/digest and rendering-contract revision
authenticated subject/account/session
digest of every rendered material field and exception vector
displayed_at, client delivery acknowledgement, idempotency key
```

The User Authority Gateway submits the display artifact through an
authenticated typed command.
Kernel validates it, requires its own database time to be before
`confirmation_not_after`, and CAS-transitions a Kernel-owned
`OperationConfirmationTicketStateHead(ticket_id, generation, effective_state,
display_receipt_id, confirmation_receipt_id)` from `issued` to `displayed`.
The immutable ticket never changes.

The User Authority Gateway then binds an authenticated response to exactly one
still-current display receipt and creates an immutable `ConfirmationReceipt`
with the ticket and display-receipt ids/digests, decision, subject,
session/authentication facts, timestamp, expected ticket generation, and
idempotency key. Natural-language text alone is not the receipt. The User
Authority Gateway submits it as another authenticated command; Kernel validates
it and CAS-
transitions the same state head only when Kernel database time is still before
`confirmation_not_after`; otherwise the ticket expires. Receipt/client clocks
are audit facts and never extend authority. Neither service writes the other's
records.

The frozen local ticket lock suffix is
`pending operation row -> TicketStateHead` for every transition that touches
both records. A non-effectful display, reject, expiry, supersession, or receipt
attachment may use this local suffix without platform heads; a head-only
transaction must finish without later acquiring the operation row. Effectful
confirmation consumption/admission uses the full
`sorted PlatformMode/EffectClass/KillSwitch heads -> pending operation row ->
TicketStateHead -> ledger -> symbol/order/attempt` order. No cleanup or ticket
transition may take a later lock and then reach backward for an earlier one.

Effective ticket state is linearized by the Kernel-owned StateHead and its
append-only `OperationConfirmationTicketStateEvent`s:

```text
issued -> displayed | expired | superseded
displayed -> confirmed | rejected | expired | superseded
confirmed -> consumed | gate_denied | effect_expired
```

`displayed` exists only with one valid TicketDisplayReceipt. `confirmed` or
`rejected` exists only with a ConfirmationReceipt bound to that display. Kernel
owns the StateHead plus issue/display/confirm/reject/supersede/expire/consume/
gate events; the User Authority Gateway owns display and response receipt
candidates. Ordinary Input Gateway, Web, Agent Runtime, Workers, and CI have no
receipt-write or receipt-signing credential. Every
transition supplies the expected generation and has unique ticket/decision
constraints. Confirm, reject, expiry, and supersession racing from the same
generation have one winner; losing receipts remain inert audit artifacts.
Duplicate delivery is idempotent, while conflicting receipts fail closed.

The future `superseded_by_ticket_id` exists only in the winning StateEvent/
StateHead projection. The immutable ticket may name only the prior ticket it
superseded at issuance; it cannot contain a future mutable successor.

`confirmed` is not broker authority by itself. Kernel consumes the receipt in
one transaction that first locks the current PlatformMode, relevant EffectClass,
and every applicable KillSwitch head in canonical order, then locks the pending
operation and TicketStateHead at the expected `confirmed` generation, checks
the exact head generations/digests, receipt digests, and expiry,
re-fetches current canonical account/quote/Provider facts, reruns the
full gate, and compares the current reviewable exception vector with the one
displayed. The current failed-key set must be a subset of the displayed set,
and every retained exception must be equal or less severe under its frozen
dimension-specific comparator. If no reviewed monotone-safe comparator exists,
the observed value and threshold must match exactly. A new failed key, lower
capacity, wider spread, lower open interest, larger usage, worse severity,
missing fact, or `UNKNOWN` atomically records `gate_denied` on the StateHead
and requires a new ticket, display, and receipt. If a successor is issued, its
immutable `supersedes_prior_ticket_id` points to the denied ticket; the denied
StateHead does not take an undeclared `confirmed -> superseded` transition.

Only after that comparison passes may the transaction insert the approval
record, Kernel `trade_grant`, reservation, execution attempt,
exact-confirmation `OperationAuthorityBinding`, and applicable Kernel-global
charges, while CAS-transitioning the StateHead to `consumed`. Concurrent
confirms have one winner. A duplicate
idempotent request returns that result; cross-ticket or changed-content replay
fails.

The persisted quantity, product/instrument, position effect, side, order
shape, price cap, max-risk ceiling, account, and revisions cannot change. A
fresh quote may produce a better working price inside the confirmed cap. A
worse risk, higher cap, wider quantity, different instrument, changed order
shape, stale/insane quote, or expired effect window requires a new ticket.

Confirmation, broker dispatch, and working-order deadlines are separate. A
ticket cannot be confirmed at or after `confirmation_not_after`. A consumed
but not yet sent attempt cannot make its first provider call after
`dispatch_not_after`; it fails and releases concurrent reservation only after
Kernel proves no send occurred. An attempt durably marked sent before that
deadline may reconcile afterward. At `working_order_not_after`, Kernel must
cancel the still-unfilled new-risk remainder; a fill racing or preceding the
cancel is still a real authorized-order fact and must be ingested.

These ticket intervals use the same half-open database-time rule; Web/client
clock and display latency cannot extend them.

Ticket construction and receipt consumption require finite timestamps ordered
as:

```text
issued_at < confirmation_not_after
issued_at <= TicketDisplayReceipt.displayed_at
TicketDisplayReceipt.displayed_at <= ConfirmationReceipt.recorded_at
ConfirmationReceipt.recorded_at < confirmation_not_after
confirmation_not_after <= dispatch_not_after
dispatch_not_after <= working_order_not_after
```

Inversion or a zero confirmation interval invalidates/supersedes the ticket.

## 19. Exact confirmation versus Kernel Class B/C

Origin-aware routing is mandatory:

- Agent-originated Class-B new risk under `autonomous_grant` requires a valid
  Live grant and all other gates.
- Agent-originated Class-B or Class-C new risk under `exact_confirmation`
  requires the exact ticket/receipt flow.
- Class C is never accepted by `autonomous_grant` in v1.
- Kernel `REJECT` is never converted to C by Delegation or confirmation.

Under the current frozen Kernel behavior, an exact confirmation may accept the
reviewable Class-C checklist failures:

```text
whitelist
per_trade_budget
total_open_risk
daily_trade_count
plan_complete
liquidity_spread for a sane fresh non-crossed quote
liquidity_oi for an otherwise supported instrument
```

The v1 `ReviewExceptionVector` comparators are explicit:

- operation quantity, instrument, derived risk/cash, price cap, plan, and
  policy/canary revisions must remain exact;
- `per_trade_budget` is no worse only when the current cap is at least the
  displayed cap for the same immutable derived risk;
- `total_open_risk` is no worse only when current pre-operation open risk is no
  greater and the current cap is no lower than displayed;
- `daily_trade_count` is no worse only when the current irreversible count is
  no greater and the configured cap is no lower than displayed;
- `liquidity_spread` is no worse only when the fresh sane relative spread is no
  wider than displayed under the same quote/check revision;
- `liquidity_oi` is no worse only when current open interest is no lower and
  the required minimum is no higher than displayed; and
- whitelist and plan failures require the same exact failed facts; a newly
  missing plan field or newly disallowed instrument is a new exception.

A comparator never treats a newly failing check as safe. Policy/revision
change supersedes the ticket instead of reinterpreting its displayed values.

It cannot override, among other Kernel hard failures:

```text
wrong account/ledger/mode or account-binding failure
insufficient or unknown buying power/equity required by the gate
stale, crossed, locked where disallowed, non-positive, or otherwise insane quote
unsupported product/instrument/order shape/precision
uncovered short, invalid close, reverse-risk, or malformed operation
risk overflow or dishonest risk declaration mismatch
active breaker without its separate valid resume transition
live-canary exhaustion or unsupported canary increment
unknown execution latch or reconciliation/divergence barrier
missing reservation/ledger facts or unavailable Kernel authority source
```

This list is additive to every frozen Kernel invariant. The Delegation policy
cannot weaken it. If the owner later wants a new non-overridable absolute dollar
or percentage ceiling, it requires an explicit Kernel policy field and code
path; renaming an existing checklist does not create that property.

## 20. Other human transitions are not trade confirmation

Breaker resume re-evaluates only the named Kernel breaker reason for its
ledger/day under the existing rule. It neither confirms a trade nor resumes a
suspended DelegationGrant. After resume, a new autonomous operation still needs
a current active grant and every gate; if the grant was suspended because of
the same loss evidence, a new grant proposal/requalification is required.

Canary widening follows the immutable clean-day revision process and never
modifies a Delegation envelope. Both limits are checked; widening one cannot
widen the other.

Unknown-effect resolution binds the exact attempt, candidate broker order,
fresh canonical pull, and adoption/rejection transaction. It cannot create a
new `ref_id`, release uncertainty by timeout, or activate new risk.

Emergency/break-glass credentials are unavailable to Agents and ordinary Web
sessions. V1 break-glass may reconcile, cancel, or reduce risk only. Any future
new-risk break-glass design is a separate architecture review and is not
authorized here.

## 21. Kernel Delegation Gate contract

Kernel is the final decision owner. It does not call an LLM and does not trust
copied limits, copied scores, Web state, an Agent's risk declaration, or a
grant-shaped JSON object.

For autonomous new risk, Kernel validates at least:

1. `authority_mode=autonomous_grant` is legal for the authenticated Agent
   origin and effect class;
2. the locked current `PlatformModeHead`, relevant `EffectClassHead`, and every
   applicable `KillSwitchHead` permit this effect and are at or below the
   operation's other authority ceilings;
3. Artifact, BehaviorEvent, GRACE intake acknowledgement, Proposal Validator,
   operation idempotency, and decision graph references are committed/current;
4. the canonical stable partition, grant id/digest, grant-scope digest,
   current ScopeHead generation, current AuthorityHealthLease id/digest, and
   `active` state resolve exactly once;
5. database time is within grant/effect validity;
6. policy/template/material revisions and current authoritative source heads
   match the current AuthorityHealthLease bound at admission;
7. the operation's account, ledger, Strategy, pipeline, Agent/Role, product,
   instrument, regime/session, action, and order shape fit the grant;
8. Kernel-derived risk/cash/quantity/concentration facts fit every envelope and
   BudgetPoolRevision;
9. current Kernel Class B, limits, live canary, buying power, reservations,
   breaker, Provider health, account binding, quote, and reconciliation gates
   pass; and
10. the atomic use/reservation transaction has capacity in every applicable
   counter and pool.

The effective authority is the dimension-wise intersection of the operation's
exact confirmation or active grant, human-owned policy, Template, Strategy,
Budget Pools, Live canary, Kernel hard/checklist rules for that route, current
account capacity, and Provider capabilities. A valid grant is necessary but
never sufficient.

Kernel emits an immutable GateDecision with the resolved canonical ids,
digests, source heads and their generations, including the exact PlatformMode,
EffectClass, and KillSwitch heads, facts, `PASS/FAIL/UNKNOWN/N/A` gates, reason
codes, and database time.

A deterministic `FAIL` or `UNKNOWN` admission commits the canonical operation
candidate/request hash, authenticated idempotency identity, and negative
GateDecision, but creates no authority binding, charge, Kernel `trade_grant`,
reservation, attempt, or broker effect. An exact retry of that idempotency key
returns the same denial even if market, grant, breaker, or quote state later
changes. Changed content conflicts; a deliberate re-proposal uses a new
operation revision/idempotency key. Infrastructure failure before a valid
decision rolls the transaction back and is not mislabeled a policy denial.

## 22. Linearization, locking, and stale-state prevention

V1 autonomous Live authorization requires the canonical Delegation ScopeHead,
grant, source-head records, Kernel operation, budgets, reservations, and
execution attempt to be readable/lockable inside one PostgreSQL transaction.
Logical schemas and database roles remain separate; co-location does not grant
cross-owner writes.

The money path may not authorize from a cache, read replica, Web read model,
eventually consistent token, or an Agent-supplied capability. A future separate
database/service topology requires a separately reviewed linearizable
authorize-and-reserve protocol with equivalent revocation fencing; it is not
approved by this specification.

For a positive autonomous admission, the gate transaction:

1. takes the authenticated operation-idempotency/admission lock;
2. locks every authoritative source-head row in canonical
   `(owner_type, head_type, head_id)` order, including the current
   `PlatformModeHead`, relevant `EffectClassHead`, and all applicable
   `KillSwitchHead`s;
3. locks the current Delegation ScopeHead and captures its exact admission
   generation plus AuthorityHealthLease id/digest;
4. locks all applicable stable BudgetPoolHeads in canonical sorted-key order;
5. takes existing Kernel ledger, symbol, order, and attempt locks in the frozen
   global order;
6. validates the locked authoritative source heads, current active-policy
   head, lease/attestation, PoolHeads, and grant body;
7. computes current Kernel facts and remaining capacities;
8. inserts the operation, `OperationAuthorityBinding`, `DelegationCharge`s,
   Kernel `trade_grant`, reservation, and execution attempt; and
9. commits before any Provider mutation.

Grant activation/replacement first locks the exact ProposalStateHead expected
generation, then follows the shared prefix. Lease renewal, suspension, policy
transition, and any path touching more than one authority head starts at the
shared prefix: sorted authoritative source heads (including
ActiveDelegationPolicyHead), then sorted ScopeHeads, then sorted
BudgetPoolHeads. Scope/Pool transitions use expected generation. A single-head
publisher never acquires a later downstream lock.
Budget and grant locks never reverse the declared order. Twenty concurrent
operations at one remaining allowance admit exactly one. Twenty concurrent
promotions produce one new head generation.

The autonomous dispatch path uses `sorted authoritative source heads ->
ScopeHead -> sorted BudgetPoolHeads -> live account execution latch -> attempt`
ordering before its send fence. Existing Kernel helpers that currently acquire
the ledger or live execution latch before these checks must be refactored;
adding upstream queries inside their present callback would invert the order.
Exact-confirmation consumption has no Delegation Scope/Pool heads, but it must
lock the same PlatformMode, EffectClass, and KillSwitch heads before its
existing `pending operation row -> TicketStateHead -> ledger ->
symbol/order/attempt` suffix. Non-effectful display, confirm, reject, expire,
and supersede transitions that touch the operation retain only the local
`pending operation row -> TicketStateHead` suffix from section 17; the global
platform/effect/kill prefix is reserved for effectful consumption/admission and
send authorization. A head-only transaction never later reaches backward for
the operation row. Fill and reconciliation never reject a real fact because
authority later changed.

### 22.1 Authority health lease

An active ScopeHead references an `AuthorityHealthLease` containing the exact
subject-specific head ids for GRACE Snapshot/invalidation, Strategy activation,
Agent deployment, Policy, Provider capability, mandatory monitoring, and
relevant Kernel health publications. It also binds the immutable candidate and
independent attestation that justified that exact rolling window. Kernel
compares every bound head to the authoritative current head inside both the
admission and dispatch transactions.

The deterministic Delegation Engine first creates an immutable
`AuthorityHealthLeaseCandidate` containing the stable partition, expected
ScopeHead generation, an `authority_target` oneof consisting of either the
current grant id/digest for renewal or the target GrantActivationCandidate
id/digest for first install/replacement, exact policy/template/material
revision manifest, current source-head manifest, current categorical eligibility
results, requested `activation_not_before/valid_until`, maximum policy lease
duration, and canonical input/output digests. The independently deployed Validator
re-fetches the canonical sources, recomputes every categorical/template
predicate, and publishes a candidate-specific `LeaseValidatorAttestation`.
Engine cannot attest; Validator cannot activate.

Only then may the privileged Activation Path create an immutable
`AuthorityHealthLease` and CAS-update the ScopeHead expected generation in the
same transaction as its append-only transition event. It may do so only when
candidate, attestation, the applicable current-grant or target-candidate
binding, ScopeHead, active-policy head, source heads, and database time still
match; every required result is `PASS`; and the lease
window is finite, within the policy maximum, and no later than the grant's
`governance_expires_at`. The immutable lease's `valid_from` is exactly the
winning activation transaction's database time; activation must be at or after
the candidate's `activation_not_before` and strictly before its fixed
`valid_until`. Delayed activation can only shorten the attested window.
Activation cannot author, edit, or self-attest the candidate. Ordinary Engine/
Validator/Agent/Web/GRACE/Kernel credentials cannot write or extend the lease.
Kernel independently rechecks the exact source-head
identities, material-revision equality, fixed grant deadlines, policy/template
identity, and every Kernel-owned health predicate; it never trusts an
attestation to relax its own gate.

Same-grant lease advancement is allowed only by an explicit directional
`same_grant_lease_compatible` rule in the active policy. It cannot change the
grant scope, capability, fixed envelope, BudgetPool issuance bindings,
material revisions, evidence-transfer rules, or any deadline. A newer source
publication may qualify only when the Engine and independent Validator both
show that the complete current Snapshot constellation still meets every
original categorical floor inside the same material revisions. Any missing,
`UNKNOWN`, worse, incompatible, or material-revision change blocks the lease
advance and triggers the applicable suspension/requalification or a completely
new authorization proposal; it never keeps an old lease alive by explanation.

A compatible lease advance may retain the same immutable grant id/digest while
incrementing the ScopeHead generation. The grant's
`activated_at_scope_head_generation` remains historical provenance. Every new
admission binds the new current generation and lease. An already admitted but
unsent autonomous attempt is deliberately not migrated: if the current
ScopeHead generation, grant id/digest, or lease id/digest differs from its
`OperationAuthorityBinding`, dispatch fails closed, records
`authority_stale_no_send`, releases only its dynamic reservation after the
attempt is durably terminal, and retains all irreversible charges. A new
operation/idempotency key must be proposed under the new head. V1 performs no
"compatible" dispatch-time rebinding.

For a first grant or replacement, the matching target-candidate LeaseCandidate
and both independent attestations are validated under the same locks. The
transaction derives the final grant and lease identities, creates both
immutable objects plus events, CASes ProposalStateHead and ScopeHead, and
commits all or none. No intermediate `active` ScopeHead can reference a grant
without a valid lease. For renewal, the LeaseCandidate instead binds the exact
already-active grant id/digest and only the lease/ScopeHead generation changes.

Any mismatch or stale `valid_until` blocks autonomous new risk until Delegation
recomputes a compatible lease. Thus a newly published invalidation cannot be
ignored merely because an event consumer or cache is behind. Lease duration,
poll/processing SLO, and source freshness are mandatory signed policy values
derived from the deployment's measured latency; they have no default. Missing
or unmeasured values disable autonomous Live.

The rolling lease is always subordinate to the immutable grant deadlines:
Kernel admits only while
`current_lease.valid_from <= database_now < current_lease.valid_until` and
before `grant.admission_not_after`. Lease advancement cannot alter
`admission_not_after`, `dispatch_not_after`, `working_order_not_after`, or
`governance_expires_at`.

Delegation service unavailability does not invalidate an otherwise current
canonical grant before its lease/expiry, but it prevents new proposals,
renewals, expansions, or lease advancement. Source-head disagreement fails
closed immediately.

## 23. Broker-send fence and revocation races

Authority is checked at candidate admission, atomic entitlement commit, first
attempt claim/send, and every risk-preserving replacement. It is not checked
only once at proposal time.

Before the first Provider call, Kernel performs a `send_authorize` transaction
that re-locks the current PlatformMode, relevant EffectClass, and every
applicable KillSwitch head for both authority routes. For autonomous authority
it then re-locks every remaining authoritative source head, the ScopeHead, and
every applicable BudgetPoolHead in the global order above; exact-confirmation
dispatch instead follows the platform/effect/kill prefix and revalidates the
consumed immutable ticket/receipt binding. In the autonomous route, all authoritative source
heads must still exactly match the bound lease even when the ScopeHead has not
yet advanced. The current ScopeHead generation, grant id/digest, and
AuthorityHealthLease id/digest must exactly equal the admission binding, and
each PoolHead generation plus PoolRevision id/digest must exactly equal the
admission charge/binding. A lease renewal, source invalidation, pool tightening
or widening, or any other head advance never silently migrates an unsent
attempt. The bound lease must also satisfy
`valid_from <= database_now < valid_until` at dispatch. Any such
mismatch durably terminalizes the unsent attempt with zero Provider calls,
retains irreversible charges, and releases dynamic reservation only after the
no-send state is committed. In both routes Kernel validates the same
OperationAuthorityBinding, charges/reservation, and requires database time to
be before both
`dispatch_not_after` and `working_order_not_after`. It also validates current
hard safety state and the execution-attempt fencing token, then inserts one
`DispatchAuthorization` that binds the exact locked platform/effect/kill head
ids, digests, and generations and durably marks the attempt sent.

The durable sent mark is the revocation linearization boundary:

- if suspension/revocation/expiry wins first, an unsent attempt cannot mark
  sent and must not call the Provider;
- if `send_authorize` wins first, the already-authorized call may occur and is
  reconciled even if revocation follows; and
- a process death after the sent mark uses the bounded same-ref/unknown
  recovery contract below, never a fresh order identity.

The same boundary governs a platform-mode downgrade or kill-switch transition:
if the send fence commits first, that one already-authorized call may proceed
and must reconcile; if the downgrade/kill transition commits first, no later
new send may pass. The Platform Activator and Kernel therefore use the same
canonical head locks rather than racing on cached mode state.

Revocation cannot undo an already sent external effect. It blocks new uses,
blocks unsent attempts, and causes Kernel to cancel the unfilled remainder of
working new-risk orders where canonical state permits. Cancel is a safety
action; it needs no current grant, but it still obeys the global Provider
unknown latch. If latched, cancellation is deferred to reconciliation or the
separate emergency path and its reservation remains held. A revoked or expired
grant cannot create a fresh replacement place. Repricing after admission closes is allowed only if
the original template and authority binding remain valid through
`dispatch_not_after`, the working order remains inside
`working_order_not_after`, the replacement remains inside the original
reservation/price cap, and the exact admission ScopeHead generation, grant,
AuthorityHealthLease, authoritative source heads, and PoolHead generations
remain current. Otherwise Kernel
cancels without replacement.

Expiry or revocation does not make an already working broker order disappear.
Kernel requests cancellation, retains its reservation until terminal/fill
reconciliation, ingests any race fill as a real fact, and emits the resulting
governance event. It never labels a real fill nonexistent merely because the
grant later changed.

Recovery of an already sent `unknown` effect continues after grant or ticket
expiry because read-only pull, candidate matching, adoption, and reconciliation
resolve the previously authorized identity and may continue without a time
limit. A placement replay is stricter: the certified same-ref path may be
called only with the original DispatchAuthorization, exact request fingerprint
and `ref_id`, and only while database time is strictly before the original
`working_order_not_after`. It cannot increase or duplicate risk and may be
consumed at most once. Before the replay Provider call, a Kernel transaction
locks the live-account unknown gate and its exact execution attempt, requires
the gate's `unknown_attempt_id` to match, requires persisted `replay_count=0`,
revalidates the original dispatch/fingerprint/ref/deadline, atomically advances
`replay_count` to `1`, appends the replay-authorized event, and commits. Only
that winner may make the byte-identical Provider call. Concurrent or later
claims observe `1` and make zero calls; a crash after consumption does not
restore the entitlement. At or after the deadline Kernel performs no placement
replay; zero matched candidates remain `unknown`, every reservation and the
global latch stay held, and only the separately authenticated reconciliation/
emergency-resolution contract may resolve the state from fresh broker facts.
Expiry never creates permission for a fresh order identity.

## 24. Risk reduction proof

For `kernel_verified_reduction`, Kernel persists a `ReductionProof` containing
fields equivalent to:

```text
reduction_proof_id, operation id/digest, origin and authority mode
canonical account/ledger/position/order revision references
normalized instrument, side, maximum quantity, and position effect
before/after conservative risk and quantity facts
held close/open reservations and reconciliation state
classification reasons, database time, and Kernel policy revision
```

The proof is computed under the existing ledger/symbol locks. It is invalid if
the action may open, add, reverse, release ambiguity, or exceed the conservative
closable quantity. It cannot be attached after the fact to relabel a new-risk
operation.

Action names are not proof:

- a close is reducing only after Kernel derives the safe side and guaranteed
  closable quantity from Provider position, exposure lots, and held/unknown
  reservations;
- cancelling a Kernel-owned working **opening** order is reducing after exact
  account/order ownership and current state are proven;
- cancelling a **closing** order removes an exit and is not automatically
  reducing; the v1 Agent route is forbidden, and only a separately
  authenticated user/Kernel emergency management command may request it after
  a fresh canonical-state display;
- cancelling an external or ambiguously owned broker order is forbidden to the
  Agent route and belongs, if needed, to a separately authenticated emergency
  path;
- an opening-order reprice remains bound to the original grant, charge, risk,
  quantity, and price cap;
- a closing-order reprice may reuse its close reservation only when the
  normalized replacement remains reducing; and
- if `tighten_stop` later creates a broker effect, Kernel must prove that the
  stop moves only in the reducing direction and that side, quantity, product,
  and protected position are unchanged.

The current Kernel's broad name-based Class-A treatment of `cancel` and
`tighten_stop` is therefore an implementation prerequisite to replace before
Agent Delegation is connected to these actions. Delegation must not widen that
temporary behavior. A future autonomous or ordinary exact-confirmation path
for cancelling a closing order requires its own reviewed order-management
ticket/policy, close-reservation semantics, and acceptance suite; the new-risk
confirmation contract in this document cannot be reused for it.

Agent-originated reduction still requires its committed Artifact and
BehaviorEvent before effect. A later GRACE/Delegation outage does not trap it.
If that commit failed, only a separately authenticated user/Kernel emergency
origin may act under its own record. This independence does not bypass a
Provider unknown/reconciliation latch; while that Kernel state is active the
automatic path remains read/reconcile-only as specified above.

## 25. Performative feedback and anti-gaming

Every BehaviorEvent and economic outcome binds the generating:

```text
DelegationPolicyRevision
AuthorizationTemplateRevision and grant/confirmation authority mode
actual fixed envelope and BudgetPoolRevision set
permit/deny/expire/suspend result
Strategy/Agent/Role/Planner selections
product, instrument universe, regime, session, and Kernel policy revisions
```

Expansion changes the observed distribution. Therefore:

- each capability expansion opens a new qualification cohort;
- the same evidence manifest cannot climb two template edges;
- evidence after the previous expansion is required for the next;
- lower-cap evidence does not prove higher-cap capacity, cost, concentration,
  or tail behavior;
- policy-development data cannot also be the final promotion validation set;
- WAIT, PASS, denied, expired, superseded, and untraded cases remain in the
  qualified stream;
- no real-money randomized A/B is created merely to train GRACE; and
- profit, account growth, or a lucky outcome never mutates a current grant.

An Agent can voluntarily use less than its envelope. Delegation never requires
it to use the full amount and never enlarges the amount to recover a loss.

## 26. Persistence ownership and credentials

Records are immutable or append-only except the fenced
ActiveDelegationPolicyHead, AuthorizationProposalStateHead,
DelegationScopeHead, BudgetPoolHead, and
OperationConfirmationTicketStateHead. Each mutable projection uses
expected-generation CAS and has complete append-only transition history.

| Record | Write owner |
|---|---|
| Policy/Template/BudgetPool revisions | privileged Delegation policy path |
| HumanPolicyDecision/DelegationPolicyStateEvent/ActiveDelegationPolicyHead | authenticated policy-owner command and privileged Activation Path, as specified |
| BudgetPoolHead/pool transition event | privileged Delegation policy/activation path |
| AuthorizationProposal | deterministic Delegation Engine |
| AuthorizationProposalStateHead/StateEvent | deterministic Delegation Workflow Coordinator; privileged Activation Path may invoke only the constrained `approved/automatically_eligible_narrowing -> activated` CAS in the atomic grant-install transaction |
| ValidatorAttestation | independent Delegation Validator |
| GrantActivationCandidate | deterministic Delegation Engine |
| GrantActivationValidatorAttestation | independent Delegation Validator |
| HumanDelegationDecision | authenticated policy-owner command path |
| AutomaticNarrowingAuthorization | deterministic Delegation Engine from an active preauthorization rule, with independent Delegation Validator attestation required before use |
| AuthorityHealthLeaseCandidate | deterministic Delegation Engine |
| LeaseValidatorAttestation | independent Delegation Validator |
| DelegationGrant/GrantStateEvent/ScopeHead/AuthorityHealthLease | privileged Activation Path from validated inputs only |
| OperationConfirmationTicket/TicketStateHead/TicketStateEvent | Kernel |
| TicketDisplayReceipt/ConfirmationReceipt/conversation-receipt binding | dedicated User Authority Gateway (`user-authority-gateway`) |
| OperationAuthorityBinding/DelegationCharge/DispatchAuthorization/GateDecision/ReductionProof | Kernel |
| operation/trade_grant/reservation/attempt/order/fill | Kernel |
| ScoreSnapshot and GRACE invalidation heads | GRACE |

Agent, Coach, Memory, Strategy Lab, GRACE Engine, Delegation Engine, Validator,
Web, and ordinary runtime credentials cannot write an active grant,
ActiveDelegationPolicyHead, ScopeHead, BudgetPoolHead, AuthorityHealthLease, or
Kernel authority binding/charge/dispatch record. Only the narrowly scoped
Workflow Coordinator can CAS AuthorizationProposalStateHead, and that
projection grants no authority. Activation cannot write GRACE, Engine
candidates, arbitrary proposal state, or Validator attestations; it has only
execute permission on the fenced activated-transition routine inside the
grant-install transaction. Kernel cannot alter a policy/grant, ScopeHead,
BudgetPoolHead, or health lease; the User Authority Gateway cannot write the Kernel
TicketStateHead.
Database foreign keys do not substitute for service identity and row-level
write ownership.

Canonical digests use one schema-versioned deterministic serialization. Raw
maps with unstable key order are not authority identities. Every externally
delivered command uses authenticated service identity, immutable owner/id/
digest references, idempotency, causation/correlation, occurred/committed/
effective times, and the expected generation of each owner-specific mutable
head it is permitted to transition, where applicable. Exact-confirmation
commands name TicketStateHead generation; policy, proposal, Scope, and Pool
commands name only their respective authorized heads.

The authoritative event vocabulary includes at least:

```text
delegation_scope_transition
delegation_policy_head_transition
authority_health_lease_candidate | authority_health_lease_activated
delegation_gate_allowed | delegation_gate_denied
delegation_charge_created | delegation_budget_converted
delegation_budget_released | delegation_budget_retained_unknown
delegation_dispatch_authorized | delegation_dispatch_blocked
authority_stale_no_send
grant_conflict_detected
risk_reduction_classified
provider_unknown_latched | provider_unknown_resolved
provider_same_ref_replay_authorized
```

A positive Gate/dispatch event commits in the same transaction as the state it
describes. It records route, operation, grant/ticket ids and digests,
partition/generation, canonical requested facts, every applicable cap/usage,
reason codes, and source revisions. Serialization failure is transaction
failure; an error-shaped placeholder is not an authorization audit record.

## 27. Failure behavior

| Failure | Required result |
|---|---|
| GRACE Snapshot absent/stale/invalid/incompatible | no autonomous eligibility; exact-confirmation and reduction remain separate |
| Delegation Engine/Validator unavailable | no new proposal, renewal, or expansion |
| Activation unavailable | current valid grant may continue to lease/expiry; no state change |
| source-head or health-lease mismatch | autonomous new risk blocked |
| zero/multiple/corrupt grants | blocked and integrity event |
| policy/template/budget unit mismatch | blocked, no coercion |
| concurrent budget exhaustion | one transaction wins; others deny without attempts |
| grant expires before first send | no Provider call; safely retire unsent attempt |
| revocation after durable send | reconcile/cancel; never pretend effect did not occur |
| Provider `unknown` | global Kernel latch, retain all capacity, exact recovery only |
| breaker active | new risk blocked; separate resume cannot silently resume grant |
| monitor unavailable | monitor-dependent autonomous new risk blocked; cancel/reduce continues |
| Web unavailable | no new exact confirmation; current safe autonomous lease may continue |
| Agent unavailable | no new Agent proposal; Kernel reconcile/cancel/reduce continues |
| Delegation database/read path unavailable | autonomous Gate fails closed; Kernel safety remains |

## 28. Rollout

### Stage 0 — schemas and deterministic observe-only replay

Implement policy/template/proposal/attestation/grant/ticket/use schemas and
replay using fixtures only. Activation cannot affect Kernel. Prove deterministic
digests, compatibility, state transitions, ownership, and crash recovery.

### Stage 1 — Shadow gate

Use separate Shadow templates/grants and real budget accounting against the
paper ledger. Compare decisions with the existing Kernel classification. Zero
Provider mutation credentials exist in this stage.

### Stage 2 — exact-confirmation modernization

Replace ambiguous generic review approval with the exact ticket/receipt/effect
class contract while preserving current Kernel Class-C semantics and all
stricter gates. This still provides no autonomous Live authority.

### Stage 3 — Live delegation observe-only

Consume published `current_authority` GRACE snapshots and generate/validate
would-have-authorized proposals beside exact-confirmed Live decisions. Do not
activate Live grants. Measure false matches, source-head lag, budget behavior,
distribution shift, and operator comprehension.

### Stage 4 — autonomous Live canary

Requires all prior acceptance, signed GRACE Calibration Pack/model-risk
approval, signed compatible Delegation PolicyRevision, final cross-module
security review, explicit owner activation, current M11 provider/canary gates,
the already completed separately exact-confirmed M11 Kernel/provider canary,
and a narrowly bounded whole-share equity template. The owner approves the
grant activation, not the individual autonomous trade. The first autonomous
effect then uses `autonomous_grant` alone under active operator observation;
it is never stacked with an exact confirmation. Options remain disabled.

The architecture does not hard-code a ticker. The first signed production
template fixture must bind an exact instrument identity/universe revision. The
owner's currently stated rollout preference is SOFI; that note is planning
input only and is neither a grant nor authorization to place an order.

### Stage 5 — bounded expansion

Only after new post-canary independent evidence satisfies the next template's
ROBUST burden. Each product/action/universe expansion is separately reviewed.
No stage automatically enables a later one. This is the scoped autonomous
production state: ordinary qualified operations use `autonomous_grant` without
per-trade human confirmation. Humans retain absolute-limit, initial/material
Strategy/model/policy, new-product/effect, non-preauthorized widening, and
incident/unknown authority. Equal-or-narrower replacement and lease renewal may
proceed only through the exact active-policy preauthorization and independent
validation path frozen above.

## 29. Acceptance suite

### 29.1 GRACE and policy mapping

- High `Q_g` with Risk/Tail `ADVERSE` cannot become autonomous.
- Agent `ROBUST` with Strategy economics `INSUFFICIENT` cannot become
  autonomous.
- Strategy profitable with one required Role `UNCERTAIN` cannot become
  autonomous.
- Every plane `CREDIBLE` in a Challenger, diagnostic, or historical Snapshot
  cannot be consumed.
- Equity `ROBUST` evidence cannot unlock options, a new universe, session, or
  action.
- One evidence cohort cannot traverse two template edges.
- Profit/equity growth does not enlarge an active grant; loss never creates
  recovery sizing.
- Integrity breach followed by profit remains suspended until its specific
  requalification completes.
- Snapshot or Champion invalidation changes the source head and blocks the
  stale lease before a new autonomous admission.

### 29.2 Scope, revision, and grant lifecycle

- Changing Agent, prompt/model/config, RoleContract, Strategy, pipeline, Tool/
  Data contract, GRACE model/Profile/Pack, policy, Kernel, canary, or Provider
  material revision cannot silently inherit authority.
- Directional compatibility cannot be reversed or widened.
- Zero or two matching grants fail closed; an operation cannot pick the larger.
- Twenty concurrent promotions produce one ScopeHead generation.
- Grant installation with neither or both ActivationAuthority variants is
  rejected. A first grant or any widened dimension with
  `automatic_narrowing` is rejected.
- Automatic replacement fixtures prove every set, numeric, time, predicate,
  product, universe, action, session, and budget dimension is equal or narrower
  under the frozen comparator and exact active policy generation.
- Initial/replacement activation validates one exact GrantActivationCandidate,
  its independent attestation, the target-candidate LeaseCandidate and its
  attestation, then atomically creates grant plus lease and advances proposal/
  Scope heads; no committed active ScopeHead can contain only one of the pair.
- A future-dated policy/grant proposal has no authority when its desired time
  arrives. Activation-time expiry, source-head, attestation, or policy mismatch
  creates no grant/head movement; v1 has no timer-only grant activation.
- A compatible AuthorityHealthLease advance may retain the grant id but
  increments ScopeHead generation; new admissions bind the new generation,
  while an unsent attempt admitted under the old generation deterministically
  becomes `authority_stale_no_send`, makes zero Provider calls, retains its
  irreversible charge, and releases dynamic reservation only after durable
  terminalization.
- Lease advancement may extend the rolling health window only up to the
  immutable grant ceiling; admission at or beyond either
  `current_lease.valid_until` or `grant.admission_not_after` is denied.
- A lease cannot be installed before its candidate `activation_not_before`;
  its immutable `valid_from` equals activation database time, and admission or
  dispatch outside `valid_from <= database_now < valid_until` makes zero
  Provider calls.
- A lease advance racing `send_authorize` linearizes to either the old exact
  binding's durable sent mark or the stale-authority no-send result, never a
  dispatch rebound onto the new lease.
- Policy tightening racing a grant use linearizes to either the old valid use
  or the tightened denial, never a mixed envelope.
- Policy tightening never installs a replacement while holding policy/source
  heads. Racing an ordinary proposal-first replacement produces either
  replacement-then-suspension or tightening-then-stale-replacement denial,
  with no reverse-order deadlock or partial head transition.
- Policy loosening and rollback do not mutate/resurrect an old grant.
- Replacement/renewal cannot reset same-day budget pools.
- Missing, zero-length effective/admission, or inverted grant deadlines fail
  validation; `database_now` equal to any applicable `not_after` is expired,
  while adjacent later deadlines may be equal only as the schema ordering
  explicitly permits.
- BudgetPoolRevision activation aggregates old charges by stable pool key;
  racing a midday tighten with an admission yields either the pre-tighten valid
  use or post-tighten denial, never a fresh counter. Pool re-key/split/merge in
  the active window is rejected.

### 29.3 Budget and Kernel Gate

- Twenty concurrent opens with one remaining per-operation/count/risk capacity
  produce exactly one use, `trade_grant`, reservation, and attempt.
- A failed/cancelled/expired admission retains its irreversible daily charge;
  an `unknown` retains every charge and reservation.
- Partial fill and cancel/replace never create a capacity gap or double charge.
- Two grants sharing an account pool cannot oversubscribe it concurrently.
- Tightening or widening a PoolHead after admission but before
  `send_authorize` cannot rebind the attempt: the head transition either loses
  to the durable sent mark or produces a deterministic no-send terminal state,
  retained irreversible charge, and post-terminal dynamic-reservation release.
- Agent-declared risk, copied grant limits, float rounding, unit mismatch,
  overflow, missing cap, or stale cache cannot increase capacity.
- One hundred retries with the same idempotency key create one operation,
  authority binding, charge set, and effect; changing route, grant/ticket,
  digest, generation, or any material operation/revision field returns conflict
  and never binds the retry to a newer grant.
- One hundred retries of a committed `FAIL`/`UNKNOWN` admission return the one
  original negative GateDecision with zero bindings/charges/attempts, even
  after the blocker clears; an intentional new evaluation requires a new
  operation revision/idempotency key.
- Agent-originated Class B without a grant and Class C with a grant are both
  denied on the autonomous route.
- Kernel absolutes, canary, buying power, breaker, account binding, and Provider
  capability remain stricter than every grant.

### 29.4 Exact confirmation and human authority

- `好` with zero or multiple displayed tickets creates no receipt. A response
  without a prior immutable TicketDisplayReceipt cannot confirm a ticket.
- Kernel's immutable ticket never gains User Authority Gateway-owned display fields;
  duplicate display/confirmation delivery is idempotent and conflicting
  display/response receipts fail closed.
- Cross-account, cross-ticket, changed-digest, expired, superseded, and replayed
  receipts create no entitlement.
- Simultaneous confirm/reject/expiry produces one legal terminal decision.
  All commands CAS the Kernel TicketStateHead generation; losing receipt
  candidates remain inert and cannot later be consumed.
- Consume racing reject/expiry/supersession/TTL cleanup follows
  `pending operation -> TicketStateHead` and produces one winner without a
  reverse-order deadlock; a head-only receipt transition never later locks the
  operation.
- Re-gate failure moves the ticket to `gate_denied`, leaves the operation
  `pending_review`, and creates no `trade_grant`, reservation, attempt, or
  broker call; only terminal operation TTL expiry changes that operation state
  under the frozen Kernel policy.
- Race another operation between display and consume so daily count or total
  open risk newly fails/worsens: the old ticket becomes `gate_denied` and
  creates no entitlement; any new ticket explicitly supersedes it.
  Equal-or-less-severe displayed exceptions may proceed only under the frozen
  per-dimension comparator.
- Changed quantity, instrument, cap, order shape, account, or material revision
  requires a new ticket; a better working price inside the same cap is allowed.
- A trade confirmation cannot activate Delegation, resume a breaker, widen the
  canary, or adopt an unknown order, and vice versa.
- Generic or spoofed `ADMIN_TOKEN` fields cannot set reviewer/owner identity.
- Ticket deadline inversion, zero confirmation interval, equality at a
  `not_after` boundary, and dispatch at/after the working-order deadline all
  fail before Provider mutation.

### 29.5 Revocation, outage, and recovery

- Revocation before `send_authorize` prevents the Provider call; revocation
  after the durable sent mark preserves/reconciles the real effect.
- Grant/ticket expiry before first send blocks it; a sent unknown may still use
  read-only reconciliation but no fresh ref. Placement same-ref replay requires
  the original DispatchAuthorization/fingerprint and
  `database_now < working_order_not_after`; at/after the boundary even a
  zero-candidate pull causes no replay and retains `unknown` plus all capacity.
- Twenty concurrent same-ref replay claims under a zero-candidate unknown
  produce one atomic `replay_count: 0 -> 1`, one replay authorization and at
  most one Provider call; every loser and every later retry makes zero calls.
  Crash after fence consumption does not recreate the entitlement.
- Revoked/expired authority cancels working entry remainder and never creates a
  fresh replacement.
- Source consumer lag, Delegation outage, stale health lease, read-replica lag,
  or conflicting head fails autonomous new risk closed.
- A canonical GRACE/Strategy/Policy/Provider/monitor/Kernel source-head change
  after admission but before `send_authorize` is detected directly even when
  ScopeHead/lease activation lags; it either loses to the durable sent mark or
  produces zero Provider calls.
- A Provider `unknown` blocks revocation-generated cancel, ordinary close,
  cancel, and reprice mutations from every grant until canonical resolution;
  read-only pulls continue, reservations remain held, and only the separately
  authenticated post-pull emergency path is eligible.
- Breaker resume does not reactivate a suspended grant.
- Complete GRACE/Delegation/Web failure still permits canonical reconcile,
  cancel, close, and Kernel emergency reduction.
- A disguised close that can add/reverse risk fails ReductionProof and cannot
  use the reduction route.
- Cancelling an opening order, cancelling a closing order, cancelling an
  external order, repricing an open, repricing a close, and tightening a stop
  exercise distinct fact-based classifications; an action name alone never
  receives Class A.
- A closing-order cancel cannot use the new-risk exact-confirmation contract,
  cannot create a `trade_grant`, and is rejected on the v1 Agent route; only
  the separately authenticated emergency-management command can reach its
  future reviewed Kernel handler.

### 29.6 Audit and ownership

- Every decision replays from canonical manifests to the documented numeric
  tolerance and same reason precedence.
- Agent/Coach/Memory/GRACE/Engine/Validator/Web credentials cannot write
  ActiveDelegationPolicyHead, Grant, ScopeHead, BudgetPoolHead,
  AuthorityHealthLease, ConfirmationTicket/TicketStateHead,
  OperationAuthorityBinding, DelegationCharge, DispatchAuthorization, or
  broker effect.
- Engine cannot attest or activate itself; Validator cannot activate; Kernel
  cannot alter the grant it consumes.
- Activation cannot create or edit a GrantActivationCandidate or either
  Validator attestation; a candidate-owner/attester credential cannot invoke
  grant installation.
- Activation cannot create its own lease candidate or attestation. A lease
  advance with a material revision, categorical floor failure, stale source,
  window beyond policy/grant bounds, or mismatched independent attestation
  creates no lease and no ScopeHead generation.
- Policy body/state separation is enforced: lifecycle changes append an event
  and CAS ActiveDelegationPolicyHead without modifying the immutable revision
  digest.
- Proposal body/state separation is enforced: validation, activation-authority
  selection, activation, expiry, and supersession CAS AuthorizationProposalStateHead and
  append events without modifying the immutable proposal digest; Coordinator
  credentials cannot create any referenced authority artifact.
- Grant activation racing proposal expiry/supersession locks the exact approved
  ProposalStateHead generation first: either one atomic grant/lease/head install
  wins with the sole legal `-> activated` transition or no authority object is
  installed.
- Serialization/digest/audit-event failure on a positive authority path rolls
  back the operation and entitlement; a placeholder `marshal_error` record is
  never accepted as money-path audit evidence.
- Later attempt/order/fill/exposure/reconciliation or replacement-grant facts
  reverse-reference immutable bindings, charges, and grants; their original
  digests never change to acquire future references.
- Deleting denied, WAIT, PASS, expired, or untraded cases invalidates the
  qualification/performative-shift manifest.

## 30. Implementation boundary

This specification authorizes no code by itself. Detailed AP11 implementation
may begin only after the roadmap's AP0 release token and every preceding stage
gate; non-Live observe-only remains mandatory first. It does not authorize
autonomous Live. Before Stage 4, the project still requires:

- exact machine schemas and migrations reviewed against this contract;
- origin-aware authority identity/idempotency fields and fact-based
  close/cancel/tighten-stop reduction proofs in Kernel;
- signed representative Policy/Template/Budget fixtures with no missing
  fields;
- the independently reviewed GRACE Calibration Pack and active compatible
  Champion;
- database-role, authentication, secret, and service-topology threat review;
- fault-injection and multi-instance acceptance against PostgreSQL;
- exact Web confirmation/privileged-action UX acceptance; and
- explicit human activation of the production PolicyRevision and canary.

Until then, a GRACE score has no Live authority side effect and Kernel must
behave as if no autonomous Live DelegationGrant exists.
