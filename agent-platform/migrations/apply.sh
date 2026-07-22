#!/bin/sh
# Applies the already-frozen AP0/AP1 database contracts exactly once per
# content digest.  This is an infrastructure bootstrapper only: it never
# creates a Run, claims a Task, calls a model, or grants an external effect.
#
# DATABASE_URL must point at the Agent Platform PostgreSQL cluster with an
# administrator/migrator bootstrap identity.  Application services must use
# their own least-privilege LOGIN profiles after provisioning; this script must
# never be their entrypoint.
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"

root=${AGENT_PLATFORM_ROOT:-/workspace}

psql_run() {
    psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname="$DATABASE_URL" "$@"
}

scalar() {
    value=$(psql_run --tuples-only --no-align --command "$1")
    printf '%s' "$value" | tr -d '[:space:]'
}

run_sql() {
    path=$1
    key=$2
    file="$root/$path"
    if [ ! -r "$file" ]; then
        echo "agent-platform migration missing: $path" >&2
        exit 1
    fi
    digest=$(sha256sum "$file" | awk '{print $1}')
    existing=$(scalar "SELECT digest FROM agent_control.schema_migration WHERE migration_key = '$key'")
    if [ -n "$existing" ]; then
        if [ "$existing" != "$digest" ]; then
            echo "agent-platform migration digest mismatch: $key" >&2
            exit 1
        fi
        echo "agent-platform migration already applied: $key"
        return
    fi
    echo "agent-platform migration applying: $key"
    psql_run --single-transaction --file "$file"
    psql_run --single-transaction --command "INSERT INTO agent_control.schema_migration (migration_key, digest) VALUES ('$key', '$digest')"
}

# The security migration owns its own schema creation.  It may execute only on
# a completely empty Agent Platform namespace.  A partial/manual bootstrap is
# intentionally rejected rather than guessed at or repaired in place.
security_path=contracts/security/v1/permissions/roles.sql
security_digest=$(sha256sum "$root/$security_path" | awk '{print $1}')
schema_exists=$(scalar "SELECT to_regnamespace('agent_control') IS NOT NULL")
ledger_exists=$(scalar "SELECT to_regclass('agent_control.schema_migration') IS NOT NULL")
if [ "$schema_exists" = "f" ] && [ "$ledger_exists" = "f" ]; then
    psql_run --single-transaction --file "$root/$security_path"
    psql_run --single-transaction --command "
        SET ROLE alpheus_agent_migrator;
        CREATE TABLE agent_control.schema_migration (
            migration_key TEXT PRIMARY KEY,
            digest CHAR(64) NOT NULL CHECK (digest ~ '^[0-9a-f]{64}$'),
            applied_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
        );
        REVOKE ALL ON TABLE agent_control.schema_migration FROM PUBLIC;
        RESET ROLE;
        INSERT INTO agent_control.schema_migration (migration_key, digest)
        VALUES ('0000_security_roles', '$security_digest');
    "
elif [ "$schema_exists" = "t" ] && [ "$ledger_exists" = "t" ]; then
    existing_security=$(scalar "SELECT digest FROM agent_control.schema_migration WHERE migration_key = '0000_security_roles'")
    if [ "$existing_security" != "$security_digest" ]; then
        echo "agent-platform migration digest mismatch: 0000_security_roles" >&2
        exit 1
    fi
else
    echo "agent-platform migration bootstrap state is incomplete; refusing repair" >&2
    exit 1
fi

run_sql agent-platform/migrations/0001_delivery.sql 0001_delivery
run_sql contracts/delivery/v1/permissions/roles.sql 0001_delivery_grants
run_sql agent-platform/migrations/0002_blob.sql 0002_blob
run_sql contracts/blob/v1/permissions/roles.sql 0002_blob_grants
run_sql agent-platform/migrations/0003_governance.sql 0003_governance
run_sql contracts/governance/v1/permissions/roles.sql 0003_governance_grants
run_sql agent-platform/migrations/0004_ap1_runtime_definitions.sql 0004_ap1_runtime_definitions
run_sql agent-platform/migrations/0005_ap1_runtime_state.sql 0005_ap1_runtime_state
run_sql agent-platform/migrations/0006_ap1_command_leases.sql 0006_ap1_command_leases
run_sql agent-platform/migrations/0007_ap1_model_calls.sql 0007_ap1_model_calls
run_sql agent-platform/migrations/0008_ap1_attempt_terminalization.sql 0008_ap1_attempt_terminalization
run_sql agent-platform/migrations/0009_ap1_child_task_requests.sql 0009_ap1_child_task_requests
run_sql agent-platform/migrations/0010_ap1_cancellation_submission.sql 0010_ap1_cancellation_submission
run_sql agent-platform/migrations/0011_ap2_input_facts.sql 0011_ap2_input_facts
run_sql agent-platform/migrations/0012_ap2_submit_user_request.sql 0012_ap2_submit_user_request
run_sql agent-platform/migrations/0013_ap2_blob_adapter.sql 0013_ap2_blob_adapter
run_sql agent-platform/migrations/0014_cortex_run_admission.sql 0014_cortex_run_admission
run_sql agent-platform/migrations/0015_runtime_deferred_guard_identity.sql 0015_runtime_deferred_guard_identity
run_sql agent-platform/migrations/0016_cortex_worker_bridge.sql 0016_cortex_worker_bridge
run_sql agent-platform/migrations/0017_cortex_worker_blob_acl.sql 0017_cortex_worker_blob_acl
run_sql agent-platform/migrations/0018_cortex_run_result.sql 0018_cortex_run_result
run_sql agent-platform/migrations/0019_cortex_run_result_fix.sql 0019_cortex_run_result_fix
run_sql agent-platform/migrations/0020_cortex_output_validation.sql 0020_cortex_output_validation
run_sql agent-platform/migrations/0021_cortex_ai_handoffs.sql 0021_cortex_ai_handoffs
run_sql agent-platform/migrations/0022_cortex_workflow_schema_fix.sql 0022_cortex_workflow_schema_fix
run_sql agent-platform/migrations/0023_cortex_web_fetch_tool.sql 0023_cortex_web_fetch_tool
run_sql agent-platform/migrations/0024_cortex_tool_authorization_lease.sql 0024_cortex_tool_authorization_lease

echo "agent-platform migration bootstrap complete"
