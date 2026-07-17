# M11 Robinhood provider gap

Status: **NO SHIP** (verified 2026-07-17)

M11 requires a provider-supported implementation of
`FindOrderByClientID(client_order_id)` before any Robinhood mutation adapter can
be wired into live mode. The current MCP surface does not provide that recovery
primitive.

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
- Any mutation response failure is `provider mutation outcome unknown`; it is
  never automatically retried.
- No real-money deduplication experiment is permitted.

## Provider capability that unblocks M11

One documented, schema-stable option is sufficient:

1. `get_*_orders(ref_id=...)` with `ref_id` echoed in each result; or
2. a dedicated `get_order_by_ref_id` tool; or
3. a provider-supported sandbox in which idempotency and recovery can be
   proven without real-money effects, plus a production lookup with the same
   stable identity.

Documentation that only promises placement deduplication does not solve crash
recovery: after a request may have reached the broker and its response is lost,
Alpheus must first discover whether that exact intent exists before it can make
another mutation.
