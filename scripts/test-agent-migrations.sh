#!/bin/sh
# Proves AP0 migrations compose with the full Kernel schema, preserve public
# tables/data, and can be transactionally abandoned without partial Agent state.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap0-migration-test-$$"
ARTIFACT_DIR=${AGENT_MIGRATION_PROBE_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-migration-probe}
IMAGE=${AGENT_MIGRATION_PROBE_IMAGE:-postgres:16-alpine}

cleanup() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

mkdir -p "$ARTIFACT_DIR"
rm -f "$ARTIFACT_DIR/summary.json" "$ARTIFACT_DIR/junit.xml"

# AP0's compatibility evidence is historical. Keep its migration set explicit
# so later Kernel or Agent Platform migrations cannot silently widen the probe.
KERNEL_MIGRATION_LIST="$ARTIFACT_DIR/ap0-kernel-migrations.txt"
AGENT_MIGRATION_LIST="$ARTIFACT_DIR/ap0-agent-migrations.txt"
printf '%s\n' \
	"$ROOT/db/migrations/0001_init.sql" \
	"$ROOT/db/migrations/0002_operation_idempotency.sql" \
	"$ROOT/db/migrations/0003_execution_entitlements.sql" \
	"$ROOT/db/migrations/0004_orders_fills.sql" \
	"$ROOT/db/migrations/0005_backfill_pre_m29_orders.sql" \
	"$ROOT/db/migrations/0006_m3a_exposure_ledger.sql" \
	"$ROOT/db/migrations/0007_m3c_pnl_breakers.sql" \
	"$ROOT/db/migrations/0008_live_canary_revisions.sql" \
	"$ROOT/db/migrations/0009_live_execution_gate.sql" \
	"$ROOT/db/migrations/0010_live_canary_authority.sql" \
	"$ROOT/db/migrations/0011_kernel_policy_authority.sql" \
	"$ROOT/db/migrations/0012_operation_policy_binding.sql" \
	"$ROOT/db/migrations/0013_execution_policy_envelope.sql" \
	"$ROOT/db/migrations/0014_broker_observations.sql" \
	"$ROOT/db/migrations/0015_pre_effect_manifests.sql" \
	"$ROOT/db/migrations/0016_pre_effect_evaluations.sql" \
	"$ROOT/db/migrations/0017_external_control_lifecycle.sql" \
	"$ROOT/db/migrations/0018_broker_external_reconciliation.sql" \
	"$ROOT/db/migrations/0019_live_canary_day_attestations.sql" \
	>"$KERNEL_MIGRATION_LIST"
printf '%s\n' \
	"$ROOT/agent-platform/migrations/0001_delivery.sql" \
	"$ROOT/agent-platform/migrations/0002_blob.sql" \
	"$ROOT/agent-platform/migrations/0003_governance.sql" \
	>"$AGENT_MIGRATION_LIST"

kernel_expected=$(awk 'END { print NR + 0 }' "$KERNEL_MIGRATION_LIST")
agent_expected=$(awk 'END { print NR + 0 }' "$AGENT_MIGRATION_LIST")
if [ "$kernel_expected" -ne 19 ] || [ "$agent_expected" -ne 3 ]; then
	echo "FAIL reason=ap0-migration-scope kernel_migrations=$kernel_expected agent_migrations=$agent_expected artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi
while IFS= read -r migration; do
	if [ ! -f "$migration" ]; then
		echo "FAIL reason=ap0-migration-missing migration=$migration artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done <"$KERNEL_MIGRATION_LIST"
while IFS= read -r migration; do
	if [ ! -f "$migration" ]; then
		echo "FAIL reason=ap0-migration-missing migration=$migration artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done <"$AGENT_MIGRATION_LIST"

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

kernel_count=0
while IFS= read -r migration; do
	kernel_count=$((kernel_count + 1))
	docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--single-transaction --username postgres --dbname probe <"$migration" \
		>"$ARTIFACT_DIR/kernel-migration-$kernel_count.txt" 2>&1
done <"$KERNEL_MIGRATION_LIST"
if [ "$kernel_count" -ne 19 ]; then
	echo "FAIL reason=ap0-kernel-migration-count actual=$kernel_count expected=19 artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --command \
	"CREATE TABLE public.ap0_agent_migration_sentinel (
	     id INTEGER PRIMARY KEY,
	     body TEXT NOT NULL
	 );
	 INSERT INTO public.ap0_agent_migration_sentinel VALUES (1, 'kernel-public-schema-preserved');" \
	>"$ARTIFACT_DIR/sentinel-create.txt" 2>&1
docker exec "$CONTAINER" pg_dump --username postgres --dbname probe \
	--schema-only --schema public --no-owner --no-privileges \
	>"$ARTIFACT_DIR/public-before.sql" 2>&1
docker exec "$CONTAINER" pg_dump --username postgres --dbname probe \
	--data-only --table public.ap0_agent_migration_sentinel --column-inserts \
	>"$ARTIFACT_DIR/sentinel-before.sql" 2>&1

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/contracts/security/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/roles-install.txt" 2>&1
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe \
	<"$ROOT/audit/repro/ap0_login_roles.sql" \
	>"$ARTIFACT_DIR/login-identity-probe.txt" 2>&1

