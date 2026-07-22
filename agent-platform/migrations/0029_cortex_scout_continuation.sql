SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- A successful Scout does not itself answer the user.  Control owns the one
-- durable bridge from its typed memo back to the waiting parent Task.
CREATE TABLE agent_control.cortex_parent_continuation (
    admission_request_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_scout_child_admission(request_id),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    parent_task_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_task(task_id),
    scout_task_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_task(task_id),
    scout_artifact_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_artifact(artifact_id),
    scout_artifact_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(scout_artifact_digest::TEXT)),
    parent_session_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_session(session_id),
    execution_binding JSONB NOT NULL CHECK (agent_control.runtime_blob_ref_valid(execution_binding,'execution_binding','')),
    context_manifest JSONB NOT NULL CHECK (agent_control.runtime_blob_ref_valid(context_manifest,'context_manifest','')),
    state TEXT NOT NULL CHECK (state='ready'),
    created_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER cortex_parent_continuation_immutable
  BEFORE UPDATE OR DELETE ON agent_control.cortex_parent_continuation
  FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.list_cortex_scout_continuation_candidates(p_limit INTEGER)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 32 THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex scout continuation list denied';
  END IF;
  RETURN COALESCE((SELECT jsonb_agg(jsonb_build_object('request_id',admission.request_id)
      ORDER BY artifact.created_at,admission.request_id)
    FROM (
      SELECT admission.request_id,artifact.created_at
      FROM agent_control.cortex_scout_child_admission admission
      JOIN agent_control.runtime_task child ON child.task_id=admission.child_task_id
      JOIN agent_control.runtime_artifact artifact ON artifact.task_id=child.task_id
      WHERE admission.state='admitted' AND child.state='succeeded'
        AND artifact.artifact_type='scout_research_memo'
        AND NOT EXISTS (SELECT 1 FROM agent_control.cortex_parent_continuation continuation
          WHERE continuation.admission_request_id=admission.request_id)
      ORDER BY artifact.created_at,admission.request_id LIMIT p_limit
    ) AS admission),'[]'::JSONB);
END $$;

