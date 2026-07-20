#!/bin/sh
# Proves AP1 model-call dispatch, unknown containment, reconciliation, and
# budget settlement against a disposable PostgreSQL 16 database. This is a
# non-money test: no model, Kernel, Provider, operation, or broker is called.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap1-model-calls-test-$$"
ARTIFACT_DIR=${AGENT_MODEL_CALLS_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-model-calls-probe-$$}
IMAGE=${AGENT_MODEL_CALLS_IMAGE:-postgres:16-alpine}

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$ARTIFACT_DIR"
rm -f \
	"$ARTIFACT_DIR/summary.json" \
	"$ARTIFACT_DIR/junit.xml" \
	"$ARTIFACT_DIR/container-id.txt" \
	"$ARTIFACT_DIR/model-call-probes.txt" \
	"$ARTIFACT_DIR/model-call-barrier.json" \
	"$ARTIFACT_DIR/model-call-manifest.json" \
	"$ARTIFACT_DIR/model-call-manifest.digest" \
	"$ARTIFACT_DIR/model-call-manifest-validation.json" \
	"$ARTIFACT_DIR/model-call-result.json" \
	"$ARTIFACT_DIR/model-call-result.digest" \
	"$ARTIFACT_DIR/model-call-result-validation.json" \
	"$ARTIFACT_DIR/model-call-event.json" \
	"$ARTIFACT_DIR/model-call-event.digest" \
	"$ARTIFACT_DIR/model-call-event-validation.json" \
	"$ARTIFACT_DIR/ap1-model-call-barrier"

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
	"$ROOT/audit/repro/ap1_model_call_barrier.go"
do
	if [ ! -f "$required_file" ]; then
		echo "FAIL reason=required-file-missing file=$required_file artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done

# Build before creating the leased fixture. A cold Go compile must not consume
# the Worker's recovery window and turn a slow machine into a false failure.
(
	cd "$ROOT/kernel"
	go build -o "$ARTIFACT_DIR/ap1-model-call-barrier" \
		../audit/repro/ap1_model_call_barrier.go
)

docker run --detach --rm --name "$CONTAINER" \
	--publish 127.0.0.1::5432 \
	--env POSTGRES_PASSWORD=probe --env POSTGRES_DB=probe "$IMAGE" \
	>"$ARTIFACT_DIR/container-id.txt"

ready=false
ready_count=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 \
	21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40
do
	if docker exec "$CONTAINER" psql --no-psqlrc --username postgres --dbname probe \
		--tuples-only --command 'SELECT 1' >/dev/null 2>&1; then
		ready_count=$((ready_count + 1))
		if [ "$ready_count" -ge 3 ]; then
			ready=true
			break
		fi
	else
		ready_count=0
	fi
	sleep 0.25
done
if [ "$ready" != true ]; then
	echo "FAIL reason=postgres-not-ready artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

load_sql() {
	file=$1
	log=$2
	docker exec --interactive "$CONTAINER" psql --no-psqlrc \
		--set ON_ERROR_STOP=1 --single-transaction \
		--username postgres --dbname probe <"$file" >"$log" 2>&1
}

load_sql "$ROOT/contracts/security/v1/permissions/roles.sql" \
	"$ARTIFACT_DIR/security-roles.txt"
load_sql "$ROOT/audit/repro/ap0_login_roles.sql" \
	"$ARTIFACT_DIR/login-fixtures.txt"

for migration in 0001_delivery 0002_blob 0003_governance
do
	load_sql "$ROOT/agent-platform/migrations/$migration.sql" \
		"$ARTIFACT_DIR/migration-${migration%%_*}.txt"
done

load_sql "$ROOT/contracts/delivery/v1/permissions/roles.sql" \
	"$ARTIFACT_DIR/delivery-grants.txt"
load_sql "$ROOT/contracts/blob/v1/permissions/roles.sql" \
	"$ARTIFACT_DIR/blob-grants.txt"
