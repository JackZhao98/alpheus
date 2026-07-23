-- GEXBOT becomes the first independently owned Research Plane Provider.  The
-- Provider is an append-only collector/archive/replay service; Research
-- Gateway is its separate consumer-facing evidence boundary and Cortex never
-- receives the connector credential or raw payload.
SET TIME ZONE 'UTC';

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='alpheus_gexbot_provider') THEN
    CREATE ROLE alpheus_gexbot_provider NOLOGIN;
  END IF;
  ALTER ROLE alpheus_gexbot_provider NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS;
END $$;

CREATE SCHEMA IF NOT EXISTS research AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA research FROM PUBLIC;

SET ROLE alpheus_agent_migrator;

-- The existing generic Blob entrypoints intentionally recognize only their
-- original owners.  GEXBOT receives three narrow, parallel entrypoints
-- instead of widening the Research Gateway's credential to a second writer.
CREATE FUNCTION platform_security.gexbot_provider_identity()
RETURNS TABLE (principal_id TEXT, profile_id TEXT, group_role NAME, owner_id TEXT)
LANGUAGE plpgsql STABLE SECURITY DEFINER
SET search_path=pg_catalog,platform_security AS $$
DECLARE safe_login BOOLEAN:=false; memberships INTEGER:=0; has_admin BOOLEAN:=false;
BEGIN
  SELECT role.rolcanlogin AND NOT role.rolsuper AND NOT role.rolcreatedb
     AND NOT role.rolcreaterole AND NOT role.rolreplication AND NOT role.rolbypassrls
    INTO safe_login FROM pg_catalog.pg_roles role WHERE role.rolname=session_user;
  IF NOT coalesce(safe_login,false) OR session_user::TEXT='' OR session_user::TEXT~'[[:space:][:cntrl:]]'
     OR pg_catalog.pg_has_role(session_user,'alpheus_agent_migrator','MEMBER') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='invalid GEXBOT Provider login identity';
  END IF;
  SELECT count(*)::INTEGER,coalesce(bool_or(member.admin_option),false)
    INTO memberships,has_admin
    FROM pg_catalog.pg_auth_members member
    JOIN pg_catalog.pg_roles granted ON granted.oid=member.roleid
    JOIN pg_catalog.pg_roles login ON login.oid=member.member
   WHERE login.rolname=session_user AND granted.rolname IN (
      'alpheus_agent_control_api','alpheus_agent_worker','alpheus_agent_delivery_dispatcher',
      'alpheus_agent_delivery_repair','alpheus_agent_validator','alpheus_agent_activator',
      'alpheus_platform_owner','alpheus_platform_halt','alpheus_research_gateway',
      'alpheus_gexbot_provider','alpheus_grace_intake','alpheus_grace_engine',
      'alpheus_delegation_engine','alpheus_agent_web','alpheus_agent_diagnostics',
      'alpheus_blob_gc','alpheus_blob_diagnostics');
  IF memberships<>1 OR has_admin OR NOT pg_catalog.pg_has_role(session_user,'alpheus_gexbot_provider','MEMBER') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT Provider login must have exactly one direct application group';
  END IF;
  principal_id:=session_user::TEXT; profile_id:='gexbot-provider'; group_role:='alpheus_gexbot_provider'; owner_id:='gexbot_provider';
  RETURN NEXT;
END $$;
REVOKE ALL ON FUNCTION platform_security.gexbot_provider_identity() FROM PUBLIC;

