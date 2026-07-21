SET TIME ZONE 'UTC';

-- AP2 starts with immutable input facts only.  This migration intentionally
-- exposes no writable command: authenticated Input Gateway admission and
-- idempotency are a later, single command boundary.  Direct table access
-- remains denied to every application profile.
CREATE SCHEMA agent_input AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA agent_input FROM PUBLIC;

SET ROLE alpheus_agent_migrator;

CREATE FUNCTION agent_input.input_identifier_valid(p_value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT p_value IS NOT NULL
       AND p_value <> ''
       AND p_value = btrim(p_value)
       AND octet_length(p_value) <= 200
       AND p_value !~ '[[:space:][:cntrl:]]'
$$;

CREATE FUNCTION agent_input.input_digest_valid(p_value TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT p_value IS NOT NULL AND p_value ~ '^[0-9a-f]{64}$'
$$;

CREATE FUNCTION agent_input.input_record_ref_valid(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
    SELECT p_value IS NOT NULL
       AND jsonb_typeof(p_value) = 'object'
       AND p_value ?& ARRAY['owner', 'record_type', 'record_id', 'schema_revision', 'record_digest']
       AND p_value->>'owner' IN (
           'agent_control', 'worker', 'platform_governance', 'blob',
           'research_gateway', 'grace', 'delegation', 'kernel'
       )
       AND p_value->>'record_type' ~ '^[a-z][a-z0-9_]{0,63}$'
       AND agent_input.input_identifier_valid(p_value->>'record_id')
       AND p_value->>'schema_revision' = '1'
       AND agent_input.input_digest_valid(p_value->>'record_digest')
$$;

CREATE FUNCTION agent_input.input_blob_ref_valid(p_value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    committed_at TIMESTAMPTZ;
BEGIN
    IF p_value IS NULL
       OR jsonb_typeof(p_value) <> 'object'
       OR NOT p_value ?& ARRAY[
           'schema_revision', 'blob_id', 'content_digest', 'media_type',
           'size_bytes', 'origin', 'committed_at'
       ]
       OR p_value->>'schema_revision' <> '1'
       OR p_value->>'blob_id' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
       OR NOT agent_input.input_digest_valid(p_value->>'content_digest')
       OR p_value->>'media_type' = ''
       OR p_value->>'media_type' <> lower(p_value->>'media_type')
       OR p_value->>'size_bytes' !~ '^[1-9][0-9]*$'
       OR (p_value->>'size_bytes')::NUMERIC > 1073741824
       OR NOT agent_input.input_record_ref_valid(p_value->'origin') THEN
        RETURN false;
    END IF;
    committed_at := (p_value->>'committed_at')::TIMESTAMPTZ;
    RETURN committed_at = timezone('UTC', committed_at);
EXCEPTION WHEN others THEN
    RETURN false;
END
$$;

CREATE FUNCTION agent_input.reject_immutable_input_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'input facts are immutable';
END
$$;

CREATE TABLE agent_input.conversation (
    conversation_id TEXT PRIMARY KEY CHECK (agent_input.input_identifier_valid(conversation_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL CHECK (agent_input.input_digest_valid(record_digest)),
    subject_principal_id TEXT NOT NULL CHECK (agent_input.input_identifier_valid(subject_principal_id)),
    subject_kind TEXT NOT NULL CHECK (subject_kind = 'user'),
    subject_audience TEXT NOT NULL CHECK (subject_audience = 'control_api'),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (conversation_id, record_digest)
);

CREATE TABLE agent_input.user_request (
    request_id TEXT PRIMARY KEY CHECK (agent_input.input_identifier_valid(request_id)),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL CHECK (agent_input.input_digest_valid(record_digest)),
    conversation_id TEXT NOT NULL CHECK (agent_input.input_identifier_valid(conversation_id)),
    conversation_digest CHAR(64) NOT NULL CHECK (agent_input.input_digest_valid(conversation_digest)),
    subject_principal_id TEXT NOT NULL CHECK (agent_input.input_identifier_valid(subject_principal_id)),
    subject_kind TEXT NOT NULL CHECK (subject_kind = 'user'),
    subject_audience TEXT NOT NULL CHECK (subject_audience = 'control_api'),
    request_kind TEXT NOT NULL CHECK (request_kind IN (
        'new_request', 'continuation', 'additional_context', 'clarification_answer',
        'correction', 'pause', 'resume', 'cancel', 'approval_intent', 'rejection_intent'
    )),
    raw_input JSONB NOT NULL CHECK (agent_input.input_blob_ref_valid(raw_input)),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (request_id, record_digest),
    FOREIGN KEY (conversation_id, conversation_digest)
        REFERENCES agent_input.conversation(conversation_id, record_digest)
        DEFERRABLE INITIALLY DEFERRED,
    CHECK ((raw_input->>'committed_at')::TIMESTAMPTZ <= created_at)
);

CREATE TABLE agent_input.user_request_attachment (
    request_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 64),
    attachment JSONB NOT NULL CHECK (agent_input.input_blob_ref_valid(attachment)),
    PRIMARY KEY (request_id, ordinal),
    FOREIGN KEY (request_id) REFERENCES agent_input.user_request(request_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE FUNCTION agent_input.validate_user_request_attachment()
RETURNS trigger
LANGUAGE plpgsql
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

CREATE UNIQUE INDEX user_request_attachment_blob_once_idx
ON agent_input.user_request_attachment (request_id, (attachment->>'blob_id'));

CREATE TABLE agent_input.user_request_reference (
    request_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 1 AND 64),
    reference JSONB NOT NULL CHECK (agent_input.input_record_ref_valid(reference)),
    PRIMARY KEY (request_id, ordinal),
    FOREIGN KEY (request_id) REFERENCES agent_input.user_request(request_id)
        DEFERRABLE INITIALLY DEFERRED
);

CREATE UNIQUE INDEX user_request_reference_once_idx
ON agent_input.user_request_reference (
    request_id, (reference->>'owner'), (reference->>'record_type'),
    (reference->>'record_id'), (reference->>'record_digest')
);

CREATE TRIGGER conversation_immutable
BEFORE UPDATE OR DELETE ON agent_input.conversation
FOR EACH ROW EXECUTE FUNCTION agent_input.reject_immutable_input_mutation();

CREATE TRIGGER user_request_immutable
BEFORE UPDATE OR DELETE ON agent_input.user_request
FOR EACH ROW EXECUTE FUNCTION agent_input.reject_immutable_input_mutation();

CREATE TRIGGER user_request_attachment_immutable
BEFORE UPDATE OR DELETE ON agent_input.user_request_attachment
FOR EACH ROW EXECUTE FUNCTION agent_input.reject_immutable_input_mutation();

CREATE CONSTRAINT TRIGGER user_request_attachment_validated
AFTER INSERT ON agent_input.user_request_attachment
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION agent_input.validate_user_request_attachment();

CREATE TRIGGER user_request_reference_immutable
BEFORE UPDATE OR DELETE ON agent_input.user_request_reference
FOR EACH ROW EXECUTE FUNCTION agent_input.reject_immutable_input_mutation();

REVOKE ALL ON ALL TABLES IN SCHEMA agent_input FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA agent_input FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA agent_input FROM PUBLIC;

RESET ROLE;
