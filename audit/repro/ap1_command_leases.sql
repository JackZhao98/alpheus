-- AP1 transactional command proof. The caller is a real NOINHERIT LOGIN that
-- must SET ROLE to the narrow Worker group; session_user remains the audit root.
-- This probe is non-money: it has no Kernel, Provider, operation, or broker path.
\set ON_ERROR_STOP on

RESET ROLE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = 'worker-2'
    ) THEN
        CREATE ROLE "worker-2" LOGIN;
    END IF;
    ALTER ROLE "worker-2" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
        NOINHERIT NOREPLICATION NOBYPASSRLS;
END
$$;
GRANT alpheus_agent_worker TO "worker-2";

SET ROLE alpheus_agent_migrator;

-- Activate the exact immutable sources already created by the AP1 state
-- fixture. Command admission must lock and match all three current heads.
INSERT INTO agent_control.runtime_policy_head (
    policy_id, generation, record_digest, selected_by_principal_id,
    selected_by_kind, selected_by_audience, selected_at
) VALUES (
    'runtime-policy-1', 1, repeat('1', 64), 'control-1',
    'workload', 'control_api', clock_timestamp()
);

INSERT INTO platform_governance.owner_policy_head (
    head_id, schema_revision, generation, revision_id, revision_digest,
    activated_by_principal_id, activated_by_kind, activated_by_audience,
    activated_at
) VALUES (
    'owner-policy-1', 1, 1, 'owner-revision-1', repeat('2', 64),
    'activator-1', 'workload', 'activator', clock_timestamp()
);

INSERT INTO agent_control.trigger_registration_head (
    registration_id, generation, record_digest, selected_by_principal_id,
    selected_by_kind, selected_by_audience, selected_at
) VALUES (
    'registration-1', 1, repeat('3', 64), 'control-1',
    'workload', 'control_api', clock_timestamp()
);

-- Derive one fresh queued Run from the already-proven exact-lineage fixture.
-- The root Task's own ledger intentionally has limit_tasks=0: it budgets only
-- descendants and must not prevent the Task itself from being claimed.
BEGIN;
SET CONSTRAINTS ALL DEFERRED;

INSERT INTO agent_control.trigger_occurrence
SELECT (jsonb_populate_record(
    NULL::agent_control.trigger_occurrence,
    to_jsonb(source_row) || jsonb_build_object(
        'occurrence_id', 'occurrence-command-1',
        'record_digest', repeat('7', 64),
        'occurrence_key', 'occurrence-command-key-1',
        'occurred_at', clock_timestamp() - interval '4 seconds',
        'observed_at', clock_timestamp() - interval '3 seconds',
        'committed_at', clock_timestamp() - interval '2 seconds'
    )
)).*
FROM agent_control.trigger_occurrence AS source_row
WHERE source_row.occurrence_id = 'occurrence-1';

INSERT INTO agent_control.runtime_run
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_run,
    to_jsonb(source_row) || jsonb_build_object(
        'run_id', 'run-command-1',
        'occurrence_id', occurrence.occurrence_id,
        'occurrence_digest', occurrence.record_digest,
        'origin_occurred_at', occurrence.occurred_at,
        'origin_observed_at', occurrence.observed_at,
        'origin_committed_at', occurrence.committed_at,
        'budget_ledger_id', 'run-ledger-command-1',
        'root_task_id', 'task-command-1',
        'state', 'queued',
        'state_generation', 1,
        'superseded_by', NULL,
        'failure', NULL,
        'created_at', clock_timestamp(),
        'updated_at', clock_timestamp(),
        'deadline_at', clock_timestamp() + interval '1 hour',
        'terminal_at', NULL
    )
)).*
FROM agent_control.runtime_run AS source_row
JOIN agent_control.trigger_occurrence AS occurrence
  ON occurrence.occurrence_id = 'occurrence-command-1'
