-- AP1 model-call transaction proof. All calls execute as a real Worker LOGIN,
-- while fixture/verification work executes only as the migrator. No external
-- model, Kernel, Provider, operation, or broker path exists in this probe.
\set ON_ERROR_STOP on

RESET ROLE;
SET ROLE alpheus_agent_migrator;

-- Seed exact committed Blob metadata without granting the Worker table access.
-- Production bytes still go through the AP0 local BlobStore; this SQL helper
-- creates only disposable metadata needed to prove the command boundary.
CREATE FUNCTION pg_temp.seed_model_blob(
    p_blob_id UUID,
    p_content_digest TEXT,
    p_size BIGINT,
    p_origin_type TEXT,
    p_origin_id TEXT,
    p_origin_digest TEXT,
    p_binding_id TEXT,
    p_owner_principal TEXT,
    p_committed_at TIMESTAMPTZ
) RETURNS JSONB
LANGUAGE plpgsql
AS $$
DECLARE
    now_at TIMESTAMPTZ := clock_timestamp();
    stage_id UUID := gen_random_uuid();
BEGIN
    INSERT INTO blob.blob_stage (
        stage_id, principal_id, issuer_owner, media_type,
        max_bytes_snapshot, expected_digest, expected_size_bytes,
        state, content_digest, size_bytes, blob_id,
        created_at, expires_at, committed_at
    ) VALUES (
        stage_id, p_owner_principal, 'agent_control', 'application/json',
        p_size, p_content_digest, p_size,
        'committed', p_content_digest, p_size, p_blob_id,
        p_committed_at - interval '1 second',
        greatest(now_at + interval '1 hour', p_committed_at + interval '1 hour'),
        p_committed_at
    );
    INSERT INTO blob.blob_content (
        content_digest, size_bytes, state, created_at, updated_at
    ) VALUES (
        p_content_digest, p_size, 'committed', p_committed_at, p_committed_at
    );
    INSERT INTO blob.blob_object (
        blob_id, stage_id, content_digest, media_type, size_bytes,
        origin_owner, origin_record_type, origin_record_id,
        origin_record_digest, state, committed_at
    ) VALUES (
        p_blob_id, stage_id, p_content_digest, 'application/json', p_size,
        'agent_control', p_origin_type, p_origin_id,
        p_origin_digest, 'committed', p_committed_at
    );
    INSERT INTO blob.blob_reference (
        binding_id, blob_id, reference_owner, reference_record_type,
        reference_record_id, reference_record_digest, owner_principal,
        access_class, retention_until, state, generation, bound_at
    ) VALUES (
        p_binding_id, p_blob_id, 'agent_control', p_origin_type,
        p_origin_id, p_origin_digest, p_owner_principal,
        'private', now_at + interval '1 hour', 'active', 1, now_at
    );
    RETURN jsonb_build_object(
        'schema_revision', 1,
        'blob_id', p_blob_id::TEXT,
        'content_digest', p_content_digest,
        'media_type', 'application/json',
        'size_bytes', p_size,
        'origin', jsonb_build_object(
            'owner', 'agent_control',
            'record_type', p_origin_type,
            'record_id', p_origin_id,
            'schema_revision', 1,
            'record_digest', p_origin_digest
        ),
        'committed_at', agent_control.runtime_utc_text(p_committed_at)
    );
END
$$;

SELECT pg_temp.seed_model_blob(
    (session.context_manifest->>'blob_id')::UUID,
    session.context_manifest->>'content_digest',
    (session.context_manifest->>'size_bytes')::BIGINT,
    session.context_manifest #>> '{origin,record_type}',
    session.context_manifest #>> '{origin,record_id}',
    session.context_manifest #>> '{origin,record_digest}',
    'binding-model-context-1', 'worker-1',
    (session.context_manifest->>'committed_at')::TIMESTAMPTZ
)::TEXT AS model_context
FROM agent_control.runtime_session AS session
WHERE session.session_id = 'session-command-1'
\gset

-- Refresh the existing executing Attempt before beginning the sequential model
-- lifecycle probes.
SELECT attempt_id AS model_attempt_id,
       state_generation AS model_attempt_generation,
       lease_generation AS model_lease_generation,
       lease_token::TEXT AS model_lease_token
FROM agent_control.runtime_attempt
WHERE task_id = 'task-command-1' AND state = 'executing'
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'heartbeat-model-setup-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'heartbeat_attempt',
        'idempotency_key', 'heartbeat-model-setup-idem-1',
        'request_digest', repeat('3', 64),
        'causation_id', 'cause-model-setup-1',
        'correlation_id', 'correlation-model-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'model_attempt_id',
    'expected_attempt_state_generation', :model_attempt_generation,
    'lease_generation', :model_lease_generation,
    'lease_token', :'model_lease_token',
    'requested_extension_seconds', 60
)::TEXT AS model_heartbeat_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.heartbeat_attempt(:'model_heartbeat_command')::TEXT
    AS model_heartbeat_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT output_contract_digest::TEXT AS model_output_contract_digest
