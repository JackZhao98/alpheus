#!/bin/sh
# Proves AP1 claim/start/heartbeat transactions against a disposable
# PostgreSQL 16 database. This is a non-money test and loads exactly 0001-0006.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap1-command-leases-test-$$"
ARTIFACT_DIR=${AGENT_COMMAND_LEASES_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-command-leases-probe}
IMAGE=${AGENT_COMMAND_LEASES_IMAGE:-postgres:16-alpine}

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
	"$ARTIFACT_DIR/command-probes.txt" \
	"$ARTIFACT_DIR/runtime-event.json" \
	"$ARTIFACT_DIR/runtime-event-digest.txt" \
	"$ARTIFACT_DIR/runtime-event-validation.json"

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
	"$ROOT/audit/repro/ap1_command_barrier.go"
do
	if [ ! -f "$required_file" ]; then
		echo "FAIL reason=required-file-missing file=$required_file artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done

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

# Seed the already-proven exact-lineage AP1 records before 0006. This also
# proves that the command migration upgrades existing state in place.
docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_runtime_state.sql" \
	>"$ARTIFACT_DIR/runtime-state-fixture.txt" 2>&1

load_sql "$ROOT/agent-platform/migrations/0006_ap1_command_leases.sql" \
	"$ARTIFACT_DIR/migration-0006.txt"

docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_command_leases.sql" \
	>"$ARTIFACT_DIR/command-probes.txt" 2>&1

if ! grep -q 'AP1_COMMAND_LEASES_PASS' "$ARTIFACT_DIR/command-probes.txt"; then
	echo "FAIL reason=command-probe-marker-missing artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

host_port=$(docker port "$CONTAINER" 5432/tcp | sed -n 's/.*://p')
if [ -z "$host_port" ]; then
	echo "FAIL reason=postgres-host-port-missing artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi
(
	cd "$ROOT/kernel"
	go run ../audit/repro/ap1_command_barrier.go \
		-host 127.0.0.1 -port "$host_port" -database probe \
		-user postgres -password probe -requests 20
) >"$ARTIFACT_DIR/concurrency-barrier.json"

if ! grep -q '"status":"PASS"' "$ARTIFACT_DIR/concurrency-barrier.json"; then
	echo "FAIL reason=command-concurrency-barrier artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

# Cross-language proof: reconstruct one SQL-written RuntimeEvent exactly as a
# Runtime v1 contract and require the Go canonicalizer to match its stored
# domain-separated digest.
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align \
	--command \
	"SELECT record_digest
	   FROM agent_control.runtime_event
	  WHERE causation_id = 'cause-claim-command-1'
	    AND subject = 'attempt';" \
	>"$ARTIFACT_DIR/runtime-event-digest.txt"
event_digest=$(tr -d '[:space:]' <"$ARTIFACT_DIR/runtime-event-digest.txt")

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align \
	--command \
	"COPY (
	   SELECT jsonb_strip_nulls(jsonb_build_object(
	       'schema_revision', schema_revision,
	       'event_id', event_id,
	       'subject', subject,
	       'subject_id', subject_id,
	       'from_state', from_state,
	       'to_state', to_state,
	       'generation', generation,
	       'actor', actor,
	       'causation_id', causation_id,
	       'correlation_id', correlation_id,
	       'reason_code', reason_code,
	       'occurred_at', agent_control.runtime_utc_text(occurred_at)
	   ))
	   FROM agent_control.runtime_event
	   WHERE causation_id = 'cause-claim-command-1'
	     AND subject = 'attempt'
	) TO STDOUT" \
	>"$ARTIFACT_DIR/runtime-event.json"

(
	cd "$ROOT/agent-platform"
	go run ./cmd/agent-platform validate-contract \
		--file "$ARTIFACT_DIR/runtime-event.json" \
		--type runtime_event --expect-digest "$event_digest"
) >"$ARTIFACT_DIR/runtime-event-validation.json"

if ! grep -q '"status":"valid"' \
	"$ARTIFACT_DIR/runtime-event-validation.json"; then
	echo "FAIL reason=runtime-event-cross-language-digest artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '%s\n' \
	'{"status":"PASS","probe":"agent-command-leases","effect_ceiling":"none","public_commands":3,"concurrent_claims":20,"processing_commands":0}' \
	>"$ARTIFACT_DIR/summary.json"
printf '%s\n' \
	'<?xml version="1.0" encoding="UTF-8"?>' \
	'<testsuite name="agent-command-leases" tests="1" failures="0"><testcase name="postgresql-command-transactions"/></testsuite>' \
	>"$ARTIFACT_DIR/junit.xml"

echo "PASS probe=agent-command-leases effect_ceiling=none artifacts=$ARTIFACT_DIR"
