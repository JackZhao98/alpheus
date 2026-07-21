SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- AP2-1 has one narrow writer: an authenticated Control API workload may
-- append a user's already-staged raw input.  The workload Actor authenticates
-- the command; the human Subject remains evidence, never delegated authority.
CREATE TABLE agent_input.submit_user_request_command (
    subject_principal_id TEXT NOT NULL CHECK (agent_input.input_identifier_valid(subject_principal_id)),
    idempotency_key TEXT NOT NULL CHECK (agent_input.input_identifier_valid(idempotency_key)),
    command_id TEXT NOT NULL UNIQUE CHECK (agent_input.input_identifier_valid(command_id)),
    request_id TEXT NOT NULL UNIQUE CHECK (agent_input.input_identifier_valid(request_id)),
    request_digest CHAR(64) NOT NULL CHECK (agent_input.input_digest_valid(request_digest)),
    body_fingerprint CHAR(64) NOT NULL CHECK (agent_input.input_digest_valid(body_fingerprint)),
    causation_id TEXT NOT NULL CHECK (agent_input.input_identifier_valid(causation_id)),
    correlation_id TEXT NOT NULL CHECK (agent_input.input_identifier_valid(correlation_id)),
    deadline_at TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('processing', 'committed', 'denied')),
    response JSONB,
    response_digest CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    committed_at TIMESTAMPTZ,
    PRIMARY KEY (subject_principal_id, idempotency_key),
    CHECK (response IS NULL OR (jsonb_typeof(response) = 'object' AND octet_length(response::TEXT) <= 1048576)),
    CHECK (response_digest IS NULL OR agent_input.input_digest_valid(response_digest)),
    CHECK (
        (state = 'processing' AND response IS NULL AND response_digest IS NULL AND committed_at IS NULL)
        OR (state IN ('committed', 'denied') AND response IS NOT NULL
            AND response_digest IS NOT NULL AND committed_at IS NOT NULL AND committed_at >= created_at)
    )
);

CREATE TRIGGER submit_user_request_command_guard
BEFORE UPDATE OR DELETE ON agent_input.submit_user_request_command
FOR EACH ROW EXECUTE FUNCTION agent_control.guard_runtime_mutable_columns(
    'state', 'response', 'response_digest', 'committed_at'
);

-- The immutable-input trigger reads its parent request.  It must run with the
-- storage owner's narrow table privilege, not with the Control API's role;
-- callers still have no direct table or trigger-function privilege.
CREATE OR REPLACE FUNCTION agent_input.validate_user_request_attachment()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_input
AS $$
DECLARE
    raw_blob_id TEXT;
    request_created_at TIMESTAMPTZ;
BEGIN
    SELECT request.raw_input->>'blob_id', request.created_at
    INTO STRICT raw_blob_id, request_created_at
    FROM agent_input.user_request AS request
    WHERE request.request_id = NEW.request_id;
    IF NEW.attachment->>'blob_id' = raw_blob_id THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'raw input cannot also be an attachment';
    END IF;
    IF (NEW.attachment->>'committed_at')::TIMESTAMPTZ > request_created_at THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'attachment committed after request creation';
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION agent_input.validate_user_request_attachment() FROM PUBLIC;

