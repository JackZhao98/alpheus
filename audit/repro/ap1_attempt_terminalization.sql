-- AP1 Attempt terminalization proof.  The caller is a real Worker LOGIN;
-- fixture and verification reads remain migrator-only.  This probe performs
-- no model, Tool, Kernel, Provider, operation, broker, GRACE, or Delegation
-- effect.
\set ON_ERROR_STOP on

RESET ROLE;
SET ROLE alpheus_agent_migrator;

-- Reuse the exact successful Result produced by ap1_model_calls.sql. Refresh
-- the lease first so slow CI cannot turn the happy path into an expiry probe.
SELECT attempt.attempt_id AS commit_attempt_id,
       attempt.state_generation AS commit_attempt_generation,
       attempt.lease_generation AS commit_lease_generation,
       attempt.lease_token::TEXT AS commit_lease_token
  FROM agent_control.runtime_attempt AS attempt
 WHERE attempt.task_id = 'task-command-1'
   AND attempt.state = 'executing'
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'heartbeat-terminalization-setup-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload',
            'audience', 'worker'
        ),
        'audience', 'control_api',
        'command_type', 'heartbeat_attempt',
        'idempotency_key', 'heartbeat-terminalization-setup-idem-1',
        'request_digest', repeat('1', 64),
        'causation_id', 'cause-terminalization-setup-1',
        'correlation_id', 'correlation-terminalization-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'commit_attempt_id',
    'expected_attempt_state_generation', :commit_attempt_generation,
    'lease_generation', :commit_lease_generation,
    'lease_token', :'commit_lease_token',
    'requested_extension_seconds', 60
)::TEXT AS commit_heartbeat_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.heartbeat_attempt(:'commit_heartbeat_command')::TEXT
    AS commit_heartbeat_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT result.result_id AS commit_result_id,
       result.record_digest::TEXT AS commit_result_digest,
       result.output::TEXT AS commit_result_output,
       task.output_contract_digest::TEXT AS commit_contract_digest,
       contract.artifact_type AS commit_artifact_type,
       ledger.consumed_active_tasks AS commit_before_active,
       ledger.generation AS commit_before_ledger_generation,
       task_ledger.generation AS commit_before_leaf_generation
  FROM agent_control.runtime_model_call_result AS result
  JOIN agent_control.runtime_attempt AS attempt
    ON attempt.attempt_id = result.attempt_id
  JOIN agent_control.runtime_task AS task
    ON task.task_id = attempt.task_id
  JOIN agent_control.output_contract_revision AS contract
    ON contract.record_digest = task.output_contract_digest
  JOIN agent_control.runtime_budget_ledger AS ledger
    ON ledger.ledger_id = (
        SELECT parent_ledger_id
          FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = task.budget_ledger_id
    )
  JOIN agent_control.runtime_budget_ledger AS task_ledger
    ON task_ledger.ledger_id = task.budget_ledger_id
 WHERE result.call_id = 'call-model-success-1'
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'commit-attempt-success-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload',
            'audience', 'worker'
        ),
        'audience', 'control_api',
        'command_type', 'commit_attempt',
        'idempotency_key', 'commit-attempt-success-idem-1',
        'request_digest', repeat('2', 64),
        'causation_id', 'cause-commit-attempt-success-1',
        'correlation_id', 'correlation-terminalization-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'commit_attempt_id',
    'expected_attempt_state_generation', :commit_attempt_generation,
    'lease_generation',
        (:'commit_heartbeat_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'commit_heartbeat_response'::JSONB->>'lease_token',
    'result', jsonb_build_object(
        'owner', 'agent_control',
        'record_type', 'model_call_result',
        'record_id', :'commit_result_id',
        'schema_revision', 1,
        'record_digest', :'commit_result_digest'
    ),
    'artifact', jsonb_build_object(
        'artifact_type', :'commit_artifact_type',
        'output_contract_digest', :'commit_contract_digest',
        'effect_class', 'none',
        'sections', jsonb_build_array(jsonb_build_object(
            'name', 'model_output',
            'required', true,
            'content', :'commit_result_output'::JSONB
        ))
    )
)::TEXT AS commit_success_command
\gset

SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                :'commit_success_command'::JSONB,
                '{envelope,command_id}',
                to_jsonb('commit-attempt-section-limit-1'::TEXT)
            ),
            '{envelope,idempotency_key}',
            to_jsonb('commit-attempt-section-limit-idem-1'::TEXT)
        ),
        '{envelope,request_digest}', to_jsonb(repeat('4', 64))
    ),
    '{artifact,sections}',
    (
        SELECT jsonb_agg(jsonb_build_object(
            'name', 'section_' || ordinal,
            'required', true,
            'content', :'commit_result_output'::JSONB
        ) ORDER BY ordinal)
          FROM generate_series(
              1,
              (SELECT policy.max_artifact_sections + 1
                 FROM agent_control.runtime_run AS run
                 JOIN agent_control.runtime_policy_revision AS policy
                   ON policy.policy_id = run.runtime_policy_id
                  AND policy.generation = run.runtime_policy_generation
                  AND policy.record_digest = run.runtime_policy_digest
                WHERE run.run_id = 'run-command-1')
          ) AS ordinal
    )
)::TEXT AS commit_section_limit_command
\gset

SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                :'commit_success_command'::JSONB,
                '{envelope,command_id}',
                to_jsonb('commit-attempt-conflicting-blob-1'::TEXT)
            ),
            '{envelope,idempotency_key}',
            to_jsonb('commit-attempt-conflicting-blob-idem-1'::TEXT)
        ),
        '{envelope,request_digest}', to_jsonb(repeat('5', 64))
    ),
    '{artifact,sections}',
    jsonb_build_array(
        :'commit_success_command'::JSONB #> '{artifact,sections,0}',
        jsonb_build_object(
            'name', 'forged_blob_alias',
            'required', false,
            'content', jsonb_set(
                :'commit_result_output'::JSONB,
                '{content_digest}', to_jsonb(repeat('a', 64))
            )
        )
    )
)::TEXT AS commit_conflicting_blob_command
\gset

SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            :'commit_success_command'::JSONB,
            '{envelope,command_id}',
            to_jsonb('commit-attempt-missing-blob-1'::TEXT)
        ),
        '{envelope,idempotency_key}',
        to_jsonb('commit-attempt-missing-blob-idem-1'::TEXT)
    ),
    '{artifact,sections}',
    (:'commit_success_command'::JSONB #> '{artifact,sections}')
        || jsonb_build_array(jsonb_build_object(
            'name', 'missing_optional_blob',
            'required', false,
            'content', jsonb_set(
                jsonb_set(
                    :'commit_result_output'::JSONB,
                    '{blob_id}',
                    to_jsonb('29999999-9999-4999-8999-999999999999'::TEXT)
                ),
                '{content_digest}', to_jsonb(repeat('b', 64))
            )
        ))
)::TEXT AS commit_missing_blob_command
\gset

SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            :'commit_success_command'::JSONB,
            '{envelope,command_id}',
            to_jsonb('commit-attempt-unresolved-turn-1'::TEXT)
        ),
        '{envelope,idempotency_key}',
        to_jsonb('commit-attempt-unresolved-turn-idem-1'::TEXT)
    ),
    '{envelope,request_digest}', to_jsonb(repeat('6', 64))
)::TEXT AS commit_unresolved_turn_command
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'fail-attempt-unresolved-turn-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload',
            'audience', 'worker'
        ),
        'audience', 'control_api',
        'command_type', 'fail_attempt',
        'idempotency_key', 'fail-attempt-unresolved-turn-idem-1',
        'request_digest', repeat('7', 64),
        'causation_id', 'cause-fail-attempt-unresolved-turn-1',
        'correlation_id', 'correlation-terminalization-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'commit_attempt_id',
    'expected_attempt_state_generation', :commit_attempt_generation,
    'lease_generation',
        (:'commit_heartbeat_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'commit_heartbeat_response'::JSONB->>'lease_token',
    'retry_class', 'none',
    'failure', jsonb_build_object(
        'code', 'probe_unresolved_turn',
        'message', 'probe unresolved turn',
        'retryable', false
    )
)::TEXT AS fail_unresolved_turn_command
\gset

BEGIN;
SET CONSTRAINTS ALL DEFERRED;
INSERT INTO agent_control.runtime_turn (
    turn_id, schema_revision, run_id, task_id, session_id, attempt_id,
    ordinal, kind, state, state_generation, request_digest,
    reservation_held, created_at, updated_at
)
SELECT 'turn-terminalization-unresolved-1', 1, attempt.run_id,
       attempt.task_id, attempt.session_id, attempt.attempt_id,
       coalesce(max(existing.ordinal), 0) + 1,
       'model_call', 'planned', 1, repeat('6', 64), false,
       transaction_timestamp(), transaction_timestamp()
  FROM agent_control.runtime_attempt AS attempt
  LEFT JOIN agent_control.runtime_turn AS existing
    ON existing.attempt_id = attempt.attempt_id
 WHERE attempt.attempt_id = :'commit_attempt_id'
 GROUP BY attempt.run_id, attempt.task_id, attempt.session_id,
          attempt.attempt_id;
SELECT agent_control.runtime_insert_event(
    'turn', 'turn-terminalization-unresolved-1', NULL, 'planned', 1,
    'worker-1', 'cause-terminalization-unresolved-1',
    'correlation-terminalization-1', 'probe_turn_planned',
    clock_timestamp()
);
COMMIT;

SELECT count(*) AS artifacts_before_section_limit
FROM agent_control.runtime_artifact
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.commit_attempt(:'commit_unresolved_turn_command')::TEXT
    AS commit_unresolved_turn_response
\gset
SELECT agent_control.fail_attempt(:'fail_unresolved_turn_command')::TEXT
    AS fail_unresolved_turn_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
