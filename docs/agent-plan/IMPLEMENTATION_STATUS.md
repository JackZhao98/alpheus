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
  effect is enabled.
- AP1-2's immutable definitions landed at `bce88cc`, its default-deny durable
  Runtime state landed at `7671762`, and its first transactional lease slice
  landed at `95a1af2`. Durable model-call dispatch, unknown containment,
  reconciliation, budget settlement, and expired-dispatch same-Attempt recovery
  landed at `4f3a082`. Atomic Attempt completion/failure, immutable non-effect
  Artifact retention, disabled publication intent creation, retry/dead-letter
  settlement, and final-fence race containment landed at `9ea1c04`. Its
  database surface now lets a correctly provisioned
  Worker claim, start, and heartbeat durable non-money Tasks and transact exact
  model-call and terminalization facts. The deployed bounded Cortex Worker now
  uses this canonical AP1 path for effect-none Agent Lab requests; child-task
  requests and cancellation submission are durable,
  while cancellation reconciliation and recovery commands remain absent. An
  idempotent, digest-pinned bootstrapper now deploys the already-frozen
  AP0/AP1 schema and grants in their tested order; it is the database substrate
  used by the separately deployed Cortex Control and Worker.
  A bounded, local-only OutputContract
  validator and its future receipt command contracts landed at `f70388d`.
  They are now wired into the deployed Control/Worker loop and immutable
  schema/output evidence is persisted before Worker read access. A fenced,
  immutable Worker child-task-request slice now records the requested symbolic
  capability, reason code, objective, inputs, output Contract and subordinate
  limit without creating a runnable Task or Session. Control/Scheduler
  admission remains a separate later command. This canonical AP1 path cannot
  call a model or produce an external effect.
- The former Agent Lab MVP query queue is retired. Kernel no longer creates,
  recovers, dispatches, or extends `agent_query_job`; `POST /agent/query`
  returns `410 agent_query_retired`, and Compose no longer defines
  `agent-runtime`. Existing rows and Trace remain immutable/readable through
  the authenticated historical GET endpoint. New work enters Cortex directly.
- Architecture clarification adopted 2026-07-21: the Agent product is named
  **Cortex**. Canonical Agent Lab, collaboration and Tool history belong to
  Cortex Control; Research collection, normalized evidence and point-in-time
  replay belong to the independent Research Plane. The Kernel-owned query queue
  remains compatibility-only and must be retired rather than extended into a
  Cortex workflow owner. See
  [`CORTEX_RESEARCH_BOUNDARY.md`](CORTEX_RESEARCH_BOUNDARY.md).
- GEXBot is now the first deployed independent Research Plane Provider. The
  `gexbot-provider` has its own NOINHERIT database LOGIN, provider-only
  ingestion/read tokens, immutable `research.gexbot_observation` records,
  AP0 Blob-backed raw payloads, `available_at`-correct `as_of`, and
  generation-fenced replay cursors. `research-gateway` holds only the Provider
  read token and exposes the bounded internal read/replay façade; it never sees
  the GEXBot upstream credential or raw payload. The existing 4,215 historical
  Kernel observations were imported one-for-one with the original collector
  availability time and Provider-owned Blob references. Kernel no longer starts
  a GEXBot collector. One deliberately narrow, pre-registry Cortex Tool is now
  deployed: the immutable Intent output may propose exactly one SPX
  `research_gexbot_as_of` snapshot; Control binds its source Model result,
  Worker lease, budget and `as_of` fence; Research Gateway records a durable
  normalized evidence/receipt pair before Desk can consume it. It exposes no
  Provider credential or raw payload, and it does not collect, mutate, or
  submit an order. Live proof: Run
  `e13d25aa-595c-4d92-ab2f-02dcc96e879e` recorded both
  `tool_call_authorized` and `tool_receipt_succeeded` before Decision Desk
  completed. A Scout grant and Agent-facing replay/stream Tool remain later
  work; the code-owned Tool registry is now deployed.
