# M9 pre-live certification

Certified on 2026-07-17 with FakeBroker and an isolated PostgreSQL 16 Compose
project. The production Robinhood deployment remained `read_only` and was not
joined or restarted by the certification project.

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
| DB paused mid-propose | `certify-m9.sh` pauses the PostgreSQL process; the final operation returned 503 in 3.005701 seconds and order count was unchanged |
| DB loss after attempt claim / broker acceptance | `TestM9BrokerAcceptanceSurvivesDatabaseFailureBeforeResolution` injects the failed resolution write, then requires client-id reconciliation and exactly one provider order |
| Broker timeout | `TestBrokerTimeoutLeavesAttemptUnknownAndReservationHeld` proves ambiguity stays held; verified Fake dedupe recovery is covered by the M2.8 crash-window suite |
| Broker accepted but response was lost | `TestReconcilerHandlesThreeCrashWindows/accepted` requires `FindOrderByClientID` to recover the one accepted order |
| Kernel death during execution | `TestClaimStealFencesLateWorkerAndBrokerDedupes` deterministically stops the first worker inside the broker call, steals the expired claim as a restarted worker, and fences the late completion; `TestReconcilerHandlesThreeCrashWindows` covers durable pending/claimed/accepted restart states |
| Crash between reprice cancel and replacement | `TestM5BRecoveryFinishesConfirmedCancelWithoutDuplicateSlot` and the pending-cancel/replacement recovery tests require one cancel, one replacement, and the original grant |
| Clock skew / market-day boundary | `TestMarketDayWindowUsesNewYorkBoundaries` plus PostgreSQL `TestStableLedgerLockSpansMarketDaysPostgres` prove market-time assignment while retaining one stable per-ledger lock |
| PostgreSQL process replacement | `certify-m9.sh` kills and replaces the DB process; the existing kernel recovers without restart and accepts a recorded operation |
| Daily-trade barriers | `audit/repro/i4_barrier.go` on fresh live and shadow ledgers: each produced exactly 1 Class-B and 19 Class-C results at one remaining slot |
| Open-risk and buying-power barriers | `TestM9CounterBarriersSerializeOpenRiskAndBuyingPower`: 20 same-process requests produce exactly one grant at each remaining capacity boundary |
| Close-reservation barrier | `TestDelayedCloseReservationBlocksNineteenFollowers`: one reservation wins and nineteen followers cannot reuse quantity |
| Deterministic advisory lock | PostgreSQL `TestStableLedgerLockSpansMarketDaysPostgres` directly holds the stable ledger lock and proves another market day cannot bypass it |
| Full-day idempotent replay | `TestM9FullTradingDayReplayIsIdempotent`: six unique daily inputs replay to the original operation ids with six operations/grants/reservations/attempts/orders and no duplicates |
| Risk coverage target | `internal/risk` statement coverage: **96.6%** (target: at least 90%) |
| Reconciliation target | Final isolated black-box probe: unresolved `unknown` attempts **0**, unsafe orphan reservations **0** |

The kernel SIGKILL timing is represented by deterministic blocking/fencing
fault seams rather than a random signal race. FakeBroker is in-process, so a
Docker SIGKILL would destroy both the kernel and the simulated venue and would
not model an external broker retaining an accepted order. The deterministic
tests preserve the external venue while killing the worker's ability to finish,
which exercises the actual reconciliation invariant.
