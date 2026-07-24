#!/usr/bin/env bash
# Repeatable, non-money launch verifier for the deployed Cortex + Research
# slice. With --restart it first restarts every required long-running service
# and then proves that durable recovery converges without process-local state.
set -euo pipefail

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

restart=false
if [[ ${1:-} == "--restart" ]]; then
	restart=true
elif [[ $# -ne 0 ]]; then
	echo "usage: $0 [--restart]" >&2
	exit 2
fi

required_services=(
	db
	kernel
	cortex-input
	cortex-worker
	research-gateway
	gexbot-provider
)

if $restart; then
	docker compose restart \
		kernel cortex-input cortex-worker research-gateway gexbot-provider \
		>/dev/null
fi

wait_for_service() {
	local service=$1
	local container state health
	for _ in $(seq 1 120); do
		container=$(docker compose ps -q "$service")
		if [[ -n $container ]]; then
			state=$(docker inspect --format '{{.State.Status}}' "$container")
			health=$(docker inspect --format \
				'{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' \
				"$container")
			if [[ $state == running ]] &&
				[[ $health == healthy || $health == none ]]; then
				return
			fi
		fi
		sleep 0.5
	done
	echo "FAIL reason=service_not_healthy service=$service" >&2
	exit 1
}

for service in "${required_services[@]}"; do
	wait_for_service "$service"
done

if docker compose config --services | grep -qx agent-runtime; then
	echo "FAIL reason=legacy_agent_runtime_present" >&2
	exit 1
fi

legacy_status=$(curl -sS -o /dev/null -w '%{http_code}' \
	-X POST http://127.0.0.1:8100/agent/query \
	-H 'Content-Type: application/json' \
	--data '{"symbol":"SPY","query":"launch verifier"}')
if [[ $legacy_status != 410 ]]; then
	echo "FAIL reason=legacy_query_write_open status=$legacy_status" >&2
	exit 1
fi

overview=$(curl --fail --silent --show-error --retry 10 \
	--retry-connrefused --retry-delay 1 \
	http://127.0.0.1:8100/agent/cortex-operations)
python3 -c '
import json, sys
value = json.load(sys.stdin)
assert value["status"] == "healthy"
assert value["cortex"]["status"] == "healthy"
assert value["research"]["status"] == "healthy"
assert value["cortex"]["runs"]["active"] == 0
assert all(count == 0 for count in value["cortex"]["risks"].values())
assert value["research"]["collector_configured"] is True
series = value["research"]["series"]
assert {item["category"] for item in series} == {
    "gex_full", "gex_zero", "gex_one"
}
assert all(item["available"] and item["fresh"] for item in series)
' <<<"$overview"

cancellation_invariant=$(docker compose exec -T db \
	psql --no-psqlrc -U alpheus -d alpheus --tuples-only --no-align \
	--command "
WITH latest AS (
  SELECT request_id,run_id
  FROM agent_control.cortex_run_cancellation
  WHERE state='canceled'
  ORDER BY terminal_at DESC
  LIMIT 1
)
SELECT CASE WHEN EXISTS (
  SELECT 1
  FROM latest
  JOIN agent_control.runtime_run AS run USING (run_id)
  WHERE run.state='canceled'
    AND run.terminal_at IS NOT NULL
    AND EXISTS (
      SELECT 1 FROM agent_control.runtime_event AS event
      WHERE event.subject='run' AND event.subject_id=run.run_id
        AND event.reason_code='user_cancel'
        AND event.to_state='canceled'
    )
    AND NOT EXISTS (
      SELECT 1 FROM agent_control.runtime_task AS task
      WHERE task.run_id=run.run_id
        AND NOT agent_control.runtime_terminal_state('task',task.state)
    )
    AND NOT EXISTS (
      SELECT 1 FROM agent_control.runtime_session AS session
      WHERE session.run_id=run.run_id AND session.state<>'closed'
    )
    AND NOT EXISTS (
      SELECT 1 FROM agent_control.runtime_attempt AS attempt
      WHERE attempt.run_id=run.run_id
        AND NOT agent_control.runtime_terminal_state('attempt',attempt.state)
    )
    AND NOT EXISTS (
      SELECT 1 FROM agent_control.runtime_turn AS turn
      WHERE turn.run_id=run.run_id
        AND NOT agent_control.runtime_terminal_state('turn',turn.state)
    )
) THEN 'PASS' ELSE 'FAIL' END;
" | tr -d '[:space:]')
if [[ $cancellation_invariant != PASS ]]; then
	echo "FAIL reason=cancellation_terminal_invariant" >&2
	exit 1
fi

recovery_evidence=$(docker compose exec -T db \
	psql --no-psqlrc -U alpheus -d alpheus --tuples-only --no-align \
	--command "
SELECT json_build_object(
  'expired_runs',(
    SELECT count(*) FROM agent_control.cortex_expired_run_recovery
  ),
  'tool_recovery_events',(
    SELECT count(*) FROM agent_control.cortex_tool_recovery_event
  ),
  'canceled_runs',(
    SELECT count(*) FROM agent_control.cortex_run_cancellation
    WHERE state='canceled'
  )
)::TEXT;
" | tr -d '\n')
python3 -c '
import json, sys
value = json.load(sys.stdin)
assert value["expired_runs"] > 0
assert value["tool_recovery_events"] > 0
assert value["canceled_runs"] > 0
' <<<"$recovery_evidence"

printf '{"status":"PASS","probe":"cortex-research-operations","restart_tested":%s,"required_services":6,"legacy_writer_retired":true,"cortex_risks_zero":true,"research_fresh":true,"cancellation_invariants":true,"durable_recovery_evidence":%s}\n' \
	"$restart" "$recovery_evidence"
