-- AP1 Runtime state persistence proof.  This file is intentionally storage-only:
-- it exercises exact lineage, fail-closed validation, state history, lease and
-- checkpoint fencing, deferred cross-record invariants, and default-deny ACLs.
-- It creates no command function and performs no Provider, Kernel, or broker effect.

\set ON_ERROR_STOP on
SET ROLE alpheus_agent_migrator;

INSERT INTO agent_control.runtime_policy_revision (
    policy_id, schema_revision, generation, record_digest,
    max_model_calls, max_input_tokens, max_output_tokens, max_tool_calls,
    max_external_cost_micro_usd, max_wall_time_ms, max_idle_time_ms,
    max_tasks, max_depth, max_fanout, max_parallelism,
    max_invalid_output_retries, max_infrastructure_retries,
    max_lease_seconds, max_heartbeat_extension_seconds, max_claim_batch,
    max_dependencies, max_artifact_sections, dead_letter_retention_seconds,
    updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
) VALUES (
    'runtime-policy-1', 1, 1, repeat('1', 64),
    10, 10000, 10000, 10, 1000000, 3600000, 600000,
    10, 4, 4, 2, 2, 2, 300, 60, 2, 16, 16, 86400,
    'control-1', 'workload', 'control_api', '2026-07-19 18:00:00Z'
);

INSERT INTO platform_governance.owner_policy_revision (
    revision_id, schema_revision, policy_id, generation, record_digest,
    origin_kind, source_owner, source_record_type,
    initiating_kind, initiating_audience, initiating_principal_id,
    effect_ceiling, author_principal_id, author_kind, author_audience,
    reason_code, created_at
) VALUES (
    'owner-revision-1', 1, 'owner-policy-1', 1, repeat('2', 64),
    'schedule', 'agent_control', 'schedule_occurrence',
    'workload', 'control_api', 'scheduler-1',
    'none', 'activator-1', 'workload', 'activator',
    'fixture_policy', '2026-07-19 18:00:00Z'
);

INSERT INTO agent_control.trigger_registration_revision (
    registration_id, schema_revision, generation, record_digest,
    kind, source_key,
    owner_policy_owner, owner_policy_record_type, owner_policy_record_id,
    owner_policy_schema_revision, owner_policy_record_digest,
    owner_policy_generation,
    runtime_policy_owner, runtime_policy_record_type, runtime_policy_record_id,
    runtime_policy_schema_revision, runtime_policy_record_digest,
    runtime_policy_generation, enabled,
    updated_by_principal_id, updated_by_kind, updated_by_audience, updated_at
) VALUES (
    'registration-1', 1, 1, repeat('3', 64),
    'schedule', 'fixture-schedule',
    'platform_governance', 'owner_policy_revision', 'owner-revision-1',
    1, repeat('2', 64), 1,
    'agent_control', 'runtime_policy', 'runtime-policy-1',
    1, repeat('1', 64), 1, true,
    'control-1', 'workload', 'control_api', '2026-07-19 18:00:00Z'
);

INSERT INTO agent_control.output_contract_revision (
    revision_id, schema_revision, generation, record_digest, artifact_type,
    schema_blob_schema_revision, schema_blob_id, schema_blob_content_digest,
    schema_blob_media_type, schema_blob_size_bytes,
    schema_origin_owner, schema_origin_record_type, schema_origin_record_id,
    schema_origin_schema_revision, schema_origin_record_digest,
    schema_blob_committed_at, effect_class,
    author_principal_id, author_kind, author_audience, reason_code, created_at
) VALUES (
    'output-contract-1', 1, 1, repeat('4', 64), 'analysis_result',
    1, '00000000-0000-4000-8000-000000000004', repeat('a', 64),
    'application/json', 2,
    'agent_control', 'output_contract_schema', 'output-schema-1',
    1, repeat('b', 64), '2026-07-19 18:00:00Z', 'none',
    'control-1', 'workload', 'control_api', 'fixture_contract',
    '2026-07-19 18:00:01Z'
);

BEGIN;
SET CONSTRAINTS ALL DEFERRED;

INSERT INTO agent_control.trigger_occurrence (
    occurrence_id, schema_revision, record_digest,
    registration_owner, registration_record_type, registration_id,
    registration_schema_revision, registration_generation, registration_digest,
    kind, source_owner, source_record_type, source_record_id,
    source_schema_revision, source_record_digest,
    initiating_principal_id, initiating_kind, initiating_audience,
    owner_policy_owner, owner_policy_record_type, owner_policy_record_id,
    owner_policy_schema_revision, owner_policy_record_digest,
    owner_policy_generation, occurrence_key,
    occurred_at, observed_at, committed_at
) VALUES (
    'occurrence-1', 1, repeat('5', 64),
    'agent_control', 'trigger_registration', 'registration-1', 1, 1,
    repeat('3', 64),
    'schedule', 'agent_control', 'schedule_occurrence', 'schedule-source-1',
    1, repeat('6', 64),
    'scheduler-1', 'workload', 'control_api',
    'platform_governance', 'owner_policy_revision', 'owner-revision-1',
    1, repeat('2', 64), 1, 'occurrence-key-1',
    '2026-07-19 18:01:00Z', '2026-07-19 18:01:01Z',
    '2026-07-19 18:01:02Z'
);

INSERT INTO agent_control.runtime_run (
    run_id, schema_revision,
    occurrence_owner, occurrence_record_type, occurrence_id,
    occurrence_schema_revision, occurrence_digest, origin_kind,
    origin_source_owner, origin_source_record_type, origin_source_record_id,
    origin_source_schema_revision, origin_source_record_digest,
    origin_initiating_principal_id, origin_initiating_kind,
    origin_initiating_audience,
    origin_owner_policy_owner, origin_owner_policy_record_type,
    origin_owner_policy_record_id, origin_owner_policy_schema_revision,
    origin_owner_policy_record_digest, origin_owner_policy_generation,
    origin_occurred_at, origin_observed_at, origin_committed_at,
    runtime_policy_owner, runtime_policy_record_type, runtime_policy_id,
    runtime_policy_schema_revision, runtime_policy_generation,
    runtime_policy_digest, budget_ledger_id, root_task_id,
    state, state_generation, created_at, updated_at, deadline_at
) VALUES (
    'run-1', 1,
    'agent_control', 'trigger_occurrence', 'occurrence-1', 1, repeat('5', 64),
    'schedule',
    'agent_control', 'schedule_occurrence', 'schedule-source-1', 1,
    repeat('6', 64), 'scheduler-1', 'workload', 'control_api',
    'platform_governance', 'owner_policy_revision', 'owner-revision-1', 1,
    repeat('2', 64), 1,
    '2026-07-19 18:01:00Z', '2026-07-19 18:01:01Z',
    '2026-07-19 18:01:02Z',
    'agent_control', 'runtime_policy', 'runtime-policy-1', 1, 1,
    repeat('1', 64), 'run-ledger-1', 'task-root-1',
    'queued', 1, '2026-07-19 18:01:03Z', '2026-07-19 18:01:03Z',
    '2026-07-20 18:01:03Z'
);

