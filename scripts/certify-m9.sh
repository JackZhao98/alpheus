#!/usr/bin/env bash
# M9 pre-live certification. All Docker effects are isolated under a dedicated
# Compose project and FakeBroker; the Robinhood read-only deployment is never
# joined, restarted, or mutated by this script.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
PROJECT=${M9_PROJECT:-alpheus-m9-cert}
BASE_URL=http://127.0.0.1:19100
DB_PORT=15439
COMPOSE_FILES=( -f "$ROOT/docker-compose.yml" -f "$ROOT/audit/repro/m9-compose.yml" )
DB_PAUSED=0
export GOCACHE=${M9_GOCACHE:-${TMPDIR:-/tmp}/alpheus-m9-go-cache}

compose() {
  docker compose -p "$PROJECT" "${COMPOSE_FILES[@]}" "$@"
}

cleanup() {
  if [ "$DB_PAUSED" = "1" ]; then
    compose unpause db >/dev/null 2>&1 || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_healthy() {
  local service=$1
  local container="${PROJECT}-${service}-1"
  local state=""
  for _ in $(seq 1 40); do
    state=$(docker inspect "$container" --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' 2>/dev/null || true)
    if [ "$state" = "healthy" ] || { [ "$service" = "agent-runtime" ] && [ "$state" = "running" ]; }; then
      return 0
    fi
    sleep 1
  done
  echo "FAIL: $service did not become healthy (last state: $state)" >&2
  compose logs --tail 100 "$service" >&2 || true
  return 1
}

start_sim_stack() {
  compose down -v --remove-orphans >/dev/null 2>&1 || true
  BROKER=fake TRADING_MODE=sim LIVE_TRADING_ENABLED=false \
    RUNTIME_TOKEN=m9-runtime-secret ADMIN_TOKEN=m9-admin-secret KERNEL_TOKEN=m9-kernel-secret \
    CONSOLE_ORIGIN="$BASE_URL" TICK_SECONDS=0 \
    compose up -d --build
  wait_healthy db
  wait_healthy kernel
  wait_healthy agent-runtime
}

echo "== M9 static, unit, race, and fault-seam suite =="
unformatted=$(find "$ROOT/kernel" "$ROOT/agent-runtime" "$ROOT/audit/repro" -name '*.go' -type f -print0 | xargs -0 gofmt -l)
if [ -n "$unformatted" ]; then
  echo "FAIL: gofmt changes required" >&2
  echo "$unformatted" >&2
  exit 1
fi
go -C "$ROOT/kernel" vet ./...
go -C "$ROOT/kernel" test -race ./...
go -C "$ROOT/agent-runtime" vet ./...
go -C "$ROOT/agent-runtime" test -race ./...

coverage_file=$(mktemp "${TMPDIR:-/tmp}/alpheus-risk-cover.XXXXXX")
go -C "$ROOT/kernel" test -coverprofile="$coverage_file" ./internal/risk
risk_coverage=$(go -C "$ROOT/kernel" tool cover -func="$coverage_file" | awk '/^total:/{gsub(/%/,"",$3); print $3}')
rm -f "$coverage_file"
awk -v coverage="$risk_coverage" 'BEGIN { if (coverage + 0 < 90) exit 1 }' || {
  echo "FAIL: risk coverage ${risk_coverage}% is below 90%" >&2
  exit 1
}
echo "PASS: risk coverage ${risk_coverage}%"

echo "== M9 fresh PostgreSQL integration and deterministic advisory-lock suite =="
compose down -v --remove-orphans >/dev/null 2>&1 || true
compose up -d db
wait_healthy db
for database in alpheus_test alpheus_m3a alpheus_m3a_activation alpheus_m3c alpheus_backfill; do
  docker exec "${PROJECT}-db-1" createdb -U alpheus "$database"
done
database_base="postgresql://alpheus:alpheus@127.0.0.1:${DB_PORT}"
ALPHEUS_TEST_DATABASE_URL="${database_base}/alpheus_test?sslmode=disable" \
ALPHEUS_TEST_M3A_DATABASE_URL="${database_base}/alpheus_m3a?sslmode=disable" \
ALPHEUS_TEST_M3A_ACTIVATION_DATABASE_URL="${database_base}/alpheus_m3a_activation?sslmode=disable" \
ALPHEUS_TEST_M3C_DATABASE_URL="${database_base}/alpheus_m3c?sslmode=disable" \
ALPHEUS_TEST_BACKFILL_DATABASE_URL="${database_base}/alpheus_backfill?sslmode=disable" \
ALPHEUS_TEST_MIGRATIONS_DIR="$ROOT/db/migrations" \
  go -C "$ROOT/kernel" test -race ./internal/store

echo "== M9 daily counter barrier: live ledger =="
start_sim_stack
go run "$ROOT/audit/repro/i4_barrier.go" -urls "$BASE_URL"

echo "== M9 daily counter barrier: shadow ledger =="
start_sim_stack
go run "$ROOT/audit/repro/i4_barrier.go" -urls "$BASE_URL" -shadow

echo "== M9 full Compose smoke =="
start_sim_stack
COMPOSE_PROJECT_NAME="$PROJECT" \
COMPOSE_FILE="$ROOT/docker-compose.yml:$ROOT/audit/repro/m9-compose.yml" \
KERNEL_URL="$BASE_URL" CONSOLE_ORIGIN="$BASE_URL" \
  "$ROOT/scripts/smoke.sh"

echo "== M9 paused-DB honesty =="
curl -fsS -X POST "$BASE_URL/sim/quote" -H 'Content-Type: application/json' \
  -H "Origin: $BASE_URL" \
  --data '{"symbol":"DBFAIL","bid":0.99,"ask":1,"open_interest":1000}' >/dev/null
orders_before=$(compose exec -T db psql -U alpheus -d alpheus -Atqc 'select count(*) from orders')
compose pause db >/dev/null
DB_PAUSED=1
pause_result=$(curl -sS --max-time 8 -o "${TMPDIR:-/tmp}/alpheus-m9-db-pause.json" \
  -w '%{http_code} %{time_total}' -X POST "$BASE_URL/operations" \
  -H 'Content-Type: application/json' \
  --data '{"proposer":"m9-db-pause","action":"open","kind":"equity","underlying":"DBFAIL","symbol":"DBFAIL","side":"buy","qty":1,"max_risk_usd":1,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}')
pause_code=${pause_result%% *}
pause_seconds=${pause_result##* }
if [ "$pause_code" != "503" ] || ! awk -v elapsed="$pause_seconds" 'BEGIN { exit !(elapsed + 0 <= 5.0) }'; then
  echo "FAIL: paused DB returned HTTP $pause_code in ${pause_seconds}s" >&2
  exit 1
fi
compose unpause db >/dev/null
DB_PAUSED=0
wait_healthy db
orders_after=$(compose exec -T db psql -U alpheus -d alpheus -Atqc 'select count(*) from orders')
if [ "$orders_after" != "$orders_before" ]; then
  echo "FAIL: paused-DB proposal changed orders ($orders_before -> $orders_after)" >&2
  exit 1
fi
echo "PASS: paused DB returned 503 in ${pause_seconds}s with zero order effects"

echo "== M9 PostgreSQL process replacement =="
compose kill db >/dev/null
compose up -d db >/dev/null
wait_healthy db
state_code=""
for _ in $(seq 1 20); do
  state_code=$(curl -sS -o "${TMPDIR:-/tmp}/alpheus-m9-state.json" -w '%{http_code}' "$BASE_URL/state" || true)
  [ "$state_code" = "200" ] && break
  sleep 1
done
if [ "$state_code" != "200" ]; then
  echo "FAIL: kernel did not recover after PostgreSQL replacement" >&2
  exit 1
fi
curl -fsS -X POST "$BASE_URL/sim/quote" -H 'Content-Type: application/json' \
  -H "Origin: $BASE_URL" \
  --data '{"symbol":"DBFAIL","bid":0.99,"ask":1,"open_interest":1000}' >/dev/null
recovered_code=$(curl -sS -o "${TMPDIR:-/tmp}/alpheus-m9-recovered.json" -w '%{http_code}' \
  -X POST "$BASE_URL/operations" -H 'Content-Type: application/json' \
  --data '{"proposer":"m9-db-recovered","action":"open","kind":"equity","underlying":"DBFAIL","symbol":"DBFAIL","side":"buy","qty":1,"max_risk_usd":1,"plan":{"stop":"x","invalidation":"x","time_stop":"x","target":"x"}}')
if [ "$recovered_code" != "200" ]; then
  echo "FAIL: post-replacement proposal returned HTTP $recovered_code" >&2
  exit 1
fi
unknown=$(compose exec -T db psql -U alpheus -d alpheus -Atqc "select count(*) from execution_attempt where state='unknown'")
unsafe_orphans=$(compose exec -T db psql -U alpheus -d alpheus -Atqc \
  "select count(*) from open_reservation r where r.resource_state='held' and not exists (select 1 from execution_attempt a join orders o on o.execution_attempt_id=a.id where a.open_reservation_id=r.id)")
if [ "$unknown" != "0" ] || [ "$unsafe_orphans" != "0" ]; then
  echo "FAIL: unresolved unknown=$unknown unsafe_orphans=$unsafe_orphans" >&2
  exit 1
fi
echo "PASS: PostgreSQL replacement recovered without kernel restart; unknown=0 unsafe_orphans=0"
echo "M9 CERTIFICATION PASS"
