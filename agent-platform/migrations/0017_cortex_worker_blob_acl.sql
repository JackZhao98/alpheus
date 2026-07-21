SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.prepare_cortex_root_session_v2(
    p_task_id TEXT, p_execution_binding JSONB, p_context_manifest JSONB,
    p_raw_input JSONB, p_worker_principal TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob SET timezone='UTC' AS $$
DECLARE
    invoker RECORD; task_row agent_control.runtime_task%ROWTYPE;
    run_row agent_control.runtime_run%ROWTYPE; session_row agent_control.runtime_session%ROWTYPE;
    sid TEXT:=gen_random_uuid()::TEXT; execution_id TEXT; context_id TEXT; raw_id TEXT;
    at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
    SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
       OR NOT agent_control.runtime_identifier_valid(p_task_id)
       OR NOT agent_control.runtime_identifier_valid(p_worker_principal)
       OR NOT agent_control.runtime_blob_ref_valid(p_execution_binding,'execution_binding','')
       OR NOT agent_control.runtime_blob_ref_valid(p_context_manifest,'context_manifest','')
       OR NOT agent_control.runtime_blob_ref_valid(p_raw_input,'input_raw','') THEN
      RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex session preparation denied';
    END IF;
    SELECT * INTO STRICT task_row FROM agent_control.runtime_task WHERE task_id=p_task_id FOR UPDATE;
    SELECT * INTO STRICT run_row FROM agent_control.runtime_run WHERE run_id=task_row.run_id FOR SHARE;
    IF task_row.state<>'ready' OR run_row.state<>'queued' OR at_time>=task_row.deadline_at OR at_time>=run_row.deadline_at THEN
      RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='cortex root Task is not session-preparable';
    END IF;
    IF task_row.session_id IS NOT NULL THEN
      SELECT * INTO STRICT session_row FROM agent_control.runtime_session WHERE session_id=task_row.session_id;
      IF session_row.execution_binding<>p_execution_binding OR session_row.context_manifest<>p_context_manifest THEN
        RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex Session identity conflict';
      END IF;
      RETURN jsonb_build_object('status','ready','session_id',session_row.session_id);
    END IF;
    execution_id:='cortex-session:'||sid||':execution'; context_id:='cortex-session:'||sid||':context'; raw_id:='cortex-session:'||sid||':raw-input';
    PERFORM blob.bind_reference_internal('agent_control',execution_id,(p_execution_binding->>'blob_id')::UUID,
      p_execution_binding#>>'{origin,record_type}',p_execution_binding#>>'{origin,record_id}',p_execution_binding#>>'{origin,record_digest}',
      invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
    PERFORM blob.change_acl_internal('agent_control',execution_id,invoker.principal_id,p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
    PERFORM blob.bind_reference_internal('agent_control',context_id,(p_context_manifest->>'blob_id')::UUID,
      p_context_manifest#>>'{origin,record_type}',p_context_manifest#>>'{origin,record_id}',p_context_manifest#>>'{origin,record_digest}',
      invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
    PERFORM blob.change_acl_internal('agent_control',context_id,invoker.principal_id,p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
    PERFORM blob.bind_reference_internal('agent_control',raw_id,(p_raw_input->>'blob_id')::UUID,
      p_raw_input#>>'{origin,record_type}',p_raw_input#>>'{origin,record_id}',p_raw_input#>>'{origin,record_digest}',
      invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
    PERFORM blob.change_acl_internal('agent_control',raw_id,invoker.principal_id,p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
    INSERT INTO agent_control.runtime_session(session_id,schema_revision,run_id,task_id,generation,execution_binding,context_manifest,state,created_at)
      VALUES(sid,1,run_row.run_id,task_row.task_id,1,p_execution_binding,p_context_manifest,'open',at_time);
    UPDATE agent_control.runtime_task SET session_id=sid WHERE task_id=task_row.task_id;
    RETURN jsonb_build_object('status','ready','session_id',sid,'context_binding_id',context_id,'raw_input_binding_id',raw_id);
END $$;

CREATE FUNCTION agent_control.publish_cortex_model_output_v2(
    p_call_id TEXT,p_manifest_digest TEXT,p_output JSONB,p_worker_principal TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob SET timezone='UTC' AS $$
DECLARE invoker RECORD; manifest agent_control.runtime_model_call_manifest%ROWTYPE; binding_id_value TEXT; retention TIMESTAMPTZ;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_blob_ref_valid(p_output,'model_call_manifest','')
     OR p_output#>>'{origin,record_id}'<>p_call_id OR p_output#>>'{origin,record_digest}'<>p_manifest_digest THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex model output publication denied';
  END IF;
  SELECT * INTO STRICT manifest FROM agent_control.runtime_model_call_manifest WHERE call_id=p_call_id AND record_digest::TEXT=p_manifest_digest FOR SHARE;
  SELECT least(run.deadline_at,clock_timestamp()+interval '1 day') INTO retention FROM agent_control.runtime_attempt attempt JOIN agent_control.runtime_run run ON run.run_id=attempt.run_id WHERE attempt.attempt_id=manifest.attempt_id;
  binding_id_value:='cortex-model-output:'||p_call_id;
  PERFORM blob.bind_reference_internal('agent_control',binding_id_value,(p_output->>'blob_id')::UUID,'model_call_manifest',p_call_id,p_manifest_digest,invoker.principal_id,'explicit',retention,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control',binding_id_value,invoker.principal_id,p_worker_principal,0,'grant','cortex_worker_model_output',invoker.principal_id);
  RETURN jsonb_build_object('status','published','binding_id',binding_id_value);
END $$;

REVOKE ALL ON FUNCTION agent_control.prepare_cortex_root_session_v2(TEXT,JSONB,JSONB,JSONB,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.publish_cortex_model_output_v2(TEXT,TEXT,JSONB,TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.prepare_cortex_root_session_v2(TEXT,JSONB,JSONB,JSONB,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.publish_cortex_model_output_v2(TEXT,TEXT,JSONB,TEXT) TO alpheus_agent_control_api;
RESET ROLE;