load_sql "$ROOT/contracts/governance/v1/permissions/roles.sql" \
	"$ARTIFACT_DIR/governance-grants.txt"
load_sql "$ROOT/agent-platform/migrations/0004_ap1_runtime_definitions.sql" \
	"$ARTIFACT_DIR/migration-0004.txt"
load_sql "$ROOT/agent-platform/migrations/0005_ap1_runtime_state.sql" \
	"$ARTIFACT_DIR/migration-0005.txt"

docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_runtime_state.sql" \
	>"$ARTIFACT_DIR/runtime-state-fixture.txt" 2>&1

load_sql "$ROOT/agent-platform/migrations/0006_ap1_command_leases.sql" \
	"$ARTIFACT_DIR/migration-0006.txt"

# Reuse the already-proven command fixture to create current heads and a real
# executing Worker Attempt. Its own catalog assertion runs before 0007 adds
# the three model-call entrypoints.
docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_command_leases.sql" \
	>"$ARTIFACT_DIR/lease-fixture.txt" 2>&1

load_sql "$ROOT/agent-platform/migrations/0007_ap1_model_calls.sql" \
	"$ARTIFACT_DIR/migration-0007.txt"

if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_model_calls.sql" \
	>"$ARTIFACT_DIR/model-call-probes.txt" 2>&1; then
	tail -80 "$ARTIFACT_DIR/model-call-probes.txt" >&2
	echo "FAIL reason=model-call-sql-probe artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

if ! grep -q 'AP1_MODEL_CALLS_PASS' "$ARTIFACT_DIR/model-call-probes.txt"; then
	echo "FAIL reason=model-call-probe-marker-missing artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

host_port=$(docker port "$CONTAINER" 5432/tcp | sed -n 's/.*://p')
if [ -z "$host_port" ]; then
	echo "FAIL reason=postgres-host-port-missing artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi
"$ARTIFACT_DIR/ap1-model-call-barrier" \
	-host 127.0.0.1 -port "$host_port" -database probe \
	-user postgres -password probe -requests 20 \
	>"$ARTIFACT_DIR/model-call-barrier.json"

if ! grep -q '"status":"PASS"' "$ARTIFACT_DIR/model-call-barrier.json"; then
	echo "FAIL reason=model-call-concurrency-barrier artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

# Cross-language canonical proof for one SQL-written Manifest, Result, and
# terminal Turn event.
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT record_digest FROM agent_control.runtime_model_call_manifest
	  WHERE call_id = 'call-model-success-1';" \
	>"$ARTIFACT_DIR/model-call-manifest.digest"
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"COPY (
	 SELECT jsonb_build_object(
	   'schema_revision', schema_revision, 'call_id', call_id,
	   'turn_id', turn_id, 'attempt_id', attempt_id,
	   'idempotency_key', idempotency_key, 'provider', provider,
	   'model', model, 'prompt_digest', prompt_digest,
	   'context_manifest', context_manifest,
	   'output_contract_digest', output_contract_digest,
	   'request_digest', request_digest,
	   'max_output_tokens', max_output_tokens,
	   'reserved_input_tokens', reserved_input_tokens,
	   'reserved_external_cost_micro_usd', reserved_external_cost_micro_usd,
	   'timeout_ms', timeout_ms, 'temperature_micros', temperature_micros,
	   'created_at', agent_control.runtime_utc_text(created_at)
	 ) FROM agent_control.runtime_model_call_manifest
	 WHERE call_id = 'call-model-success-1'
	) TO STDOUT" >"$ARTIFACT_DIR/model-call-manifest.json"

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT record_digest FROM agent_control.runtime_model_call_result
	  WHERE call_id = 'call-model-success-1';" \
	>"$ARTIFACT_DIR/model-call-result.digest"
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"COPY (
	 SELECT jsonb_build_object(
	   'schema_revision', schema_revision, 'result_id', result_id,
	   'call_id', call_id, 'attempt_id', attempt_id, 'turn_id', turn_id,
	   'idempotency_key', idempotency_key, 'request_digest', request_digest,
	   'provider_request_id', provider_request_id, 'output', output,
	   'input_tokens', input_tokens, 'output_tokens', output_tokens,
	   'external_cost_micro_usd', external_cost_micro_usd,
	   'wall_time_ms', wall_time_ms, 'finish_reason', finish_reason,
	   'committed_at', agent_control.runtime_utc_text(committed_at)
	 ) FROM agent_control.runtime_model_call_result
	 WHERE call_id = 'call-model-success-1'
	) TO STDOUT" >"$ARTIFACT_DIR/model-call-result.json"

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT record_digest FROM agent_control.runtime_event
	  WHERE causation_id = 'cause-model-resolve-success-1'
	    AND subject = 'turn';" >"$ARTIFACT_DIR/model-call-event.digest"
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"COPY (
	 SELECT jsonb_strip_nulls(jsonb_build_object(
	   'schema_revision', schema_revision, 'event_id', event_id,
	   'subject', subject, 'subject_id', subject_id,
	   'from_state', from_state, 'to_state', to_state,
	   'generation', generation, 'actor', actor,
	   'causation_id', causation_id, 'correlation_id', correlation_id,
	   'reason_code', reason_code,
	   'occurred_at', agent_control.runtime_utc_text(occurred_at)
	 )) FROM agent_control.runtime_event
	 WHERE causation_id = 'cause-model-resolve-success-1'
	   AND subject = 'turn'
	) TO STDOUT" >"$ARTIFACT_DIR/model-call-event.json"