BEGIN;
SET CONSTRAINTS ALL DEFERRED;
UPDATE agent_control.runtime_turn
   SET state = 'canceled', state_generation = 2,
       updated_at = statement_timestamp(), finished_at = statement_timestamp()
 WHERE turn_id = 'turn-terminalization-unresolved-1';
SELECT agent_control.runtime_insert_event(
    'turn', 'turn-terminalization-unresolved-1', 'planned', 'canceled', 2,
    'worker-1', 'cause-terminalization-unresolved-clear-1',
    'correlation-terminalization-1', 'probe_turn_canceled',
    clock_timestamp()
);
COMMIT;

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.commit_attempt(:'commit_missing_blob_command')::TEXT
    AS commit_missing_blob_response
\gset
SELECT agent_control.commit_attempt(:'commit_section_limit_command')::TEXT
    AS commit_section_limit_response
\gset
\set ON_ERROR_STOP off
SELECT agent_control.commit_attempt(:'commit_conflicting_blob_command');
SELECT :'SQLSTATE' = '22023' AS commit_conflicting_blob_rejected
\gset
\set ON_ERROR_STOP on
SELECT agent_control.commit_attempt(:'commit_success_command')::TEXT
    AS commit_success_response
\gset
SELECT agent_control.commit_attempt(jsonb_set(
    :'commit_success_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('commit-attempt-success-retry-1'::TEXT)
)::TEXT)::TEXT AS commit_success_retry_response
\gset

\set ON_ERROR_STOP off
SELECT agent_control.commit_attempt(jsonb_set(
    :'commit_success_command'::JSONB,
    '{artifact,sections,0,name}',
    to_jsonb('changed_result'::TEXT)
)::TEXT);
SELECT :'SQLSTATE' = '23505' AS commit_changed_body_rejected
\gset
\set ON_ERROR_STOP on
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'commit_success_response'::JSONB->>'status' = 'committed'
    AND :'commit_unresolved_turn_response'::JSONB->>'status' = 'denied'
    AND :'commit_unresolved_turn_response'::JSONB->>'reason_code'
        = 'unresolved_turn_exists'
    AND :'fail_unresolved_turn_response'::JSONB->>'status' = 'denied'
    AND :'fail_unresolved_turn_response'::JSONB->>'reason_code'
        = 'unresolved_turn_exists'
    AND :'commit_missing_blob_response'::JSONB->>'status' = 'denied'
    AND :'commit_missing_blob_response'::JSONB->>'reason_code'
        = 'artifact_blob_unavailable'
    AND :'commit_section_limit_response'::JSONB->>'status' = 'denied'
    AND :'commit_conflicting_blob_rejected'::BOOLEAN
    AND (SELECT count(*) FROM agent_control.runtime_artifact)
        = :artifacts_before_section_limit + 1
    AND :'commit_success_response'::JSONB
        = :'commit_success_retry_response'::JSONB
    AND :'commit_changed_body_rejected'::BOOLEAN
    AND EXISTS (
        SELECT 1
          FROM agent_control.runtime_attempt
         WHERE attempt_id = :'commit_attempt_id'
           AND state = 'result_committed'
           AND state_generation = :commit_attempt_generation + 1
           AND result_artifact_id
               = :'commit_success_response'::JSONB->>'artifact_id'
           AND result_artifact_digest::TEXT
               = :'commit_success_response'::JSONB->>'artifact_digest'
           AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1
          FROM agent_control.runtime_task
         WHERE task_id = 'task-command-1'
           AND state = 'succeeded'
           AND result_artifact_id
               = :'commit_success_response'::JSONB->>'artifact_id'
           AND NOT budget_slot_held
           AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_session
         WHERE session_id = 'session-command-1'
           AND state = 'closed' AND generation = 2
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_run
         WHERE run_id = 'run-command-1'
           AND state = 'succeeded' AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1
          FROM agent_control.runtime_artifact AS artifact
          JOIN agent_control.runtime_artifact_section AS section
            ON section.artifact_id = artifact.artifact_id
          JOIN agent_control.runtime_artifact_publication_intent AS intent
            ON intent.artifact_id = artifact.artifact_id
          JOIN blob.blob_reference AS artifact_binding
            ON artifact_binding.blob_id
               = (section.content->>'blob_id')::UUID
           AND artifact_binding.reference_owner = 'agent_control'
           AND artifact_binding.reference_record_type = 'artifact'
           AND artifact_binding.reference_record_id = artifact.artifact_id
           AND artifact_binding.reference_record_digest = artifact.record_digest
         WHERE artifact.artifact_id
               = :'commit_success_response'::JSONB->>'artifact_id'
           AND artifact.source_result_id = :'commit_result_id'
           AND artifact.source_result_digest::TEXT = :'commit_result_digest'
           AND artifact.effect_class = 'none'
           AND section.required
           AND section.content = :'commit_result_output'::JSONB
           AND intent.state = 'disabled'
           AND artifact_binding.state = 'active'
           AND artifact_binding.retention_until > clock_timestamp()
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = 'run-ledger-command-1'
           AND consumed_active_tasks = :commit_before_active - 1
           AND generation = :commit_before_ledger_generation + 1
    )
    AND (SELECT generation FROM agent_control.runtime_budget_ledger
          WHERE ledger_id = 'task-ledger-command-1')
        = :commit_before_leaf_generation
    AND (SELECT count(*) FROM agent_control.runtime_attempt_lease_event
          WHERE attempt_id = :'commit_attempt_id'
            AND transition = 'released') = 1
) AS commit_attempt_exact
\gset
\if :commit_attempt_exact
\else
    \echo 'FAIL commit_attempt did not terminalize exact lineage atomically'
    \quit 1
