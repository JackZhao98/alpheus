#!/usr/bin/env bash
# Day-1 smoke test: exercise all three approval paths by hand.
# Usage: ./scripts/smoke.sh   (kernel must be up on :8100)
set -e
K=${KERNEL_URL:-http://localhost:8100}
ORIGIN=${CONSOLE_ORIGIN:-http://localhost:8100}
PLAN='{"stop":"-30%","invalidation":"thesis dead","time_stop":"15:45 ET","target":"+50%"}'
SMOKE_DB_CHECK=${SMOKE_DB_CHECK:-1}
SMOKE_DB_NAME=${SMOKE_DB_NAME:-alpheus}

sql_scalar() {
  docker compose exec -T db psql -U alpheus -d "$SMOKE_DB_NAME" -Atqc "$1"
}

if [ "$SMOKE_DB_CHECK" = "1" ]; then
  shadow_orders_before=$(sql_scalar "select count(*) from orders where ledger='shadow'")
  shadow_fills_before=$(sql_scalar "select count(*) from fills where ledger='shadow'")
  live_orders_before=$(sql_scalar "select count(*) from orders where ledger='live'")
  live_fills_before=$(sql_scalar "select count(*) from fills where ledger='live'")
fi

quote() {
  curl -s -X POST "$K/sim/quote" -H 'Content-Type: application/json' -H "Origin: $ORIGIN" -d "$1"
  echo
}

echo "== limits =="
curl -s $K/limits | head -c 400; echo; echo

echo "== state (expect day.live + day.shadow) =="
curl -s $K/state; echo; echo

echo "== seed exact option quote: 1 contract costs 35.00 =="
quote '{"symbol":"SPY","bid":0.34,"ask":0.35,"open_interest":45000}'

echo "== 1) compliant shadow open -> expect auto_approved (Class B) =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":35,"shadow":true,"thesis":"smoke","setup":"smoke","plan":'"$PLAN"'}'; echo; echo

echo "== move option quote: 1 contract costs 200.00 =="
quote '{"symbol":"SPY","bid":1.99,"ask":2.00,"open_interest":45000}'

echo "== 2) over-budget open -> expect pending_review (Class C) =="
class_c_response=$(curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":200,"shadow":true,"plan":'"$PLAN"'}')
echo "$class_c_response"; echo
class_c_id=$(printf '%s' "$class_c_response" | sed -E 's/.*"operation_id":"([^"]+)".*/\1/')
test -n "$class_c_id"

echo "== 2b) approve Class C -> expect one atomic entitlement and execution =="
curl -s -X POST "$K/operations/$class_c_id/review" -H 'Content-Type: application/json' -H "Origin: $ORIGIN" -d '{"verdict":"approved","rationale":"smoke M4"}'; echo; echo

echo "== seed equity quote =="
quote '{"symbol":"SMOKE","bid":100,"ask":100.1,"open_interest":0}'

echo "== 3a) open a live long -> expect Class B submitted at mid =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"open","kind":"equity","underlying":"SMOKE","symbol":"SMOKE","side":"buy","qty":1,"limit":100.1,"max_risk_usd":100.1,"plan":'"$PLAN"'}'; echo; echo

echo "== trade through the resting mid limit -> FakeBroker fills it =="
quote '{"symbol":"SMOKE","bid":100.04,"ask":100.05,"open_interest":0}'

echo "== 3b) close the existing long -> expect Class A filled at bid =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"close","symbol":"SMOKE","qty":1}'; echo; echo

echo "== 3c) close again while flat -> expect 400 and no broker effect =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"close","symbol":"SMOKE","qty":1}'; echo; echo

echo "== 4) cancel unknown order -> expect rejected proposal and no broker effect =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"cancel","broker_order_id":"missing-order"}'; echo; echo

echo "== 5) naked short -> expect rejected =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"sell","qty":1,"max_risk_usd":200,"plan":'"$PLAN"'}'; echo; echo

echo "== 6) runtime wake without KERNEL_TOKEN bearer -> expect HTTP 401 =="
runtime_unauth=$(docker compose exec -T agent-runtime sh -c \
  "wget -S -O /dev/null --header='Content-Type: application/json' --post-data='{\"role\":\"scout\",\"trigger\":\"spine\",\"occurrence_id\":\"smoke-unauthorized\"}' http://127.0.0.1:8200/wake" 2>&1 || true)
echo "$runtime_unauth"
printf '%s' "$runtime_unauth" | grep -q '401 Unauthorized'
echo

echo "== final state (shadow risk must remain isolated from live) =="
curl -s "$K/state"; echo; echo

if [ "$SMOKE_DB_CHECK" = "1" ]; then
  shadow_orders_after=$(sql_scalar "select count(*) from orders where ledger='shadow'")
  shadow_fills_after=$(sql_scalar "select count(*) from fills where ledger='shadow'")
  live_orders_after=$(sql_scalar "select count(*) from orders where ledger='live'")
  live_fills_after=$(sql_scalar "select count(*) from fills where ledger='live'")
  orphan_attempts=$(sql_scalar "select count(*) from execution_attempt a left join orders o on o.execution_attempt_id=a.id where o.id is null and a.intent in ('place','paper_place')")
  live_pnl=$(sql_scalar "select local_realized_pnl_micros from daily_pnl where ledger='live' order by market_day desc limit 1")
  shadow_pnl=$(sql_scalar "select local_realized_pnl_micros from daily_pnl where ledger='shadow' order by market_day desc limit 1")
  halted_breakers=$(sql_scalar "select count(*) from breaker_state where halted")
  test "$((shadow_orders_after-shadow_orders_before))" -eq 2
  test "$((shadow_fills_after-shadow_fills_before))" -eq 2
  test "$((live_orders_after-live_orders_before))" -eq 2
  test "$((live_fills_after-live_fills_before))" -eq 2
  test "$orphan_attempts" -eq 0
  test "$live_pnl" -eq -10000
  test "$shadow_pnl" -eq 0
  test "$halted_breakers" -eq 0
  echo "db invariants: +2 shadow orders/fills (including approved Class C), +2 live orders/fills, 0 orphan attempts, live pnl -0.01, shadow pnl 0, breakers clear"
else
  echo "db invariants skipped (SMOKE_DB_CHECK=0)"
fi