WHERE source_row.run_id = 'run-1';

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
    consumed_tasks, generation, state, updated_at
) VALUES
    (
        'run-ledger-command-1', 1, 'run', 'run-command-1', NULL,
        'agent_control', 'runtime_policy', 'runtime-policy-1', 1, 1,
        repeat('1', 64),
        10, 10000, 10000, 10, 1000000, 3600000, 600000, 10,
        4, 4, 2, 2, 2,
        1, 1, 'open', clock_timestamp()
    ),
    (
        'task-ledger-command-1', 1, 'task', 'task-command-1',
        'run-ledger-command-1',
        'agent_control', 'runtime_policy', 'runtime-policy-1', 1, 1,
        repeat('1', 64),
        5, 5000, 5000, 5, 500000, 1800000, 300000, 0,
        2, 2, 1, 1, 1,
        0, 1, 'open', clock_timestamp()
    );

INSERT INTO agent_control.runtime_task (
    task_id, schema_revision, run_id, parent_task_id, depth, objective,
    output_contract_owner, output_contract_record_type,
    output_contract_revision_id, output_contract_schema_revision,
    output_contract_generation, output_contract_digest,
    budget_ledger_id, session_id, state, state_generation,
    budget_slot_held, created_at, updated_at, deadline_at
)
SELECT
    'task-command-1', 1, 'run-command-1', NULL, 0,
    jsonb_set(
        source_row.objective,
        '{origin,record_id}',
        to_jsonb('task-command-1'::TEXT)
    ),
    source_row.output_contract_owner,
    source_row.output_contract_record_type,
    source_row.output_contract_revision_id,
    source_row.output_contract_schema_revision,
    source_row.output_contract_generation,
    source_row.output_contract_digest,
    'task-ledger-command-1', 'session-command-1',
    'ready', 1, false,
    clock_timestamp(), clock_timestamp(), clock_timestamp() + interval '50 minutes'
FROM agent_control.runtime_task AS source_row
WHERE source_row.task_id = 'task-root-1';

INSERT INTO agent_control.runtime_session (
    session_id, schema_revision, run_id, task_id, generation,
    execution_binding, context_manifest, state, created_at
)
SELECT
    'session-command-1', 1, 'run-command-1', 'task-command-1', 1,
    jsonb_set(
        source_row.execution_binding,
        '{origin,record_id}',
        to_jsonb('session-command-1'::TEXT)
    ),
    jsonb_set(
        source_row.context_manifest,
        '{origin,record_id}',
        to_jsonb('session-command-1'::TEXT)
    ),
    'open', clock_timestamp()
FROM agent_control.runtime_session AS source_row
WHERE source_row.session_id = 'session-1';

COMMIT;

-- Build the claim outside the Worker role so no direct Runtime read is needed.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'claim-command-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1',
            'kind', 'workload',
            'audience', 'worker'
        ),
        'audience', 'control_api',
        'command_type', 'claim_task',
        'idempotency_key', 'claim-command-idem-1',
        'request_digest', repeat('a', 64),
        'causation_id', 'cause-claim-command-1',
        'correlation_id', 'correlation-command-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'task_id', 'task-command-1',
    'expected_task_state_generation', 1,
    'requested_lease_seconds', 5
)::TEXT AS claim_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.claim_task(:'claim_command')::TEXT AS claim_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;

SELECT (
    :'claim_response'::JSONB->>'status' = 'committed'
    AND :'claim_response'::JSONB->>'reclaimed' = 'false'
    AND EXISTS (
        SELECT 1 FROM agent_control.runtime_run
        WHERE run_id = 'run-command-1' AND state = 'running'
          AND state_generation = 2
    ) AND EXISTS (
        SELECT 1 FROM agent_control.runtime_task
        WHERE task_id = 'task-command-1' AND state = 'running'
          AND state_generation = 2 AND budget_slot_held
    ) AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'run-ledger-command-1'
          AND consumed_tasks = 1 AND consumed_active_tasks = 1
          AND generation = 2
    ) AND EXISTS (
        SELECT 1 FROM agent_control.runtime_budget_ledger
        WHERE ledger_id = 'task-ledger-command-1'
          AND limit_tasks = 0 AND consumed_active_tasks = 0
          AND generation = 1
    ) AND (SELECT count(*) FROM agent_control.runtime_attempt
           WHERE task_id = 'task-command-1' AND state = 'leased') = 1
) AS claim_committed_correctly
\gset
\if :claim_committed_correctly
\else
    \echo 'FAIL claim did not atomically advance state and ancestor budget'
    \quit 1
