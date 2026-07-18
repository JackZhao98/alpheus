# Delegation Policy and Risk Authorization

> Status: **FROZEN ARCHITECTURE — GRACE/authorization separation, authority
> sources, scoped grants, fail-closed enforcement, and promotion/demotion
> direction are authoritative. The exact v1 policy, capability templates,
> grants, human-authority boundary, Kernel Gate, and rollout are frozen in
> `DELEGATION_POLICY.md`. Autonomous Live remains disabled.**

## Purpose

Delegation answers a question that GRACE intentionally does not answer:

> Given a validated credibility rating, human-owned policy, current deployment
> state, and Kernel constraints, what bounded authority may this Agent/Strategy
> combination exercise without a new human confirmation?

The answer is produced by deterministic policy code. A score is evidence, not
permission. An Agent cannot authorize itself, and profitable output cannot
directly become buying power.

Read `DELEGATION_POLICY.md` before implementing or changing any authority
route, policy mapping, grant, confirmation, budget, or Kernel Gate behavior.

## System boundary

Delegation consists of:

| Component | LLM | Responsibility |
|---|---:|---|
| Delegation Policy Engine | No | Map validated inputs and human-owned rules into a structured authorization proposal |
| Delegation Validator | No | Reproduce the mapping, validate revision compatibility and policy gates, and prepare an approval artifact |
| Privileged Activation Path | No | Create, revoke, expire, or atomically replace the single applicable active grant |
| Kernel Delegation Gate | No | Validate a grant and enforce it together with every stricter Kernel limit |

An optional human-facing Advisor may explain a proposal, but its prose is not a
policy input. No Delegation component calls a broker. Only Kernel may convert a
valid operation and effective authority into a broker effect.

## Authority sources remain distinct

The system does not collapse all authority into one score-derived number:

1. **Human-owned absolute policy:** account, product, loss, exposure, and
   deployment boundaries that Agent- or GRACE-reachable paths cannot raise.
2. **Autonomous delegation:** an expiring scoped grant created through this
   system from an approved policy and compatible GRACE evidence.
3. **Exact human confirmation:** authority bound to one immutable proposal or
   confirmation ticket under `USER_INPUT.md`, never inferred from ordinary
   conversation.
4. **Risk-reducing Kernel path:** close/cancel/tighten intent that Kernel proves
   is actually risk reducing; it is not blocked by a low GRACE score or absent
   autonomous grant.

Exact human confirmation is not a GRACE upgrade. Under the frozen v1 policy it
is an exclusive one-operation authority route that substitutes for, rather
than stacks with, an autonomous grant. It may accept only Kernel-defined
reviewable Class-C exceptions and cannot exceed the separately frozen Kernel
absolutes, mode/capability gates, canary, account capacity, or reconciliation
barriers.

## Frozen invariants

1. GRACE publishes `ScoreSnapshot`; Delegation publishes
   `AuthorizationProposal`; only the privileged path creates a
   `DelegationGrant`.
2. No LLM or Agent may calculate, approve, activate, expand, or backdate a
   grant.
3. A grant is scoped to exact account/ledger, deployment mode, grantee/Role,
   Strategy/decision-policy revision, products/actions, and time interval.
4. A score or grant cannot raise a human-owned absolute limit or relax a Kernel
   invariant.
5. Upgrades are slow, stepwise, evidence-gated, and initially human-approved.
   Policy-defined downgrade, suspension, expiry, or revocation may be fast and
   automatic.
6. Missing, stale, expired, incompatible, unreconciled, selectively observed,
   or invalid GRACE evidence can never increase authority.
7. A prompt, model, Strategy, RoleContract, Agent, Tool/data, or GRACE model
   revision cannot silently inherit a grant.
8. Shadow and Live grants are separate. Shadow credibility never silently
   becomes Live authority.
9. Broker ambiguity, unknown effects, stale execution facts, or an open
   reconciliation barrier cannot be released by Delegation.
10. Risk-reducing classification is derived by Kernel from canonical state,
    never trusted from an Agent or grant label.
11. Delegation changes are fully versioned, expiring, reversible, and
    attributable to their complete ScoreSnapshotBinding set and human policy
    inputs.
12. A profitable policy violation cannot support an upgrade and may trigger an
    immediate suspension or review under frozen policy.

## Input contract

The Delegation Policy Engine consumes fields equivalent to:

```text
request/proposal id, policy revision, and evaluation time
account, ledger, deployment mode, and user-owned policy revision
target AgentRevision, RoleContract, StrategyVersion, and decision pipeline
complete required GRACE ScoreSnapshotBinding set, Champion/Profile/Calibration
revisions, categorical plane floors, and source-head manifest
GRACE scopes, effective exposure, uncertainty, staleness, and limitation flags
current grant, canary, Strategy, product, and operational envelopes
Kernel-published breaker, reconciliation, Provider-health, and ledger state
required human review/promotion burden
```

