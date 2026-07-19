SET ROLE alpheus_agent_migrator;

CREATE TABLE blob.storage_policy (
    singleton BOOLEAN PRIMARY KEY DEFAULT true CHECK (singleton),
    max_blob_bytes BIGINT NOT NULL CHECK (max_blob_bytes BETWEEN 1 AND 1073741824),
    stage_ttl_seconds INTEGER NOT NULL CHECK (stage_ttl_seconds BETWEEN 1 AND 86400),
    orphan_grace_seconds INTEGER NOT NULL CHECK (orphan_grace_seconds BETWEEN 1 AND 604800),
    max_retention_seconds BIGINT NOT NULL CHECK (max_retention_seconds BETWEEN 60 AND 315576000),
    max_gc_batch INTEGER NOT NULL CHECK (max_gc_batch BETWEEN 1 AND 10000),
    max_gc_lease_seconds INTEGER NOT NULL CHECK (max_gc_lease_seconds BETWEEN 1 AND 86400),
    allowed_media_types TEXT[] NOT NULL CHECK (cardinality(allowed_media_types) BETWEEN 1 AND 256),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_by TEXT NOT NULL CHECK (updated_by <> '' AND updated_by !~ '[[:space:][:cntrl:]]')
);

INSERT INTO blob.storage_policy (
    singleton, max_blob_bytes, stage_ttl_seconds, orphan_grace_seconds,
    max_retention_seconds, max_gc_batch, max_gc_lease_seconds,
    allowed_media_types, updated_by
) VALUES (
    true, 20971520, 3600, 86400, 315576000, 100, 300,
    ARRAY[
        'application/json', 'application/pdf', 'image/jpeg', 'image/png',
        'text/markdown; charset=utf-8', 'text/plain; charset=utf-8'
    ],
    'bootstrap'
);

CREATE TABLE blob.storage_policy_event (
    event_id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    previous_policy JSONB,
    new_policy JSONB NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    changed_by TEXT NOT NULL CHECK (changed_by <> '' AND changed_by !~ '[[:space:][:cntrl:]]')
);

INSERT INTO blob.storage_policy_event (previous_policy, new_policy, changed_by)
SELECT NULL, to_jsonb(policy), 'bootstrap'
FROM blob.storage_policy AS policy
WHERE singleton;

CREATE TABLE blob.blob_stage (
    stage_id UUID PRIMARY KEY,
    principal_id TEXT NOT NULL CHECK (principal_id <> '' AND principal_id !~ '[[:space:][:cntrl:]]'),
    issuer_owner TEXT NOT NULL CHECK (issuer_owner IN ('agent_control', 'research_gateway')),
    media_type TEXT NOT NULL CHECK (media_type = lower(media_type) AND length(media_type) <= 200),
    max_bytes_snapshot BIGINT NOT NULL CHECK (max_bytes_snapshot BETWEEN 1 AND 1073741824),
    expected_digest CHAR(64) CHECK (expected_digest ~ '^[0-9a-f]{64}$'),
    expected_size_bytes BIGINT CHECK (expected_size_bytes BETWEEN 1 AND 1073741824),
    state TEXT NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'materialized', 'committed', 'cleanup_leased', 'aborted', 'cleaned')),
    content_digest CHAR(64) CHECK (content_digest ~ '^[0-9a-f]{64}$'),
    size_bytes BIGINT CHECK (size_bytes BETWEEN 1 AND 1073741824),
    blob_id UUID UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    committed_at TIMESTAMPTZ,
    cleanup_previous_state TEXT CHECK (cleanup_previous_state IN ('open', 'materialized', 'committed')),
    cleanup_token UUID,
    cleanup_expires_at TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK (expected_size_bytes IS NULL OR expected_size_bytes <= max_bytes_snapshot),
    CHECK (
        (state = 'open' AND content_digest IS NULL AND size_bytes IS NULL AND blob_id IS NULL AND committed_at IS NULL
            AND cleanup_previous_state IS NULL AND cleanup_token IS NULL AND cleanup_expires_at IS NULL)
        OR
        (state = 'materialized' AND content_digest IS NOT NULL AND size_bytes IS NOT NULL AND blob_id IS NULL
            AND committed_at IS NULL AND cleanup_previous_state IS NULL AND cleanup_token IS NULL AND cleanup_expires_at IS NULL)
        OR
        (state = 'committed' AND content_digest IS NOT NULL AND size_bytes IS NOT NULL AND blob_id IS NOT NULL
            AND committed_at IS NOT NULL AND cleanup_previous_state IS NULL AND cleanup_token IS NULL AND cleanup_expires_at IS NULL)
        OR
        (state = 'cleanup_leased' AND cleanup_previous_state IS NOT NULL AND cleanup_token IS NOT NULL
            AND cleanup_expires_at IS NOT NULL)
        OR
        (state IN ('aborted', 'cleaned') AND cleanup_previous_state IS NULL AND cleanup_token IS NULL AND cleanup_expires_at IS NULL)
    )
);

CREATE INDEX blob_stage_gc_idx ON blob.blob_stage (state, expires_at, cleanup_expires_at);

CREATE TABLE blob.blob_content (
    content_digest CHAR(64) PRIMARY KEY CHECK (content_digest ~ '^[0-9a-f]{64}$'),
    size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 1073741824),
    state TEXT NOT NULL CHECK (state IN ('staged', 'committed', 'quarantined', 'deleting', 'deleted')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    gc_token UUID,
    gc_expires_at TIMESTAMPTZ,
    quarantine_reason TEXT,
    CHECK (
        (state = 'deleting' AND gc_token IS NOT NULL AND gc_expires_at IS NOT NULL AND quarantine_reason IS NULL)
        OR
        (state = 'quarantined' AND gc_token IS NULL AND gc_expires_at IS NULL AND quarantine_reason IS NOT NULL)
        OR
        (state IN ('staged', 'committed', 'deleted') AND gc_token IS NULL AND gc_expires_at IS NULL AND quarantine_reason IS NULL)
    )
);

CREATE INDEX blob_content_gc_idx ON blob.blob_content (state, updated_at, gc_expires_at);