\endif

SELECT count(*) AS claim_event_count
FROM agent_control.runtime_event
WHERE causation_id = 'cause-claim-command-1'
\gset
SELECT consumed_active_tasks AS claim_active_count
FROM agent_control.runtime_budget_ledger
WHERE ledger_id = 'run-ledger-command-1'
\gset

-- Exact retry deliberately changes only CommandID. The original response and
-- all mutations must be replayed, not executed again.
SELECT jsonb_set(
    :'claim_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('claim-command-retry-1'::TEXT)
)::TEXT AS claim_retry_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.claim_task(:'claim_retry_command')::TEXT
    AS claim_retry_response
\gset

\set ON_ERROR_STOP off
SELECT agent_control.claim_task(jsonb_set(
    :'claim_command'::JSONB,
    '{requested_lease_seconds}',
    '6'::JSONB
)::TEXT);
SELECT :'SQLSTATE' = '23505' AS changed_body_rejected
\gset
\set ON_ERROR_STOP on
\if :changed_body_rejected
\else
    \echo 'FAIL changed-body idempotency reuse was accepted'
    \quit 1
\endif

-- Spoofed actor and an unknown top-level field are invalid before a durable
-- command row is created.
\set ON_ERROR_STOP off
SELECT agent_control.claim_task((jsonb_set(
    :'claim_command'::JSONB,
    '{envelope,actor,principal_id}',
    to_jsonb('worker-2'::TEXT)
) || jsonb_build_object('worker_id', 'spoof'))::TEXT);
SELECT :'SQLSTATE' = '22023' AS spoof_rejected
\gset
\set ON_ERROR_STOP on
\if :spoof_rejected
\else
    \echo 'FAIL spoofed/unknown command shape was accepted'
    \quit 1
\endif

-- Raw TEXT is intentional: JSONB would erase both duplicate keys and exponent
-- notation before the frozen canonical-profile checks could reject them.
SELECT left(:'claim_command', length(:'claim_command') - 1)
    || ',"task_id":"duplicate-task"}' AS duplicate_key_command,
    regexp_replace(
        :'claim_command', '"schema_revision": 1', '"schema_revision": 1e0'
    ) AS exponent_number_command,
    jsonb_set(
        :'claim_command'::JSONB,
        '{envelope,deadline}',
        to_jsonb('2026-07-19T24:00:00Z'::TEXT)
    )::TEXT AS normalized_time_command
\gset

\set ON_ERROR_STOP off
SELECT agent_control.claim_task(:'duplicate_key_command');
SELECT :'SQLSTATE' = '22023' AS duplicate_key_rejected
\gset
SELECT agent_control.claim_task(:'exponent_number_command');
SELECT :'SQLSTATE' = '22023' AS exponent_number_rejected
\gset
SELECT agent_control.claim_task(:'normalized_time_command');
SELECT :'SQLSTATE' = '22023' AS normalized_time_rejected
\gset
\set ON_ERROR_STOP on
SELECT :'duplicate_key_rejected'::BOOLEAN
    AND :'exponent_number_rejected'::BOOLEAN
    AND :'normalized_time_rejected'::BOOLEAN AS lexical_input_rejected
\gset
\if :lexical_input_rejected
\else
    \echo 'FAIL raw JSON/RFC3339 lexical validation did not fail closed'
    \quit 1
\endif

RESET ROLE;
RESET SESSION AUTHORIZATION;
SET ROLE alpheus_agent_migrator;

SELECT (
    :'claim_retry_response'::JSONB = :'claim_response'::JSONB
    AND (SELECT count(*) FROM agent_control.runtime_command
           WHERE principal_id = 'worker-1' AND command_type = 'claim_task'
             AND idempotency_key = 'claim-command-idem-1') = 1
    AND (SELECT count(*) FROM agent_control.runtime_event
           WHERE causation_id = 'cause-claim-command-1') = :claim_event_count
    AND (SELECT consumed_active_tasks
           FROM agent_control.runtime_budget_ledger
           WHERE ledger_id = 'run-ledger-command-1') = :claim_active_count
) AS claim_replayed_exactly
\gset
\if :claim_replayed_exactly
\else
    \echo 'FAIL claim exact replay mutated durable state'
    \quit 1
