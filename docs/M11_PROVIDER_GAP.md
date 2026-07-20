# M11 Robinhood provider gap

Status: **EQUITY PROVIDER CONTRACT VERIFIED AND WIRED; DEPLOYMENT REMAINS READ-ONLY; OPTION MUTATIONS BLOCKED**
(provider lookup absent; equity placement dedupe and exact limit precision
verified under explicit owner authorization on 2026-07-17)

The frozen M11 baseline requires a provider-supported implementation of
`FindOrderByClientID(client_order_id)` before a Robinhood mutation adapter can
be wired into live mode. The current MCP surface does not provide that recovery
primitive. Plan amendment v1.5 permits a bounded equity-only alternative after
production dedupe evidence. That alternative is implemented and certified
against FakeBroker plus isolated PostgreSQL. Owner-authorized v1.6 evidence now
also establishes the exact equity limit quantity and price contract Alpheus
uses, so the equity-only execution adapter is wired behind the existing
`TRADING_MODE=live` and `LIVE_TRADING_ENABLED=true` startup gates. The deployed
Robinhood stack remains `read_only`; option placement remains unsupported.

## Verified facts

- `place_equity_order` and `place_option_order` accept a caller-supplied UUID
  `ref_id`. Their descriptions say the upstream deduplicates retries using it.
- Neither place response returns `ref_id`.
- `get_equity_orders` and `get_option_orders` can filter by broker `order_id`,
  but cannot filter by `ref_id`, and their order records do not return `ref_id`.
- Symbol, quantity, side, time, and `placed_agent` are not a unique recovery
  identity. Alpheus must not guess from those fields after a lost response.
- Exact-symbol `search` results expose the stable equity instrument UUID.
- For the equity limit shape Alpheus sends, quantity is positive whole shares;
  prices above $1 use a $0.01 tick and prices at or below $1 use a $0.0001 tick.
- A live `ListTools` discovery on 2026-07-17 returned 50 tools. The only tool
  not present in the committed 49-tool snapshot was
  `get_option_historicals`; the four order schemas below were unchanged.

## Owner-authorized production evidence

The owner explicitly authorized a bounded real-money equity experiment on the
bound account. It used $1 SPY regular-hours market orders so the maximum new
exposure was fixed and visible before each mutation:

1. A fresh UUID `ref_id` created one broker order in `queued` state.
2. Replaying the exact same normalized request with the exact same `ref_id`
   returned `provider mutation outcome unknown`. A subsequent narrow
   `get_equity_orders` query showed only the original broker order; no duplicate
   order was created.
3. Sending the same normalized intent with a different fresh `ref_id` created a
   second, distinct broker order in `queued` state.

This is positive production evidence that equity placement records/enforces
`ref_id` for deduplication. It is **not** evidence of client-id lookup: the
replay did not return the original broker order, and neither place nor read
responses exposed `ref_id`. The transport deliberately collapsed the replay's
raw provider error to an unknown outcome, so the exact duplicate-response code
is not yet known. No option-order conclusion may be inferred from this equity
probe.

The owner then authorized a bounded option follow-up: one SPY call, buy to
open, one contract, $0.10 GFD limit, after a successful provider review. The
fresh-ref placement and its one exact same-ref replay both returned
`provider mutation outcome unknown`; narrow `get_option_orders` pulls after
each call returned zero candidates. No option order or exposure was created.
This is not option dedupe evidence: the request may have been rejected before
placement, and the mutation transport erased the provider's structured error.
Before another option probe, Alpheus must retain a sanitized provider error
category/code for audit while continuing to classify the money-path outcome as
unknown and while never logging credentials or raw transport secrets.

The owner subsequently authorized a separate, bounded equity-limit precision
probe on Ford (`F`) after the exact ticket and live quote were reviewed:

1. A one-share buy limit at $13.50, GFD and regular-hours only, was accepted as
   one queued broker order. A canonical `get_equity_orders(order_id)` read
   matched the symbol, instrument UUID, side, quantity, limit and session. The
   order was immediately cancelled, its final state was `cancelled`, and its
   cumulative filled quantity remained zero.
