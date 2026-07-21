SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- A Worker can ask for child work, but cannot create a runnable child Task,
-- Session, Agent binding, capability grant, Tool permission, or effect.  This
-- migration durably records the fenced request for a later Control/Scheduler
-- admission command.  All denials below use a stable reason_code in the
-- persisted runtime_command response.

CREATE FUNCTION agent_control.runtime_child_input_refs_valid(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT jsonb_typeof(p_value) = 'array'
       AND jsonb_array_length(p_value) <= 4096
       AND NOT EXISTS (
           SELECT 1
             FROM jsonb_array_elements(p_value) AS item(value)
            WHERE NOT agent_control.runtime_record_ref_valid(item.value, '', '')
       )
       AND NOT EXISTS (
           SELECT item.value
             FROM jsonb_array_elements(p_value) AS item(value)
            GROUP BY item.value
           HAVING count(*) > 1
       )
$$;

CREATE FUNCTION agent_control.runtime_child_revision_ref_valid(
    p_value JSONB,
    p_expected_owner TEXT,
    p_expected_record_type TEXT
) RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT jsonb_typeof(p_value) = 'object'
       AND p_value ?& ARRAY[
           'owner', 'record_type', 'record_id', 'schema_revision',
           'record_digest', 'generation'
       ]
       AND p_value - ARRAY[
           'owner', 'record_type', 'record_id', 'schema_revision',
           'record_digest', 'generation'
       ] = '{}'::JSONB
       AND agent_control.runtime_record_ref_valid(
               p_value - 'generation', p_expected_owner, p_expected_record_type
           )
       AND agent_control.runtime_positive_bigint_json(p_value->'generation')
$$;

CREATE FUNCTION agent_control.runtime_child_budget_limit_valid(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT jsonb_typeof(p_value) = 'object'
       AND p_value ?& ARRAY[
           'max_model_calls', 'max_input_tokens', 'max_output_tokens',
           'max_tool_calls', 'max_external_cost_micro_usd', 'max_wall_time_ms',
           'max_idle_time_ms', 'max_tasks', 'max_depth', 'max_fanout',
           'max_parallelism', 'max_invalid_output_retries',
           'max_infrastructure_retries'
       ]
       AND p_value - ARRAY[
           'max_model_calls', 'max_input_tokens', 'max_output_tokens',
           'max_tool_calls', 'max_external_cost_micro_usd', 'max_wall_time_ms',
           'max_idle_time_ms', 'max_tasks', 'max_depth', 'max_fanout',
           'max_parallelism', 'max_invalid_output_retries',
           'max_infrastructure_retries'
       ] = '{}'::JSONB
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_model_calls')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_input_tokens')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_output_tokens')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_tool_calls')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_external_cost_micro_usd')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_wall_time_ms')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_idle_time_ms')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_tasks')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_depth')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_fanout')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_parallelism')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_invalid_output_retries')
       AND agent_control.runtime_nonnegative_bigint_json(p_value->'max_infrastructure_retries')
       AND (p_value->>'max_wall_time_ms')::BIGINT > 0
       AND (p_value->>'max_parallelism')::BIGINT > 0
$$;

CREATE FUNCTION agent_control.runtime_request_child_task_command_valid(
    p_command JSONB
) RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
AS $$
    SELECT agent_control.runtime_worker_command_valid(
               p_command,
               'request_child_task',
               ARRAY[
                   'schema_revision', 'envelope', 'parent_task_id', 'attempt_id',
                   'expected_attempt_state_generation', 'lease_generation',
                   'lease_token', 'required_capability', 'reason_code',
                   'objective', 'input_refs', 'output_contract', 'requested_limit'
               ]
           )
       AND jsonb_typeof(p_command->'parent_task_id') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'parent_task_id')
       AND jsonb_typeof(p_command->'attempt_id') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'attempt_id')
       AND agent_control.runtime_positive_bigint_json(
               p_command->'expected_attempt_state_generation'
           )
       AND agent_control.runtime_positive_bigint_json(p_command->'lease_generation')
       AND jsonb_typeof(p_command->'lease_token') = 'string'
       AND agent_control.runtime_identifier_valid(p_command->>'lease_token')
       AND jsonb_typeof(p_command->'required_capability') = 'string'
       AND agent_control.runtime_name_valid(p_command->>'required_capability')
       AND jsonb_typeof(p_command->'reason_code') = 'string'
       AND agent_control.runtime_name_valid(p_command->>'reason_code')
       AND agent_control.runtime_blob_ref_valid(
               p_command->'objective', 'task_objective', ''
           )
       AND agent_control.runtime_child_input_refs_valid(p_command->'input_refs')
       AND agent_control.runtime_child_revision_ref_valid(
               p_command->'output_contract', 'agent_control',
               'output_contract_revision'
           )
       AND agent_control.runtime_child_budget_limit_valid(
               p_command->'requested_limit'
           )
