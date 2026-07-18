# Research and Data Plane

> Status: **FROZEN ARCHITECTURE — evidence authority, provenance, point-in-time
> snapshots, Data Desk, research funnel, and core/specialist role boundaries are
> authoritative. Providers, schemas, source priority details, retention, and
> implementation acceptance are not yet specified or authorized.**

## Purpose

The Research/Data Plane is an evidence system, not a collection of search tools
or text pasted into prompts. It must explain where information came from, when
it became knowable, how it was transformed, where it conflicts, and which
decision used it.

```text
external source
  -> connector / Tool Gateway
  -> raw Evidence
  -> normalization and entity resolution
  -> dedupe, conflict, freshness, and quality processing
  -> Evidence Store and point-in-time Snapshot
  -> Evidence Bundle
  -> Research/Decision Agents
```

## Authority boundary

The Kernel Provider remains the sole authority for the bound account,
positions, buying power, broker orders/fills, tradable instrument identity,
execution quote, and risk facts. External research prices may add historical or
contextual evidence but cannot override the Kernel price or permit a trade.

Official regulatory/company sources may be authoritative for their own filed or
published content. News, third-party estimates, web pages, and Agent
interpretation remain claims/evidence rather than Kernel facts. Source priority
is defined per data type rather than by one global score.

## Evidence classes

- `RawDocument`: immutable source document, page, response, or payload;
- `ExtractedClaim`: a statement extracted from a source but not thereby proven;
- `ValidatedFact`: a fact established through an approved deterministic parse,
  official-source contract, or independent validation rule;
- `DerivedMetric`: a deterministic calculation with recorded inputs and code
  revision;
- `AgentInterpretation`: analysis, relevance, causality, forecast, or thesis.

Summarization or repetition cannot promote a Claim/Interpretation into a
ValidatedFact. Transformations create a new object with lineage; they never
overwrite their source.

## Evidence identity and time

The detailed schema remains future work, but Evidence must bind:

- stable Evidence and source identifiers, type, entity/symbol, and source
  location/document reference;
- publication, effective, first-observed, retrieval, and normalization times;
- raw checksum and immutable/raw storage reference;
- connector, normalizer, parser, metric, model/prompt, and Contract revisions as
  applicable;
- units, currency, timezone, market calendar, adjustment method, and window;
- freshness/expiry, quality limitations, conflicts, supersession/correction,
  license, and retention policy.

`published_at`, `effective_at`, `retrieved_at`, and `observed_at` are distinct.
Historical replay and strategy evaluation use what Alpheus could have known at
the decision time, never a later revision or publication. Every Evidence Bundle
binds a point-in-time Snapshot and complete query/coverage manifest.

## Raw source retention

Normalized facts and summaries do not replace raw Evidence. The system must be
able to reproduce what was retrieved and which bytes a parser/model saw. Store
raw content once and pass references, not copies, between Agents. Storage may
later separate large immutable objects from PostgreSQL metadata without
changing identity or lineage.

## Research capability categories

The active registry is expected to support, without fixing Providers yet:

1. market regime and macro conditions;
2. broad and targeted universe discovery;
3. company fundamentals, guidance, valuation, and capital structure;
4. filings, corporate actions, insider and regulatory events;
5. news, catalysts, conferences, product, legal, and macro events;
6. industry, peers, value chain, supply chain, bottlenecks, and pricing power;
7. technical structure, price/volume, volatility, liquidity, and relative
   strength;
8. options and derivatives surface, Greeks, volatility term structure, and
   contract liquidity;
9. portfolio context and risk from the Kernel;
10. Alpheus decisions, execution, outcomes, Post Mortems, Playbooks, and GRACE
    publications.

This is a capability taxonomy, not a requirement to call every category for
every task.

## Data Desk

Data Desk is a restricted LLM evidence coordinator used for open-ended or
multi-source research. It:

- translates a research question into evidence goals and categories;
- produces an `EvidencePlan` within the Task budget;
- selects source capabilities with the Registry and Coverage Resolver;
- requests deterministic connector/Tool work;
- identifies missing, stale, conflicting, weak, or correlated evidence;
- returns an `EvidenceBundle` and `CoverageReport`.

It is not a scraper or Provider implementation. It cannot decide a trade,
increase budget, validate its own output, write Kernel/GRACE/Delegation state,
or interpret absence of evidence as evidence of absence.

Simple deterministic queries may bypass the Data Desk LLM when the active Skill
already identifies the exact capability and output Contract.

## Connectors and normalization

Connectors are deterministic, credential-scoped Tool implementations. They
record request identity, source/provider revision, timing, response checksum,
cost/rate limits, retry/effect state, and bounded raw result. They do not create
investment conclusions.

Normalization performs entity/symbol resolution, units and timestamps, schema
conversion, corporate-action/adjustment handling, duplicate detection, and
source-specific validation. Entity ambiguity or incompatible definitions are
preserved as explicit conflict/gap state rather than guessed.

## News and event processing

News handling distinguishes original/official sources, primary reporting,
syndication, secondary reporting, and unverified claims. The pipeline records
publication/retrieval timing, deduplicates copies without losing lineage,
resolves entities, and produces event/claim references.