INSERT INTO agent_control.runtime_budget_ledger (
    ledger_id, schema_revision, scope, scope_id, parent_ledger_id,
    runtime_policy_owner, runtime_policy_record_type, runtime_policy_id,
    runtime_policy_schema_revision, runtime_policy_generation,
    runtime_policy_digest,
    limit_model_calls, limit_input_tokens, limit_output_tokens,
    limit_tool_calls, limit_external_cost_micro_usd,
    limit_wall_time_ms, limit_idle_time_ms, limit_tasks,
    limit_depth, limit_fanout, limit_parallelism,
    limit_invalid_output_retries, limit_infrastructure_retries,
    generation, state, updated_at
) VALUES
    (
        'run-ledger-1', 1, 'run', 'run-1', NULL,
        'agent_control', 'runtime_policy', 'runtime-policy-1', 1, 1,
        repeat('1', 64),
        10, 10000, 10000, 10, 1000000, 3600000, 600000, 10,
        4, 4, 2, 2, 2, 1, 'open', '2026-07-19 18:01:03Z'
    ),
    (
        'task-ledger-root-1', 1, 'task', 'task-root-1', 'run-ledger-1',
        'agent_control', 'runtime_policy', 'runtime-policy-1', 1, 1,
        repeat('1', 64),
        5, 5000, 5000, 5, 500000, 1800000, 300000, 0,
        2, 2, 1, 1, 1, 1, 'open', '2026-07-19 18:01:03Z'
    );

INSERT INTO agent_control.runtime_task (
    task_id, schema_revision, run_id, parent_task_id, depth, objective,
    output_contract_owner, output_contract_record_type,
    output_contract_revision_id, output_contract_schema_revision,
    output_contract_generation, output_contract_digest,
    budget_ledger_id, state, state_generation, budget_slot_held,
    created_at, updated_at, deadline_at
) VALUES (
    'task-root-1', 1, 'run-1', NULL, 0,
    jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '00000000-0000-4000-8000-000000000011',
        'content_digest', repeat('a', 64),
        'media_type', 'application/json', 'size_bytes', 2,
        'origin', jsonb_build_object(
            'owner', 'agent_control', 'record_type', 'task_objective',
            'record_id', 'task-root-1', 'schema_revision', 1,
            'record_digest', repeat('b', 64)
        ),
        'committed_at', '2026-07-19T18:01:02Z'
    ),
    'agent_control', 'output_contract_revision', 'output-contract-1', 1,
    1, repeat('4', 64), 'task-ledger-root-1',
    'ready', 1, false,
    '2026-07-19 18:01:03Z', '2026-07-19 18:01:03Z',
    '2026-07-20 18:01:03Z'
);

COMMIT;

INSERT INTO agent_control.runtime_task_input_ref (task_id, ordinal, reference)
VALUES (
    'task-root-1', 1,
    jsonb_build_object(
        'owner', 'agent_control', 'record_type', 'fixture_input',
        'record_id', 'fixture-ref-1', 'schema_revision', 1,
        'record_digest', repeat('a', 64)
    )
);
DO $$
BEGIN
    BEGIN
        INSERT INTO agent_control.runtime_task_input_ref (
            task_id, ordinal, reference
        ) VALUES (
            'task-root-1', 2,
            jsonb_build_object(
                'owner', 'agent_control', 'record_type', 'fixture_input',
                'record_id', 'fixture-ref-1', 'schema_revision', 1,
                'record_digest', repeat('b', 64)
            )
        );
        RAISE EXCEPTION 'duplicate input identity was accepted';
    EXCEPTION WHEN unique_violation THEN
        NULL;
    END;
END
$$;

BEGIN;
SET CONSTRAINTS ALL DEFERRED;

INSERT INTO agent_control.runtime_session (
    session_id, schema_revision, run_id, task_id, generation,
    execution_binding, context_manifest, state, created_at
) VALUES (
    'session-1', 1, 'run-1', 'task-root-1', 1,
    jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '00000000-0000-4000-8000-000000000012',
        'content_digest', repeat('a', 64),
        'media_type', 'application/json', 'size_bytes', 2,
        'origin', jsonb_build_object(
            'owner', 'agent_control', 'record_type', 'execution_binding',
            'record_id', 'session-1', 'schema_revision', 1,
            'record_digest', repeat('b', 64)
        ),
        'committed_at', '2026-07-19T18:01:03Z'
    ),
    jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '00000000-0000-4000-8000-000000000013',
        'content_digest', repeat('c', 64),
        'media_type', 'application/json', 'size_bytes', 2,
        'origin', jsonb_build_object(
            'owner', 'agent_control', 'record_type', 'context_manifest',
            'record_id', 'session-1', 'schema_revision', 1,
            'record_digest', repeat('d', 64)
        ),
        'committed_at', '2026-07-19T18:01:03Z'
    ),
    'open', '2026-07-19 18:02:00Z'
);

UPDATE agent_control.runtime_run
   SET state = 'running', state_generation = 2,
       updated_at = '2026-07-19 18:02:00Z'
 WHERE run_id = 'run-1';

UPDATE agent_control.runtime_task
   SET session_id = 'session-1', state = 'running', state_generation = 2,
       budget_slot_held = true, updated_at = '2026-07-19 18:02:00Z'
 WHERE task_id = 'task-root-1';

UPDATE agent_control.runtime_task
   SET state = 'waiting', state_generation = 3,
       budget_slot_held = true, updated_at = '2026-07-19 18:02:00.100Z'
 WHERE task_id = 'task-root-1';

DO $$
BEGIN
    BEGIN
        UPDATE agent_control.runtime_task
           SET state = 'ready', state_generation = 4,
               budget_slot_held = false,
               updated_at = '2026-07-19 18:02:00.200Z'
         WHERE task_id = 'task-root-1';
        RAISE EXCEPTION 'waiting Task released an active budget slot';
    EXCEPTION WHEN SQLSTATE '40001' THEN
        NULL;
    END;
END
$$;

UPDATE agent_control.runtime_task
   SET state = 'ready', state_generation = 4,
       budget_slot_held = true, updated_at = '2026-07-19 18:02:00.200Z'
 WHERE task_id = 'task-root-1';

