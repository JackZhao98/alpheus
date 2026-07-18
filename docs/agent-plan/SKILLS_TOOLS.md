# Skills, Tools, and Capability Registry

> Status: **FROZEN ARCHITECTURE — file-backed Skill support, progressive
> disclosure, Tool Gateway, capability discovery, and least-privilege injection
> are authoritative. Exact metadata schemas, taxonomy, limits, and
> implementation acceptance are not yet specified or authorized.**

## Definitions

- **Skill:** a versioned method package describing how to perform a class of
  work, including applicability, procedure, context, required capabilities,
  output Contract, and acceptance behavior.
- **Tool:** a typed capability that reads, computes, writes, or submits intent
  through an owned Provider/connector.
- **Capability:** the provider-neutral problem-solving ability exposed by one or
  more Skills or Tools.
- **Playbook:** versioned trading knowledge and strategy, not an operational
  Skill and not a Tool permission.

Skills explain how to work; Tools provide executable capabilities; neither can
grant itself authority.

## File-backed Skill contract

Alpheus must support installed Skill directories with a `SKILL.md` entrypoint,
description metadata, and optional referenced resources:

```text
skill-name/
  SKILL.md
  references/
  scripts/
  templates/
```

The final metadata schema remains to be frozen, but the registry must know the
Skill id/name, human-readable description, revision, applicability, input and
output Contracts, required and optional capabilities, risk/side-effect class,
dependencies, publisher/trust state, and lifecycle state.

`description` exists for discovery. `SKILL.md` contains the authoritative
working instructions. A Skill is not correctly used when the model saw only a
search snippet or description.

## Progressive Skill disclosure

```text
IntentDraft
  -> search installed Skill metadata
  -> select one primary and any supporting Skills
  -> pin exact revisions
  -> read each selected SKILL.md completely
  -> read required or conditionally relevant resources completely
  -> plan and execute the Task
```

The Intent Interpreter sees only a compact Skill catalog. The Planner and
Worker responsible for applying a selected Skill must receive the complete
entrypoint. `SKILL.md` should remain bounded and route detailed material to
references instead of becoming an encyclopedia.

Required references are always read completely. Conditional references are
read completely when their documented condition applies. Unrelated resources
remain unloaded. Scripts and templates are invoked only through their reviewed
Skill contract and execution boundary.

If the user explicitly names a Skill, the Resolver pins the requested active
revision or reports that it is unavailable/not permitted. It cannot silently
substitute a different Skill. Without an explicit Skill, selection uses the
Intent, catalog descriptions, applicability, and capability coverage.

## Skill composition

One primary Skill owns the overall procedure and output Contract. Supporting
Skills contribute bounded subtasks. Their instruction precedence is:

```text
system and safety policy
  > explicit user scope
  > primary Skill
  > supporting Skill
  > memory and external evidence
```

A conflict that cannot be resolved by this order stops planning or asks the
user; it is not decided by whichever Skill text appears last.

Agents may propose Candidate Skill revisions but cannot install, activate,
edit, or supersede an active Skill. Promotion follows a separate reviewed and
versioned workflow.

Candidate author, independent `CapabilityValidator`, activation-decision owner,
and fenced `CapabilityActivator` are separate write authorities. The Activator
may only CAS an `ActiveCapabilityHead` from the exact validated revision and
authorization digests. Workers and Tool Providers have read-only access to that
head and never hold activation credentials. Initial or external-effect
capabilities require explicit owner approval; a policy may later preauthorize
only an independently validated read-only/equal-or-narrower revision.

## Skill injection and read receipt

Selected Skill instructions occupy a dedicated context section, separate from
user input, external evidence, and memory. Bind the Skill id, revision, content
checksum, resources read, and read time to the Session manifest. Recovery and
compact reload the pinned entrypoint rather than relying on a generated
summary.

## Tool Registry

Each Tool registration must identify at least:

- stable id, revision, description, provider, and capability mappings;
- input and output schemas;
- read, Agent-plane write, external write, money-intent, or privileged side
  effect class;
- required user, Agent, Skill, Run, and deployment scopes;
- credential owner and network boundary;
- timeout, retry, idempotency, and unknown-effect behavior;
- freshness, source/provenance, result-size, cost, and rate-limit contract;
- known limitations, prerequisites, alternatives, and complementary
  capabilities;