manifest_digest=$(tr -d '[:space:]' <"$ARTIFACT_DIR/model-call-manifest.digest")
result_digest=$(tr -d '[:space:]' <"$ARTIFACT_DIR/model-call-result.digest")
event_digest=$(tr -d '[:space:]' <"$ARTIFACT_DIR/model-call-event.digest")
(
	cd "$ROOT/agent-platform"
	go run ./cmd/agent-platform validate-contract \
		--file "$ARTIFACT_DIR/model-call-manifest.json" \
		--type model_call_manifest --expect-digest "$manifest_digest"
) >"$ARTIFACT_DIR/model-call-manifest-validation.json"
(
	cd "$ROOT/agent-platform"
	go run ./cmd/agent-platform validate-contract \
		--file "$ARTIFACT_DIR/model-call-result.json" \
		--type model_call_result --expect-digest "$result_digest"
) >"$ARTIFACT_DIR/model-call-result-validation.json"
(
	cd "$ROOT/agent-platform"
	go run ./cmd/agent-platform validate-contract \
		--file "$ARTIFACT_DIR/model-call-event.json" \
		--type runtime_event --expect-digest "$event_digest"
) >"$ARTIFACT_DIR/model-call-event-validation.json"

for validation in \
	"$ARTIFACT_DIR/model-call-manifest-validation.json" \
	"$ARTIFACT_DIR/model-call-result-validation.json" \
	"$ARTIFACT_DIR/model-call-event-validation.json"
do
	if ! grep -q '"status":"valid"' "$validation"; then
		echo "FAIL reason=cross-language-contract-digest file=$validation artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done

processing=$(docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align \
	--command "SELECT count(*) FROM agent_control.runtime_command WHERE state = 'processing';" | tr -d '[:space:]')
if [ "$processing" != 0 ]; then
	echo "FAIL reason=processing-command-leak count=$processing artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '%s\n' \
	'{"status":"PASS","probe":"agent-model-calls","effect_ceiling":"none","public_commands":6,"concurrent_dispatches":20,"processing_commands":0}' \
	>"$ARTIFACT_DIR/summary.json"
printf '%s\n' \
	'<?xml version="1.0" encoding="UTF-8"?>' \
	'<testsuite name="agent-model-calls" tests="1" failures="0"><testcase name="postgresql-model-call-transactions"/></testsuite>' \
	>"$ARTIFACT_DIR/junit.xml"

echo "PASS probe=agent-model-calls effect_ceiling=none artifacts=$ARTIFACT_DIR"
