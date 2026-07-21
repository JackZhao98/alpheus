SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';
CREATE FUNCTION agent_control.get_cortex_run_result(p_run_id TEXT) RETURNS JSONB
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,agent_control,platform_security AS $$
DECLARE invoker RECORD; run_row agent_control.runtime_run%ROWTYPE; found RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex result read denied';
  END IF;
  SELECT * INTO run_row FROM agent_control.runtime_run WHERE run_id=p_run_id;
  IF NOT FOUND THEN RETURN NULL; END IF;
  IF run_row.state<>'succeeded' THEN RETURN jsonb_build_object('run_id',run_row.run_id,'state',run_row.state); END IF;
  SELECT artifact.artifact_id,artifact.record_digest::TEXT AS artifact_digest,section.content
    INTO STRICT found FROM agent_control.runtime_artifact artifact
    JOIN agent_control.runtime_artifact_section section ON section.artifact_id=artifact.artifact_id
    WHERE artifact.run_id=run_row.run_id AND section.name='response' AND section.required
    ORDER BY artifact.created_at DESC LIMIT 1;
  RETURN jsonb_build_object('run_id',run_row.run_id,'state',run_row.state,'output',found.content,
    'binding_id','artifact:'||found.artifact_id||':blob:'||(found.content->>'blob_id'),
    'owning_reference',jsonb_build_object('owner','agent_control','record_type','artifact','record_id',found.artifact_id,
      'schema_revision',1,'record_digest',found.artifact_digest));
END $$;
REVOKE ALL ON FUNCTION agent_control.get_cortex_run_result(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_result(TEXT) TO alpheus_agent_control_api;
RESET ROLE;
