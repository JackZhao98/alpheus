# K0 Database-Authoritative Live Canary Certification

Status: **LANDED** in `d24b8b9`; exact target-database bootstrap and read-only
deployment certified on 2026-07-18.

K0 is a non-money control-plane repair. Its deployment CLI does not construct a
Provider or submit, query, or cancel a broker order. The later target deployment
certification restarted the Kernel only in `read_only` and used authenticated
read-only account/state queries; it performed no Robinhood mutation.

## Authority model

- `live_canary_revision.authority_version=1` distinguishes K0 authority from
  pre-K0 descriptive rows.
- The latest authoritative immutable row is active; its `id` is also the
  monotonic generation. No generic KV table, separate head service, cache,
  watcher or Config Service exists.
- Database triggers reject update/delete of an authoritative row and reject
  promotion of a legacy row in place.
- A `live_canary_revision_recorded` event written atomically with the revision
  and found by its matching `payload.revision_id`, valid typed fields, supported
  authority version and non-future database market day are required on every
  authority read. K0 does not claim full event-payload equivalence validation.
- Migration 0010 adds a nullable `trade_grant.live_canary_revision_id` foreign
  key. Legacy grants remain readable but make new Live admission fail closed;
  both new-grant paths bind the exact active revision.
- Activation and new Live grant admission serialize on the stable Live-ledger
  PostgreSQL advisory lock. Provider mutation/send calls remain outside the
  transaction; authenticated Provider fact reads may occur during admission.

## Governance surface

There is one deployment-only command and no HTTP mutation route:

```text
kernel canary-policy \
  --expected-revision=0 \
  --daily-risk-cap-usd=50 \
  --clean-days-before-raise=5 \
  --recorded-by=deploy:<subject> \
  --reason='initial one-position canary'
```

The command does not load broker credentials, construct a Provider or read
`limits.yaml`. It derives effective market day from the database clock. Exact-
value retries are idempotent; a different candidate requires the current
revision ID and stale candidates conflict.

K0 accepts initial bootstrap and tightening only. `cap increase OR clean_days
decrease` is widening, including mixed-direction changes, and every widening is
denied. The removed implementation counted `day_open`, which is not proof of a
final broker-reconciled completed day. K1 owns the typed completed-day
attestation required before widening can exist.

## Runtime behavior

- Live startup requires a valid database authority before watchdog,
  reconciler, repricer or HTTP workers start.
- Sim, Shadow and read-only startup do not require one; their `/limits`
  projection reports missing/invalid status without treating it as Live
  permission.
- Live proposal and Class-C approval read authority in the same transaction
  that calculates usage and writes grant/event evidence.
- `/limits` separates `build_pinned_kernel_limits` from `db_live_canary`.
  `/state` exposes the same database revision/generation.
- Missing, invalid or future authority during a running Live admission returns
  service unavailable before operation entitlement, reservation, attempt,
  order or broker effect.
- `limits.yaml` contains no Live canary field; editing or restarting with a
  legacy key cannot alter runtime canary authority.

## Acceptance evidence

The following passed against a fresh isolated PostgreSQL 16 container:

- fresh 0001→0010 migration;
- real 0009→0010 upgrade with preserved legacy revision/grant data and no
  implicit authority promotion;
- update, delete and in-place legacy-promotion rejection;
- exact bootstrap idempotency and stale expected-revision conflict;
- 20 simultaneous identical bootstraps producing one revision/event/generation;
- full two-dimensional tighten/widen classification and fail-closed widening;
- activation racing Live admission, proving a grant and its event bind wholly
  to the old revision before the new revision becomes active;
- immutable old grant binding across later tightening;
- missing audit event and future market-day rejection;
- bound versus legacy/unbound grant usage and exclusion semantics; and
- the complete Store race suite:

```text
go test -race -count=1 -timeout=180s ./internal/store
ok alpheus/kernel/internal/store 5.648s
```

Repository-wide checks also passed:

```text
kernel:        go test -race ./...
kernel:        go vet ./...
agent-runtime: go test -race ./...
agent-runtime: go vet ./...
```

The isolated test container was removed after code certification. At that
point, the existing production/read-only stack had not been joined, modified or
restarted.

## Target deployment certification

Under explicit owner authorization on 2026-07-18, the already tested deployment
sequence ran against the exact target database:

- pre-migration state was schema version 7 with 92 operations, 217 events, zero
  execution attempts/orders/fills, zero open/close reservations, zero Live
  exposure, zero unknown attempts, and `halted=false`;
- a PostgreSQL custom-format backup was created and its format verified before
  migration;
- `/kernel canary-policy --expected-revision=0` migrated the database through
  version 10 and activated authority revision/generation `1/1`, authority
  version 1, with a `$50` daily authorized-risk cap and five clean days;
- the 92 operations were preserved and exactly one
  `live_canary_revision_recorded` event bound revision/generation `1/1`;
- the current Kernel image `sha256:2d3b80abc17c...` replaced the old container
  with `TRADING_MODE=read_only`, `LIVE_TRADING_ENABLED=false`, and
  `BROKER=robinhood`; it reached healthy state;
- authenticated `/limits` and `/state` reported the same active database
  authority, mode `read_only`, account buying power/equity `$401.16`, no
  positions, no open orders, zero current-day trades/open risk, and no Halt;
  and
- post-startup database state remained zero attempts, orders, fills,
  reservations, Live exposure, current-day Live grants, authoritative-bound
  Live grants, and unknown attempts. The only additional event was the expected
  `kernel_start`.

Five historical Live grants remain from the pre-K0 test history. They are not
current-day grants and none is bound to K0 authority; the canary admission code
must continue to treat legacy/unbound usage according to its fail-closed rules.
No proposal or real broker effect was created by this certification.

## Remaining M11 gate

K0 authorizes no trade. The target database and read-only deployment step is
complete. M11 now remains in progress only for the separately confirmed exact
one-share equity canary ticket and its Live startup, Halt, observation,
reconciliation/cancel-or-fill, clean-accounting, and return-to-read-only
sequence. Option mutations remain structurally disabled.
