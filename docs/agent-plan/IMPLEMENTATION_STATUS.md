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
  model-call and terminalization facts. No deployed Worker uses this canonical
  AP1 path yet; child-task requests and cancellation submission are durable,
  while cancellation reconciliation and recovery commands remain absent. An
  idempotent, digest-pinned bootstrapper now deploys the already-frozen
  AP0/AP1 schema and grants in their tested order; it is a database substrate
  only, not a deployed Cortex Control or Worker.
  A bounded, local-only OutputContract
  validator and its future receipt command contracts landed at `f70388d`.
  They are not yet wired into a deployed Control/Worker loop; database receipt
  persistence is intentionally deferred until after the MVP loop. A fenced,
  immutable Worker child-task-request slice now records the requested symbolic
  capability, reason code, objective, inputs, output Contract and subordinate
  limit without creating a runnable Task or Session. Control/Scheduler
  admission remains a separate later command. This canonical AP1 path cannot
  call a model or produce an external effect.
- The Agent Lab MVP query queue gained crash recovery at `fde5fc2`. Kernel-owned
  Jobs now use database-time leases, per-attempt fencing tokens and bounded
  recovery scans. A Kernel restart can reclaim a queued or expired-running Job;
  the stale process cannot overwrite the winner. Recovery reloads the current
  encrypted OpenAI credential rather than persisting it with the Job. This is
  stability for the existing read-only MVP path, not the canonical AP1 Worker.
- Architecture clarification adopted 2026-07-21: the Agent product is named
  **Cortex**. Canonical Agent Lab, collaboration and Tool history belong to
  Cortex Control; Research collection, normalized evidence and point-in-time
  replay belong to the independent Research Plane. The Kernel-owned query queue
  remains compatibility-only and must be retired rather than extended into a
  Cortex workflow owner. See
  [`CORTEX_RESEARCH_BOUNDARY.md`](CORTEX_RESEARCH_BOUNDARY.md).
- AP2-1 has begun with strict in-memory contracts for immutable Cortex
  `Conversation` and raw `UserRequest` facts.  They bind user/control-api
  identity, exact BlobRef-backed input/attachments, referenced-record
  deduplication and creation time before any model interpretation.  An
  independent immutable `agent_input` schema now persists the facts under
  default-deny grants and defers cross-table attachment validation to commit.
  The sole admission command now also persists an exact, idempotent request
  through the scoped Control API database role: it binds the workload actor to
  its login identity, validates the referenced Conversation digest, and writes
  the raw input, attachments, and referenced records atomically. The
  disposable database probe exercises the real restricted role, exact replay,
  idempotency conflict, and default-deny table boundary. The deployed Input
  Gateway landed at `126057f` and now supplies a strict authenticated HTTP API, real
  owner-only local content-addressed Blob persistence, PostgreSQL Blob and
  submission adapters, exact transport-retry recovery, and a separately
  provisioned `cortex-control-1` NOINHERIT LOGIN/container on localhost port
  8400. All Agent Platform race tests, vet, shell syntax, Compose validation,
  the 17-migration disposable PostgreSQL replay/role probe, container health,
  owner-only Blob mode, and exact duplicate HTTP write smoke pass. The target
  database contains one immutable smoke request and one committed 28-byte Blob;
  the Blob root is mode 0700 and content is mode 0400 under `cortex:cortex`.
  IntentDraft, PolicyResolution,
  Run admission, question, confirmation, and Agent Lab UI routing remain
  disabled by this slice.
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
| AP1-2 PostgreSQL durable state and command transactions | In progress; immutable definitions at `bce88cc`; durable Runtime state at `7671762`; claim/start/heartbeat commands at `95a1af2`; model-call transactions at `4f3a082`; Attempt terminalization at `9ea1c04`; bounded output validator contracts at `f70388d`; deployed root admission pending this module commit | OwnerPolicy, RuntimePolicy, JSON OutputContract, Run/Task/Session/Attempt/Turn, model-call, Artifact, Checkpoint, budget, cancellation, recovery, idempotency-record, and transition-event state are durable, exact-lineage-bound, default-deny, and effect `none`. Cortex now uses separate Activator and Control LOGINS: the former selects only the fixed effect-none user-request OwnerPolicy, while the latter commits the output-schema/task-objective Blobs and atomically admits an exact-current-policy Run, root Task, ledgers, input ref, and initial events. Exact replay is stable. Attempt creation remains Worker-only at Task claim. Validation receipts, cancellation reconciliation, child admission, and recovery commands remain deferred while the MVP Worker loop is built. |
| AP1-3 Control Plane and bounded Worker execution | MVP preview at `dd55a30`; password-protected Agent Lab at `04d0139`; minimal durable query dispatcher at `95ac5f2`; read-only Scout -> Decision Desk workflow at `e84eadb`; typed Intent/capability routing at `24f94ff`; deterministic Robinhood research enrichment at `015e9d4`; RSI/MACD/ATR context at `20ce09d`; encrypted database credentials at `5759e04`; isolated Robinhood news gateway at `2d6aa00`; guarded Web Search/Fetch at `1e77534`; crash-recoverable MVP query jobs at `fde5fc2`; canonical AP1 Worker integration not started | The default Auto query first runs a typed research-only Intent Interpreter against a code-owned active capability manifest. It may route only to Scout or Team, or refuse; unknown, duplicate, or route-incomplete capability selections fail closed. Manual Team/Scout selection remains available to avoid the extra model call. Team runs a typed Scout evidence brief over normalized quote/bars plus eight default concurrent read enrichments: provider-computed daily RSI/MACD/ATR, equity fundamentals, quarterly financials, symbol earnings results, normalized Robinhood news headlines, and bounded Brave Web Search. If the user supplies a URL, an additional guarded Web Fetch provides bounded static page text. An unavailable enrichment is explicitly marked rather than failing the whole query. Headlines, snippets, and fetched pages are retrieval-stamped untrusted claims, not execution truth, instructions, or proof of the underlying claim. Decision Desk receives the Scout artifact plus canonical portfolio context for a typed `WAIT/PASS` synthesis. Code rejects `PROPOSE`, non-empty proposals, or a blackboard patch on this path, so it cannot emit an operation. The legacy scheduled/wake `runSession -> POST /operations` path is retired: a non-empty operation output is logged as `agent_output_operation_forbidden_ap1` and never reaches Kernel. Kernel persists the asynchronous `agent_query_job` lifecycle and sanitized result/error. Jobs use database-time leases and per-attempt fencing, so queued or expired-running work survives Kernel restart without stale-result overwrite. OpenAI, Brave, and the separate read-only Robinhood research credential use provider-bound AES-256-GCM envelopes in PostgreSQL; the wrapping key is independently derived from the existing Agent Web session key. Browser requests, jobs, events, responses and Agent Runtime environment contain no persisted plaintext key. Rotating the session key intentionally makes old envelopes unreadable until the credentials are replaced. This MVP dispatcher is deliberately not the canonical AP1 Worker. Effect remains `none`; there is no broker mutation or Live effect. |
| AP1-4 crash/concurrency acceptance and stage seal | Not started | Race, crash-window, duplicate-delivery, stale-lease, budget, cancellation, recovery and non-money acceptance evidence. |

