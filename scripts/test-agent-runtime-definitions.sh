#!/bin/sh
# Proves AP1 immutable definition storage and default-deny role isolation in a
# disposable PostgreSQL 16 database. It intentionally does not load AP1 state
# or command migrations, so 0004 cannot acquire Runtime behavior by accident.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap1-runtime-definitions-test-$$"
ARTIFACT_DIR=${AGENT_RUNTIME_DEFINITIONS_PROBE_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-runtime-definitions-probe}
IMAGE=${AGENT_RUNTIME_DEFINITIONS_PROBE_IMAGE:-postgres:16-alpine}

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

mkdir -p "$ARTIFACT_DIR"
rm -f \
	"$ARTIFACT_DIR/summary.json" \
	"$ARTIFACT_DIR/junit.xml" \
	"$ARTIFACT_DIR/container-id.txt" \
	"$ARTIFACT_DIR/security-roles.txt" \
	"$ARTIFACT_DIR/login-fixtures.txt" \
	"$ARTIFACT_DIR/migration-0001.txt" \
	"$ARTIFACT_DIR/migration-0002.txt" \
	"$ARTIFACT_DIR/migration-0003.txt" \
	"$ARTIFACT_DIR/delivery-grants.txt" \
	"$ARTIFACT_DIR/blob-grants.txt" \
	"$ARTIFACT_DIR/governance-grants.txt" \
	"$ARTIFACT_DIR/preflight-absence.txt" \
	"$ARTIFACT_DIR/migration-0004.txt" \
	"$ARTIFACT_DIR/definition-probes.txt" \
	"$ARTIFACT_DIR/object-inventory.txt"

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
	"$ROOT/audit/repro/ap1_runtime_definitions.sql"
do
	if [ ! -f "$required_file" ]; then
		echo "FAIL reason=required-file-missing file=$required_file artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done

docker run --detach --rm --name "$CONTAINER" \
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

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/contracts/security/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/security-roles.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap0_login_roles.sql" \
	>"$ARTIFACT_DIR/login-fixtures.txt" 2>&1

# Keep the migration order explicit. This is an AP1 probe, not a wildcard that
# silently absorbs future migrations into its authority surface.
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/agent-platform/migrations/0001_delivery.sql" \
	>"$ARTIFACT_DIR/migration-0001.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/agent-platform/migrations/0002_blob.sql" \
	>"$ARTIFACT_DIR/migration-0002.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/agent-platform/migrations/0003_governance.sql" \
	>"$ARTIFACT_DIR/migration-0003.txt" 2>&1

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/contracts/delivery/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/delivery-grants.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/contracts/blob/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/blob-grants.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/contracts/governance/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/governance-grants.txt" 2>&1

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT to_regclass('platform_governance.owner_policy_revision') IS NULL
	        AND to_regclass('platform_governance.owner_policy_head') IS NULL
	        AND to_regclass('platform_governance.owner_policy_event') IS NULL
	        AND to_regclass('agent_control.runtime_policy_revision') IS NULL
	        AND to_regclass('agent_control.runtime_policy_head') IS NULL
	        AND to_regclass('agent_control.runtime_policy_event') IS NULL
	        AND to_regclass('agent_control.trigger_registration_revision') IS NULL
	        AND to_regclass('agent_control.trigger_registration_head') IS NULL
	        AND to_regclass('agent_control.trigger_registration_event') IS NULL
	        AND to_regclass('agent_control.output_contract_revision') IS NULL;" \
	>"$ARTIFACT_DIR/preflight-absence.txt" 2>&1
if [ "$(tr -d '[:space:]' <"$ARTIFACT_DIR/preflight-absence.txt")" != "t" ]; then
	echo "FAIL reason=ap1-definitions-preexisted artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/agent-platform/migrations/0004_ap1_runtime_definitions.sql" \
	>"$ARTIFACT_DIR/migration-0004.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_runtime_definitions.sql" \
	>"$ARTIFACT_DIR/definition-probes.txt" 2>&1

if ! grep -q '^ ap1-runtime-definitions-pass$' \
	"$ARTIFACT_DIR/definition-probes.txt"; then
	echo "FAIL reason=definitions-probe-marker artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --field-separator '|' \
	--command \
	"SELECT namespace.nspname, relation.relname, owner_role.rolname
	 FROM pg_catalog.pg_class AS relation
	 JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
	 JOIN pg_catalog.pg_roles AS owner_role ON owner_role.oid = relation.relowner
	 WHERE (namespace.nspname, relation.relname) IN (
	     ('platform_governance', 'owner_policy_revision'),
	     ('platform_governance', 'owner_policy_head'),
	     ('platform_governance', 'owner_policy_event'),
	     ('agent_control', 'runtime_policy_revision'),
	     ('agent_control', 'runtime_policy_head'),
	     ('agent_control', 'runtime_policy_event'),
	     ('agent_control', 'trigger_registration_revision'),
	     ('agent_control', 'trigger_registration_head'),
	     ('agent_control', 'trigger_registration_event'),
	     ('agent_control', 'output_contract_revision')
	 ) AND relation.relkind = 'r'
	 ORDER BY namespace.nspname, relation.relname;" \
	>"$ARTIFACT_DIR/object-inventory.txt" 2>&1
if [ "$(wc -l <"$ARTIFACT_DIR/object-inventory.txt" | tr -d '[:space:]')" -ne 10 ]; then
	echo "FAIL reason=definition-object-inventory artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi
if grep -v '|alpheus_agent_migrator$' "$ARTIFACT_DIR/object-inventory.txt" \
	>/dev/null 2>&1; then
	echo "FAIL reason=definition-object-owner artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"ap1-runtime-definitions","postgres_image":"%s","ap0_migrations":3,"ap1_migration":"0004","definition_tables":10,"valid_fixtures":true,"recovery_policy_rejected":true,"parser_ceilings_rejected":true,"immutable_revisions_events":true,"exact_local_refs":true,"exact_owner_policy_binding":true,"direct_table_access":false,"runtime_effects_granted":false}\n' \
	"$IMAGE" >"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap1-runtime-definitions" tests="12" failures="0"><testcase name="migration-composition"/><testcase name="owner-policy-fixture"/><testcase name="runtime-policy-fixture"/><testcase name="trigger-registration-fixture"/><testcase name="output-contract-fixture"/><testcase name="recovery-policy-rejected"/><testcase name="parser-ceilings"/><testcase name="immutable-revisions-events"/><testcase name="exact-local-reference"/><testcase name="owner-policy-existence-kind"/><testcase name="role-isolation"/><testcase name="no-runtime-effect"/></testsuite>\n' \
	>"$ARTIFACT_DIR/junit.xml"
echo "PASS probe=ap1-runtime-definitions artifacts=$ARTIFACT_DIR definition_tables=10"