\endif

-- Clone isolated root Attempts through the legal state transitions.  The
-- fixture is migrator-only and intentionally creates no Turn: fail_attempt
-- must also contain Worker failures that occur before a model dispatch.
CREATE FUNCTION pg_temp.seed_failure_attempt(
    p_suffix TEXT,
    p_invalid_consumed BIGINT,
    p_infrastructure_consumed BIGINT
) RETURNS JSONB
LANGUAGE plpgsql
AS $$
DECLARE
    occurrence_id_value TEXT := 'occurrence-terminal-' || p_suffix;
    run_id_value TEXT := 'run-terminal-' || p_suffix;
    task_id_value TEXT := 'task-terminal-' || p_suffix;
    session_id_value TEXT := 'session-terminal-' || p_suffix;
    attempt_id_value TEXT := 'attempt-terminal-' || p_suffix;
    run_ledger_id_value TEXT := 'run-ledger-terminal-' || p_suffix;
    task_ledger_id_value TEXT := 'task-ledger-terminal-' || p_suffix;
    lease_token_value UUID := gen_random_uuid();
    now_at TIMESTAMPTZ := clock_timestamp();
BEGIN
    SET CONSTRAINTS ALL DEFERRED;

    INSERT INTO agent_control.trigger_occurrence
    SELECT (jsonb_populate_record(
        NULL::agent_control.trigger_occurrence,
        to_jsonb(source_row) || jsonb_build_object(
            'occurrence_id', occurrence_id_value,
            'record_digest', encode(sha256(convert_to(
                'occurrence-terminal:' || p_suffix, 'UTF8'
            )), 'hex'),
            'occurrence_key', 'occurrence-terminal-key-' || p_suffix,
            'occurred_at', now_at - interval '4 seconds',
            'observed_at', now_at - interval '3 seconds',
            'committed_at', now_at - interval '2 seconds'
        )
    )).*
      FROM agent_control.trigger_occurrence AS source_row
     WHERE source_row.occurrence_id = 'occurrence-command-1';

    INSERT INTO agent_control.runtime_run
    SELECT (jsonb_populate_record(
        NULL::agent_control.runtime_run,
        to_jsonb(source_row) || jsonb_build_object(
            'run_id', run_id_value,
            'occurrence_id', occurrence.occurrence_id,
            'occurrence_digest', occurrence.record_digest,
            'origin_occurred_at', occurrence.occurred_at,
            'origin_observed_at', occurrence.observed_at,
            'origin_committed_at', occurrence.committed_at,
            'budget_ledger_id', run_ledger_id_value,
            'root_task_id', task_id_value,
            'state', 'queued', 'state_generation', 1,
            'superseded_by', NULL, 'failure', NULL,
            'created_at', now_at, 'updated_at', now_at,
            'deadline_at', now_at + interval '10 minutes',
            'terminal_at', NULL
        )
    )).*
      FROM agent_control.runtime_run AS source_row
      JOIN agent_control.trigger_occurrence AS occurrence
        ON occurrence.occurrence_id = occurrence_id_value
     WHERE source_row.run_id = 'run-command-1';

    INSERT INTO agent_control.runtime_budget_ledger
    SELECT (jsonb_populate_record(
        NULL::agent_control.runtime_budget_ledger,
        to_jsonb(source_row) || jsonb_build_object(
            'ledger_id', run_ledger_id_value,
            'scope_id', run_id_value,
            'consumed_model_calls', 0,
            'consumed_input_tokens', 0,
            'consumed_output_tokens', 0,
            'consumed_tool_calls', 0,
            'consumed_external_cost_micro_usd', 0,
            'consumed_wall_time_ms', 0,
            'consumed_tasks', 1,
            'consumed_active_tasks', 1,
            'consumed_invalid_output_retries', p_invalid_consumed,
            'consumed_infrastructure_retries', p_infrastructure_consumed,
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
            'generation', 1, 'state', 'open', 'updated_at', now_at
        )
    )).*
      FROM agent_control.runtime_budget_ledger AS source_row
     WHERE source_row.ledger_id = 'run-ledger-command-1';

    INSERT INTO agent_control.runtime_budget_ledger
    SELECT (jsonb_populate_record(
        NULL::agent_control.runtime_budget_ledger,
        to_jsonb(source_row) || jsonb_build_object(
            'ledger_id', task_ledger_id_value,
            'scope_id', task_id_value,
            'parent_ledger_id', run_ledger_id_value,
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
            'generation', 1, 'state', 'open', 'updated_at', now_at
        )
    )).*
      FROM agent_control.runtime_budget_ledger AS source_row
     WHERE source_row.ledger_id = 'task-ledger-command-1';

    INSERT INTO agent_control.runtime_task
    SELECT (jsonb_populate_record(
        NULL::agent_control.runtime_task,
        to_jsonb(source_row) || jsonb_build_object(
            'task_id', task_id_value,
            'run_id', run_id_value,
            'parent_task_id', NULL,
            'depth', 0,
            'objective', jsonb_set(
                source_row.objective,
                '{origin,record_id}', to_jsonb(task_id_value)
            ),
            'budget_ledger_id', task_ledger_id_value,
            'session_id', session_id_value,
            'result_artifact_id', NULL,
            'state', 'ready', 'state_generation', 1,
            'budget_slot_held', false, 'failure', NULL,
            'created_at', now_at, 'updated_at', now_at,
            'deadline_at', now_at + interval '9 minutes',
            'terminal_at', NULL
        )
    )).*
      FROM agent_control.runtime_task AS source_row
     WHERE source_row.task_id = 'task-command-1';

    INSERT INTO agent_control.runtime_session
    SELECT (jsonb_populate_record(
        NULL::agent_control.runtime_session,
        to_jsonb(source_row) || jsonb_build_object(
            'session_id', session_id_value,
            'run_id', run_id_value,
            'task_id', task_id_value,
            'generation', 1,
            'execution_binding', jsonb_set(
                source_row.execution_binding,
                '{origin,record_id}', to_jsonb(session_id_value)
            ),
            'context_manifest', jsonb_set(
                source_row.context_manifest,
                '{origin,record_id}', to_jsonb(session_id_value)
            ),
            'latest_checkpoint_id', NULL,
            'state', 'open', 'created_at', now_at, 'closed_at', NULL
        )
    )).*
      FROM agent_control.runtime_session AS source_row
     WHERE source_row.session_id = 'session-command-1';

    INSERT INTO agent_control.runtime_attempt (
        attempt_id, schema_revision, run_id, task_id, session_id, ordinal,
        state, state_generation, lease_generation, lease_token,
        lease_worker, lease_claimed_at, lease_heartbeat_at,
        lease_expires_at, created_at, updated_at
    ) VALUES (
        attempt_id_value, 1, run_id_value, task_id_value,
        session_id_value, 1, 'leased', 1, 1, lease_token_value,
        jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload',
            'audience', 'worker'
        ),
        now_at, now_at, now_at + interval '5 minutes', now_at, now_at
    );

    INSERT INTO agent_control.runtime_attempt_lease_event (
        event_id, schema_revision, attempt_id, event_generation,
        lease_generation, transition, worker_principal_id, lease_token,
        previous_expires_at, new_expires_at, actor, causation_id,
        correlation_id, occurred_at
    ) VALUES (
        gen_random_uuid()::TEXT, 1, attempt_id_value, 1, 1, 'claimed',
        'worker-1', lease_token_value, NULL, now_at + interval '5 minutes',
        jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload',
            'audience', 'worker'
        ),
        'cause-seed-' || p_suffix, 'correlation-seed-' || p_suffix, now_at
    );

    UPDATE agent_control.runtime_run
       SET state = 'running', state_generation = 2
     WHERE run_id = run_id_value;
    UPDATE agent_control.runtime_task
       SET state = 'running', state_generation = 2,
           budget_slot_held = true
     WHERE task_id = task_id_value;
    UPDATE agent_control.runtime_attempt
       SET state = 'executing', state_generation = 2
     WHERE attempt_id = attempt_id_value;

    RETURN jsonb_build_object(
        'run_id', run_id_value,
        'task_id', task_id_value,
        'session_id', session_id_value,
        'attempt_id', attempt_id_value,
        'run_ledger_id', run_ledger_id_value,
        'task_ledger_id', task_ledger_id_value,
        'lease_token', lease_token_value::TEXT
    );