- Moody Blues is now the deployed, canonical temporal-data control surface in
  Research Gateway. Its first declaration is `gexbot_classic`: it reports a
  distinct official on-demand `live` read plus `as_of` and replay capability, microsecond
  query fences, 30-second observation cadence, and
  `latest_available_at_lte_as_of` semantics. Migration `0048` provides a
  Provider-only collection-status projection with no raw payload exposure.
  The directory, three-series collection status, an archived `as_of` result,
  and a generation-fenced replay step were verified after the migration and
  service recreation on 2026-07-23. The three SPX categories are
  `gex_full`, `gex_zero`, and `gex_one`; their latest verified archive
  observation is `2026-07-22T19:59:30Z`. This must not be presented as an
  historical GEXBOT live quote. `market_gexbot_live` now performs a separate
  official API fetch, archives the raw Blob and records normalized Evidence
  and Receipt while preserving both provider `source_timestamp` and request
  `fetched_at`. Real Run `edf5bb71-51c2-4df6-8ded-17b890f13d51` completed
  Options Scout and Decision Desk with that receipt and refused to call the
  older source timestamp real-time. Legacy `/internal/v1/gexbot/*` routes remain
  narrow compatibility aliases while callers migrate to `/moody-blues/*`.
- The first narrow Kernel fact bridge, `kernel_earnings_results`, is deployed
  and Cortex-enabled. It can request only one uppercase ticker through the
  bound Robinhood MCP `get_earnings_results` read call and returns only
  normalized EPS/report-date facts to Cortex. It deliberately exposes neither
  a generic MCP method nor a brokerage account/credential. Migration `0049`
  corrected JSONB key-whitelist precedence without mutating the applied
  migration history. Real Run `e025fff6-706e-48f9-abc7-da0655ca2e33`
  completed Intent → Desk, authorized `kernel_earnings_results`, persisted
  receipt `9a64ad74-ec04-497d-a8ec-f4ccb10fd279`, and answered only from the
  normalized TSLA evidence. Its Agent Lab precision-test row is now unlocked.
- All remaining 33 reviewed Robinhood MCP read/preflight capabilities are now
  Cortex-enabled through a versioned Kernel-read protocol. Every Tool ID is
  paired with exactly one upstream function and an argument allowlist; model
  input cannot contain `account_number`, and Kernel injects only the permanently
  bound account. Control migration `0050` persists immutable authorization,
  sanitized evidence, receipt and Trace records; `0051` gives only new Runs the
  v6 workflow contract; `0052` fixes the result-digest helper without rewriting
  migration history. Upstream MCP framing and guide text are discarded before
  evidence reaches Desk. The two review tools are simulations and have no order
  placement path. Real Provider runs passed for `kernel_portfolio`
  (`b4557073-4bd6-4b95-85a2-f50d3bf94c73`), `kernel_equity_quotes`
  (`8819eb56-b071-43af-8cc1-cbae5869f692`), `kernel_search`
  (`e02b29f0-527a-404b-ab3c-2db7f7c9f5ce`) and `kernel_accounts`
  (`581e5e0b-a928-489e-9009-8e43e7d37602`).
- Moody Blues now includes deterministic transform `gex_compact_v1` after
  point-in-time selection and before Cortex evidence. It keeps reviewed timing,
  provenance, raw Blob reference and six normalized GEX metrics, rejects
  prompt-shaped/unreviewed fields, caps output at 16 KiB, and performs no
  market interpretation. This is the requested mathematical preprocessing
  frame; future calculations must add a new version rather than mutate v1.
