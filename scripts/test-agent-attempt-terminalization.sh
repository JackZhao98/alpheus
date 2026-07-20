#!/bin/sh
# Proves AP1 commit_attempt/fail_attempt state, Artifact lineage, retry budget,
# idempotency, and ACL invariants against disposable PostgreSQL 16. This is a
# non-money test and calls no model, Tool, Kernel, Provider, operation, broker,
# GRACE, or Delegation path.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap1-attempt-terminal-test-$$"
ARTIFACT_DIR=${AGENT_ATTEMPT_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-attempt-probe-$$}
IMAGE=${AGENT_ATTEMPT_IMAGE:-postgres:16-alpine}

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
	"$ARTIFACT_DIR/attempt-terminalization-probes.txt" \
	"$ARTIFACT_DIR/artifact.json" \
	"$ARTIFACT_DIR/artifact.digest" \
	"$ARTIFACT_DIR/artifact-validation.json" \
	"$ARTIFACT_DIR/publication-intent.json" \
	"$ARTIFACT_DIR/publication-intent.digest" \
	"$ARTIFACT_DIR/publication-intent-validation.json" \
	"$ARTIFACT_DIR/attempt-terminalization-barrier" \
	"$ARTIFACT_DIR/attempt-terminalization-barrier.json"

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
	"$ROOT/audit/repro/ap1_attempt_terminalization.sql" \
	"$ROOT/audit/repro/ap1_attempt_terminalization_barrier.go"
do
	if [ ! -f "$required_file" ]; then
		echo "FAIL reason=required-file-missing file=$required_file artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done

(
	cd "$ROOT/kernel"
	GOCACHE=${GOCACHE:-/tmp/alpheus-go-cache-kernel} go build \
		-o "$ARTIFACT_DIR/attempt-terminalization-barrier" \
		../audit/repro/ap1_attempt_terminalization_barrier.go
)

docker run --detach --rm --name "$CONTAINER" \
	--publish 127.0.0.1::5432 \
	--env POSTGRES_PASSWORD=probe --env POSTGRES_DB=probe "$IMAGE" \
	>"$ARTIFACT_DIR/container-id.txt"

published_address=$(docker port "$CONTAINER" 5432/tcp)
postgres_port=${published_address##*:}
case "$postgres_port" in
	''|*[!0-9]*)
		echo "FAIL reason=postgres-port-invalid address=$published_address artifacts=$ARTIFACT_DIR" >&2
		exit 1
		;;
esac

ready=false
ready_count=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 \
	21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40
do
	if docker exec "$CONTAINER" psql --no-psqlrc --username postgres \
		--dbname probe --tuples-only --command 'SELECT 1' >/dev/null 2>&1; then
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
	if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc \
		--set ON_ERROR_STOP=1 --single-transaction \
		--username postgres --dbname probe <"$file" >"$log" 2>&1; then
		tail -80 "$log" >&2
		echo "FAIL reason=sql-load file=$file artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
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

if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_runtime_state.sql" \
	>"$ARTIFACT_DIR/runtime-state-fixture.txt" 2>&1; then
	tail -80 "$ARTIFACT_DIR/runtime-state-fixture.txt" >&2
	exit 1
fi

load_sql "$ROOT/agent-platform/migrations/0006_ap1_command_leases.sql" \
	"$ARTIFACT_DIR/migration-0006.txt"
if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_command_leases.sql" \
	>"$ARTIFACT_DIR/lease-fixture.txt" 2>&1; then
	tail -80 "$ARTIFACT_DIR/lease-fixture.txt" >&2
	exit 1
fi

load_sql "$ROOT/agent-platform/migrations/0007_ap1_model_calls.sql" \
	"$ARTIFACT_DIR/migration-0007.txt"
if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_model_calls.sql" \
	>"$ARTIFACT_DIR/model-call-fixture.txt" 2>&1; then
	tail -80 "$ARTIFACT_DIR/model-call-fixture.txt" >&2
	exit 1
fi

load_sql "$ROOT/agent-platform/migrations/0008_ap1_attempt_terminalization.sql" \
	"$ARTIFACT_DIR/migration-0008.txt"
if ! docker exec --interactive "$CONTAINER" psql --no-psqlrc \
	--set ON_ERROR_STOP=1 --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_attempt_terminalization.sql" \
	>"$ARTIFACT_DIR/attempt-terminalization-probes.txt" 2>&1; then
	tail -100 "$ARTIFACT_DIR/attempt-terminalization-probes.txt" >&2
	echo "FAIL reason=attempt-terminalization-sql-probe artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

if ! grep -q 'AP1_ATTEMPT_TERMINALIZATION_PASS' \
	"$ARTIFACT_DIR/attempt-terminalization-probes.txt"; then
	echo "FAIL reason=attempt-terminalization-marker-missing artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