- health, deprecation, and validation state.

Tool descriptions state what problem the Tool solves and where it fails. They
must not be promotional prose or require the Planner to infer capability from a
vendor-specific name.

## Tool discovery and injection

Workers do not receive every Tool schema. The Control Plane preloads schemas for
the selected Skill's permitted required Tools. Optional discovery uses bounded
registry operations equivalent to:

```text
search_tools(query, capability, task scope)
describe_tool(tool id, revision)
```

Search returns a diverse, permission-filtered candidate set across relevant
capability clusters, not only the most semantically similar names. After a Tool
is selected, policy validates it and the next model turn receives its complete
call schema and, where necessary, its reviewed usage manual. Implementation
details, credentials, and secrets never enter model context.

The effective Tool set is the intersection of:

```text
Agent maximum permission
  ∩ selected Skill declaration
  ∩ EffectiveRunAuthority (authenticated user or registered owner policy)
  ∩ Run mode and budget
  ∩ deployment policy
  ∩ current health and safety policy
```

For scheduled/event work, `EffectiveRunAuthority` is derived from the immutable
RunOrigin occurrence, authenticated workload identity, and current owner policy;
it is never a cached or fabricated interactive user token.

A Skill can request a Tool but cannot grant it. Research-only work cannot gain a
mutation Tool through prompt text. Agent Workers never receive a Robinhood
mutation capability; they may submit typed operation intent only through the
Kernel API.

## Tool Gateway

All invocations pass through a deterministic Gateway that checks identity,
scope, budget, schema, side-effect policy, and current Tool revision before
calling the Provider. It normalizes and bounds the result, persists provenance
and effect state, and returns a reference to the Worker.

Tool retries follow the registered effect contract. An uncertain external
write cannot be treated as failure or blindly retried. Kernel/broker effects
remain governed entirely by the frozen Kernel attempt and reconciliation model.

## Capability Registry

The Planner needs a complete view of what Alpheus can do without loading every
manual. The Registry therefore generates a compact `CapabilityManifest`
covering all active capabilities, Skills, and Tools together with availability,
scope, cost/latency class, freshness, principal limitations, alternatives, and
complements.

Capability search is provider-neutral and combines a reviewed taxonomy,
synonyms, structured filters, and semantic/lexical retrieval. A new Tool or
Skill is not Active until it appears correctly in the generated manifest and
passes discovery tests. The manifest is generated from registrations; it is
not a second hand-maintained inventory.

## Mandatory source-level registration guide

The implementation package that owns capability registration must begin with a
complete developer/AI guide, preferably a package document such as
`capabilities/doc.go`. It must explain:

- Capability, Skill, Tool, and Playbook boundaries;
- when to register a new capability versus another Provider;
- every registration field and the required description style;
- taxonomy/synonym selection and capability composition;
- required/optional, alternative/complementary, effect, permission, freshness,
  cost, failure, versioning, and deprecation semantics;
- a minimal correct registration and common invalid examples;
- the commands/tests required to regenerate and validate the manifest.

This guide is an implementation requirement, not optional commentary. AI and
human contributors should be able to read it before changing the registry.

Documentation is backed by validators. Missing descriptions or capability
mappings, undeclared effects, unknown Contracts/dependencies, conflicting ids
or revisions, unauthorized capability claims, broken Skill Tool references,
and active registrations absent from the manifest must fail tests or startup as
appropriate. Correctness cannot depend on a contributor remembering the guide.

## Utilization and coverage audit

Record which capabilities were considered, selected, skipped, invoked,
successful, redundant, unavailable, or materially useful. Audit for installed
capabilities that are never retrieved, often retrieved but never selected,
frequently called without incremental evidence, duplicated by alternatives, or
persistently unhealthy.

Low usage alone is not failure; rare capabilities may be valuable. The goal is
that relevant capability is discoverable and considered, not that every Tool is
forced into every Run.

## Required later specification

Before implementation, freeze the Skill metadata and trust model, resource
read protocol, capability taxonomy and manifest schema, Tool effect classes,
Gateway and dynamic-binding protocol, installation/promotion lifecycle,
registration validators, and discovery/utilization acceptance suite.