CREATE FUNCTION agent_control.get_cortex_parent_continuation_seed(p_request_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; admission agent_control.cortex_scout_child_admission%ROWTYPE;
  parent_task agent_control.runtime_task%ROWTYPE; child_task agent_control.runtime_task%ROWTYPE;
  handoff agent_control.cortex_handoff%ROWTYPE; artifact agent_control.runtime_artifact%ROWTYPE;
  memo JSONB; request_row agent_input.user_request%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR NOT agent_control.runtime_identifier_valid(p_request_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex parent continuation seed denied';
  END IF;
  SELECT * INTO admission FROM agent_control.cortex_scout_child_admission
    WHERE request_id=p_request_id AND state='admitted' FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='scout admission not found'; END IF;
  SELECT * INTO STRICT parent_task FROM agent_control.runtime_task WHERE task_id=admission.parent_task_id FOR SHARE;
  SELECT * INTO STRICT child_task FROM agent_control.runtime_task WHERE task_id=admission.child_task_id FOR SHARE;
  IF parent_task.state<>'waiting' OR child_task.state<>'succeeded' THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='scout parent is not continuation-ready';
  END IF;
  SELECT * INTO STRICT handoff FROM agent_control.cortex_handoff WHERE handoff_id=admission.handoff_id FOR SHARE;
  SELECT * INTO STRICT artifact FROM agent_control.runtime_artifact
    WHERE task_id=child_task.task_id AND artifact_type='scout_research_memo' FOR SHARE;
  SELECT section.content INTO STRICT memo FROM agent_control.runtime_artifact_section section
    WHERE section.artifact_id=artifact.artifact_id AND section.name='memo' AND section.required;
  SELECT request.* INTO STRICT request_row FROM agent_input.user_request request
    JOIN agent_control.runtime_run run ON run.origin_source_record_id=request.request_id
      AND run.origin_source_record_digest=request.record_digest
    WHERE run.run_id=admission.run_id FOR SHARE OF request;
  RETURN jsonb_build_object(
    'request_id',request_row.request_id,'conversation_id',request_row.conversation_id,
    'subject_principal_id',request_row.subject_principal_id,'raw_input',request_row.raw_input,
    'parent_task_id',parent_task.task_id,'handoff_id',handoff.handoff_id,
    'objective',handoff.objective,'rationale',handoff.rationale,
    'scout_artifact',jsonb_build_object('owner','agent_control','record_type','artifact','record_id',artifact.artifact_id,
      'schema_revision',1,'record_digest',artifact.record_digest::TEXT),
    'scout_memo',memo
  );
END $$;

CREATE FUNCTION agent_control.continue_cortex_parent_from_scout(
    p_request_id TEXT,p_execution_binding JSONB,p_context_manifest JSONB,p_worker_principal TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; admission agent_control.cortex_scout_child_admission%ROWTYPE;
  existing agent_control.cortex_parent_continuation%ROWTYPE; run_row agent_control.runtime_run%ROWTYPE;
  parent_task agent_control.runtime_task%ROWTYPE; parent_session agent_control.runtime_session%ROWTYPE;
  child_task agent_control.runtime_task%ROWTYPE; artifact agent_control.runtime_artifact%ROWTYPE;
  memo JSONB; raw_input JSONB; next_generation BIGINT; session_id_value TEXT;
  at_time TIMESTAMPTZ:=clock_timestamp(); response JSONB; continuation_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR NOT agent_control.runtime_identifier_valid(p_request_id)
     OR p_worker_principal<>'cortex-worker-1'
     OR NOT agent_control.runtime_blob_ref_valid(p_execution_binding,'execution_binding','')
     OR NOT agent_control.runtime_blob_ref_valid(p_context_manifest,'context_manifest','') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cortex parent continuation';
  END IF;
  SELECT * INTO admission FROM agent_control.cortex_scout_child_admission
    WHERE request_id=p_request_id AND state='admitted' FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='scout admission not found'; END IF;
  SELECT * INTO existing FROM agent_control.cortex_parent_continuation WHERE admission_request_id=p_request_id;
  IF FOUND THEN
    IF existing.execution_binding<>p_execution_binding OR existing.context_manifest<>p_context_manifest THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex parent continuation identity conflict';
    END IF;
    RETURN jsonb_build_object('status','ready','request_id',p_request_id,'parent_task_id',existing.parent_task_id,
      'parent_session_id',existing.parent_session_id);
  END IF;
  SELECT * INTO run_row FROM agent_control.runtime_run WHERE run_id=admission.run_id FOR UPDATE;
  SELECT * INTO parent_task FROM agent_control.runtime_task
    WHERE task_id=admission.parent_task_id AND run_id=admission.run_id FOR UPDATE;
  SELECT * INTO child_task FROM agent_control.runtime_task
    WHERE task_id=admission.child_task_id AND run_id=admission.run_id FOR SHARE;
  IF run_row.state<>'running' OR parent_task.state<>'waiting' OR child_task.state<>'succeeded'
     OR at_time>=run_row.deadline_at OR at_time>=parent_task.deadline_at THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='scout parent is not continuation-ready';
  END IF;
  SELECT * INTO STRICT parent_session FROM agent_control.runtime_session
    WHERE session_id=parent_task.session_id AND state='closed' FOR SHARE;
  SELECT * INTO STRICT artifact FROM agent_control.runtime_artifact
    WHERE task_id=child_task.task_id AND artifact_type='scout_research_memo' FOR SHARE;
  SELECT section.content INTO STRICT memo FROM agent_control.runtime_artifact_section section
    WHERE section.artifact_id=artifact.artifact_id AND section.name='memo' AND section.required;
  IF NOT agent_control.runtime_blob_ref_valid(memo,'model_call_manifest','')
     OR p_execution_binding#>>'{origin,record_id}'<>'cortex-desk-continuation-'||p_request_id
     OR p_context_manifest#>>'{origin,record_id}'<>'cortex-desk-context-'||p_request_id THEN
    RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='cortex parent continuation binding invalid';
  END IF;
  SELECT request.raw_input INTO STRICT raw_input FROM agent_input.user_request request
    WHERE request.request_id=run_row.origin_source_record_id AND request.record_digest=run_row.origin_source_record_digest FOR SHARE;
  IF NOT agent_control.runtime_blob_ref_valid(raw_input,'input_raw','') THEN
    RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='cortex parent raw input unavailable';
  END IF;
  next_generation:=parent_session.generation+1;
  session_id_value:='cortex-desk-session-'||p_request_id;
  PERFORM blob.bind_reference_internal('agent_control','cortex-session:'||session_id_value||':execution',
    (p_execution_binding->>'blob_id')::UUID,p_execution_binding#>>'{origin,record_type}',p_execution_binding#>>'{origin,record_id}',
    p_execution_binding#>>'{origin,record_digest}',invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control','cortex-session:'||session_id_value||':execution',invoker.principal_id,
    p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
  PERFORM blob.bind_reference_internal('agent_control','cortex-session:'||session_id_value||':context',
    (p_context_manifest->>'blob_id')::UUID,p_context_manifest#>>'{origin,record_type}',p_context_manifest#>>'{origin,record_id}',
    p_context_manifest#>>'{origin,record_digest}',invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control','cortex-session:'||session_id_value||':context',invoker.principal_id,
    p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
  PERFORM blob.bind_reference_internal('agent_control','cortex-session:'||session_id_value||':raw-input',
    (raw_input->>'blob_id')::UUID,raw_input#>>'{origin,record_type}',raw_input#>>'{origin,record_id}',
    raw_input#>>'{origin,record_digest}',invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control','cortex-session:'||session_id_value||':raw-input',invoker.principal_id,
    p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
  PERFORM blob.bind_reference_internal('agent_control','cortex-session:'||session_id_value||':scout-memo',
    (memo->>'blob_id')::UUID,'artifact',artifact.artifact_id,artifact.record_digest::TEXT,
    invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control','cortex-session:'||session_id_value||':scout-memo',invoker.principal_id,
    p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
  INSERT INTO agent_control.runtime_session(session_id,schema_revision,run_id,task_id,generation,execution_binding,context_manifest,state,created_at)
    VALUES(session_id_value,1,run_row.run_id,parent_task.task_id,next_generation,p_execution_binding,p_context_manifest,'open',at_time);
  UPDATE agent_control.runtime_task SET session_id=session_id_value,state='ready',state_generation=state_generation+1,
    updated_at=greatest(at_time,updated_at) WHERE task_id=parent_task.task_id;
  PERFORM agent_control.runtime_insert_event('task',parent_task.task_id,'waiting','ready',parent_task.state_generation+1,
    p_worker_principal,p_request_id,p_request_id,'parent_desk_continuation_ready',at_time);
  response:=jsonb_build_object('status','ready','request_id',p_request_id,'parent_task_id',parent_task.task_id,
    'parent_session_id',session_id_value);
  continuation_digest:=agent_control.runtime_contract_digest('agent_control.cortex_parent_continuation.v1',
    jsonb_build_object('admission_request_id',p_request_id,'run_id',run_row.run_id,'parent_task_id',parent_task.task_id,
      'scout_task_id',child_task.task_id,'scout_artifact_id',artifact.artifact_id,'scout_artifact_digest',artifact.record_digest::TEXT,
      'parent_session_id',session_id_value,'execution_binding',p_execution_binding,'context_manifest',p_context_manifest,
      'state','ready','response',response));
  INSERT INTO agent_control.cortex_parent_continuation(
    admission_request_id,schema_revision,record_digest,run_id,parent_task_id,scout_task_id,scout_artifact_id,
    scout_artifact_digest,parent_session_id,execution_binding,context_manifest,state,created_at
  ) VALUES(
    p_request_id,1,continuation_digest,run_row.run_id,parent_task.task_id,child_task.task_id,artifact.artifact_id,
    artifact.record_digest,session_id_value,p_execution_binding,p_context_manifest,'ready',at_time
  );
  RETURN response;
END $$;

-- New workflow schema identity for the sole additional handoff target.  The
-- v2 schema and its historical Runs are intentionally untouched.
CREATE FUNCTION agent_control.ensure_cortex_workflow_output_contract_v3(p_schema JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob SET timezone='UTC' AS $$
DECLARE invoker RECORD; at_time TIMESTAMPTZ:=clock_timestamp(); body JSONB; contract_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_blob_ref_valid(p_schema,'output_contract_schema','') OR p_schema->>'media_type'<>'application/json'
     OR NOT EXISTS (SELECT 1 FROM blob.blob_object object WHERE object.blob_id=(p_schema->>'blob_id')::UUID AND object.state='committed'
       AND object.content_digest=p_schema->>'content_digest' AND object.origin_owner='agent_control' AND object.origin_record_type='output_contract_schema'
       AND object.origin_record_id=p_schema#>>'{origin,record_id}' AND object.origin_record_digest=p_schema#>>'{origin,record_digest}') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex workflow v3 output contract denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'revision_id','cortex-workflow-output-v3','generation',1,'artifact_type','assistant_response','schema',p_schema,
    'effect_class','none','author',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'reason_code','cortex_workflow_output','created_at',agent_control.runtime_utc_text(at_time));
  contract_digest:=agent_control.runtime_contract_digest('agent-platform.contract.output_contract_revision.v1',body);
  INSERT INTO agent_control.output_contract_revision(
    revision_id,schema_revision,generation,record_digest,artifact_type,schema_blob_schema_revision,schema_blob_id,
    schema_blob_content_digest,schema_blob_media_type,schema_blob_size_bytes,schema_origin_owner,schema_origin_record_type,
    schema_origin_record_id,schema_origin_schema_revision,schema_origin_record_digest,schema_blob_committed_at,effect_class,
    author_principal_id,author_kind,author_audience,reason_code,created_at
  ) VALUES(
    'cortex-workflow-output-v3',1,1,contract_digest,'assistant_response',1,(p_schema->>'blob_id')::UUID,
    p_schema->>'content_digest',p_schema->>'media_type',(p_schema->>'size_bytes')::BIGINT,'agent_control',
    'output_contract_schema',p_schema#>>'{origin,record_id}',1,p_schema#>>'{origin,record_digest}',
    (p_schema->>'committed_at')::TIMESTAMPTZ,'none',invoker.principal_id,'workload','control_api',
    'cortex_workflow_output',at_time
  ) ON CONFLICT(revision_id) DO NOTHING;
  IF NOT EXISTS (SELECT 1 FROM agent_control.output_contract_revision
      WHERE revision_id='cortex-workflow-output-v3' AND schema_blob_id=(p_schema->>'blob_id')::UUID) THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex workflow v3 output contract identity conflict';
  END IF;
  SELECT record_digest INTO contract_digest FROM agent_control.output_contract_revision WHERE revision_id='cortex-workflow-output-v3';
  RETURN jsonb_build_object('status','ready','output_contract_digest',contract_digest);
END $$;

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.admit_cortex_user_request_run_v3(jsonb)'::regprocedure) INTO definition;
  IF position('cortex-workflow-output-v2' IN definition)=0 THEN
    RAISE EXCEPTION 'expected Cortex workflow admission v3 definition';
  END IF;
  EXECUTE replace(replace(definition,'admit_cortex_user_request_run_v3','admit_cortex_user_request_run_v4'),
    'cortex-workflow-output-v2','cortex-workflow-output-v3');
END $$;

-- A worker receives only the exact role inputs and per-session Blob bindings
-- it needs.  Its role is database-derived; no prompt can promote itself.
CREATE OR REPLACE FUNCTION agent_control.next_cortex_task()
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob SET timezone='UTC' AS $$
DECLARE invoker RECORD; selected RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_worker' OR invoker.profile_id<>'worker' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex Worker discovery denied';
  END IF;
  SELECT task.task_id,task.state_generation,task.output_contract_digest,task.output_contract_revision_id,task.deadline_at,
    session.session_id,session.context_manifest,request.raw_input,
    CASE WHEN scout_admission.request_id IS NOT NULL THEN 'scout'
         WHEN continuation.admission_request_id IS NOT NULL THEN 'desk' ELSE 'intent' END AS role,
    CASE WHEN task.output_contract_revision_id='cortex-workflow-output-v3' THEN true ELSE false END AS scout_enabled,
    handoff.objective,handoff.rationale,
    memo.content AS scout_memo,artifact.artifact_id AS scout_artifact_id,artifact.record_digest::TEXT AS scout_artifact_digest
  INTO selected FROM agent_control.runtime_task task
    JOIN agent_control.runtime_session session ON session.session_id=task.session_id
    JOIN agent_control.runtime_run run ON run.run_id=task.run_id
    JOIN agent_input.user_request request ON request.request_id=run.origin_source_record_id
      AND request.record_digest=run.origin_source_record_digest
    LEFT JOIN agent_control.cortex_scout_child_admission scout_admission
      ON scout_admission.child_task_id=task.task_id AND scout_admission.state='admitted'
    LEFT JOIN agent_control.cortex_parent_continuation continuation ON continuation.parent_task_id=task.task_id
    LEFT JOIN agent_control.cortex_scout_child_admission continued_admission
      ON continued_admission.request_id=continuation.admission_request_id
    LEFT JOIN agent_control.cortex_handoff handoff ON handoff.handoff_id=COALESCE(scout_admission.handoff_id,continued_admission.handoff_id)
    LEFT JOIN agent_control.runtime_artifact artifact ON artifact.artifact_id=continuation.scout_artifact_id
    LEFT JOIN agent_control.runtime_artifact_section memo ON memo.artifact_id=artifact.artifact_id
      AND memo.name='memo' AND memo.required
  WHERE task.state='ready' AND session.state='open' AND run.state IN ('queued','running','waiting')
    AND task.deadline_at>clock_timestamp()+interval '90 seconds'
    AND run.deadline_at>clock_timestamp()+interval '90 seconds'
  ORDER BY task.created_at,task.task_id LIMIT 1;
  IF NOT FOUND THEN RETURN NULL; END IF;
  RETURN jsonb_build_object(
    'task_id',selected.task_id,'task_state_generation',selected.state_generation,
    'output_contract_digest',selected.output_contract_digest::TEXT,'deadline',agent_control.runtime_utc_text(selected.deadline_at),
    'session_id',selected.session_id,'context_manifest',selected.context_manifest,
    'context_binding_id','cortex-session:'||selected.session_id||':context','raw_input',selected.raw_input,
    'raw_input_binding_id','cortex-session:'||selected.session_id||':raw-input','role',selected.role,
    'scout_enabled',selected.scout_enabled,'objective',selected.objective,'rationale',selected.rationale,
    'scout_memo',selected.scout_memo,
    'scout_memo_binding_id',CASE WHEN selected.scout_memo IS NULL THEN NULL ELSE 'cortex-session:'||selected.session_id||':scout-memo' END,
    'scout_artifact_id',selected.scout_artifact_id,'scout_artifact_digest',selected.scout_artifact_digest
  );
END $$;

REVOKE ALL ON TABLE agent_control.cortex_parent_continuation FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.list_cortex_scout_continuation_candidates(INTEGER) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_parent_continuation_seed(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.continue_cortex_parent_from_scout(TEXT,JSONB,JSONB,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.ensure_cortex_workflow_output_contract_v3(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.admit_cortex_user_request_run_v4(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.list_cortex_scout_continuation_candidates(INTEGER) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_parent_continuation_seed(TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.continue_cortex_parent_from_scout(TEXT,JSONB,JSONB,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.ensure_cortex_workflow_output_contract_v3(JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.admit_cortex_user_request_run_v4(JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task() TO alpheus_agent_worker;
RESET ROLE;
