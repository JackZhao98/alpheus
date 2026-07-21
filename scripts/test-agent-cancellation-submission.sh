#!/bin/sh
# Proves AP1 Control Plane cancellation requests are durable, replay-safe,
# fenced and non-effectful against disposable PostgreSQL 16.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap1-cancellation-test-$$"
ARTIFACT_DIR=${AGENT_CANCELLATION_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-cancellation-probe-$$}
IMAGE=${AGENT_CANCELLATION_IMAGE:-postgres:16-alpine}

cleanup() { docker rm -f "$CONTAINER" >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM
mkdir -p "$ARTIFACT_DIR"
rm -f "$ARTIFACT_DIR/probe.txt" "$ARTIFACT_DIR/summary.json" "$ARTIFACT_DIR/junit.xml"

for file in \
 contracts/security/v1/permissions/roles.sql audit/repro/ap0_login_roles.sql \
 agent-platform/migrations/0001_delivery.sql agent-platform/migrations/0002_blob.sql \
 agent-platform/migrations/0003_governance.sql contracts/delivery/v1/permissions/roles.sql \
 contracts/blob/v1/permissions/roles.sql contracts/governance/v1/permissions/roles.sql \
 agent-platform/migrations/0004_ap1_runtime_definitions.sql agent-platform/migrations/0005_ap1_runtime_state.sql \
 audit/repro/ap1_runtime_state.sql agent-platform/migrations/0006_ap1_command_leases.sql \
 audit/repro/ap1_command_leases.sql agent-platform/migrations/0007_ap1_model_calls.sql \
 audit/repro/ap1_model_calls.sql agent-platform/migrations/0008_ap1_attempt_terminalization.sql \
 agent-platform/migrations/0009_ap1_child_task_requests.sql \
 agent-platform/migrations/0010_ap1_cancellation_submission.sql audit/repro/ap1_cancellation_submission.sql
do
 [ -f "$ROOT/$file" ] || { echo "FAIL reason=required_file_missing file=$file" >&2; exit 1; }
done

docker run --detach --rm --name "$CONTAINER" --publish 127.0.0.1::5432 \
 --env POSTGRES_PASSWORD=probe --env POSTGRES_DB=probe "$IMAGE" >/dev/null
ready=false
for n in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
 if docker exec "$CONTAINER" psql --no-psqlrc --username postgres --dbname probe --tuples-only --command 'SELECT 1' >/dev/null 2>&1; then ready=true; break; fi
 sleep 0.25
done
[ "$ready" = true ] || { echo 'FAIL reason=postgres_not_ready' >&2; exit 1; }
load() {
 if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 --username postgres --dbname probe <"$ROOT/$1" >>"$ARTIFACT_DIR/probe.txt" 2>&1; then
  tail -100 "$ARTIFACT_DIR/probe.txt" >&2; echo "FAIL reason=sql_probe_failed file=$1" >&2; exit 1
 fi
}
for file in \
 contracts/security/v1/permissions/roles.sql audit/repro/ap0_login_roles.sql \
 agent-platform/migrations/0001_delivery.sql agent-platform/migrations/0002_blob.sql \
 agent-platform/migrations/0003_governance.sql contracts/delivery/v1/permissions/roles.sql \
 contracts/blob/v1/permissions/roles.sql contracts/governance/v1/permissions/roles.sql \
 agent-platform/migrations/0004_ap1_runtime_definitions.sql agent-platform/migrations/0005_ap1_runtime_state.sql \
 audit/repro/ap1_runtime_state.sql agent-platform/migrations/0006_ap1_command_leases.sql \
 audit/repro/ap1_command_leases.sql agent-platform/migrations/0007_ap1_model_calls.sql \
 audit/repro/ap1_model_calls.sql agent-platform/migrations/0008_ap1_attempt_terminalization.sql \
 agent-platform/migrations/0009_ap1_child_task_requests.sql \
 agent-platform/migrations/0010_ap1_cancellation_submission.sql audit/repro/ap1_cancellation_submission.sql
do load "$file"; done
grep -q 'AP1_CANCELLATION_SUBMISSION_PASS' "$ARTIFACT_DIR/probe.txt" || { echo "FAIL reason=pass_marker_missing" >&2; exit 1; }
printf '{"status":"PASS","probe":"ap1-cancellation-submission","request_only":true,"replay_safe":true,"stable_reason_codes":true,"effect_ceiling":"none"}\n' >"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap1-cancellation-submission" tests="4" failures="0"><testcase name="fenced-request"/><testcase name="idempotent-replay"/><testcase name="no-state-change"/><testcase name="worker-isolation"/></testsuite>\n' >"$ARTIFACT_DIR/junit.xml"
cat "$ARTIFACT_DIR/summary.json"
