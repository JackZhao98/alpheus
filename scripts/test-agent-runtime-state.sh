#!/bin/sh
# Proves AP1 Runtime state persistence and default-deny isolation in a
# disposable PostgreSQL 16 database. It loads exactly migrations 0001-0005;
# no AP1 command function, scheduler, model call, or money effect is enabled.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
CONTAINER="alpheus-ap1-runtime-state-test-$$"
ARTIFACT_DIR=${AGENT_RUNTIME_STATE_PROBE_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-runtime-state-probe}
IMAGE=${AGENT_RUNTIME_STATE_PROBE_IMAGE:-postgres:16-alpine}

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
	"$ARTIFACT_DIR/security-roles.txt" \
	"$ARTIFACT_DIR/login-fixtures.txt" \
	"$ARTIFACT_DIR/migration-0001.txt" \
	"$ARTIFACT_DIR/migration-0002.txt" \
	"$ARTIFACT_DIR/migration-0003.txt" \
	"$ARTIFACT_DIR/migration-0004.txt" \
	"$ARTIFACT_DIR/migration-0005.txt" \
	"$ARTIFACT_DIR/delivery-grants.txt" \
	"$ARTIFACT_DIR/blob-grants.txt" \
	"$ARTIFACT_DIR/governance-grants.txt" \
	"$ARTIFACT_DIR/preflight-absence.txt" \
	"$ARTIFACT_DIR/routines-before-0005.txt" \
	"$ARTIFACT_DIR/routines-after-0005.txt" \
	"$ARTIFACT_DIR/routines-new-0005.txt" \
	"$ARTIFACT_DIR/routines-new-0005-expected.txt" \
	"$ARTIFACT_DIR/state-probes.txt" \
	"$ARTIFACT_DIR/object-inventory.txt" \
	"$ARTIFACT_DIR/object-inventory-expected.txt" \
	"$ARTIFACT_DIR/privilege-inventory.txt"

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
	"$ROOT/audit/repro/ap1_runtime_state.sql"
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

# Keep the migration order explicit so this AP1 proof cannot silently absorb
# authority from a later migration.
for migration in 0001_delivery 0002_blob 0003_governance
do
	docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
		--single-transaction --username postgres --dbname probe \
		<"$ROOT/agent-platform/migrations/$migration.sql" \
		>"$ARTIFACT_DIR/migration-${migration%%_*}.txt" 2>&1
done

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

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/agent-platform/migrations/0004_ap1_runtime_definitions.sql" \
	>"$ARTIFACT_DIR/migration-0004.txt" 2>&1

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT to_regclass('agent_control.runtime_command') IS NULL
	        AND to_regclass('agent_control.runtime_run') IS NULL
	        AND to_regclass('agent_control.runtime_task') IS NULL
	        AND to_regclass('agent_control.runtime_attempt') IS NULL
	        AND to_regclass('agent_control.runtime_event') IS NULL;" \
	>"$ARTIFACT_DIR/preflight-absence.txt" 2>&1
if [ "$(tr -d '[:space:]' <"$ARTIFACT_DIR/preflight-absence.txt")" != "t" ]; then
	echo "FAIL reason=ap1-runtime-state-preexisted artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --field-separator '|' \
	--command \
	"SELECT routine.proname, owner_role.rolname
	   FROM pg_catalog.pg_proc AS routine
	   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
	   JOIN pg_catalog.pg_roles AS owner_role ON owner_role.oid = routine.proowner
	  WHERE namespace.nspname = 'agent_control'
	  ORDER BY routine.proname, routine.oid;" \
	>"$ARTIFACT_DIR/routines-before-0005.txt" 2>&1

docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--single-transaction --username postgres --dbname probe \
	<"$ROOT/agent-platform/migrations/0005_ap1_runtime_state.sql" \
	>"$ARTIFACT_DIR/migration-0005.txt" 2>&1

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --field-separator '|' \
	--command \
	"SELECT routine.proname, owner_role.rolname
	   FROM pg_catalog.pg_proc AS routine
	   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
	   JOIN pg_catalog.pg_roles AS owner_role ON owner_role.oid = routine.proowner
	  WHERE namespace.nspname = 'agent_control'
	  ORDER BY routine.proname, routine.oid;" \
	>"$ARTIFACT_DIR/routines-after-0005.txt" 2>&1
comm -13 "$ARTIFACT_DIR/routines-before-0005.txt" \
	"$ARTIFACT_DIR/routines-after-0005.txt" \
	>"$ARTIFACT_DIR/routines-new-0005.txt"
