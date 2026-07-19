# Cross-cutting Policy Ownership

[Back to Plan Index](INDEX.md)

> Amendment: **v1.8.1**
>
> Status: normative ownership model; implementation is split into the narrow
> M11 blocker and the post-M11/pre-AP0 migration below.

## Why this amendment exists

The frozen plan used `limits.yaml` as the Kernel constitution. That was a good
bootstrap boundary, but it is the wrong long-term source of mutable human risk
policy: values change by editing a deployment file and restarting, old work
does not bind the policy that authorized it, and multiple instances may load
different values.

The concrete M11 audit exposed the pre-K0 problem. `live_canary_revision`
already had a database ledger, but the production canary gate still read
`limits.yaml`; no production path recorded or activated the database revision.
The audit trail was therefore descriptive, not authoritative, and editing the
YAML plus restarting could bypass the intended clean-day widening proof. K0
closed this canary-specific hole; K1 remains responsible for migrating the
broader Kernel policy domains below.

This amendment moves human/business policy to typed immutable database
revisions without turning every constant into a remotely editable setting and
without creating a Config Service.

## One authority rule

For every effect, the effective permission is:

```text
code structural invariants and non-overridable absolutes
  AND deployment capability ceiling
  AND current database-policy absolutes
  AND current Kernel/Provider facts
  AND (
        automatic policy checks pass
        OR an exact scoped approval names only reviewable Class-C failures
      )
  AND the scoped grant/ticket envelope, where that effect requires one
```

"Strictest" is semantic, not always numeric `min`: allowed products and symbols
are intersections, deadlines choose the earlier time, risk/cash quantities
choose the lower capacity, and any closed capability wins. No lower layer may
open a capability closed by an upper layer. The second branch preserves the
frozen Class-C review contract: a human may approve an exact operation that
failed named reviewable checklist thresholds, but no approval or delegation can
override a structural invariant, non-overridable absolute, stale/ambiguous fact
or a different operation's envelope. Cancel, reconciliation and verified risk-
reduction do not require an opening-trade grant.

Terminology is important:

- a **structural invariant** is fixed in code and cannot be overridden;
- a **deployment ceiling** describes what this installation is physically
  allowed or able to do;
- a **human policy limit** is mandatory for Agents but deliberately revisable
  by an authorized human through an audited database revision; and
- a **Provider fact** is observed, not configured.

The typed policy schema fixes whether a field is non-overridable or Class-C
reviewable. A policy revision changes values, not approval-class semantics.

The historical YAML key `hard_limits` means "Agents cannot override these". It
does not mean the values should be compiled into code or remain static files.

## Where each kind of value belongs

| Owner | Examples | Change path | Failure behavior |
|---|---|---|---|
| Code | exact money/quantity units; supported order/effect shapes; no blind retry; same-ref replay at most once; schema/id length; absolute JSON/file/page/result/fan-out/recursion ceilings; Agent cannot call broker | reviewed code release | unsupported or invalid is rejected |
| Deploy/secret | DB URL; Provider endpoint/adapter; account binding; ports/TLS; API keys/OAuth/token and `0600` secret paths; socket/call/pool timeouts; logging; Live-enabled maximum ceiling | deployment control | unavailable capability stays closed |
| `KernelPolicyRevision/Head` | risk/checklist thresholds; whitelist; OI/spread; quote age; proposal TTL; breaker/tolerance values; execution/reprice/fee policy | immutable candidate + explicit activation | missing, invalid or stale Kernel head fails closed after K1; never YAML fallback |
| `LiveCanaryRevision/Head` | daily authorized-risk cap and clean-days widening proof only | immutable candidate + guarded activation | missing/invalid canary authority closes Live |
| `PlatformModeHead` | active platform mode/effect class below the deployment ceiling | Platform governance activation | missing/invalid head is disabled/no-effect; env alone never raises it |
| Scoped record | operation intent; grant/approval; reservation; approved quantity/risk/price/deadline; Kernel-generated `ref_id`, send window and bound revisions | transaction that authorizes or records work | cannot expand after creation |
| Provider/runtime fact | buying power/equity/position; quote/OI/tick/quantity increment; instrument/provider-order/fill identity and status; pull observations | authenticated read, then durable evidence | stale/ambiguous fact blocks effect |

API keys may temporarily be fixed per deployment, but "fixed" means injected by
environment or a permission-restricted secret file. A literal secret is never
committed to source, ordinary YAML, the policy tables, logs, prompts or Web
responses.

A database policy may lower a code resource ceiling for one Run or role. It
cannot raise that ceiling. For example, maximum files read per tool call stays
a code constant; a lower per-Run file/tool/token allowance may be DB policy.

## Minimal persistence model

Do not add a generic settings/KV table. Use one typed, schema-versioned Kernel
policy document and a small active head:

- `kernel_policy_revision`: immutable ID, schema version, canonical typed
  policy, digest, author/reason, creation/effective time and change class;
- `kernel_policy_head`: one active revision for the bound deployment/account,
  with a monotonically increasing generation and activation audit identity;
