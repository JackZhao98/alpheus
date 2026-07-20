# Agent Platform Implementation Status

> This file tracks implementation against the frozen architecture. It records
> progress; it does not change a stage gate or authorize an effect.

## Current boundary

- The frozen Lean v1 architecture remains authoritative.
- Non-money AP0 is implemented and accepted with effect ceiling `none` at
  corrected source `6c276e9`, evidence seal `628b717`, and release digest
  `0614bf77...932d1da2`.
- AP1-1's durable Runtime contract freeze is complete at `df73161`, with its
  persistence-blocking contract correction at `006e623`, exact
  OwnerPolicy/OutputContract canonical sources at `fef99de`, and reclaimed
  Attempt lease chronology correction at `d23215c`; failed-Attempt retry budget
  classification was made explicit at `ce0da6e`. AP1 is not accepted and no
  Runtime behavior or effect is enabled.
- AP1-2's immutable definitions landed at `bce88cc`, its default-deny durable
  Runtime state landed at `7671762`, and its first transactional lease slice
  landed at `95a1af2`. Durable model-call dispatch, unknown containment,
  reconciliation, budget settlement, and expired-dispatch same-Attempt recovery
  landed at `4f3a082`. Atomic Attempt completion/failure, immutable non-effect
  Artifact retention, disabled publication intent creation, retry/dead-letter
  settlement, and final-fence race containment landed at `9ea1c04`. Its
  database surface now lets a correctly provisioned
  Worker claim, start, and heartbeat durable non-money Tasks and transact exact
  model-call and terminalization facts. No deployed Worker uses it yet; there
  is still no model adapter or network call, and child-task, cancellation, and
  recovery commands remain absent. A bounded, local-only OutputContract
  validator and its future receipt command contracts landed at `f70388d`.
  They are not yet wired into a deployed Control/Worker loop; database receipt
  persistence is intentionally deferred until after the MVP loop. The system
  cannot call a model or produce an external effect.
- The Kernel, Provider, Runtime behavior, operation path, GRACE, Delegation,
  Live mode, and UI were not changed by AP0-1 through AP0-6.
- `./scripts/certify-agent.sh ap0` is the permanent historical non-money
  acceptance verifier. Since `714bee2` it reconstructs the protected source
  and seal from Git, while current-head AP1 gates remain a separate stage.

## AP0 work packets

| Packet | Status | Scope |
|---|---|---|
| AP0-1 common identity and release-verification foundation | Code complete at `a7fafa2`; certification correction at `775f176` | Versioned canonical JSON/digests, common identity and authority-bearing Go contracts, fail-closed RunOrigin/recovery lineage, EffectiveRunAuthority freshness, idempotency replay comparison, digest-bound release manifest verifier and CLI, golden/race tests, certification entrypoint scaffold |
| AP0-2 common Schema Freeze Pack | Complete at `3175afd` | Machine-readable manifest, JSON Schema, canonicalization profile, valid/invalid goldens and digest vectors, compatibility declaration, contract validation command, and automated Go/Schema field and enum drift detection |
| AP0-3 service security and durable delivery scaffold | Complete at `83bce82`; identity/provenance hardening at `6c276e9` | Credential-isolated service profiles, bounded owner-only secret-file loading, per-owner database roles, durable outbox/inbox contracts, dynamic delivery policy, poison quarantine and explicit replay, role/concurrency/replay/secret-leak probes; no shared writer credential |
| AP0-4 BlobRef and bounded local BlobStore | Complete at `bd9bb52`; identity/ownership hardening at `6c276e9` | Local package plus owner-only content-addressed volume, database-issued staging bounds, persisted pre-materialization facts, verified reads, exact principal/reference/ACL/retention checks, audited reference/ACL/policy transitions, bounded staged/content GC, and mismatch/unauthorized/missing/concurrency probes |
| AP0-5 platform/effect governance registry | Complete at `f8f2e74`; authority/locking hardening at `6c276e9` | Frozen governance Schema Pack, immutable typed mode/effect/kill-switch revisions, fenced heads and append-only events, single-use bounded ActivationReceipts, separate owner/Activator/emergency-halt roles, stable-subject CAS, exact current-head projection, deterministic fail-closed Go resolver, and role/stale/malformed/concurrency probes |
| AP0-6 integration and AP0 acceptance | Complete; corrected source `6c276e9`; evidence seal `628b717`; release digest `0614bf77...932d1da2` | Full Kernel/Agent migration compatibility, complete common and AP0 threat probes, cross-language canonical digest validation, machine-readable certification evidence, bound release files, and exact owner-approved digest verification |