END
$$;

SELECT pg_temp.seed_failure_attempt('none', 0, 0)::TEXT AS fail_none_fixture
\gset
SELECT pg_temp.seed_failure_attempt('infra', 0, 0)::TEXT AS fail_infra_fixture
\gset
SELECT pg_temp.seed_failure_attempt('invalid', 0, 0)::TEXT AS fail_invalid_fixture
\gset
SELECT pg_temp.seed_failure_attempt('exhausted', 0, 2)::TEXT
    AS fail_exhausted_fixture
\gset

-- Build commands centrally; only the narrow TEXT wrapper is callable by the
-- Worker login below.
CREATE FUNCTION pg_temp.fail_command(
    p_fixture JSONB,
    p_name TEXT,
    p_retry_class TEXT,
    p_retryable BOOLEAN
) RETURNS TEXT
LANGUAGE sql
AS $$
    SELECT jsonb_build_object(
        'schema_revision', 1,
        'envelope', jsonb_build_object(
            'schema_revision', 1,
            'command_id', 'fail-attempt-' || p_name,
            'actor', jsonb_build_object(
                'principal_id', 'worker-1', 'kind', 'workload',
                'audience', 'worker'
            ),
            'audience', 'control_api',
            'command_type', 'fail_attempt',
            'idempotency_key', 'fail-attempt-idem-' || p_name,
            'request_digest', encode(sha256(convert_to(
                'fail-attempt-request:' || p_name, 'UTF8'
            )), 'hex'),
            'causation_id', 'cause-fail-attempt-' || p_name,
            'correlation_id', 'correlation-fail-attempt-' || p_name,
            'deadline', agent_control.runtime_utc_text(
                clock_timestamp() + interval '5 minutes'
            )
        ),
        'attempt_id', p_fixture->>'attempt_id',
        'expected_attempt_state_generation', 2,
        'lease_generation', 1,
        'lease_token', p_fixture->>'lease_token',
        'retry_class', p_retry_class,
        'failure', jsonb_build_object(
            'code', 'fixture_failure_' || p_name,
            'message', 'fixture failure ' || p_name,
            'retryable', p_retryable
        )
    )::TEXT