CREATE TABLE blob.blob_object (
    blob_id UUID PRIMARY KEY,
    stage_id UUID NOT NULL UNIQUE REFERENCES blob.blob_stage(stage_id),
    content_digest CHAR(64) NOT NULL REFERENCES blob.blob_content(content_digest),
    media_type TEXT NOT NULL CHECK (media_type = lower(media_type) AND length(media_type) <= 200),
    size_bytes BIGINT NOT NULL CHECK (size_bytes BETWEEN 1 AND 1073741824),
    origin_owner TEXT NOT NULL CHECK (origin_owner IN (
        'agent_control', 'blob', 'delegation', 'grace', 'kernel',
        'platform_governance', 'research_gateway', 'worker'
    )),
    origin_record_type TEXT NOT NULL CHECK (origin_record_type ~ '^[a-z][a-z0-9_]{0,63}$'),
    origin_record_id TEXT NOT NULL CHECK (origin_record_id <> '' AND origin_record_id !~ '[[:space:][:cntrl:]]'),
    origin_record_digest CHAR(64) NOT NULL CHECK (origin_record_digest ~ '^[0-9a-f]{64}$'),
    state TEXT NOT NULL CHECK (state IN ('committed', 'quarantined', 'deleting', 'deleted')),
    committed_at TIMESTAMPTZ NOT NULL,
    quarantined_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    CHECK (
        (state = 'committed' AND quarantined_at IS NULL AND deleted_at IS NULL)
        OR (state = 'quarantined' AND quarantined_at IS NOT NULL AND deleted_at IS NULL)
        OR (state = 'deleting' AND deleted_at IS NULL)
        OR (state = 'deleted' AND deleted_at IS NOT NULL)
    )
);

CREATE INDEX blob_object_digest_idx ON blob.blob_object (content_digest, state);

CREATE TABLE blob.blob_reference (
    binding_id TEXT PRIMARY KEY CHECK (binding_id <> '' AND binding_id !~ '[[:space:][:cntrl:]]'),
    blob_id UUID NOT NULL REFERENCES blob.blob_object(blob_id),
    reference_owner TEXT NOT NULL CHECK (reference_owner IN ('agent_control', 'research_gateway')),
    reference_record_type TEXT NOT NULL CHECK (reference_record_type ~ '^[a-z][a-z0-9_]{0,63}$'),
    reference_record_id TEXT NOT NULL CHECK (reference_record_id <> '' AND reference_record_id !~ '[[:space:][:cntrl:]]'),
    reference_record_digest CHAR(64) NOT NULL CHECK (reference_record_digest ~ '^[0-9a-f]{64}$'),
    owner_principal TEXT NOT NULL CHECK (owner_principal <> '' AND owner_principal !~ '[[:space:][:cntrl:]]'),
    access_class TEXT NOT NULL CHECK (access_class IN ('private', 'explicit')),
    retention_until TIMESTAMPTZ NOT NULL,
    state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active', 'released')),
    generation BIGINT NOT NULL DEFAULT 1 CHECK (generation > 0),
    bound_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    released_at TIMESTAMPTZ,
    CHECK (retention_until > bound_at),
    CHECK ((state = 'active' AND released_at IS NULL) OR (state = 'released' AND released_at IS NOT NULL))
);

CREATE INDEX blob_reference_gc_idx ON blob.blob_reference (blob_id, state, retention_until);

CREATE TABLE blob.blob_acl (
    binding_id TEXT NOT NULL REFERENCES blob.blob_reference(binding_id),
    principal_id TEXT NOT NULL CHECK (principal_id <> '' AND principal_id !~ '[[:space:][:cntrl:]]'),
    state TEXT NOT NULL CHECK (state IN ('active', 'revoked')),
    generation BIGINT NOT NULL CHECK (generation > 0),
    granted_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    PRIMARY KEY (binding_id, principal_id),
    CHECK ((state = 'active' AND revoked_at IS NULL) OR (state = 'revoked' AND revoked_at IS NOT NULL))
);

CREATE TABLE blob.lifecycle_event (
    sequence BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id UUID NOT NULL UNIQUE DEFAULT gen_random_uuid(),
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('stage', 'blob', 'binding', 'acl')),
    subject_id TEXT NOT NULL CHECK (subject_id <> '' AND subject_id !~ '[[:space:][:cntrl:]]'),
    transition TEXT NOT NULL CHECK (transition IN (
        'staged', 'committed', 'quarantined', 'gc_claimed', 'deleted',
        'reference_bound', 'reference_released', 'acl_granted', 'acl_revoked'
    )),
    generation BIGINT NOT NULL CHECK (generation > 0),
    actor TEXT NOT NULL CHECK (actor <> '' AND actor !~ '[[:space:][:cntrl:]]'),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    reason_code TEXT NOT NULL CHECK (reason_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    details JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(details) = 'object' AND octet_length(details::text) <= 16384
    )
);

CREATE VIEW blob.blob_health AS
SELECT
    (SELECT count(*) FROM blob.blob_stage WHERE state = 'open') AS open_stages,
    (SELECT count(*) FROM blob.blob_content WHERE state = 'staged') AS staged_content,
    (SELECT count(*) FROM blob.blob_content WHERE state = 'committed') AS committed_content,
    (SELECT count(*) FROM blob.blob_content WHERE state = 'quarantined') AS quarantined_content,
    (SELECT count(*) FROM blob.blob_reference WHERE state = 'active' AND retention_until > clock_timestamp()) AS retained_references,
    (SELECT min(created_at) FROM blob.blob_stage WHERE state = 'open') AS oldest_open_stage_at;

