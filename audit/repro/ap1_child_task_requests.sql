-- AP1 child-task request proof. A Worker can persist one fenced, immutable
-- request, but cannot create a child Runtime Task, Session, Agent binding,
-- capability grant, Tool permission, or external effect.
\set ON_ERROR_STOP on

RESET ROLE;
SET ROLE alpheus_agent_migrator;

-- Refresh the fixture lease first: the probe intentionally spends time testing
-- replay and denial behavior and must not accidentally become an expiry test.
SELECT attempt.state_generation AS child_heartbeat_attempt_generation,
       attempt.attempt_id AS child_heartbeat_attempt_id,
       attempt.lease_generation AS child_heartbeat_lease_generation,
       attempt.lease_token::TEXT AS child_heartbeat_lease_token
  FROM agent_control.runtime_task AS task
  JOIN agent_control.runtime_attempt AS attempt
    ON attempt.task_id = task.task_id
   AND attempt.run_id = task.run_id
 WHERE task.task_id = 'task-command-1'
   AND task.state = 'running'
   AND attempt.state = 'executing'
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'child-request-heartbeat-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api',
        'command_type', 'heartbeat_attempt',
        'idempotency_key', 'child-request-heartbeat-idem-1',
        'request_digest', repeat('b', 64),
        'causation_id', 'cause-child-request-heartbeat-1',
        'correlation_id', 'correlation-child-request-probe-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'child_heartbeat_attempt_id',
    'expected_attempt_state_generation', :child_heartbeat_attempt_generation,
    'lease_generation', :child_heartbeat_lease_generation,
    'lease_token', :'child_heartbeat_lease_token',
    'requested_extension_seconds', 60
)::TEXT AS child_heartbeat_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.heartbeat_attempt(:'child_heartbeat_command')::TEXT
    AS child_heartbeat_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SELECT task.run_id AS child_run_id,
       task.task_id AS child_parent_task_id,
       task.state_generation AS child_parent_generation,
       attempt.attempt_id AS child_attempt_id,
       attempt.lease_generation AS child_lease_generation,
       attempt.lease_token::TEXT AS child_lease_token,
       task.objective::TEXT AS child_objective,
       task.output_contract_revision_id AS child_contract_id,
       task.output_contract_generation AS child_contract_generation,
       task.output_contract_digest::TEXT AS child_contract_digest
  FROM agent_control.runtime_task AS task
  JOIN agent_control.runtime_attempt AS attempt
    ON attempt.task_id = task.task_id
   AND attempt.run_id = task.run_id
 WHERE task.task_id = 'task-command-1'
   AND task.state = 'running'
   AND attempt.state = 'executing'
   AND attempt.lease_expires_at > clock_timestamp()
\gset

SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'child-request-probe-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api',
        'command_type', 'request_child_task',
        'idempotency_key', 'child-request-probe-idem-1',
        'request_digest', repeat('c', 64),
        'causation_id', 'cause-child-request-probe-1',
        'correlation_id', 'correlation-child-request-probe-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'parent_task_id', :'child_parent_task_id',
    'attempt_id', :'child_attempt_id',
    'expected_attempt_state_generation', :child_parent_generation,
    'lease_generation', :child_lease_generation,
    'lease_token', :'child_lease_token',
    'required_capability', 'market_research',
    'reason_code', 'delegate_research',
    'objective', :'child_objective'::JSONB,
    'input_refs', jsonb_build_array(),
    'output_contract', jsonb_build_object(
        'owner', 'agent_control',
        'record_type', 'output_contract_revision',
        'record_id', :'child_contract_id',
        'schema_revision', 1,
        'record_digest', :'child_contract_digest',
        'generation', :child_contract_generation
    ),
    'requested_limit', jsonb_build_object(
        'max_model_calls', 0, 'max_input_tokens', 0, 'max_output_tokens', 0,
        'max_tool_calls', 0, 'max_external_cost_micro_usd', 0,
        'max_wall_time_ms', 1, 'max_idle_time_ms', 0, 'max_tasks', 0,
        'max_depth', 0, 'max_fanout', 0, 'max_parallelism', 1,
        'max_invalid_output_retries', 0, 'max_infrastructure_retries', 0
    )
)::TEXT AS child_request_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.request_child_task(:'child_request_command')::TEXT
    AS child_request_response
\gset
-- Exact delivery replay returns the first durable response without making a
-- second request.
SELECT agent_control.request_child_task(:'child_request_command')::TEXT
    AS child_request_replay
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
\echo child-request-assert-replay
SELECT CASE WHEN EXISTS (
                    SELECT 1
                      FROM agent_control.runtime_command AS command
                     WHERE command.command_id = 'child-request-probe-1'
                       AND command.command_type = 'request_child_task'
                       AND command.state = 'committed'
                       AND command.response @> jsonb_build_object(
                           'status', 'committed',
                           'child_request_id', 'child-request-probe-1',
                           'request_state', 'pending_control',
                           'reason_code', 'child_request_pending_control'
                       )
                ) THEN 1 ELSE 1 / (random()::INTEGER) END AS child_request_replay_ok;
\echo child-request-assert-persisted
SELECT CASE WHEN (SELECT count(*)
                    FROM agent_control.runtime_child_task_request AS request
                   WHERE request.request_id = 'child-request-probe-1'
                     AND request.parent_task_id = :'child_parent_task_id'
                     AND request.required_capability = 'market_research'
                     AND request.reason_code = 'delegate_research') = 1
                 AND (SELECT count(*)
                        FROM agent_control.runtime_task AS task
                       WHERE task.parent_task_id = :'child_parent_task_id') = 0
            THEN 1 ELSE 1 / (random()::INTEGER) END AS child_request_persisted_not_admitted;

SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            jsonb_set(
                :'child_request_command'::JSONB,
                '{envelope,command_id}', to_jsonb('child-request-stale-1'::TEXT)
            ),
            '{envelope,idempotency_key}', to_jsonb('child-request-stale-idem-1'::TEXT)
        ),
        '{envelope,request_digest}', to_jsonb(repeat('d', 64))
    ),
    '{expected_attempt_state_generation}', to_jsonb(999::BIGINT)
)::TEXT AS child_request_stale_command
\gset

\echo child-request-call-stale
RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.request_child_task(:'child_request_stale_command')::TEXT
    AS child_request_stale_response
\gset
\echo child-request-assert-acl
SELECT CASE WHEN has_table_privilege(
    'alpheus_agent_worker', 'agent_control.runtime_child_task_request', 'SELECT'
) THEN 1 / (random()::INTEGER) ELSE 1 END AS child_request_worker_no_table_read;
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
\echo child-request-assert-stale-code
SELECT CASE WHEN :'child_request_stale_response'::JSONB @> jsonb_build_object(
                 'status', 'denied',
                 'reason_code', 'stale_parent_task_generation'
            ) THEN 1 ELSE 1 / (random()::INTEGER) END AS child_request_stale_reason_code;

SELECT 'AP1_CHILD_TASK_REQUESTS_PASS' AS marker;
RESET ROLE;