CREATE FUNCTION blob.gexbot_begin_stage(
  p_stage_id UUID,p_principal_id TEXT,p_media_type TEXT,p_requested_max_bytes BIGINT,
  p_expected_digest TEXT,p_expected_size_bytes BIGINT,p_ttl_seconds INTEGER,p_actor TEXT
) RETURNS TABLE(stage_id UUID,max_bytes BIGINT,issued_at TIMESTAMPTZ,expires_at TIMESTAMPTZ)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,blob,platform_security AS $$
DECLARE policy blob.storage_policy%ROWTYPE; existing blob.blob_stage%ROWTYPE; inserted BOOLEAN;
  created TIMESTAMPTZ:=clock_timestamp(); invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_principal_id IS DISTINCT FROM invoker.principal_id OR p_actor IS DISTINCT FROM invoker.principal_id THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT blob stage identity denied';
  END IF;
  SELECT * INTO STRICT policy FROM blob.storage_policy WHERE singleton;
  IF p_stage_id IS NULL OR p_media_type IS NULL OR p_media_type<>lower(p_media_type)
     OR NOT p_media_type=ANY(policy.allowed_media_types) OR p_requested_max_bytes<1 OR p_requested_max_bytes>policy.max_blob_bytes
     OR p_ttl_seconds<1 OR p_ttl_seconds>policy.stage_ttl_seconds
     OR (p_expected_digest IS NOT NULL AND p_expected_digest !~ '^[0-9a-f]{64}$')
     OR (p_expected_size_bytes IS NOT NULL AND (p_expected_size_bytes<1 OR p_expected_size_bytes>p_requested_max_bytes)) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT blob stage request';
  END IF;
  INSERT INTO blob.blob_stage(stage_id,principal_id,issuer_owner,media_type,max_bytes_snapshot,expected_digest,expected_size_bytes,created_at,expires_at)
    VALUES(p_stage_id,invoker.principal_id,'gexbot_provider',p_media_type,p_requested_max_bytes,p_expected_digest,p_expected_size_bytes,created,created+make_interval(secs=>p_ttl_seconds))
    ON CONFLICT ON CONSTRAINT blob_stage_pkey DO NOTHING RETURNING true INTO inserted;
  SELECT * INTO STRICT existing FROM blob.blob_stage WHERE blob_stage.stage_id=p_stage_id;
  IF NOT coalesce(inserted,false) AND NOT (existing.principal_id=invoker.principal_id AND existing.issuer_owner='gexbot_provider'
      AND existing.media_type=p_media_type AND existing.max_bytes_snapshot=p_requested_max_bytes
      AND existing.expected_digest IS NOT DISTINCT FROM p_expected_digest AND existing.expected_size_bytes IS NOT DISTINCT FROM p_expected_size_bytes
      AND existing.state IN ('open','materialized','committed')) THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='GEXBOT blob stage identity conflict';
  END IF;
  IF inserted THEN INSERT INTO blob.lifecycle_event(subject_kind,subject_id,transition,generation,actor,reason_code)
    VALUES('stage',p_stage_id::TEXT,'staged',1,invoker.principal_id,'gexbot_stage_opened'); END IF;
  RETURN QUERY SELECT existing.stage_id,existing.max_bytes_snapshot,existing.created_at,existing.expires_at;
END $$;

