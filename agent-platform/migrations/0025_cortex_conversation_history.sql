-- A Conversation is durable user state, but a Worker never receives an
-- unbounded transcript. Control alone materializes the last bounded set of
-- completed exchanges into the new Session's immutable context manifest.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.bind_cortex_conversation_raw(p_request_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,blob,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; request_row agent_input.user_request%ROWTYPE; policy blob.storage_policy%ROWTYPE;
  binding_id TEXT; retention_until TIMESTAMPTZ;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_request_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='conversation raw binding denied';
  END IF;
  SELECT * INTO STRICT request_row FROM agent_input.user_request WHERE request_id=p_request_id FOR SHARE;
  SELECT * INTO STRICT policy FROM blob.storage_policy WHERE singleton;
  binding_id:='cortex-conversation:'||request_row.request_id||':raw';
  retention_until:=clock_timestamp()+make_interval(secs=>policy.max_retention_seconds::DOUBLE PRECISION);
  PERFORM blob.bind_reference_internal('agent_control',binding_id,(request_row.raw_input->>'blob_id')::UUID,
    'user_request',request_row.request_id,request_row.record_digest::TEXT,invoker.principal_id,'private',retention_until,invoker.principal_id);
  RETURN jsonb_build_object('status','bound','binding_id',binding_id);
END $$;

CREATE FUNCTION agent_control.get_cortex_conversation_history(
  p_conversation_id TEXT,p_subject_principal_id TEXT,p_exclude_request_id TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,blob,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; conversation_row agent_input.conversation%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_conversation_id)
     OR NOT agent_control.runtime_identifier_valid(p_subject_principal_id)
     OR (p_exclude_request_id IS NOT NULL AND NOT agent_control.runtime_identifier_valid(p_exclude_request_id)) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='conversation history read denied';
  END IF;
  SELECT * INTO STRICT conversation_row FROM agent_input.conversation
    WHERE conversation_id=p_conversation_id FOR SHARE;
  IF conversation_row.subject_principal_id<>p_subject_principal_id THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='conversation subject mismatch';
  END IF;
  RETURN COALESCE((
    SELECT jsonb_agg(item.body ORDER BY item.created_at,item.request_id)
    FROM (
      SELECT selected.request_id,selected.created_at,jsonb_build_object(
        'request_id',selected.request_id,'kind',selected.request_kind,
        'created_at',agent_control.runtime_utc_text(selected.created_at),
        'request',jsonb_build_object('owner','agent_control','record_type','user_request','record_id',selected.request_id,
          'schema_revision',1,'record_digest',selected.request_digest::TEXT),
        'raw_input',selected.raw_input,'run_id',selected.run_id,
        'artifact',jsonb_build_object('owner','agent_control','record_type','artifact','record_id',selected.artifact_id,
          'schema_revision',1,'record_digest',selected.artifact_digest::TEXT),
        'response',selected.response
      ) AS body
      FROM (
        SELECT request_row.request_id,request_row.request_kind,request_row.created_at,request_row.record_digest AS request_digest,
          request_row.raw_input,resolved.run_id,resolved.artifact_id,resolved.artifact_digest,resolved.response
        FROM agent_input.user_request request_row
        JOIN LATERAL (
          SELECT run.run_id,artifact.artifact_id,artifact.record_digest AS artifact_digest,section.content AS response
          FROM agent_control.runtime_run run
          JOIN agent_control.runtime_artifact artifact ON artifact.run_id=run.run_id
          JOIN agent_control.runtime_artifact_section section ON section.artifact_id=artifact.artifact_id
          WHERE run.origin_kind='user_request' AND run.origin_source_record_type='user_request'
            AND run.origin_source_record_id=request_row.request_id
            AND run.origin_source_record_digest=request_row.record_digest
            AND run.state='succeeded' AND section.name='response' AND section.required
          ORDER BY artifact.created_at DESC,artifact.artifact_id DESC LIMIT 1
        ) resolved ON true
        WHERE request_row.conversation_id=conversation_row.conversation_id
          AND request_row.conversation_digest=conversation_row.record_digest
          AND request_row.subject_principal_id=conversation_row.subject_principal_id
          AND (p_exclude_request_id IS NULL OR request_row.request_id<>p_exclude_request_id)
          AND EXISTS (SELECT 1 FROM blob.blob_reference raw_binding
            WHERE raw_binding.binding_id='cortex-conversation:'||request_row.request_id||':raw'
              AND raw_binding.blob_id=(request_row.raw_input->>'blob_id')::UUID
              AND raw_binding.reference_owner='agent_control' AND raw_binding.reference_record_type='user_request'
              AND raw_binding.reference_record_id=request_row.request_id
              AND raw_binding.reference_record_digest=request_row.record_digest
              AND raw_binding.owner_principal=invoker.principal_id AND raw_binding.state='active'
              AND raw_binding.retention_until>clock_timestamp())
          AND EXISTS (SELECT 1 FROM blob.blob_reference artifact_binding
            WHERE artifact_binding.binding_id='artifact:'||resolved.artifact_id||':blob:'||(resolved.response->>'blob_id')
              AND artifact_binding.blob_id=(resolved.response->>'blob_id')::UUID
              AND artifact_binding.reference_owner='agent_control' AND artifact_binding.reference_record_type='artifact'
              AND artifact_binding.reference_record_id=resolved.artifact_id
              AND artifact_binding.reference_record_digest=resolved.artifact_digest
              AND artifact_binding.owner_principal=invoker.principal_id AND artifact_binding.state='active'
              AND artifact_binding.retention_until>clock_timestamp())
        ORDER BY request_row.created_at DESC,request_row.request_id DESC LIMIT 6
      ) selected
    ) item
  ),'[]'::JSONB);
END $$;

REVOKE ALL ON FUNCTION agent_control.bind_cortex_conversation_raw(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_conversation_history(TEXT,TEXT,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.bind_cortex_conversation_raw(TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_conversation_history(TEXT,TEXT,TEXT) TO alpheus_agent_control_api;
RESET ROLE;