2. The otherwise identical 0.5-share limit was definitively rejected before an
   order existed with provider HTTP 400 detail
   `Limit order quantity cannot include fractional shares.`
3. Read-only reviews established the price boundary: $1.001 was rejected
   because prices above $1 cannot use sub-penny increments; $0.5001 passed
   precision validation; and $0.50001 was rejected because prices cannot have
   more than four decimal places.
4. Exact-symbol `search` returned one `F` identity and its instrument UUID.
   Lookalike results are never accepted as identity evidence.

The accepted placement response reliably supplied the new broker order ID but
did not supply every canonical order field. The execution adapter therefore
uses that ID only to perform an immediate canonical order read, then validates
every persisted provider-visible field before resolving success. If the
canonical read is unavailable or drifts, the already-sent attempt becomes
`unknown`; it is never treated as not-sent or retried with a fresh ref. The
post-probe order pull showed no working `F` order and no fill.

Canonical input+output schema SHA-256:

| Tool | SHA-256 |
|---|---|
| `place_equity_order` | `96b75b9fd3ebb34040beada5eda31172d297ccfc577481185ada27c6ce407cde` |
| `place_option_order` | `95218621583ba851683a9a93bb9b8cf4a10b407488a1de6ddfcbdc94ae645691` |
| `get_equity_orders` | `337255fd23e466b740aa22090923ff162d51cf68d07293a84f43a7af769b84f1` |
| `get_option_orders` | `5959fbc62f85298f99450317817e52b0960d4f27b771f951e07324e9d80b6915` |

## Enforced production boundary

- Robinhood execution is constructed only in explicit `live` mode after the
  account, secret and `LIVE_TRADING_ENABLED` gates pass. A valid database
  canary authority is then required before watchdog, reconciler, repricer or
  HTTP workers start and before any effect admission. `read_only` and `shadow`
  construct no execution provider. The currently deployed stack remains
  `read_only`.
- The read client always rejects mutation tools. Live mode constructs a second,
  no-retry MCP session whose capability is restricted to four reviewed order
  mutation tools and the exact bound Agentic account.
- The separate mutation transport has a fixed four-tool allowlist, no response
  cache, SDK reconnect retries disabled, and exactly one `CallTool` invocation.
  Its constructor binds one account number, every call must match it exactly,
  and place calls must include a caller-supplied UUID `ref_id`.
- Mutation failures are classified before durable resolution: local validation,
  connection-before-call, and rate-wait failures are `not_sent`; MCP tool
  errors are sanitized `rejected`; transport/protocol/response ambiguity is
  `unknown`. Only a genuine post-send unknown engages recovery, and no transport
  layer automatically retries it.
- The execution adapter exposes a separate exact-candidate capability instead
  of pretending Robinhood implements client-id lookup. It revalidates exact
  instrument identity, multiplier, the variable equity tick schedule and
  whole-share quantity increment before mutation. A successful equity mutation
  is resolved only after a canonical order-id read validates every visible
  field. Only equity mutations are certified in the production constructor;
  options fail before a live grant and before mutation transport.
- The first live grant of each market day must use exactly one provider-reported
  quantity increment. Missing or inconsistent metadata fails before the grant;
  the certified equity canary increment is one share. Option canaries remain
  disabled despite their read-only instrument metadata.
- Authoritative canary limit revisions are immutable and audit-bound.
  Tightening is immediate; K0 classifies but denies every widening. K1 must add
  durable completed-day reconciliation attestations before any widening can be
  enabled. Concurrent identical CLI bootstraps collapse to one row under the
  stable Live-ledger transaction advisory lock.
- No further autonomous real-money deduplication experiment is permitted. The
  bounded probe above was initiated and confirmed by the human owner; future
  probes require equally explicit authority and a separately bounded ticket.

