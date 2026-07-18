# Multi-level Memory and Context Management

> Status: **FROZEN ARCHITECTURE — memory levels, authority separation,
> candidate promotion, temporal retrieval, context budgeting, and compact
> safety are authoritative. Exact schemas, ranking algorithms, retention
> periods, storage/index technology, and implementation acceptance are not yet
> specified or authorized.**

## Purpose

Memory is a governed retrieval layer, not an infinite conversation and not a
source-of-truth database. It helps an Agent recover work and recall relevant
experience while preserving provenance, validity, authority, and temporal
boundaries.

Kernel, Evidence Store, Strategy/Playbook Registry, Skill Registry, GRACE, and
Delegation remain authoritative for their own facts and revisions. Memory
references and organizes them; it cannot copy itself into authority or replace
them.

## Frozen principles

1. Agent continuity comes from durable state, artifacts, checkpoints, and
   scoped retrieval, not a permanently running model process.
2. Memory is typed, sourced, time-bounded, scoped, and versioned.
3. Agent output enters long-term memory only through Candidate, validation, and
   promotion states.
4. Retrieval is hybrid and policy-filtered; vector similarity alone is never an
   authority or relevance decision.
5. Context is assembled within explicit section budgets. Memory cannot displace
   required instructions, current user intent, Kernel facts, or Evidence.
6. Compact may summarize eligible narrative but cannot rewrite or delete
   canonical references or unresolved state.
7. Historical replay can retrieve only information that existed by its `as_of`
   boundary.
8. Popularity, repeated Agent wording, retrieval frequency, or profitable
   outcome does not independently validate a memory.
9. No Agent owns a hidden private long-term memory that other system components
   cannot audit.
10. Credentials, authentication tokens, private hidden chain-of-thought, and
    uncontrolled external instructions are not memory content.

## Memory levels

### L0 — Turn Scratchpad

Ephemeral state for one model/tool attempt: temporary calculations, tentative
ideas, intermediate tool handling, and the immediate next step. It is discarded
when the Attempt ends unless an allowed structured result is committed.

L0 does not preserve raw private chain-of-thought. Explanatory conclusions and
evidence references belong in typed Artifacts where required.

### L1 — Task and Run Working Memory

Durable restorable state for active work:

- current user objective and Task Contract;
- completed and pending steps;
- latest valid Checkpoint;
- decisions, constraints, and rejected alternatives with reasons;
- unresolved questions, conflicts, and unknowns;
- required Artifact, Evidence, operation, policy, and revision references;
- next action and current budget state.

L1 closes when its Run completes or is terminally cancelled. It does not remain
an indefinitely appended chat history.

### L2 — Episodic Memory

Versioned cases describing what happened in a bounded historical episode, such
as a research Run, candidate analysis, trading decision/outcome, recovery,
Tool-use failure, or Post Mortem. An episode retains its generating market
regime, strategy/prompt/model/policy revisions, point-in-time Evidence Snapshot,
decision, outcome, and known limitations.

An episode is experience, not automatically a reusable rule. A profitable or
memorable outcome remains one case until independently validated.

### L3 — Semantic Memory

Reviewed knowledge intended for reuse across Runs, including explicit user
preferences, validated operational knowledge, stable entity/industry
relationships, reviewed research methods, and reusable experience with a
defined applicability window.

L3 requires stronger provenance, scope, conflict handling, validation, and
expiry/requalification than L2. Agent-written summaries cannot enter it
directly.

### L4 — Authoritative Registry References

Playbooks, strategies, Kernel policy, GRACE model/ScoreSnapshot revisions,
Delegation policy/grants, Skills, and human-owned limits are not Memory. L4 is a
reference layer that binds the exact active or historical authoritative
revision. Memory cannot fork a copied policy and make the copy authoritative.

### Cold Archive

Original messages, superseded Checkpoints, full Artifacts, source documents,
and inactive history remain available for audit or explicit retrieval but are
excluded from default active retrieval. Archival and retention follow the
owning source's policy and legal/licensing constraints.

## Scope and sharing

Memory items carry an explicit scope such as user, team, Agent role revision,
strategy revision, entity, portfolio/account, market regime, Task, or Run.
Access is the intersection of authenticated user, Agent permission, Task/Skill
scope, sensitivity, and deployment policy.

Workers may have private L0 and Task-bound L1 state. Durable L2/L3 knowledge is
stored once in a shared governed store with role-specific retrieval views. This
prevents invisible persona bias, duplicate conflicting truth, and knowledge
silos while preserving least privilege.

Changing an Agent prompt/model/contract revision does not automatically inherit
private identity claims. Any allowed transfer is explicit and attributable.