$$;

SELECT pg_temp.fail_command(
    :'fail_none_fixture'::JSONB, 'none', 'none', false
) AS fail_none_command
\gset
SELECT pg_temp.fail_command(
    :'fail_infra_fixture'::JSONB, 'infra', 'infrastructure', true
) AS fail_infra_command
\gset
SELECT pg_temp.fail_command(
    :'fail_invalid_fixture'::JSONB, 'invalid', 'invalid_output', true
) AS fail_invalid_command
\gset
SELECT pg_temp.fail_command(
    :'fail_exhausted_fixture'::JSONB,
    'exhausted', 'infrastructure', true
) AS fail_exhausted_command
\gset

-- Schema/Go retryability compatibility is rejected before a durable command
-- row can be created.
RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
\set ON_ERROR_STOP off
SELECT agent_control.fail_attempt(jsonb_set(
    :'fail_infra_command'::JSONB,
    '{retry_class}', to_jsonb('none'::TEXT)
)::TEXT);
SELECT :'SQLSTATE' = '22023' AS retryable_none_rejected
\gset
SELECT agent_control.fail_attempt(jsonb_set(
    :'fail_none_command'::JSONB,
    '{retry_class}', to_jsonb('infrastructure'::TEXT)
)::TEXT);
SELECT :'SQLSTATE' = '22023' AS nonretryable_infrastructure_rejected
\gset
\set ON_ERROR_STOP on

SELECT agent_control.fail_attempt(:'fail_none_command')::TEXT
    AS fail_none_response
\gset
SELECT agent_control.fail_attempt(jsonb_set(
    :'fail_none_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('fail-attempt-none-retry'::TEXT)
)::TEXT)::TEXT AS fail_none_retry_response
\gset
SELECT agent_control.fail_attempt(:'fail_infra_command')::TEXT
    AS fail_infra_response
\gset
SELECT agent_control.fail_attempt(:'fail_invalid_command')::TEXT
    AS fail_invalid_response
\gset
SELECT agent_control.fail_attempt(:'fail_exhausted_command')::TEXT
    AS fail_exhausted_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'retryable_none_rejected'::BOOLEAN
    AND :'nonretryable_infrastructure_rejected'::BOOLEAN
    AND NOT EXISTS (
        SELECT 1 FROM agent_control.runtime_command
         WHERE command_id IN ('fail-attempt-infra', 'fail-attempt-none')
           AND body_fingerprint IN (
               agent_control.runtime_sha256_json(jsonb_set(
                   :'fail_infra_command'::JSONB,
                   '{retry_class}', to_jsonb('none'::TEXT)
               )),
               agent_control.runtime_sha256_json(jsonb_set(
                   :'fail_none_command'::JSONB,
                   '{retry_class}', to_jsonb('infrastructure'::TEXT)
               ))
           )
    )
    AND :'fail_none_response'::JSONB->>'status' = 'committed'
    AND :'fail_none_response'::JSONB = :'fail_none_retry_response'::JSONB
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_attempt
         WHERE attempt_id = :'fail_none_fixture'::JSONB->>'attempt_id'
           AND state = 'failed' AND state_generation = 3
           AND failure #>> '{retryable}' = 'false'
           AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_task
         WHERE task_id = :'fail_none_fixture'::JSONB->>'task_id'
           AND state = 'failed' AND state_generation = 3
           AND NOT budget_slot_held AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_session
         WHERE session_id = :'fail_none_fixture'::JSONB->>'session_id'
           AND state = 'closed' AND generation = 2
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_run
         WHERE run_id = :'fail_none_fixture'::JSONB->>'run_id'
           AND state = 'failed' AND state_generation = 3
           AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = :'fail_none_fixture'::JSONB->>'run_ledger_id'
           AND consumed_active_tasks = 0
           AND consumed_invalid_output_retries = 0
           AND consumed_infrastructure_retries = 0
           AND generation = 2
    )
) AS fail_none_exact
\gset
\if :fail_none_exact
\else
    \echo 'FAIL non-retryable failure did not close exact runtime state'
    \quit 1