## Amended recovery contract (plan v1.5)

The verified equity behavior permits a bounded same-ref replay path, but not
heuristic ownership guessing:

1. Before the first provider call, persist the UUID `client_order_id`/`ref_id`,
   an exact canonical provider-intent fingerprint, the account binding, and the
   send-time window. The fingerprint includes instrument UUID (not only
   symbol), side, order type/trigger, quantity or dollar amount, limit/stop
   prices, time-in-force, market-hours session, and position effect when
   applicable.
2. Recovery may replay at most once and only with the same `ref_id` and a
   byte-for-byte equivalent canonical intent. A different `ref_id`, changed
   price, changed quantity, or coercion is a new order and is forbidden during
   recovery.
3. After an uncertain call or an uncertain same-ref replay, page
   `get_*_orders` over the persisted narrow time window and require exact
   equality on every provider-visible fingerprint field, the bound account,
   and `placed_agent=agentic`. Approximate time, price, symbol, or amount
   matching is never sufficient.
4. Exactly one matching broker order is a **candidate**, not automatically
   proof of ownership when another Agentic client can write the account. Zero
   or multiple candidates leave the attempt `unknown`, retain every grant and
   reservation, keep the account mutation latch engaged, and require human
   reconciliation.
5. Automatic adoption of the sole exact candidate is allowed only under an
   explicit audited exclusive-writer deployment mode, with one unresolved
   provider-visible fingerprint at a time and acceptance tests that inject
   external lookalike orders. Without that mode, a human must approve the
   candidate broker order ID before adoption. After adoption, canonical
   `GetOrder(order_id)` and fill reconciliation remain mandatory.
6. This path initially applies only to equity placement. Option placement and
   any automatic option recovery remain closed until the structured rejection
   reason is observable and option deduplication is separately evidenced.

### Unknown latch

A genuine post-send `unknown` sets a durable latch on the bound live account
before the worker may do anything else:

- Block every automatic provider mutation: new opens, closes, reprices,
  replacements, and fresh-ref retries. Read-only provider pulls continue.
- Retain the attempt's trade grant and every cash, risk, position, and close
  reservation. Uncertainty never frees capacity.
- The recovery loop immediately pulls the narrow order window and continues on
  its normal schedule. The account's overall order history may be non-empty;
  reconciliation counts only exact candidates for this uncertain intent. One
  pull with zero exact candidates does not prove absence because provider
  visibility may be eventually consistent, so the latch remains engaged.
- One exact candidate follows the candidate-ownership rule above. Multiple
  candidates remain ambiguous. No path generates a new `ref_id` while latched.
- Clear the latch only in the same database transaction that durably adopts a
  uniquely resolved broker order and its current fills, or after an explicitly
  audited human resolution. Timeout alone never clears it.
- An operator may still issue a separately authenticated emergency action only
  after a fresh broker-state pull; it is never an automatic recovery path and
  cannot reuse or silently release the uncertain attempt's reservations.

This makes uncertainty an account execution stop, not merely an error field on
one operation.

The pull-and-match step therefore reduces operational uncertainty without
pretending that non-unique order attributes are a client identity.

## Implemented evidence

- Code baseline: `0bf183a` (`feat: add fail-closed live recovery latch`).
- Migration 0009 adds the singleton live-account execution gate and durable
  provider account, canonical intent JSON, SHA-256 fingerprint, initial send
  timestamp/window, replay count, sanitized provider error code, and candidate
  broker order ID.
- The gate is transactionally acquired before every live provider mutation.
  Resolving an active attempt to `unknown` atomically moves the same ID into the
  unknown latch; resolving it to a proven state atomically clears the latch.
- The recovery worker pages equity orders with bound account,
  `placed_agent=agentic`, symbol, and lower time bound, then locally requires
  exact instrument UUID, side, share quantity, absence of dollar/stop amount,
  limit, order type, trigger, time in force, market-hours session, and the full
  persisted time window. Approximate matches are not candidates.