FROM agent_control.runtime_task WHERE task_id = 'task-command-1'
\gset
SELECT consumed_model_calls AS before_calls,
       consumed_input_tokens AS before_input,
       consumed_output_tokens AS before_output,
       consumed_external_cost_micro_usd AS before_cost,
       consumed_wall_time_ms AS before_wall,
       reserved_model_calls AS before_reserved_calls,
       generation AS before_budget_generation
FROM agent_control.runtime_budget_ledger
WHERE ledger_id = 'run-ledger-command-1'
\gset
SELECT generation AS before_leaf_generation
FROM agent_control.runtime_budget_ledger
WHERE ledger_id = 'task-ledger-command-1'
\gset

-- Dispatch reserves worst-case budget and writes both planned and dispatched
-- Turn events before the transaction becomes visible to a model adapter.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'dispatch-model-success-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'dispatch_model_call',
        'idempotency_key', 'dispatch-model-success-idem-1',
        'request_digest', repeat('4', 64),
        'causation_id', 'cause-model-dispatch-success-1',
        'correlation_id', 'correlation-model-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'model_attempt_id',
    'expected_attempt_state_generation', :model_attempt_generation,
    'lease_generation',
        (:'model_heartbeat_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'model_heartbeat_response'::JSONB->>'lease_token',
    'turn_id', 'turn-model-success-1',
    'manifest', jsonb_build_object(
        'call_id', 'call-model-success-1',
        'idempotency_key', 'provider-call-model-success-1',
        'provider', 'fixture-provider', 'model', 'fixture-model',
        'prompt_digest', repeat('5', 64),
        'context_manifest', :'model_context'::JSONB,
        'output_contract_digest', :'model_output_contract_digest',
        'request_digest', repeat('6', 64),
        'max_output_tokens', 20,
        'reserved_input_tokens', 10,
        'reserved_external_cost_micro_usd', 30,
        'timeout_ms', 40,
        'temperature_micros', 0
    )
)::TEXT AS dispatch_success_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.dispatch_model_call(:'dispatch_success_command')::TEXT
    AS dispatch_success_response
\gset
SELECT agent_control.dispatch_model_call(jsonb_set(
    :'dispatch_success_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('dispatch-model-success-retry-1'::TEXT)
)::TEXT)::TEXT AS dispatch_success_retry_response
\gset

\set ON_ERROR_STOP off
SELECT agent_control.dispatch_model_call(jsonb_set(
    :'dispatch_success_command'::JSONB,
    '{manifest,max_output_tokens}', '21'::JSONB
)::TEXT);
SELECT :'SQLSTATE' = '23505' AS dispatch_changed_body_rejected
\gset
SELECT agent_control.dispatch_model_call(
    left(:'dispatch_success_command', length(:'dispatch_success_command') - 1)
    || ',"turn_id":"duplicate-turn"}'
);
SELECT :'SQLSTATE' = '22023' AS dispatch_duplicate_key_rejected
\gset
\set ON_ERROR_STOP on
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'dispatch_success_response'::JSONB->>'status' = 'committed'
    AND :'dispatch_success_response'::JSONB->>'turn_state' = 'dispatched'
    AND (:'dispatch_success_response'::JSONB->>'turn_state_generation')::BIGINT = 2
    AND :'dispatch_success_response'::JSONB = :'dispatch_success_retry_response'::JSONB
    AND :'dispatch_success_response'::JSONB->>'manifest_digest' = (
        SELECT record_digest::TEXT FROM agent_control.runtime_model_call_manifest
        WHERE call_id = 'call-model-success-1'
    )
    AND (SELECT count(*) FROM agent_control.runtime_event
         WHERE subject = 'turn' AND subject_id = 'turn-model-success-1') = 2
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'run-ledger-command-1'
          AND reserved_model_calls = :before_reserved_calls + 1
          AND reserved_input_tokens = 10
          AND reserved_output_tokens = 20
          AND reserved_external_cost_micro_usd = 30
          AND reserved_wall_time_ms = 40
          AND generation = :before_budget_generation + 1
    )
    AND (SELECT generation FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = 'task-ledger-command-1') = :before_leaf_generation
    AND :'dispatch_changed_body_rejected'::BOOLEAN
    AND :'dispatch_duplicate_key_rejected'::BOOLEAN
) AS dispatch_committed_exactly
\gset
\if :dispatch_committed_exactly
\else
    \echo 'FAIL dispatch did not reserve, persist, canonicalize, or replay exactly'
    \quit 1
\endif

-- Simulate the Control Plane committing the model output through AP0 Blob
-- metadata before the Worker presents the resulting BlobRef.
SELECT pg_temp.seed_model_blob(
    '00000000-0000-4000-8000-000000000072', repeat('7', 64), 2,
    'model_call_manifest', 'call-model-success-1',
    :'dispatch_success_response'::JSONB->>'manifest_digest',
    'binding-model-output-success-1', 'worker-1', clock_timestamp()
)::TEXT AS model_output
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'resolve-model-success-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'resolve_model_call',
        'idempotency_key', 'resolve-model-success-idem-1',
        'request_digest', repeat('8', 64),
        'causation_id', 'cause-model-resolve-success-1',
        'correlation_id', 'correlation-model-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'model_attempt_id',
    'expected_attempt_state_generation', :model_attempt_generation,
    'lease_generation',
        (:'model_heartbeat_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'model_heartbeat_response'::JSONB->>'lease_token',
    'turn_id', 'turn-model-success-1',
    'expected_turn_state_generation', 2,
    'outcome', 'result_committed',
    'result', jsonb_build_object(
        'call_id', 'call-model-success-1',
        'request_digest', repeat('6', 64),
        'provider_request_id', 'provider-request-model-success-1',
        'output', :'model_output'::JSONB,
        'input_tokens', 7, 'output_tokens', 11,
        'external_cost_micro_usd', 13, 'wall_time_ms', 17,
        'finish_reason', 'stop'
    )
)::TEXT AS resolve_success_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.resolve_model_call(:'resolve_success_command')::TEXT
    AS resolve_success_response
