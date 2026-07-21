SET ROLE alpheus_agent_migrator;

-- Retry reconciliation for the local Blob adapter. The original AP0 command
-- intentionally rejects a committed stage; this narrow wrapper permits an
-- exact replay to verify that state without reopening or mutating it.
CREATE FUNCTION blob.reconcile_stage_facts(
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
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api'
       OR invoker.owner_id <> 'agent_control'
       OR p_principal_id IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'staged blob reconciliation identity denied';
    END IF;
    SELECT * INTO STRICT staged FROM blob.blob_stage AS candidate
    WHERE candidate.stage_id = p_stage_id FOR UPDATE;
    IF staged.state = 'committed' THEN
        IF staged.principal_id <> invoker.principal_id OR staged.issuer_owner <> invoker.owner_id
           OR staged.content_digest <> p_content_digest OR staged.size_bytes <> p_size_bytes THEN
            RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'committed stage facts conflict';
        END IF;
        RETURN false;
    END IF;
    RETURN blob.record_stage_facts(
        p_stage_id, p_principal_id, p_content_digest, p_size_bytes, p_actor
    );
END
$$;

-- Returns only the caller-owned stage state and, after commit, its immutable
-- BlobRef fields. It exposes no table read primitive and checks the exact
-- origin identity before allowing a completed retry to reuse a BlobRef.
CREATE FUNCTION blob.resume_agent_control_stage(
    p_stage_id UUID,
    p_principal_id TEXT,
    p_content_digest TEXT,
    p_size_bytes BIGINT,
    p_origin_record_type TEXT,
    p_origin_record_id TEXT,
    p_origin_record_digest TEXT,
    p_actor TEXT
) RETURNS TABLE (
    stage_state TEXT,
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
    object blob.blob_object%ROWTYPE;
    invoker RECORD;
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT <> 'alpheus_agent_control_api'
       OR invoker.owner_id <> 'agent_control'
       OR p_principal_id IS DISTINCT FROM invoker.principal_id
       OR p_actor IS DISTINCT FROM invoker.principal_id THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob stage resume identity denied';
    END IF;
    SELECT * INTO STRICT staged FROM blob.blob_stage AS candidate
    WHERE candidate.stage_id = p_stage_id;
    IF staged.principal_id <> invoker.principal_id OR staged.issuer_owner <> invoker.owner_id
       OR staged.expected_digest <> p_content_digest OR staged.expected_size_bytes <> p_size_bytes
       OR staged.state NOT IN ('open', 'materialized', 'committed') THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'blob stage resume denied';
    END IF;
    IF staged.state <> 'committed' THEN
        RETURN QUERY SELECT staged.state, NULL::UUID, NULL::TEXT, staged.media_type,
            NULL::BIGINT, NULL::TIMESTAMPTZ;
        RETURN;
    END IF;
    SELECT * INTO STRICT object FROM blob.blob_object AS candidate
    WHERE candidate.stage_id = p_stage_id;
    IF object.state <> 'committed' OR object.origin_owner <> 'agent_control'
       OR object.origin_record_type <> p_origin_record_type
       OR object.origin_record_id <> p_origin_record_id
       OR object.origin_record_digest <> p_origin_record_digest
       OR object.content_digest <> p_content_digest OR object.size_bytes <> p_size_bytes THEN
        RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'committed Blob origin conflict';
    END IF;
    RETURN QUERY SELECT staged.state, object.blob_id, object.content_digest::TEXT,
        object.media_type, object.size_bytes, object.committed_at;
END
$$;

REVOKE ALL ON FUNCTION blob.reconcile_stage_facts(UUID, TEXT, TEXT, BIGINT, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION blob.resume_agent_control_stage(UUID, TEXT, TEXT, BIGINT, TEXT, TEXT, TEXT, TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION blob.reconcile_stage_facts(UUID, TEXT, TEXT, BIGINT, TEXT)
    TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION blob.resume_agent_control_stage(UUID, TEXT, TEXT, BIGINT, TEXT, TEXT, TEXT, TEXT)
    TO alpheus_agent_control_api;

RESET ROLE;
