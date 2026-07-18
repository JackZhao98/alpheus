# M11 Robinhood provider gap

Status: **EQUITY RECOVERY IMPLEMENTED OFFLINE; LIVE DISABLED; OPTION MUTATIONS BLOCKED**
(provider lookup absent; equity placement dedupe verified under explicit owner
authorization on 2026-07-17)

The frozen M11 baseline requires a provider-supported implementation of
`FindOrderByClientID(client_order_id)` before a Robinhood mutation adapter can
be wired into live mode. The current MCP surface does not provide that recovery
primitive. Plan amendment v1.5 permits a bounded equity-only alternative after
production dedupe evidence. That alternative is implemented and certified
against FakeBroker plus isolated PostgreSQL, but live wiring remains closed
because no production-supported asset class currently satisfies every order
contract below.

## Verified facts

- `place_equity_order` and `place_option_order` accept a caller-supplied UUID
  `ref_id`. Their descriptions say the upstream deduplicates retries using it.
- Neither place response returns `ref_id`.
- `get_equity_orders` and `get_option_orders` can filter by broker `order_id`,
  but cannot filter by `ref_id`, and their order records do not return `ref_id`.
- Symbol, quantity, side, time, and `placed_agent` are not a unique recovery
  identity. Alpheus must not guess from those fields after a lost response.
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

Canonical input+output schema SHA-256:

| Tool | SHA-256 |
|---|---|
| `place_equity_order` | `96b75b9fd3ebb34040beada5eda31172d297ccfc577481185ada27c6ce407cde` |
| `place_option_order` | `95218621583ba851683a9a93bb9b8cf4a10b407488a1de6ddfcbdc94ae645691` |
| `get_equity_orders` | `337255fd23e466b740aa22090923ff162d51cf68d07293a84f43a7af769b84f1` |
| `get_option_orders` | `5959fbc62f85298f99450317817e52b0960d4f27b771f951e07324e9d80b6915` |

## Enforced behavior while blocked

- Robinhood production execution remains unavailable at startup.
- The read client rejects mutation tools.
- The separate mutation transport has a fixed four-tool allowlist, no response
  cache, SDK reconnect retries disabled, and exactly one `CallTool` invocation.
  Its constructor binds one account number, every call must match it exactly,
  and place calls must include a caller-supplied UUID `ref_id`.
- Mutation failures are classified before durable resolution: local validation,
  connection-before-call, and rate-wait failures are `not_sent`; MCP tool
  errors are sanitized `rejected`; transport/protocol/response ambiguity is
  `unknown`. Only a genuine post-send unknown engages recovery, and no transport
  layer automatically retries it.
- The dormant execution adapter exposes a separate exact-candidate capability
  instead of pretending Robinhood implements client-id lookup. It revalidates exact instrument identity,
  multiplier, price tick and quantity increment before mutation; requires an
  explicit option `position_effect`; validates the provider's order echo; and
  normalizes only reviewed order states and stable execution IDs/fills.
- The first live grant of each market day must use exactly one provider-reported
  quantity increment. Missing increment metadata fails before the grant; with
  today's provider facts, this permits a one-contract option canary and keeps
  equity live trading closed until its exact increment/tick contract exists.
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
  were removed after the test.

## Remaining gates for M11

A documented, schema-stable lookup remains the preferred long-term provider
improvement:

1. `get_*_orders(ref_id=...)` with `ref_id` echoed in each result; or
2. a dedicated `get_order_by_ref_id` tool; or
3. a provider-supported sandbox in which idempotency and recovery can be
   proven without real-money effects, plus a production lookup with the same
   stable identity.

The v1.5 bounded recovery component now satisfies the offline equity recovery
requirement without weakening it to loose field matching. Live remains a
no-ship for separate reasons:

1. Robinhood's current schemas do not document one exact quantity increment and
   price-tick rule for the equity limit order shape Alpheus would send. The live
   market-data adapter therefore rejects equity instruments before any grant.
2. Option placement produced only unknown/zero-order negative evidence; option
   mutation and recovery remain closed pending structured error visibility and
   separate dedupe proof.
3. Production construction remains deliberately unwired until one asset class
   satisfies its exact metadata contract and the full M11 deployment
   certification is rerun. No additional real-money probe is authorized by
   this implementation.

Verified placement deduplication prevents a second broker effect when the exact
same ref and intent are replayed, but it does not by itself solve accounting:
after a lost response Alpheus still needs a broker order ID before it can adopt
state and fills. Candidate matching is therefore bounded, exact, ambiguity-
intolerant, and human-gated unless exclusive-writer mode is in force.