\gset
SELECT agent_control.resolve_model_call(jsonb_set(
    :'resolve_success_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('resolve-model-success-retry-1'::TEXT)
)::TEXT)::TEXT AS resolve_success_retry_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'resolve_success_response'::JSONB->>'status' = 'committed'
    AND :'resolve_success_response'::JSONB->>'turn_state' = 'result_committed'
    AND :'resolve_success_response'::JSONB = :'resolve_success_retry_response'::JSONB
    AND :'resolve_success_response'::JSONB->>'result_digest' = (
        SELECT record_digest::TEXT FROM agent_control.runtime_model_call_result
        WHERE call_id = 'call-model-success-1'
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-success-1'
          AND state = 'result_committed' AND state_generation = 3
          AND NOT reservation_held AND result_id IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'run-ledger-command-1'
          AND consumed_model_calls = :before_calls + 1
          AND consumed_input_tokens = :before_input + 7
          AND consumed_output_tokens = :before_output + 11
          AND consumed_external_cost_micro_usd = :before_cost + 13
          AND consumed_wall_time_ms = :before_wall + 17
          AND reserved_model_calls = :before_reserved_calls
          AND reserved_input_tokens = 0 AND reserved_output_tokens = 0
          AND reserved_external_cost_micro_usd = 0
          AND reserved_wall_time_ms = 0
          AND generation = :before_budget_generation + 2
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_model_provider_request
        WHERE provider = 'fixture-provider'
          AND provider_request_id = 'provider-request-model-success-1'
          AND call_id = 'call-model-success-1'
    )
) AS resolve_success_committed_exactly
\gset
\if :resolve_success_committed_exactly
\else
    \echo 'FAIL success resolution did not settle and bind exactly once'
    \quit 1
\endif

-- A known failure has no trustworthy provider usage fields in the frozen
-- command, so it conservatively consumes the entire held reservation.
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    :'dispatch_success_command'::JSONB,
                    '{envelope,command_id}',
                    to_jsonb('dispatch-model-failure-1'::TEXT)
                ),
                '{envelope,idempotency_key}',
                to_jsonb('dispatch-model-failure-idem-1'::TEXT)
            ),
            '{envelope,request_digest}', to_jsonb(repeat('9', 64))
        ),
        '{envelope,causation_id}',
        to_jsonb('cause-model-dispatch-failure-1'::TEXT)
    ),
    '{turn_id}', to_jsonb('turn-model-failure-1'::TEXT)
) AS dispatch_failure_base
\gset
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    :'dispatch_failure_base'::JSONB,
                    '{manifest,call_id}', to_jsonb('call-model-failure-1'::TEXT)
                ),
                '{manifest,idempotency_key}',
                to_jsonb('provider-call-model-failure-1'::TEXT)
            ),
            '{manifest,request_digest}', to_jsonb(repeat('a', 64))
        ),
        '{manifest,reserved_input_tokens}', '3'::JSONB
    ),
    '{manifest,max_output_tokens}', '5'::JSONB
) AS dispatch_failure_partial
\gset
SELECT jsonb_set(
    jsonb_set(
        :'dispatch_failure_partial'::JSONB,
        '{manifest,reserved_external_cost_micro_usd}', '7'::JSONB
    ),
    '{manifest,timeout_ms}', '9'::JSONB
)::TEXT AS dispatch_failure_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.dispatch_model_call(:'dispatch_failure_command')::TEXT
    AS dispatch_failure_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'unknown-model-failure-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'mark_model_call_unknown',
        'idempotency_key', 'unknown-model-failure-idem-1',
        'request_digest', repeat('b', 64),
        'causation_id', 'cause-model-unknown-failure-1',
        'correlation_id', 'correlation-model-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'model_attempt_id',
    'expected_attempt_state_generation', :model_attempt_generation,
    'lease_generation',
        (:'model_heartbeat_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'model_heartbeat_response'::JSONB->>'lease_token',
    'turn_id', 'turn-model-failure-1',
    'expected_turn_state_generation', 2,
    'failure', jsonb_build_object(
        'code', 'provider_outcome_unknown',
        'message', 'provider outcome could not be verified',
        'retryable', true
    )
)::TEXT AS mark_unknown_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.mark_model_call_unknown(:'mark_unknown_command')::TEXT
    AS mark_unknown_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'mark_unknown_response'::JSONB->>'turn_state' = 'unknown'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-failure-1'
          AND state = 'unknown' AND state_generation = 3
          AND reservation_held AND finished_at IS NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'run-ledger-command-1'
          AND reserved_model_calls = :before_reserved_calls + 1
          AND reserved_input_tokens = 3 AND reserved_output_tokens = 5
          AND reserved_external_cost_micro_usd = 7
          AND reserved_wall_time_ms = 9
    )
) AS unknown_preserved_reservation
\gset
\if :unknown_preserved_reservation
\else
    \echo 'FAIL unknown model call released or lost its reservation'
    \quit 1