AP0 is complete only when all six packets pass the frozen AP0 acceptance
criteria. These packets are implementation-sized units, not new architecture
milestones and not independent authorization gates.

## AP1 work packets

| Packet | Status | Scope |
|---|---|---|
| AP1-1 durable Runtime contract freeze | Complete at `df73161`; corrected at `006e623`; canonical sources at `fef99de`; lease chronology corrected at `d23215c`; retry classification corrected at `ce0da6e` | Strict Go contracts and semantic validation for triggers, runs, tasks, dependencies, reconstructable BlobRef-backed sessions and checkpoints, fenced and reclaimable attempts and leases, replay-safe model dispatch/result/unknown commands, explicit failed-Attempt retry budget classification, exact OwnerPolicy and JSON OutputContract revisions, canonical non-money artifacts, disabled publication intents, budgets, cancellation, recovery and transition events; JSON Schema, exact authority-ref and state-machine parity, permissions/retention boundaries, valid/invalid goldens and digest vectors. Operational limits remain database policy; effect ceiling is `none`. |
| AP1-2 PostgreSQL durable state and command transactions | In progress; immutable definitions at `bce88cc`; durable Runtime state at `7671762`; claim/start/heartbeat commands at `95a1af2`; model-call transactions at `4f3a082`; Attempt terminalization at `9ea1c04`; bounded output validator contracts at `f70388d`; AP0 historical certification isolated at `714bee2` | OwnerPolicy, RuntimePolicy, TriggerRegistration, JSON OutputContract, Run/Task/Session/Attempt/Turn, model-call, Artifact, Checkpoint, budget, cancellation, recovery, idempotency-record, and transition-event state are durable, exact-lineage-bound, default-deny, and effect `none`. Role-derived claim/start/heartbeat, model-call dispatch/unknown/resolve, and Attempt commit/fail transactions enforce strict raw JSON, exact idempotency and Blob lineage, current admission at dispatch, root-to-leaf budget reservation/settlement, final database-time fences, persistent Artifact Blob bindings, exact slot release, retry/dead-letter accounting, Worker-only grants, and canonical transition events. Database validation-receipt persistence, child-task, cancellation, and recovery commands are deferred while the MVP Control/Worker loop is built. |
| AP1-3 Control Plane and bounded Worker execution | Not started | Integrate the existing `agent-runtime` deployable with AP1 persistence; no second service, operation emission, Provider/broker access or Live effect. |
| AP1-4 crash/concurrency acceptance and stage seal | Not started | Race, crash-window, duplicate-delivery, stale-lease, budget, cancellation, recovery and non-money acceptance evidence. |

AP1-1 freezes data shape and fail-closed validation only. It does not create
tables, start a scheduler, claim work, call a model, publish a behavior event,
or authorize any Kernel-facing effect.

## AP0-1 contract profile

The code lives under `agent-platform/` as an independent Go module. Its
canonicalization profile is `alpheus-c14n-v1`:

- one strict UTF-8 JSON value;
- null, booleans, strings, arrays, objects, and base-10 integers only;
- duplicate object keys, floats, exponents, negative zero, invalid UTF-8, and
  trailing values are rejected;
