#!/bin/sh
# Runs AP0 Blob metadata/role probes in a disposable PostgreSQL container.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap0-blob-test-$$"
ARTIFACT_DIR=${AGENT_BLOB_PROBE_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-blob-probe}
IMAGE=${AGENT_BLOB_PROBE_IMAGE:-postgres:16-alpine}

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

mkdir -p "$ARTIFACT_DIR"
rm -f "$ARTIFACT_DIR/summary.json" "$ARTIFACT_DIR/junit.xml"
docker run --detach --rm --name "$CONTAINER" \
	--env POSTGRES_PASSWORD=probe --env POSTGRES_DB=probe "$IMAGE" \
	>"$ARTIFACT_DIR/container-id.txt"

ready=false
ready_count=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40; do
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

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/contracts/security/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/roles-install.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/audit/repro/ap0_login_roles.sql" \
	>"$ARTIFACT_DIR/login-fixtures.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/agent-platform/migrations/0002_blob.sql" \
	>"$ARTIFACT_DIR/blob-migration.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/contracts/blob/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/blob-grants.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/audit/repro/ap0_blob.sql" \
	>"$ARTIFACT_DIR/blob-probes.txt" 2>&1

pids=""
for worker in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
	stage_id=$(printf '50000000-0000-4000-8000-%012d' "$worker")
	docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--username control-1 --dbname probe --command \
		"SET ROLE alpheus_agent_control_api;
		 SELECT * FROM blob.begin_stage(
		     '$stage_id', 'control-1', 'application/json', 10,
		     repeat('e', 64), 10, 60, 'control-1'
		 );
		 SELECT blob.record_stage_facts(
		     '$stage_id', 'control-1', repeat('e', 64), 10, 'control-1'
		 );
		 SELECT * FROM blob.commit_stage(
		     '$stage_id', 'control-1', repeat('e', 64), 10,
		     'agent_control', 'raw_document', 'concurrent-$worker', repeat('f', 64), 'control-1'
		 );
		 RESET ROLE;" \
		>"$ARTIFACT_DIR/concurrent-commit-$worker.txt" 2>&1 &
	pids="$pids $!"
done
for pid in $pids; do
	wait "$pid"
done

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT (SELECT count(*) FROM blob.blob_content
	         WHERE content_digest = repeat('e', 64) AND state = 'committed') = 1
	        AND (SELECT count(*) FROM blob.blob_object
	             WHERE content_digest = repeat('e', 64) AND state = 'committed') = 20
	        AND (SELECT count(DISTINCT blob_id) FROM blob.blob_object
	             WHERE content_digest = repeat('e', 64)) = 20;" \
	>"$ARTIFACT_DIR/concurrent-result.txt" 2>&1
if [ "$(tr -d '[:space:]' <"$ARTIFACT_DIR/concurrent-result.txt")" != "t" ]; then
	echo "FAIL reason=concurrent-commit artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"ap0-blob","postgres_image":"%s","concurrent_commits":20}\n' "$IMAGE" \
	>"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap0-blob" tests="6" failures="0"><testcase name="role-isolation"/><testcase name="stage-commit"/><testcase name="acl-read"/><testcase name="retention-gc"/><testcase name="policy-cas"/><testcase name="concurrent-commit"/></testsuite>\n' \
	>"$ARTIFACT_DIR/junit.xml"
echo "PASS probe=ap0-blob artifacts=$ARTIFACT_DIR concurrent_commits=20"