- Zero candidates retain the latch. A byte-identical equity replay is allowed
  at most once with the persisted ref; multiple candidates retain the latch;
  one candidate is only recorded for human review.
- Admin adoption is two-step in Cockpit and requires the attempt ID plus broker
  order ID. The kernel claims the latched attempt, re-pulls, requires the same
  sole exact candidate, then ingests canonical order/fill state and clears the
  latch in one database transaction.
- Offline acceptance covers exact-field lookalikes, a zero-candidate single
  replay limit, a multiple-candidate refusal, and human adoption. An isolated
  PostgreSQL race probe launched 20 simultaneous pending attempts: exactly one
  acquired the live gate; unknown blocked the other 19; a second replay was
  rejected; proven settlement cleared the gate. The full store race suite is
  green against migration 0009. A clean-volume isolated Compose build/start
  and the full FakeBroker smoke suite are also green; the test stack and volume
  were removed afterward.
- Candidate-adoption fault injection covers query failure, zero/multiple/
  changed candidates, canonical-order failure, provider state drift, and 20
  concurrent confirmation requests. Every failure retains the grant,
  reservation, and unknown latch; concurrent confirmation has exactly one
  winner. A separate PostgreSQL crash-window probe forces the adoption
  transaction to roll back after an unknown attempt is claimed, then proves a
  replacement worker can reclaim that same fenced attempt after lease expiry
  while the unknown latch remains engaged. The isolated database and volume
  were removed after the test. A positive-path PostgreSQL probe separately
  proves candidate adoption commits the attempt state, broker order identity,
  canonical fill, exposure conversion, reservation conversion, and latch clear
  together; no latch is cleared ahead of durable accounting.
- On 2026-07-17 the owner set the initial live canary controls to a $50 daily
  authorized-risk cap and five clean completed live-ledger days before any
  widening. An isolated fresh-volume deployment then started the production
  construction in `live` mode against the exact bound Robinhood account, with
  no published port, no agent runtime and no proposal. The kernel reached
  healthy state and emitted `kernel_start` with `mode=live` and
  `broker=robinhood`. The fresh database contained zero operations, trade
  grants, execution attempts, orders and fills. The isolated containers,
  network and volume were removed; the running production stack remained
  healthy and `read_only`.

## M11 gate status

### M11 Canary Stop and Recovery Acceptance — landed 2026-07-20

The owner separately confirmed both production tickets, and both mutations
were routed through the Alpheus Kernel rather than sent directly through the
Robinhood MCP.

The first ticket staged one SOFI share as a regular-hours GFD limit order. The
Kernel created operation `38bc4b8b-009c-4ebe-880c-9afd31166889`, attempt
`ba66fbfb-dc42-4621-ad39-0b2a6fd9c7d5`, and broker order
`6a5e708f-5e3e-4f49-bfce-6245793adda4`. The database-fenced Halt cut drove the
working order to a canonical cancelled terminal state with zero fills and no
position. Its `$17.12` grant remained consumed as required.

The second, independently confirmed ticket exercised a true regular-hours GFD
Market buy for one SOFI share with the remaining `$32.88` of the immutable
daily authority. Kernel operation `9edd30ad-f49b-405e-a809-78a00df08cf1`
created attempt `ad9f9ce7-24ac-4bb3-a64b-5cd8914581c8` and Provider `ref_id`
`78b7a8f2-fa49-4691-9c0e-04a2c65c17d3`. Robinhood created broker order
`6a5e783d-36a0-4869-942f-f817b10213f3` at
`2026-07-20T19:34:21.1426Z` and filled one share at `$17.09` at
`2026-07-20T19:34:21.292Z`, with fill id
`6a5e783d-7d1e-4f2f-bc54-2c54b10e85ef` and zero fees.