$$;

CREATE TABLE agent_control.runtime_child_task_request (
    request_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(request_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (
        agent_control.runtime_digest_valid(record_digest::TEXT)
    ),
    command_principal_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(command_principal_id)
    ),
    command_id TEXT NOT NULL UNIQUE CHECK (agent_control.runtime_identifier_valid(command_id)),
    run_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(run_id)),
    parent_task_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(parent_task_id)),
    attempt_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(attempt_id)),
    required_capability TEXT NOT NULL CHECK (
        agent_control.runtime_name_valid(required_capability)
    ),
    reason_code TEXT NOT NULL CHECK (agent_control.runtime_name_valid(reason_code)),
    objective JSONB NOT NULL CHECK (
        agent_control.runtime_blob_ref_valid(objective, 'task_objective', '')
    ),
    input_refs JSONB NOT NULL CHECK (
        agent_control.runtime_child_input_refs_valid(input_refs)
    ),
    output_contract_owner TEXT NOT NULL CHECK (output_contract_owner = 'agent_control'),
    output_contract_record_type TEXT NOT NULL CHECK (
        output_contract_record_type = 'output_contract_revision'
    ),
    output_contract_revision_id TEXT NOT NULL CHECK (
        agent_control.runtime_identifier_valid(output_contract_revision_id)
    ),
    output_contract_schema_revision SMALLINT NOT NULL CHECK (
        output_contract_schema_revision = 1
    ),
    output_contract_generation BIGINT NOT NULL CHECK (output_contract_generation > 0),
    output_contract_digest CHAR(64) NOT NULL CHECK (
        agent_control.runtime_digest_valid(output_contract_digest::TEXT)
    ),
    requested_limit JSONB NOT NULL CHECK (
        agent_control.runtime_child_budget_limit_valid(requested_limit)
    ),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (attempt_id, record_digest),
    FOREIGN KEY (parent_task_id, run_id)
        REFERENCES agent_control.runtime_task(task_id, run_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (attempt_id, run_id, parent_task_id)
        REFERENCES agent_control.runtime_attempt(attempt_id, run_id, task_id)
        DEFERRABLE INITIALLY DEFERRED,
    FOREIGN KEY (
        output_contract_revision_id, output_contract_generation, output_contract_digest
    ) REFERENCES agent_control.output_contract_revision(
        revision_id, generation, record_digest
    ) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX runtime_child_task_request_pending_idx
ON agent_control.runtime_child_task_request (run_id, parent_task_id, created_at, request_id);

CREATE TRIGGER runtime_child_task_request_immutable
BEFORE UPDATE OR DELETE ON agent_control.runtime_child_task_request
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.runtime_child_limit_within_parent(
    p_requested JSONB,
    p_parent agent_control.runtime_budget_ledger
) RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
STRICT
AS $$
    SELECT (p_requested->>'max_model_calls')::BIGINT
               <= p_parent.limit_model_calls - p_parent.consumed_model_calls - p_parent.reserved_model_calls
       AND (p_requested->>'max_input_tokens')::BIGINT
               <= p_parent.limit_input_tokens - p_parent.consumed_input_tokens - p_parent.reserved_input_tokens
       AND (p_requested->>'max_output_tokens')::BIGINT
               <= p_parent.limit_output_tokens - p_parent.consumed_output_tokens - p_parent.reserved_output_tokens
       AND (p_requested->>'max_tool_calls')::BIGINT
               <= p_parent.limit_tool_calls - p_parent.consumed_tool_calls - p_parent.reserved_tool_calls
       AND (p_requested->>'max_external_cost_micro_usd')::BIGINT
               <= p_parent.limit_external_cost_micro_usd - p_parent.consumed_external_cost_micro_usd - p_parent.reserved_external_cost_micro_usd
       AND (p_requested->>'max_wall_time_ms')::BIGINT
               <= p_parent.limit_wall_time_ms - p_parent.consumed_wall_time_ms - p_parent.reserved_wall_time_ms
       AND (p_requested->>'max_idle_time_ms')::BIGINT <= p_parent.limit_idle_time_ms
       AND (p_requested->>'max_tasks')::BIGINT
               <= p_parent.limit_tasks - p_parent.consumed_tasks - p_parent.reserved_tasks
       AND (p_requested->>'max_depth')::BIGINT < p_parent.limit_depth
       AND (p_requested->>'max_fanout')::BIGINT <= p_parent.limit_fanout
       AND (p_requested->>'max_parallelism')::BIGINT <= p_parent.limit_parallelism
       AND (p_requested->>'max_invalid_output_retries')::BIGINT
               <= p_parent.limit_invalid_output_retries - p_parent.consumed_invalid_output_retries - p_parent.reserved_invalid_output_retries
       AND (p_requested->>'max_infrastructure_retries')::BIGINT
               <= p_parent.limit_infrastructure_retries - p_parent.consumed_infrastructure_retries - p_parent.reserved_infrastructure_retries
$$;

CREATE FUNCTION agent_control.runtime_request_child_task(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, blob, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_command%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE;
    parent_task agent_control.runtime_task%ROWTYPE;
    attempt_row agent_control.runtime_attempt%ROWTYPE;
    parent_ledger agent_control.runtime_budget_ledger%ROWTYPE;
    envelope JSONB;
    principal TEXT;
    now_at TIMESTAMPTZ;
    child_request_digest CHAR(64);
    response JSONB;
BEGIN
    IF p_command IS NULL
       OR NOT agent_control.runtime_request_child_task_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid_request_child_task_command';
    END IF;

    envelope := p_command->'envelope';
    principal := envelope #>> '{actor,principal_id}';
    command_row := agent_control.runtime_begin_command(p_command);
    IF command_row.state IN ('committed', 'denied') THEN
        RETURN command_row.response;
    END IF;

    SELECT task.run_id INTO run_row.run_id
    FROM agent_control.runtime_task AS task
    WHERE task.task_id = p_command->>'parent_task_id';
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_task_not_found');
    END IF;

    SELECT * INTO run_row
    FROM agent_control.runtime_run AS run
    WHERE run.run_id = run_row.run_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_run_not_found');
    END IF;

    SELECT * INTO parent_task
    FROM agent_control.runtime_task AS task
    WHERE task.task_id = p_command->>'parent_task_id'
      AND task.run_id = run_row.run_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_task_run_mismatch');
    END IF;

    SELECT * INTO attempt_row
    FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.attempt_id = p_command->>'attempt_id'
    FOR UPDATE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_attempt_not_found');
    END IF;
    IF attempt_row.run_id <> run_row.run_id OR attempt_row.task_id <> parent_task.task_id THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_attempt_task_mismatch');
    END IF;

    now_at := clock_timestamp();
    IF now_at >= command_row.deadline_at THEN
        RETURN agent_control.runtime_deny_command(command_row, 'command_deadline_expired');
    END IF;
    IF run_row.state <> 'running' THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_run_not_running');
    END IF;
    IF parent_task.state <> 'running' THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_task_not_running');
    END IF;
    IF parent_task.state_generation <> (p_command->>'expected_attempt_state_generation')::BIGINT THEN
        RETURN agent_control.runtime_deny_command(command_row, 'stale_parent_task_generation');
    END IF;
    IF attempt_row.state <> 'executing' THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_attempt_not_executing');
    END IF;
    IF attempt_row.lease_generation <> (p_command->>'lease_generation')::BIGINT
       OR attempt_row.lease_token::TEXT <> p_command->>'lease_token'
       OR attempt_row.lease_worker->>'principal_id' <> principal THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_attempt_lease_stale');
    END IF;
    IF now_at >= attempt_row.lease_expires_at THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_attempt_lease_expired');
    END IF;
    IF now_at >= run_row.deadline_at OR now_at >= parent_task.deadline_at THEN
        RETURN agent_control.runtime_deny_command(command_row, 'runtime_deadline_expired');
    END IF;
    IF NOT agent_control.runtime_run_admission_current(run_row.run_id) THEN
        RETURN agent_control.runtime_deny_command(command_row, 'runtime_authority_not_current');
    END IF;
    PERFORM 1
    FROM agent_control.runtime_turn AS turn
    WHERE turn.attempt_id = attempt_row.attempt_id
      AND turn.state IN ('planned', 'dispatched', 'unknown')
    FOR UPDATE;
    IF FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_turn_unresolved');
    END IF;

    SELECT * INTO parent_ledger
    FROM agent_control.runtime_budget_ledger AS ledger
    WHERE ledger.ledger_id = parent_task.budget_ledger_id
    FOR UPDATE;
    IF NOT FOUND OR parent_ledger.state <> 'open' THEN
        RETURN agent_control.runtime_deny_command(command_row, 'parent_budget_unavailable');
    END IF;
    IF parent_ledger.limit_fanout <= 0 OR EXISTS (
        SELECT 1
        FROM agent_control.runtime_child_task_request AS request
        WHERE request.parent_task_id = parent_task.task_id
    ) AND (
        SELECT count(*)
        FROM agent_control.runtime_child_task_request AS request
        WHERE request.parent_task_id = parent_task.task_id
    ) >= parent_ledger.limit_fanout THEN
        RETURN agent_control.runtime_deny_command(command_row, 'child_fanout_exhausted');
    END IF;
    IF NOT agent_control.runtime_child_limit_within_parent(
        p_command->'requested_limit', parent_ledger
    ) THEN
        RETURN agent_control.runtime_deny_command(command_row, 'child_budget_exceeds_parent');
    END IF;
    PERFORM 1
    FROM agent_control.output_contract_revision AS contract
    WHERE contract.revision_id = p_command #>> '{output_contract,record_id}'
      AND contract.generation = (p_command #>> '{output_contract,generation}')::BIGINT
      AND contract.record_digest::TEXT = p_command #>> '{output_contract,record_digest}'
    FOR SHARE;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_command(command_row, 'child_output_contract_not_found');
    END IF;

    child_request_digest := agent_control.runtime_contract_digest(
        'agent_control.runtime_child_task_request.v1',
        jsonb_build_object(
            'request_id', command_row.command_id,
            'command_principal_id', principal,
            'run_id', run_row.run_id,
            'parent_task_id', parent_task.task_id,
            'attempt_id', attempt_row.attempt_id,
            'required_capability', p_command->>'required_capability',
            'reason_code', p_command->>'reason_code',
            'objective', p_command->'objective',
            'input_refs', p_command->'input_refs',
            'output_contract', p_command->'output_contract',
            'requested_limit', p_command->'requested_limit'
        )
    );
    INSERT INTO agent_control.runtime_child_task_request (
        request_id, schema_revision, record_digest, command_principal_id,
        command_id, run_id, parent_task_id, attempt_id, required_capability,
        reason_code, objective, input_refs, output_contract_owner,
        output_contract_record_type, output_contract_revision_id,
        output_contract_schema_revision, output_contract_generation,
        output_contract_digest, requested_limit, created_at
    ) VALUES (
        command_row.command_id, 1, child_request_digest, principal,
        command_row.command_id, run_row.run_id, parent_task.task_id,
        attempt_row.attempt_id, p_command->>'required_capability',
        p_command->>'reason_code', p_command->'objective', p_command->'input_refs',
        p_command #>> '{output_contract,owner}',
        p_command #>> '{output_contract,record_type}',
        p_command #>> '{output_contract,record_id}',
        (p_command #>> '{output_contract,schema_revision}')::SMALLINT,
        (p_command #>> '{output_contract,generation}')::BIGINT,
        p_command #>> '{output_contract,record_digest}',
        p_command->'requested_limit', now_at
    );

    response := jsonb_build_object(
        'schema_revision', 1,
        'status', 'committed',
        'command_id', command_row.command_id,
        'command_type', 'request_child_task',
        'child_request_id', command_row.command_id,
        'request_state', 'pending_control',
        'reason_code', 'child_request_pending_control'
    );
    RETURN agent_control.runtime_finish_command(command_row, 'committed', response);
END
$$;

CREATE FUNCTION agent_control.request_child_task(p_command TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    parsed JSONB := agent_control.runtime_parse_worker_command(p_command);
BEGIN
    IF parsed IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '22023',
            MESSAGE = 'invalid_raw_request_child_task_command';
    END IF;
    RETURN agent_control.runtime_request_child_task(parsed);
END
$$;

REVOKE ALL ON FUNCTION agent_control.runtime_child_input_refs_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_child_revision_ref_valid(
    JSONB, TEXT, TEXT
) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_child_budget_limit_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_request_child_task_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_child_limit_within_parent(
    JSONB, agent_control.runtime_budget_ledger
) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_request_child_task(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.request_child_task(TEXT) FROM PUBLIC;

GRANT EXECUTE ON FUNCTION agent_control.request_child_task(TEXT)
    TO alpheus_agent_worker;

RESET ROLE;
