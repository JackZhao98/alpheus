SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- AP1 cancellation is deliberately two-phase.  The Control Plane may record
-- a fenced intent here, but this command never cancels an in-flight provider
-- call, releases a reservation, or changes a Run/Task/Attempt/Turn.  The
-- later reconciler must lock and compare the target generation again.

CREATE TABLE agent_control.runtime_cancellation_command (
    principal_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(principal_id)),
    idempotency_key TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(idempotency_key)),
    command_id TEXT NOT NULL UNIQUE CHECK (agent_control.runtime_identifier_valid(command_id)),
    request_id TEXT NOT NULL UNIQUE CHECK (agent_control.runtime_identifier_valid(request_id)),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    body_fingerprint CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(body_fingerprint::TEXT)),
    causation_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(causation_id)),
    correlation_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(correlation_id)),
    deadline_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('processing', 'committed', 'denied')),
    response JSONB,
    response_digest CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    committed_at TIMESTAMPTZ,
    PRIMARY KEY (principal_id, idempotency_key),
    CHECK (response IS NULL OR (
        jsonb_typeof(response) = 'object' AND octet_length(response::TEXT) <= 1048576
    )),
    CHECK (response_digest IS NULL OR agent_control.runtime_digest_valid(response_digest::TEXT)),
    CHECK (
        (state = 'processing' AND response IS NULL AND response_digest IS NULL AND committed_at IS NULL)
        OR
        (state IN ('committed', 'denied') AND response IS NOT NULL
            AND response_digest IS NOT NULL AND committed_at IS NOT NULL
            AND committed_at >= created_at)
    )
);

CREATE TRIGGER runtime_cancellation_command_guard
BEFORE UPDATE OR DELETE ON agent_control.runtime_cancellation_command
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'state', 'response', 'response_digest', 'committed_at'
);

CREATE FUNCTION agent_control.runtime_cancellation_control_command_valid(p_command JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
STRICT
AS $$
DECLARE
    envelope JSONB;
    request JSONB;
    invoker RECORD;
BEGIN
    IF jsonb_typeof(p_command) <> 'object'
       OR NOT (p_command ?& ARRAY['schema_revision', 'envelope', 'request'])
       OR p_command - ARRAY['schema_revision', 'envelope', 'request'] <> '{}'::JSONB
       OR p_command->>'schema_revision' <> '1'
       OR jsonb_typeof(p_command->'envelope') <> 'object'
       OR jsonb_typeof(p_command->'request') <> 'object' THEN
        RETURN false;
    END IF;

    envelope := p_command->'envelope';
    request := p_command->'request';
    IF NOT (envelope ?& ARRAY[
            'schema_revision', 'command_id', 'actor', 'audience', 'command_type',
            'idempotency_key', 'request_digest', 'causation_id', 'correlation_id', 'deadline'
        ])
       OR envelope - ARRAY[
            'schema_revision', 'command_id', 'actor', 'audience', 'command_type',
            'idempotency_key', 'request_digest', 'causation_id', 'correlation_id', 'deadline'
        ] <> '{}'::JSONB
       OR envelope->>'schema_revision' <> '1'
       OR jsonb_typeof(envelope->'command_id') <> 'string'
       OR jsonb_typeof(envelope->'actor') <> 'object'
       OR jsonb_typeof(envelope->'audience') <> 'string'
       OR jsonb_typeof(envelope->'command_type') <> 'string'
       OR jsonb_typeof(envelope->'idempotency_key') <> 'string'
       OR jsonb_typeof(envelope->'request_digest') <> 'string'
       OR jsonb_typeof(envelope->'causation_id') <> 'string'
       OR jsonb_typeof(envelope->'correlation_id') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'command_id')
       OR NOT agent_control.runtime_actor_valid(envelope->'actor')
       OR envelope #>> '{actor,kind}' <> 'workload'
       OR envelope #>> '{actor,audience}' <> 'control_api'
       OR envelope->>'audience' <> 'control_api'
       OR envelope->>'command_type' <> 'submit_cancellation_request'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'idempotency_key')
       OR NOT agent_control.runtime_digest_valid(envelope->>'request_digest')
       OR NOT agent_control.runtime_identifier_valid(envelope->>'causation_id')
       OR NOT agent_control.runtime_identifier_valid(envelope->>'correlation_id')
       OR NOT agent_control.runtime_utc_instant_json(envelope->'deadline') THEN
        RETURN false;
    END IF;

    IF NOT (request ?& ARRAY[
            'schema_revision', 'request_id', 'target', 'target_id',
            'expected_state_generation', 'mode', 'actor', 'reason_code', 'requested_at'
        ])
       OR request - ARRAY[
            'schema_revision', 'request_id', 'target', 'target_id',
            'expected_state_generation', 'mode', 'superseded_by_run_id', 'actor',
            'reason_code', 'requested_at'
        ] <> '{}'::JSONB
       OR request->>'schema_revision' <> '1'
       OR jsonb_typeof(request->'request_id') <> 'string'
       OR jsonb_typeof(request->'target') <> 'string'
       OR jsonb_typeof(request->'target_id') <> 'string'
       OR jsonb_typeof(request->'expected_state_generation') <> 'number'
       OR jsonb_typeof(request->'mode') <> 'string'
       OR jsonb_typeof(request->'actor') <> 'object'
       OR jsonb_typeof(request->'reason_code') <> 'string'
       OR NOT agent_control.runtime_identifier_valid(request->>'request_id')
       OR request->>'target' NOT IN ('run', 'task')
       OR NOT agent_control.runtime_identifier_valid(request->>'target_id')
       OR NOT agent_control.runtime_positive_bigint_json(request->'expected_state_generation')
       OR request->>'mode' NOT IN ('cancel', 'supersede')
       OR NOT agent_control.runtime_actor_valid(request->'actor')
       OR request->'actor' <> envelope->'actor'
       OR NOT agent_control.runtime_name_valid(request->>'reason_code')
       OR NOT agent_control.runtime_utc_instant_json(request->'requested_at')
       OR (
            request->>'mode' = 'cancel'
            AND request ? 'superseded_by_run_id'
        )
       OR (
            request->>'mode' = 'supersede'
            AND (
                jsonb_typeof(request->'superseded_by_run_id') <> 'string'
                OR
                request->>'target' <> 'run'
                OR NOT agent_control.runtime_identifier_valid(request->>'superseded_by_run_id')
                OR request->>'superseded_by_run_id' = request->>'target_id'
            )
        ) THEN
        RETURN false;
    END IF;

    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    RETURN invoker.group_role = 'alpheus_agent_control_api'::NAME
       AND invoker.profile_id = 'control-api'
       AND invoker.owner_id = 'agent_control'
       AND envelope #>> '{actor,principal_id}' = invoker.principal_id;