The initial Kernel response became `unknown` because Robinhood canonically
backfilled `price` on the filled Market order, while the adapter expected that
field to remain null. No retry or second order was sent. Global Halt committed,
the exact bounded pull produced one candidate, and the Admin recovery flow
adopted that exact broker order. Commit `2d1b66b` accepts a positive canonical
execution price for a filled Market order without changing the original Market
intent matcher. Commit `65492f1` supplies the guarded equity Market path, and
commit `23a1a13` supplies the database-fenced Halt resume used before the
separately confirmed effect.

Final acceptance evidence showed the operation `executed`, attempt `settled`,
one local order, one local fill, a converted reservation with zero remaining
quantity/risk/cash, `$17.09` Live open risk, no open broker order, an empty
`live_execution_gate`, and no control warning. The two irreversible daily
grants total exactly `$50`. The account intentionally retains one SOFI share at
an average `$17.09`; no closing trade was authorized. Global Halt remains
committed and the deployed Kernel returned to `read_only` with mutations
disabled. Exact order/fill origin is `alpheus`; aggregate position origin
remains conservatively `ambiguous` because the Provider position object lacks
an order/fill identity, which is an observability limitation rather than an
unreconciled money-path effect.

This satisfies the frozen one-share production Canary and v1.7 stop/recovery
acceptance against the post-K1/B0 Kernel. M11 is `LANDED`. This evidence does
not certify option mutation, automatic Robinhood replay, or Agent Live
activation, and does not itself activate AP13.

### v1.8.1 canary policy authority — landed

Commit `d24b8b9` lands K0. Migration 0010 distinguishes legacy descriptive rows
from authoritative rows, makes authoritative revisions append-only, and binds
new Live grants to the exact revision. A single deployment-only
`canary-policy` CLI performs explicit bootstrap/tightening with expected-
revision CAS and DB-derived market time. Live startup, both grant paths,
`/state`, `/limits`, grant/refusal events and cap enforcement now read the
database authority; `limits.yaml` has no canary field or fallback.

The 0009→0010 preservation test, 20-way bootstrap, activation/admission
linearization, immutable-row probes and full isolated PostgreSQL race suite are
green. K0 made no production/Robinhood Provider call and did not restart the
production stack. Review also proved that the former `day_open` query was not
final-day evidence, so K0 classifies but denies every widening. K1 must add a
durable completed-day reconciliation attestation before any cap increase or
clean-days decrease can be enabled.

### Exact target K0 bootstrap and read-only deployment — complete

Under explicit owner authorization on 2026-07-18, the target database was
backed up, migrated from version 7 through 10, and bootstrapped with exact
authority revision/generation `1/1`, `$50` daily authorized risk, and five clean
days. All 92 operations were preserved. The current Kernel image is healthy
with `TRADING_MODE=read_only`, `LIVE_TRADING_ENABLED=false`, and the Robinhood
Provider. Authenticated state showed `$401.16` buying power/equity, no positions
or open orders, and zero current-day risk/trades. Database verification showed
zero attempts, orders, fills, reservations, Live exposure, current-day Live
grants, authoritative-bound Live grants, and unknown attempts. No broker
mutation or proposal occurred.

### v1.7 recovery hardening found by cross-module review — landed

The verified upstream `ref_id` behavior was sufficient for duplicate-effect
containment, but cross-module review found that the local implementation was
not yet the complete recovery contract:

- the first send persisted one fixed candidate window, while replay advanced
  only `replay_count` without proving it was still inside that window;
  a delayed replay can therefore create an order that exact recovery can never
  discover;
- the account gate prevented a second Provider mutation but allowed new Live
  operations, grants, reservations and pending attempts to be staged while the
  account was active or unknown;
- the in-memory Halt check was not serialized with every background send path,
  so a recovered pending attempt, replacement or replay could cross a committed
  Halt; and
- changing the deployment to `read_only` removed the execution adapter and the
  Admin adoption route, so it could not be the first response to an unresolved
  attempt.