AP1-1 freezes data shape and fail-closed validation only. It does not create
tables, start a scheduler, claim work, call a model, publish a behavior event,
or authorize any Kernel-facing effect.

## Cortex cutover execution ledger

This ledger counts only accepted completion, not code written or unit-tested.

| Step | Scope | Current status |
|---|---|---|
| 1 | Freeze Kernel / Cortex / Research boundary | Complete and frozen |
| 2 | Immutable Conversation / UserRequest and default-deny storage | Complete |
| 3 | Restricted idempotent Control API submission | Complete |
| 4 | Blob-first Input Gateway admission orchestration | Complete |
| 5 | Independent HTTP input API | Complete at `126057f`; deployed and exact-replay smoke passed |
| 6 | Real local Blob persistence and PostgreSQL adapters | Complete at `126057f`; database and owner-only file probes passed |
| 7 | Dedicated Cortex LOGIN, container, and localhost port | Complete at `126057f`; healthy on `127.0.0.1:8400` |
| 8 | Canonical Run / root Task admission; Attempt on Worker claim | Complete locally; deployed, exact-replay smoke passed, Run `queued` / Task `ready` persisted |
| 9 | Verified OpenAI Worker with durable Turn / Artifact | Complete; deployed smoke persisted succeeded Run/Task, result-committed Attempt/Turn, and `assistant_response` Artifact |
| 10 | Agent Lab cutover and Kernel queue retirement | Complete; page uses direct Cortex request/Run polling; legacy Kernel queue endpoints are deprecated and unused by the UI |

Accepted cutover completion is now **10 / 10**. The deployed path is Agent Lab
→ Cortex UserRequest → canonical Run/Task → Worker claim/Attempt → durable
model dispatch → OpenAI `gpt-5.6-sol` → Control-owned output Blob → resolved
Turn → effect-none Artifact. A real Agent Lab smoke returned “Direct Cortex UI
path succeeded.” from the canonical Run. The old Kernel queue remains only as
a deprecated compatibility API and is not called by the page.

## Current read-only Research Gateway slice

Commit `2d6aa00` adds the first narrowly typed `research-gateway` connector.
The Kernel decrypts the separately imported robinhood-cli secondary credential
for one internal call; the gateway may refresh that credential and call only
the fixed Robinhood news endpoint. A refreshed credential returns only to the
Kernel and is immediately re-encrypted in PostgreSQL. Agent Runtime receives at
most 20 normalized headline records and never receives the credential, a
generic Robinhood request primitive, or any broker mutation capability.

The deployed smoke on 2026-07-20 returned nine SOFI headlines through
Kernel -> Research Gateway -> Robinhood. Gateway, Kernel, and Runtime race
tests, vet, frontend syntax, and Compose validation passed. This is research
evidence only and does not amend the production Robinhood Provider boundary.

Commit `fde5fc2` adds migration 0025 and makes the existing Agent Lab queue
restart-safe. A 20-way isolated PostgreSQL claim barrier produced one winner;
an expired lease was reclaimed as attempt 2, and the attempt-1 token could not
commit. Full/race/vet passed. The target database migrated to v25 with its one
succeeded and four failed historical Jobs unchanged, and the rebuilt Kernel
started healthy in `read_only` with Live disabled. No Agent Runtime, model,
operation or broker effect was invoked during deployment.

Commit `1e77534` adds typed Brave Web Search and static Web Fetch connectors.
The fetcher allows only HTTP(S) on standard ports, resolves and pins a validated
public IP for every hop, revalidates redirects, denies local/private/metadata
and special-use ranges, accepts only bounded textual media, and returns clean
untrusted text with source and retrieval metadata. The deployed fetch of
`https://example.com` and the degraded no-Brave-key Agent assembly passed.
Brave provider normalization is covered by race tests but has not received a
Live provider smoke because no Brave key is currently configured.

The existing Tofi provider semantics and extraction behavior were reused, but
its generic Chrome subprocess and Python DuckDuckGo fallback were not copied.
The MVP intentionally avoids a browser dependency and a second untyped search
runtime. JavaScript rendering or a separately normalized fallback can be added
later when a demonstrated source requires it; neither is needed to keep the
current read-only Agent path functional.

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