\endif

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'resolve-model-failure-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'resolve_model_call',
        'idempotency_key', 'resolve-model-failure-idem-1',
        'request_digest', repeat('c', 64),
        'causation_id', 'cause-model-resolve-failure-1',
        'correlation_id', 'correlation-model-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'model_attempt_id',
    'expected_attempt_state_generation', :model_attempt_generation,
    'lease_generation',
        (:'model_heartbeat_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'model_heartbeat_response'::JSONB->>'lease_token',
    'turn_id', 'turn-model-failure-1',
    'expected_turn_state_generation', 3,
    'outcome', 'failed',
    'failure', jsonb_build_object(
        'code', 'provider_failure_confirmed',
        'message', 'provider confirmed the call failed',
        'retryable', false
    )
)::TEXT AS resolve_failure_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.resolve_model_call(:'resolve_failure_command')::TEXT
    AS resolve_failure_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'resolve_failure_response'::JSONB->>'turn_state' = 'failed'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-failure-1'
          AND state = 'failed' AND state_generation = 4
          AND NOT reservation_held AND finished_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'run-ledger-command-1'
          AND consumed_model_calls = :before_calls + 2
          AND consumed_input_tokens = :before_input + 10
          AND consumed_output_tokens = :before_output + 16
          AND consumed_external_cost_micro_usd = :before_cost + 20
          AND consumed_wall_time_ms = :before_wall + 26
          AND reserved_model_calls = :before_reserved_calls
          AND reserved_input_tokens = 0 AND reserved_output_tokens = 0
          AND reserved_external_cost_micro_usd = 0
          AND reserved_wall_time_ms = 0
    )
) AS failed_resolution_conservative
\gset
\if :failed_resolution_conservative
\else
    \echo 'FAIL known failure did not consume the held worst-case reservation'
    \quit 1
\endif

-- Oversized reservation and fabricated context references fail closed before
-- creating a Turn or mutating any ledger.
SELECT generation AS before_denial_generation
FROM agent_control.runtime_budget_ledger
WHERE ledger_id = 'run-ledger-command-1'
\gset
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    :'dispatch_success_command'::JSONB,
                    '{envelope,command_id}', to_jsonb('dispatch-model-over-budget-1'::TEXT)
                ),
                '{envelope,idempotency_key}', to_jsonb('dispatch-model-over-budget-idem-1'::TEXT)
            ),
            '{envelope,request_digest}', to_jsonb(repeat('d', 64))
        ),
        '{turn_id}', to_jsonb('turn-model-over-budget-1'::TEXT)
    ),
    '{manifest,call_id}', to_jsonb('call-model-over-budget-1'::TEXT)
) AS over_budget_partial
\gset
SELECT jsonb_set(
    jsonb_set(
        :'over_budget_partial'::JSONB,
        '{manifest,idempotency_key}', to_jsonb('provider-call-over-budget-1'::TEXT)
    ),
    '{manifest,max_output_tokens}', '1000001'::JSONB
)::TEXT AS over_budget_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.dispatch_model_call(:'over_budget_command')::TEXT
    AS over_budget_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'over_budget_response'::JSONB->>'status' = 'denied'
    AND (SELECT generation FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = 'run-ledger-command-1') = :before_denial_generation
    AND NOT EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-over-budget-1'
    )
) AS budget_denial_atomic
\gset
\if :budget_denial_atomic
\else
    \echo 'FAIL over-budget dispatch partially mutated state'
    \quit 1
\endif

-- BIGINT-valid durations must fail as durable command denials, never escape as
-- PostgreSQL datetime/interval overflow and disappear on every retry.
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    :'dispatch_success_command'::JSONB,
                    '{envelope,command_id}',
                    to_jsonb('dispatch-model-huge-timeout-1'::TEXT)
                ),
                '{envelope,idempotency_key}',
                to_jsonb('dispatch-model-huge-timeout-idem-1'::TEXT)
            ),
            '{envelope,request_digest}', to_jsonb(repeat('0', 64))
        ),
        '{envelope,causation_id}',
        to_jsonb('cause-model-huge-timeout-1'::TEXT)
    ),
    '{turn_id}', to_jsonb('turn-model-huge-timeout-1'::TEXT)
) AS huge_timeout_base
\gset
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                :'huge_timeout_base'::JSONB,
                '{manifest,call_id}',
                to_jsonb('call-model-huge-timeout-1'::TEXT)
            ),
            '{manifest,idempotency_key}',
            to_jsonb('provider-call-model-huge-timeout-1'::TEXT)
        ),
        '{manifest,request_digest}', to_jsonb(repeat('1', 64))
    ),
    '{manifest,timeout_ms}', '9223372036854775807'::JSONB
)::TEXT AS huge_timeout_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.dispatch_model_call(:'huge_timeout_command')::TEXT
    AS huge_timeout_response