It consumes only published structured facts from authoritative owners. Raw
Agent confidence, Coach prose, Memory, an Advisor explanation, or a manually
edited score is not an authorization input.

The Engine rejects mismatched account/ledger/Strategy/Agent scope, unsupported
model-policy compatibility, stale ScoreSnapshots, incomplete manifests,
unresolved unknown effects, and inputs generated after their claimed effective
time.

## Authorization proposal contract

The Engine produces a structured `AuthorizationProposal` with fields
equivalent to:

```text
authorization_proposal_id and policy revision
source ScoreSnapshotBinding set, GRACE model/Profile/Calibration, and complete
input-manifest references
target account, ledger, deployment mode, AgentRevision/Role, Strategy, and
decision-pipeline scope
current and proposed AuthorizationTemplateRevision/display stage
allowed operation classes, products, instruments/universe, and session scope
per-operation, aggregate open-risk, loss, concentration, frequency, and
duration envelopes
human-review and confirmation requirements
required Evidence/regime/Provider/monitoring health conditions
effective, expiry, cooldown, next-review, and requalification times
deterioration, suspension, and revocation conditions
machine-readable passed/failed gates and reasons
parent, superseded, rollback, dedupe, and audit references
```

An `AuthorizationProposal` has no effect by itself. It cannot be passed to a
broker or treated as an active grant.

## Active grant and authority envelope

After required validation and human approval, the privileged path may create
an immutable `DelegationGrant`. The grant pins the exact proposal, policy,
complete ScoreSnapshotBinding set, GRACE model/Profile/Calibration Pack/
Champion manifest, scope, envelope, effective time, expiry, and rollback
target. Replacement is atomic; concurrent promotion cannot create overlapping
ambiguous active grants for the same scope.

Every autonomous risk-creating Kernel proposal carries an authority envelope
equivalent to:

```text
grant id and immutable digest
account/ledger and target Strategy/Agent/Role scope
user request, Run, Task, Artifact, and OperationProposal references
allowed action/product and requested risk envelope
effective and expiry time
causation, correlation, idempotency, and dedupe identities
```

Kernel resolves the canonical grant by id and validates its digest and scope.
It does not trust an Agent-supplied copy of limits or a label claiming that the
operation is authorized.

## Capability and display-stage direction

The lifecycle distinguishes at least these human-readable stages:

1. research only;
2. Shadow only;
3. Live proposal requiring exact human review;
4. tightly bounded autonomous Live;
5. a higher but still human-capped trusted tier only if later evidence and
   policy justify it.

Machine policy does not treat these labels as one scalar ladder. It uses the
versioned capability-template lattice in `DELEGATION_POLICY.md`; a transition
exists only on an explicit reviewed graph edge. One transition cannot skip a
required edge. A Strategy, Agent, product, universe, or model revision does not
inherit capability without an explicit directional compatibility and
credibility-transfer decision. A display stage is not an account balance and
cannot be spent outside its exact scoped envelope.

## Effective authorization and Kernel enforcement

Kernel first requires exactly one legal authority route: an active autonomous
grant, one exact confirmation, a Kernel-verified reduction, or a separately
originated user/Kernel emergency reduction. Grant and exact confirmation are
never stacked. It then enforces the strictest applicable dimension:

```text
effective authorization = dimension-wise intersection of
  human-owned absolute policy,
  Kernel hard risk and reservation rules,
  active Live canary,
  exactly one applicable grant OR exact confirmation where new risk is allowed,
  Strategy-specific envelope,
  remaining ledger capacity
```

Set, time, revision, health, and predicate dimensions use the exact
intersection algebra in `DELEGATION_POLICY.md`; they are not reduced to one
numeric `minimum`.

Kernel Gate fails closed when a required grant is absent, stale, expired,
revoked, corrupt, unsupported, or mismatched to account, ledger, deployment,
Agent/Role, Strategy, product, action, or GRACE/policy revision. It also rejects
an envelope that cannot be resolved to canonical state.

Kernel independently recomputes quantity, exposure, risk, position effect,
close normalization, fresh canonical Provider account/buying-power, position,
order and reservation facts, and breaker state. There is no independent
`settled_cash` authority field. A valid grant is necessary for autonomous new
risk but never sufficient for execution.

## Upgrade, downgrade, and decay

- Upgrade requires a complete compatible GRACE ScoreSnapshotBinding
  constellation with sufficient credible exposure, acceptable downside/tail
  evidence, calibration, operational integrity, matching regime/coverage, and
  every human-owned promotion gate.
- An increase moves across at most one declared capability-template edge and
  remains inside a human-owned change budget and cooldown.
- Downgrade/suspension may follow deterioration, drawdown, rule violation,
  unresolved unknown, Provider divergence, model invalidation, missing required
  monitoring, or incompatible regime.
- Authority expires, decays, or returns to human review when evidence is stale
  or validated coverage no longer applies.