## Memory item contract direction

The detailed schema remains future work, but a memory binds at least:

```text
memory id, type, scope, subject/entity/strategy
source Artifact and Evidence references
author and generating Agent/model/prompt/Skill revisions
created, observed, valid-from, valid-until, and review times
market regime and point-in-time Snapshot where relevant
authority and sensitivity classes
validation state and validator reference
confidence/uncertainty with reason
supersedes, contradicts, narrows, extends, or invalidates links
retrieval/access policy
content and index/embedding revisions
```

The item distinguishes fact reference, episode, user preference, hypothesis,
interpretation, candidate lesson, and validated semantic knowledge. It cannot
hide these distinctions behind one generic text field or score.

## Candidate and promotion lifecycle

```text
source Artifact / user instruction / canonical event
  -> MemoryCandidate
  -> schema, identity, provenance, and permission validation
  -> dedupe and contradiction analysis
  -> applicability, temporal, and evidence review
  -> approved Active Memory or rejected/expired Candidate
```

Promotion policy depends on type:

- an explicit durable user preference may be activated with user visibility,
  correction, and deletion controls;
- canonical Kernel or deterministic Tool facts remain references to their
  source rather than copied semantic claims;
- one research conclusion or outcome enters Episodic memory;
- Coach output and profitable experience enter Candidate Lesson state;
- reusable semantic knowledge requires independent evidence/review;
- strategy changes go to Playbook/Strategy Candidate, not Semantic Memory;
- GRACE score or Delegation authority is never created through Memory
  promotion.

An Agent cannot validate its own claim by saving and later retrieving it. A
summary of an Agent assertion is not independent evidence.

## Retrieval pipeline

Memory retrieval is controlled RAG:

```text
Task and Skill retrieval need
  -> query interpretation
  -> access, scope, type, and as-of filters
  -> structured/exact retrieval
  -> lexical and semantic retrieval
  -> authority, freshness, applicability, and conflict processing
  -> dedupe and evidence/source diversity
  -> budgeted reranking and Context packing
  -> retrieval manifest
```

The system should support structured and exact filters, keyword/full-text
search, semantic similarity, and relationship traversal where justified. The
storage/index technology is not frozen. Embeddings are derived, versioned,
rebuildable indexes and never the authoritative record.

Search returns compact metadata first: type, scope, source, time, validation,
applicability, conflict, summary, and references. Full content or source ranges
are loaded only when selected by the Task/Skill.

Retrieval records the query, filters, candidate ids, ranking/index revisions,
selected items, omissions caused by budget, and final Context manifest.

## Temporal retrieval and replay

Every retrieval supports an `as_of` boundary together with relevant entity,
strategy/policy revision, regime, Task type, and allowed memory types. A replay
cannot see a Memory, Post Mortem, Evidence revision, or correction first
observed after the reconstructed decision time.

Memories with narrower regimes, strategies, or applicability windows remain
marked as such. Textual similarity cannot silently remove those limitations.

## Conflict, supersession, and expiry

New memory does not overwrite old memory. Relationships explicitly record
supersession, contradiction, narrowing, extension, or invalidation. When
material conflict is unresolved, retrieval returns both sides with their
sources, times, and validation states rather than selecting one as truth.

Expired or stale memory exits normal retrieval but remains attributable and
auditable. Requalification produces a new validation event/revision. Retrieval
frequency alone cannot renew validity.

## Context authority and order

Context sections preserve authority and instruction boundaries:

```text
system safety and authority rules
  -> current user request and explicit constraints
  -> active Skill instructions
  -> Task Contract and valid Checkpoint
  -> canonical Kernel facts
  -> validated Evidence and current Snapshot
  -> authoritative Strategy/Playbook/GRACE/Delegation references
  -> retrieved Memory
  -> untrusted external content
```

Memory is data, not a system instruction. A past preference cannot override the
current request; Agent/external prose cannot override a Skill, Kernel, GRACE,
Delegation, or human-owned policy.

## Context budget

Context is divided into explicit budgets for non-compressible system and
authority material, current request/Task, active Skill entrypoints, canonical
Kernel and Evidence facts, Checkpoint, retrieved Memory, and optional source
narrative. Memory cannot consume another required section's reserve.

On pressure, the assembler first applies scope/freshness/authority filters,
deduplication, source diversity, compact summaries with references, and
optional-detail removal. It then splits or stops the Task if necessary. It
never silently truncates the risk-relevant tail or claims complete coverage
when budget omitted material candidates.

## Safe automatic compact

Compact is an atomic, validated checkpoint transition:

1. Control Plane builds a deterministic `MustPreserveManifest`.
2. The Compactor summarizes only eligible narrative and produces a structured
   Checkpoint candidate.
3. A Validator proves that every required item/reference and unresolved state
   is preserved with the correct identity and status.
4. On success, commit a new Checkpoint revision and atomically make it active.
5. Archive, but do not rewrite, original Turns, Messages, Artifacts, and the
   previous Checkpoint.
6. On failure, retain the previous active Checkpoint and stop/retry within the
   existing budget.

The manifest includes at least objective, user constraints, Task state,
decisions and owners, rejected alternatives and reasons, required Evidence,
Artifact, operation, policy and revision ids, conflicts/unknowns, outstanding
questions, budget/authority state, and next work.

## Summary-drift protection

- Every Checkpoint records its source ranges and all generator revisions.
- Canonical facts remain references and are not paraphrased into authority.
- A later compact can use original Artifacts and structured state rather than
  recursively summarizing only the last summary.
- Checkpoint validation compares against the Must-Preserve manifest.
- Important narrative can be regenerated from archived sources.
- A summary is never counted as evidence independent of its sources.
- Skill entrypoints are reloaded completely from their pinned revisions after
  compact or recovery.

## Growth, consolidation, and retention

Physical audit storage may grow while active retrieval and Context remain
bounded:

- L0 is discarded after the Attempt;
- L1 is sealed when its Run ends;
- L2 retains a case index while low-value/full narrative may move to archive;
- L3 has review, validity, decay/requalification, and supersession policy;
- authoritative records follow their owner and are only referenced;
- search/embedding indexes can be rebuilt without changing memory identity.

Maintenance performs deduplication, clustering, expiry, contradiction
detection, broken-lineage checks, index rebuild, sensitivity scanning, and
retrieval-quality audit. It cannot delete an owning system's canonical record,
promote frequently retrieved content, or declare rarely used content invalid
without policy.

## Memory tools

Agents receive bounded capabilities equivalent to:

```text
search_memory(query, scope, type, as_of)
read_memory(memory_id)
read_memory_sources(memory_id)
explain_memory_lineage(memory_id)
submit_memory_candidate(...)
```

Skills may request memory types and scopes but cannot widen Agent/user access or
write Active memory directly. Tool results follow `SKILLS_TOOLS.md` and are
persisted as references rather than uncontrolled context.

## Trading-learning boundary

```text
canonical decision and reconciled outcome
  -> Episodic Case
  -> Coach Post Mortem
  -> Candidate Lesson
  -> independent validation
  -> Playbook/Strategy Candidate
  -> versioned strategy review
  -> GRACE independent evaluation
  -> separate Delegation review if authority is requested
```

Memory preserves the case, attribution hypothesis, evidence, and lesson state.
It does not activate a strategy, change Kernel limits, score credibility, or
increase Delegation authorization. Shadow/Live distinctions, selection
effects, and the performative-feedback boundary in `GRACE.md` remain intact.

## Logical components

The first implementation may remain one deployable Agent Platform, but it has
clear ownership for:

- Candidate Writer;
- Validator/Promoter;
- Memory Store;
- structured, lexical, and semantic indexes;
- Retriever;
- Context Assembler;
- Compactor and Checkpoint Validator;
- Auditor/Janitor.

LLMs assist query interpretation, candidate extraction, narrative summary, and
relevance analysis. Code owns access, time, state, lineage, budget, promotion,
and authoritative references.

## Failure and security behavior

- Retrieval unavailable: a Task whose Skill requires memory waits/fails; an
  explicitly memory-optional Task may continue with the absence recorded.
- Required memory stale/missing: surface the gap; do not substitute lower-
  authority text silently.
- Compact validation fails: no checkpoint cutover and no source deletion.
- Index unavailable/corrupt: rebuild from authoritative memory records; do not
  treat empty search as proof of no memory.
- Conflicting memory: return conflict state and sources.
- External/prompt-injection content: store as untrusted source and never promote
  its instructions.
- Secret/sensitive content: reject, redact, or quarantine by policy before
  indexing or model injection.
- Agent self-reinforcement: prevent self-authored retrieval from counting as
  independent validation.

## Required later specification

Before implementation, freeze the exact MemoryCandidate/Item/Validation,
Checkpoint/MustPreserve, scope/access, temporal, conflict, retention,
retrieval/ranking, Context budget/manifest, index revision, user correction and
deletion, and audit schemas. Acceptance must cover crash-safe compact, summary
drift, missing must-preserve facts, future leakage, prompt injection, private
scope, conflicting/expired memory, index rebuild, self-validation, Context
overflow, and bounded growth.