EXCEPTION
    WHEN insufficient_privilege THEN RAISE;
    WHEN OTHERS THEN RETURN false;
END
$$;

CREATE FUNCTION agent_control.runtime_begin_cancellation_command(p_command JSONB)
RETURNS agent_control.runtime_cancellation_command
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    command_row agent_control.runtime_cancellation_command%ROWTYPE;
    envelope JSONB := p_command->'envelope';
    fingerprint CHAR(64) := agent_control.runtime_sha256_json(
        jsonb_set(p_command, '{envelope}', (p_command->'envelope') - 'command_id', false)
    );
    denied JSONB;
BEGIN
    INSERT INTO agent_control.runtime_cancellation_command (
        principal_id, idempotency_key, command_id, request_id, request_digest,
        body_fingerprint, causation_id, correlation_id, deadline_at, state
    ) VALUES (
        envelope #>> '{actor,principal_id}', envelope->>'idempotency_key',
        envelope->>'command_id', p_command #>> '{request,request_id}',
        envelope->>'request_digest', fingerprint, envelope->>'causation_id',
        envelope->>'correlation_id', (envelope->>'deadline')::TIMESTAMPTZ, 'processing'
    ) ON CONFLICT DO NOTHING;

    SELECT * INTO command_row
    FROM agent_control.runtime_cancellation_command
    WHERE principal_id = envelope #>> '{actor,principal_id}'
      AND idempotency_key = envelope->>'idempotency_key'
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'cancellation_command_identity_conflict';
    END IF;
    IF command_row.request_digest::TEXT <> envelope->>'request_digest'
       OR command_row.body_fingerprint <> fingerprint
       OR command_row.causation_id <> envelope->>'causation_id'
       OR command_row.correlation_id <> envelope->>'correlation_id'
       OR command_row.deadline_at <> (envelope->>'deadline')::TIMESTAMPTZ THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'cancellation_command_idempotency_conflict';
    END IF;
    IF command_row.state IN ('committed', 'denied') THEN RETURN command_row; END IF;
    IF command_row.deadline_at <= clock_timestamp() THEN
        denied := jsonb_build_object(
            'schema_revision', 1, 'status', 'denied', 'command_id', command_row.command_id,
            'command_type', 'submit_cancellation_request', 'reason_code', 'command_deadline_expired'
        );
        UPDATE agent_control.runtime_cancellation_command
        SET state = 'denied', response = denied,
            response_digest = agent_control.runtime_sha256_json(denied),
            committed_at = greatest(clock_timestamp(), created_at)
        WHERE principal_id = command_row.principal_id AND idempotency_key = command_row.idempotency_key
        RETURNING * INTO command_row;
    END IF;
    RETURN command_row;
END
$$;

CREATE FUNCTION agent_control.runtime_finish_cancellation_command(
    p_command agent_control.runtime_cancellation_command, p_state TEXT, p_response JSONB
) RETURNS JSONB
LANGUAGE plpgsql VOLATILE STRICT
AS $$
DECLARE returned_response JSONB;
BEGIN
    IF p_state NOT IN ('committed', 'denied') OR jsonb_typeof(p_response) <> 'object' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_cancellation_command_completion';
    END IF;
    UPDATE agent_control.runtime_cancellation_command
    SET state = p_state, response = p_response,
        response_digest = agent_control.runtime_sha256_json(p_response),
        committed_at = greatest(clock_timestamp(), created_at)
    WHERE principal_id = p_command.principal_id AND idempotency_key = p_command.idempotency_key
      AND state = 'processing'
    RETURNING response INTO returned_response;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'cancellation_command_not_processing';
    END IF;
    RETURN returned_response;