\endif

-- First-use expiry is a durable denial, and the stored denial is replayed
-- even when the retry carries a fresh CommandID.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1,
        'command_id', 'claim-expired-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'claim_task',
        'idempotency_key', 'claim-expired-idem-1',
        'request_digest', repeat('b', 64),
        'causation_id', 'cause-expired-command-1',
        'correlation_id', 'correlation-command-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() - interval '1 second'
        )
    ),
    'task_id', 'missing-task-command-1',
    'expected_task_state_generation', 1,
    'requested_lease_seconds', 5
)::TEXT AS expired_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.claim_task(:'expired_command')::TEXT
    AS expired_response
\gset
SELECT agent_control.claim_task(jsonb_set(
    :'expired_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('claim-expired-retry-1'::TEXT)
)::TEXT)::TEXT AS expired_retry_response
\gset

\set ON_ERROR_STOP off
SELECT agent_control.claim_task(jsonb_set(
    :'claim_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('claim-expired-1'::TEXT)
)::TEXT);
SELECT :'SQLSTATE' = '23505' AS retry_command_id_collision_rejected
\gset
\set ON_ERROR_STOP on
\if :retry_command_id_collision_rejected
\else
    \echo 'FAIL retry reused another durable command id'
    \quit 1
\endif
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'expired_response'::JSONB->>'reason_code' = 'command_deadline_expired'
    AND :'expired_retry_response'::JSONB = :'expired_response'::JSONB
    AND EXISTS (
           SELECT 1 FROM agent_control.runtime_command
           WHERE principal_id = 'worker-1' AND command_type = 'claim_task'
             AND idempotency_key = 'claim-expired-idem-1' AND state = 'denied'
    )
) AS expired_command_durable
\gset
\if :expired_command_durable
\else
    \echo 'FAIL expired command was not durably denied/replayed'
    \quit 1
\endif

-- Start the claimed Attempt with its exact state and lease fence.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'start-command-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'start_attempt',
        'idempotency_key', 'start-command-idem-1',
        'request_digest', repeat('c', 64),
        'causation_id', 'cause-start-command-1',
        'correlation_id', 'correlation-command-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'claim_response'::JSONB->>'attempt_id',
    'expected_attempt_state_generation',
        (:'claim_response'::JSONB->>'attempt_state_generation')::BIGINT,
    'lease_generation',
        (:'claim_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'claim_response'::JSONB->>'lease_token'
)::TEXT AS start_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.start_attempt(:'start_command')::TEXT
    AS start_response
\gset
SELECT agent_control.start_attempt(jsonb_set(
    :'start_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('start-command-retry-1'::TEXT)
)::TEXT)::TEXT AS start_retry_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'start_response'::JSONB->>'attempt_state' = 'executing'
    AND :'start_response'::JSONB = :'start_retry_response'::JSONB
    AND EXISTS (
           SELECT 1 FROM agent_control.runtime_attempt
           WHERE attempt_id = :'start_response'::JSONB->>'attempt_id'
             AND state = 'executing' AND state_generation = 2
    )
) AS start_committed_exactly
\gset
\if :start_committed_exactly
\else
    \echo 'FAIL start_attempt did not commit/replay exactly'
    \quit 1
\endif

-- A 60-second heartbeat after a 5-second claim must advance from database now.
SELECT jsonb_build_object(
    'schema_revision', 1,
    'envelope', jsonb_build_object(
        'schema_revision', 1, 'command_id', 'heartbeat-command-1',
        'actor', jsonb_build_object(
            'principal_id', 'worker-1', 'kind', 'workload', 'audience', 'worker'
        ),
        'audience', 'control_api', 'command_type', 'heartbeat_attempt',
        'idempotency_key', 'heartbeat-command-idem-1',
        'request_digest', repeat('d', 64),
        'causation_id', 'cause-heartbeat-command-1',
        'correlation_id', 'correlation-command-1',
        'deadline', agent_control.runtime_utc_text(
            clock_timestamp() + interval '5 minutes'
        )
    ),
    'attempt_id', :'start_response'::JSONB->>'attempt_id',
    'expected_attempt_state_generation', 2,
    'lease_generation',
        (:'start_response'::JSONB->>'lease_generation')::BIGINT,
    'lease_token', :'start_response'::JSONB->>'lease_token',
    'requested_extension_seconds', 60
)::TEXT AS heartbeat_command
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.heartbeat_attempt(:'heartbeat_command')::TEXT
    AS heartbeat_response