CREATE FUNCTION blob.gexbot_record_stage_facts(p_stage_id UUID,p_principal_id TEXT,p_content_digest TEXT,p_size_bytes BIGINT,p_actor TEXT)
RETURNS BOOLEAN LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,blob,platform_security AS $$
DECLARE staged blob.blob_stage%ROWTYPE; content_ready BOOLEAN; now_at TIMESTAMPTZ:=clock_timestamp(); invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_principal_id IS DISTINCT FROM invoker.principal_id OR p_actor IS DISTINCT FROM invoker.principal_id THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT staged blob identity denied'; END IF;
  IF p_content_digest IS NULL OR p_content_digest !~ '^[0-9a-f]{64}$' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT staged blob facts'; END IF;
  SELECT * INTO STRICT staged FROM blob.blob_stage candidate WHERE candidate.stage_id=p_stage_id FOR UPDATE;
  IF staged.state='materialized' THEN
    IF staged.principal_id<>invoker.principal_id OR staged.issuer_owner<>'gexbot_provider' OR staged.content_digest<>p_content_digest OR staged.size_bytes<>p_size_bytes THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='GEXBOT staged blob facts conflict'; END IF;
    RETURN false;
  END IF;
  IF staged.state<>'open' OR staged.expires_at<=now_at OR staged.principal_id<>invoker.principal_id OR staged.issuer_owner<>'gexbot_provider'
     OR p_size_bytes<1 OR p_size_bytes>staged.max_bytes_snapshot
     OR (staged.expected_digest IS NOT NULL AND staged.expected_digest<>p_content_digest)
     OR (staged.expected_size_bytes IS NOT NULL AND staged.expected_size_bytes<>p_size_bytes) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='GEXBOT staged blob facts violate grant'; END IF;
  INSERT INTO blob.blob_content(content_digest,size_bytes,state,created_at,updated_at)
    VALUES(p_content_digest,p_size_bytes,'staged',now_at,now_at)
    ON CONFLICT ON CONSTRAINT blob_content_pkey DO UPDATE SET
      state=CASE WHEN blob.blob_content.state='deleted' THEN 'staged' ELSE blob.blob_content.state END,
      updated_at=CASE WHEN blob.blob_content.state IN ('staged','deleted') THEN now_at ELSE blob.blob_content.updated_at END,
      gc_token=NULL,gc_expires_at=NULL,quarantine_reason=NULL
    WHERE blob.blob_content.size_bytes=EXCLUDED.size_bytes AND blob.blob_content.state IN ('staged','committed','deleted')
    RETURNING true INTO content_ready;
  IF NOT coalesce(content_ready,false) THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='GEXBOT blob content unavailable or conflicting'; END IF;
  UPDATE blob.blob_stage SET state='materialized',content_digest=p_content_digest,size_bytes=p_size_bytes WHERE stage_id=p_stage_id;
  RETURN true;
END $$;

CREATE FUNCTION blob.gexbot_commit_stage(
  p_stage_id UUID,p_principal_id TEXT,p_content_digest TEXT,p_size_bytes BIGINT,
  p_origin_record_type TEXT,p_origin_record_id TEXT,p_origin_record_digest TEXT,p_actor TEXT
) RETURNS TABLE(blob_id UUID,content_digest TEXT,media_type TEXT,size_bytes BIGINT,committed_at TIMESTAMPTZ)
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,blob,platform_security AS $$
DECLARE staged blob.blob_stage%ROWTYPE; existing blob.blob_object%ROWTYPE; new_blob_id UUID;
  committed TIMESTAMPTZ:=clock_timestamp(); content_ready BOOLEAN; invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_principal_id IS DISTINCT FROM invoker.principal_id OR p_actor IS DISTINCT FROM invoker.principal_id THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT blob commit identity denied'; END IF;
  IF p_content_digest IS NULL OR p_content_digest !~ '^[0-9a-f]{64}$' OR p_origin_record_type !~ '^[a-z][a-z0-9_]{0,63}$'
     OR p_origin_record_id IS NULL OR p_origin_record_id='' OR p_origin_record_id~'[[:space:][:cntrl:]]'
     OR p_origin_record_digest IS NULL OR p_origin_record_digest !~ '^[0-9a-f]{64}$' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT blob commit'; END IF;
  SELECT * INTO STRICT staged FROM blob.blob_stage candidate WHERE candidate.stage_id=p_stage_id FOR UPDATE;
  IF staged.state='committed' THEN
    SELECT * INTO STRICT existing FROM blob.blob_object object WHERE object.stage_id=p_stage_id;
    IF staged.principal_id<>invoker.principal_id OR staged.issuer_owner<>'gexbot_provider' OR staged.content_digest<>p_content_digest
       OR staged.size_bytes<>p_size_bytes OR existing.origin_owner<>'gexbot_provider' OR existing.origin_record_type<>p_origin_record_type
       OR existing.origin_record_id<>p_origin_record_id OR existing.origin_record_digest<>p_origin_record_digest THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='GEXBOT blob commit identity conflict'; END IF;
    RETURN QUERY SELECT existing.blob_id,existing.content_digest::TEXT,existing.media_type,existing.size_bytes,existing.committed_at; RETURN;
  END IF;
  IF staged.state<>'materialized' OR staged.expires_at<=committed OR staged.principal_id<>invoker.principal_id OR staged.issuer_owner<>'gexbot_provider'
     OR staged.content_digest<>p_content_digest OR staged.size_bytes<>p_size_bytes THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='GEXBOT blob stage cannot commit'; END IF;
  INSERT INTO blob.blob_content(content_digest,size_bytes,state,created_at,updated_at)
    VALUES(p_content_digest,p_size_bytes,'committed',committed,committed)
    ON CONFLICT ON CONSTRAINT blob_content_pkey DO UPDATE SET state='committed',updated_at=committed,gc_token=NULL,gc_expires_at=NULL,quarantine_reason=NULL
      WHERE blob.blob_content.size_bytes=EXCLUDED.size_bytes AND blob.blob_content.state IN ('staged','committed') RETURNING true INTO content_ready;
  IF NOT coalesce(content_ready,false) THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='GEXBOT blob content unavailable or conflicting'; END IF;
  new_blob_id:=gen_random_uuid();
  INSERT INTO blob.blob_object(blob_id,stage_id,content_digest,media_type,size_bytes,origin_owner,origin_record_type,origin_record_id,origin_record_digest,state,committed_at)
    VALUES(new_blob_id,p_stage_id,p_content_digest,staged.media_type,p_size_bytes,'gexbot_provider',p_origin_record_type,p_origin_record_id,p_origin_record_digest,'committed',committed);
  UPDATE blob.blob_stage SET state='committed',content_digest=p_content_digest,size_bytes=p_size_bytes,blob_id=new_blob_id,committed_at=committed WHERE stage_id=p_stage_id;
  INSERT INTO blob.lifecycle_event(subject_kind,subject_id,transition,generation,actor,occurred_at,reason_code,details)
    VALUES('blob',new_blob_id::TEXT,'committed',1,invoker.principal_id,committed,'gexbot_stage_committed',jsonb_build_object('stage_id',p_stage_id,'content_digest',p_content_digest));
  RETURN QUERY SELECT new_blob_id,p_content_digest,staged.media_type,p_size_bytes,committed;