END
$$;

CREATE FUNCTION agent_control.runtime_deny_cancellation_command(
    p_command agent_control.runtime_cancellation_command, p_reason_code TEXT
) RETURNS JSONB
LANGUAGE plpgsql VOLATILE STRICT
AS $$
BEGIN
    IF NOT agent_control.runtime_name_valid(p_reason_code) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_cancellation_denial_reason';
    END IF;
    RETURN agent_control.runtime_finish_cancellation_command(p_command, 'denied', jsonb_build_object(
        'schema_revision', 1, 'status', 'denied', 'command_id', p_command.command_id,
        'command_type', 'submit_cancellation_request', 'reason_code', p_reason_code
    ));
END
$$;

CREATE FUNCTION agent_control.runtime_submit_cancellation_request(p_command JSONB)
RETURNS JSONB
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    command_row agent_control.runtime_cancellation_command%ROWTYPE;
    target_generation BIGINT;
    request_digest CHAR(64);
    response JSONB;
BEGIN
    IF p_command IS NULL OR NOT agent_control.runtime_cancellation_control_command_valid(p_command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_submit_cancellation_request_command';
    END IF;
    command_row := agent_control.runtime_begin_cancellation_command(p_command);
    IF command_row.state IN ('committed', 'denied') THEN RETURN command_row.response; END IF;

    IF p_command #>> '{request,target}' = 'run' THEN
        SELECT state_generation INTO target_generation
        FROM agent_control.runtime_run
        WHERE run_id = p_command #>> '{request,target_id}' FOR UPDATE;
    ELSE
        SELECT state_generation INTO target_generation
        FROM agent_control.runtime_task
        WHERE task_id = p_command #>> '{request,target_id}' FOR UPDATE;
    END IF;
    IF NOT FOUND THEN
        RETURN agent_control.runtime_deny_cancellation_command(command_row, 'cancellation_target_not_found');
    END IF;
    IF target_generation <> (p_command #>> '{request,expected_state_generation}')::BIGINT THEN
        RETURN agent_control.runtime_deny_cancellation_command(command_row, 'stale_cancellation_target_generation');
    END IF;

    request_digest := agent_control.runtime_contract_digest(
        'agent_control.runtime_cancellation_request.v1', p_command->'request'
    );
    BEGIN
        INSERT INTO agent_control.runtime_cancellation_request (
            request_id, schema_revision, record_digest, target, target_id,
            expected_state_generation, mode, superseded_by_run_id, actor,
            reason_code, requested_at
        ) VALUES (
            p_command #>> '{request,request_id}', 1, request_digest,
            p_command #>> '{request,target}', p_command #>> '{request,target_id}',
            (p_command #>> '{request,expected_state_generation}')::BIGINT,
            p_command #>> '{request,mode}', p_command #>> '{request,superseded_by_run_id}',
            p_command #> '{request,actor}', p_command #>> '{request,reason_code}',
            (p_command #>> '{request,requested_at}')::TIMESTAMPTZ
        );
    EXCEPTION WHEN unique_violation THEN
        IF NOT EXISTS (
            SELECT 1 FROM agent_control.runtime_cancellation_request
            WHERE request_id = p_command #>> '{request,request_id}'
              AND record_digest = request_digest
        ) THEN
            RETURN agent_control.runtime_deny_cancellation_command(command_row, 'cancellation_request_id_conflict');
        END IF;
    END;
    response := jsonb_build_object(
        'schema_revision', 1, 'status', 'committed', 'command_id', command_row.command_id,
        'command_type', 'submit_cancellation_request',
        'cancellation_request_id', p_command #>> '{request,request_id}',
        'request_state', 'pending_reconciliation',
        'reason_code', 'cancellation_pending_reconciliation'
    );
    RETURN agent_control.runtime_finish_cancellation_command(command_row, 'committed', response);
END
$$;

CREATE FUNCTION agent_control.submit_cancellation_request(p_command TEXT)
RETURNS JSONB
LANGUAGE plpgsql SECURITY DEFINER
SET search_path = pg_catalog, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE parsed JSONB := agent_control.runtime_parse_worker_command(p_command);
BEGIN
    IF parsed IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_raw_submit_cancellation_request_command';
    END IF;
    RETURN agent_control.runtime_submit_cancellation_request(parsed);
END
$$;

REVOKE ALL ON TABLE agent_control.runtime_cancellation_command FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_cancellation_control_command_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_begin_cancellation_command(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_finish_cancellation_command(
    agent_control.runtime_cancellation_command, TEXT, JSONB
) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_deny_cancellation_command(
    agent_control.runtime_cancellation_command, TEXT
) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.runtime_submit_cancellation_request(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.submit_cancellation_request(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.submit_cancellation_request(TEXT)
    TO alpheus_agent_control_api;

RESET ROLE;
