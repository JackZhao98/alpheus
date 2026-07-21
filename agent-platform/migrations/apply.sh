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

run_sql() {
    path=$1
    key=$2
    file="$root/$path"
    if [ ! -r "$file" ]; then
        echo "agent-platform migration missing: $path" >&2
        exit 1
    fi
    digest=$(sha256sum "$file" | awk '{print $1}')
    existing=$(psql_run --tuples-only --no-align --command "SELECT digest FROM agent_control.schema_migration WHERE migration_key = '$key'" | tr -d '[:space:]')
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
schema_exists=$(psql_run --tuples-only --no-align --command "SELECT to_regnamespace('agent_control') IS NOT NULL" | tr -d '[:space:]')
ledger_exists=$(psql_run --tuples-only --no-align --command "SELECT to_regclass('agent_control.schema_migration') IS NOT NULL" | tr -d '[:space:]')
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
    existing_security=$(psql_run --tuples-only --no-align --command "SELECT digest FROM agent_control.schema_migration WHERE migration_key = '0000_security_roles'" | tr -d '[:space:]')
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

echo "agent-platform migration bootstrap complete"