- object keys are UTF-8 lexically sorted and strings use minimal JSON escapes;
- SHA-256 input binds profile, explicit domain, and canonical body; and
- checked-in input, canonical output, and digest goldens pin behavior.

The common contracts reject unknown revisions, owners, enums, audiences,
effects, malformed digests, temporal inversion, cross-owner event identity,
fabricated conversations on non-user origins, and recovery without its original
causation, idempotency, authority, and external-effect references. Missing
platform mode resolves to `disabled`; malformed mode is rejected.

The release verifier rejects unknown/duplicate fields, oversized or trailing
JSON, unsorted evidence, failed checks in an authorized release, unknown effect
classes, stage mismatch, and digest mismatch. AP0 release manifests have an
effect ceiling of `none`. The CLI requires both `--expect-stage` and
`--expect-digest`; the trusted expected digest must come from the stage gate or
activation record, never from the same untrusted manifest being checked.

## Verification

The corrected AP0 implementation and protected aggregate stage command pass the
code, contract, role, concurrency, migration, and release-verification probes
below:

```text
gofmt
go vet ./...
go test -race ./...
JSON Schema 2020-12 meta-validation and valid/invalid golden validation
independent Python validation of all 21 canonical golden digests
secret-leak probe
disposable PostgreSQL role/delivery probe
disposable PostgreSQL Blob role/ACL/retention/GC probe
disposable PostgreSQL governance role/receipt/CAS probe
full Kernel plus Agent migration compatibility and transactional rollback probe
Docker Compose configuration validation
static non-money boundary probe
exact release-manifest document and evidence verification
```

The PostgreSQL probe exercises exact retry and conflicting identity behavior,
stale lease rejection, inbox deduplication, quarantine/replay, dynamic-policy
compare-and-swap and audit history, capacity limits, role isolation, and 20
events claimed concurrently by eight dispatchers with no duplicate lease.

The focused AP1 Runtime-state probe loads exactly Agent migrations 0001-0005
in PostgreSQL 16 and verifies 21 new state tables, exact object/routine
inventory, initial and terminal state guards, Task slot history, reclaimed
lease chronology, unresolved model-call containment, forward Checkpoint CAS,
exact Result/Artifact/Recovery lineage, fail-closed JSON and nullable tuples,
deferred cross-record invariants, zero non-owner state authority, and effect
ceiling `none`.

The first AP1 command probe upgrades that existing state through migration
0006 and verifies strict raw JSON before PostgreSQL normalization, exact
actor-scoped idempotency, current authority heads, root-to-leaf ancestry budget
charging, database-time lease fences and reclaim, worker-only execution grants,
and zero direct table authority. A 20-way in-process claim barrier commits one
claim with one nonterminal Attempt and no processing command left behind. Its
RuntimeEvent is independently revalidated by the Go canonicalization CLI; the
slice retains effect ceiling `none`.

The AP1 model-call probe upgrades the same state through migration 0007 and
verifies atomic dispatch-before-network persistence, exact committed BlobRef
lineage, worst-case budget reservation and conservative failure settlement,
provider-request uniqueness, unknown containment, and expired-dispatch recovery
on the same Attempt without a blind resend. Separate 20-way barriers admit
exactly one dispatch and one outcome winner. A held global identity row is
observed blocking dispatch past its command deadline; the command then durably
denies with no Turn, Manifest, or budget delta. Maximum legal identifiers and
`BIGINT` duration input fail or proceed without representation overflow.
Manifest, Result, and RuntimeEvent digests are independently revalidated by the
Go canonicalization CLI. The probe invokes no model, Provider, Kernel,
operation, or broker path and retains effect ceiling `none`.

