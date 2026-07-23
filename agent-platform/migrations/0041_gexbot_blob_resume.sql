-- A committed raw Blob is an idempotency result even after its short staging
-- lease expires. Provider retries must resume it before attempting to stage
-- bytes again; otherwise a crash between Blob commit and Observation commit
-- would make an immutable payload unrecoverable.
SET ROLE alpheus_agent_migrator;
CREATE FUNCTION blob.gexbot_resume_committed_stage(
  p_stage_id UUID,p_principal_id TEXT,p_content_digest TEXT,p_size_bytes BIGINT,
  p_origin_record_type TEXT,p_origin_record_id TEXT,p_origin_record_digest TEXT,p_actor TEXT
) RETURNS TABLE(blob_id UUID,content_digest TEXT,media_type TEXT,size_bytes BIGINT,committed_at TIMESTAMPTZ)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,blob,platform_security AS $$
DECLARE invoker RECORD; staged blob.blob_stage%ROWTYPE; object_row blob.blob_object%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_principal_id IS DISTINCT FROM invoker.principal_id OR p_actor IS DISTINCT FROM invoker.principal_id
     OR p_content_digest !~ '^[0-9a-f]{64}$' OR p_origin_record_type !~ '^[a-z][a-z0-9_]{0,63}$'
     OR p_origin_record_id IS NULL OR p_origin_record_id='' OR p_origin_record_digest !~ '^[0-9a-f]{64}$' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT Blob resume identity denied'; END IF;
  SELECT * INTO staged FROM blob.blob_stage WHERE stage_id=p_stage_id FOR SHARE;
  IF NOT FOUND OR staged.state<>'committed' THEN RETURN; END IF;
  SELECT * INTO STRICT object_row FROM blob.blob_object WHERE stage_id=p_stage_id FOR SHARE;
  IF staged.principal_id<>invoker.principal_id OR staged.issuer_owner<>'gexbot_provider'
     OR staged.content_digest<>p_content_digest OR staged.size_bytes<>p_size_bytes
     OR object_row.origin_owner<>'gexbot_provider' OR object_row.origin_record_type<>p_origin_record_type
     OR object_row.origin_record_id<>p_origin_record_id OR object_row.origin_record_digest<>p_origin_record_digest THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='GEXBOT committed Blob identity conflict'; END IF;
  RETURN QUERY SELECT object_row.blob_id,object_row.content_digest::TEXT,object_row.media_type,object_row.size_bytes,object_row.committed_at;
END $$;
REVOKE ALL ON FUNCTION blob.gexbot_resume_committed_stage(UUID,TEXT,TEXT,BIGINT,TEXT,TEXT,TEXT,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION blob.gexbot_resume_committed_stage(UUID,TEXT,TEXT,BIGINT,TEXT,TEXT,TEXT,TEXT) TO alpheus_gexbot_provider;
RESET ROLE;