\gset
SELECT agent_control.dispatch_model_call(:'huge_timeout_command')::TEXT
    AS huge_timeout_retry_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'huge_timeout_response'::JSONB->>'status' = 'denied'
    AND :'huge_timeout_response'::JSONB->>'reason_code'
        = 'model_call_window_unavailable'
    AND :'huge_timeout_response'::JSONB
        = :'huge_timeout_retry_response'::JSONB
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_command
        WHERE command_id = 'dispatch-model-huge-timeout-1'
          AND state = 'denied'
    )
    AND (SELECT generation FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = 'run-ledger-command-1')
        = :before_denial_generation
    AND NOT EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-huge-timeout-1'
    )
) AS huge_timeout_denied_durably
\gset
\if :huge_timeout_denied_durably
\else
    \echo 'FAIL huge timeout escaped durable fail-closed command handling'
    \quit 1
\endif

SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    jsonb_set(
                        :'over_budget_partial'::JSONB,
                        '{envelope,command_id}', to_jsonb('dispatch-model-fake-blob-1'::TEXT)
                    ),
                    '{envelope,idempotency_key}', to_jsonb('dispatch-model-fake-blob-idem-1'::TEXT)
                ),
                '{envelope,request_digest}', to_jsonb(repeat('e', 64))
            ),
            '{turn_id}', to_jsonb('turn-model-fake-blob-1'::TEXT)
        ),
        '{manifest,call_id}', to_jsonb('call-model-fake-blob-1'::TEXT)
    ),
    '{manifest,idempotency_key}', to_jsonb('provider-call-model-fake-blob-1'::TEXT)
) AS fake_blob_partial
\gset
SELECT jsonb_set(
    :'fake_blob_partial'::JSONB,
    '{manifest,context_manifest,blob_id}',
    to_jsonb('00000000-0000-4000-8000-000000000099'::TEXT)
)::TEXT AS fake_blob_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.dispatch_model_call(:'fake_blob_command')::TEXT
    AS fake_blob_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'fake_blob_response'::JSONB->>'status' = 'denied'
    AND NOT EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-fake-blob-1'
    )
) AS fake_blob_denied
\gset
\if :fake_blob_denied
\else
    \echo 'FAIL fabricated context BlobRef entered durable model state'
    \quit 1
\endif

-- Crash-window proof uses its own occurrence, Run, Task, Session, ledgers, and
-- exact AP0-backed context BlobRef. It cannot consume or strand state owned by
-- either the sequential lifecycle or the later concurrency harness.
SELECT pg_temp.seed_model_blob(
    '00000000-0000-4000-8000-000000000073',
    repeat('6', 63) || '1', 2,
    'context_manifest', 'session-model-crash-1', repeat('6', 63) || '2',
    'binding-model-context-crash-1', 'worker-1',
    clock_timestamp() - interval '5 seconds'
)::TEXT AS crash_model_context
\gset

BEGIN;
SET CONSTRAINTS ALL DEFERRED;

INSERT INTO agent_control.trigger_occurrence
SELECT (jsonb_populate_record(
    NULL::agent_control.trigger_occurrence,
    to_jsonb(source_row) || jsonb_build_object(
        'occurrence_id', 'occurrence-model-crash-1',
        'record_digest', repeat('6', 63) || '3',
        'occurrence_key', 'occurrence-model-crash-key-1',
        'occurred_at', clock_timestamp() - interval '4 seconds',
        'observed_at', clock_timestamp() - interval '3 seconds',
        'committed_at', clock_timestamp() - interval '2 seconds'
    )
)).*
FROM agent_control.trigger_occurrence AS source_row
WHERE source_row.occurrence_id = 'occurrence-command-1';

INSERT INTO agent_control.runtime_run
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_run,
    to_jsonb(source_row) || jsonb_build_object(
        'run_id', 'run-model-crash-1',
        'occurrence_id', occurrence.occurrence_id,
        'occurrence_digest', occurrence.record_digest,
        'origin_occurred_at', occurrence.occurred_at,
        'origin_observed_at', occurrence.observed_at,
        'origin_committed_at', occurrence.committed_at,
        'budget_ledger_id', 'run-ledger-model-crash-1',
        'root_task_id', 'task-model-crash-1',
        'state', 'queued', 'state_generation', 1,
        'superseded_by', NULL, 'failure', NULL,
        'created_at', clock_timestamp(), 'updated_at', clock_timestamp(),
        'deadline_at', clock_timestamp() + interval '1 hour',
        'terminal_at', NULL
    )
)).*
FROM agent_control.runtime_run AS source_row
JOIN agent_control.trigger_occurrence AS occurrence
  ON occurrence.occurrence_id = 'occurrence-model-crash-1'
WHERE source_row.run_id = 'run-command-1';