\endif

SELECT (
    :'fail_infra_response'::JSONB->>'status' = 'committed'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_attempt
         WHERE attempt_id = :'fail_infra_fixture'::JSONB->>'attempt_id'
           AND state = 'failed' AND state_generation = 3
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_task
         WHERE task_id = :'fail_infra_fixture'::JSONB->>'task_id'
           AND state = 'waiting' AND state_generation = 3
           AND budget_slot_held AND terminal_at IS NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_session
         WHERE session_id = :'fail_infra_fixture'::JSONB->>'session_id'
           AND state = 'open' AND generation = 1
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_run
         WHERE run_id = :'fail_infra_fixture'::JSONB->>'run_id'
           AND state = 'waiting' AND state_generation = 3
           AND terminal_at IS NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = :'fail_infra_fixture'::JSONB->>'run_ledger_id'
           AND consumed_active_tasks = 1
           AND consumed_invalid_output_retries = 0
           AND consumed_infrastructure_retries = 1
           AND generation = 2
    )
) AS fail_infrastructure_retry_exact
\gset
\if :fail_infrastructure_retry_exact
\else
    \echo 'FAIL infrastructure retry did not preserve slot and charge once'
    \quit 1
\endif

SELECT (
    :'fail_invalid_response'::JSONB->>'status' = 'committed'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_task
         WHERE task_id = :'fail_invalid_fixture'::JSONB->>'task_id'
           AND state = 'waiting' AND budget_slot_held
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = :'fail_invalid_fixture'::JSONB->>'run_ledger_id'
           AND consumed_active_tasks = 1
           AND consumed_invalid_output_retries = 1
           AND consumed_infrastructure_retries = 0
           AND generation = 2
    )
) AS fail_invalid_retry_exact
\gset
\if :fail_invalid_retry_exact
\else
    \echo 'FAIL invalid-output retry charged the wrong frozen counter'
    \quit 1
\endif

SELECT (
    :'fail_exhausted_response'::JSONB->>'status' = 'committed'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_attempt
         WHERE attempt_id = :'fail_exhausted_fixture'::JSONB->>'attempt_id'
           AND state = 'failed' AND state_generation = 3
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_task
         WHERE task_id = :'fail_exhausted_fixture'::JSONB->>'task_id'
           AND state = 'dead_lettered' AND state_generation = 3
           AND NOT budget_slot_held AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_session
         WHERE session_id = :'fail_exhausted_fixture'::JSONB->>'session_id'
           AND state = 'closed' AND generation = 2
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_run
         WHERE run_id = :'fail_exhausted_fixture'::JSONB->>'run_id'
           AND state = 'dead_lettered' AND state_generation = 3
           AND terminal_at IS NOT NULL
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = :'fail_exhausted_fixture'::JSONB->>'run_ledger_id'
           AND consumed_active_tasks = 0
           AND consumed_infrastructure_retries = 2
           AND generation = 2
    )
) AS fail_retry_exhausted_exact
\gset
\if :fail_retry_exhausted_exact
\else
    \echo 'FAIL retry exhaustion did not dead-letter without exceeding cap'
    \quit 1
\endif

-- Waiting retries reuse the historical active slot. claim/start allocates a
-- new Attempt but must not charge active_tasks a second time.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'claim-after-infra-retry-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload',
            'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'claim_task',
        'idempotency_key', 'claim-after-infra-retry-idem-1',
        'request_digest', repeat('3', 64),
        'causation_id', 'cause-claim-after-infra-retry-1',
        'correlation_id', 'correlation-fail-attempt-infra',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'task_id', :'fail_infra_fixture'::JSONB->>'task_id',
    'expected_task_state_generation', 3,
    'requested_lease_seconds', 60
)::TEXT AS retry_claim_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.claim_task(:'retry_claim_command')::TEXT
    AS retry_claim_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'retry_claim_response'::JSONB->>'status' = 'committed'
    AND :'retry_claim_response'::JSONB->>'reclaimed' = 'false'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_attempt
         WHERE attempt_id = :'retry_claim_response'::JSONB->>'attempt_id'
           AND ordinal = 2 AND state = 'leased'
           AND state_generation = 1
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_task
         WHERE task_id = :'fail_infra_fixture'::JSONB->>'task_id'
           AND state = 'running' AND state_generation = 4
           AND budget_slot_held
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_run
         WHERE run_id = :'fail_infra_fixture'::JSONB->>'run_id'
           AND state = 'running' AND state_generation = 4
    )
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
         WHERE ledger_id = :'fail_infra_fixture'::JSONB->>'run_ledger_id'
           AND consumed_active_tasks = 1
           AND consumed_infrastructure_retries = 1
           AND generation = 2
    )
) AS retry_claim_reused_slot
\gset
\if :retry_claim_reused_slot
\else
    \echo 'FAIL waiting retry claim double-charged or reused the old Attempt'
    \quit 1