END $$;

ALTER TABLE blob.blob_stage DROP CONSTRAINT IF EXISTS blob_stage_issuer_owner_check;
ALTER TABLE blob.blob_stage ADD CONSTRAINT blob_stage_issuer_owner_check CHECK (issuer_owner IN ('agent_control','research_gateway','gexbot_provider'));
ALTER TABLE blob.blob_object DROP CONSTRAINT IF EXISTS blob_object_origin_owner_check;
ALTER TABLE blob.blob_object ADD CONSTRAINT blob_object_origin_owner_check CHECK (origin_owner IN ('agent_control','blob','delegation','grace','kernel','platform_governance','research_gateway','gexbot_provider','worker'));

CREATE TABLE research.gexbot_observation (
  observation_id UUID PRIMARY KEY,
  schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
  provider TEXT NOT NULL CHECK (provider='gexbot_classic'),
  provider_revision TEXT NOT NULL CHECK (provider_revision='gexbot_classic_v1'),
  source_kind TEXT NOT NULL CHECK (source_kind IN ('provider_poll','collector_push','legacy_kernel_import')),
  symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9._-]{1,16}$'),
  category TEXT NOT NULL CHECK (category IN ('gex_full','gex_zero','gex_one')),
  source_timestamp TIMESTAMPTZ NOT NULL,
  observed_at TIMESTAMPTZ NOT NULL,
  fetched_at TIMESTAMPTZ NOT NULL,
  available_at TIMESTAMPTZ NOT NULL,
  ingested_at TIMESTAMPTZ NOT NULL,
  raw_blob_id UUID NOT NULL UNIQUE REFERENCES blob.blob_object(blob_id),
  raw_content_digest CHAR(64) NOT NULL CHECK (raw_content_digest ~ '^[0-9a-f]{64}$'),
  raw_size_bytes BIGINT NOT NULL CHECK (raw_size_bytes BETWEEN 1 AND 2097152),
  raw_origin_digest CHAR(64) NOT NULL CHECK (raw_origin_digest ~ '^[0-9a-f]{64}$'),
  spot NUMERIC,
  zero_gamma NUMERIC,
  major_pos_vol NUMERIC,
  major_pos_oi NUMERIC,
  major_neg_vol NUMERIC,
  major_neg_oi NUMERIC,
  quality_state TEXT NOT NULL CHECK (quality_state='accepted'),
  record_digest CHAR(64) NOT NULL UNIQUE CHECK (record_digest ~ '^[0-9a-f]{64}$'),
  body JSONB NOT NULL CHECK (jsonb_typeof(body)='object'),
  UNIQUE(source_kind,symbol,category,observed_at,raw_content_digest),
  CHECK (fetched_at>=observed_at AND available_at>=fetched_at AND ingested_at>=available_at)
);
CREATE INDEX gexbot_observation_as_of_idx ON research.gexbot_observation(symbol,category,available_at DESC,observed_at DESC,observation_id DESC);
CREATE TRIGGER research_gexbot_observation_immutable BEFORE UPDATE OR DELETE ON research.gexbot_observation
  FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE research.gexbot_replay_session (
  replay_id UUID PRIMARY KEY,
  schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
  principal_id TEXT NOT NULL CHECK (principal_id ~ '^[A-Za-z0-9._-]{1,200}$'),
  request_digest CHAR(64) NOT NULL UNIQUE CHECK (request_digest ~ '^[0-9a-f]{64}$'),
  symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9._-]{1,16}$'),
  category TEXT NOT NULL CHECK (category IN ('gex_full','gex_zero','gex_one')),
  start_available_at TIMESTAMPTZ NOT NULL,
  end_available_at TIMESTAMPTZ NOT NULL,
  as_of TIMESTAMPTZ NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('active','complete')),
  generation BIGINT NOT NULL CHECK (generation>0),
  cursor_available_at TIMESTAMPTZ,
  cursor_observation_id UUID,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  completed_at TIMESTAMPTZ,
  CHECK (end_available_at>=start_available_at AND as_of>=end_available_at AND updated_at>=created_at),
  CHECK ((cursor_available_at IS NULL AND cursor_observation_id IS NULL) OR (cursor_available_at IS NOT NULL AND cursor_observation_id IS NOT NULL)),
  CHECK ((state='active' AND completed_at IS NULL) OR (state='complete' AND completed_at IS NOT NULL))
);