INSERT INTO agent_control.runtime_budget_ledger
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_budget_ledger,
    to_jsonb(source_row) || jsonb_build_object(
        'ledger_id', 'run-ledger-model-crash-1',
        'scope_id', 'run-model-crash-1',
        'consumed_model_calls', 0,
        'consumed_input_tokens', 0,
        'consumed_output_tokens', 0,
        'consumed_tool_calls', 0,
        'consumed_external_cost_micro_usd', 0,
        'consumed_wall_time_ms', 0,
        'consumed_tasks', 1,
        'consumed_active_tasks', 0,
        'consumed_invalid_output_retries', 0,
        'consumed_infrastructure_retries', 0,
        'reserved_model_calls', 0,
        'reserved_input_tokens', 0,
        'reserved_output_tokens', 0,
        'reserved_tool_calls', 0,
        'reserved_external_cost_micro_usd', 0,
        'reserved_wall_time_ms', 0,
        'reserved_tasks', 0,
        'reserved_active_tasks', 0,
        'reserved_invalid_output_retries', 0,
        'reserved_infrastructure_retries', 0,
        'generation', 1, 'state', 'open',
        'updated_at', clock_timestamp()
    )
)).*
FROM agent_control.runtime_budget_ledger AS source_row
WHERE source_row.ledger_id = 'run-ledger-command-1';

INSERT INTO agent_control.runtime_budget_ledger
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_budget_ledger,
    to_jsonb(source_row) || jsonb_build_object(
        'ledger_id', 'task-ledger-model-crash-1',
        'scope_id', 'task-model-crash-1',
        'parent_ledger_id', 'run-ledger-model-crash-1',
        'consumed_model_calls', 0,
        'consumed_input_tokens', 0,
        'consumed_output_tokens', 0,
        'consumed_tool_calls', 0,
        'consumed_external_cost_micro_usd', 0,
        'consumed_wall_time_ms', 0,
        'consumed_tasks', 0,
        'consumed_active_tasks', 0,
        'consumed_invalid_output_retries', 0,
        'consumed_infrastructure_retries', 0,
        'reserved_model_calls', 0,
        'reserved_input_tokens', 0,
        'reserved_output_tokens', 0,
        'reserved_tool_calls', 0,
        'reserved_external_cost_micro_usd', 0,
        'reserved_wall_time_ms', 0,
        'reserved_tasks', 0,
        'reserved_active_tasks', 0,
        'reserved_invalid_output_retries', 0,
        'reserved_infrastructure_retries', 0,
        'generation', 1, 'state', 'open',
        'updated_at', clock_timestamp()
    )
)).*
FROM agent_control.runtime_budget_ledger AS source_row
WHERE source_row.ledger_id = 'task-ledger-command-1';

INSERT INTO agent_control.runtime_task
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_task,
    to_jsonb(source_row) || jsonb_build_object(
        'task_id', 'task-model-crash-1',
        'run_id', 'run-model-crash-1',
        'objective', jsonb_set(
            source_row.objective,
            '{origin,record_id}', to_jsonb('task-model-crash-1'::TEXT)
        ),
        'budget_ledger_id', 'task-ledger-model-crash-1',
        'session_id', 'session-model-crash-1',
        'result_artifact_id', NULL,
        'state', 'ready', 'state_generation', 1,
        'budget_slot_held', false, 'failure', NULL,
        'created_at', clock_timestamp(), 'updated_at', clock_timestamp(),
        'deadline_at', clock_timestamp() + interval '50 minutes',
        'terminal_at', NULL
    )
)).*
FROM agent_control.runtime_task AS source_row
WHERE source_row.task_id = 'task-command-1';

INSERT INTO agent_control.runtime_session
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_session,
    to_jsonb(source_row) || jsonb_build_object(
        'session_id', 'session-model-crash-1',
        'run_id', 'run-model-crash-1',
        'task_id', 'task-model-crash-1',
        'execution_binding', jsonb_set(
            source_row.execution_binding,
            '{origin,record_id}', to_jsonb('session-model-crash-1'::TEXT)
        ),
        'context_manifest', :'crash_model_context'::JSONB,
        'latest_checkpoint_id', NULL,
        'state', 'open', 'generation', 1,
        'created_at', clock_timestamp(), 'closed_at', NULL
    )
)).*
FROM agent_control.runtime_session AS source_row
WHERE source_row.session_id = 'session-command-1';

COMMIT;

