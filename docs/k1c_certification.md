# K1C Completed-Day Attestation and Canary Widening Certification

Status: **LANDED** in `d696010`; certified on 2026-07-19 against fresh,
isolated PostgreSQL 16 and FakeBroker Compose projects. The production stack
remained `read_only`; no production database, deployment, account or broker
mutation was performed.

Post-certification status: plan amendment v1.9.3 landed the separately
confirmed M11 Canary on 2026-07-20. Historical deferred-status statements below
describe the K1C certification boundary at the time and are not current status.
Amendment v1.9.4 subsequently reopened M11 execution acceptance; v1.9.5
recertified it. K1C itself remains landed.

## Durable completed-day evidence

Migration 0019 adds an immutable typed `live_canary_day_attestation`. A row
binds one exact account and market day to the active Live-canary revision and
Kernel-policy revision/generation/digest, day-open equity, actual nonzero Live
grants and authorized risk, local and Provider realized PnL, the tolerance used,
and the exact reconciled broker observation plus local-state generation.

The deployment-only `canary-attest-day` command derives that proof from durable
Kernel facts. It does not accept caller-supplied PnL and does not call the
Provider. Attestation fails closed unless:

- the market day is past and Provider PnL was persisted after the conservative
  20:00 America/New_York session boundary;
- local PnL still recomputes exactly and remains within the active typed
  policy's reconciliation tolerance;
- the day has real nonzero Live grants bound to the exact canary revision and
  every grant has terminal accepted order evidence;
- no pending, placed or unknown attempt, working order, divergence event or
  active Live execution gate remains; and
- the current complete broker observation is the reconciled head and still
  binds the current local-state generation.

`day_open`, a read-only day, or an ordinary event cannot manufacture this
evidence. Exact retries return the same immutable attestation; conflicting
same-account/day evidence is rejected.

## Guarded widening

Live-canary authority version 2 preserves legacy version-1 initial/tightening
rows while enabling a `widen` revision only inside the stable Live-ledger
transaction lock. Activation requires one account and exactly
`max(old_clean_days,new_clean_days)` recent eligible attestations bound to the
previous canary revision. It rejects skipped observed Live days, unresolved
unknown effects, PnL divergence, stale broker reconciliation, changed local
PnL or grants, and evidence outside the current Kernel-policy tolerance.

The widening revision records the required proof count and immutable ordered
attestation links. Initial bootstrap and tightening remain immediate. Twenty
simultaneous identical widening requests collapse to one revision/generation.

`/state` and the Cockpit expose the completed Live-canary days for the bound
account, including grant/risk, PnL and broker-generation evidence. They add no
policy mutation surface.

## Acceptance evidence

The PostgreSQL integration suite proves:

- complete-day attestation and exact retry idempotency;
- rejection of `day_open` plus final PnL without a canary execution;
- rejection of widening with an incomplete evidence window;
- exact account, canary and Kernel-policy bindings;
- one-generation behavior under a 20-request widening barrier; and
- update/delete rejection for attestations and widening evidence.

The complete pre-live certification was rerun from the repository root:

```bash
./scripts/certify-m9.sh
```

It passed repository tests, `go vet`, race tests, 96.8% risk coverage, fresh
PostgreSQL Store integration, live and shadow 20-request daily-counter
barriers, full Compose smoke, paused-database honesty and PostgreSQL process
replacement recovery. The run ended with `M9 CERTIFICATION PASS`; final
unknown effects and unsafe orphan reservations were both zero.

Cockpit acceptance also passed at 1280px and 390px against an isolated
FakeBroker stack. The completed-day section rendered correctly, neither
viewport had horizontal overflow, and the browser console had no errors.

## Result and next gate

K1 is complete. Together with landed B0 broker coexistence, the Kernel
prerequisites before AP0 are closed. This does not authorize AP0 by itself:
Lean v1 still requires owner review/freeze, the Charter closeout, and a
refreshed digest-pinned audit/release record. M11 remains `CANARY DEFERRED`,
production remains read-only, and the real canary remains a hard gate before
AP13 rather than before non-money Agent development.