agent_count=0
while IFS= read -r migration; do
	agent_count=$((agent_count + 1))
	docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--single-transaction --username postgres --dbname probe <"$migration" \
		>"$ARTIFACT_DIR/agent-migration-$agent_count.txt" 2>&1
done <"$AGENT_MIGRATION_LIST"
if [ "$agent_count" -ne 3 ]; then
	echo "FAIL reason=ap0-agent-migration-count actual=$agent_count expected=3 artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi
for grants in "$ROOT"/contracts/delivery/v1/permissions/roles.sql \
	"$ROOT"/contracts/blob/v1/permissions/roles.sql \
	"$ROOT"/contracts/governance/v1/permissions/roles.sql; do
	docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--single-transaction --username postgres --dbname probe <"$grants" \
		>"$ARTIFACT_DIR/grants-$(basename "$(dirname "$(dirname "$grants")")").txt" 2>&1
done

docker exec "$CONTAINER" pg_dump --username postgres --dbname probe \
	--schema-only --schema public --no-owner --no-privileges \
	>"$ARTIFACT_DIR/public-after.sql" 2>&1
docker exec "$CONTAINER" pg_dump --username postgres --dbname probe \
	--data-only --table public.ap0_agent_migration_sentinel --column-inserts \
	>"$ARTIFACT_DIR/sentinel-after.sql" 2>&1
for dump in public-before public-after sentinel-before sentinel-after; do
	sed '/^\\restrict /d; /^\\unrestrict /d' "$ARTIFACT_DIR/$dump.sql" \
		>"$ARTIFACT_DIR/$dump.normalized.sql"
done
if ! cmp -s "$ARTIFACT_DIR/public-before.normalized.sql" "$ARTIFACT_DIR/public-after.normalized.sql"; then
	echo "FAIL reason=kernel-public-schema-changed artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi
if ! cmp -s "$ARTIFACT_DIR/sentinel-before.normalized.sql" "$ARTIFACT_DIR/sentinel-after.normalized.sql"; then
	echo "FAIL reason=kernel-public-data-changed artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

docker exec "$CONTAINER" createdb --username postgres rollback_probe
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname rollback_probe \
	<"$ROOT/contracts/security/v1/permissions/roles.sql" \
	>"$ARTIFACT_DIR/rollback-roles.txt" 2>&1
(
	printf 'BEGIN;\n'
	while IFS= read -r migration; do
		sed '/^SET ROLE /d; /^RESET ROLE;/d' "$migration"
	done <"$AGENT_MIGRATION_LIST"
	printf 'ROLLBACK;\n'
	sed '/^\\set /d' "$ROOT/audit/repro/ap0_migration_rollback.sql"
) | docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname rollback_probe \
	>"$ARTIFACT_DIR/rollback-probe.txt" 2>&1
if ! grep -q '^ ap0-agent-migration-rollback-pass$' "$ARTIFACT_DIR/rollback-probe.txt"; then
	echo "FAIL reason=transactional-rollback artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT (SELECT count(*) = 1 FROM public.ap0_agent_migration_sentinel)
	        AND (SELECT count(*) = 4 FROM pg_namespace
	             WHERE nspname IN ('agent_control', 'blob', 'platform_governance', 'platform_security'))
	        AND EXISTS (
	            SELECT 1
	            FROM pg_proc AS routine
	            JOIN pg_namespace AS namespace ON namespace.oid = routine.pronamespace
	            JOIN pg_roles AS owner ON owner.oid = routine.proowner
	            WHERE namespace.nspname = 'platform_security'
	              AND routine.proname = 'invoker_identity'
	              AND routine.prosecdef
	              AND owner.rolname = 'alpheus_agent_migrator'
	        )
	        AND NOT has_function_privilege(
	            'worker-1', 'platform_security.invoker_identity()', 'EXECUTE'
	        )
	        AND NOT EXISTS (
	            SELECT 1 FROM pg_roles WHERE rolname LIKE 'alpheus_%'
	              AND (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls)
	        );" \
	>"$ARTIFACT_DIR/final-result.txt" 2>&1
if [ "$(tr -d '[:space:]' <"$ARTIFACT_DIR/final-result.txt")" != "t" ]; then
	echo "FAIL reason=final-compatibility-state artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"ap0-migration-compatibility","kernel_migrations":%s,"agent_migrations":%s,"public_schema_preserved":true,"transactional_rollback":true,"login_identity":true}\n' \
	"$kernel_count" "$agent_count" >"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap0-migration-compatibility" tests="5" failures="0"><testcase name="kernel-forward-migrations"/><testcase name="agent-forward-migrations"/><testcase name="public-schema-preserved"/><testcase name="transactional-rollback"/><testcase name="login-identity"/></testsuite>\n' \
	>"$ARTIFACT_DIR/junit.xml"
echo "PASS probe=ap0-migration-compatibility artifacts=$ARTIFACT_DIR kernel_migrations=$kernel_count agent_migrations=$agent_count"
