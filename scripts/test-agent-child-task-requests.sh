#!/bin/sh
# Proves AP1 Worker child-task requests are fenced, durable, replay-safe and
# non-admitting against disposable PostgreSQL 16. The probe calls no model,
# Tool, Kernel, Provider, operation, broker, GRACE, or Delegation path.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap1-child-request-test-$$"
ARTIFACT_DIR=${AGENT_CHILD_REQUEST_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-child-request-probe-$$}
IMAGE=${AGENT_CHILD_REQUEST_IMAGE:-postgres:16-alpine}

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$ARTIFACT_DIR"
rm -f "$ARTIFACT_DIR/summary.json" "$ARTIFACT_DIR/junit.xml" \
	"$ARTIFACT_DIR/child-task-requests.txt"

for required_file in \
	"$ROOT/contracts/security/v1/permissions/roles.sql" \
	"$ROOT/audit/repro/ap0_login_roles.sql" \
	"$ROOT/agent-platform/migrations/0001_delivery.sql" \
	"$ROOT/agent-platform/migrations/0002_blob.sql" \
	"$ROOT/agent-platform/migrations/0003_governance.sql" \
	"$ROOT/contracts/delivery/v1/permissions/roles.sql" \
	"$ROOT/contracts/blob/v1/permissions/roles.sql" \
	"$ROOT/contracts/governance/v1/permissions/roles.sql" \
	"$ROOT/agent-platform/migrations/0004_ap1_runtime_definitions.sql" \
	"$ROOT/agent-platform/migrations/0005_ap1_runtime_state.sql" \
	"$ROOT/audit/repro/ap1_runtime_state.sql" \
	"$ROOT/agent-platform/migrations/0006_ap1_command_leases.sql" \
	"$ROOT/audit/repro/ap1_command_leases.sql" \
	"$ROOT/agent-platform/migrations/0007_ap1_model_calls.sql" \
	"$ROOT/audit/repro/ap1_model_calls.sql" \
	"$ROOT/agent-platform/migrations/0008_ap1_attempt_terminalization.sql" \
	"$ROOT/agent-platform/migrations/0009_ap1_child_task_requests.sql" \
	"$ROOT/audit/repro/ap1_child_task_requests.sql"
do
	if [ ! -f "$required_file" ]; then
		echo "FAIL reason=required_file_missing file=$required_file artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done

docker run --detach --rm --name "$CONTAINER" --publish 127.0.0.1::5432 \
	--env POSTGRES_PASSWORD=probe --env POSTGRES_DB=probe "$IMAGE" >/dev/null

ready=false
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20
do
	if docker exec "$CONTAINER" psql --no-psqlrc --username postgres \
		--dbname probe --tuples-only --command 'SELECT 1' >/dev/null 2>&1; then
		ready=true
		break
	fi
	sleep 0.25
done
if [ "$ready" != true ]; then
	echo "FAIL reason=postgres_not_ready artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

load_sql() {
	file=$1
	if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc \
		--set ON_ERROR_STOP=1 --username postgres --dbname probe \
		<"$file" >>"$ARTIFACT_DIR/child-task-requests.txt" 2>&1; then
		tail -100 "$ARTIFACT_DIR/child-task-requests.txt" >&2
		echo "FAIL reason=sql_probe_failed file=$file artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
}

for file in \
	contracts/security/v1/permissions/roles.sql \
	audit/repro/ap0_login_roles.sql \
	agent-platform/migrations/0001_delivery.sql \
	agent-platform/migrations/0002_blob.sql \
	agent-platform/migrations/0003_governance.sql \
	contracts/delivery/v1/permissions/roles.sql \
	contracts/blob/v1/permissions/roles.sql \
	contracts/governance/v1/permissions/roles.sql \
	agent-platform/migrations/0004_ap1_runtime_definitions.sql \
	agent-platform/migrations/0005_ap1_runtime_state.sql \
	audit/repro/ap1_runtime_state.sql \
	agent-platform/migrations/0006_ap1_command_leases.sql \
	audit/repro/ap1_command_leases.sql \
	agent-platform/migrations/0007_ap1_model_calls.sql \
	audit/repro/ap1_model_calls.sql \
	agent-platform/migrations/0008_ap1_attempt_terminalization.sql \
	agent-platform/migrations/0009_ap1_child_task_requests.sql \
	audit/repro/ap1_child_task_requests.sql
do
	load_sql "$ROOT/$file"
done

if ! grep -q 'AP1_CHILD_TASK_REQUESTS_PASS' \
	"$ARTIFACT_DIR/child-task-requests.txt"; then
	echo "FAIL reason=pass_marker_missing artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"ap1-child-task-requests","migrations":["0004","0005","0006","0007","0008","0009"],"request_only":true,"replay_safe":true,"stable_reason_codes":true,"worker_direct_table_access":false,"effect_ceiling":"none"}\n' \
	>"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap1-child-task-requests" tests="5" failures="0"><testcase name="fenced-request"/><testcase name="idempotent-replay"/><testcase name="no-runnable-child"/><testcase name="stable-denial"/><testcase name="worker-table-isolation"/></testsuite>\n' \
	>"$ARTIFACT_DIR/junit.xml"
cat "$ARTIFACT_DIR/summary.json"
