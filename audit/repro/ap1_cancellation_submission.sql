-- AP1 Control Plane cancellation submission proof. Recording a request is
-- idempotent and fenced, but has no runtime-state or external-effect change.
\set ON_ERROR_STOP on

RESET ROLE;
SET ROLE alpheus_agent_migrator;

SELECT task.task_id AS cancel_task_id,
       task.state_generation AS cancel_task_generation,
       task.state::TEXT AS cancel_task_state,
       task.updated_at::TEXT AS cancel_task_updated_at
  FROM agent_control.runtime_task AS task
 WHERE task.task_id = 'task-command-1'
   AND task.state = 'running'
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'cancel-submit-command-1',
        'actor', jsonb_build_object(
            'principal_id', 'control-1', 'kind', 'workload', 'audience', 'control_api'
        ),
        'audience', 'control_api',
        'command_type', 'submit_cancellation_request',
        'idempotency_key', 'cancel-submit-idem-1',
        'request_digest', repeat('e', 64),
        'causation_id', 'cause-cancel-submit-1',
        'correlation_id', 'correlation-cancel-submit-1',
        'deadline', agent_control.runtime_utc_text(clock_timestamp() + interval '5 minutes')
    ),
    'request', jsonb_build_object(
        'schema_revision', 1,
        'request_id', 'cancel-request-probe-1',
        'target', 'task',
        'target_id', :'cancel_task_id',
        'expected_state_generation', :cancel_task_generation,
        'mode', 'cancel',
        'actor', jsonb_build_object(
            'principal_id', 'control-1', 'kind', 'workload', 'audience', 'control_api'
        ),
        'reason_code', 'user_cancel',
        'requested_at', agent_control.runtime_utc_text(clock_timestamp())
    )
)::TEXT AS cancel_submit_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.submit_cancellation_request(:'cancel_submit_command')::TEXT
    AS cancel_submit_response
\gset
SELECT agent_control.submit_cancellation_request(:'cancel_submit_command')::TEXT
    AS cancel_submit_replay
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
\echo cancellation-assert-persisted
SELECT CASE WHEN EXISTS (
                    SELECT 1
                      FROM agent_control.runtime_cancellation_command AS command
                     WHERE command.command_id = 'cancel-submit-command-1'
                       AND command.state = 'committed'
                       AND command.response @> jsonb_build_object(
                           'status', 'committed',
                           'cancellation_request_id', 'cancel-request-probe-1',
                           'request_state', 'pending_reconciliation',
                           'reason_code', 'cancellation_pending_reconciliation'
                       )
                )
                 AND EXISTS (
                    SELECT 1 FROM agent_control.runtime_cancellation_request AS request
                     WHERE request.request_id = 'cancel-request-probe-1'
                       AND request.target_id = :'cancel_task_id'
                 )
                 AND EXISTS (
                    SELECT 1 FROM agent_control.runtime_task AS task
                     WHERE task.task_id = :'cancel_task_id'
                       AND task.state::TEXT = :'cancel_task_state'
                       AND task.state_generation = :cancel_task_generation
                       AND task.updated_at::TEXT = :'cancel_task_updated_at'
                 )
            THEN 1 ELSE 1 / (random()::INTEGER) END AS cancellation_recorded_no_effect;

SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                :'cancel_submit_command'::JSONB,
                '{envelope,command_id}', to_jsonb('cancel-submit-stale-1'::TEXT)
            ),
            '{envelope,idempotency_key}', to_jsonb('cancel-submit-stale-idem-1'::TEXT)
        ),
        '{envelope,request_digest}', to_jsonb(repeat('f', 64))
    ),
    '{request}', jsonb_set(
        jsonb_set(
            :'cancel_submit_command'::JSONB->'request',
            '{request_id}', to_jsonb('cancel-request-stale-1'::TEXT)
        ),
        '{expected_state_generation}', to_jsonb(999::BIGINT)
    )
)::TEXT AS cancel_stale_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
SELECT agent_control.submit_cancellation_request(:'cancel_stale_command')::TEXT
    AS cancel_stale_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
\echo cancellation-assert-stale
SELECT CASE WHEN :'cancel_stale_response'::JSONB @> jsonb_build_object(
                 'status', 'denied', 'reason_code', 'stale_cancellation_target_generation'
            ) THEN 1 ELSE 1 / (random()::INTEGER) END AS cancellation_stale_reason_code;

\echo cancellation-assert-acl
SELECT CASE WHEN has_table_privilege(
    'alpheus_agent_worker', 'agent_control.runtime_cancellation_request', 'SELECT'
) OR has_function_privilege(
    'alpheus_agent_worker', 'agent_control.submit_cancellation_request(TEXT)', 'EXECUTE'
) THEN 1 / (random()::INTEGER) ELSE 1 END AS cancellation_worker_isolated;

SELECT 'AP1_CANCELLATION_SUBMISSION_PASS' AS marker;
RESET ROLE;