CREATE FUNCTION blob.update_storage_policy(
    p_expected_updated_at TIMESTAMPTZ,
    p_max_blob_bytes BIGINT,
    p_stage_ttl_seconds INTEGER,
    p_orphan_grace_seconds INTEGER,
    p_max_retention_seconds BIGINT,
    p_max_gc_batch INTEGER,
    p_max_gc_lease_seconds INTEGER,
    p_allowed_media_types TEXT[],
    p_actor TEXT
) RETURNS TIMESTAMPTZ
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    previous blob.storage_policy%ROWTYPE;
    changed_at TIMESTAMPTZ;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_blob_gc' OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob policy identity denied';
    END IF;
    IF p_allowed_media_types IS NULL OR cardinality(p_allowed_media_types) NOT BETWEEN 1 AND 256
       OR EXISTS (SELECT 1 FROM unnest(p_allowed_media_types) AS media(value)
                  WHERE value = '' OR value <> lower(value) OR length(value) > 200) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid blob policy update';
    END IF;
    SELECT * INTO STRICT previous FROM blob.storage_policy WHERE singleton FOR UPDATE;
    IF previous.updated_at <> p_expected_updated_at THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale blob policy';
    END IF;
    changed_at := greatest(clock_timestamp(), previous.updated_at + interval '1 microsecond');
    UPDATE blob.storage_policy
    SET max_blob_bytes = p_max_blob_bytes,
        stage_ttl_seconds = p_stage_ttl_seconds,
        orphan_grace_seconds = p_orphan_grace_seconds,
        max_retention_seconds = p_max_retention_seconds,
        max_gc_batch = p_max_gc_batch,
        max_gc_lease_seconds = p_max_gc_lease_seconds,
        allowed_media_types = p_allowed_media_types,
        updated_at = changed_at,
        updated_by = invoker.principal_id
    WHERE singleton;
    INSERT INTO blob.storage_policy_event (previous_policy, new_policy, changed_at, changed_by)
    SELECT to_jsonb(previous), to_jsonb(policy), changed_at, invoker.principal_id
    FROM blob.storage_policy AS policy WHERE singleton;
    RETURN changed_at;
END;
$$;

