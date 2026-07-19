# Agent Platform Implementation Status

> This file tracks implementation against the frozen architecture. It records
> progress; it does not change a stage gate or authorize an effect.

## Current boundary

- The frozen Lean v1 architecture remains authoritative.
- Only non-money AP0 implementation is authorized.
- AP1 and later stages remain closed.
- The Kernel, Provider, Runtime behavior, operation path, GRACE, Delegation,
  Live mode, and UI were not changed by AP0-1.
- `./scripts/certify-agent.sh ap0` intentionally exits non-zero until every AP0
  mandatory probe exists. A green AP0-1 package test is not AP0 acceptance.

## AP0 work packets

| Packet | Status | Scope |
|---|---|---|
| AP0-1 common identity and release-verification foundation | Code complete at `a7fafa2`; certification correction at `775f176` | Versioned canonical JSON/digests, common identity and authority-bearing Go contracts, fail-closed RunOrigin/recovery lineage, EffectiveRunAuthority freshness, idempotency replay comparison, digest-bound release manifest verifier and CLI, golden/race tests, certification entrypoint scaffold |
| AP0-2 common Schema Freeze Pack | Next | Machine-readable manifest, JSON Schema, canonicalization profile, valid/invalid goldens, compatibility declaration, contract validation command; must match AP0-1 before any handler or migration work |
| AP0-3 service security and durable delivery scaffold | Pending | Credential-isolated service profiles, secret loading, per-owner database roles, outbox/inbox contracts, quarantine and replay evidence; no shared writer credential |
| AP0-4 BlobRef and bounded local BlobStore | Pending | Staging/commit/read verification, size/media/digest bounds, ACL/retention audit, orphan cleanup, unauthorized/mismatched blob probes |
| AP0-5 platform/effect governance registry | Pending | Immutable mode/effect/kill-switch revisions and heads, activation receipts, compare-and-swap and fail-closed resolution |
| AP0-6 integration and AP0 acceptance | Pending | Migration compatibility proof, complete threat probes, machine-readable certification artifacts, reviewed AP0 release manifest and exact digest verification |

AP0 is complete only when all six packets pass the frozen AP0 acceptance
criteria. These packets are implementation-sized units, not new architecture
milestones and not independent authorization gates.

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

AP0-1 currently passes:

```text
gofmt
go vet ./...
go test -race ./...
```

The partial stage command also runs those checks and retains JSON/JUnit
artifacts, then returns `FAIL mandatory-ap0-probes-not-implemented` by design.
It may return AP0 `PASS` only after AP0-2 through AP0-6 land and all mandatory
probes execute.