- Losses never trigger martingale sizing or a larger envelope intended to win
  back credibility.
- A downgrade cannot strand fact-proven risk reduction: Kernel-validated
  close/cancel/tighten paths remain available only when the exact action is
  reducing under canonical state and the Provider unknown/reconciliation latch
  permits that mutation. A closing-order cancel is not reduction by name.

The Delegation Engine never edits a grant in place. Every change creates a new
proposal and, if approved or automatically permitted by frozen downgrade
policy, an atomic revision transition.

## Human confirmation boundary

The Web or conversation interface does not call Delegation or Kernel with
free-form approval. A human confirmation is valid only when bound to an exact,
current, immutable ticket that names the operation, account, Strategy,
instrument, side, quantity/risk, price constraints, maximum loss, expiry, and
relevant revisions.

Words such as `好`, `确认`, or `买吧` do not authorize a changed, expired,
ambiguous, or unbound proposal. Confirmation of one proposal cannot activate a
general autonomous grant. Any future emergency/break-glass process requires a
separate credential, short expiry, explicit reason, and immutable audit trail;
it is not an Agent feature.

## Failure behavior

- GRACE unavailable or ScoreSnapshot stale: no autonomous authority increase;
  preserve/reduce only under frozen policy.
- Delegation Engine or Validator unavailable: no new or expanded grant.
- Activation ambiguity or concurrent update: no new active revision; retain
  the last unambiguous valid state or fail closed.
- Grant missing/expired/mismatched: autonomous new risk is rejected. Exact
  human review is possible only through an explicit new proposal revision and
  Kernel ticket; the denied route is never silently broadened or rerouted.
- Kernel unavailable: no broker effect regardless of grant state.
- Agent Runtime unavailable: existing Kernel safety and human-authorized risk
  reduction remain independent.
- Position monitoring requirement unavailable: block monitor-dependent new
  autonomous risk and surface the condition.
- GRACE model rollback/incompatibility: suspend affected grants. A conservative
  replacement requires a newly evaluated current proposal; rollback never
  revives an expired, revoked, stale, or now-incompatible prior grant.

## Persistence and audit

Schemas must preserve immutable or append-only records equivalent to:

- human-owned delegation policy and compatibility revisions;
- authorization input manifests and proposals;
- Validator and human approval/rejection artifacts;
- active, expired, revoked, suspended, and superseded grants;
- fenced ScopeHead generations with append-only transition history, authority
  bindings, budget charges, dispatch authorizations, authority envelopes, and
  Kernel Gate decisions;
- upgrade, downgrade, decay, review, rollback, and failure events;
- exact GRACE ScoreSnapshotBinding set and Kernel policy references.

Agent and GRACE database roles cannot write active grants. Only the privileged
activation role may transition them, and Kernel receives read/validation access
without becoming the owner of Agent prose or GRACE estimation data.

## Acceptance boundary before autonomous Live use

Delegation cannot authorize autonomous Live risk until acceptance proves:

- GRACE score, authorization proposal, active grant, exact human confirmation,
  and Kernel execution are distinct records and authorities;
- no Agent/LLM path can create, widen, activate, backdate, or suppress a grant;
- every grant is scoped, expiring, attributable, and atomically replaceable;
- a mismatched/stale/invalid ScoreSnapshot cannot support an increase;
- one lucky outcome, Shadow-only evidence, or profitable violation cannot
  cause promotion;
- Strategy/Agent/Role/model changes cannot silently inherit authority;
- upgrade is one-template-edge, cooldown-bound, and human-approved as specified;
- qualifying deterioration/violation/unknown causes deterministic suspension
  or downgrade;
- concurrency cannot produce multiple active grants or oversubscribe an
  envelope;
- exact confirmation cannot be replayed, widened, or applied to another
  proposal;
- risk-reducing operations remain possible without converting them into new
  risk;
- Kernel independently applies the strictest limit and rejects unresolved
  authority state;
- rollback re-evaluates current facts and, only when still compatible, creates
  a new proposal/grant with the preceding conservative policy/template
  semantics; it never restores the old grant instance;
- observe-only, Shadow, and tightly capped Live canary stages pass independent
  adversarial review.

## Detailed specification and remaining boundary

`DELEGATION_POLICY.md` freezes the exact policy schema, compatibility and
capability-template lattice, scope/partition model, GRACE mapping, human
authority classes, grant/ticket/use state machines, concurrency, Kernel Gate,
rollout, and adversarial acceptance probes.

Implementation may proceed only in that order and initially through non-Live,
Shadow, exact-confirmation, and observe-only stages. Autonomous Live remains
disabled until the signed GRACE Calibration Pack/model-risk review, exact
machine schemas and security review, PostgreSQL fault suite, final cross-module
audit, and explicit production PolicyRevision activation pass. Until then, no
GRACE score grants Live authority and Kernel behaves as if no autonomous Live
DelegationGrant exists.