- Agent Lab now separates two operator tests. Stage A gives each enabled Tool
  a precision prompt and requires the exact authorization plus matching
  receipt. Stage B does not name a Tool ID or Agent and instead validates an
  ordered persisted route. The deployed page now reports 37 enabled Tools and
  0 locked candidates. Five Provider-UUID-dependent rows require an exact ID
  from their displayed prerequisite Tool instead of asking the model to invent
  one. Browser-run earnings route
  `168a9741-6668-4d4f-bb53-5b1e56b84526` and full Scout collaboration route
  `47557a5a-fa86-43b6-b8ec-e114ed671981` both passed their expected database
  trace; the page emitted no browser-console errors. Six registered Specialist
  roles are active: `market_scout`, `fundamental_scout`, `options_scout`,
  `position_manager`, `catalyst_scout`, and `discovery_scout`. Control enforces
  the unique Tool grant before authorization; every Specialist memo is a
  separate persisted model Turn before Decision Desk.
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
| AP1-2 PostgreSQL durable state and command transactions | In progress; immutable definitions at `bce88cc`; durable Runtime state at `7671762`; claim/start/heartbeat commands at `95a1af2`; model-call transactions at `4f3a082`; Attempt terminalization at `9ea1c04`; bounded output validator contracts at `f70388d`; root admission and immutable Cortex output-validation evidence deployed | OwnerPolicy, RuntimePolicy, JSON OutputContract, Run/Task/Session/Attempt/Turn, model-call, Artifact, Checkpoint, budget, cancellation, recovery, idempotency-record, and transition-event state are durable, exact-lineage-bound, default-deny, and effect `none`. Cortex uses separate Activator, Control, and Worker LOGINS. Control atomically admits an exact-current-policy Run/root Task, and validates each model output against the exact committed schema before binding its Blob to Worker. The fixed validator identity plus exact schema/output digests are immutable database evidence. Formal Result-linked validation receipts, cancellation reconciliation, child admission, and complete unknown-outcome recovery remain deferred. |
| AP1-3 Control Plane and bounded Worker execution | Canonical read-only slice deployed; Agent Lab uses Cortex directly; verified OpenAI Worker persists canonical Run/Task/Attempt/Turn/Artifact | The deployed Worker claims only canonical effect-none Tasks, starts a fenced Attempt, durably dispatches Responses API calls to explicit `gpt-5.6-sol`, heartbeats its lease during provider wait, persists actual token usage, validates and publishes structured output through Control, then resolves and commits the Attempt and Artifact. Intent may answer directly, use open Scout child work, or hand off to one of six grant-bound Specialists; Specialist and Desk each produce separate persisted Turns. Invalid provider output and exhausted Control publication retries close the Turn/Attempt explicitly. The legacy Kernel query writer and static runtime deployment are retired. External cost remains zero until an authoritative versioned price registry exists; unknown provider outcomes remain fail-closed and are not blindly retried. |
| AP1-4 crash/concurrency acceptance and stage seal | Started; real expired-Scout recovery and terminal-child reconciliation now deployed | The complete race, duplicate-delivery, stale-lease, cancellation and stage-seal matrix remains open. The deployed probes now prove bounded reservation, actual token settlement, immutable validator evidence, fail-closed recovery of an expired dispatched Scout Turn, and deterministic parent/Run terminalization when a Scout exhausts its bounded retries. |

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
| 9 | Verified OpenAI Worker with durable Turn / Artifact | Complete; deployed smoke persisted succeeded Run/Task, result-committed Attempt/Turn, `assistant_response` Artifact, and exact output-validation evidence |
| 10 | Agent Lab cutover and Kernel queue retirement | Complete; page uses direct Cortex request/Run polling; `POST /agent/query` is terminal 410, recovery is disabled, `agent-runtime` is absent from Compose, and historical GET remains read-only |

Accepted cutover completion is now **10 / 10**. The deployed path is Agent Lab
→ Cortex UserRequest → canonical Run/Task → Worker claim/Attempt → durable
model dispatch → OpenAI `gpt-5.6-sol` → Control-owned output Blob → resolved
Turn → effect-none Artifact. A real Agent Lab smoke returned “Direct Cortex UI
path succeeded.” from the canonical Run. Old queue rows remain only as
read-only historical audit records; no production path can create or execute
another one.