UPDATE agent_control.runtime_task
   SET state = 'running', state_generation = 5,
       budget_slot_held = true, updated_at = '2026-07-19 18:02:00.300Z'
 WHERE task_id = 'task-root-1';

INSERT INTO agent_control.runtime_attempt (
    attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
    state, state_generation, lease_generation, lease_token, lease_worker,
    lease_claimed_at, lease_heartbeat_at, lease_expires_at,
    created_at, updated_at
) VALUES (
    'attempt-1', 1, 'run-1', 'task-root-1', 'session-1', 1,
    'leased', 1, 1, '00000000-0000-4000-8000-000000000021',
    '{"principal_id":"worker-1","kind":"workload","audience":"worker"}',
    '2026-07-19 18:02:01Z', '2026-07-19 18:02:01Z',
    '2026-07-19 18:02:59Z',
    '2026-07-19 18:02:01Z', '2026-07-19 18:02:01Z'
);

-- Same-Attempt reclaim keeps StateGeneration but advances lease generation,
-- token and claim time at or after the expired prior lease fence.
UPDATE agent_control.runtime_attempt
   SET lease_generation = 2,
       lease_token = '00000000-0000-4000-8000-000000000022',
       lease_claimed_at = '2026-07-19 18:03:00Z',
       lease_heartbeat_at = '2026-07-19 18:03:00Z',
       lease_expires_at = '2026-07-19 18:08:00Z',
       updated_at = '2026-07-19 18:03:00Z'
 WHERE attempt_id = 'attempt-1';

UPDATE agent_control.runtime_attempt
   SET state = 'executing', state_generation = 2,
       updated_at = '2026-07-19 18:03:01Z'
 WHERE attempt_id = 'attempt-1';

INSERT INTO agent_control.runtime_checkpoint (
    checkpoint_id, schema_revision, record_digest,
    run_id, task_id, session_id, generation, previous_checkpoint_id,
    manifest, created_by_attempt_id, created_at
) VALUES
    (
        'checkpoint-1', 1, repeat('a', 64),
        'run-1', 'task-root-1', 'session-1', 1, NULL,
        jsonb_build_object(
            'schema_revision', 1,
            'blob_id', '00000000-0000-4000-8000-000000000031',
            'content_digest', repeat('b', 64),
            'media_type', 'application/json', 'size_bytes', 2,
            'origin', jsonb_build_object(
                'owner', 'agent_control', 'record_type', 'checkpoint_manifest',
                'record_id', 'checkpoint-1', 'schema_revision', 1,
                'record_digest', repeat('c', 64)
            ),
            'committed_at', '2026-07-19T18:03:01Z'
        ),
        'attempt-1', '2026-07-19 18:03:02Z'
    ),
    (
        'checkpoint-2', 1, repeat('d', 64),
        'run-1', 'task-root-1', 'session-1', 2, 'checkpoint-1',
        jsonb_build_object(
            'schema_revision', 1,
            'blob_id', '00000000-0000-4000-8000-000000000032',
            'content_digest', repeat('e', 64),
            'media_type', 'application/json', 'size_bytes', 2,
            'origin', jsonb_build_object(
                'owner', 'agent_control', 'record_type', 'checkpoint_manifest',
                'record_id', 'checkpoint-2', 'schema_revision', 1,
                'record_digest', repeat('f', 64)
            ),
            'committed_at', '2026-07-19T18:03:02Z'
        ),
        'attempt-1', '2026-07-19 18:03:03Z'
    );

UPDATE agent_control.runtime_session
   SET latest_checkpoint_id = 'checkpoint-1'
 WHERE session_id = 'session-1';
UPDATE agent_control.runtime_session
   SET latest_checkpoint_id = 'checkpoint-2'
 WHERE session_id = 'session-1';

INSERT INTO agent_control.runtime_checkpoint_preserve_ref (
    checkpoint_id, ordinal, reference
) VALUES (
    'checkpoint-2', 1,
    jsonb_build_object(
        'owner', 'agent_control', 'record_type', 'fixture_fact',
        'record_id', 'fixture-fact-1', 'schema_revision', 1,
        'record_digest', repeat('a', 64)
    )
);

DO $$
BEGIN
    BEGIN
        UPDATE agent_control.runtime_session
           SET latest_checkpoint_id = 'checkpoint-1'
         WHERE session_id = 'session-1';
        RAISE EXCEPTION 'checkpoint CAS moved backwards';
    EXCEPTION WHEN SQLSTATE '40001' THEN
        NULL;
    END;
    BEGIN
        UPDATE agent_control.runtime_session
           SET latest_checkpoint_id = 'checkpoint-2'
         WHERE session_id = 'session-1';
        RAISE EXCEPTION 'checkpoint same-generation overwrite was accepted';
    EXCEPTION WHEN SQLSTATE '40001' THEN
        NULL;
    END;
    BEGIN
        INSERT INTO agent_control.runtime_checkpoint_preserve_ref (
            checkpoint_id, ordinal, reference
        ) VALUES (
            'checkpoint-2', 2,
            jsonb_build_object(
                'owner', 'agent_control', 'record_type', 'fixture_fact',
                'record_id', 'fixture-fact-1', 'schema_revision', 1,
                'record_digest', repeat('b', 64)
            )
        );
        RAISE EXCEPTION 'duplicate checkpoint reference identity was accepted';
    EXCEPTION WHEN unique_violation THEN
        NULL;
    END;
END
$$;

INSERT INTO agent_control.runtime_turn (
    turn_id, schema_revision, run_id, task_id, session_id, attempt_id,
    ordinal, kind, state, state_generation, request_digest,
    reservation_held, created_at, updated_at
) VALUES (
    'turn-1', 1, 'run-1', 'task-root-1', 'session-1', 'attempt-1',
    1, 'model_call', 'planned', 1, repeat('a', 64), false,
    '2026-07-19 18:04:00Z', '2026-07-19 18:04:00Z'
);

INSERT INTO agent_control.runtime_model_call_manifest (
    call_id, schema_revision, record_digest, turn_id, attempt_id,
    idempotency_key, provider, model, prompt_digest, context_manifest,
    output_contract_digest, request_digest, max_output_tokens,
    reserved_input_tokens, reserved_external_cost_micro_usd,
    timeout_ms, temperature_micros, created_at
) VALUES (
    'call-1', 1, repeat('7', 64), 'turn-1', 'attempt-1',
    'call-idem-1', 'fixture-provider', 'fixture-model', repeat('b', 64),
    jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '00000000-0000-4000-8000-000000000041',
        'content_digest', repeat('c', 64),
        'media_type', 'application/json', 'size_bytes', 2,
        'origin', jsonb_build_object(
            'owner', 'agent_control', 'record_type', 'context_manifest',
            'record_id', 'turn-1', 'schema_revision', 1,
            'record_digest', repeat('d', 64)
        ),
        'committed_at', '2026-07-19T18:04:00Z'
    ),
    repeat('4', 64), repeat('a', 64), 100, 10, 1000,
    30000, 0, '2026-07-19 18:04:01Z'
);