\gset
SELECT agent_control.heartbeat_attempt(jsonb_set(
    :'heartbeat_command'::JSONB,
    '{envelope,command_id}',
    to_jsonb('heartbeat-command-retry-1'::TEXT)
)::TEXT)::TEXT AS heartbeat_retry_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT count(*) AS heartbeat_event_count
FROM agent_control.runtime_attempt_lease_event
WHERE attempt_id = :'heartbeat_response'::JSONB->>'attempt_id'
  AND transition = 'heartbeat'
\gset
SELECT lease_expires_at::TEXT AS first_heartbeat_expiry
FROM agent_control.runtime_attempt
WHERE attempt_id = :'heartbeat_response'::JSONB->>'attempt_id'
\gset

SELECT (
    :'heartbeat_response'::JSONB = :'heartbeat_retry_response'::JSONB
    AND :heartbeat_event_count = 1
) AS heartbeat_replayed_exactly
\gset
\if :heartbeat_replayed_exactly
\else
    \echo 'FAIL heartbeat exact replay repeated its effect'
    \quit 1
\endif

-- A second unique heartbeat may advance by elapsed database time, but may not
-- stack another full extension on top of the old expiry.
SELECT jsonb_set(
    jsonb_set(
        :'heartbeat_command'::JSONB,
        '{envelope,command_id}',
        to_jsonb('heartbeat-command-2'::TEXT)
    ),
    '{envelope,idempotency_key}',
    to_jsonb('heartbeat-command-idem-2'::TEXT)
)::TEXT AS heartbeat_command_2
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
SELECT agent_control.heartbeat_attempt(:'heartbeat_command_2')::TEXT
    AS heartbeat_response_2
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'heartbeat_response_2'::JSONB->>'status' = 'committed'
    AND lease_expires_at > :'first_heartbeat_expiry'::TIMESTAMPTZ
    AND lease_expires_at < :'first_heartbeat_expiry'::TIMESTAMPTZ
        + interval '30 seconds'
    AND lease_expires_at <= clock_timestamp() + interval '61 seconds'
) AS heartbeat_did_not_stack
FROM agent_control.runtime_attempt
WHERE attempt_id = :'heartbeat_response_2'::JSONB->>'attempt_id'
\gset
\if :heartbeat_did_not_stack
\else
    \echo 'FAIL heartbeat extensions stacked or escaped database time'
    \quit 1
\endif

-- Possession of the token is insufficient: the authenticated worker is part
-- of the fence, and a mismatch must be a durable generic denial.
SELECT jsonb_set(
    jsonb_set(
        jsonb_set(
            :'heartbeat_command'::JSONB,
            '{envelope,command_id}',
            to_jsonb('heartbeat-wrong-worker-1'::TEXT)
        ),
        '{envelope,idempotency_key}',
        to_jsonb('heartbeat-wrong-worker-idem-1'::TEXT)
    ),
    '{envelope,actor,principal_id}',
    to_jsonb('worker-2'::TEXT)
)::TEXT AS wrong_worker_command
\gset

SELECT lease_expires_at::TEXT AS before_wrong_worker_expiry
FROM agent_control.runtime_attempt
WHERE attempt_id = :'heartbeat_response_2'::JSONB->>'attempt_id'
\gset
SELECT count(*) AS before_wrong_worker_events
FROM agent_control.runtime_attempt_lease_event
WHERE attempt_id = :'heartbeat_response_2'::JSONB->>'attempt_id'
\gset

RESET ROLE;
SET SESSION AUTHORIZATION "worker-2";
SET ROLE alpheus_agent_worker;
SELECT agent_control.heartbeat_attempt(:'wrong_worker_command')::TEXT
    AS wrong_worker_response
\gset
RESET ROLE;
RESET SESSION AUTHORIZATION;

