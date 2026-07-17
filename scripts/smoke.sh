#!/usr/bin/env bash
# Day-1 smoke test: exercise all three approval paths by hand.
# Usage: ./scripts/smoke.sh   (kernel must be up on :8100)
set -e
K=${KERNEL_URL:-http://localhost:8100}
PLAN='{"stop":"-30%","invalidation":"thesis dead","time_stop":"15:45 ET","target":"+50%"}'

quote() {
  curl -s -X POST "$K/sim/quote" -H 'Content-Type: application/json' -d "$1"
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
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"buy","qty":1,"max_risk_usd":200,"shadow":true,"plan":'"$PLAN"'}'; echo; echo

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

echo "== 4) cancel unknown order -> expect order state rejected =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"cancel","broker_order_id":"missing-order"}'; echo; echo

echo "== 5) naked short -> expect rejected =="
curl -s -X POST $K/operations -H 'Content-Type: application/json' -d '{"proposer":"smoke","action":"open","kind":"option","underlying":"SPY","symbol":"SPY","side":"sell","qty":1,"max_risk_usd":200,"plan":'"$PLAN"'}'; echo; echo

echo "db check: docker compose exec db psql -U alpheus -c \"select class,status from operations order by ts desc limit 5;\""