CREATE FUNCTION research.gexbot_observation_body(
  p_observation_id UUID,p_source_kind TEXT,p_symbol TEXT,p_category TEXT,p_source_timestamp TIMESTAMPTZ,
  p_observed_at TIMESTAMPTZ,p_fetched_at TIMESTAMPTZ,p_available_at TIMESTAMPTZ,p_ingested_at TIMESTAMPTZ,
  p_raw_blob_id UUID,p_raw_digest TEXT,p_raw_size BIGINT,p_spot NUMERIC,p_zero_gamma NUMERIC,
  p_major_pos_vol NUMERIC,p_major_pos_oi NUMERIC,p_major_neg_vol NUMERIC,p_major_neg_oi NUMERIC
) RETURNS JSONB LANGUAGE sql IMMUTABLE AS $$
  SELECT jsonb_build_object('schema_revision',1,'observation_id',p_observation_id::TEXT,
    'provider','gexbot_classic','provider_revision','gexbot_classic_v1','source_kind',p_source_kind,
    'symbol',p_symbol,'category',p_category,'source_timestamp',to_char(p_source_timestamp AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    'observed_at',to_char(p_observed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    'fetched_at',to_char(p_fetched_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    'available_at',to_char(p_available_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    'ingested_at',to_char(p_ingested_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),
    'raw',jsonb_build_object('blob_id',p_raw_blob_id::TEXT,'content_digest',p_raw_digest,'size_bytes',p_raw_size),
    'metrics',jsonb_strip_nulls(jsonb_build_object('spot',p_spot,'zero_gamma',p_zero_gamma,'major_pos_vol',p_major_pos_vol,'major_pos_oi',p_major_pos_oi,'major_neg_vol',p_major_neg_vol,'major_neg_oi',p_major_neg_oi)),
    'quality_state','accepted');
$$;

CREATE FUNCTION research.record_gexbot_observation(
  p_observation_id UUID,p_source_kind TEXT,p_symbol TEXT,p_category TEXT,p_source_timestamp TIMESTAMPTZ,
  p_observed_at TIMESTAMPTZ,p_fetched_at TIMESTAMPTZ,p_raw_blob_id UUID,p_raw_digest TEXT,p_raw_size BIGINT,
  p_raw_origin_digest TEXT,p_spot NUMERIC,p_zero_gamma NUMERIC,p_major_pos_vol NUMERIC,p_major_pos_oi NUMERIC,
  p_major_neg_vol NUMERIC,p_major_neg_oi NUMERIC
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,research,blob,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; object_row blob.blob_object%ROWTYPE; existing research.gexbot_observation%ROWTYPE;
  now_at TIMESTAMPTZ:=clock_timestamp(); body_value JSONB; digest_value CHAR(64); inserted BOOLEAN;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_observation_id IS NULL OR p_source_kind NOT IN ('provider_poll','collector_push','legacy_kernel_import')
     OR p_symbol !~ '^[A-Z0-9._-]{1,16}$' OR p_category NOT IN ('gex_full','gex_zero','gex_one')
     OR p_source_timestamp IS NULL OR p_observed_at IS NULL OR p_fetched_at IS NULL
     OR p_source_timestamp>now_at OR p_observed_at>now_at OR p_fetched_at>now_at OR p_fetched_at<p_observed_at
     OR p_raw_blob_id IS NULL OR p_raw_digest !~ '^[0-9a-f]{64}$' OR p_raw_size NOT BETWEEN 1 AND 2097152
     OR p_raw_origin_digest !~ '^[0-9a-f]{64}$' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT observation'; END IF;
  SELECT * INTO existing FROM research.gexbot_observation WHERE observation_id=p_observation_id;
  IF FOUND THEN
    RETURN existing.body || jsonb_build_object('record_digest',existing.record_digest::TEXT);
  END IF;
  SELECT * INTO STRICT object_row FROM blob.blob_object object JOIN blob.blob_content content ON content.content_digest=object.content_digest
    WHERE object.blob_id=p_raw_blob_id AND object.state='committed' AND content.state='committed' FOR SHARE;
  IF object_row.origin_owner<>'gexbot_provider' OR object_row.origin_record_type<>'gexbot_raw_observation'
     OR object_row.origin_record_id<>p_observation_id::TEXT OR object_row.origin_record_digest<>p_raw_origin_digest
     OR object_row.content_digest<>p_raw_digest OR object_row.size_bytes<>p_raw_size THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT raw Blob provenance denied'; END IF;
  body_value:=research.gexbot_observation_body(p_observation_id,p_source_kind,p_symbol,p_category,p_source_timestamp,p_observed_at,p_fetched_at,now_at,now_at,
     p_raw_blob_id,p_raw_digest,p_raw_size,p_spot,p_zero_gamma,p_major_pos_vol,p_major_pos_oi,p_major_neg_vol,p_major_neg_oi);
  digest_value:=encode(sha256(convert_to(body_value::TEXT,'UTF8')),'hex');
  INSERT INTO research.gexbot_observation(observation_id,schema_revision,provider,provider_revision,source_kind,symbol,category,source_timestamp,observed_at,fetched_at,available_at,ingested_at,
      raw_blob_id,raw_content_digest,raw_size_bytes,raw_origin_digest,spot,zero_gamma,major_pos_vol,major_pos_oi,major_neg_vol,major_neg_oi,quality_state,record_digest,body)
    VALUES(p_observation_id,1,'gexbot_classic','gexbot_classic_v1',p_source_kind,p_symbol,p_category,p_source_timestamp,p_observed_at,p_fetched_at,now_at,now_at,
      p_raw_blob_id,p_raw_digest,p_raw_size,p_raw_origin_digest,p_spot,p_zero_gamma,p_major_pos_vol,p_major_pos_oi,p_major_neg_vol,p_major_neg_oi,'accepted',digest_value,body_value)
    ON CONFLICT (observation_id) DO NOTHING RETURNING true INTO inserted;
  SELECT * INTO STRICT existing FROM research.gexbot_observation WHERE observation_id=p_observation_id;
  IF NOT coalesce(inserted,false) AND existing.body<>body_value THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='GEXBOT observation identity conflict'; END IF;
  INSERT INTO blob.blob_reference(binding_id,blob_id,reference_owner,reference_record_type,reference_record_id,reference_record_digest,owner_principal,access_class,retention_until,bound_at)
    VALUES('gexbot-observation:'||p_observation_id::TEXT||':raw',p_raw_blob_id,'gexbot_provider','gexbot_observation',p_observation_id::TEXT,digest_value,invoker.principal_id,'private',now_at+interval '5 years',now_at)
    ON CONFLICT (binding_id) DO NOTHING;
  RETURN existing.body || jsonb_build_object('record_digest',existing.record_digest::TEXT);
END $$;

CREATE FUNCTION research.gexbot_as_of(p_symbol TEXT,p_category TEXT,p_as_of TIMESTAMPTZ)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,research,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; result research.gexbot_observation%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_symbol !~ '^[A-Z0-9._-]{1,16}$' OR p_category NOT IN ('gex_full','gex_zero','gex_one') OR p_as_of IS NULL OR p_as_of>clock_timestamp() THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT as_of query'; END IF;
  SELECT * INTO result FROM research.gexbot_observation WHERE symbol=p_symbol AND category=p_category AND available_at<=p_as_of
    ORDER BY available_at DESC,observed_at DESC,observation_id DESC LIMIT 1;
  IF NOT FOUND THEN RETURN jsonb_build_object('available',false,'symbol',p_symbol,'category',p_category,'as_of',to_char(p_as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"')); END IF;
  RETURN result.body || jsonb_build_object('record_digest',result.record_digest::TEXT);
END $$;

CREATE FUNCTION research.create_gexbot_replay(p_replay_id UUID,p_request_digest TEXT,p_symbol TEXT,p_category TEXT,p_start TIMESTAMPTZ,p_end TIMESTAMPTZ,p_as_of TIMESTAMPTZ)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,research,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; existing research.gexbot_replay_session%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp(); inserted BOOLEAN;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_replay_id IS NULL OR p_request_digest !~ '^[0-9a-f]{64}$' OR p_symbol !~ '^[A-Z0-9._-]{1,16}$'
     OR p_category NOT IN ('gex_full','gex_zero','gex_one') OR p_start IS NULL OR p_end IS NULL OR p_as_of IS NULL
     OR p_end<p_start OR p_as_of<p_end OR p_as_of>at_time THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT replay'; END IF;
  INSERT INTO research.gexbot_replay_session(replay_id,schema_revision,principal_id,request_digest,symbol,category,start_available_at,end_available_at,as_of,state,generation,created_at,updated_at)
    VALUES(p_replay_id,1,invoker.principal_id,p_request_digest,p_symbol,p_category,p_start,p_end,p_as_of,'active',1,at_time,at_time)
    ON CONFLICT (request_digest) DO NOTHING RETURNING true INTO inserted;
  SELECT * INTO STRICT existing FROM research.gexbot_replay_session WHERE request_digest=p_request_digest;
  IF NOT coalesce(inserted,false) AND (existing.principal_id<>invoker.principal_id OR existing.symbol<>p_symbol OR existing.category<>p_category OR existing.start_available_at<>p_start OR existing.end_available_at<>p_end OR existing.as_of<>p_as_of) THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='GEXBOT replay request identity conflict'; END IF;
  RETURN jsonb_build_object('schema_revision',1,'replay_id',existing.replay_id::TEXT,'state',existing.state,'generation',existing.generation,'symbol',existing.symbol,'category',existing.category,
    'start_available_at',to_char(existing.start_available_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'end_available_at',to_char(existing.end_available_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'),'as_of',to_char(existing.as_of AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"'));
END $$;

CREATE FUNCTION research.consume_gexbot_replay(p_replay_id UUID,p_expected_generation BIGINT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,research,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; session research.gexbot_replay_session%ROWTYPE; next_row research.gexbot_observation%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp(); next_generation BIGINT;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.gexbot_provider_identity();
  IF p_replay_id IS NULL OR p_expected_generation<1 THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT replay cursor'; END IF;
  SELECT * INTO session FROM research.gexbot_replay_session WHERE replay_id=p_replay_id AND principal_id=invoker.principal_id FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='GEXBOT replay access denied'; END IF;
  IF session.generation<>p_expected_generation THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='stale GEXBOT replay generation'; END IF;
  IF session.state='complete' THEN RETURN jsonb_build_object('schema_revision',1,'replay_id',session.replay_id::TEXT,'state','complete','generation',session.generation,'observation',NULL); END IF;
  SELECT * INTO next_row FROM research.gexbot_observation
   WHERE symbol=session.symbol AND category=session.category AND available_at>=session.start_available_at AND available_at<=session.end_available_at AND available_at<=session.as_of
     AND (session.cursor_available_at IS NULL OR (available_at,observation_id)>(session.cursor_available_at,session.cursor_observation_id))
   ORDER BY available_at,observation_id LIMIT 1;
  next_generation:=session.generation+1;
  IF NOT FOUND THEN
    UPDATE research.gexbot_replay_session SET state='complete',generation=next_generation,updated_at=at_time,completed_at=at_time WHERE replay_id=session.replay_id;
    RETURN jsonb_build_object('schema_revision',1,'replay_id',session.replay_id::TEXT,'state','complete','generation',next_generation,'observation',NULL);
  END IF;
  UPDATE research.gexbot_replay_session SET generation=next_generation,cursor_available_at=next_row.available_at,cursor_observation_id=next_row.observation_id,updated_at=at_time WHERE replay_id=session.replay_id;
  RETURN jsonb_build_object('schema_revision',1,'replay_id',session.replay_id::TEXT,'state','active','generation',next_generation,'observation',next_row.body || jsonb_build_object('record_digest',next_row.record_digest::TEXT));
END $$;

REVOKE ALL ON ALL TABLES IN SCHEMA research FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA research FROM PUBLIC;
REVOKE ALL ON FUNCTION blob.gexbot_begin_stage(UUID,TEXT,TEXT,BIGINT,TEXT,BIGINT,INTEGER,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION blob.gexbot_record_stage_facts(UUID,TEXT,TEXT,BIGINT,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION blob.gexbot_commit_stage(UUID,TEXT,TEXT,BIGINT,TEXT,TEXT,TEXT,TEXT) FROM PUBLIC;
GRANT USAGE ON SCHEMA research,blob TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION blob.gexbot_begin_stage(UUID,TEXT,TEXT,BIGINT,TEXT,BIGINT,INTEGER,TEXT) TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION blob.gexbot_record_stage_facts(UUID,TEXT,TEXT,BIGINT,TEXT) TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION blob.gexbot_commit_stage(UUID,TEXT,TEXT,BIGINT,TEXT,TEXT,TEXT,TEXT) TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION research.record_gexbot_observation(UUID,TEXT,TEXT,TEXT,TIMESTAMPTZ,TIMESTAMPTZ,TIMESTAMPTZ,UUID,TEXT,BIGINT,TEXT,NUMERIC,NUMERIC,NUMERIC,NUMERIC,NUMERIC,NUMERIC) TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION research.gexbot_as_of(TEXT,TEXT,TIMESTAMPTZ) TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION research.create_gexbot_replay(UUID,TEXT,TEXT,TEXT,TIMESTAMPTZ,TIMESTAMPTZ,TIMESTAMPTZ) TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION research.consume_gexbot_replay(UUID,BIGINT) TO alpheus_gexbot_provider;
REVOKE INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER ON ALL TABLES IN SCHEMA research FROM alpheus_gexbot_provider;

RESET ROLE;