printf '%s|alpheus_agent_migrator\n' \
	guard_runtime_attempt_lease_update guard_runtime_budget_update \
	guard_runtime_command_update guard_runtime_initial_insert \
	guard_runtime_mutable_columns guard_runtime_session_checkpoint_cas \
	guard_runtime_state_transition guard_runtime_task_budget_slot \
	guard_runtime_turn_update reject_runtime_immutable_mutation \
	runtime_actor_valid runtime_blob_ref_valid runtime_digest_valid \
	runtime_failure_valid runtime_identifier_valid runtime_initial_state_valid \
	runtime_name_valid runtime_record_ref_valid runtime_subject_state_valid \
	runtime_terminal_state runtime_transition_allowed \
	validate_runtime_artifact_section_time validate_runtime_artifact_sections \
	validate_runtime_budget_structure validate_runtime_manifest_contract \
	validate_runtime_run_binding validate_runtime_unresolved_turn_attempt \
	validate_trigger_occurrence_binding \
	>"$ARTIFACT_DIR/routines-new-0005-expected.txt"
if ! cmp -s "$ARTIFACT_DIR/routines-new-0005-expected.txt" \
	"$ARTIFACT_DIR/routines-new-0005.txt"; then
	echo "FAIL reason=runtime-state-routine-inventory artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

# The probe owns its transaction boundaries because several invariants are
# deferred and must be shown to reject at COMMIT.
docker exec --interactive "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe \
	<"$ROOT/audit/repro/ap1_runtime_state.sql" \
	>"$ARTIFACT_DIR/state-probes.txt" 2>&1

for marker in \
	AP1_FINAL2_POSITIVE_FIXTURE_PASS \
	AP1_FINAL2_IMMEDIATE_NEGATIVE_PROBES_PASS \
	AP1_FINAL2_FAIL_CLOSED_TUPLE_PROBES_PASS \
	ap1-runtime-state-pass
do
	if ! grep -q "$marker" "$ARTIFACT_DIR/state-probes.txt"; then
		echo "FAIL reason=state-probe-marker marker=$marker artifacts=$ARTIFACT_DIR" >&2
		exit 1
	fi
done

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --field-separator '|' \
	--command \
	"SELECT relation.relname, owner_role.rolname
	   FROM pg_catalog.pg_class AS relation
	   JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
	   JOIN pg_catalog.pg_roles AS owner_role ON owner_role.oid = relation.relowner
	  WHERE namespace.nspname = 'agent_control'
	    AND relation.relkind = 'r'
	  ORDER BY relation.relname;" \
	>"$ARTIFACT_DIR/object-inventory.txt" 2>&1
printf '%s|alpheus_agent_migrator\n' \
	delivery_inbox delivery_outbox delivery_policy delivery_policy_event \
	delivery_quarantine output_contract_revision runtime_artifact \
	runtime_artifact_publication_intent runtime_artifact_section \
	runtime_attempt runtime_attempt_lease_event runtime_budget_ledger \
	runtime_cancellation_request runtime_checkpoint \
	runtime_checkpoint_preserve_ref runtime_command runtime_event \
	runtime_model_call_manifest runtime_model_call_result runtime_policy_event \
	runtime_policy_head runtime_policy_revision runtime_recovery_record \
	runtime_run runtime_session runtime_task runtime_task_dependency \
	runtime_task_input_ref runtime_turn trigger_occurrence \
	trigger_registration_event trigger_registration_head \
	trigger_registration_revision \
	>"$ARTIFACT_DIR/object-inventory-expected.txt"
if ! cmp -s "$ARTIFACT_DIR/object-inventory-expected.txt" \
	"$ARTIFACT_DIR/object-inventory.txt"; then
	echo "FAIL reason=agent-control-object-inventory artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