UPDATE agent_control.runtime_turn
   SET state = 'dispatched', state_generation = 2,
       reservation_held = true,
       dispatched_at = '2026-07-19 18:04:02Z',
       updated_at = '2026-07-19 18:04:02Z'
 WHERE turn_id = 'turn-1';

INSERT INTO agent_control.runtime_model_call_result (
    result_id, schema_revision, record_digest, call_id, attempt_id, turn_id,
    idempotency_key, request_digest, provider_request_id,
    output_origin_owner, output_origin_record_type, output_origin_record_id,
    output_origin_schema_revision, output_origin_record_digest,
    output, input_tokens, output_tokens, external_cost_micro_usd,
    wall_time_ms, finish_reason, committed_at
) VALUES (
    'result-1', 1, repeat('8', 64), 'call-1', 'attempt-1', 'turn-1',
    'call-idem-1', repeat('a', 64), 'provider-request-1',
    'agent_control', 'model_call_manifest', 'call-1', 1, repeat('7', 64),
    jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '00000000-0000-4000-8000-000000000042',
        'content_digest', repeat('e', 64),
        'media_type', 'application/json', 'size_bytes', 2,
        'origin', jsonb_build_object(
            'owner', 'agent_control', 'record_type', 'model_call_manifest',
            'record_id', 'call-1', 'schema_revision', 1,
            'record_digest', repeat('7', 64)
        ),
        'committed_at', '2026-07-19T18:04:09Z'
    ),
    10, 20, 500, 7000, 'stop', '2026-07-19 18:04:10Z'
);

UPDATE agent_control.runtime_turn
   SET state = 'result_committed', state_generation = 3,
       result_owner = 'agent_control',
       result_record_type = 'model_call_result', result_id = 'result-1',
       result_schema_revision = 1, result_digest = repeat('8', 64),
       reservation_held = false,
       finished_at = '2026-07-19 18:04:10Z',
       updated_at = '2026-07-19 18:04:10Z'
 WHERE turn_id = 'turn-1';

INSERT INTO agent_control.runtime_artifact (
    artifact_id, schema_revision, record_digest,
    run_id, task_id, session_id, attempt_id,
    source_result_owner, source_result_record_type, source_result_id,
    source_result_schema_revision, source_result_digest,
    artifact_type, output_contract_digest, effect_class, created_at
) VALUES (
    'artifact-1', 1, repeat('9', 64),
    'run-1', 'task-root-1', 'session-1', 'attempt-1',
    'agent_control', 'model_call_result', 'result-1', 1, repeat('8', 64),
    'analysis_result', repeat('4', 64), 'none',
    '2026-07-19 18:04:11Z'
);

INSERT INTO agent_control.runtime_artifact_section (
    artifact_id, ordinal, name, required, content
) VALUES (
    'artifact-1', 1, 'model_output', true,
    jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '00000000-0000-4000-8000-000000000042',
        'content_digest', repeat('e', 64),
        'media_type', 'application/json', 'size_bytes', 2,
        'origin', jsonb_build_object(
            'owner', 'agent_control', 'record_type', 'model_call_manifest',
            'record_id', 'call-1', 'schema_revision', 1,
            'record_digest', repeat('7', 64)
        ),
        'committed_at', '2026-07-19T18:04:09Z'
    )
);

INSERT INTO agent_control.runtime_artifact_publication_intent (
    intent_id, schema_revision, record_digest,
    artifact_owner, artifact_record_type, artifact_id,
    artifact_schema_revision, artifact_digest, state, reason_code, created_at
) VALUES (
    'intent-1', 1, repeat('a', 64),
    'agent_control', 'artifact', 'artifact-1', 1, repeat('9', 64),
    'disabled', 'ap1_disabled', '2026-07-19 18:04:12Z'
);

UPDATE agent_control.runtime_attempt
   SET state = 'result_committed', state_generation = 3,
       result_artifact_owner = 'agent_control',
       result_artifact_record_type = 'artifact',
       result_artifact_id = 'artifact-1', result_artifact_schema_revision = 1,
       result_artifact_digest = repeat('9', 64),
       updated_at = '2026-07-19 18:04:12Z',
       terminal_at = '2026-07-19 18:04:12Z'
 WHERE attempt_id = 'attempt-1';

UPDATE agent_control.runtime_task
   SET state = 'result_committed', state_generation = 6,
       result_artifact_id = 'artifact-1',
       updated_at = '2026-07-19 18:04:12Z'
 WHERE task_id = 'task-root-1';
UPDATE agent_control.runtime_task
   SET state = 'succeeded', state_generation = 7,
       budget_slot_held = false,
       updated_at = '2026-07-19 18:04:13Z',
       terminal_at = '2026-07-19 18:04:13Z'
 WHERE task_id = 'task-root-1';

UPDATE agent_control.runtime_session
   SET state = 'closed', generation = 2,
       closed_at = '2026-07-19 18:04:14Z'
 WHERE session_id = 'session-1';

DO $$
BEGIN
    BEGIN
        UPDATE agent_control.runtime_session
           SET latest_checkpoint_id = 'checkpoint-1'
         WHERE session_id = 'session-1';
        RAISE EXCEPTION 'closed session was mutable';
    EXCEPTION WHEN SQLSTATE '55000' THEN
        NULL;
    END;
END
$$;

UPDATE agent_control.runtime_run
   SET state = 'succeeded', state_generation = 3,
       updated_at = '2026-07-19 18:04:15Z',
       terminal_at = '2026-07-19 18:04:15Z'
 WHERE run_id = 'run-1';

COMMIT;

UPDATE agent_control.runtime_budget_ledger
   SET consumed_model_calls = 1, generation = 2,
       updated_at = '2026-07-19 18:05:00Z'
 WHERE ledger_id = 'run-ledger-1';
DO $$
BEGIN
    BEGIN
        UPDATE agent_control.runtime_budget_ledger
           SET consumed_model_calls = 0, generation = 3,
               updated_at = '2026-07-19 18:05:01Z'
         WHERE ledger_id = 'run-ledger-1';
        RAISE EXCEPTION 'cumulative budget consumption decreased';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
END
$$;