The AP1 Attempt-terminalization probe upgrades through migration 0008 and
verifies exact Result/OutputContract lineage, immutable Artifact and persistent
Blob bindings, disabled publication, success/failure/retry/dead-letter state,
single active-slot release, strict Worker-only ACLs, and cross-language
Artifact/publication digests. A 20-way mixed commit/fail barrier produces one
winner and 19 durable denials. Held budget and Result locks force requests past
their lease or command deadline and prove the final fence leaves no state or
event delta. Conflicting Blob metadata, unresolved Turns, missing Blobs, stale
fences, and malformed retry semantics fail closed. This slice remains effect
`none`; it does not validate custom OutputContract bytes or enable downstream
Artifact consumption.

The governance probe exercises all three typed head families, immutable
revision/receipt/event records, owner-versus-Activator role isolation,
least-privilege emergency halt, stale receipt rejection, single-use receipt
consumption, direct private-lock-table denial, expiry while waiting on a subject
lock, and 20 concurrent activations against both an existing head and an absent
bootstrap head. Each subject produces exactly one generation, one event, and
one receipt consumption.

The stage command runs every mandatory probe, retains JSON/JUnit artifacts, and
fails on a dirty worktree, skipped or unavailable infrastructure, leaked secret,
changed bound file, missing evidence, wrong source commit, or digest mismatch.

## AP0-2 Schema Freeze Pack

The normative pack lives at `contracts/common/v1/`. Its `.yaml` artifacts use
the JSON-compatible YAML 1.2 subset so the standard library can parse them
without adding an early supply-chain dependency. The pack includes:

- JSON Schema 2020-12 definitions for the common contracts and release
  manifest;
- empty but explicit OpenAPI, AsyncAPI, state-machine, and database-ownership
  boundaries instead of invented handlers, channels, states, or tables;
- immutable retention, replay, compatibility, privacy, and migration rules;
- valid and invalid user/schedule/authority/command fixtures;
- domain-separated golden digests for every valid fixture; and
- a `validate-contract` CLI that performs duplicate-key and lexical JSON
  checks, strict unknown-field decoding, semantic validation, canonicalization,
  and optional exact-digest comparison.

Tests compare every top-level Go JSON field, required/optional field, supported
contract type, and common enum with the Schema Pack. Unlisted or missing golden
files fail the pack test. AP0 certification retains separate Go semantic and
independent JSON Schema/cross-language digest evidence.

## AP0-3 security and durable delivery scaffold

The security freeze pack binds each service profile to one exact audience and
database role. Profile sets reject shared principals, roles, or secret paths.
Configuration may reference only absolute secret files declared for that
profile; loading rejects symlinks, non-regular files, group/world permissions,
oversized values, NUL bytes, and multiline values. Secret values are not valid
configuration fields.

The delivery freeze pack and migration establish at-least-once delivery with:

- owner state and outbox writes sharing an owner transaction boundary;
- consumer effects and inbox receipts sharing a consumer transaction boundary;
- stable event, causation, correlation, digest, and owner-sequence identity;
- database-time leases with stale-token rejection and concurrent
  `SKIP LOCKED` claims;
- bounded poison quarantine and explicit, generation-checked replay; and
- delivery attempt, quarantine, claim-batch, and lease limits held in an
  audited database policy row and changed by compare-and-swap, not hardcoded
  deployment configuration.

Separate NOLOGIN database roles expose only narrow `SECURITY DEFINER`
functions. Workers, research, GRACE, Delegation, validation, activation, Web,
and diagnostics do not receive a catch-all writer credential. Each production
process uses a distinct, non-elevated LOGIN whose name exactly matches its
configured `principal_id` and which belongs directly to exactly one application
group. One internal definer derives principal, profile, group, and owner solely
from `session_user`; zero or multiple application-group memberships, any
membership with `ADMIN OPTION`, and any migrator membership fail closed. Agent
LOGINs cannot call PostgreSQL advisory-lock functions, so they cannot block the
Kernel's private coordination keys. There is no caller-writable identity
mapping. This packet creates persistence and contracts only: it does not start
a dispatcher or alter Agent Runtime, Kernel, Provider, Live, operation, or UI
behavior.

