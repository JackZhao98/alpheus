# Broker Coexistence and Pre-effect Facts

[Back to Plan Index](INDEX.md)

> Amendment: **v1.9**
>
> Status: **FROZEN ARCHITECTURE; amendment v1.9.1 permits B0 after the M11
> non-money gate and requires it before AP0; the real Canary remains required
> before AP13**
>
> Scope: coexistence with orders, fills, and positions changed outside Alpheus;
> final Provider-fact refresh before a broker mutation; ownership and audit of
> external actions. This does not add a second broker path or authorize Live.

## 1. Decision

The broker account is shared reality. A human or another authorized broker
client may place, cancel, or fill an order without first creating an Alpheus
operation. Alpheus must observe that fact, account for its current risk, and
allow a later AI or human request to manage it through the normal Kernel path.

Alpheus does **not** adopt an external order into a fictional system-owned
lifecycle. Ownership describes who originated an effect; it does not decide
whether Kernel may later act on the resulting broker object.

The resulting model is:

```text
external or Alpheus broker activity
              |
      typed Provider observation
              |
       Kernel reconciliation
      /                     \
system-owned fact       external fact
      \                     /
       aggregate account safety truth
              |
 new cancel/close intent -> normal Kernel gate -> Provider
```

The Kernel remains the only Alpheus component with broker mutation authority.
Agents never receive the production Provider credential and never directly
cancel, close, adopt, or rewrite broker facts.

## 2. Non-negotiable semantics

### 2.1 Origin and authority are separate

Every observed order, fill, and position change has an origin state:

- `alpheus`: exact account plus durable broker order id or exact Provider
  client reference matches one Alpheus attempt/order lineage;
- `external`: the broker fact is real but no exact Alpheus lineage matches;
- `ambiguous`: identity, quantity, product semantics, or reconciliation is not
  sufficient to classify it safely.

Classification is evidence-based and append-only. Symbol, side, quantity,
price, and time similarity alone never convert an external object into an
Alpheus order. A later exact identity may supersede an earlier classification
with an audit event; history is not rewritten.

External and ambiguous facts remain visible in the canonical account view and
consume applicable safety capacity. `ambiguous` always takes the stricter risk
treatment and cannot support new-risk authority.

### 2.2 Aggregate broker state is safety truth

The current Provider account, positions, working orders, fills, and buying
power are the external safety boundary. Internal reservations protect gaps
before those effects become visible and protect unresolved attempts; they are
not permission to ignore a newer adverse Provider fact.

For funds, Provider `buying_power` remains the sole hard external capacity.
Kernel subtracts only local reservations not already reflected by the Provider,
or conservatively double-counts when reflection cannot be proved. It must not
add back an assumed external or Alpheus hold.

For quantity and open risk:

- working opening orders count toward pending exposure and applicable risk
  limits whether their origin is Alpheus or external;
- working closing orders consume closable quantity whether their origin is
  Alpheus or external;
- aggregate Provider position bounds every close and prevents reversal;
- unresolved or semantically ambiguous working orders reserve the worst
  supported effect until reconciled or explicitly resolved.

### 2.3 External management creates a new lifecycle

An AI or human may request either:

- cancel of an exact external broker order; or
- close/reduce of an externally originated or mixed-origin position.

That request creates a new Alpheus operation, authority decision, attempt, and
audit trail referencing the observed broker object and observation revision.
It does not backfill an Alpheus open operation, grant, reservation, or Strategy
binding for the earlier external action.

Kernel derives the actual effect from fresh broker state. A cancel is not
automatically Class A: canceling an opening order usually reduces pending risk,
while canceling a protective/closing order may prolong or increase risk. If
Kernel cannot establish the semantics, it rejects or routes the exact action
to the applicable review path; it never labels ambiguity as reduction.

### 2.4 Manual changes to Alpheus positions

A human may partially or fully reduce, add to, or otherwise alter a position
whose earlier lots include Alpheus activity. Reconciliation must:

- preserve the actual broker fill and aggregate position as economic truth;
- update remaining exposure and reservations without inventing an Alpheus
  order or pretending the original lifecycle completed normally;
- record a typed external control episode linked to affected position/economic
  sets and quantities;
- mark attribution uncertain when broker facts cannot identify which economic
  lots the human intended to change; and