INSERT INTO agent_control.runtime_recovery_record (
    recovery_id, schema_revision, record_digest, run_id, task_id,
    previous_attempt_id, original_causation_id, original_idempotency_key,
    decision, committed_artifact_owner, committed_artifact_record_type,
    committed_artifact_id, committed_artifact_schema_revision,
    committed_artifact_digest, next_attempt_id, reason_code, decided_at
) VALUES (
    'recovery-reuse-1', 1, repeat('c', 64), 'run-1', 'task-root-1',
    'attempt-1', 'original-cause-1', 'original-idempotency-1',
    'reuse_committed_result', 'agent_control', 'artifact',
    'artifact-1', 1, repeat('9', 64), NULL,
    'reuse_committed', '2026-07-19 18:05:02Z'
);

SELECT 'AP1_FINAL2_POSITIVE_FIXTURE_PASS';

\set ON_ERROR_STOP on
SET ROLE alpheus_agent_migrator;

DO $$
DECLARE
    valid_blob JSONB := jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '00000000-0000-4000-8000-000000000060',
        'content_digest', repeat('a', 64),
        'media_type', 'application/json',
        'size_bytes', 2,
        'origin', jsonb_build_object(
            'owner', 'agent_control',
            'record_type', 'task_objective',
            'record_id', 'helper-probe',
            'schema_revision', 1,
            'record_digest', repeat('b', 64)
        ),
        'committed_at', '2026-07-19T18:00:00Z'
    );
BEGIN
    IF agent_control.runtime_actor_valid(
        '{"principal_id":"worker-1","kind":null,"audience":"worker"}'::JSONB
    ) IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'Actor.kind JSON null failed open';
    END IF;
    IF agent_control.runtime_actor_valid(
        '{"principal_id":"worker-1","kind":"workload","audience":null}'::JSONB
    ) IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'Actor.audience JSON null failed open';
    END IF;
    IF agent_control.runtime_record_ref_valid(
        jsonb_build_object(
            'owner', NULL, 'record_type', 'fixture_input',
            'record_id', 'helper-probe', 'schema_revision', 1,
            'record_digest', repeat('a', 64)
        ), '', ''
    ) IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'RecordRef.owner JSON null failed open';
    END IF;
    IF agent_control.runtime_failure_valid(
        '{"code":"probe","message":null,"retryable":true}'::JSONB
    ) IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'Failure.message JSON null failed open';
    END IF;
    IF agent_control.runtime_blob_ref_valid(
        jsonb_set(valid_blob, '{media_type}', 'null'::JSONB),
        'task_objective', ''
    ) IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'BlobRef.media_type JSON null failed open';
    END IF;
    IF agent_control.runtime_blob_ref_valid(
        jsonb_set(valid_blob, '{origin,owner}', 'null'::JSONB),
        'task_objective', ''
    ) IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'BlobRef.origin.owner JSON null failed open';
    END IF;
END
$$;

DO $$
BEGIN
    BEGIN
        INSERT INTO agent_control.runtime_task (
            task_id, schema_revision, run_id, parent_task_id, depth, objective,
            output_contract_owner, output_contract_record_type,
            output_contract_revision_id, output_contract_schema_revision,
            output_contract_generation, output_contract_digest,
            budget_ledger_id, state, state_generation, budget_slot_held,
            created_at, updated_at, deadline_at
        )
        SELECT 'task-bad-initial-slot', schema_revision, run_id,
               parent_task_id, depth, objective,
               output_contract_owner, output_contract_record_type,
               output_contract_revision_id, output_contract_schema_revision,
               output_contract_generation, output_contract_digest,
               budget_ledger_id, 'ready', 1, true,
               created_at, created_at, deadline_at
          FROM agent_control.runtime_task
         WHERE task_id = 'task-root-1';
        RAISE EXCEPTION 'initial ready Task held a budget slot';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.runtime_attempt (
            attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
            state, state_generation, lease_generation, lease_token, lease_worker,
            lease_claimed_at, lease_heartbeat_at, lease_expires_at,
            created_at, updated_at
        ) VALUES (
            'attempt-predates', 1, 'run-1', 'task-root-1', 'session-1', 2,
            'leased', 1, 1, '00000000-0000-4000-8000-000000000061',
            '{"principal_id":"worker-1","kind":"workload","audience":"worker"}',
            '2026-07-19 18:05:59Z', '2026-07-19 18:06:00Z',
            '2026-07-19 18:11:00Z',
            '2026-07-19 18:06:00Z', '2026-07-19 18:06:00Z'
        );
        RAISE EXCEPTION 'lease claim predating Attempt was accepted';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.runtime_attempt (
            attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
            state, state_generation, lease_generation, lease_token, lease_worker,
            lease_claimed_at, lease_heartbeat_at, lease_expires_at,
            created_at, updated_at
        ) VALUES (
            'attempt-bad-initial', 1, 'run-1', 'task-root-1', 'session-1', 2,
            'executing', 1, 1, '00000000-0000-4000-8000-000000000062',
            '{"principal_id":"worker-1","kind":"workload","audience":"worker"}',
            '2026-07-19 18:06:00Z', '2026-07-19 18:06:00Z',
            '2026-07-19 18:11:00Z',
            '2026-07-19 18:06:00Z', '2026-07-19 18:06:00Z'
        );
        RAISE EXCEPTION 'non-initial Attempt state was accepted';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        UPDATE agent_control.runtime_attempt
           SET updated_at = '2026-07-19 18:06:01Z'
         WHERE attempt_id = 'attempt-1';
        RAISE EXCEPTION 'terminal Attempt update was accepted';
    EXCEPTION WHEN SQLSTATE '55000' THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.runtime_budget_ledger (
            ledger_id, schema_revision, scope, scope_id,
            runtime_policy_owner, runtime_policy_record_type,
            runtime_policy_id, runtime_policy_schema_revision,
            runtime_policy_generation, runtime_policy_digest,
            limit_model_calls, limit_input_tokens, limit_output_tokens,
            limit_tool_calls, limit_external_cost_micro_usd,
            limit_wall_time_ms, limit_idle_time_ms, limit_tasks,
            limit_depth, limit_fanout, limit_parallelism,
            limit_invalid_output_retries, limit_infrastructure_retries,
            generation, state
        ) VALUES (
            'ledger-bad-initial', 1, 'run', 'run-bad-initial',
            'agent_control', 'runtime_policy', 'runtime-policy-1', 1,
            1, repeat('1', 64),
            0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0,
            1, 'exhausted'
        );
        RAISE EXCEPTION 'non-open initial Budget was accepted';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
END
$$;