The next highest-priority Cortex milestone is parallel multi-Agent TaskGraph
execution. P1's independent frozen `alpheus.taskgraph` v1 pack is complete:
`task_graph_plan` and the Control-only `admit_task_graph_command` bind exact
role/Tool revisions, output contracts, per-node and aggregate budgets,
deadlines, graph depth/fanout/parallelism/round ceilings, dependency edges and
explicit `all_required` / `minimum_succeeded` Join behavior. Strict Go and JSON
Schema parity, semantic goldens and tests reject cycles, missing joins,
cross-role Tool grants, revision drift, Desk escalation, unbounded child
expansion and aggregate overcommit. P1 did not enable execution by itself;
P2/P3 now provide the separately reviewed database admission and scheduler
boundaries. P2's first storage slice is deployed: six default-deny immutable
tables persist Graph, Node, dependency Edge, Join, Join upstream and exact
per-node Specialist Tool-grant snapshots. Their foreign keys bind existing
Run/Task/model Result/RuntimePolicy/OutputContract/role-grant identities;
neither Control nor Worker LOGIN has direct table access. P2's Control-only
atomic admission command is also complete and migrated. It independently
revalidates the canonical Plan digest, exact current Run/parent generations,
source Result, RuntimePolicy, committed objective Blobs, output contracts,
role/Tool grants, aggregate and per-node budgets, DAG depth/fanout/cycles and
Join edge sets before creating every ledger, Task, dependency and immutable
snapshot in one transaction. A rollback-only database probe admits three ready
Specialists plus one blocked Desk, parks the parent Attempt/Session, returns
the exact same response on replay, rejects a cycle and changed-body replay,
and leaves no fixture rows. Future root Tasks now receive the RuntimePolicy's
bounded descendant Task allowance instead of the old single-Scout value 2;
historical Runs remain immutable. P3 is now complete: Control idempotently
prepares every node's execution/context/request/objective Blob bindings and
Worker ACL; four Worker lanes can claim independent effect-none Specialist
Tasks; and a database-owned per-Graph schedule atomically accounts
`ready → running` slots separately from the wider Run ledger. Its rollback
probe starts two nodes at `max_parallelism=2`, rejects a third concurrent
claim, and proves slot release/reacquisition. Tool-granted nodes and Decision
Desk nodes remained undiscoverable until their dedicated boundaries were
reviewed. P4 is now complete: Control resolves `all_required` and
`minimum_succeeded` barriers only after every upstream Task is terminal,
binds only committed required memo sections into the downstream Session,
grants narrowly scoped Worker read ACLs, and atomically releases the blocked
Decision Desk. Failed thresholds dead-letter the downstream Task, parked root
Task and Run; successful Desk output remains the exact child Artifact that
produced it, while an immutable graph-result row records promotion and the
parked root Task is superseded. Both success and failure close every node and
parent Session and close the graph schedule. Worker discovery and prompts now
consume the exact joined memo list and produce a strict `answer_v1` response.
The rollback audit covers ready-gating, two-lane parallel accounting, failed
Join closure, successful result promotion, result reads, terminal Session
lifecycle and immediate deferred-constraint validation. The staged TODO is
tracked in
[`CORTEX_RESEARCH_LAUNCH_TRACKER.md`](CORTEX_RESEARCH_LAUNCH_TRACKER.md):
P5 is also complete at the execution boundary. A Tool-granted Specialist must
reserve two model calls: the first is validated against the existing closed v8
workflow contract and may only formulate arguments for the exact admitted
Tool; the second receives the durable receipt plus normalized evidence and
must emit `specialist_memo_v1`. Discovery returns the exact Tool revision,
effect, remaining model budget and planner contract digest. Control
authorization accepts either a legacy immutable handoff or an exact graph-node
grant, and rejects Tool substitution, multi-action proposals and Decision Desk
Tool escalation. The database audit proves both the allowed graph grant and a
wrong-Tool denial. Research input already crosses Moody Blues'
`gex_compact_v1` deterministic transform: it whitelists six reviewed metrics,
normalizes finite numbers, caps output at 16 KiB and rejects raw payloads,
unexpected fields and prompt-like data before Worker context construction.
P6 now has a real first-round activation path. After the root Intent Turn, a
strict authority-free model proposal selects two to four installed Specialist
branches; Control reads the exact validated proposal Blob under the live
Attempt lease, expands it into the immutable bounded graph, commits objectives,
admits every Task and parks the parent. Four Worker lanes execute independent
branches concurrently, the database Join releases Decision Desk only after its
threshold is proven, and the exact Desk Artifact completes the Run. Empty Tool
grants are encoded as arrays, a parked graph parent releases only its own
active slot, and TaskGraph Desk output uses its frozen synthesis budget instead
of the legacy 2k-token linear cap. Real Run
`3d2bbe7e-85ce-48ae-9e74-f2a1002e19ee` completed four no-Tool Specialist
branches, `all_required` Join and Decision Desk successfully. Agent Lab now
renders the persisted graph as a collapsible four-lane DAG; Run
`dbe27a81-68ee-48bc-af69-d333c6bdd703` was verified in the real browser from
four simultaneous branch states through Join and final success. Durable Trace
labels proposal, graph admission, each role/task, Join and final Artifact
without mislabeling graph Turns as Intent. The remaining P6 item is a
Control-owned bounded next-round decision; the remaining P7 work is the full
crash/duplicate/slow/partial-failure acceptance matrix.