Plan v1.7 makes the persisted original `send_window_end` the immutable replay
deadline, with an atomic database-time check, and requires a transactional pre-
entitlement Live admission check. It also serializes the database-backed Halt
cut with every Live open send authorization. It deliberately reuses the
existing account latch, Halt, exact matcher and two-step adoption. No second
send window, configurable replay TTL, `recovery_only` mode, retry framework or
automatic heuristic adoption is introduced.

Implementation clarification v1.7.1 supersedes one over-strong implication in
that contract: authorizing a replay immediately before `send_window_end` does
not prove that Provider `created_at` will remain in the original match window.
The atomic replay predicate therefore also reserves a certified Provider
creation-latency guard inside the same window. FakeBroker can prove that bound;
the current Robinhood evidence proves same-`ref_id` dedupe but not server-side
creation latency, so production automatic replay remains disabled there. This
does not disable exact pulls, the durable unknown latch or Admin adoption, and
it introduces no second window or policy parameter.

Commit `0913010` lands the non-money v1.7.1 recovery implementation. It makes
the Halt cut database-authoritative, refuses new Live entitlements while the
account latch is active or unknown, atomically compares replay evidence while
consuming the single replay slot, and preserves `executed` whenever any fill is
durable. Fill-integrity failures now enter the same database-fenced Global Halt
path rather than relying on a process-local refresh. Robinhood automatic replay
remains disabled because the required creation-latency bound is still absent.

The canary completion artifact is named **M11 Canary Stop and Recovery
Acceptance**. It records real broker facts and returns the deployment to
`read_only` only after reconciliation is clean. It never claims that a fill can
be rolled back.

In the v1.5 contract, "read-only Provider pulls continue" means non-mutating
queries performed while the deployment remains `TRADING_MODE=live` and Halt is
committed. It does not mean switching the deployment to `read_only`, because
that mode intentionally constructs no execution adapter and disables adoption.
The v1.5 verified same-ref behavior supersedes only the frozen M11 requirements
for `FindOrderByClientID` and for zero live dedupe evidence; it does not waive
any other M11 gate.

A documented, schema-stable lookup remains the preferred long-term provider
improvement:

1. `get_*_orders(ref_id=...)` with `ref_id` echoed in each result; or
2. a dedicated `get_order_by_ref_id` tool; or
3. a provider-supported sandbox in which idempotency and recovery can be
   proven without real-money effects, plus a production lookup with the same
   stable identity.

The v1.5 bounded recovery design remains the correct identity model without
weakening it to loose field matching. The v1.6 evidence closes the equity-limit
metadata gate and commit `319f657` wires the equity-only adapter behind explicit
live startup controls. The isolated no-order live-mode startup certification
passes. The v1.7 local recovery hardening above and its acceptance evidence had
to land before a canary. Commit `0913010` satisfies that non-money gate and
commit `d24b8b9` satisfies K0 database authority. The separately confirmed
canary described above subsequently landed M11:

Plan amendment v1.9.1 records the historical deferral that allowed K1, B0 and
non-money AP0–AP12 work to proceed. Amendment v1.9.3 records completion against
the final applicable post-K1/B0 Kernel. Landing M11 removes only that specific
AP13 prerequisite; it does not activate Agent Live or waive any other gate.

1. The Alpheus-routed live canary was separately human-confirmed, used exactly
   one share per ticket, and remained within the active immutable $50/five-day
   authority. Direct MCP evidence was not treated as the acceptance order.
2. The running Robinhood deployment has returned to `read_only`; documentation
   work must not restart it or issue another mutation.
3. Option placement produced only unknown/zero-order negative evidence. The
   production constructor is equity-only; option placement and recovery remain
   closed pending separate evidence and certification.

No additional real-money probe is authorized by this acceptance record.

Verified placement deduplication prevents a second broker effect when the exact
same ref and intent are replayed, but it does not by itself solve accounting:
after a lost response Alpheus still needs a broker order ID before it can adopt
state and fills. Candidate matching is therefore bounded, exact, ambiguity-
intolerant, and human-gated unless exclusive-writer mode is in force.
