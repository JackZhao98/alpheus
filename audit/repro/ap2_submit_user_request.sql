\set ON_ERROR_STOP on

-- A real Control API LOGIN may invoke the one admission function, receives
-- an exact idempotent replay, and cannot change the recorded request under
-- the same idempotency identity.  The test-only command factory is a definer
-- wrapper solely because digest helpers are intentionally not application API.
SET ROLE alpheus_agent_migrator;

CREATE FUNCTION agent_input.ap2_test_submit_command(p_conflict BOOLEAN DEFAULT false)
RETURNS TEXT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_input, agent_control
AS $$
DECLARE
    conversation JSONB;
    request JSONB;
    raw_input JSONB;
    attachment JSONB;
    reference JSONB;
    deadline TEXT := agent_control.runtime_utc_text(clock_timestamp() + interval '5 minutes');
    conversation_digest CHAR(64);
    request_digest CHAR(64);
BEGIN
    conversation := jsonb_build_object(
        'schema_revision', 1, 'conversation_id', 'conversation-command-1',
        'subject', jsonb_build_object('principal_id', 'owner-1', 'kind', 'user', 'audience', 'control_api'),
        'created_at', '2026-07-21T16:00:00Z'
    );
    conversation_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.conversation.v1', conversation
    );
    raw_input := jsonb_build_object(
        'schema_revision', 1,
        'blob_id', CASE WHEN p_conflict THEN '22222222-2222-4222-8222-222222222222' ELSE '11111111-1111-4111-8111-111111111111' END,
        'content_digest', repeat(CASE WHEN p_conflict THEN 'e' ELSE 'c' END, 64),
        'media_type', 'text/plain; charset=utf-8', 'size_bytes', 5,
        'origin', jsonb_build_object('owner', 'agent_control', 'record_type', 'input_raw',
            'record_id', 'raw-command-1', 'schema_revision', 1, 'record_digest', repeat('d', 64)),
        'committed_at', '2026-07-21T16:00:00Z'
    );
    attachment := jsonb_build_object(
        'schema_revision', 1, 'blob_id', '33333333-3333-4333-8333-333333333333',
        'content_digest', repeat('f', 64), 'media_type', 'application/json', 'size_bytes', 2,
        'origin', jsonb_build_object('owner', 'agent_control', 'record_type', 'input_attachment',
            'record_id', 'attachment-command-1', 'schema_revision', 1, 'record_digest', repeat('a', 64)),
        'committed_at', '2026-07-21T16:00:00Z'
    );
    reference := jsonb_build_object('owner', 'research_gateway', 'record_type', 'market_snapshot',
        'record_id', 'snapshot-command-1', 'schema_revision', 1, 'record_digest', repeat('b', 64));
    request := jsonb_build_object(
        'schema_revision', 1, 'request_id', 'request-command-1',
        'conversation', jsonb_build_object('owner', 'agent_control', 'record_type', 'conversation',
            'record_id', 'conversation-command-1', 'schema_revision', 1, 'record_digest', conversation_digest),
        'subject', jsonb_build_object('principal_id', 'owner-1', 'kind', 'user', 'audience', 'control_api'),
        'kind', 'new_request', 'raw_input', raw_input,
        'attachments', jsonb_build_array(attachment), 'referenced_objects', jsonb_build_array(reference),
        'created_at', '2026-07-21T16:00:00Z'
    );
    request_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.user_request.v1', request
    );
    RETURN jsonb_build_object(
        'schema_revision', 1,
        'envelope', jsonb_build_object(
            'schema_revision', 1,
            'command_id', CASE WHEN p_conflict THEN 'submit-command-2' ELSE 'submit-command-1' END,
            'actor', jsonb_build_object('principal_id', 'control-1', 'kind', 'workload', 'audience', 'control_api'),
            'audience', 'control_api', 'command_type', 'submit_user_request',
            'idempotency_key', 'submit-idempotency-1', 'request_digest', request_digest,
            'causation_id', 'submit-causation-1', 'correlation_id', 'submit-correlation-1', 'deadline', deadline
        ),
        'conversation', conversation, 'request', request
    )::TEXT;
END
$$;

REVOKE ALL ON FUNCTION agent_input.ap2_test_submit_command(BOOLEAN) FROM PUBLIC;
GRANT USAGE ON SCHEMA agent_input TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_input.ap2_test_submit_command(BOOLEAN) TO alpheus_agent_control_api;
RESET ROLE;

SET SESSION AUTHORIZATION "control-1";
SET ROLE alpheus_agent_control_api;
DO $$
DECLARE command_text TEXT; first_response JSONB; replay_response JSONB;
BEGIN
    command_text := agent_input.ap2_test_submit_command(false);
    first_response := agent_input.submit_user_request(command_text);
    replay_response := agent_input.submit_user_request(command_text);
    IF first_response <> replay_response OR first_response->>'status' <> 'committed'
       OR first_response->>'reason_code' <> 'user_request_recorded' THEN
        RAISE EXCEPTION 'submit user request did not commit an exact replay';
    END IF;
    BEGIN
        PERFORM agent_input.submit_user_request(agent_input.ap2_test_submit_command(true));
        RAISE EXCEPTION 'idempotency conflict unexpectedly accepted';
    EXCEPTION WHEN unique_violation THEN
        NULL;
    END;
END
$$;
RESET ROLE;
RESET SESSION AUTHORIZATION;

DO $$
BEGIN
    IF (SELECT count(*) FROM agent_input.conversation WHERE conversation_id = 'conversation-command-1') <> 1
       OR (SELECT count(*) FROM agent_input.user_request WHERE request_id = 'request-command-1') <> 1
       OR (SELECT count(*) FROM agent_input.user_request_attachment WHERE request_id = 'request-command-1') <> 1
       OR (SELECT count(*) FROM agent_input.user_request_reference WHERE request_id = 'request-command-1') <> 1
       OR (SELECT count(*) FROM agent_input.submit_user_request_command WHERE idempotency_key = 'submit-idempotency-1') <> 1 THEN
        RAISE EXCEPTION 'submit user request did not persist exactly one immutable fact set';
    END IF;
END
$$;

SET ROLE alpheus_agent_migrator;
DROP FUNCTION agent_input.ap2_test_submit_command(BOOLEAN);
RESET ROLE;
