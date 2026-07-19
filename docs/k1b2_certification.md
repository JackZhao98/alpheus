# K1B-2 Execution Policy Envelope Certification

Status: **LANDED** in `bb07274`; certified on 2026-07-18 against a fresh,
isolated PostgreSQL 16 Compose project. Production remained `read_only`; no
production database, deployment, account or broker mutation was touched.

## Durable authority

Migration 0013 carries each bound operation's policy revision, generation and
digest into its trade grant, open/close reservation, execution attempt and
order. Attempts and orders additionally freeze their absolute authorization
deadline and typed execution envelope. Database constraints and triggers reject
partial bindings, downstream binding substitution, later envelope mutation,
bound-operation authority mutation and silent adoption of legacy rows.

Orders preserve the exact approved price bound. A later policy widening cannot
increase an old order's reprice count, interval authority, quote lifetime,
price movement or authorization lifetime. Current policy may only tighten
future risk-increasing work: effective max reprices and quote age take the
lower value, while the effective reprice interval takes the longer value.
Reprice finalization rechecks current authority under the shared policy lock so
a tightening racing a confirmed cancel cannot create a replacement.

Claim ownership now persists `lease_expires_at` from the database clock in the
same statement that records `claimed_at`. Recovery compares the stored lease
with the database clock; a worker's local timeout can define its next lease but
cannot steal another worker's unexpired lease.

Historical pre-K1 rows remain explicitly unbound and use the documented
compatibility path. New post-head rows cannot enter that path.

## Acceptance evidence

The PostgreSQL integration suite proves:

- exact binding propagation through all five downstream fact types;
- immutable operation, attempt and order authorization evidence;
- no old-order expansion after a later policy widening;
- no claim theft by an instance configured with a shorter timeout;
- successful reclaim only after persisted database-time lease expiry; and
- current max-reprice tightening suppresses a replacement after broker cancel
  and safely releases the remaining reservation.

The complete pre-live gate was rerun from the repository root:

```bash
./scripts/certify-m9.sh
```

It passed repository tests, `go vet`, the race suite, 96.6% risk coverage,
fresh PostgreSQL Store integration, live and shadow 20-request daily-counter
barriers, full Compose smoke, paused-database honesty and PostgreSQL process
replacement recovery. The run ended with `M9 CERTIFICATION PASS`; final
unknown effects and unsafe orphan reservations were both zero.

## Remaining K1 work

K1B is complete. K1 is not `LANDED`: K1C still owns typed completed-day
attestation and the guarded Live-canary widening path. K1B authorizes no trade
and does not change the deferred M11 canary gate.
