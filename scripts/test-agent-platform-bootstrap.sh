#!/bin/sh
# Verifies the AP0/AP1 migration bootstrapper in a disposable PostgreSQL 16
# database.  The first execution installs the frozen database surface; the
# second must validate every digest and replay no DDL.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-agent-platform-bootstrap-test-$$"
ARTIFACT_DIR=${AGENT_PLATFORM_BOOTSTRAP_PROBE_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-platform-bootstrap-probe}
IMAGE=${AGENT_PLATFORM_BOOTSTRAP_PROBE_IMAGE:-postgres:16-alpine}

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

mkdir -p "$ARTIFACT_DIR"
docker run --detach --rm --name "$CONTAINER" \
	--env POSTGRES_PASSWORD=probe --env POSTGRES_DB=probe "$IMAGE" \
	>"$ARTIFACT_DIR/container-id.txt"

ready=false
ready_count=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
	if docker exec "$CONTAINER" pg_isready --username postgres --dbname probe >/dev/null 2>&1; then
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
	echo "FAIL reason=postgres_not_ready artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

run_bootstrap() {
	docker run --rm --network "container:$CONTAINER" \
		--env 'DATABASE_URL=postgresql://postgres:probe@127.0.0.1:5432/probe?sslmode=disable' \
		--env AGENT_PLATFORM_ROOT=/workspace \
		--volume "$ROOT:/workspace:ro" "$IMAGE" \
		/bin/sh /workspace/agent-platform/migrations/apply.sh
}

run_bootstrap >"$ARTIFACT_DIR/first.txt"
run_bootstrap >"$ARTIFACT_DIR/second.txt"

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/audit/repro/ap0_login_roles.sql" \
	>"$ARTIFACT_DIR/login-fixtures.txt"
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/audit/repro/ap2_input_facts.sql" \
	>"$ARTIFACT_DIR/input-facts.txt"
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/audit/repro/ap2_submit_user_request.sql" \
	>"$ARTIFACT_DIR/submit-user-request.txt"

if ! grep -q 'agent-platform migration already applied: 0120_cortex_paper_effect_authorization' \
	"$ARTIFACT_DIR/second.txt"; then
	echo "FAIL reason=second_execution_replayed_or_incomplete artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

count=$(docker exec "$CONTAINER" psql --no-psqlrc --username postgres --dbname probe \
	--tuples-only --no-align --command 'SELECT count(*) FROM agent_control.schema_migration' \
	| tr -d '[:space:]')
if [ "$count" != 124 ]; then
	echo "FAIL reason=migration_ledger_count count=$count artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"agent-platform-bootstrap","migration_files":120,"ledger_entries":124,"second_execution":"digest-verified-no-ddl-replay"}\n' \
	>"$ARTIFACT_DIR/summary.json"
cat "$ARTIFACT_DIR/summary.json"
