#!/bin/sh
# Runs AP0 platform/effect governance role and CAS probes in disposable PostgreSQL.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap0-governance-test-$$"
ARTIFACT_DIR=${AGENT_GOVERNANCE_PROBE_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-governance-probe}
IMAGE=${AGENT_GOVERNANCE_PROBE_IMAGE:-postgres:16-alpine}

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
	--username postgres --dbname probe <"$ROOT/agent-platform/migrations/0003_governance.sql" \
	>"$ARTIFACT_DIR/governance-migration.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/contracts/governance/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/governance-grants.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe <"$ROOT/audit/repro/ap0_governance.sql" \
	>"$ARTIFACT_DIR/governance-probes.txt" 2>&1

if ! grep -q '^ ap0-governance-base-pass$' "$ARTIFACT_DIR/governance-probes.txt"; then
	echo "FAIL reason=governance-base-probe artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

pids=""
for worker in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
	docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--username postgres --dbname probe --command \
		"SET ROLE alpheus_agent_activator;
		 SELECT * FROM platform_governance.activate_head(
		     '20000000-0000-4000-8000-000000000003', 2, 'activator-$worker'
		 );
		 RESET ROLE;" \
		>"$ARTIFACT_DIR/concurrent-activation-$worker.txt" 2>&1 &
	pids="$pids $!"
	docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--username postgres --dbname probe --command \
		"SET ROLE alpheus_agent_activator;
		 SELECT * FROM platform_governance.activate_head(
		     '20000000-0000-4000-8000-000000000301', 0, 'activator-$worker'
		 );
		 RESET ROLE;" \
		>"$ARTIFACT_DIR/concurrent-bootstrap-$worker.txt" 2>&1 &
	pids="$pids $!"
done
for pid in $pids; do
	wait "$pid"
done

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT (SELECT generation = 3 AND mode = 'read_only'
	         FROM platform_governance.platform_mode_head WHERE head_id = 'global')
	        AND (SELECT count(*) = 1 FROM platform_governance.governance_event
	             WHERE subject_kind = 'platform_mode' AND generation = 3)
	        AND (SELECT count(*) = 1 FROM platform_governance.activation_receipt_consumption
	             WHERE receipt_id = '20000000-0000-4000-8000-000000000003')
	        AND (SELECT generation = 1 AND state = 'enabled'
	             FROM platform_governance.kill_switch_head WHERE switch_id = 'strategy_activation')
	        AND (SELECT count(*) = 1 FROM platform_governance.governance_event
	             WHERE subject_kind = 'kill_switch' AND subject_id = 'strategy_activation' AND generation = 1)
	        AND (SELECT count(*) = 1 FROM platform_governance.activation_receipt_consumption
	             WHERE receipt_id = '20000000-0000-4000-8000-000000000301');" \
	>"$ARTIFACT_DIR/concurrent-result.txt" 2>&1
if [ "$(tr -d '[:space:]' <"$ARTIFACT_DIR/concurrent-result.txt")" != "t" ]; then
	echo "FAIL reason=concurrent-cas artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"ap0-governance","postgres_image":"%s","concurrent_activations_per_subject":20}\n' "$IMAGE" \
	>"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap0-governance" tests="7" failures="0"><testcase name="role-isolation"/><testcase name="typed-heads"/><testcase name="receipt-single-use"/><testcase name="emergency-halt"/><testcase name="stale-generation"/><testcase name="concurrent-cas"/><testcase name="concurrent-bootstrap"/></testsuite>\n' \
	>"$ARTIFACT_DIR/junit.xml"
echo "PASS probe=ap0-governance artifacts=$ARTIFACT_DIR concurrent_activations_per_subject=20"