BEGIN;
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO agent_control.runtime_attempt (
    attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
    state, state_generation, lease_generation, lease_token, lease_worker,
    lease_claimed_at, lease_heartbeat_at, lease_expires_at,
    created_at, updated_at
) VALUES (
    'attempt-live', 1, 'run-1', 'task-root-1', 'session-1', 2,
    'leased', 1, 1, '00000000-0000-4000-8000-000000000063',
    '{"principal_id":"worker-1","kind":"workload","audience":"worker"}',
    '2026-07-19 18:06:00Z', '2026-07-19 18:06:00Z',
    '2026-07-19 18:11:00Z',
    '2026-07-19 18:06:00Z', '2026-07-19 18:06:00Z'
);
DO $$
BEGIN
    BEGIN
        UPDATE agent_control.runtime_attempt
           SET lease_generation = 2,
               lease_token = '00000000-0000-4000-8000-000000000064',
               lease_claimed_at = '2026-07-19 18:10:00Z',
               lease_heartbeat_at = '2026-07-19 18:10:00Z',
               lease_expires_at = '2026-07-19 18:15:00Z',
               updated_at = '2026-07-19 18:10:00Z'
         WHERE attempt_id = 'attempt-live';
        RAISE EXCEPTION 'same-Attempt reclaim before expiry was accepted';
    EXCEPTION WHEN SQLSTATE '40001' THEN
        NULL;
    END;

    BEGIN
        UPDATE agent_control.runtime_attempt
           SET lease_generation = 2,
               lease_token = '00000000-0000-4000-8000-000000000063',
               lease_claimed_at = '2026-07-19 18:11:00Z',
               lease_heartbeat_at = '2026-07-19 18:11:00Z',
               lease_expires_at = '2026-07-19 18:16:00Z',
               updated_at = '2026-07-19 18:11:00Z'
         WHERE attempt_id = 'attempt-live';
        RAISE EXCEPTION 'same-Attempt reclaim reused lease token';
    EXCEPTION WHEN SQLSTATE '40001' THEN
        NULL;
    END;
END
$$;
INSERT INTO agent_control.runtime_turn (
    turn_id, schema_revision, run_id, task_id, session_id, attempt_id,
    ordinal, kind, state, state_generation, request_digest,
    reservation_held, created_at, updated_at
) VALUES (
    'turn-live', 1, 'run-1', 'task-root-1', 'session-1', 'attempt-live',
    1, 'model_call', 'planned', 1, repeat('a', 64), false,
    '2026-07-19 18:06:01Z', '2026-07-19 18:06:01Z'
);
UPDATE agent_control.runtime_turn
   SET state = 'dispatched', state_generation = 2,
       reservation_held = true,
       dispatched_at = '2026-07-19 18:06:02Z',
       updated_at = '2026-07-19 18:06:02Z'
 WHERE turn_id = 'turn-live';
COMMIT;

DO $$
BEGIN
    BEGIN
        UPDATE agent_control.runtime_turn
           SET updated_at = '2026-07-19 18:06:03Z'
         WHERE turn_id = 'turn-live';
        RAISE EXCEPTION 'same-state Turn body mutation was accepted';
    EXCEPTION WHEN SQLSTATE '55000' THEN
        NULL;
    END;

    BEGIN
        UPDATE agent_control.runtime_turn
           SET state = 'unknown', state_generation = 3,
               failure = '{"code":"provider_unknown","message":"probe","retryable":true}',
               reservation_held = true,
               dispatched_at = '2026-07-19 18:06:03Z',
               updated_at = '2026-07-19 18:06:03Z'
         WHERE turn_id = 'turn-live';
        RAISE EXCEPTION 'Turn dispatched_at rewrite was accepted';
    EXCEPTION WHEN SQLSTATE '55000' THEN
        NULL;
    END;
END
$$;

UPDATE agent_control.runtime_turn
   SET state = 'unknown', state_generation = 3,
       failure = '{"code":"provider_unknown","message":"probe","retryable":true}',
       reservation_held = true,
       updated_at = '2026-07-19 18:06:03Z'
 WHERE turn_id = 'turn-live';

SELECT 'AP1_FINAL2_IMMEDIATE_NEGATIVE_PROBES_PASS';

\set ON_ERROR_STOP on
SET ROLE alpheus_agent_migrator;