- the existing `live_canary_revision`, whose latest authoritative immutable row
  is the K0 head and whose row ID is its generation; and
- revision foreign keys/digests on the records that consume authorization.

`KernelPolicyRevision` does not duplicate canary values or platform mode. An
operation binds the Kernel policy plus the applicable canary/mode generation;
each domain has exactly one owner.

The canonical policy may be stored as validated JSONB to avoid a table per
field, but it is not free-form configuration: Go decoding rejects unknown
fields, schema/version validation is mandatory, database constraints cover
critical ranges, and the canonical digest is verified on read. Activation and
revision creation occur through the Kernel's Admin boundary or a small
governance CLI using existing authentication and audit machinery. There is no
new daemon.

For K1's general policy migration, `limits.yaml` becomes input to an explicit,
one-time empty-database bootstrap command and a human-readable export for
development. The K0 canary CLI deliberately does not read YAML. Normal
production startup does not record a revision from the file. Once the
corresponding domain head exists, startup never reads that domain's YAML values
as an alternative authority. Failure to load, decode or verify the required
head prevents the effects it governs. Shadow inherits the same Kernel policy
by default; a future experimental Shadow policy must be explicitly bound and
cannot silently alter Live.

## Binding and time semantics

- A new operation records the active Kernel policy revision/generation used to
  classify it. Its grant/reservations inherit that binding.
- A proposal stores an absolute database-time `expires_at` at creation. Restart
  or a later TTL change never recomputes that timestamp.
- Review rechecks current non-overridable absolutes, account state and Provider
  facts. Reviewable failures are evaluated under both bound and current policy;
  a scoped approval may name only those failures and binds the exact approved
  risk/quantity/price/deadline. It cannot become a general policy exception.
- A typed order/execution attempt records its execution-policy revision and the
  exact bounds it is allowed to consume. A later increase in `max_reprices`,
  risk, price tolerance or duration cannot grant extra work to an old order.
  A current tightening affects old work only when that field's typed transition
  rule says `tighten_existing`; it never rewrites old evidence or disables
  canonical reconciliation, cancel or separately verified risk-reduction.
- Policy activation and new entitlement creation serialize on stable database
  scope locks. Each transaction sees exactly one active generation.
- Claim ownership uses database-time `lease_expires_at`. Network/socket timeout
  values remain deployment configuration, but one instance's environment
  cannot redefine when another instance's claim becomes stealable.
- Read APIs expose effective revision ID, generation, digest and observed time
  with the resulting limits; they do not return an unexplained merge of YAML
  and database values.

Transition behavior is declared in code per typed field, not supplied by the
policy document:

| Field class | Transition for existing work |
|---|---|
| proposal TTL | `new_only`; persisted `expires_at` never changes |
| execution/reprice envelope | original authorization remains the ceiling; a current lower bound may stop future risk-increasing work |
| fresh account capacity, breakers, Halt, unknown latch and Provider facts | always rechecked before the effect |
| other policy values | `new_only` unless the reviewed schema explicitly declares `tighten_existing` |

## Values to migrate, delete or keep

### Migrate to Kernel policy

- per-trade and total-open-risk percentages;
- daily new-trade count, daily-loss breaker and loss-streak threshold;
- underlying universe/whitelist;
- minimum open interest and maximum relative spread;
- plan requirements;
- quote maximum age;
- risk-declaration and PnL-reconciliation tolerances;
- proposal TTL; and
- effective start-price, reprice interval/count and conservative fee
  assumptions that are actually enforced.

### Do not mechanically migrate dead YAML

- `account.base_currency` is not decoded today; currency is an account/Provider
  capability fact until multi-currency is designed.
- `execution_policy.order_type` is not read; M11 is structurally limit-only.
  Supported order shapes belong to code plus Provider capability evidence.
- `allow_naked_short_options` is not a functioning switch; `open + sell` is a
  code-level absolute reject in the single-leg model.
- `profile` is currently only an event label. Keep it as optional revision
  metadata or remove it; do not pretend it changes enforcement.

Every retained policy field must have an enforcement reader and an acceptance
test. Decorative fields are deleted rather than copied into the database.

### Keep outside policy DB

- one-megabyte request body and bounded MCP result ceilings;
- parser depth, cursor/page/batch/result counts, identifier lengths and Agent
  context/file absolute ceilings;
- exact fixed-point representation and rounding rules;
- Provider mutation no-auto-retry and same-ref replay count ceiling;
- supported product/order/position-effect shapes;
- DB/Broker/HTTP timeouts and connection-pool sizing; and
- secrets, endpoints, physical account binding and deployment Live ceiling.

## Narrow implementation sequence

### K0 — before the separately confirmed M11 canary

Do only the Live-critical repair:

1. record the initial canary revision through an explicit one-time bootstrap or
   Admin governance path and make the database revision/head the actual gate;
2. treat `cap increase OR clean_days decrease` as widening; mixed-direction
   changes are widening or must be split, while only cap-nonincreasing and
   clean-days-nondecreasing changes are tightening;