LLMs may assess relevance, materiality, narrative, and possible market impact,
but source class and official-document identity should be deterministic where
possible. Retrieved content is untrusted and never enters the instruction layer
of a prompt.

## Technical and quantitative evidence

Technical indicators and deterministic statistics are computed by versioned
code from a pinned price/volume Snapshot. Record the source, observation window,
market calendar, timezone, adjustment method, exact parameters, algorithm
revision, and input checksum. LLMs interpret metrics but do not invent their
values.

Technical analysis is one evidence dimension, not an automatically mandatory
indicator set. The Task/Skill determines material relevance and the Coverage
Report records whether it was used or skipped.

## Conflict, freshness, and coverage

An Evidence Bundle distinguishes:

- consistent validated facts;
- conflicting definitions or values;
- stale/expired evidence;
- missing capability or coverage;
- extracted but unvalidated Claims;
- Agent interpretations and uncertainty.

Conflicts follow data-type-specific source rules and remain visible when those
rules do not resolve them. One LLM confidence score cannot erase a conflict.
Freshness is part of the Evidence contract and is rechecked at Task use, not
only at acquisition.

## Acquisition modes

### Persistent tracked universe

Maintain configured, budgeted monitoring for current positions, watchlist,
active candidates, imminent catalysts, and strategy-covered entities.

### On-demand broad discovery

Query wider universes through registered capabilities when a Run requires new
opportunities. Broad discovery does not require permanent ingestion of the
entire market. Selected candidates enter tracked state through a durable,
versioned decision.

## Research funnel

Research progresses by information value and cost:

```text
broad, inexpensive discovery
  -> initial evidence and event enrichment
  -> deep specialist research on a smaller set
  -> independent challenge
  -> Decision Desk synthesis
```

Heavy filing, supply-chain, options, or other specialist work is invoked when
relevant to a surviving candidate and Task, not for every discovered symbol.
The exact funnel thresholds remain future policy.

## Core and specialist Agent direction

Stable logical core roles:

- **Data Desk:** evidence planning, source coverage, conflict, and gaps;
- **Scout:** discovery coordinator that merges and deduplicates candidate
  signals into a Candidate Set;
- **Decision Desk:** synthesis of evidence, challenge, portfolio context, and
  WAIT/PASS/PROPOSE intent;
- **Position Manager:** existing positions, original thesis, invalidation,
  monitoring, and exit intent;
- **Challenger:** independent attack on claims, evidence, assumptions, and
  risks;
- **Coach:** post-outcome attribution hypotheses, candidate lessons, and
  strategy improvement proposals.

On-demand specialist capabilities may include macro/regime,
fundamental/valuation, filings, catalyst/news, industry/supply-chain,
technical/market-structure, options/volatility, and strategy research.

These are architectural responsibilities, not frozen Agent count, names,
prompts, schedules, or hierarchy. A Specialist runs only when the Task graph
requires its capability. No universal Scout is expected to discover, research,
challenge, decide, monitor, and learn alone.

## Candidate signal contract direction

Independent discovery routes normalize outputs into a common Candidate Signal
with entity, observation time, discovery reason/type, horizon, Evidence
references, freshness, and known limitations. Scout merges and deduplicates
signals without assuming multiple mentions are independent evidence; common
underlying sources remain linked.

Where the Candidate Signal is scoreable, it also binds the frozen discovery
universe/route, rank or bucket, prediction target, approved confidence
semantics, benchmark/comparator, primary evaluation horizon, exclusion state,
and evaluation Contract. The complete eligible, excluded, expired, denied, and
untraded stream remains available to GRACE; Scout cannot submit only candidates
whose later prices were favorable.

Specialist outputs distinguish conclusion, supporting and contradicting Claims,
Evidence, conflict, missing information, validity/invalidation, and uncertainty
with reasons. Narrative is stored once as an Artifact; downstream Agents receive
the structured summary and retrieve details on demand.

## Context injection

Agent context receives the question, Coverage Summary, key Claim/Fact/Metric
summaries, conflicts, gaps, freshness, Evidence references, and Snapshot id.
Raw documents are loaded only when the Skill/Task requires their relevant
sections. Compact preserves all Evidence and Snapshot references so summaries
can be regenerated.

## Failure behavior

- Source unavailable/stale: mark the gap and fail/wait where required; do not
  treat cache as current without its freshness contract.
- Conflicting source data: preserve and route the conflict.
- Entity or unit ambiguity: refuse normalization until resolved.
- Connector/model extraction failure: preserve raw source and explicit failure;
  do not fabricate a normalized fact.
- External research quote differs from Kernel: report divergence; Kernel remains
  authoritative for execution.
- Data Plane unavailable: no new evidence-dependent decision; Kernel operation
  safety and position truth continue independently.

## Required later specification

Before implementation, freeze the source registry, Evidence/Claim/Fact/Metric,
Snapshot, EvidencePlan/Bundle, coverage/conflict/freshness, tracked-universe,
connector/normalizer, and specialist Artifact schemas; provider/source policy;
licensing/retention; and point-in-time, provenance, injection, stale-data,
disagreement, and future-leakage acceptance probes.