SET ROLE alpheus_agent_migrator;
SELECT (
    :'wrong_worker_response'::JSONB->>'reason_code' = 'stale_lease_fence'
    AND (SELECT lease_expires_at FROM agent_control.runtime_attempt
           WHERE attempt_id = :'heartbeat_response_2'::JSONB->>'attempt_id')
            = :'before_wrong_worker_expiry'::TIMESTAMPTZ
    AND (SELECT count(*) FROM agent_control.runtime_attempt_lease_event
           WHERE attempt_id = :'heartbeat_response_2'::JSONB->>'attempt_id')
            = :before_wrong_worker_events
) AS wrong_worker_denied
\gset
\if :wrong_worker_denied
\else
    \echo 'FAIL wrong Worker crossed or mutated the lease fence'
    \quit 1
\endif

RESET ROLE;

-- Non-Workers do not have the public entrypoint at all.
SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
\set ON_ERROR_STOP off
SELECT agent_control.claim_task(:'claim_command');
SELECT :'SQLSTATE' = '42501' AS nonworker_denied
\gset
\set ON_ERROR_STOP on
\if :nonworker_denied
\else
    \echo 'FAIL Control API executed Worker command'
    \quit 1
\endif
RESET ROLE;
RESET SESSION AUTHORIZATION;

-- Worker has no direct table/helper access despite schema USAGE.
SET SESSION AUTHORIZATION "worker-1";
SET ROLE alpheus_agent_worker;
DO $$
BEGIN
    BEGIN
        PERFORM * FROM agent_control.runtime_task LIMIT 1;
        RAISE EXCEPTION 'Worker read Runtime table directly';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
    BEGIN
        PERFORM agent_control.runtime_sha256_json('{}'::JSONB);
        RAISE EXCEPTION 'Worker executed private Runtime helper';
    EXCEPTION WHEN insufficient_privilege THEN
        NULL;
    END;
END
$$;
RESET ROLE;
RESET SESSION AUTHORIZATION;

-- Catalog inventory: three public definer commands, fixed path/time zone,
-- migrator ownership, Worker-group execute, and no PUBLIC execute.
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
          'claim_task', 'start_attempt', 'heartbeat_attempt'
      )
      AND pg_catalog.pg_get_function_identity_arguments(routine.oid) = 'p_command text'
      AND routine.prosecdef
      AND owner_role.rolname = 'alpheus_agent_migrator'
      AND 'search_path=pg_catalog, agent_control, platform_security' = ANY(routine.proconfig)
      AND 'TimeZone=UTC' = ANY(routine.proconfig)
      AND pg_catalog.has_function_privilege(
          'alpheus_agent_worker', routine.oid, 'EXECUTE'
      )
      AND NOT pg_catalog.has_function_privilege('public', routine.oid, 'EXECUTE');
    IF command_count <> 3 THEN
        RAISE EXCEPTION 'public Worker command ACL/catalog inventory mismatch: %',
            command_count;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_catalog.pg_proc AS routine
        JOIN pg_catalog.pg_namespace AS namespace
          ON namespace.oid = routine.pronamespace
        JOIN pg_catalog.pg_roles AS owner_role
          ON owner_role.oid = routine.proowner
        WHERE namespace.nspname = 'agent_control'
          AND routine.proname = 'validate_runtime_unresolved_turn_attempt'
          AND routine.prosecdef
          AND owner_role.rolname = 'alpheus_agent_migrator'
          AND 'search_path=pg_catalog, agent_control' = ANY(routine.proconfig)
          AND NOT pg_catalog.has_function_privilege(
              'alpheus_agent_worker', routine.oid, 'EXECUTE'
          )
          AND NOT pg_catalog.has_function_privilege(
              'public', routine.oid, 'EXECUTE'
          )
    ) THEN
        RAISE EXCEPTION 'deferred unresolved-Turn trigger is not safely owned';
    END IF;

    IF EXISTS (
        SELECT 1 FROM agent_control.runtime_command WHERE state = 'processing'
    ) THEN
        RAISE EXCEPTION 'processing Runtime command leaked after probe';
    END IF;
END
$$;