CREATE FUNCTION agent_input.submit_user_request_valid(p_command JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
STABLE
STRICT
AS $$
DECLARE
    envelope JSONB;
    conversation JSONB;
    request JSONB;
    invoker RECORD;
BEGIN
    IF jsonb_typeof(p_command) <> 'object'
       OR NOT (p_command ?& ARRAY['schema_revision', 'envelope', 'conversation', 'request'])
       OR p_command - ARRAY['schema_revision', 'envelope', 'conversation', 'request'] <> '{}'::JSONB
       OR jsonb_typeof(p_command->'schema_revision') <> 'number'
       OR p_command->>'schema_revision' <> '1'
       OR jsonb_typeof(p_command->'envelope') <> 'object'
       OR jsonb_typeof(p_command->'conversation') <> 'object'
       OR jsonb_typeof(p_command->'request') <> 'object' THEN
        RETURN false;
    END IF;

    envelope := p_command->'envelope';
    conversation := p_command->'conversation';
    request := p_command->'request';
    IF NOT (envelope ?& ARRAY[
            'schema_revision', 'command_id', 'actor', 'audience', 'command_type',
            'idempotency_key', 'request_digest', 'causation_id', 'correlation_id', 'deadline'
        ])
       OR envelope - ARRAY[
            'schema_revision', 'command_id', 'actor', 'audience', 'command_type',
            'idempotency_key', 'request_digest', 'causation_id', 'correlation_id', 'deadline'
        ] <> '{}'::JSONB
       OR jsonb_typeof(envelope->'schema_revision') <> 'number'
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
       OR envelope->>'command_type' <> 'submit_user_request'
       OR NOT agent_control.runtime_identifier_valid(envelope->>'idempotency_key')
       OR NOT agent_control.runtime_digest_valid(envelope->>'request_digest')
       OR NOT agent_control.runtime_identifier_valid(envelope->>'causation_id')
       OR NOT agent_control.runtime_identifier_valid(envelope->>'correlation_id')
       OR NOT agent_control.runtime_utc_instant_json(envelope->'deadline') THEN
        RETURN false;
    END IF;

    IF NOT (conversation ?& ARRAY['schema_revision', 'conversation_id', 'subject', 'created_at'])
       OR conversation - ARRAY['schema_revision', 'conversation_id', 'subject', 'created_at'] <> '{}'::JSONB
       OR jsonb_typeof(conversation->'schema_revision') <> 'number'
       OR conversation->>'schema_revision' <> '1'
       OR jsonb_typeof(conversation->'conversation_id') <> 'string'
       OR NOT agent_input.input_identifier_valid(conversation->>'conversation_id')
       OR NOT agent_control.runtime_actor_valid(conversation->'subject')
       OR conversation #>> '{subject,kind}' <> 'user'
       OR conversation #>> '{subject,audience}' <> 'control_api'
       OR NOT agent_control.runtime_utc_instant_json(conversation->'created_at') THEN
        RETURN false;
    END IF;

    IF NOT (request ?& ARRAY[
            'schema_revision', 'request_id', 'conversation', 'subject', 'kind',
            'raw_input', 'created_at'
        ])
       OR request - ARRAY[
            'schema_revision', 'request_id', 'conversation', 'subject', 'kind',
            'raw_input', 'attachments', 'referenced_objects', 'created_at'
        ] <> '{}'::JSONB
       OR jsonb_typeof(request->'schema_revision') <> 'number'
       OR request->>'schema_revision' <> '1'
       OR jsonb_typeof(request->'request_id') <> 'string'
       OR NOT agent_input.input_identifier_valid(request->>'request_id')
       OR NOT agent_control.runtime_record_ref_valid(request->'conversation', 'agent_control', 'conversation')
       OR NOT agent_control.runtime_actor_valid(request->'subject')
       OR request->'subject' <> conversation->'subject'
       OR request #>> '{subject,kind}' <> 'user'
       OR request #>> '{subject,audience}' <> 'control_api'
       OR jsonb_typeof(request->'kind') <> 'string'
       OR request->>'kind' NOT IN (
            'new_request', 'continuation', 'additional_context', 'clarification_answer',
            'correction', 'pause', 'resume', 'cancel', 'approval_intent', 'rejection_intent'
        )
       OR NOT agent_control.runtime_blob_ref_valid(request->'raw_input', '', '')
       OR NOT agent_control.runtime_utc_instant_json(request->'created_at')
       OR (request ? 'attachments' AND (
            jsonb_typeof(request->'attachments') <> 'array'
            OR jsonb_array_length(request->'attachments') > 64
       ))
       OR (request ? 'referenced_objects' AND (
            jsonb_typeof(request->'referenced_objects') <> 'array'
            OR jsonb_array_length(request->'referenced_objects') > 64
       )) THEN
        RETURN false;
    END IF;

    IF EXISTS (
        SELECT 1 FROM jsonb_array_elements(COALESCE(request->'attachments', '[]'::JSONB)) AS item(value)
        WHERE NOT agent_control.runtime_blob_ref_valid(item.value, '', '')
           OR NOT agent_control.runtime_utc_instant_json(item.value->'committed_at')
           OR (item.value->>'committed_at')::TIMESTAMPTZ > (request->>'created_at')::TIMESTAMPTZ
    ) OR EXISTS (
        SELECT 1 FROM jsonb_array_elements(COALESCE(request->'referenced_objects', '[]'::JSONB)) AS item(value)
        WHERE NOT agent_control.runtime_record_ref_valid(item.value, '', '')
    )
       OR (request #>> '{raw_input,committed_at}')::TIMESTAMPTZ > (request->>'created_at')::TIMESTAMPTZ
       OR (conversation->>'created_at')::TIMESTAMPTZ > (request->>'created_at')::TIMESTAMPTZ
       OR (request->>'created_at')::TIMESTAMPTZ > (envelope->>'deadline')::TIMESTAMPTZ THEN
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

CREATE FUNCTION agent_input.begin_submit_user_request(p_command JSONB)
RETURNS agent_input.submit_user_request_command
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE
    command_row agent_input.submit_user_request_command%ROWTYPE;
    envelope JSONB := p_command->'envelope';
    fingerprint CHAR(64) := agent_control.runtime_sha256_json(
        jsonb_set(p_command, '{envelope}', (p_command->'envelope') - 'command_id', false)
    );
    denied JSONB;
BEGIN
    INSERT INTO agent_input.submit_user_request_command (
        subject_principal_id, idempotency_key, command_id, request_id, request_digest,
        body_fingerprint, causation_id, correlation_id, deadline_at, state
    ) VALUES (
        p_command #>> '{request,subject,principal_id}', envelope->>'idempotency_key',
        envelope->>'command_id', p_command #>> '{request,request_id}', envelope->>'request_digest',
        fingerprint, envelope->>'causation_id', envelope->>'correlation_id',
        (envelope->>'deadline')::TIMESTAMPTZ, 'processing'
    ) ON CONFLICT DO NOTHING;

    SELECT * INTO command_row FROM agent_input.submit_user_request_command
    WHERE subject_principal_id = p_command #>> '{request,subject,principal_id}'
      AND idempotency_key = envelope->>'idempotency_key'
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'submit_user_request_command_identity_conflict';
    END IF;
    IF command_row.request_digest::TEXT <> envelope->>'request_digest'
       OR command_row.body_fingerprint <> fingerprint
       OR command_row.causation_id <> envelope->>'causation_id'
       OR command_row.correlation_id <> envelope->>'correlation_id'
       OR command_row.deadline_at <> (envelope->>'deadline')::TIMESTAMPTZ THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'submit_user_request_idempotency_conflict';
    END IF;
    IF command_row.state IN ('committed', 'denied') THEN RETURN command_row; END IF;
    IF command_row.deadline_at <= clock_timestamp() THEN
        denied := jsonb_build_object(
            'schema_revision', 1, 'status', 'denied', 'command_id', command_row.command_id,
            'command_type', 'submit_user_request', 'reason_code', 'command_deadline_expired'
        );
        UPDATE agent_input.submit_user_request_command
        SET state = 'denied', response = denied,
            response_digest = agent_control.runtime_sha256_json(denied),
            committed_at = greatest(clock_timestamp(), created_at)
        WHERE subject_principal_id = command_row.subject_principal_id
          AND idempotency_key = command_row.idempotency_key
        RETURNING * INTO command_row;
    END IF;
    RETURN command_row;
END
$$;

CREATE FUNCTION agent_input.finish_submit_user_request(
    p_command agent_input.submit_user_request_command, p_state TEXT, p_response JSONB
) RETURNS JSONB
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
DECLARE returned_response JSONB;
BEGIN
    IF p_state NOT IN ('committed', 'denied') OR jsonb_typeof(p_response) <> 'object' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_submit_user_request_completion';
    END IF;
    UPDATE agent_input.submit_user_request_command
    SET state = p_state, response = p_response,
        response_digest = agent_control.runtime_sha256_json(p_response),
        committed_at = greatest(clock_timestamp(), created_at)
    WHERE subject_principal_id = p_command.subject_principal_id
      AND idempotency_key = p_command.idempotency_key AND state = 'processing'
    RETURNING response INTO returned_response;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'submit_user_request_command_not_processing';
    END IF;
    RETURN returned_response;
END
$$;

CREATE FUNCTION agent_input.deny_submit_user_request(
    p_command agent_input.submit_user_request_command, p_reason_code TEXT
) RETURNS JSONB
LANGUAGE plpgsql
VOLATILE
STRICT
AS $$
BEGIN
    IF NOT agent_control.runtime_name_valid(p_reason_code) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_submit_user_request_denial_reason';
    END IF;
    RETURN agent_input.finish_submit_user_request(p_command, 'denied', jsonb_build_object(
        'schema_revision', 1, 'status', 'denied', 'command_id', p_command.command_id,
        'command_type', 'submit_user_request', 'reason_code', p_reason_code
    ));
END
$$;

CREATE FUNCTION agent_input.submit_user_request(p_raw TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, agent_input, agent_control, platform_security
SET timezone = 'UTC'
AS $$
DECLARE
    command JSONB := agent_control.runtime_parse_worker_command(p_raw);
    command_row agent_input.submit_user_request_command%ROWTYPE;
    conversation_digest CHAR(64);
    request_digest CHAR(64);
    response JSONB;
    item JSONB;
    ordinal INTEGER := 0;
BEGIN
    IF command IS NULL OR NOT agent_input.submit_user_request_valid(command) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid_submit_user_request_command';
    END IF;
    command_row := agent_input.begin_submit_user_request(command);
    IF command_row.state IN ('committed', 'denied') THEN RETURN command_row.response; END IF;

    conversation_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.conversation.v1', command->'conversation'
    );
    IF command #> '{request,conversation}' <> jsonb_build_object(
        'owner', 'agent_control', 'record_type', 'conversation',
        'record_id', command #>> '{conversation,conversation_id}', 'schema_revision', 1,
        'record_digest', conversation_digest
    ) THEN
        RETURN agent_input.deny_submit_user_request(command_row, 'conversation_reference_mismatch');
    END IF;
    request_digest := agent_control.runtime_contract_digest(
        'agent-platform.contract.user_request.v1', command->'request'
    );
    IF request_digest <> command #>> '{envelope,request_digest}' THEN
        RETURN agent_input.deny_submit_user_request(command_row, 'request_digest_mismatch');
    END IF;

    INSERT INTO agent_input.conversation (
        conversation_id, schema_revision, record_digest, subject_principal_id,
        subject_kind, subject_audience, created_at
    ) VALUES (
        command #>> '{conversation,conversation_id}', 1, conversation_digest,
        command #>> '{conversation,subject,principal_id}', 'user', 'control_api',
        (command #>> '{conversation,created_at}')::TIMESTAMPTZ
    ) ON CONFLICT (conversation_id) DO NOTHING;
    IF NOT EXISTS (
        SELECT 1 FROM agent_input.conversation WHERE conversation_id = command #>> '{conversation,conversation_id}'
          AND record_digest = conversation_digest
          AND subject_principal_id = command #>> '{conversation,subject,principal_id}'
          AND created_at = (command #>> '{conversation,created_at}')::TIMESTAMPTZ
    ) THEN
        RETURN agent_input.deny_submit_user_request(command_row, 'conversation_identity_conflict');
    END IF;

    BEGIN
        INSERT INTO agent_input.user_request (
            request_id, schema_revision, record_digest, conversation_id, conversation_digest,
            subject_principal_id, subject_kind, subject_audience, request_kind, raw_input, created_at
        ) VALUES (
            command #>> '{request,request_id}', 1, request_digest,
            command #>> '{conversation,conversation_id}', conversation_digest,
            command #>> '{request,subject,principal_id}', 'user', 'control_api',
            command #>> '{request,kind}', command #> '{request,raw_input}',
            (command #>> '{request,created_at}')::TIMESTAMPTZ
        );
    EXCEPTION WHEN unique_violation THEN
        RETURN agent_input.deny_submit_user_request(command_row, 'user_request_identity_conflict');
    END;

    FOR item IN SELECT value FROM jsonb_array_elements(COALESCE(command #> '{request,attachments}', '[]'::JSONB)) LOOP
        ordinal := ordinal + 1;
        INSERT INTO agent_input.user_request_attachment (request_id, ordinal, attachment)
        VALUES (command #>> '{request,request_id}', ordinal, item);
    END LOOP;
    ordinal := 0;
    FOR item IN SELECT value FROM jsonb_array_elements(COALESCE(command #> '{request,referenced_objects}', '[]'::JSONB)) LOOP
        ordinal := ordinal + 1;
        INSERT INTO agent_input.user_request_reference (request_id, ordinal, reference)
        VALUES (command #>> '{request,request_id}', ordinal, item);
    END LOOP;

    response := jsonb_build_object(
        'schema_revision', 1, 'status', 'committed', 'command_id', command_row.command_id,
        'command_type', 'submit_user_request',
        'conversation_id', command #>> '{conversation,conversation_id}',
        'request_id', command #>> '{request,request_id}',
        'request_digest', request_digest, 'reason_code', 'user_request_recorded'
    );
    RETURN agent_input.finish_submit_user_request(command_row, 'committed', response);
END
$$;

REVOKE ALL ON ALL TABLES IN SCHEMA agent_input FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_input.submit_user_request_valid(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_input.begin_submit_user_request(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_input.finish_submit_user_request(agent_input.submit_user_request_command, TEXT, JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_input.deny_submit_user_request(agent_input.submit_user_request_command, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_input.submit_user_request(TEXT) FROM PUBLIC;
GRANT USAGE ON SCHEMA agent_input TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_input.submit_user_request(TEXT) TO alpheus_agent_control_api;

RESET ROLE;