-- Dispatch under a one-second lease, let the Worker disappear before it can
-- mark the call unknown, then reclaim the same Attempt. Reclaim must durably
-- record dispatched -> unknown without allocating another call identity.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'claim-model-crash-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'claim_task',
        'idempotency_key', 'claim-model-crash-idem-1',
        'request_digest', repeat('e', 64),
        'causation_id', 'cause-model-crash-claim-1',
        'correlation_id', 'correlation-model-crash-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'task_id', 'task-model-crash-1',
    'expected_task_state_generation', 1,
    'requested_lease_seconds', 1
)::TEXT AS crash_claim_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.claim_task(:'crash_claim_command')::TEXT AS crash_claim_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'start-model-crash-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'start_attempt',
        'idempotency_key', 'start-model-crash-idem-1',
        'request_digest', repeat('f', 64),
        'causation_id', 'cause-model-crash-start-1',
        'correlation_id', 'correlation-model-crash-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'crash_claim_response'::JSONB->>'attempt_id',
    'expected_attempt_state_generation', 1,
    'lease_generation', 1,
    'lease_token', :'crash_claim_response'::JSONB->>'lease_token'
)::TEXT AS crash_start_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.start_attempt(:'crash_start_command')::TEXT AS crash_start_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    :'dispatch_failure_command'::JSONB,
                    '{envelope,command_id}', to_jsonb('dispatch-model-crash-1'::TEXT)
                ),
                '{envelope,idempotency_key}', to_jsonb('dispatch-model-crash-idem-1'::TEXT)
            ),
            '{envelope,request_digest}', to_jsonb(repeat('1', 64))
        ),
        '{envelope,causation_id}', to_jsonb('cause-model-crash-dispatch-1'::TEXT)
    ),
    '{attempt_id}', :'crash_start_response'::JSONB->'attempt_id'
) AS crash_dispatch_partial
\gset
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                jsonb_set(
                    :'crash_dispatch_partial'::JSONB,
                    '{expected_attempt_state_generation}', '2'::JSONB
                ),
                '{lease_generation}', :'crash_start_response'::JSONB->'lease_generation'
            ),
            '{lease_token}', :'crash_start_response'::JSONB->'lease_token'
        ),
        '{turn_id}', to_jsonb('turn-model-crash-1'::TEXT)
    ),
    '{manifest,call_id}', to_jsonb('call-model-crash-1'::TEXT)
) AS crash_dispatch_partial_2
\gset
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            :'crash_dispatch_partial_2'::JSONB,
            '{manifest,idempotency_key}', to_jsonb('provider-call-model-crash-1'::TEXT)
        ),
        '{manifest,request_digest}', to_jsonb(repeat('2', 64))
    ),
        '{manifest,context_manifest}', :'crash_model_context'::JSONB
)::TEXT AS crash_dispatch_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.dispatch_model_call(:'crash_dispatch_command')::TEXT
    AS crash_dispatch_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT pg_sleep(1.1);
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            :'crash_claim_command'::JSONB,
            '{envelope,command_id}', to_jsonb('claim-model-crash-reclaim-1'::TEXT)
        ),
        '{envelope,idempotency_key}', to_jsonb('claim-model-crash-reclaim-idem-1'::TEXT)
    ),
    '{expected_task_state_generation}', '2'::JSONB
) AS crash_reclaim_partial
\gset
SELECT jsonb_set(
    jsonb_set(
        :'crash_reclaim_partial'::JSONB,
        '{envelope,request_digest}', to_jsonb(repeat('3', 64))
    ),
    '{requested_lease_seconds}', '30'::JSONB
)::TEXT AS crash_reclaim_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.claim_task(:'crash_reclaim_command')::TEXT
    AS crash_reclaim_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'crash_reclaim_response'::JSONB->>'status' = 'committed'
    AND :'crash_reclaim_response'::JSONB->>'reclaimed' = 'true'
    AND :'crash_reclaim_response'::JSONB->>'attempt_id'
        = :'crash_claim_response'::JSONB->>'attempt_id'
    AND :'crash_reclaim_response'::JSONB->>'unresolved_turn_id'
        = 'turn-model-crash-1'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-crash-1'
          AND state = 'unknown' AND state_generation = 3
          AND reservation_held AND failure->>'code' = 'provider_outcome_ambiguous'
    )
    AND (SELECT count(*) FROM agent_control.runtime_attempt
         WHERE task_id = 'task-model-crash-1') = 1
    AND (SELECT count(*) FROM agent_control.runtime_model_call_manifest
         WHERE call_id = 'call-model-crash-1') = 1
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'run-ledger-model-crash-1'
          AND reserved_model_calls = 1
          AND reserved_input_tokens = 3 AND reserved_output_tokens = 5
          AND reserved_external_cost_micro_usd = 7
          AND reserved_wall_time_ms = 9
    )
) AS dispatched_crash_reclaimed_safely
\gset
\if :dispatched_crash_reclaimed_safely
\else
    \echo 'FAIL expired dispatched Turn wedged or created a second call identity'
    \quit 1
\endif