The first post-cutover hardening slice is deployed. Worker provider waits now
heartbeat the Attempt lease, use a 75-second provider deadline inside the
120-second lease, and close known invalid-output or Control-publication
failures. Input-token reservation is derived conservatively from the exact
request bytes rather than fixed at 100,000; successful calls still settle the
provider-reported actual token counts. Control validates output locally against
the exact committed JSON Schema and migration `0020_cortex_output_validation`
persists immutable validator/schema/instance evidence before Worker read access
is granted. Run `265b8742-d11e-4cef-94d5-57de94ecdcf3` completed with all
canonical states terminal, 2,918 reserved input tokens, 52 actual input tokens,
68 actual output tokens, and validator `v6.0.2` evidence.

The first post-cutover collaboration slice established the Desk edge. The
current system extends that contract with six bounded Specialists. The Intent
Interpreter's typed model output chooses a direct answer, open Scout work,
Decision Desk, or the unique Specialist that owns the selected Tool. A handoff writes immutable
`agent_control.cortex_handoff` evidence tied to the source ModelCall result,
then the Desk executes as its own canonical Turn before the root Attempt can
commit. `get_cortex_run_trace` derives the UI trace from these records rather
than returning a fabricated array. On 2026-07-21, Run
`4f478f50-e97d-4371-8766-bdb1fd38fea8` completed
`intent_interpreter_completed → handoff_to_desk → decision_desk_completed`,
with both Turns `result_committed` and the Run `succeeded`.

This direct Desk edge remains deliberately narrow: it is an in-Attempt Desk
handoff for requests that do not need a separate research memo. The UI never
asks the user to select it; its compatibility field is forced to `auto` and
never enters the immutable UserRequest.

The first AP3 cross-plane Tool slice began with
`research_web_fetch`, only when a normal routed request
handoff sees exactly one explicit public HTTP(S) URL in the immutable user
text. Cortex Control owns the Tool-call intent, policy/budget charge and final
receipt acknowledgement; Research Gateway owns connector execution, normalized
untrusted web Evidence and the durable receipt. The Research login has no
direct table-write grant and may call only its reviewed authorization/receipt
functions. Workers never receive Research credentials and may include source
text in the Desk prompt only after Control has matched the exact persisted
Research receipt.

Migrations `0023_cortex_web_fetch_tool` and
`0024_cortex_tool_authorization_lease` add the immutable intent, evidence,
receipt and acknowledgement records plus a live-lease fence for every
idempotent authorization read. Run
`120b598d-7f80-4a0b-993c-f34ebb177e55` completed the real sequence
`intent_interpreter_completed → handoff_to_desk → tool_call_authorized →
tool_receipt_succeeded → decision_desk_completed` against `https://example.com`;
its answer names that source. Agent Lab trace now retains the Tool call, Tool
identifier and receipt identifier so the page displays actual Tool evidence
rather than a fabricated timeline. This is not a generic browser or search
capability, not a raw-source archive, and not AP3 registry/activation
completion. Migrations `0026_cortex_tool_recovery` and
`0027_cortex_tool_recovery_claim_fix` now add the explicit interrupted-Tool
reconciler: Cortex Control waits 45 seconds after the original authorization,
then durably claims only an unacknowledged immutable `tool_call_id` with a
short fenced lease. It retries the exact Research request with bounded
backoff; Research first returns an already persisted receipt instead of
fetching again after a lost Control response. A stale recovery lease cannot
requeue a newer owner, and no recovery path creates an intent, changes a URL,
or revives the old Worker/Attempt. The deployed reconciler recovered two
historical interrupted calls (one missing only the Control acknowledgement and
one missing its Research receipt); the permanent queue is now fully
acknowledged and has an append-only claim/receipt audit trail.

