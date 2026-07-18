# M9 pre-live certification

Certified on 2026-07-17 and recertified on 2026-07-18 at `d2605b9` with
FakeBroker and an isolated PostgreSQL 16 Compose project. The market-day clock
repair is `66b0281`; `d2605b9` changes only the PostgreSQL rollback-proof query.
The production Robinhood deployment remained `read_only` and was not joined or
restarted by either certification run.

Run the complete gate from the repository root:

```bash
./scripts/certify-m9.sh
```

The script owns the disposable `alpheus-m9-cert` project, destroys its volumes
between destructive groups, and removes it on exit. A successful run ends with
`M9 CERTIFICATION PASS`.

## Evidence matrix

| Certification item | Executable evidence |
|---|---|
| DB paused mid-propose | `certify-m9.sh` pauses the PostgreSQL process; the 2026-07-18 recertification returned 503 in 3.009385 seconds and order count was unchanged |
| DB loss after attempt claim / broker acceptance | `TestM9BrokerAcceptanceSurvivesDatabaseFailureBeforeResolution` injects the failed resolution write, then requires client-id reconciliation and exactly one provider order |
| Broker timeout | `TestBrokerTimeoutLeavesAttemptUnknownAndReservationHeld` proves ambiguity stays held; verified Fake dedupe recovery is covered by the M2.8 crash-window suite |
| Broker accepted but response was lost | `TestReconcilerHandlesThreeCrashWindows/accepted` requires `FindOrderByClientID` to recover the one accepted order |
| Kernel death during execution | `TestClaimStealFencesLateWorkerAndBrokerDedupes` deterministically stops the first worker inside the broker call, steals the expired claim as a restarted worker, and fences the late completion; `TestReconcilerHandlesThreeCrashWindows` covers durable pending/claimed/accepted restart states |
| Crash between reprice cancel and replacement | `TestM5BRecoveryFinishesConfirmedCancelWithoutDuplicateSlot` and the pending-cancel/replacement recovery tests require one cancel, one replacement, and the original grant |
| Clock skew / market-day boundary | `TestMarketDayWindowUsesNewYorkBoundaries` covers winter/summer cutovers and 23/25-hour DST days; process/DB disagreement, provider reads, RecentFills, live/shadow split, proposal staging and approval staging all fail closed on a crossed market day; PostgreSQL M3C cases reject noncanonical date/TZ/DST windows before writes and preserve one authoritative observation timestamp |
| Breaker day ownership | Fixed-clock tests prove same-day resume expiry and reject a stale prior-day `daily_loss` resume without an override; PostgreSQL asserts breaker, override, daily-PnL and canonical event timestamps use the same database observation |
| Cross-midnight transaction rollback | Open proposal and approved-review callbacks sample the database clock as their final statement; PostgreSQL rollback probes stage operation, event, grant, reservation, attempt and order, then prove an error leaves all six absent |
| PostgreSQL process replacement | `certify-m9.sh` kills and replaces the DB process; the existing kernel recovers without restart and accepts a recorded operation |
| Daily-trade barriers | `audit/repro/i4_barrier.go` on fresh live and shadow ledgers: each produced exactly 1 Class-B and 19 Class-C results at one remaining slot |
| Open-risk and buying-power barriers | `TestM9CounterBarriersSerializeOpenRiskAndBuyingPower`: 20 same-process requests produce exactly one grant at each remaining capacity boundary |
| Close-reservation barrier | `TestDelayedCloseReservationBlocksNineteenFollowers`: one reservation wins and nineteen followers cannot reuse quantity |
| Deterministic advisory lock | PostgreSQL `TestStableLedgerLockSerializesLiveAcrossConnectionsPostgres` directly holds the stable live-ledger lock, proves a second connection cannot bypass it, and confirms shadow remains independent |
| Full-day idempotent replay | `TestM9FullTradingDayReplayIsIdempotent`: six unique daily inputs replay to the original operation ids with six operations/grants/reservations/attempts/orders and no duplicates |
| Risk coverage target | `internal/risk` statement coverage: **96.6%** (target: at least 90%) |
| Reconciliation target | Final isolated black-box probe: unresolved `unknown` attempts **0**, unsafe orphan reservations **0** |

The kernel SIGKILL timing is represented by deterministic blocking/fencing
fault seams rather than a random signal race. FakeBroker is in-process, so a
Docker SIGKILL would destroy both the kernel and the simulated venue and would
not model an external broker retaining an accepted order. The deterministic
tests preserve the external venue while killing the worker's ability to finish,
which exercises the actual reconciliation invariant.