-- Reconciliation under the reclaimed fence confirms a failed provider call.
-- This must settle the held worst-case reservation rather than leaving budget
-- permanently stranded behind the recovered unknown Turn.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'resolve-model-crash-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'resolve_model_call',
        'idempotency_key', 'resolve-model-crash-idem-1',
        'request_digest', repeat('4', 64),
        'causation_id', 'cause-model-crash-resolve-1',
        'correlation_id', 'correlation-model-crash-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'crash_reclaim_response'::JSONB->>'attempt_id',
    'expected_attempt_state_generation',
        (:'crash_reclaim_response'::JSONB
            ->>'attempt_state_generation')::BIGINT,
    'lease_generation',
        (:'crash_reclaim_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'crash_reclaim_response'::JSONB->>'lease_token',
    'turn_id', 'turn-model-crash-1',
    'expected_turn_state_generation', 3,
    'outcome', 'failed',
    'failure', jsonb_build_object(
        'code', 'provider_failure_confirmed',
        'message', 'reconciliation confirmed no model result',
        'retryable', false
    )
)::TEXT AS crash_resolve_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.resolve_model_call(:'crash_resolve_command')::TEXT
    AS crash_resolve_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'crash_resolve_response'::JSONB->>'status' = 'committed'
    AND :'crash_resolve_response'::JSONB->>'turn_state' = 'failed'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_turn
        WHERE turn_id = 'turn-model-crash-1'
          AND state = 'failed' AND state_generation = 4
          AND NOT reservation_held AND finished_at IS NOT NULL
          AND failure->>'code' = 'provider_failure_confirmed'
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'run-ledger-model-crash-1'
          AND consumed_model_calls = 1
          AND consumed_input_tokens = 3 AND consumed_output_tokens = 5
          AND consumed_external_cost_micro_usd = 7
          AND consumed_wall_time_ms = 9
          AND reserved_model_calls = 0
          AND reserved_input_tokens = 0 AND reserved_output_tokens = 0
          AND reserved_external_cost_micro_usd = 0
          AND reserved_wall_time_ms = 0
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'task-ledger-model-crash-1'
          AND consumed_model_calls = 0 AND reserved_model_calls = 0
    )
    AND NOT EXISTS (
        SELECT 1 FROM agent_control.runtime_model_call_result
        WHERE call_id = 'call-model-crash-1'
    )
    AND NOT EXISTS (
        SELECT 1 FROM agent_control.runtime_model_provider_request
        WHERE call_id = 'call-model-crash-1'
    )
) AS reclaimed_unknown_resolved_without_reservation
\gset
\if :reclaimed_unknown_resolved_without_reservation
\else
    \echo 'FAIL reclaimed unknown Turn did not settle its reservation'
    \quit 1
\endif

-- Exact public surface and least privilege after the second command slice.
RESET ROLE;
SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
\set ON_ERROR_STOP off
SELECT agent_control.dispatch_model_call(:'dispatch_success_command');
SELECT :'SQLSTATE' = '42501' AS nonworker_model_command_denied
\gset
\set ON_ERROR_STOP on
\if :nonworker_model_command_denied
\else
    \echo 'FAIL non-Worker executed a model-call command'
    \quit 1
\endif
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
DO $$
BEGIN
    BEGIN
        PERFORM * FROM agent_control.runtime_model_call_manifest LIMIT 1;
        RAISE EXCEPTION 'Worker read model-call tables directly';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
    BEGIN
        PERFORM agent_control.runtime_reserve_model_budget_ancestors(
            'run-command-1', 'task-ledger-command-1',
            1, 1, 1, 1, 1, clock_timestamp()
        );
        RAISE EXCEPTION 'Worker executed private model budget helper';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
    BEGIN
        INSERT INTO agent_control.runtime_model_identity_lock (identity_key)
        VALUES ('worker-forged-identity-lock');
        RAISE EXCEPTION 'Worker inserted a model identity lock directly';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
    BEGIN
        PERFORM agent_control.runtime_lock_model_identity_keys(
            ARRAY['worker-forged-identity-lock']::TEXT[]
        );
        RAISE EXCEPTION 'Worker executed private identity lock helper';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
END
$$;
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
DO $$
DECLARE
    command_count INTEGER;
BEGIN
    SELECT count(*) INTO command_count
    FROM pg_catalog.pg_proc AS routine
    JOIN pg_catalog.pg_namespace AS namespace
      ON namespace.oid = routine.pronamespace
    JOIN pg_catalog.pg_roles AS owner_role
      ON owner_role.oid = routine.proowner
    WHERE namespace.nspname = 'agent_control'
      AND routine.proname IN (
          'claim_task', 'start_attempt', 'heartbeat_attempt',
          'dispatch_model_call', 'resolve_model_call',
          'mark_model_call_unknown'
      )
      AND pg_catalog.pg_get_function_identity_arguments(routine.oid)
          = 'p_command text'
      AND routine.prosecdef
      AND owner_role.rolname = 'alpheus_agent_migrator'
      AND 'search_path=pg_catalog, agent_control, platform_security' = ANY(routine.proconfig)
      AND 'TimeZone=UTC' = ANY(routine.proconfig)
      AND pg_catalog.has_function_privilege(
          'alpheus_agent_worker', routine.oid, 'EXECUTE'
      )
      AND NOT pg_catalog.has_function_privilege('public', routine.oid, 'EXECUTE');
    IF command_count <> 6 THEN
        RAISE EXCEPTION 'Worker public command ACL/catalog mismatch: %',
            command_count;
    END IF;

    IF EXISTS (
        SELECT 1 FROM agent_control.runtime_command WHERE state = 'processing'
    ) THEN
        RAISE EXCEPTION 'processing Runtime command leaked after model probe';
    END IF;
    IF EXISTS (
        SELECT 1
        FROM agent_control.runtime_turn AS turn
        LEFT JOIN agent_control.runtime_model_call_manifest AS manifest
          ON manifest.turn_id = turn.turn_id
         AND manifest.attempt_id = turn.attempt_id
         AND manifest.request_digest = turn.request_digest
        WHERE turn.state IN ('dispatched', 'unknown')
          AND turn.turn_id LIKE 'turn-model-%'
          AND (NOT turn.reservation_held OR manifest.call_id IS NULL)
    ) THEN
        RAISE EXCEPTION 'unresolved Turn lost its manifest or reservation';
    END IF;
END
$$;

RESET ROLE;
SELECT 'AP1_MODEL_CALLS_PASS';