- invalidate stale Agent proposals or management plans whose assumed account,
  order, position, or quantity no longer matches.

Risk accounting may use a deterministic conservative allocation rule where a
lot split is required, but that accounting convention is never presented as
causal GRACE credit. Mixed-control evaluation belongs to the separate GRACE
mixed-control attribution specification in
[`../agent-plan/GRACE_MIXED_CONTROL.md`](../agent-plan/GRACE_MIXED_CONTROL.md).

## 3. Provider observation boundary

B0 extends the existing typed Provider; it does not add an MCP proxy service,
an Agent Tool, or a second source of broker truth. The Kernel calls typed Go
Provider methods. Robinhood MCP/session mechanics remain inside the Provider
adapter.

Each observation used for an effect records at minimum:

```text
bound account id
Provider/source revision
observed_at and completed_at
requested fact families and per-family success/failure
canonical order/position/account identities
normalized values and source object digest/reference
reconciliation generation
```

High-frequency identical reads need not create unlimited duplicate rows. B0
may retain an append-only change/event record plus a fenced current projection
and exact pre-effect manifests. Retention and compaction must preserve every
observation used by an authority, risk, reconciliation, attribution, or broker
effect decision.

Partial snapshots are explicit. A successful account read does not make a
failed positions or orders read fresh. Any required family that is missing,
stale, malformed, from the wrong account, or internally inconsistent fails the
dependent action closed.

## 4. Two refresh points

Provider reads are inexpensive deterministic I/O relative to an LLM call, but
a full account refresh is multiple upstream calls and is not assumed to be
free, instantaneous, or atomically isolated from human activity.

### 4.1 Decision snapshot

At the start of a trade/management decision, Kernel refreshes and publishes a
bounded canonical snapshot so Agents reason about current buying power,
positions, working orders, and applicable quotes. The Artifact/Proposal binds
the observation revision or digest it used.

No fixed one-second full-account poll is required. Refresh is event-driven:
decision start, broker/reconciliation event, active-order/position monitoring,
or explicit user refresh. Background polling may be added only from measured
Provider capacity and a deployment ceiling; it cannot weaken freshness at an
effect boundary.

### 4.2 Pre-effect barrier

Immediately before every broker mutation, Kernel performs the smallest
action-specific fresh read, then enters the existing database gate and
recomputes the effect against the fresh Provider facts, active database policy,
Halt/unknown state, grants, and reservations:

| Effect | Required fresh Provider facts |
|---|---|
| open/place | bound account and buying power; positions; working orders; executable quote/instrument facts |
| close/reduce | bound account; position quantity/direction; working orders consuming closable quantity; executable quote/instrument facts |
| cancel | bound account plus the exact target order and its current cancellable state |
| replace/reprice | exact old-order state plus every fact required for the replacement effect |

Inside the gate, Kernel verifies that the proposal's assumed facts are still
compatible, computes effective capacity from Provider facts plus local holds,
and commits the attempt/send fence before calling Provider. If incompatible,
the proposal becomes stale/rejected or returns for a new decision; Kernel does
not silently resize or reinterpret it.

The Provider can still reject because the broker owns final execution and
external state can change after the read. Such a definitive rejection is a
normal reconciled outcome. A missing/timeout result enters the existing
`unknown` latch and pull-based recovery; it never causes blind resend.

## 5. Policy and configuration ownership

B0 introduces no numeric trading value in code or YAML.

- Human/business freshness limits, allowed external-action classes, maximum
  stale-decision age, and any coexistence risk tolerance are typed immutable
  database policy under K1.
- Code owns structural rules: exact identity before `alpheus` classification,
  external facts cannot be erased or adopted by an Agent, ambiguity cannot
  authorize new risk, and every mutation crosses the pre-effect barrier.
- Deployment config owns Provider endpoint/session, credentials, request
  deadlines, connection limits, account binding, and maximum polling/capability
  ceilings.
- Provider responses are observed facts, never settings and never proposer
  input.

The effective action uses the strictest of Provider capacity, active database
policy, current Kernel risk/reservations, structural invariants, and the exact
grant/ticket envelope. A broker's willingness to accept an order never widens
Alpheus policy.

## 6. Minimal persistence and APIs

