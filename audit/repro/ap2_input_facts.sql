\set ON_ERROR_STOP on

-- The AP2-1 input store accepts a valid immutable raw request, rejects a raw
-- Blob reused as its own attachment at commit, and provides no direct table
-- privilege to the ordinary Control API LOGIN.
BEGIN;
SET ROLE alpheus_agent_migrator;

INSERT INTO agent_input.conversation (
    conversation_id, schema_revision, record_digest, subject_principal_id,
    subject_kind, subject_audience, created_at
) VALUES (
    'conversation-1', 1, repeat('a', 64), 'owner-1', 'user', 'control_api',
    '2026-07-21T16:00:00Z'
);

INSERT INTO agent_input.user_request (
    request_id, schema_revision, record_digest, conversation_id,
    conversation_digest, subject_principal_id, subject_kind, subject_audience,
    request_kind, raw_input, created_at
) VALUES (
    'request-1', 1, repeat('b', 64), 'conversation-1', repeat('a', 64),
    'owner-1', 'user', 'control_api', 'new_request',
    jsonb_build_object(
        'schema_revision', 1,
        'blob_id', '11111111-1111-4111-8111-111111111111',
        'content_digest', repeat('c', 64),
        'media_type', 'text/plain; charset=utf-8',
        'size_bytes', '5',
        'origin', jsonb_build_object(
            'owner', 'agent_control', 'record_type', 'input_raw',
            'record_id', 'raw-1', 'schema_revision', 1,
            'record_digest', repeat('d', 64)
        ),
        'committed_at', '2026-07-21T16:00:00Z'
    ),
    '2026-07-21T16:00:00Z'
);

SAVEPOINT duplicate_raw;
INSERT INTO agent_input.user_request_attachment (request_id, ordinal, attachment)
SELECT request_id, 1, raw_input FROM agent_input.user_request WHERE request_id = 'request-1';
DO $$
BEGIN
    SET CONSTRAINTS agent_input.user_request_attachment_validated IMMEDIATE;
    RAISE EXCEPTION 'duplicate raw attachment unexpectedly committed';
EXCEPTION WHEN check_violation THEN
    NULL;
END
$$;
ROLLBACK TO SAVEPOINT duplicate_raw;

RESET ROLE;
ROLLBACK;

SET SESSION AUTHORIZATION 'control-1';
DO $$
BEGIN
    BEGIN
        INSERT INTO agent_input.conversation (
            conversation_id, schema_revision, record_digest, subject_principal_id,
            subject_kind, subject_audience, created_at
        ) VALUES ('forbidden-1', 1, repeat('e', 64), 'owner-1', 'user', 'control_api', clock_timestamp());
    EXCEPTION WHEN insufficient_privilege THEN
        RETURN;
    END;
    RAISE EXCEPTION 'control API retained direct agent_input table write';
END
$$;
RESET SESSION AUTHORIZATION;
