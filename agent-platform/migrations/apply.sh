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
run_sql agent-platform/migrations/0025_cortex_conversation_history.sql 0025_cortex_conversation_history
run_sql agent-platform/migrations/0026_cortex_tool_recovery.sql 0026_cortex_tool_recovery
run_sql agent-platform/migrations/0027_cortex_tool_recovery_claim_fix.sql 0027_cortex_tool_recovery_claim_fix
run_sql agent-platform/migrations/0028_cortex_scout_child_admission.sql 0028_cortex_scout_child_admission
run_sql agent-platform/migrations/0029_cortex_scout_continuation.sql 0029_cortex_scout_continuation
run_sql agent-platform/migrations/0030_cortex_scout_continuation_list_fix.sql 0030_cortex_scout_continuation_list_fix
run_sql agent-platform/migrations/0031_cortex_session_continuation_generation.sql 0031_cortex_session_continuation_generation
run_sql agent-platform/migrations/0032_cortex_scout_memo_artifact_binding.sql 0032_cortex_scout_memo_artifact_binding
run_sql agent-platform/migrations/0033_cortex_scout_trace.sql 0033_cortex_scout_trace
run_sql agent-platform/migrations/0034_cortex_scout_trace_progress.sql 0034_cortex_scout_trace_progress
run_sql agent-platform/migrations/0035_cortex_expired_model_recovery.sql 0035_cortex_expired_model_recovery
run_sql agent-platform/migrations/0036_cortex_scout_terminal_reconciliation.sql 0036_cortex_scout_terminal_reconciliation
run_sql agent-platform/migrations/0037_gexbot_research_provider.sql 0037_gexbot_research_provider
run_sql agent-platform/migrations/0038_gexbot_provider_identity_grant.sql 0038_gexbot_provider_identity_grant
run_sql agent-platform/migrations/0039_gexbot_blob_reference_owner.sql 0039_gexbot_blob_reference_owner
run_sql agent-platform/migrations/0040_gexbot_legacy_temporal_repair.sql 0040_gexbot_legacy_temporal_repair
run_sql agent-platform/migrations/0041_gexbot_blob_resume.sql 0041_gexbot_blob_resume
run_sql agent-platform/migrations/0042_cortex_gexbot_as_of_tool.sql 0042_cortex_gexbot_as_of_tool
run_sql agent-platform/migrations/0043_cortex_gexbot_workflow_contract.sql 0043_cortex_gexbot_workflow_contract
run_sql agent-platform/migrations/0044_cortex_gexbot_json_parentheses_fix.sql 0044_cortex_gexbot_json_parentheses_fix
run_sql agent-platform/migrations/0045_cortex_gexbot_decimal_metrics.sql 0045_cortex_gexbot_decimal_metrics
run_sql agent-platform/migrations/0046_cortex_kernel_earnings_results_tool.sql 0046_cortex_kernel_earnings_results_tool
run_sql agent-platform/migrations/0047_cortex_kernel_earnings_workflow_contract.sql 0047_cortex_kernel_earnings_workflow_contract
run_sql agent-platform/migrations/0048_moody_blues_gexbot_collection_status.sql 0048_moody_blues_gexbot_collection_status
run_sql agent-platform/migrations/0049_cortex_kernel_earnings_json_parentheses_fix.sql 0049_cortex_kernel_earnings_json_parentheses_fix
run_sql agent-platform/migrations/0050_cortex_kernel_read_tools.sql 0050_cortex_kernel_read_tools
run_sql agent-platform/migrations/0051_cortex_kernel_read_workflow_contract.sql 0051_cortex_kernel_read_workflow_contract
run_sql agent-platform/migrations/0052_cortex_kernel_read_result_digest_fix.sql 0052_cortex_kernel_read_result_digest_fix
run_sql agent-platform/migrations/0053_cortex_agent_role_registry.sql 0053_cortex_agent_role_registry
run_sql agent-platform/migrations/0054_cortex_specialist_tool_grants.sql 0054_cortex_specialist_tool_grants
run_sql agent-platform/migrations/0055_cortex_specialist_handoffs.sql 0055_cortex_specialist_handoffs
run_sql agent-platform/migrations/0056_cortex_specialist_tool_enforcement.sql 0056_cortex_specialist_tool_enforcement
run_sql agent-platform/migrations/0057_cortex_specialist_trace.sql 0057_cortex_specialist_trace
run_sql agent-platform/migrations/0058_cortex_specialist_workflow_contract.sql 0058_cortex_specialist_workflow_contract
run_sql agent-platform/migrations/0059_cortex_specialist_trace_attempt_join.sql 0059_cortex_specialist_trace_attempt_join
run_sql agent-platform/migrations/0060_cortex_gexbot_live_tool.sql 0060_cortex_gexbot_live_tool
run_sql agent-platform/migrations/0061_cortex_gexbot_live_workflow_contract.sql 0061_cortex_gexbot_live_workflow_contract
run_sql agent-platform/migrations/0062_cortex_gexbot_live_json_parentheses_fix.sql 0062_cortex_gexbot_live_json_parentheses_fix
run_sql agent-platform/migrations/0063_cortex_gexbot_live_decimal_metrics.sql 0063_cortex_gexbot_live_decimal_metrics
run_sql agent-platform/migrations/0064_cortex_task_graph_storage.sql 0064_cortex_task_graph_storage
run_sql agent-platform/migrations/0065_cortex_task_graph_admission.sql 0065_cortex_task_graph_admission
run_sql agent-platform/migrations/0066_cortex_conversation_binding_replay.sql 0066_cortex_conversation_binding_replay
run_sql agent-platform/migrations/0067_cortex_conversation_binding_replay_fix.sql 0067_cortex_conversation_binding_replay_fix
run_sql agent-platform/migrations/0068_cortex_task_graph_node_sessions.sql 0068_cortex_task_graph_node_sessions
run_sql agent-platform/migrations/0069_cortex_task_graph_worker_discovery.sql 0069_cortex_task_graph_worker_discovery
run_sql agent-platform/migrations/0070_cortex_task_graph_parallelism.sql 0070_cortex_task_graph_parallelism
run_sql agent-platform/migrations/0071_cortex_task_graph_join_resolution.sql 0071_cortex_task_graph_join_resolution
run_sql agent-platform/migrations/0072_cortex_task_graph_join_failure_contract.sql 0072_cortex_task_graph_join_failure_contract
run_sql agent-platform/migrations/0073_cortex_task_graph_decision_desk_discovery.sql 0073_cortex_task_graph_decision_desk_discovery
run_sql agent-platform/migrations/0074_cortex_task_graph_result_promotion.sql 0074_cortex_task_graph_result_promotion
run_sql agent-platform/migrations/0075_cortex_task_graph_parent_session_terminalization.sql 0075_cortex_task_graph_parent_session_terminalization
run_sql agent-platform/migrations/0076_cortex_task_graph_tool_execution_preconditions.sql 0076_cortex_task_graph_tool_execution_preconditions
run_sql agent-platform/migrations/0077_cortex_task_graph_tool_worker_discovery.sql 0077_cortex_task_graph_tool_worker_discovery
run_sql agent-platform/migrations/0078_cortex_task_graph_tool_grant_enforcement.sql 0078_cortex_task_graph_tool_grant_enforcement
run_sql agent-platform/migrations/0079_cortex_task_graph_proposal_contract.sql 0079_cortex_task_graph_proposal_contract
run_sql agent-platform/migrations/0080_cortex_intermediate_model_contracts.sql 0080_cortex_intermediate_model_contracts
run_sql agent-platform/migrations/0081_cortex_task_graph_proposal_context.sql 0081_cortex_task_graph_proposal_context
run_sql agent-platform/migrations/0082_cortex_task_graph_proposal_schema_replay.sql 0082_cortex_task_graph_proposal_schema_replay
run_sql agent-platform/migrations/0083_cortex_intermediate_manifest_guard.sql 0083_cortex_intermediate_manifest_guard
run_sql agent-platform/migrations/0084_cortex_intermediate_contract_definer.sql 0084_cortex_intermediate_contract_definer
run_sql agent-platform/migrations/0085_cortex_manifest_guard_definer.sql 0085_cortex_manifest_guard_definer
run_sql agent-platform/migrations/0086_cortex_task_graph_parent_slot_release.sql 0086_cortex_task_graph_parent_slot_release
run_sql agent-platform/migrations/0087_cortex_task_graph_parked_slot.sql 0087_cortex_task_graph_parked_slot
run_sql agent-platform/migrations/0088_cortex_task_graph_trace.sql 0088_cortex_task_graph_trace
run_sql agent-platform/migrations/0089_cortex_expired_run_recovery.sql 0089_cortex_expired_run_recovery
run_sql agent-platform/migrations/0090_cortex_expired_run_trace.sql 0090_cortex_expired_run_trace
run_sql agent-platform/migrations/0091_cortex_task_graph_round_contract.sql 0091_cortex_task_graph_round_contract
run_sql agent-platform/migrations/0092_cortex_task_graph_round_continuation.sql 0092_cortex_task_graph_round_continuation
run_sql agent-platform/migrations/0093_cortex_task_graph_dynamic_round_context.sql 0093_cortex_task_graph_dynamic_round_context
run_sql agent-platform/migrations/0094_cortex_task_graph_round_contract_replay.sql 0094_cortex_task_graph_round_contract_replay
run_sql agent-platform/migrations/0095_cortex_task_graph_round_blob_binding.sql 0095_cortex_task_graph_round_blob_binding
run_sql agent-platform/migrations/0096_cortex_task_graph_round_blob_origin.sql 0096_cortex_task_graph_round_blob_origin
run_sql agent-platform/migrations/0097_cortex_task_graph_round_proposal_contract.sql 0097_cortex_task_graph_round_proposal_contract
run_sql agent-platform/migrations/0098_cortex_task_graph_round_trace.sql 0098_cortex_task_graph_round_trace
run_sql agent-platform/migrations/0099_cortex_operations_health.sql 0099_cortex_operations_health
run_sql agent-platform/migrations/0100_cortex_operations_health_active_tools.sql 0100_cortex_operations_health_active_tools
run_sql agent-platform/migrations/0101_cortex_run_cancellation.sql 0101_cortex_run_cancellation
run_sql agent-platform/migrations/0102_cortex_run_cancellation_replay_fix.sql 0102_cortex_run_cancellation_replay_fix
run_sql agent-platform/migrations/0103_cortex_run_cancellation_trace.sql 0103_cortex_run_cancellation_trace
run_sql agent-platform/migrations/0104_cortex_run_cancellation_trace_state.sql 0104_cortex_run_cancellation_trace_state
run_sql agent-platform/migrations/0105_cortex_agent_rooms.sql 0105_cortex_agent_rooms
run_sql agent-platform/migrations/0106_cortex_attempt_failure_trace.sql 0106_cortex_attempt_failure_trace
run_sql agent-platform/migrations/0107_cortex_decision_trigger_registry.sql 0107_cortex_decision_trigger_registry
run_sql agent-platform/migrations/0108_cortex_decision_trigger_digest_qualifier.sql 0108_cortex_decision_trigger_digest_qualifier
run_sql agent-platform/migrations/0109_cortex_decision_trigger_evaluation.sql 0109_cortex_decision_trigger_evaluation
run_sql agent-platform/migrations/0110_cortex_decision_trigger_evaluation_lock.sql 0110_cortex_decision_trigger_evaluation_lock
run_sql agent-platform/migrations/0111_cortex_decision_trigger_occurrence.sql 0111_cortex_decision_trigger_occurrence
run_sql agent-platform/migrations/0112_cortex_decision_trigger_occurrence_decimal.sql 0112_cortex_decision_trigger_occurrence_decimal
run_sql agent-platform/migrations/0113_cortex_decision_trigger_wake_run.sql 0113_cortex_decision_trigger_wake_run
run_sql agent-platform/migrations/0114_cortex_decision_trigger_wake_recovery.sql 0114_cortex_decision_trigger_wake_recovery
run_sql agent-platform/migrations/0115_cortex_paper_trade_candidate.sql 0115_cortex_paper_trade_candidate
run_sql agent-platform/migrations/0116_cortex_paper_candidate_workflow_contract.sql 0116_cortex_paper_candidate_workflow_contract
run_sql agent-platform/migrations/0117_cortex_paper_candidate_worker_flags.sql 0117_cortex_paper_candidate_worker_flags
run_sql agent-platform/migrations/0118_cortex_paper_candidate_projection.sql 0118_cortex_paper_candidate_projection
run_sql agent-platform/migrations/0119_cortex_paper_candidate_review.sql 0119_cortex_paper_candidate_review
run_sql agent-platform/migrations/0120_cortex_paper_effect_authorization.sql 0120_cortex_paper_effect_authorization
run_sql agent-platform/migrations/0121_cortex_paper_effect_projection.sql 0121_cortex_paper_effect_projection
run_sql agent-platform/migrations/0122_cortex_candidate_intermediate_contract.sql 0122_cortex_candidate_intermediate_contract
run_sql agent-platform/migrations/0123_cortex_paper_decimal_digest.sql 0123_cortex_paper_decimal_digest
run_sql agent-platform/migrations/0124_cortex_paper_sha256.sql 0124_cortex_paper_sha256
run_sql agent-platform/migrations/0125_cortex_paper_effect_trace.sql 0125_cortex_paper_effect_trace
run_sql agent-platform/migrations/0126_cortex_candidate_task_graph_round.sql 0126_cortex_candidate_task_graph_round
run_sql agent-platform/migrations/0127_cortex_gexbot_decision_trigger_sampling.sql 0127_cortex_gexbot_decision_trigger_sampling
run_sql agent-platform/migrations/0128_cortex_decision_trigger_candidate_run.sql 0128_cortex_decision_trigger_candidate_run
run_sql agent-platform/migrations/0129_cortex_decision_trigger_occurrence_recovery.sql 0129_cortex_decision_trigger_occurrence_recovery
run_sql agent-platform/migrations/0130_trigger_occurrence_binding_definer.sql 0130_trigger_occurrence_binding_definer
run_sql agent-platform/migrations/0131_cortex_trigger_task_graph_context.sql 0131_cortex_trigger_task_graph_context
run_sql agent-platform/migrations/0132_cortex_trigger_task_graph_sessions.sql 0132_cortex_trigger_task_graph_sessions
run_sql agent-platform/migrations/0133_cortex_task_graph_session_recovery.sql 0133_cortex_task_graph_session_recovery
run_sql agent-platform/migrations/0134_cortex_trigger_task_graph_rounds.sql 0134_cortex_trigger_task_graph_rounds
run_sql agent-platform/migrations/0135_cortex_candidate_task_graph_round_prepare.sql 0135_cortex_candidate_task_graph_round_prepare
run_sql agent-platform/migrations/0136_cortex_moody_blues_replay_trigger.sql 0136_cortex_moody_blues_replay_trigger
run_sql agent-platform/migrations/0137_cortex_moody_blues_replay_json_fix.sql 0137_cortex_moody_blues_replay_json_fix
run_sql agent-platform/migrations/0138_cortex_moody_blues_replay_decimal_strings.sql 0138_cortex_moody_blues_replay_decimal_strings
run_sql agent-platform/migrations/0139_cortex_moody_blues_replay_row_lock.sql 0139_cortex_moody_blues_replay_row_lock

echo "agent-platform migration bootstrap complete"