DO $$
BEGIN
    BEGIN
        INSERT INTO agent_control.runtime_command (
            principal_id, command_type, idempotency_key, command_id,
            schema_revision, actor_kind, actor_audience, command_audience,
            request_digest, body_fingerprint, causation_id, correlation_id,
            deadline_at, state, response, response_digest, created_at,
            committed_at
        ) VALUES (
            'worker-1', 'claim_task', 'command-null-completed-at',
            'command-null-completed-at', 1, 'workload', 'worker',
            'control_api', repeat('1', 64), repeat('2', 64),
            'cause-null-completed-at', 'correlation-null-completed-at',
            '2026-07-20 18:00:00Z', 'committed', '{}'::JSONB,
            repeat('3', 64), '2026-07-19 18:00:00Z', NULL
        );
        RAISE EXCEPTION 'completed command accepted NULL committed_at';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.trigger_occurrence
        SELECT (jsonb_populate_record(
            NULL::agent_control.trigger_occurrence,
            to_jsonb(source_row) || jsonb_build_object(
                'occurrence_id', 'occurrence-null-registration',
                'record_digest', repeat('e', 64),
                'occurrence_key', 'occurrence-null-registration',
                'registration_digest', NULL
            )
        )).* 
          FROM agent_control.trigger_occurrence AS source_row
         WHERE occurrence_id = 'occurrence-1';
        RAISE EXCEPTION 'registered occurrence accepted partial NULL tuple';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.runtime_run
        SELECT (jsonb_populate_record(
            NULL::agent_control.runtime_run,
            to_jsonb(source_row) || jsonb_build_object(
                'run_id', 'run-null-occurrence',
                'occurrence_digest', NULL,
                'state', 'queued', 'state_generation', 1,
                'superseded_by', NULL, 'failure', NULL, 'terminal_at', NULL,
                'updated_at', source_row.created_at
            )
        )).* 
          FROM agent_control.runtime_run AS source_row
         WHERE run_id = 'run-1';
        RAISE EXCEPTION 'non-user Run accepted partial NULL occurrence tuple';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.runtime_run
        SELECT (jsonb_populate_record(
            NULL::agent_control.runtime_run,
            to_jsonb(source_row) || jsonb_build_object(
                'run_id', 'run-null-conversation',
                'occurrence_owner', NULL,
                'occurrence_record_type', NULL,
                'occurrence_id', NULL,
                'occurrence_schema_revision', NULL,
                'occurrence_digest', NULL,
                'origin_kind', 'user_request',
                'origin_source_owner', 'agent_control',
                'origin_source_record_type', 'user_request',
                'origin_initiating_kind', 'user',
                'origin_initiating_audience', 'control_api',
                'origin_conversation_owner', 'agent_control',
                'origin_conversation_record_type', 'conversation',
                'origin_conversation_record_id', 'conversation-null-digest',
                'origin_conversation_schema_revision', 1,
                'origin_conversation_record_digest', NULL,
                'state', 'queued', 'state_generation', 1,
                'superseded_by', NULL, 'failure', NULL, 'terminal_at', NULL,
                'updated_at', source_row.created_at
            )
        )).* 
          FROM agent_control.runtime_run AS source_row
         WHERE run_id = 'run-1';
        RAISE EXCEPTION 'user Run accepted partial NULL conversation tuple';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.runtime_run
        SELECT (jsonb_populate_record(
            NULL::agent_control.runtime_run,
            to_jsonb(source_row) || jsonb_build_object(
                'run_id', 'run-null-recovery',
                'origin_kind', 'system_recovery',
                'origin_source_owner', 'agent_control',
                'origin_source_record_type', 'recovery_occurrence',
                'origin_initiating_kind', 'workload',
                'origin_initiating_audience', 'control_api',
                'recovery_original_causation_id', 'original-cause-null-probe',
                'recovery_original_idempotency_key', 'original-idem-null-probe',
                'recovery_authority_owner', 'agent_control',
                'recovery_authority_record_type', 'recovery_authority',
                'recovery_authority_record_id', 'authority-null-probe',
                'recovery_authority_schema_revision', 1,
                'recovery_authority_record_digest', NULL,
                'recovery_effect_owner', 'kernel',
                'recovery_effect_record_type', 'operation',
                'recovery_effect_record_id', 'effect-null-probe',
                'recovery_effect_schema_revision', 1,
                'recovery_effect_record_digest', repeat('f', 64),
                'state', 'queued', 'state_generation', 1,
                'superseded_by', NULL, 'failure', NULL, 'terminal_at', NULL,
                'updated_at', source_row.created_at
            )
        )).* 
          FROM agent_control.runtime_run AS source_row
         WHERE run_id = 'run-1';
        RAISE EXCEPTION 'recovery Run accepted partial NULL authority tuple';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        UPDATE agent_control.runtime_attempt
           SET state = 'executing', state_generation = 2,
               updated_at = '2026-07-19 18:06:04Z'
         WHERE attempt_id = 'attempt-live';
        UPDATE agent_control.runtime_attempt
           SET state = 'result_committed', state_generation = 3,
               result_artifact_owner = NULL,
               result_artifact_record_type = 'artifact',
               result_artifact_id = 'artifact-1',
               result_artifact_schema_revision = 1,
               result_artifact_digest = repeat('9', 64),
               updated_at = '2026-07-19 18:06:05Z',
               terminal_at = '2026-07-19 18:06:05Z'
         WHERE attempt_id = 'attempt-live';
        RAISE EXCEPTION 'Attempt accepted partial NULL Artifact tuple';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        UPDATE agent_control.runtime_turn
           SET state = 'result_committed', state_generation = 4,
               result_owner = NULL,
               result_record_type = 'model_call_result',
               result_id = 'result-1', result_schema_revision = 1,
               result_digest = repeat('6', 64), failure = NULL,
               reservation_held = false,
               updated_at = '2026-07-19 18:06:04Z',
               finished_at = '2026-07-19 18:06:04Z'
         WHERE turn_id = 'turn-live';
        RAISE EXCEPTION 'Turn accepted partial NULL result tuple';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;

    BEGIN
        INSERT INTO agent_control.runtime_recovery_record
        SELECT (jsonb_populate_record(
            NULL::agent_control.runtime_recovery_record,
            to_jsonb(source_row) || jsonb_build_object(
                'recovery_id', 'recovery-null-artifact-owner',
                'record_digest', repeat('d', 64),
                'previous_attempt_id', 'attempt-live',
                'committed_artifact_owner', NULL
            )
        )).* 
          FROM agent_control.runtime_recovery_record AS source_row
         WHERE recovery_id = 'recovery-reuse-1';
        RAISE EXCEPTION 'Recovery accepted partial NULL Artifact tuple';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
END
$$;

SELECT 'AP1_FINAL2_FAIL_CLOSED_TUPLE_PROBES_PASS';