docker exec "$CONTAINER" psql --no-psqlrc --set ON_ERROR_STOP=1 \
	--username postgres --dbname probe --tuples-only --no-align --command \
	"SELECT
	   (SELECT count(*)
	      FROM pg_catalog.pg_class AS relation
	      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
	     WHERE namespace.nspname = 'agent_control'
	       AND relation.relkind = 'r'
	       AND relation.relname IN (
	         'runtime_command', 'trigger_occurrence', 'runtime_run',
	         'runtime_budget_ledger', 'runtime_task', 'runtime_task_input_ref',
	         'runtime_task_dependency', 'runtime_session', 'runtime_attempt',
	         'runtime_attempt_lease_event', 'runtime_turn',
	         'runtime_model_call_manifest', 'runtime_model_call_result',
	         'runtime_artifact', 'runtime_artifact_section',
	         'runtime_artifact_publication_intent', 'runtime_checkpoint',
	         'runtime_checkpoint_preserve_ref', 'runtime_cancellation_request',
	         'runtime_recovery_record', 'runtime_event'
	       )
	       AND (
	         EXISTS (
	           SELECT 1 FROM aclexplode(COALESCE(
	             relation.relacl, acldefault('r', relation.relowner)
	           )) AS acl
	           WHERE acl.grantee <> relation.relowner
	         )
	         OR
	         has_table_privilege('alpheus_agent_control_api', relation.oid,
	           'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
	         OR has_table_privilege('alpheus_agent_worker', relation.oid,
	           'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
	       )),
	   (SELECT count(*)
	      FROM pg_catalog.pg_proc AS routine
	      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = routine.pronamespace
	     WHERE namespace.nspname = 'agent_control'
	       AND routine.proname IN (
	         'runtime_identifier_valid', 'runtime_name_valid',
	         'runtime_digest_valid', 'runtime_actor_valid',
	         'runtime_record_ref_valid', 'runtime_failure_valid',
	         'runtime_blob_ref_valid', 'reject_runtime_immutable_mutation',
	         'guard_runtime_mutable_columns', 'runtime_transition_allowed',
	         'runtime_terminal_state', 'guard_runtime_initial_insert',
	         'guard_runtime_state_transition', 'guard_runtime_turn_update',
	         'guard_runtime_command_update', 'validate_trigger_occurrence_binding',
	         'validate_runtime_run_binding', 'guard_runtime_budget_update',
	         'guard_runtime_task_budget_slot', 'validate_runtime_budget_structure',
	         'guard_runtime_attempt_lease_update',
	         'validate_runtime_unresolved_turn_attempt',
	         'validate_runtime_manifest_contract',
	         'validate_runtime_artifact_sections',
	         'validate_runtime_artifact_section_time',
	         'guard_runtime_session_checkpoint_cas',
	         'runtime_subject_state_valid', 'runtime_initial_state_valid'
	       )
	       AND EXISTS (
	         SELECT 1 FROM aclexplode(COALESCE(
	           routine.proacl, acldefault('f', routine.proowner)
	         )) AS acl
	         WHERE acl.grantee <> routine.proowner
	           AND lower(acl.privilege_type) = 'execute'
	       )),
	   (SELECT count(*)
	      FROM pg_catalog.pg_class AS relation
	      JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = relation.relnamespace
	     WHERE namespace.nspname = 'agent_control'
	       AND relation.relkind = 'S'
	       AND relation.relname LIKE 'runtime_%'
	       AND EXISTS (
	         SELECT 1 FROM aclexplode(COALESCE(
	           relation.relacl, acldefault('S', relation.relowner)
	         )) AS acl
	         WHERE acl.grantee <> relation.relowner
	       ));" \
	>"$ARTIFACT_DIR/privilege-inventory.txt" 2>&1
if [ "$(tr -d '[:space:]' <"$ARTIFACT_DIR/privilege-inventory.txt")" != "0|0|0" ]; then
	echo "FAIL reason=runtime-state-default-deny artifacts=$ARTIFACT_DIR" >&2
	exit 1
fi

printf '{"status":"PASS","probe":"ap1-runtime-state","postgres_image":"%s","ap0_migrations":3,"ap1_migrations":["0004","0005"],"runtime_tables":21,"valid_lineage":true,"deferred_constraints":true,"json_null_fail_closed":true,"nullable_tuples_fail_closed":true,"task_slot_history":true,"lease_reclaim_fenced":true,"checkpoint_cas":true,"artifact_result_lineage":true,"direct_table_access":false,"public_execute":false,"effect_ceiling":"none"}\n' \
	"$IMAGE" >"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="ap1-runtime-state" tests="14" failures="0"><testcase name="migration-composition"/><testcase name="positive-lineage"/><testcase name="deferred-invariants"/><testcase name="initial-state"/><testcase name="terminal-immutability"/><testcase name="json-null-fail-closed"/><testcase name="nullable-tuples-fail-closed"/><testcase name="task-slot-history"/><testcase name="lease-reclaim"/><testcase name="unknown-turn"/><testcase name="checkpoint-cas"/><testcase name="artifact-result-lineage"/><testcase name="role-isolation"/><testcase name="no-runtime-effect"/></testsuite>\n' \
	>"$ARTIFACT_DIR/junit.xml"
echo "PASS probe=ap1-runtime-state artifacts=$ARTIFACT_DIR runtime_tables=21"