B0 requires typed records equivalent to, but not necessarily named exactly:

- `BrokerObservation` and `BrokerObservationItem` or an equivalent normalized
  change log plus current projection;
- `BrokerObjectOriginEvent` for evidence-backed origin/supersession;
- `ExternalControlEpisode` for manual/external changes affecting a tracked
  economic set;
- pre-effect fact binding on every mutation attempt;
- reconciliation generation and stale-proposal reason; and
- ownership/origin plus freshness in Kernel read models and Cockpit/API views.

The existing authenticated Kernel proposal path accepts an exact
`broker_order_id` for cancel and canonical position identity/quantity for
close. It never accepts caller-supplied origin, risk reduction, buying power,
position direction, fill, or ownership as authoritative. There is no generic
"adopt" write endpoint.

## 7. Implementation boundary and order

### Deferred controlled M11 canary

Amendment v1.9.1 supersedes the original ordering: B0 may land while the real
one-share M11 Canary is deferred. Because K1/B0 change the final safety path,
the later Canary runs against the then-current Kernel commit after their
non-money acceptance. It still requires a fresh bound account with no position,
working order, or unresolved Provider effect and remains an exact
human-confirmed Alpheus ticket through Kernel, not a direct MCP order.

Any unexpected external change during that window stops the Canary and enters
reconciliation. The clean-account restriction is a one-time certification
condition, not the production coexistence design.

### B0 — after the M11 non-money gate, before AP0

Implement B0 as a Kernel safety amendment after the landed M11 code and target
read-only evidence defined by amendment v1.9.1. K1 policy ownership and B0 are
separate modules: K1 owns revisable policy; B0 owns observed broker facts and
reconciliation. They may be fixture-developed independently, but B0 binds any
policy-bearing behavior to K1 and both must land before AP0 so Agent contracts
consume the real canonical shape.

Agent Live, including AP13-AP15, is forbidden until B0 acceptance and the
deferred M11 Canary both pass. Shadow may consume recorded external facts but
may not mutate them.

## 8. Acceptance

B0 is complete only when PostgreSQL, race, fault, and Provider-fixture probes
prove at least:

1. An unmatched external working buy appears as `external`, consumes pending
   risk, and cannot be relabeled `alpheus` by a similar local proposal.
2. An external working sell reduces closable quantity; concurrent internal and
   external close attempts cannot reverse the position.
3. A human partial/full sale of an Alpheus-origin position updates aggregate
   exposure, records an external control episode, invalidates stale management
   work, and creates no fictional internal fill.
4. A human add increases aggregate risk and can block the next Alpheus open
   even though no Alpheus grant created it.
5. AI cancel of an exact external opening order uses a new audited Kernel
   operation; cancel of a protective/closing order is not automatically Class
   A; garbage or stale target identity has zero Provider effects.
6. AI close of an external position is bounded by fresh aggregate position and
   all working close quantity, with normalized side and no reversal.
7. Open, close, cancel, and reprice each fail closed when any action-required
   Provider family is stale, partial, malformed, wrong-account, or conflicting.
8. Buying power is refreshed before place; concurrent local reservations and
   Provider-visible holds cannot authorize overspend. A definitive broker
   rejection reconciles cleanly and an unknown result preserves the existing
   latch/no-resend invariant.
9. A manual broker change between decision snapshot and pre-effect refresh
   makes the bound proposal stale and produces no silently resized or
   reinterpreted order.
10. Restart/replay reconstructs identical origin, external-control, risk, and
    stale-proposal state without duplicating economic PnL or broker objects.
11. Cockpit/API display system-owned, external, and ambiguous facts distinctly
    without exposing Provider credentials or allowing adoption.
12. No Agent, Web process, GRACE component, or Research Gateway can call the
    production Provider or write broker-origin classifications.

## 9. Explicit non-goals

- no broker account takeover or prohibition on human trading;
- no automatic adoption of external orders or inferred historical strategy;
- no attempt to infer human intent from price outcomes;
- no duplicate funds model beside Provider buying power;
- no always-on one-second full snapshot requirement;
- no new order transport, recovery service, or second mutation API;
- no causal GRACE credit assignment inside Kernel; and
- no options/multi-leg coexistence claim until those Provider mutations and
  position semantics have their own frozen evidence.