-- Force each deferred cross-record invariant inside a PL/pgSQL subtransaction
-- so this DO statement remains atomic while proving commit-time rejection.
DO $$
BEGIN
    BEGIN
        UPDATE agent_control.runtime_attempt
           SET state = 'failed', state_generation = 2,
               failure = '{"code":"worker_failed","message":"probe","retryable":true}',
               updated_at = '2026-07-19 18:07:00Z',
               terminal_at = '2026-07-19 18:07:00Z'
         WHERE attempt_id = 'attempt-live';
        INSERT INTO agent_control.runtime_attempt (
            attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
            state, state_generation, lease_generation, lease_token, lease_worker,
            lease_claimed_at, lease_heartbeat_at, lease_expires_at,
            created_at, updated_at
        ) VALUES (
            'attempt-after-unknown', 1, 'run-1', 'task-root-1', 'session-1', 3,
            'leased', 1, 1, '00000000-0000-4000-8000-000000000071',
            '{"principal_id":"worker-2","kind":"workload","audience":"worker"}',
            '2026-07-19 18:07:01Z', '2026-07-19 18:07:01Z',
            '2026-07-19 18:12:01Z',
            '2026-07-19 18:07:01Z', '2026-07-19 18:07:01Z'
        );
        SET CONSTRAINTS ALL IMMEDIATE;
        RAISE EXCEPTION 'terminal Attempt plus replacement escaped unresolved Turn guard';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
    SET CONSTRAINTS ALL DEFERRED;

    BEGIN
        INSERT INTO agent_control.trigger_occurrence (
            occurrence_id, schema_revision, record_digest,
            registration_owner, registration_record_type, registration_id,
            registration_schema_revision, registration_generation,
            registration_digest, kind, source_owner, source_record_type,
            source_record_id, source_schema_revision, source_record_digest,
            initiating_principal_id, initiating_kind, initiating_audience,
            owner_policy_owner, owner_policy_record_type, owner_policy_record_id,
            owner_policy_schema_revision, owner_policy_record_digest,
            owner_policy_generation, occurrence_key,
            occurred_at, observed_at, committed_at
        ) VALUES (
            'occurrence-bad-binding', 1, repeat('d', 64),
            'agent_control', 'trigger_registration', 'registration-1', 1, 1,
            repeat('3', 64), 'schedule', 'agent_control',
            'schedule_occurrence', 'schedule-source-bad', 1, repeat('e', 64),
            'wrong-scheduler', 'workload', 'control_api',
            'platform_governance', 'owner_policy_revision', 'owner-revision-1',
            1, repeat('2', 64), 1, 'occurrence-key-bad',
            '2026-07-19 18:08:00Z', '2026-07-19 18:08:01Z',
            '2026-07-19 18:08:02Z'
        );
        SET CONSTRAINTS ALL IMMEDIATE;
        RAISE EXCEPTION 'TriggerOccurrence escaped exact registration binding';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
    SET CONSTRAINTS ALL DEFERRED;

    BEGIN
        INSERT INTO agent_control.runtime_budget_ledger (
            ledger_id, schema_revision, scope, scope_id, parent_ledger_id,
            runtime_policy_owner, runtime_policy_record_type, runtime_policy_id,
            runtime_policy_schema_revision, runtime_policy_generation,
            runtime_policy_digest,
            limit_model_calls, limit_input_tokens, limit_output_tokens,
            limit_tool_calls, limit_external_cost_micro_usd,
            limit_wall_time_ms, limit_idle_time_ms, limit_tasks,
            limit_depth, limit_fanout, limit_parallelism,
            limit_invalid_output_retries, limit_infrastructure_retries,
            generation, state, updated_at
        ) VALUES (
            'child-ledger-bad', 1, 'task', 'task-child-bad', 'run-ledger-1',
            'agent_control', 'runtime_policy', 'runtime-policy-1', 1, 1,
            repeat('1', 64),
            1, 100, 100, 1, 1000, 10000, 1000, 0,
            0, 0, 1, 0, 0, 1, 'open', '2026-07-19 18:09:00Z'
        );
        INSERT INTO agent_control.runtime_task (
            task_id, schema_revision, run_id, parent_task_id, depth, objective,
            output_contract_owner, output_contract_record_type,
            output_contract_revision_id, output_contract_schema_revision,
            output_contract_generation, output_contract_digest,
            budget_ledger_id, state, state_generation, budget_slot_held,
            created_at, updated_at, deadline_at
        ) VALUES (
            'task-child-bad', 1, 'run-1', 'task-root-1', 1,
            jsonb_build_object(
                'schema_revision', 1,
                'blob_id', '00000000-0000-4000-8000-000000000081',
                'content_digest', repeat('a', 64),
                'media_type', 'application/json', 'size_bytes', 2,
                'origin', jsonb_build_object(
                    'owner', 'agent_control', 'record_type', 'task_objective',
                    'record_id', 'task-child-bad', 'schema_revision', 1,
                    'record_digest', repeat('b', 64)
                ),
                'committed_at', '2026-07-19T18:08:59Z'
            ),
            'agent_control', 'output_contract_revision', 'output-contract-1', 1,
            1, repeat('4', 64), 'child-ledger-bad', 'ready', 1, false,
            '2026-07-19 18:09:00Z', '2026-07-19 18:09:00Z',
            '2026-07-20 18:09:00Z'
        );
        SET CONSTRAINTS ALL IMMEDIATE;
        RAISE EXCEPTION 'child Task escaped exact parent-ledger binding';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
    SET CONSTRAINTS ALL DEFERRED;

    BEGIN
        INSERT INTO agent_control.runtime_model_call_manifest (
            call_id, schema_revision, record_digest, turn_id, attempt_id,
            idempotency_key, provider, model, prompt_digest, context_manifest,
            output_contract_digest, request_digest, max_output_tokens,
            reserved_input_tokens, reserved_external_cost_micro_usd,
            timeout_ms, temperature_micros, created_at
        ) VALUES (
            'call-bad-contract', 1, repeat('c', 64),
            'turn-live', 'attempt-live', 'call-idem-bad',
            'fixture-provider', 'fixture-model', repeat('d', 64),
            jsonb_build_object(
                'schema_revision', 1,
                'blob_id', '00000000-0000-4000-8000-000000000091',
                'content_digest', repeat('e', 64),
                'media_type', 'application/json', 'size_bytes', 2,
                'origin', jsonb_build_object(
                    'owner', 'agent_control', 'record_type', 'context_manifest',
                    'record_id', 'turn-live', 'schema_revision', 1,
                    'record_digest', repeat('f', 64)
                ),
                'committed_at', '2026-07-19T18:06:03Z'
            ),
            repeat('e', 64), repeat('a', 64), 10, 1, 10,
            1000, 0, '2026-07-19 18:06:04Z'
        );
        SET CONSTRAINTS ALL IMMEDIATE;
        RAISE EXCEPTION 'ModelCallManifest escaped Task output contract';
    EXCEPTION WHEN check_violation THEN
        NULL;
    END;
    SET CONSTRAINTS ALL DEFERRED;

    BEGIN
        INSERT INTO agent_control.runtime_artifact
        SELECT (jsonb_populate_record(
            NULL::agent_control.runtime_artifact,
            to_jsonb(source_row) || jsonb_build_object(
                'artifact_id', 'artifact-bad-result-lineage',
                'record_digest', repeat('0', 64),
                'attempt_id', 'attempt-live'
            )
        )).*
          FROM agent_control.runtime_artifact AS source_row
         WHERE artifact_id = 'artifact-1';
        INSERT INTO agent_control.runtime_artifact_section (
            artifact_id, ordinal, name, required, content
        )
        SELECT 'artifact-bad-result-lineage', ordinal, name, required, content
          FROM agent_control.runtime_artifact_section
         WHERE artifact_id = 'artifact-1';
        SET CONSTRAINTS ALL IMMEDIATE;
        RAISE EXCEPTION 'Artifact escaped exact source Result Attempt lineage';
    EXCEPTION WHEN foreign_key_violation THEN
        NULL;
    END;
    SET CONSTRAINTS ALL DEFERRED;
END
$$;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM agent_control.runtime_attempt
         WHERE attempt_id = 'attempt-after-unknown'
    ) OR EXISTS (
        SELECT 1
          FROM agent_control.trigger_occurrence
         WHERE occurrence_id = 'occurrence-bad-binding'
    ) OR EXISTS (
        SELECT 1
          FROM agent_control.runtime_task
         WHERE task_id = 'task-child-bad'
    ) OR EXISTS (
        SELECT 1
          FROM agent_control.runtime_model_call_manifest
         WHERE call_id = 'call-bad-contract'
    ) OR EXISTS (
        SELECT 1
          FROM agent_control.runtime_artifact
         WHERE artifact_id = 'artifact-bad-result-lineage'
    ) THEN
        RAISE EXCEPTION 'deferred negative fixture leaked a row';
    END IF;
END
$$;

RESET ROLE;

SELECT 'ap1-runtime-state-pass';