-- Leave one independent queued root for the in-process concurrency barrier.
-- It shares immutable policy sources but has distinct occurrence, Run, Task,
-- Session, and ledgers, so the sequential assertions above remain isolated.
SET ROLE alpheus_agent_migrator;
BEGIN;
SET CONSTRAINTS ALL DEFERRED;

INSERT INTO agent_control.trigger_occurrence
SELECT (jsonb_populate_record(
    NULL::agent_control.trigger_occurrence,
    to_jsonb(source_row) || jsonb_build_object(
        'occurrence_id', 'occurrence-command-concurrency-1',
        'record_digest', repeat('8', 64),
        'occurrence_key', 'occurrence-command-concurrency-key-1',
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
        'run_id', 'run-command-concurrency-1',
        'occurrence_id', occurrence.occurrence_id,
        'occurrence_digest', occurrence.record_digest,
        'origin_occurred_at', occurrence.occurred_at,
        'origin_observed_at', occurrence.observed_at,
        'origin_committed_at', occurrence.committed_at,
        'budget_ledger_id', 'run-ledger-command-concurrency-1',
        'root_task_id', 'task-command-concurrency-1',
        'state', 'queued', 'state_generation', 1,
        'superseded_by', NULL, 'failure', NULL,
        'created_at', clock_timestamp(), 'updated_at', clock_timestamp(),
        'deadline_at', clock_timestamp() + interval '1 hour',
        'terminal_at', NULL
    )
)).*
FROM agent_control.runtime_run AS source_row
JOIN agent_control.trigger_occurrence AS occurrence
  ON occurrence.occurrence_id = 'occurrence-command-concurrency-1'
WHERE source_row.run_id = 'run-command-1';

INSERT INTO agent_control.runtime_budget_ledger
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_budget_ledger,
    to_jsonb(source_row) || jsonb_build_object(
        'ledger_id', 'run-ledger-command-concurrency-1',
        'scope_id', 'run-command-concurrency-1',
        'consumed_active_tasks', 0,
        'generation', 1,
        'updated_at', clock_timestamp()
    )
)).*
FROM agent_control.runtime_budget_ledger AS source_row
WHERE source_row.ledger_id = 'run-ledger-command-1';

INSERT INTO agent_control.runtime_budget_ledger
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_budget_ledger,
    to_jsonb(source_row) || jsonb_build_object(
        'ledger_id', 'task-ledger-command-concurrency-1',
        'scope_id', 'task-command-concurrency-1',
        'parent_ledger_id', 'run-ledger-command-concurrency-1',
        'generation', 1,
        'updated_at', clock_timestamp()
    )
)).*
FROM agent_control.runtime_budget_ledger AS source_row
WHERE source_row.ledger_id = 'task-ledger-command-1';

INSERT INTO agent_control.runtime_task
SELECT (jsonb_populate_record(
    NULL::agent_control.runtime_task,
    to_jsonb(source_row) || jsonb_build_object(
        'task_id', 'task-command-concurrency-1',
        'run_id', 'run-command-concurrency-1',
        'objective', jsonb_set(
            source_row.objective,
            '{origin,record_id}',
            to_jsonb('task-command-concurrency-1'::TEXT)
        ),
        'budget_ledger_id', 'task-ledger-command-concurrency-1',
        'session_id', 'session-command-concurrency-1',
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
        'session_id', 'session-command-concurrency-1',
        'run_id', 'run-command-concurrency-1',
        'task_id', 'task-command-concurrency-1',
        'execution_binding', jsonb_set(
            source_row.execution_binding,
            '{origin,record_id}',
            to_jsonb('session-command-concurrency-1'::TEXT)
        ),
        'context_manifest', jsonb_set(
            source_row.context_manifest,
            '{origin,record_id}',
            to_jsonb('session-command-concurrency-1'::TEXT)
        ),
        'latest_checkpoint_id', NULL,
        'state', 'open', 'generation', 1,
        'created_at', clock_timestamp(), 'closed_at', NULL
    )
)).*
FROM agent_control.runtime_session AS source_row
WHERE source_row.session_id = 'session-command-1';

COMMIT;
RESET ROLE;

SELECT 'AP1_COMMAND_LEASES_PASS';