3. fail Live startup if the authoritative canary revision is missing/invalid;
4. expose the active canary revision/generation in state and refusal events;
5. prove editing `limits.yaml` after activation cannot change the running or
   restarted canary; and
6. retain landed commit `0913010` as the v1.7.1 replay/admission/Halt
   prerequisite before any real canary.

K0 uses one append-only authoritative row stream: `authority_version=1` marks
post-K0 rows, the latest such row is active, and its row ID is the generation.
Pre-K0 descriptive rows are never promoted. Authoritative rows cannot be
updated, deleted or promoted in place; activation and Live grant admission
serialize on the stable Live-ledger database lock. New Live grants bind the
exact canary revision by foreign key.

K0 deliberately supports only explicit initial bootstrap and tightening. The
implementation review proved that `day_open` means a day was observed, not
that the final broker PnL, fills and reconciliation were completed. Therefore
every widening (`cap increase OR clean_days decrease`, including mixed changes)
is classified correctly but denied fail-closed in K0. K1 owns a typed durable
completed-day attestation and may enable widening only after proving
`max(old_clean_days,new_clean_days)` consecutive eligible attestations with no
unknown effect or PnL divergence. A startup file, ordinary event or `day_open`
row can never serve as that proof.

Do not combine the first one-share canary with a migration of every existing
limit. Until K1, the remaining frozen YAML thresholds are a temporary,
build-pinned ceiling for that separately human-confirmed one-share ticket; the
database canary cap and the ticket are stricter. Production returns to
`read_only` after the canary sequence.

### K1 — after M11, before AP0 implementation

Add `KernelPolicyRevision/Head`, one-time bootstrap/import, revision bindings,
absolute proposal expiry and database lease expiry. Migrate only live fields
with proven readers, then stop loading policy values from YAML whenever a head
exists. Remove dead fields. This is an in-process Kernel/Store module and schema
migration, not a service. Add the typed Live-canary completed-day attestation
and guarded widening path described above; the attestation is evidence, not a
generic settings mechanism.

### K2 — with the owning Agent modules

Apply the same ownership rule without one universal config table:

- Agent prompt/model/Role/Skill/Tool activation stays in `AgentRevision` and
  capability heads;
- triggers/schedules stay in durable trigger registrations;
- lower Run/Task/tool/token/time/fan-out budgets freeze on Run creation;
- GRACE calibration and Delegation policy keep their own revisions; and
- `PlatformModeHead` alone owns active platform mode/effect classes below
  deployment ceilings.

Each domain owns its schema and activation rules. Shared code may implement
canonical hashing and optimistic head generations; shared ownership does not
justify a Config Service.

## Acceptance and audit probes

### K0 probes

- Canary bootstrap/activation is explicit and idempotent; concurrent candidate
  changes produce one authoritative latest generation or one head winner.
- Missing/corrupt canary authority closes Live without using YAML canary values.
- Changing YAML after canary activation changes no running/restarted canary
  behavior.
- Canary clean-days decrease is widening; cap/threshold mixed changes cannot
  bypass classification, and every widening is denied until K1's durable
  completed-day attestation lands.
- Pre-K0 rows remain non-authoritative after 0009→0010 migration; authoritative
  rows reject update/delete/in-place promotion, and concurrent activation plus
  grant admission binds wholly to the old or new generation, never torn fields.

### K0 implementation status

**LANDED** in `d24b8b9`. Migration 0010, the single deployment-only
`canary-policy` CLI, Live startup check, per-admission database read, grant
foreign key, state/limits projection, immutable-row trigger, legacy upgrade and
concurrency/race probes are complete. No HTTP mutation surface, head table,
Config Service, hot reload, automatic YAML import or production broker call was
added. See [`../k0_certification.md`](../k0_certification.md).

### K1 probes

- Empty-database bootstrap is explicit and idempotent; two concurrent
  activations produce one winning generation and an audited loser/conflict.
- Missing/corrupt/stale Kernel policy fails closed; it never uses migrated YAML
  values to continue.
- Changing the bootstrap file or another instance's environment after the
  Kernel head exists changes no effective migrated business/risk value.
- New operations bind one revision. Pending review, restart and reprice tests
  prove later widening adds no TTL, risk, price movement or reprice count.
- A reviewable Class-C failure can still receive an exact scoped approval;
  structural/non-overridable failures cannot. Field-specific tightening follows
  its code-declared transition rule without mutating old evidence.
- Different instance timeout environments cannot steal a database lease before
  its persisted expiry.
- Every policy field appears in an enforcement-reader inventory and a test;
  unknown/dead fields fail validation or are absent.
- Secret scans prove no policy snapshot, event, prompt, Web/API response or
  fixture contains credentials.

## Explicit non-goals

- no Config Service, feature-flag platform or generic settings UI;
- no hot-reload watcher; a head change is observed at a defined transaction or
  bounded refresh point;
- no field-by-field policy tables unless measured query needs justify them;
- no database toggle that enables unsupported products, blind retry, direct
  Agent broker access or weaker fixed-point rules; and
- no rewrite of the M11 canary into a broad policy migration.