CREATE FUNCTION blob.begin_stage(
    p_stage_id UUID,
    p_principal_id TEXT,
    p_media_type TEXT,
    p_requested_max_bytes BIGINT,
    p_expected_digest TEXT,
    p_expected_size_bytes BIGINT,
    p_ttl_seconds INTEGER,
    p_actor TEXT
) RETURNS TABLE (stage_id UUID, max_bytes BIGINT, issued_at TIMESTAMPTZ, expires_at TIMESTAMPTZ)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    policy blob.storage_policy%ROWTYPE;
    existing blob.blob_stage%ROWTYPE;
    inserted BOOLEAN;
    created TIMESTAMPTZ := clock_timestamp();
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT NOT IN ('alpheus_agent_control_api', 'alpheus_research_gateway')
       OR invoker.owner_id NOT IN ('agent_control', 'research_gateway')
       OR p_principal_id IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob stage identity denied';
    END IF;
    SELECT * INTO STRICT policy FROM blob.storage_policy WHERE singleton;
    IF p_stage_id IS NULL
       OR p_media_type IS NULL OR p_media_type <> lower(p_media_type) OR NOT p_media_type = ANY(policy.allowed_media_types)
       OR p_requested_max_bytes < 1 OR p_requested_max_bytes > policy.max_blob_bytes
       OR p_ttl_seconds < 1 OR p_ttl_seconds > policy.stage_ttl_seconds
       OR (p_expected_digest IS NOT NULL AND p_expected_digest !~ '^[0-9a-f]{64}$')
       OR (p_expected_size_bytes IS NOT NULL AND (p_expected_size_bytes < 1 OR p_expected_size_bytes > p_requested_max_bytes)) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid blob stage request';
    END IF;
    INSERT INTO blob.blob_stage (
        stage_id, principal_id, issuer_owner, media_type, max_bytes_snapshot,
        expected_digest, expected_size_bytes, created_at, expires_at
    ) VALUES (
        p_stage_id, invoker.principal_id, invoker.owner_id, p_media_type, p_requested_max_bytes,
        p_expected_digest, p_expected_size_bytes, created, created + make_interval(secs => p_ttl_seconds)
    ) ON CONFLICT ON CONSTRAINT blob_stage_pkey DO NOTHING
    RETURNING true INTO inserted;
    SELECT * INTO STRICT existing FROM blob.blob_stage AS candidate WHERE candidate.stage_id = p_stage_id;
    IF NOT coalesce(inserted, false) AND NOT (
        existing.principal_id = invoker.principal_id AND existing.issuer_owner = invoker.owner_id
        AND existing.media_type = p_media_type
        AND existing.max_bytes_snapshot = p_requested_max_bytes
        AND existing.expected_digest IS NOT DISTINCT FROM p_expected_digest
        AND existing.expected_size_bytes IS NOT DISTINCT FROM p_expected_size_bytes
        AND existing.state IN ('open', 'materialized', 'committed')
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'blob stage identity conflict';
    END IF;
    IF inserted THEN
        INSERT INTO blob.lifecycle_event (
            subject_kind, subject_id, transition, generation, actor, reason_code
        ) VALUES ('stage', p_stage_id::text, 'staged', 1, invoker.principal_id, 'stage_opened');
    END IF;
    RETURN QUERY SELECT existing.stage_id, existing.max_bytes_snapshot, existing.created_at, existing.expires_at;
END;
$$;

CREATE FUNCTION blob.record_stage_facts(
    p_stage_id UUID,
    p_principal_id TEXT,
    p_content_digest TEXT,
    p_size_bytes BIGINT,
    p_actor TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    staged blob.blob_stage%ROWTYPE;
    content_ready BOOLEAN;
    now_at TIMESTAMPTZ := clock_timestamp();
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT NOT IN ('alpheus_agent_control_api', 'alpheus_research_gateway')
       OR invoker.owner_id NOT IN ('agent_control', 'research_gateway')
       OR p_principal_id IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'staged blob identity denied';
    END IF;
    IF p_content_digest IS NULL OR p_content_digest !~ '^[0-9a-f]{64}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid staged blob facts';
    END IF;
    SELECT * INTO STRICT staged FROM blob.blob_stage AS candidate
    WHERE candidate.stage_id = p_stage_id FOR UPDATE;
    IF staged.state = 'materialized' THEN
        IF staged.principal_id <> invoker.principal_id OR staged.issuer_owner <> invoker.owner_id
           OR staged.content_digest <> p_content_digest
           OR staged.size_bytes <> p_size_bytes THEN
            RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'staged blob facts conflict';
        END IF;
        RETURN false;
    END IF;
    IF staged.state <> 'open' OR staged.expires_at <= now_at
       OR staged.principal_id <> invoker.principal_id OR staged.issuer_owner <> invoker.owner_id
       OR p_size_bytes < 1 OR p_size_bytes > staged.max_bytes_snapshot
       OR (staged.expected_digest IS NOT NULL AND staged.expected_digest <> p_content_digest)
       OR (staged.expected_size_bytes IS NOT NULL AND staged.expected_size_bytes <> p_size_bytes) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'staged blob facts violate grant';
    END IF;
    INSERT INTO blob.blob_content (content_digest, size_bytes, state, created_at, updated_at)
    VALUES (p_content_digest, p_size_bytes, 'staged', now_at, now_at)
    ON CONFLICT ON CONSTRAINT blob_content_pkey DO UPDATE
    SET state = CASE
            WHEN blob.blob_content.state = 'deleted' THEN 'staged'
            ELSE blob.blob_content.state
        END,
        updated_at = CASE
            WHEN blob.blob_content.state IN ('staged', 'deleted') THEN now_at
            ELSE blob.blob_content.updated_at
        END,
        gc_token = NULL,
        gc_expires_at = NULL,
        quarantine_reason = NULL
    WHERE blob.blob_content.size_bytes = EXCLUDED.size_bytes
      AND blob.blob_content.state IN ('staged', 'committed', 'deleted')
    RETURNING true INTO content_ready;
    IF NOT coalesce(content_ready, false) THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'blob content unavailable or conflicting';
    END IF;
    UPDATE blob.blob_stage
    SET state = 'materialized', content_digest = p_content_digest, size_bytes = p_size_bytes
    WHERE stage_id = p_stage_id;
    RETURN true;
END
$$;

CREATE FUNCTION blob.commit_stage(
    p_stage_id UUID,
    p_principal_id TEXT,
    p_content_digest TEXT,
    p_size_bytes BIGINT,
    p_origin_owner TEXT,
    p_origin_record_type TEXT,
    p_origin_record_id TEXT,
    p_origin_record_digest TEXT,
    p_actor TEXT
) RETURNS TABLE (
    blob_id UUID,
    content_digest TEXT,
    media_type TEXT,
    size_bytes BIGINT,
    committed_at TIMESTAMPTZ
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    staged blob.blob_stage%ROWTYPE;
    existing blob.blob_object%ROWTYPE;
    new_blob_id UUID;
    committed TIMESTAMPTZ := clock_timestamp();
    content_ready BOOLEAN;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT NOT IN ('alpheus_agent_control_api', 'alpheus_research_gateway')
       OR invoker.owner_id NOT IN ('agent_control', 'research_gateway')
       OR p_principal_id IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id
       OR p_origin_owner IS DISTINCT FROM invoker.owner_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob commit identity denied';
    END IF;
    IF p_content_digest IS NULL OR p_content_digest !~ '^[0-9a-f]{64}$'
       OR p_origin_record_type !~ '^[a-z][a-z0-9_]{0,63}$'
       OR p_origin_record_id IS NULL OR p_origin_record_id = '' OR p_origin_record_id ~ '[[:space:][:cntrl:]]'
       OR p_origin_record_digest IS NULL OR p_origin_record_digest !~ '^[0-9a-f]{64}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid blob commit';
    END IF;
    SELECT * INTO STRICT staged FROM blob.blob_stage AS candidate
    WHERE candidate.stage_id = p_stage_id FOR UPDATE;
    IF staged.state = 'committed' THEN
        SELECT * INTO STRICT existing FROM blob.blob_object AS object WHERE object.stage_id = p_stage_id;
        IF staged.principal_id <> invoker.principal_id OR staged.issuer_owner <> invoker.owner_id
           OR staged.content_digest <> p_content_digest
           OR staged.size_bytes <> p_size_bytes OR existing.origin_owner <> invoker.owner_id
           OR existing.origin_record_type <> p_origin_record_type OR existing.origin_record_id <> p_origin_record_id
           OR existing.origin_record_digest <> p_origin_record_digest THEN
            RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'blob commit identity conflict';
        END IF;
        RETURN QUERY SELECT existing.blob_id, existing.content_digest::text, existing.media_type,
            existing.size_bytes, existing.committed_at;
        RETURN;
    END IF;
    IF staged.state <> 'materialized' OR staged.expires_at <= committed
       OR staged.principal_id <> invoker.principal_id OR staged.issuer_owner <> invoker.owner_id
       OR staged.content_digest <> p_content_digest OR staged.size_bytes <> p_size_bytes THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'blob stage cannot commit';
    END IF;

    INSERT INTO blob.blob_content (content_digest, size_bytes, state, created_at, updated_at)
    VALUES (p_content_digest, p_size_bytes, 'committed', committed, committed)
    ON CONFLICT ON CONSTRAINT blob_content_pkey DO UPDATE
    SET state = 'committed', updated_at = committed,
        gc_token = NULL, gc_expires_at = NULL, quarantine_reason = NULL
    WHERE blob.blob_content.size_bytes = EXCLUDED.size_bytes
      AND blob.blob_content.state IN ('staged', 'committed')
    RETURNING true INTO content_ready;
    IF NOT coalesce(content_ready, false) THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'blob content unavailable or conflicting';
    END IF;

    new_blob_id := gen_random_uuid();
    INSERT INTO blob.blob_object (
        blob_id, stage_id, content_digest, media_type, size_bytes,
        origin_owner, origin_record_type, origin_record_id, origin_record_digest,
        state, committed_at
    ) VALUES (
        new_blob_id, p_stage_id, p_content_digest, staged.media_type, p_size_bytes,
        invoker.owner_id, p_origin_record_type, p_origin_record_id, p_origin_record_digest,
        'committed', committed
    );
    UPDATE blob.blob_stage
    SET state = 'committed', content_digest = p_content_digest, size_bytes = p_size_bytes,
        blob_id = new_blob_id, committed_at = committed
    WHERE blob.blob_stage.stage_id = p_stage_id;
    INSERT INTO blob.lifecycle_event (
        subject_kind, subject_id, transition, generation, actor, occurred_at, reason_code,
        details
    ) VALUES (
        'blob', new_blob_id::text, 'committed', 1, invoker.principal_id, committed, 'stage_committed',
        jsonb_build_object('stage_id', p_stage_id, 'content_digest', p_content_digest)
    );
    RETURN QUERY SELECT new_blob_id, p_content_digest, staged.media_type, p_size_bytes, committed;
END
$$;

CREATE FUNCTION blob.bind_reference_internal(
    p_reference_owner TEXT,
    p_binding_id TEXT,
    p_blob_id UUID,
    p_reference_record_type TEXT,
    p_reference_record_id TEXT,
    p_reference_record_digest TEXT,
    p_owner_principal TEXT,
    p_access_class TEXT,
    p_retention_until TIMESTAMPTZ,
    p_actor TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    policy blob.storage_policy%ROWTYPE;
    existing blob.blob_reference%ROWTYPE;
    inserted BOOLEAN;
    now_at TIMESTAMPTZ := clock_timestamp();
BEGIN
    IF p_reference_owner NOT IN ('agent_control', 'research_gateway')
       OR p_binding_id IS NULL OR p_binding_id = '' OR p_binding_id ~ '[[:space:][:cntrl:]]'
       OR p_reference_record_type !~ '^[a-z][a-z0-9_]{0,63}$'
       OR p_reference_record_id IS NULL OR p_reference_record_id = '' OR p_reference_record_id ~ '[[:space:][:cntrl:]]'
       OR p_reference_record_digest !~ '^[0-9a-f]{64}$'
       OR p_owner_principal IS NULL OR p_owner_principal = '' OR p_owner_principal ~ '[[:space:][:cntrl:]]'
       OR p_access_class NOT IN ('private', 'explicit')
       OR p_actor IS NULL OR p_actor = '' OR p_actor ~ '[[:space:][:cntrl:]]' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid blob reference';
    END IF;
    SELECT * INTO STRICT policy FROM blob.storage_policy WHERE singleton;
    IF p_retention_until <= now_at
       OR p_retention_until > now_at + make_interval(secs => policy.max_retention_seconds::double precision) THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'blob retention outside policy';
    END IF;
    PERFORM 1
    FROM blob.blob_content AS content
    JOIN blob.blob_object AS object ON object.content_digest = content.content_digest
    JOIN blob.blob_stage AS stage ON stage.stage_id = object.stage_id
    WHERE object.blob_id = p_blob_id AND object.state = 'committed' AND content.state = 'committed'
      AND object.origin_owner = p_reference_owner
      AND stage.issuer_owner = p_reference_owner
      AND stage.principal_id = p_owner_principal
    FOR UPDATE OF content;
    IF NOT FOUND THEN
        RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'blob is not referenceable';
    END IF;
    INSERT INTO blob.blob_reference (
        binding_id, blob_id, reference_owner, reference_record_type,
        reference_record_id, reference_record_digest, owner_principal,
        access_class, retention_until, bound_at
    ) VALUES (
        p_binding_id, p_blob_id, p_reference_owner, p_reference_record_type,
        p_reference_record_id, p_reference_record_digest, p_owner_principal,
        p_access_class, p_retention_until, now_at
    ) ON CONFLICT ON CONSTRAINT blob_reference_pkey DO NOTHING
    RETURNING true INTO inserted;
    SELECT * INTO STRICT existing FROM blob.blob_reference AS reference
    WHERE reference.binding_id = p_binding_id;
    IF NOT coalesce(inserted, false) AND NOT (
        existing.blob_id = p_blob_id AND existing.reference_owner = p_reference_owner
        AND existing.reference_record_type = p_reference_record_type
        AND existing.reference_record_id = p_reference_record_id
        AND existing.reference_record_digest = p_reference_record_digest
        AND existing.owner_principal = p_owner_principal AND existing.access_class = p_access_class
        AND existing.retention_until = p_retention_until AND existing.state = 'active'
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'blob reference identity conflict';
    END IF;
    IF inserted THEN
        INSERT INTO blob.lifecycle_event (
            subject_kind, subject_id, transition, generation, actor, reason_code,
            details
        ) VALUES (
            'binding', p_binding_id, 'reference_bound', 1, p_actor, 'owner_reference_bound',
            jsonb_build_object('blob_id', p_blob_id, 'reference_owner', p_reference_owner)
        );
    END IF;
    RETURN coalesce(inserted, false);
END
$$;

CREATE FUNCTION blob.bind_agent_control_reference(
    p_binding_id TEXT, p_blob_id UUID, p_reference_record_type TEXT,
    p_reference_record_id TEXT, p_reference_record_digest TEXT,
    p_owner_principal TEXT, p_access_class TEXT,
    p_retention_until TIMESTAMPTZ, p_actor TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api'
       OR invoker.owner_id <> 'agent_control'
       OR p_owner_principal IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'agent-control blob binding identity denied';
    END IF;
    RETURN blob.bind_reference_internal(
        'agent_control', p_binding_id, p_blob_id, p_reference_record_type,
        p_reference_record_id, p_reference_record_digest, invoker.principal_id,
        p_access_class, p_retention_until, invoker.principal_id
    );
END;
$$;

CREATE FUNCTION blob.bind_research_gateway_reference(
    p_binding_id TEXT, p_blob_id UUID, p_reference_record_type TEXT,
    p_reference_record_id TEXT, p_reference_record_digest TEXT,
    p_owner_principal TEXT, p_access_class TEXT,
    p_retention_until TIMESTAMPTZ, p_actor TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_research_gateway'
       OR invoker.owner_id <> 'research_gateway'
       OR p_owner_principal IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'research blob binding identity denied';
    END IF;
    RETURN blob.bind_reference_internal(
        'research_gateway', p_binding_id, p_blob_id, p_reference_record_type,
        p_reference_record_id, p_reference_record_digest, invoker.principal_id,
        p_access_class, p_retention_until, invoker.principal_id
    );
END;
$$;

CREATE FUNCTION blob.change_acl_internal(
    p_reference_owner TEXT,
    p_binding_id TEXT,
    p_owner_principal TEXT,
    p_grantee_principal TEXT,
    p_expected_generation BIGINT,
    p_action TEXT,
    p_reason_code TEXT,
    p_actor TEXT
) RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    reference blob.blob_reference%ROWTYPE;
    access blob.blob_acl%ROWTYPE;
    next_generation BIGINT;
    now_at TIMESTAMPTZ := clock_timestamp();
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT NOT IN ('alpheus_agent_control_api', 'alpheus_research_gateway')
       OR invoker.owner_id NOT IN ('agent_control', 'research_gateway')
       OR p_reference_owner IS DISTINCT FROM invoker.owner_id
       OR p_owner_principal IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob ACL identity denied';
    END IF;
    IF p_grantee_principal IS NULL OR p_grantee_principal = '' OR p_grantee_principal ~ '[[:space:][:cntrl:]]'
       OR p_grantee_principal = p_owner_principal OR p_action NOT IN ('grant', 'revoke')
       OR p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid blob ACL change';
    END IF;
    SELECT * INTO STRICT reference FROM blob.blob_reference AS binding
    WHERE binding.binding_id = p_binding_id FOR UPDATE;
    IF reference.reference_owner <> p_reference_owner OR reference.owner_principal <> p_owner_principal
       OR reference.state <> 'active' OR reference.retention_until <= now_at
       OR reference.access_class <> 'explicit' THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob ACL change denied';
    END IF;
    SELECT * INTO access FROM blob.blob_acl AS candidate
    WHERE candidate.binding_id = p_binding_id AND candidate.principal_id = p_grantee_principal
    FOR UPDATE;
    IF p_action = 'grant' THEN
        IF NOT FOUND THEN
            IF p_expected_generation <> 0 THEN
                RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale blob ACL generation';
            END IF;
            next_generation := 1;
            INSERT INTO blob.blob_acl (
                binding_id, principal_id, state, generation, granted_at, reason_code
            ) VALUES (
                p_binding_id, p_grantee_principal, 'active', next_generation, now_at, p_reason_code
            );
        ELSE
            IF access.generation <> p_expected_generation OR access.state <> 'revoked' THEN
                RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale blob ACL generation';
            END IF;
            next_generation := access.generation + 1;
            UPDATE blob.blob_acl
            SET state = 'active', generation = next_generation, granted_at = now_at,
                revoked_at = NULL, reason_code = p_reason_code
            WHERE binding_id = p_binding_id AND principal_id = p_grantee_principal;
        END IF;
        INSERT INTO blob.lifecycle_event (
            subject_kind, subject_id, transition, generation, actor, reason_code,
            details
        ) VALUES (
            'acl', p_binding_id || ':' || p_grantee_principal, 'acl_granted',
            next_generation, invoker.principal_id, p_reason_code,
            jsonb_build_object('binding_id', p_binding_id, 'principal_id', p_grantee_principal)
        );
    ELSE
        IF NOT FOUND OR access.generation <> p_expected_generation OR access.state <> 'active' THEN
            RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale blob ACL generation';
        END IF;
        next_generation := access.generation + 1;
        UPDATE blob.blob_acl
        SET state = 'revoked', generation = next_generation, revoked_at = now_at,
            reason_code = p_reason_code
        WHERE binding_id = p_binding_id AND principal_id = p_grantee_principal;
        INSERT INTO blob.lifecycle_event (
            subject_kind, subject_id, transition, generation, actor, reason_code,
            details
        ) VALUES (
            'acl', p_binding_id || ':' || p_grantee_principal, 'acl_revoked',
            next_generation, invoker.principal_id, p_reason_code,
            jsonb_build_object('binding_id', p_binding_id, 'principal_id', p_grantee_principal)
        );
    END IF;
    RETURN next_generation;
END
$$;

CREATE FUNCTION blob.grant_agent_control_read(
    p_binding_id TEXT, p_owner_principal TEXT, p_grantee_principal TEXT,
    p_expected_generation BIGINT, p_reason_code TEXT, p_actor TEXT
) RETURNS BIGINT
LANGUAGE SQL SECURITY DEFINER SET search_path = pg_catalog, blob
AS $$ SELECT blob.change_acl_internal('agent_control', p_binding_id, p_owner_principal,
    p_grantee_principal, p_expected_generation, 'grant', p_reason_code, p_actor) $$;

CREATE FUNCTION blob.revoke_agent_control_read(
    p_binding_id TEXT, p_owner_principal TEXT, p_grantee_principal TEXT,
    p_expected_generation BIGINT, p_reason_code TEXT, p_actor TEXT
) RETURNS BIGINT
LANGUAGE SQL SECURITY DEFINER SET search_path = pg_catalog, blob
AS $$ SELECT blob.change_acl_internal('agent_control', p_binding_id, p_owner_principal,
    p_grantee_principal, p_expected_generation, 'revoke', p_reason_code, p_actor) $$;

CREATE FUNCTION blob.grant_research_gateway_read(
    p_binding_id TEXT, p_owner_principal TEXT, p_grantee_principal TEXT,
    p_expected_generation BIGINT, p_reason_code TEXT, p_actor TEXT
) RETURNS BIGINT
LANGUAGE SQL SECURITY DEFINER SET search_path = pg_catalog, blob
AS $$ SELECT blob.change_acl_internal('research_gateway', p_binding_id, p_owner_principal,
    p_grantee_principal, p_expected_generation, 'grant', p_reason_code, p_actor) $$;

CREATE FUNCTION blob.revoke_research_gateway_read(
    p_binding_id TEXT, p_owner_principal TEXT, p_grantee_principal TEXT,
    p_expected_generation BIGINT, p_reason_code TEXT, p_actor TEXT
) RETURNS BIGINT
LANGUAGE SQL SECURITY DEFINER SET search_path = pg_catalog, blob
AS $$ SELECT blob.change_acl_internal('research_gateway', p_binding_id, p_owner_principal,
    p_grantee_principal, p_expected_generation, 'revoke', p_reason_code, p_actor) $$;

CREATE FUNCTION blob.release_reference_internal(
    p_reference_owner TEXT,
    p_binding_id TEXT,
    p_owner_principal TEXT,
    p_expected_generation BIGINT,
    p_reason_code TEXT,
    p_actor TEXT
) RETURNS BIGINT
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    next_generation BIGINT;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT NOT IN ('alpheus_agent_control_api', 'alpheus_research_gateway')
       OR invoker.owner_id NOT IN ('agent_control', 'research_gateway')
       OR p_reference_owner IS DISTINCT FROM invoker.owner_id
       OR p_owner_principal IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob release identity denied';
    END IF;
    IF p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid blob reference release';
    END IF;
    UPDATE blob.blob_reference
    SET state = 'released', generation = generation + 1, released_at = clock_timestamp()
    WHERE binding_id = p_binding_id AND reference_owner = p_reference_owner
      AND owner_principal = p_owner_principal AND state = 'active'
      AND generation = p_expected_generation
    RETURNING generation INTO next_generation;
    IF next_generation IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '40001', MESSAGE = 'stale blob reference generation';
    END IF;
    INSERT INTO blob.lifecycle_event (
        subject_kind, subject_id, transition, generation, actor, reason_code
    ) VALUES (
        'binding', p_binding_id, 'reference_released', next_generation, invoker.principal_id, p_reason_code
    );
    RETURN next_generation;
END
$$;

CREATE FUNCTION blob.release_agent_control_reference(
    p_binding_id TEXT, p_owner_principal TEXT, p_expected_generation BIGINT,
    p_reason_code TEXT, p_actor TEXT
) RETURNS BIGINT
LANGUAGE SQL SECURITY DEFINER SET search_path = pg_catalog, blob
AS $$ SELECT blob.release_reference_internal('agent_control', p_binding_id, p_owner_principal,
    p_expected_generation, p_reason_code, p_actor) $$;

CREATE FUNCTION blob.release_research_gateway_reference(
    p_binding_id TEXT, p_owner_principal TEXT, p_expected_generation BIGINT,
    p_reason_code TEXT, p_actor TEXT
) RETURNS BIGINT
LANGUAGE SQL SECURITY DEFINER SET search_path = pg_catalog, blob
AS $$ SELECT blob.release_reference_internal('research_gateway', p_binding_id, p_owner_principal,
    p_expected_generation, p_reason_code, p_actor) $$;

CREATE FUNCTION blob.authorize_read(
    p_principal_id TEXT,
    p_binding_id TEXT,
    p_blob_id UUID,
    p_reference_owner TEXT,
    p_reference_record_type TEXT,
    p_reference_record_id TEXT,
    p_reference_record_digest TEXT
) RETURNS TABLE (
    schema_revision SMALLINT,
    blob_id UUID,
    content_digest TEXT,
    media_type TEXT,
    size_bytes BIGINT,
    origin_owner TEXT,
    origin_record_type TEXT,
    origin_record_id TEXT,
    origin_record_digest TEXT,
    committed_at TIMESTAMPTZ,
    authorized_at TIMESTAMPTZ,
    valid_until TIMESTAMPTZ
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT NOT IN (
        'alpheus_agent_control_api', 'alpheus_agent_worker', 'alpheus_research_gateway'
    ) OR p_principal_id IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob read identity denied';
    END IF;
    RETURN QUERY
    SELECT 1::smallint, object.blob_id, object.content_digest::text,
        object.media_type, object.size_bytes, object.origin_owner,
        object.origin_record_type, object.origin_record_id,
        object.origin_record_digest::text, object.committed_at,
        clock_timestamp(), least(reference.retention_until, clock_timestamp() + interval '60 seconds')
    FROM blob.blob_reference AS reference
    JOIN blob.blob_object AS object ON object.blob_id = reference.blob_id
    JOIN blob.blob_content AS content ON content.content_digest = object.content_digest
    WHERE reference.binding_id = p_binding_id
      AND reference.blob_id = p_blob_id
      AND reference.reference_owner = p_reference_owner
      AND reference.reference_record_type = p_reference_record_type
      AND reference.reference_record_id = p_reference_record_id
      AND reference.reference_record_digest = p_reference_record_digest
      AND reference.state = 'active'
      AND reference.retention_until > clock_timestamp()
      AND object.state = 'committed'
      AND content.state = 'committed'
      AND (
          reference.owner_principal = invoker.principal_id
          OR (
              reference.access_class = 'explicit'
              AND EXISTS (
                  SELECT 1 FROM blob.blob_acl AS acl
                  WHERE acl.binding_id = reference.binding_id
                    AND acl.principal_id = invoker.principal_id
                    AND acl.state = 'active'
              )
          )
      );
END;
$$;

CREATE FUNCTION blob.claim_stage_gc(
    p_worker_id TEXT,
    p_limit INTEGER,
    p_lease_seconds INTEGER
) RETURNS TABLE (stage_id UUID, claim_token UUID, claim_expires_at TIMESTAMPTZ)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    policy blob.storage_policy%ROWTYPE;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_blob_gc' OR p_worker_id IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'stage GC identity denied';
    END IF;
    SELECT * INTO STRICT policy FROM blob.storage_policy WHERE singleton;
    IF p_limit < 1 OR p_limit > policy.max_gc_batch
       OR p_lease_seconds < 1 OR p_lease_seconds > policy.max_gc_lease_seconds THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid stage GC claim';
    END IF;
    RETURN QUERY
    WITH candidates AS (
        SELECT candidate.stage_id
        FROM blob.blob_stage AS candidate
        WHERE (candidate.state IN ('open', 'materialized', 'committed') AND candidate.expires_at < clock_timestamp())
           OR (candidate.state = 'cleanup_leased' AND candidate.cleanup_expires_at < clock_timestamp())
        ORDER BY candidate.expires_at, candidate.stage_id
        FOR UPDATE SKIP LOCKED
        LIMIT p_limit
    )
    UPDATE blob.blob_stage AS stage
    SET cleanup_previous_state = CASE
            WHEN stage.state = 'cleanup_leased' THEN stage.cleanup_previous_state
            ELSE stage.state
        END,
        state = 'cleanup_leased',
        cleanup_token = gen_random_uuid(),
        cleanup_expires_at = clock_timestamp() + make_interval(secs => p_lease_seconds)
    FROM candidates
    WHERE stage.stage_id = candidates.stage_id
    RETURNING stage.stage_id, stage.cleanup_token, stage.cleanup_expires_at;
END
$$;

CREATE FUNCTION blob.complete_stage_gc(
    p_stage_id UUID,
    p_claim_token UUID,
    p_actor TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    changed INTEGER;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_blob_gc' OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'stage GC identity denied';
    END IF;
    UPDATE blob.blob_stage
    SET state = CASE WHEN cleanup_previous_state IN ('open', 'materialized') THEN 'aborted' ELSE 'cleaned' END,
        cleanup_previous_state = NULL, cleanup_token = NULL, cleanup_expires_at = NULL
    WHERE stage_id = p_stage_id AND state = 'cleanup_leased' AND cleanup_token = p_claim_token;
    GET DIAGNOSTICS changed = ROW_COUNT;
    IF changed = 0 THEN
        RETURN false;
    END IF;
    INSERT INTO blob.lifecycle_event (
        subject_kind, subject_id, transition, generation, actor, reason_code
    ) VALUES ('stage', p_stage_id::text, 'deleted', 1, invoker.principal_id, 'staged_bytes_removed');
    RETURN true;
END
$$;

CREATE FUNCTION blob.claim_content_gc(
    p_worker_id TEXT,
    p_limit INTEGER,
    p_lease_seconds INTEGER
) RETURNS TABLE (
    content_digest TEXT,
    size_bytes BIGINT,
    claim_token UUID,
    claim_expires_at TIMESTAMPTZ
)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    policy blob.storage_policy%ROWTYPE;
    candidate RECORD;
    token UUID;
    expires TIMESTAMPTZ;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_blob_gc' OR p_worker_id IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'content GC identity denied';
    END IF;
    SELECT * INTO STRICT policy FROM blob.storage_policy WHERE singleton;
    IF p_limit < 1 OR p_limit > policy.max_gc_batch
       OR p_lease_seconds < 1 OR p_lease_seconds > policy.max_gc_lease_seconds THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid content GC claim';
    END IF;
    FOR candidate IN
        SELECT content.content_digest, content.size_bytes, content.state
        FROM blob.blob_content AS content
        WHERE (
            content.state = 'staged'
            AND content.updated_at < clock_timestamp() - make_interval(secs => policy.orphan_grace_seconds)
            AND NOT EXISTS (
                SELECT 1 FROM blob.blob_stage AS stage
                WHERE stage.content_digest = content.content_digest
                  AND stage.state = 'materialized'
                  AND stage.expires_at > clock_timestamp()
            )
        ) OR (
            content.state = 'committed'
            AND content.updated_at < clock_timestamp() - make_interval(secs => policy.orphan_grace_seconds)
            AND NOT EXISTS (
                SELECT 1
                FROM blob.blob_object AS object
                JOIN blob.blob_reference AS reference ON reference.blob_id = object.blob_id
                WHERE object.content_digest = content.content_digest
                  AND object.state = 'committed'
                  AND reference.state = 'active'
                  AND reference.retention_until > clock_timestamp()
            )
        ) OR (
            content.state = 'deleting' AND content.gc_expires_at < clock_timestamp()
        )
        ORDER BY content.updated_at, content.content_digest
        FOR UPDATE SKIP LOCKED
        LIMIT p_limit
    LOOP
        token := gen_random_uuid();
        expires := clock_timestamp() + make_interval(secs => p_lease_seconds);
        UPDATE blob.blob_content AS content
        SET state = 'deleting', gc_token = token, gc_expires_at = expires,
            updated_at = clock_timestamp(), quarantine_reason = NULL
        WHERE content.content_digest = candidate.content_digest;
        UPDATE blob.blob_object AS object
        SET state = 'deleting'
        WHERE object.content_digest = candidate.content_digest AND object.state = 'committed';
        INSERT INTO blob.lifecycle_event (
            subject_kind, subject_id, transition, generation, actor, reason_code
        ) VALUES (
            'blob', candidate.content_digest::text, 'gc_claimed', 1,
            invoker.principal_id, 'committed_orphan_claimed'
        );
        content_digest := candidate.content_digest::text;
        size_bytes := candidate.size_bytes;
        claim_token := token;
        claim_expires_at := expires;
        RETURN NEXT;
    END LOOP;
END
$$;

CREATE FUNCTION blob.complete_content_gc(
    p_content_digest TEXT,
    p_claim_token UUID,
    p_actor TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    changed INTEGER;
    deleted_time TIMESTAMPTZ := clock_timestamp();
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_blob_gc' OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'content GC identity denied';
    END IF;
    IF p_content_digest !~ '^[0-9a-f]{64}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid content GC completion';
    END IF;
    UPDATE blob.blob_content
    SET state = 'deleted', updated_at = deleted_time, gc_token = NULL, gc_expires_at = NULL
    WHERE content_digest = p_content_digest AND state = 'deleting' AND gc_token = p_claim_token;
    GET DIAGNOSTICS changed = ROW_COUNT;
    IF changed = 0 THEN
        RETURN false;
    END IF;
    UPDATE blob.blob_object
    SET state = 'deleted', deleted_at = deleted_time
    WHERE content_digest = p_content_digest AND state = 'deleting';
    INSERT INTO blob.lifecycle_event (
        subject_kind, subject_id, transition, generation, actor, occurred_at, reason_code
    ) VALUES (
        'blob', p_content_digest, 'deleted', 1, invoker.principal_id, deleted_time, 'committed_orphan_removed'
    );
    RETURN true;
END
$$;

CREATE FUNCTION blob.quarantine_content(
    p_content_digest TEXT,
    p_reason_code TEXT,
    p_actor TEXT
) RETURNS BOOLEAN
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, blob
AS $$
DECLARE
    changed INTEGER;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_blob_gc' OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'content quarantine identity denied';
    END IF;
    IF p_content_digest !~ '^[0-9a-f]{64}$' OR p_reason_code !~ '^[a-z][a-z0-9_]{0,63}$' THEN
        RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid content quarantine';
    END IF;
    UPDATE blob.blob_content
    SET state = 'quarantined', updated_at = clock_timestamp(),
        gc_token = NULL, gc_expires_at = NULL, quarantine_reason = p_reason_code
    WHERE content_digest = p_content_digest AND state IN ('committed', 'deleting');
    GET DIAGNOSTICS changed = ROW_COUNT;
    IF changed = 0 THEN
        RETURN false;
    END IF;
    UPDATE blob.blob_object
    SET state = 'quarantined', quarantined_at = clock_timestamp()
    WHERE content_digest = p_content_digest AND state IN ('committed', 'deleting');
    INSERT INTO blob.lifecycle_event (
        subject_kind, subject_id, transition, generation, actor, reason_code
    ) VALUES ('blob', p_content_digest, 'quarantined', 1, invoker.principal_id, p_reason_code);
    RETURN true;
END
$$;

RESET ROLE;