if ! "$ARTIFACT_DIR/attempt-terminalization-barrier" \
	--host 127.0.0.1 --port "$postgres_port" --database probe \
	--user postgres --password probe --worker worker-1 --requests 20 \
	>"$ARTIFACT_DIR/attempt-terminalization-barrier.json"; then
	echo "FAIL reason=attempt-terminalization-barrier artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi
if ! grep -q '"status":"PASS"' \
	"$ARTIFACT_DIR/attempt-terminalization-barrier.json"; then
	echo "FAIL reason=attempt-terminalization-barrier-marker artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

# Reconstruct the two SQL-authored immutable records and prove that PostgreSQL
# and Go produce the same canonical contract digests.
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT record_digest FROM agent_control.runtime_artifact
	  WHERE task_id = 'task-command-1';" >"$ARTIFACT_DIR/artifact.digest"
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"COPY (
	 SELECT jsonb_build_object(
	   'schema_revision', artifact.schema_revision,
	   'artifact_id', artifact.artifact_id,
	   'run_id', artifact.run_id, 'task_id', artifact.task_id,
	   'session_id', artifact.session_id,
	   'attempt_id', artifact.attempt_id,
	   'source_result', jsonb_build_object(
	     'owner', artifact.source_result_owner,
	     'record_type', artifact.source_result_record_type,
	     'record_id', artifact.source_result_id,
	     'schema_revision', artifact.source_result_schema_revision,
	     'record_digest', artifact.source_result_digest
	   ),
	   'artifact_type', artifact.artifact_type,
	   'output_contract_digest', artifact.output_contract_digest,
	   'effect_class', artifact.effect_class,
	   'sections', (
	     SELECT jsonb_agg(jsonb_build_object(
	       'name', section.name, 'required', section.required,
	       'content', section.content
	     ) ORDER BY section.ordinal)
	     FROM agent_control.runtime_artifact_section AS section
	     WHERE section.artifact_id = artifact.artifact_id
	   ),
	   'created_at', agent_control.runtime_utc_text(artifact.created_at)
	 ) FROM agent_control.runtime_artifact AS artifact
	 WHERE artifact.task_id = 'task-command-1'
	) TO STDOUT" >"$ARTIFACT_DIR/artifact.json"

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT intent.record_digest
	 FROM agent_control.runtime_artifact_publication_intent AS intent
	 JOIN agent_control.runtime_artifact AS artifact
	   ON artifact.artifact_id = intent.artifact_id
	 WHERE artifact.task_id = 'task-command-1';" \
	>"$ARTIFACT_DIR/publication-intent.digest"
docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"COPY (
	 SELECT jsonb_build_object(
	   'schema_revision', intent.schema_revision,
	   'intent_id', intent.intent_id,
	   'artifact', jsonb_build_object(
	     'owner', intent.artifact_owner,
	     'record_type', intent.artifact_record_type,
	     'record_id', intent.artifact_id,
	     'schema_revision', intent.artifact_schema_revision,
	     'record_digest', intent.artifact_digest
	   ),
	   'state', intent.state, 'reason_code', intent.reason_code,
	   'created_at', agent_control.runtime_utc_text(intent.created_at)
	 )
	 FROM agent_control.runtime_artifact_publication_intent AS intent
	 JOIN agent_control.runtime_artifact AS artifact
	   ON artifact.artifact_id = intent.artifact_id
	 WHERE artifact.task_id = 'task-command-1'
	) TO STDOUT" >"$ARTIFACT_DIR/publication-intent.json"

artifact_digest=$(tr -d '[:space:]' <"$ARTIFACT_DIR/artifact.digest")
intent_digest=$(tr -d '[:space:]' <"$ARTIFACT_DIR/publication-intent.digest")
(
	cd "$ROOT/agent-platform"
	go run ./cmd/agent-platform validate-contract \
		--file "$ARTIFACT_DIR/artifact.json" \
		--type artifact --expect-digest "$artifact_digest"
) >"$ARTIFACT_DIR/artifact-validation.json"
(
	cd "$ROOT/agent-platform"
	go run ./cmd/agent-platform validate-contract \
		--file "$ARTIFACT_DIR/publication-intent.json" \
		--type artifact_publication_intent --expect-digest "$intent_digest"
) >"$ARTIFACT_DIR/publication-intent-validation.json"

for validation in \
	"$ARTIFACT_DIR/artifact-validation.json" \
	"$ARTIFACT_DIR/publication-intent-validation.json"
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
	'{"status":"PASS","probe":"agent-attempt-terminalization","effect_ceiling":"none","public_commands":8,"processing_commands":0}' \
	>"$ARTIFACT_DIR/summary.json"
printf '%s\n' \
	'<?xml version="1.0" encoding="UTF-8"?>' \
	'<testsuite name="agent-attempt-terminalization" tests="1" failures="0"><testcase name="postgresql-attempt-terminalization"/></testsuite>' \
	>"$ARTIFACT_DIR/junit.xml"

echo "PASS probe=agent-attempt-terminalization effect_ceiling=none artifacts=$ARTIFACT_DIR"
