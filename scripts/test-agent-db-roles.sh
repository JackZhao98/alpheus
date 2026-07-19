#!/bin/sh
# Runs AP0 delivery/role probes in a disposable PostgreSQL container.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap0-db-role-test-$$"
ARTIFACT_DIR=${AGENT_DB_PROBE_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-db-probe}
IMAGE=${AGENT_DB_PROBE_IMAGE:-postgres:16-alpine}

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
	--username postgres --dbname probe \
	<"$ROOT/contracts/security/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/roles-install.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe \
	<"$ROOT/audit/repro/ap0_login_roles.sql" \
	>"$ARTIFACT_DIR/login-roles-install.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe \
	<"$ROOT/agent-platform/migrations/0001_delivery.sql" \
	>"$ARTIFACT_DIR/delivery-migration.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe \
	<"$ROOT/contracts/delivery/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/delivery-grants.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe \
	<"$ROOT/audit/repro/ap0_delivery_roles.sql" \
	>"$ARTIFACT_DIR/role-probes.txt" 2>&1

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username control-1 --dbname probe --command \
	"SET ROLE alpheus_agent_control_api; SELECT agent_control.enqueue_outbox(
	    'concurrent-' || value, 'concurrent', 'agent_control', 100 + value,
	    'probe_event', repeat('e', 64), 'concurrent-cause', 'concurrent-run',
	    jsonb_build_object('value', value), clock_timestamp(), clock_timestamp()
	 ) FROM generate_series(1, 20) AS value; RESET ROLE;" \
	>"$ARTIFACT_DIR/concurrent-enqueue.txt" 2>&1

pids=""
for worker in 1 2 3 4 5 6 7 8; do
	docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--username dispatcher-1 --dbname probe --command \
		"SET ROLE alpheus_agent_delivery_dispatcher; SELECT event_id FROM agent_control.claim_outbox(
		    'concurrent-dispatcher-$worker', 'concurrent', 20, 30
		 ); RESET ROLE;" \
		>"$ARTIFACT_DIR/concurrent-claim-$worker.txt" 2>&1 &
	pids="$pids $!"
done
for pid in $pids; do
	wait "$pid"
done

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT count(*) = 20
	        AND count(DISTINCT lease_token) = 20
	        AND min(attempt_count) = 1
	        AND max(attempt_count) = 1
	 FROM agent_control.delivery_outbox
	 WHERE destination = 'concurrent' AND state = 'leased';" \
	>"$ARTIFACT_DIR/concurrent-result.txt" 2>&1

if [ "$(tr -d '[:space:]' <"$ARTIFACT_DIR/concurrent-result.txt")" != "t" ]; then
	echo "FAIL reason=concurrent-claim artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"ap0-delivery-roles","postgres_image":"%s","session_identity":true,"inbox_envelope_binding":true,"inbox_active_lease_fence":true,"policy_actor_binding":true,"concurrent_events":20,"claimers":8}\n' "$IMAGE" \
	>"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap0-delivery-roles" tests="9" failures="0"><testcase name="role-isolation"/><testcase name="session-identity"/><testcase name="inbox-envelope-binding"/><testcase name="inbox-cross-destination-denied"/><testcase name="inbox-unclaimed-denied"/><testcase name="policy-actor-binding"/><testcase name="idempotency"/><testcase name="quarantine-replay"/><testcase name="concurrent-claim"/></testsuite>\n' \
	>"$ARTIFACT_DIR/junit.xml"
echo "PASS probe=ap0-delivery-roles artifacts=$ARTIFACT_DIR concurrent_events=20 claimers=8"
