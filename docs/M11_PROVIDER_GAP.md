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

- Robinhood execution is constructed only in explicit `live` mode after all
  existing account, secret, canary and `LIVE_TRADING_ENABLED` gates pass.
  `read_only` and `shadow` construct no execution provider. The currently
  deployed stack remains `read_only`.
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
- Canary limit revisions have an immutable database audit trail. Tightening is
  immediate; widening requires the greater of the old/new clean-day thresholds,
  that many completed live-ledger days, no PnL-divergence event on those days,
  and zero currently unresolved `unknown` attempts. Concurrent identical
  startup revisions collapse to one row under a transaction advisory lock.
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

## Remaining gates for M11

### v1.8 canary policy authority

Migration 0008 and `RecordLiveCanaryRevision` currently provide an immutable
audit ledger, but the production gate still reads `s.limits.LiveCanary` and no
production path records or activates the database revision. The ledger is not
yet authority. In addition, lowering `clean_days_before_raise` without raising
the cap is currently classified as an ordinary policy change even though it
widens when a future cap increase becomes eligible.

Before the separately confirmed one-share canary, plan v1.8 K0 requires the
database canary revision/head to drive startup, gate decisions, state and
events; missing/invalid authority fails Live closed with no YAML fallback.
`cap increase OR clean-days decrease` is widening. Editing `limits.yaml` and
restarting after activation must not change the effective canary. The broader
Kernel policy migration remains a separate post-M11/pre-AP0 module so the first
canary is not coupled to an unnecessary configuration rewrite.

### v1.7 recovery hardening found by cross-module review

The verified upstream `ref_id` behavior remains sufficient for duplicate-effect
containment, but the local implementation is not yet the complete recovery
contract:

- the first send persists one fixed candidate window, while replay currently
  advances only `replay_count` without proving it is still inside that window;
  a delayed replay can therefore create an order that exact recovery can never
  discover;
- the account gate prevents a second Provider mutation but currently allows new
  Live operations, grants, reservations and pending attempts to be staged while
  the account is active or unknown;
- the in-memory Halt check is not serialized with every background send path,
  so a recovered pending attempt, replacement or replay can cross a committed
  Halt; and
- changing the deployment to `read_only` removes the execution adapter and the
  Admin adoption route, so it cannot be the first response to an unresolved
  attempt.

Plan v1.7 makes the persisted original `send_window_end` the immutable replay
deadline, with an atomic database-time check, and requires a transactional pre-
entitlement Live admission check. It also serializes the database-backed Halt
cut with every Live open send authorization. It deliberately reuses the
existing account latch, Halt, exact matcher and two-step adoption. No second
send window, configurable replay TTL, `recovery_only` mode, retry framework or
automatic heuristic adoption is introduced.

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
passes. The v1.7 local recovery hardening above and its acceptance evidence must
land before a canary. M11 is not yet marked landed:

1. The first Alpheus-routed live canary remains a separate human-confirmed
   action. Its ticket must be exactly one share and remain within the immutable
   daily canary risk cap. Direct MCP evidence is not silently treated as that
   acceptance order.
2. The currently running Robinhood deployment remains `read_only`; do not
   change its mode or restart it as part of documentation work.
3. Option placement produced only unknown/zero-order negative evidence. The
   production constructor is equity-only; option placement and recovery remain
   closed pending separate evidence and certification.

No additional real-money probe is authorized by this implementation or by the
completion of the earlier bounded evidence ticket.

Verified placement deduplication prevents a second broker effect when the exact
same ref and intent are replayed, but it does not by itself solve accounting:
after a lost response Alpheus still needs a broker order ID before it can adopt
state and fills. Candidate matching is therefore bounded, exact, ambiguity-
intolerant, and human-gated unless exclusive-writer mode is in force.
