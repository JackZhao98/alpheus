-- The initial import established the correct immutable bytes and identities,
-- but its availability time was the migration time. Historical replay must use
-- when the legacy collector first stored a row, not when the new Provider was
-- introduced. Replace only this bootstrap-owned import before any Provider
-- reader can rely on it, then accept the original availability time solely for
-- this named one-way legacy source. Fresh collection still assigns availability
-- inside the Provider.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DROP TRIGGER IF EXISTS research_gexbot_observation_immutable ON research.gexbot_observation;
DELETE FROM blob.blob_reference reference
 WHERE reference.reference_owner='gexbot_provider'
   AND reference.reference_record_type='gexbot_observation'
   AND EXISTS (SELECT 1 FROM research.gexbot_observation observation
                WHERE observation.observation_id::TEXT=reference.reference_record_id
                  AND observation.source_kind='legacy_kernel_import');
DELETE FROM research.gexbot_observation WHERE source_kind='legacy_kernel_import';
CREATE TRIGGER research_gexbot_observation_immutable BEFORE UPDATE OR DELETE ON research.gexbot_observation
  FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION research.record_gexbot_observation(
  p_observation_id UUID,p_source_kind TEXT,p_symbol TEXT,p_category TEXT,p_source_timestamp TIMESTAMPTZ,
  p_observed_at TIMESTAMPTZ,p_fetched_at TIMESTAMPTZ,p_raw_blob_id UUID,p_raw_digest TEXT,p_raw_size BIGINT,
  p_raw_origin_digest TEXT,p_spot NUMERIC,p_zero_gamma NUMERIC,p_major_pos_vol NUMERIC,p_major_pos_oi NUMERIC,
  p_major_neg_vol NUMERIC,p_major_neg_oi NUMERIC,p_legacy_available_at TIMESTAMPTZ
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,research,blob,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; object_row blob.blob_object%ROWTYPE; existing research.gexbot_observation%ROWTYPE;
  now_at TIMESTAMPTZ:=clock_timestamp(); available_at_value TIMESTAMPTZ; body_value JSONB; digest_value CHAR(64); inserted BOOLEAN;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_observation_id IS NULL OR p_source_kind NOT IN ('provider_poll','collector_push','legacy_kernel_import')
     OR p_symbol !~ '^[A-Z0-9._-]{1,16}$' OR p_category NOT IN ('gex_full','gex_zero','gex_one')
     OR p_source_timestamp IS NULL OR p_observed_at IS NULL OR p_fetched_at IS NULL
     OR p_source_timestamp>now_at OR p_observed_at>now_at OR p_fetched_at>now_at OR p_fetched_at<p_observed_at
     OR p_raw_blob_id IS NULL OR p_raw_digest !~ '^[0-9a-f]{64}$' OR p_raw_size NOT BETWEEN 1 AND 2097152
     OR p_raw_origin_digest !~ '^[0-9a-f]{64}$' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT observation'; END IF;
  IF p_source_kind='legacy_kernel_import' THEN
    IF p_legacy_available_at IS NULL OR p_legacy_available_at>now_at OR p_legacy_available_at<p_fetched_at THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid legacy GEXBOT availability'; END IF;
    available_at_value:=p_legacy_available_at;
  ELSIF p_legacy_available_at IS NOT NULL THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='fresh GEXBOT availability is Provider-assigned';
  ELSE
    available_at_value:=now_at;
  END IF;
  SELECT * INTO existing FROM research.gexbot_observation WHERE observation_id=p_observation_id;
  IF FOUND THEN RETURN existing.body || jsonb_build_object('record_digest',existing.record_digest::TEXT); END IF;
  SELECT * INTO STRICT object_row FROM blob.blob_object object JOIN blob.blob_content content ON content.content_digest=object.content_digest
    WHERE object.blob_id=p_raw_blob_id AND object.state='committed' AND content.state='committed' FOR SHARE;
  IF object_row.origin_owner<>'gexbot_provider' OR object_row.origin_record_type<>'gexbot_raw_observation'
     OR object_row.origin_record_id<>p_observation_id::TEXT OR object_row.origin_record_digest<>p_raw_origin_digest
     OR object_row.content_digest<>p_raw_digest OR object_row.size_bytes<>p_raw_size THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT raw Blob provenance denied'; END IF;
  body_value:=research.gexbot_observation_body(p_observation_id,p_source_kind,p_symbol,p_category,p_source_timestamp,p_observed_at,p_fetched_at,available_at_value,now_at,
     p_raw_blob_id,p_raw_digest,p_raw_size,p_spot,p_zero_gamma,p_major_pos_vol,p_major_pos_oi,p_major_neg_vol,p_major_neg_oi);
  digest_value:=encode(sha256(convert_to(body_value::TEXT,'UTF8')),'hex');
  INSERT INTO research.gexbot_observation(observation_id,schema_revision,provider,provider_revision,source_kind,symbol,category,source_timestamp,observed_at,fetched_at,available_at,ingested_at,
      raw_blob_id,raw_content_digest,raw_size_bytes,raw_origin_digest,spot,zero_gamma,major_pos_vol,major_pos_oi,major_neg_vol,major_neg_oi,quality_state,record_digest,body)
    VALUES(p_observation_id,1,'gexbot_classic','gexbot_classic_v1',p_source_kind,p_symbol,p_category,p_source_timestamp,p_observed_at,p_fetched_at,available_at_value,now_at,
      p_raw_blob_id,p_raw_digest,p_raw_size,p_raw_origin_digest,p_spot,p_zero_gamma,p_major_pos_vol,p_major_pos_oi,p_major_neg_vol,p_major_neg_oi,'accepted',digest_value,body_value)
    ON CONFLICT (observation_id) DO NOTHING RETURNING true INTO inserted;
  SELECT * INTO STRICT existing FROM research.gexbot_observation WHERE observation_id=p_observation_id;
  IF NOT coalesce(inserted,false) AND existing.body<>body_value THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='GEXBOT observation identity conflict'; END IF;
  INSERT INTO blob.blob_reference(binding_id,blob_id,reference_owner,reference_record_type,reference_record_id,reference_record_digest,owner_principal,access_class,retention_until,bound_at)
    VALUES('gexbot-observation:'||p_observation_id::TEXT||':raw',p_raw_blob_id,'gexbot_provider','gexbot_observation',p_observation_id::TEXT,digest_value,invoker.principal_id,'private',now_at+interval '5 years',now_at)
    ON CONFLICT (binding_id) DO NOTHING;
  RETURN existing.body || jsonb_build_object('record_digest',existing.record_digest::TEXT);
END $$;
GRANT EXECUTE ON FUNCTION research.record_gexbot_observation(UUID,TEXT,TEXT,TEXT,TIMESTAMPTZ,TIMESTAMPTZ,TIMESTAMPTZ,UUID,TEXT,BIGINT,TEXT,NUMERIC,NUMERIC,NUMERIC,NUMERIC,NUMERIC,NUMERIC,TIMESTAMPTZ) TO alpheus_gexbot_provider;
RESET ROLE;