\endif

-- No caller gains the private JSONB helpers or table access.  Control API has
-- its own database identity and cannot impersonate the Worker command path.
RESET ROLE;
SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
\set ON_ERROR_STOP off
SELECT agent_control.commit_attempt(:'commit_success_command');
SELECT :'SQLSTATE' = '42501' AS control_commit_denied
\gset
SELECT agent_control.fail_attempt(:'fail_none_command');
SELECT :'SQLSTATE' = '42501' AS control_fail_denied
\gset
\set ON_ERROR_STOP on
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
\set ON_ERROR_STOP off
SELECT agent_control.runtime_commit_attempt(:'commit_success_command'::JSONB);
SELECT :'SQLSTATE' = '42501' AS worker_private_commit_denied
\gset
SELECT agent_control.runtime_fail_attempt(:'fail_none_command'::JSONB);
SELECT :'SQLSTATE' = '42501' AS worker_private_fail_denied
\gset
SELECT * FROM agent_control.runtime_lock_worker_blob_source_binding(
    '{}'::JSONB, 'worker-1'
);
SELECT :'SQLSTATE' = '42501' AS worker_private_blob_helper_denied
\gset
SELECT count(*) FROM agent_control.runtime_artifact;
SELECT :'SQLSTATE' = '42501' AS worker_artifact_read_denied
\gset
\set ON_ERROR_STOP on
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'control_commit_denied'::BOOLEAN
    AND :'control_fail_denied'::BOOLEAN
    AND :'worker_private_commit_denied'::BOOLEAN
    AND :'worker_private_fail_denied'::BOOLEAN
    AND :'worker_private_blob_helper_denied'::BOOLEAN
    AND :'worker_artifact_read_denied'::BOOLEAN
    AND NOT pg_catalog.has_function_privilege(
        'control-1', 'agent_control.commit_attempt(text)', 'EXECUTE'
    )
    AND NOT pg_catalog.has_function_privilege(
        'control-1', 'agent_control.fail_attempt(text)', 'EXECUTE'
    )
    AND NOT EXISTS (
        SELECT 1
          FROM pg_catalog.pg_proc AS procedure
          CROSS JOIN LATERAL pg_catalog.aclexplode(
              coalesce(
                  procedure.proacl,
                  pg_catalog.acldefault('f', procedure.proowner)
              )
          ) AS privilege
         WHERE procedure.oid IN (
             'agent_control.commit_attempt(text)'::REGPROCEDURE,
             'agent_control.fail_attempt(text)'::REGPROCEDURE
         )
           AND privilege.grantee = 0
           AND privilege.privilege_type = 'EXECUTE'
    )
    AND (
        SELECT count(*)
          FROM pg_catalog.pg_proc AS procedure
          JOIN pg_catalog.pg_namespace AS namespace
            ON namespace.oid = procedure.pronamespace
          JOIN pg_catalog.pg_roles AS owner_role
            ON owner_role.oid = procedure.proowner
         WHERE namespace.nspname = 'agent_control'
           AND procedure.proname IN ('commit_attempt', 'fail_attempt')
           AND pg_catalog.pg_get_function_identity_arguments(procedure.oid)
               = 'p_command text'
           AND procedure.prosecdef
           AND owner_role.rolname = 'alpheus_agent_migrator'
           AND procedure.proconfig @> ARRAY[
               'search_path=pg_catalog, agent_control, platform_security',
               'TimeZone=UTC'
           ]
    ) = 2
    AND (
        SELECT count(*)
          FROM information_schema.routine_privileges
         WHERE routine_schema = 'agent_control'
           AND routine_name IN (
               'claim_task', 'start_attempt', 'heartbeat_attempt',
               'dispatch_model_call', 'resolve_model_call',
               'mark_model_call_unknown', 'commit_attempt', 'fail_attempt'
           )
           AND grantee = 'alpheus_agent_worker'
           AND privilege_type = 'EXECUTE'
    ) = 8
    AND NOT EXISTS (
        SELECT 1 FROM agent_control.runtime_command
         WHERE state = 'processing'
    )
    AND NOT EXISTS (
        SELECT 1
          FROM agent_control.runtime_attempt AS attempt
          JOIN agent_control.runtime_turn AS turn
            ON turn.attempt_id = attempt.attempt_id
         WHERE attempt.state IN (
             'result_committed', 'failed', 'timed_out',
             'canceled', 'superseded'
         )
           AND (
               turn.state IN ('planned', 'dispatched', 'unknown')
               OR turn.reservation_held
           )
    )
) AS terminalization_acl_and_invariants
\gset
\if :terminalization_acl_and_invariants
\else
    \echo 'FAIL terminalization ACL or final invariant drifted'
    \quit 1
\endif

\echo 'AP1_ATTEMPT_TERMINALIZATION_PASS'

RESET ROLE;