The open Scout persistent collaboration slice is deployed locally. An
Intent Interpreter may itself choose the fixed `scout` route only when its
immutable Run has the Scout workflow contract. Control persists the handoff,
an immutable child-work request, `cortex_scout_child_admission`, exactly one
Scout Task/Session/ledger, a typed `scout_research_memo` Artifact, and exactly
one `cortex_parent_continuation` before the parent Desk Task resumes. The
parent cannot re-run Intent or create another Scout child, and the Desk reads
the memo through an Artifact-owned Blob binding rather than a fabricated
prompt reference. The Worker uses the same credential-free role pool with
fixed `intent`, `scout`, and `desk` child execution modes. Separately, six
registered Specialist roles now execute bounded in-Attempt memo Turns with
exactly one Control-enforced Tool grant and return only to Decision Desk.

On 2026-07-22, real Agent Lab Run
`af7eb22e-0f60-498e-adc4-98d53a818c59` completed
`intent_interpreter_completed → handoff_to_scout → scout_task_admitted →
scout_research_completed → desk_continuation_ready →
decision_desk_completed`, ending in the parent user-facing Artifact. Its trace
is read-only projection from durable Turns, handoffs, admissions and
continuations; it reports in-progress versus completed stages rather than
pretending a dispatched Scout has finished. The Worker heartbeat extension is
also aligned to the frozen 60-second policy maximum, so slow valid provider
calls renew their lease without denied-heartbeat noise.

The next hardening slice is also deployed locally. Migration `0035` exposes
only an expired `dispatched` or `unknown` model Turn to the Worker; after the
database reclaims its lease, the Worker marks that exact old Turn
`provider_outcome_ambiguous` and permits an ordinary bounded retry. It never
accepts a late response from the pre-crash provider call. Migration `0036`
adds the complementary terminal path: when an admitted Scout exhausts its
retries without a valid memo, Control records an immutable
`cortex_parent_scout_failure`, releases the parked parent slot, and moves the
parent Task and Run to `failed` instead of leaving the UI permanently
`running`. The read-only trace now includes `scout_parent_failed`.

Two live probes substantiate these paths. Run
`254ef676-55d9-4fe2-85f0-bcca0f1be9df` reached Scout dead-letter after repeated
invalid provider output and was reconciled to terminal `failed` with its
complete trace retained. Run `dac82d5d-f1d3-4285-ab67-912da6335cdc` was stopped
after its Scout call was dispatched; after lease expiry, its old Turn was
failed as `provider_outcome_ambiguous`, its Scout retried exactly once, and the
Run then completed `scout_research_completed → desk_continuation_ready →
decision_desk_completed`. A normal post-change Run
`ff656937-4ba0-46f5-868c-62d3d721dd01` also completed the same chain with the
Scout manifest bounded at 4,000 output tokens.

The first persistent, turn-by-turn Cortex Conversation slice is also deployed
locally. Agent Lab now retains one Cortex Conversation identifier in the page
URL (never a prompt or secret), reuses it for a `continuation` request, and
can reload its transcript from the authenticated Cortex read model. Control
does not accept browser-supplied history: it binds each immutable user-input
Blob and reads at most six completed UserRequest/Artifact pairs from the same
subject-bound Conversation. The resulting, size-bounded exchange list is
sealed into the next Session's context manifest, which the Worker reads as
record data rather than instructions. Run
`f98c3e00-2a7d-49a2-bddf-c2a1491ca57a` answered `HORIZON-37` from the prior
persisted exchange, and the conversation read endpoint returned both durable
turns. This is bounded context continuity, not an unbounded transcript,
memory system, or a mechanism for a prior message to grant new authority.

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