The advisory-function revocation assumes the dedicated Alpheus database used
by the current deployment. A future least-privilege Kernel LOGIN must receive
only the advisory-function grants its transaction gate actually needs; sharing
this database with unrelated non-superuser applications requires an explicit
grant review.

## AP0-4 BlobRef and bounded local BlobStore

The v1 byte plane is deliberately local: one Go package and one private
content-addressed volume backed by PostgreSQL metadata. It is not an object
storage daemon, distributed filesystem, cluster, or new message service.

The upload protocol prevents partially known bytes from becoming a BlobRef:

1. current database policy issues an exact principal/media/size/expiry staging
   grant;
2. the local adapter streams into an owner-only staging file while enforcing
   the bound and computing SHA-256;
3. computed digest and size are persisted before physical materialization, so
   a crash cannot create an untracked content orphan;
4. bytes are atomically linked into the content-addressed path and verified
   again; and
5. committed metadata becomes referenceable only after exact stage, origin,
   digest, size, and media validation.

`BlobRef` identifies immutable verified bytes but grants no access. Every read
requires a fresh metadata authorization binding the authenticated principal,
exact owning `RecordRef`, binding ID, active ACL, unexpired retention, committed
Blob state, digest, and size. The same opened descriptor is hashed before its
cursor is rewound and exposed, preventing path replacement between verification
and consumption.

Reference, ACL, policy, quarantine, and deletion changes are append-audited.
Operational byte, stage, retention, and GC limits live in the audited database
policy row; the code contains only the fixed absolute one-file safety ceiling.
GC removes only exact database-leased stage/content candidates. Active retained
references block content GC, and stale deletion tokens cannot complete.

The disposable probe verifies direct-table denial, owner-specific reference
functions, digest knowledge without authority, private/explicit ACL behavior,
retention protection, release and orphan deletion, stage cleanup, policy CAS,
and 20 simultaneous metadata commits of one shared content digest. Local race
tests additionally cover concurrent physical deduplication, oversize and digest
mismatch, unsafe roots, authorization callbacks, corruption, missing bytes, and
verified deletion.

## AP0-5 platform and effect governance

The governance v1 Schema Pack freezes five global platform-ceiling values:
`disabled`, `read_only`, `shadow`, `live_confirmed`, and `live_autonomous`.
A canary remains a scoped rollout and is deliberately not a global mode. This
aligns the roadmap prose with the already frozen AP0-2 machine enum; it does
not authorize Live or change the deferred M11 gate.

PostgreSQL owns separate immutable revision tables and fenced mutable heads for
the platform mode, every governed effect class, and each fixed kill switch.
ActivationReceipt bodies bind the exact target revision/digest, expected head
generation, transition direction, deployment mode/effect ceilings, owner,
reason, request digest, and a database-enforced maximum one-hour validity
window. Receipt consumption and governance events are separate append-only
records; replay of the exact current receipt is idempotent, while stale,
expired, reused-for-another-head, no-op, or direction-incompatible transitions
are rejected.

Candidate authoring, CAS activation, and emergency halt use three non-overlapping
database roles. The emergency role may only create `disabled`/`halted`
successors. A fixed, migrator-owned per-subject lock row protects both existing
heads and the otherwise rowless bootstrap race without exposing a predictable
advisory-lock key or serializing unrelated subjects. Receipt validity is
rechecked after every potentially blocking lock acquisition. Runtime profiles
receive a read-only exact-current-head projection, never base-table mutation
privileges.

The Go resolver recomputes each immutable revision digest and intersects the
fresh snapshot, deployment ceilings, global mode, exact effect head, fixed
route requirements, and every applicable kill switch. Missing, stale,
malformed, halted, unknown, or incompatible state denies the effect. Broker
mutation routes automatically require operation-emission plus exact-confirmed
or autonomous/Delegation switches; external reads automatically require the
external-capability switch. AP0 release effect ceiling remains `none`.
