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

## Moody Blues: collection and replay control

**Moody Blues** is the Research Gateway subsystem that declares and supervises
the temporal behavior of Research Providers. The name refers to the replay
ability: it reconstructs what data Alpheus could actually have observed; it
does not create a fact, reinterpret evidence, or make an investment decision.

Every registered Provider must declare independently:

- `live`: a current upstream read, which is not historical evidence unless its
  result is subsequently archived;
- `as_of`: a point-in-time read that may return only records with
  `available_at <= requested_as_of`; and
- `replay`: an ordered, generation-fenced sequence of archived observations.

Moody Blues publishes the collection owner/policy, coverage, categories, query
precision, actual observation resolution, and replay order. A precise cutoff
does not fabricate a precise observation: with a 30-second collector, an
`as_of` at `10:05:17Z` returns the most recent record available by that instant,
not an invented `10:05:17Z` sample. PostgreSQL-backed temporal requests are
canonicalized to UTC microsecond precision.

The first registered Provider is `gexbot_classic`. It currently declares
`as_of` and `replay`, but **not** `live`: the collector may read the official
API to build the archive, yet no general current-read Tool has been activated.
Future Providers must register their own truthful capability declaration; they
may not inherit GEXBOT's historical guarantees merely by being reachable
through Research Gateway.

The canonical internal management paths are intentionally Provider-specific:
`GET /internal/v1/moody-blues/providers`, then the declared GEXBOT
status/`as_of`/replay routes below that Provider. Status reports only bounded
coverage and the latest observed/available timestamps, never raw payloads or
credentials. Existing `/internal/v1/gexbot/*` routes are bounded compatibility
aliases; they do not add a generic Provider proxy or expose raw payloads,
credentials, or collection controls to Cortex.

### Future deterministic analytics slot (reserved, not implemented)

A future pure-math preprocessing module may derive filtered GEX features before
evidence reaches an Agent. Its reserved position is:

```text
Provider raw archive
  -> Moody Blues point-in-time selection / replay
  -> deterministic Research transform (future)
  -> versioned derived evidence and receipt
  -> Cortex Agent
```

Moody Blues remains the authority for collection time, `as_of`, availability,
and replay order. The future transform is a pure deterministic function: it
must have no LLM, credentials, routing decision, user prompt, or external side
effect. Every derived result must bind the transform ID/version, parameters,
input observation IDs and digests, output digest, and the original
`observed_at` / `available_at` fence so the exact result is replayable. This
reservation creates no service or container today.

## GEXBot options-data Plugin reservation

GEXBot Classic is a **read-only market-data Plugin** direction, initially for a
two-week options-data collection window and later as research evidence for a
single-day options strategy. As of 2026-07-22 its collector/archive path is a
separate `gexbot-provider` Research Plane service: it samples SPX on the fixed
30-second, 09:00–16:00 America/New_York weekday policy for `gex_full`,
`gex_zero` (0DTE), and `gex_one` (1DTE); keeps raw responses through the AP0
BlobStore; assigns `available_at` itself; and exposes bounded `as_of` and
generation-fenced replay APIs only through `research-gateway`. The former
Kernel table remains a read-only historical source during the one-way import,
but Kernel no longer schedules GEXBot requests or owns fresh snapshots.
Neither form is a Kernel Provider, an execution Plugin, or an authority to
permit an order.

The generalized registration still belongs in AP3's Capability Registry. Until
then, one intentionally narrow pre-registry path is deployed: an immutable
Cortex Intent can propose one `research_gexbot_as_of` SPX snapshot, Cortex
Control binds the Tool intent/budget/lease, and Research Gateway persists a
normalized evidence/receipt pair before Decision Desk sees it. The model has
no Provider URL, credential, raw payload, collection control, replay cursor or
generic query surface. Replay remains a Provider/Gateway simulation API for
evaluation work; it is not yet an Agent-facing stream Tool or a Scout grant.
The Plugin must not receive a Robinhood mutation credential, a Kernel mutation
path, or a Delegation grant.

Each collection result must preserve:

- the configured coverage (for example, SPX or SPY), collection-policy revision,
  source/provider revision, and request identity;
- exact observation and retrieval times, market timezone/session, response
  checksum, schema revision, and raw immutable BlobRef;
- an explicit collection gap/failure record when a scheduled observation was
  missed, stale, malformed, or rate-limited; and
- typed normalized GEX/option-surface facts and versioned derived metrics whose
  complete input Snapshot remains reconstructable.

The initial two-week history is observational evidence, not a sufficient
backtest, performance claim, or authorization for an options strategy. A later
strategy may consume only point-in-time snapshots that existed at its decision
time; it must surface missing coverage and cannot silently substitute a newer
snapshot. Any later options execution still requires its own separately frozen
Kernel/Provider product-capability track, liquidity and order-lifecycle
evidence, Shadow acceptance, and activation gates.

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
