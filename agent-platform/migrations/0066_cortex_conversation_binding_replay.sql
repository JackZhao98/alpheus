-- Session preparation is retried after an already-admitted Run. Reusing the
-- immutable Conversation raw binding must not derive a new retention timestamp
-- and then fail the exact-identity check in blob.bind_reference_internal.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.bind_cortex_conversation_raw_v2(p_request_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,blob,platform_security SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  request_row agent_input.user_request%ROWTYPE;
  existing blob.blob_reference%ROWTYPE;
  policy blob.storage_policy%ROWTYPE;
  binding_id TEXT;
  retention_until TIMESTAMPTZ;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_request_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='conversation raw binding denied';
  END IF;

  SELECT * INTO STRICT request_row
  FROM agent_input.user_request
  WHERE request_id=p_request_id
  FOR SHARE;

  binding_id:='cortex-conversation:'||request_row.request_id||':raw';
  SELECT * INTO existing
  FROM blob.blob_reference
  WHERE blob_reference.binding_id=binding_id
  FOR SHARE;

  IF FOUND THEN
    IF existing.blob_id<>(request_row.raw_input->>'blob_id')::UUID
       OR existing.reference_owner<>'agent_control'
       OR existing.reference_record_type<>'user_request'
       OR existing.reference_record_id<>request_row.request_id
       OR existing.reference_record_digest<>request_row.record_digest
       OR existing.owner_principal<>invoker.principal_id
       OR existing.access_class<>'private'
       OR existing.state<>'active'
       OR existing.retention_until<=clock_timestamp() THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='conversation raw binding identity conflict';
    END IF;
    RETURN jsonb_build_object('status','bound','binding_id',binding_id,'replayed',true);
  END IF;

  SELECT * INTO STRICT policy FROM blob.storage_policy WHERE singleton;
  retention_until:=clock_timestamp()+make_interval(secs=>policy.max_retention_seconds::DOUBLE PRECISION);
  PERFORM blob.bind_reference_internal(
    'agent_control',binding_id,(request_row.raw_input->>'blob_id')::UUID,
    'user_request',request_row.request_id,request_row.record_digest::TEXT,
    invoker.principal_id,'private',retention_until,invoker.principal_id
  );
  RETURN jsonb_build_object('status','bound','binding_id',binding_id,'replayed',false);
END
$$;

REVOKE ALL ON FUNCTION agent_control.bind_cortex_conversation_raw_v2(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.bind_cortex_conversation_raw_v2(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
