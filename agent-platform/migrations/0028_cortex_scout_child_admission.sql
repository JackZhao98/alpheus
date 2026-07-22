SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- The first bounded collaboration slice.  A Scout request is still an
-- immutable Worker-originated request, while Control alone admits the child
-- Task, Session, bindings, and parent wait transition.  This deliberately
-- does not generalize AP5 scheduling or role registration.

ALTER TABLE agent_control.cortex_handoff
  DROP CONSTRAINT cortex_handoff_target_role_check;
ALTER TABLE agent_control.cortex_handoff
  ADD CONSTRAINT cortex_handoff_target_role_check
  CHECK (target_role IN ('desk','scout'));

CREATE OR REPLACE FUNCTION agent_control.record_cortex_handoff(
    p_call_id TEXT,p_target_role TEXT,p_objective TEXT,p_rationale TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; source_row RECORD; existing agent_control.cortex_handoff%ROWTYPE;
  handoff_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_call_id) OR p_target_role NOT IN ('desk','scout')
    OR p_objective IS NULL OR p_objective<>btrim(p_objective) OR octet_length(p_objective) NOT BETWEEN 1 AND 4000
    OR p_rationale IS NULL OR p_rationale<>btrim(p_rationale) OR octet_length(p_rationale) NOT BETWEEN 1 AND 4000 THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cortex handoff';
  END IF;
  SELECT manifest.call_id,result.result_id,result.attempt_id,result.turn_id,turn.run_id,turn.task_id INTO STRICT source_row
    FROM agent_control.runtime_model_call_manifest manifest
    JOIN agent_control.runtime_model_call_result result ON result.call_id=manifest.call_id
    JOIN agent_control.runtime_turn turn ON turn.turn_id=result.turn_id
    WHERE manifest.call_id=p_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.cortex_handoff WHERE source_call_id=p_call_id;
  IF FOUND THEN
    IF existing.target_role<>p_target_role OR existing.objective<>p_objective OR existing.rationale<>p_rationale THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex handoff identity conflict';
    END IF;
    RETURN jsonb_build_object('status','recorded','handoff_id',existing.handoff_id);
  END IF;
  INSERT INTO agent_control.cortex_handoff(
    handoff_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,
    target_role,objective,rationale,created_at
  ) VALUES(
    handoff_id_value,source_row.call_id,source_row.result_id,source_row.run_id,
    source_row.task_id,source_row.attempt_id,source_row.turn_id,p_target_role,
    p_objective,p_rationale,at_time
  );
  RETURN jsonb_build_object('status','recorded','handoff_id',handoff_id_value);
END $$;

-- The child has its own typed output, rather than reusing the user-facing
-- assistant-response contract.  Its bytes are still effect-free.
CREATE FUNCTION agent_control.ensure_cortex_scout_memo_output_contract(p_schema JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob SET timezone='UTC' AS $$
DECLARE invoker RECORD; at_time TIMESTAMPTZ:=clock_timestamp(); body JSONB; contract_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_blob_ref_valid(p_schema,'output_contract_schema','')
     OR p_schema->>'media_type'<>'application/json'
     OR NOT EXISTS (SELECT 1 FROM blob.blob_object object
        WHERE object.blob_id=(p_schema->>'blob_id')::UUID AND object.state='committed'
          AND object.content_digest=p_schema->>'content_digest' AND object.origin_owner='agent_control'
          AND object.origin_record_type='output_contract_schema'
          AND object.origin_record_id=p_schema#>>'{origin,record_id}'
          AND object.origin_record_digest=p_schema#>>'{origin,record_digest}') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex scout output contract denied';
  END IF;
  body:=jsonb_build_object(
    'schema_revision',1,'revision_id','cortex-scout-research-memo-v1','generation',1,
    'artifact_type','scout_research_memo','schema',p_schema,'effect_class','none',
    'author',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'reason_code','cortex_scout_research_memo','created_at',agent_control.runtime_utc_text(at_time)
  );
  contract_digest:=agent_control.runtime_contract_digest('agent-platform.contract.output_contract_revision.v1',body);
  INSERT INTO agent_control.output_contract_revision(
    revision_id,schema_revision,generation,record_digest,artifact_type,
    schema_blob_schema_revision,schema_blob_id,schema_blob_content_digest,schema_blob_media_type,
    schema_blob_size_bytes,schema_origin_owner,schema_origin_record_type,schema_origin_record_id,
    schema_origin_schema_revision,schema_origin_record_digest,schema_blob_committed_at,effect_class,
    author_principal_id,author_kind,author_audience,reason_code,created_at
  ) VALUES(
    'cortex-scout-research-memo-v1',1,1,contract_digest,'scout_research_memo',1,
    (p_schema->>'blob_id')::UUID,p_schema->>'content_digest',p_schema->>'media_type',
    (p_schema->>'size_bytes')::BIGINT,'agent_control','output_contract_schema',p_schema#>>'{origin,record_id}',1,
    p_schema#>>'{origin,record_digest}',(p_schema->>'committed_at')::TIMESTAMPTZ,'none',
    invoker.principal_id,'workload','control_api','cortex_scout_research_memo',at_time
  ) ON CONFLICT(revision_id) DO NOTHING;
  IF NOT EXISTS (SELECT 1 FROM agent_control.output_contract_revision
      WHERE revision_id='cortex-scout-research-memo-v1' AND schema_blob_id=(p_schema->>'blob_id')::UUID) THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex scout output contract identity conflict';
  END IF;
  SELECT record_digest INTO contract_digest FROM agent_control.output_contract_revision
    WHERE revision_id='cortex-scout-research-memo-v1';
  RETURN jsonb_build_object('status','ready','output_contract_digest',contract_digest);
END $$;

-- One durable request per Scout handoff.  The HTTP caller is authenticated as
-- the Worker, but Control commits the immutable task-objective Blob first so
-- a Worker never receives Blob write authority or direct runtime DML.
CREATE FUNCTION agent_control.record_cortex_scout_child_request(
    p_call_id TEXT,p_worker_principal TEXT,p_objective JSONB
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security,blob SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; handoff agent_control.cortex_handoff%ROWTYPE;
  result_row agent_control.runtime_model_call_result%ROWTYPE;
  contract agent_control.output_contract_revision%ROWTYPE;
  request_id_value TEXT; request_digest CHAR(64); limits JSONB;
  existing agent_control.runtime_child_task_request%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_call_id)
     OR p_worker_principal<>'cortex-worker-1'
     OR NOT agent_control.runtime_blob_ref_valid(p_objective,'task_objective','') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cortex scout child request';
  END IF;
  SELECT * INTO handoff FROM agent_control.cortex_handoff
    WHERE source_call_id=p_call_id AND target_role='scout' FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='scout handoff not found'; END IF;
  SELECT * INTO STRICT result_row FROM agent_control.runtime_model_call_result
    WHERE result_id=handoff.source_result_id AND call_id=handoff.source_call_id FOR SHARE;
  IF p_objective#>>'{origin,record_id}'<>'cortex-scout-request-'||p_call_id
     OR NOT EXISTS (SELECT 1 FROM blob.blob_object object
        WHERE object.blob_id=(p_objective->>'blob_id')::UUID AND object.state='committed'
          AND object.content_digest=p_objective->>'content_digest'
          AND object.origin_owner='agent_control' AND object.origin_record_type='task_objective'
          AND object.origin_record_id=p_objective#>>'{origin,record_id}'
          AND object.origin_record_digest=p_objective#>>'{origin,record_digest}') THEN
    RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='scout objective blob is not committed';
  END IF;
  SELECT * INTO STRICT contract FROM agent_control.output_contract_revision
    WHERE revision_id='cortex-scout-research-memo-v1' FOR SHARE;
  request_id_value:='cortex-scout-request-'||p_call_id;
  limits:=jsonb_build_object(
    'max_model_calls',2,'max_input_tokens',500000,'max_output_tokens',8000,
    'max_tool_calls',1,'max_external_cost_micro_usd',0,'max_wall_time_ms',180000,
    'max_idle_time_ms',60000,'max_tasks',1,'max_depth',1,'max_fanout',0,
    'max_parallelism',1,'max_invalid_output_retries',1,'max_infrastructure_retries',1
  );
  SELECT * INTO existing FROM agent_control.runtime_child_task_request WHERE request_id=request_id_value;
  IF FOUND THEN
    IF existing.command_principal_id<>p_worker_principal OR existing.parent_task_id<>handoff.task_id
       OR existing.attempt_id<>handoff.attempt_id OR existing.objective<>p_objective
       OR existing.required_capability<>'cortex_scout_research_v1'
       OR existing.requested_limit<>limits THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex scout child request identity conflict';
    END IF;
    RETURN jsonb_build_object('status','recorded','child_request_id',existing.request_id);
  END IF;
  request_digest:=agent_control.runtime_contract_digest(
    'agent_control.runtime_child_task_request.v1',
    jsonb_build_object(
      'request_id',request_id_value,'command_principal_id',p_worker_principal,
      'run_id',handoff.run_id,'parent_task_id',handoff.task_id,'attempt_id',handoff.attempt_id,
      'required_capability','cortex_scout_research_v1','reason_code','intent_handoff_to_scout',
      'objective',p_objective,
      'input_refs',jsonb_build_array(jsonb_build_object(
        'owner','agent_control','record_type','model_call_result','record_id',result_row.result_id,
        'schema_revision',1,'record_digest',result_row.record_digest::TEXT
      )),
      'output_contract',jsonb_build_object(
        'owner','agent_control','record_type','output_contract_revision','record_id',contract.revision_id,
        'schema_revision',1,'record_digest',contract.record_digest::TEXT,'generation',contract.generation
      ),'requested_limit',limits
    )
  );
  INSERT INTO agent_control.runtime_child_task_request(
    request_id,schema_revision,record_digest,command_principal_id,command_id,run_id,parent_task_id,
    attempt_id,required_capability,reason_code,objective,input_refs,output_contract_owner,
    output_contract_record_type,output_contract_revision_id,output_contract_schema_revision,
    output_contract_generation,output_contract_digest,requested_limit,created_at
  ) VALUES(
    request_id_value,1,request_digest,p_worker_principal,request_id_value,handoff.run_id,handoff.task_id,
    handoff.attempt_id,'cortex_scout_research_v1','intent_handoff_to_scout',p_objective,
    jsonb_build_array(jsonb_build_object(
      'owner','agent_control','record_type','model_call_result','record_id',result_row.result_id,
      'schema_revision',1,'record_digest',result_row.record_digest::TEXT
    )),
    'agent_control','output_contract_revision',contract.revision_id,1,contract.generation,
    contract.record_digest,limits,at_time
  );
  RETURN jsonb_build_object('status','recorded','child_request_id',request_id_value);
END $$;

-- Control needs only this narrow, lineage-bound seed to construct the child
-- context.  It never exposes the parent's Session or arbitrary Runtime rows.
CREATE FUNCTION agent_control.get_cortex_scout_child_seed(p_call_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; handoff agent_control.cortex_handoff%ROWTYPE; request_row agent_input.user_request%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR NOT agent_control.runtime_identifier_valid(p_call_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex scout child seed denied';
  END IF;
  SELECT * INTO handoff FROM agent_control.cortex_handoff
    WHERE source_call_id=p_call_id AND target_role='scout' FOR SHARE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='scout handoff not found'; END IF;
  SELECT request.* INTO STRICT request_row FROM agent_input.user_request request
    JOIN agent_control.runtime_run run ON run.origin_source_record_id=request.request_id
      AND run.origin_source_record_digest=request.record_digest
    WHERE run.run_id=handoff.run_id FOR SHARE OF request;
  RETURN jsonb_build_object(
    'request_id',request_row.request_id,'conversation_id',request_row.conversation_id,
    'subject_principal_id',request_row.subject_principal_id,'raw_input',request_row.raw_input,
    'objective',handoff.objective,'rationale',handoff.rationale,'handoff_id',handoff.handoff_id
  );
END $$;

CREATE TABLE agent_control.cortex_scout_child_admission (
    request_id TEXT PRIMARY KEY REFERENCES agent_control.runtime_child_task_request(request_id),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision=1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    state TEXT NOT NULL CHECK (state IN ('admitted','rejected')),
    reason_code TEXT NOT NULL CHECK (agent_control.runtime_name_valid(reason_code)),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    parent_task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
    parent_attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
    handoff_id TEXT NOT NULL REFERENCES agent_control.cortex_handoff(handoff_id),
    child_task_id TEXT UNIQUE REFERENCES agent_control.runtime_task(task_id),
    child_budget_ledger_id TEXT UNIQUE REFERENCES agent_control.runtime_budget_ledger(ledger_id),
    child_session_id TEXT UNIQUE REFERENCES agent_control.runtime_session(session_id),
    execution_binding JSONB,
    context_manifest JSONB,
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CHECK ((state='admitted' AND child_task_id IS NOT NULL AND child_budget_ledger_id IS NOT NULL
      AND child_session_id IS NOT NULL
      AND agent_control.runtime_blob_ref_valid(execution_binding,'execution_binding','')
      AND agent_control.runtime_blob_ref_valid(context_manifest,'context_manifest',''))
      OR (state='rejected' AND child_task_id IS NULL AND child_budget_ledger_id IS NULL
      AND child_session_id IS NULL AND execution_binding IS NULL AND context_manifest IS NULL))
);
CREATE UNIQUE INDEX cortex_scout_child_admission_one_per_parent_idx
  ON agent_control.cortex_scout_child_admission(parent_task_id);
CREATE TRIGGER cortex_scout_child_admission_immutable
  BEFORE UPDATE OR DELETE ON agent_control.cortex_scout_child_admission
  FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- Create an exact child Task/Session and park its parent in the same
-- transaction.  The old parent Attempt is superseded (not failed) because its
-- committed Intent result has been consumed by the admitted child work.
CREATE FUNCTION agent_control.admit_cortex_scout_child(
    p_request_id TEXT,p_execution_binding JSONB,p_context_manifest JSONB,p_worker_principal TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; request_row agent_control.runtime_child_task_request%ROWTYPE;
  existing agent_control.cortex_scout_child_admission%ROWTYPE;
  handoff agent_control.cortex_handoff%ROWTYPE; run_row agent_control.runtime_run%ROWTYPE;
  parent_task agent_control.runtime_task%ROWTYPE; parent_attempt agent_control.runtime_attempt%ROWTYPE;
  parent_session agent_control.runtime_session%ROWTYPE; parent_ledger agent_control.runtime_budget_ledger%ROWTYPE;
  run_ledger agent_control.runtime_budget_ledger%ROWTYPE; output_contract agent_control.output_contract_revision%ROWTYPE;
  raw_input JSONB; child_task_id_value TEXT; child_ledger_id_value TEXT; child_session_id_value TEXT;
  at_time TIMESTAMPTZ:=clock_timestamp(); denial TEXT; response JSONB; admission_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR NOT agent_control.runtime_identifier_valid(p_request_id)
     OR p_worker_principal<>'cortex-worker-1'
     OR NOT agent_control.runtime_blob_ref_valid(p_execution_binding,'execution_binding','')
     OR NOT agent_control.runtime_blob_ref_valid(p_context_manifest,'context_manifest','') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cortex scout child admission';
  END IF;
  SELECT * INTO request_row FROM agent_control.runtime_child_task_request WHERE request_id=p_request_id FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='cortex scout request not found'; END IF;
  SELECT * INTO existing FROM agent_control.cortex_scout_child_admission WHERE request_id=p_request_id;
  IF FOUND THEN
    IF existing.state='admitted' AND (existing.execution_binding<>p_execution_binding OR existing.context_manifest<>p_context_manifest) THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex scout admission identity conflict';
    END IF;
    RETURN existing.response;
  END IF;
  SELECT * INTO handoff FROM agent_control.cortex_handoff
    WHERE run_id=request_row.run_id AND task_id=request_row.parent_task_id
      AND attempt_id=request_row.attempt_id AND target_role='scout' FOR UPDATE;
  IF NOT FOUND THEN denial:='scout_handoff_not_found'; ELSE
    SELECT * INTO run_row FROM agent_control.runtime_run WHERE run_id=request_row.run_id FOR UPDATE;
    SELECT * INTO parent_task FROM agent_control.runtime_task
      WHERE task_id=request_row.parent_task_id AND run_id=request_row.run_id FOR UPDATE;
    SELECT * INTO parent_attempt FROM agent_control.runtime_attempt
      WHERE attempt_id=request_row.attempt_id FOR UPDATE;
    IF parent_attempt.run_id<>run_row.run_id OR parent_attempt.task_id<>parent_task.task_id THEN
      denial:='parent_attempt_task_mismatch';
    ELSIF request_row.required_capability<>'cortex_scout_research_v1' THEN
      denial:='child_capability_not_installed';
    ELSIF request_row.output_contract_revision_id<>'cortex-scout-research-memo-v1' THEN
      denial:='child_output_contract_invalid';
    ELSIF parent_task.depth<>0 THEN
      denial:='child_depth_exhausted';
    ELSIF run_row.state<>'running' OR parent_task.state<>'running' THEN
      denial:='parent_task_not_running';
    ELSIF parent_attempt.state<>'executing' OR at_time>=parent_attempt.lease_expires_at THEN
      denial:='parent_attempt_lease_stale';
    ELSIF NOT agent_control.runtime_run_admission_current(run_row.run_id) THEN
      denial:='runtime_authority_not_current';
    ELSIF at_time>=run_row.deadline_at OR at_time>=parent_task.deadline_at THEN
      denial:='runtime_deadline_expired';
    ELSIF EXISTS (SELECT 1 FROM agent_control.runtime_turn turn
      WHERE turn.attempt_id=parent_attempt.attempt_id AND turn.state IN ('planned','dispatched','unknown')) THEN
      denial:='parent_turn_unresolved';
    END IF;
  END IF;
  IF denial IS NULL THEN
    SELECT * INTO parent_session FROM agent_control.runtime_session
      WHERE session_id=parent_task.session_id AND state='open' FOR UPDATE;
    IF NOT FOUND THEN denial:='parent_session_not_open'; END IF;
  END IF;
  IF denial IS NULL THEN
    SELECT * INTO run_ledger FROM agent_control.runtime_budget_ledger
      WHERE ledger_id=run_row.budget_ledger_id FOR UPDATE;
    SELECT * INTO parent_ledger FROM agent_control.runtime_budget_ledger
      WHERE ledger_id=parent_task.budget_ledger_id FOR UPDATE;
    IF run_ledger.state<>'open' OR parent_ledger.state<>'open' THEN
      denial:='parent_budget_unavailable';
    ELSIF parent_ledger.limit_fanout<1 OR parent_ledger.limit_tasks-parent_ledger.consumed_tasks-parent_ledger.reserved_tasks<1
      OR run_ledger.limit_tasks-run_ledger.consumed_tasks-run_ledger.reserved_tasks<1 THEN
      denial:='child_budget_exceeds_parent';
    ELSIF NOT agent_control.runtime_child_limit_within_parent(request_row.requested_limit,parent_ledger) THEN
      denial:='child_budget_exceeds_parent';
    END IF;
  END IF;
  IF denial IS NULL THEN
    SELECT * INTO STRICT output_contract FROM agent_control.output_contract_revision
      WHERE revision_id=request_row.output_contract_revision_id
        AND generation=request_row.output_contract_generation
        AND record_digest=request_row.output_contract_digest FOR SHARE;
    SELECT request.raw_input INTO raw_input FROM agent_input.user_request request
      WHERE request.request_id=run_row.origin_source_record_id
        AND request.record_digest=run_row.origin_source_record_digest FOR SHARE;
    IF NOT FOUND OR NOT agent_control.runtime_blob_ref_valid(raw_input,'input_raw','') THEN
      denial:='parent_raw_input_not_found';
    ELSIF p_execution_binding#>>'{origin,record_id}'<>'cortex-scout-task-'||p_request_id
       OR p_context_manifest#>>'{origin,record_id}'<>'cortex-scout-context-'||p_request_id THEN
      denial:='child_session_binding_invalid';
    END IF;
  END IF;
  IF denial IS NOT NULL THEN
    response:=jsonb_build_object('status','rejected','request_id',p_request_id,'reason_code',denial);
    admission_digest:=agent_control.runtime_contract_digest('agent_control.cortex_scout_child_admission.v1',
      jsonb_build_object('request_id',p_request_id,'state','rejected','reason_code',denial,
        'run_id',request_row.run_id,'parent_task_id',request_row.parent_task_id,
        'parent_attempt_id',request_row.attempt_id,'handoff_id',COALESCE(handoff.handoff_id,''),'response',response));
    INSERT INTO agent_control.cortex_scout_child_admission(
      request_id,schema_revision,record_digest,state,reason_code,run_id,parent_task_id,parent_attempt_id,handoff_id,response,created_at
    ) VALUES(p_request_id,1,admission_digest,'rejected',denial,request_row.run_id,request_row.parent_task_id,
      request_row.attempt_id,COALESCE(handoff.handoff_id,'missing-handoff'),response,at_time);
    RETURN response;
  END IF;
  child_task_id_value:='cortex-scout-task-'||p_request_id;
  child_ledger_id_value:='cortex-scout-ledger-'||p_request_id;
  child_session_id_value:='cortex-scout-session-'||p_request_id;
  -- The fixed child Task is counted once on its parent and Run ledgers.
  UPDATE agent_control.runtime_budget_ledger SET consumed_tasks=consumed_tasks+1,generation=generation+1,
    updated_at=greatest(at_time,updated_at) WHERE ledger_id=run_ledger.ledger_id;
  UPDATE agent_control.runtime_budget_ledger SET consumed_tasks=consumed_tasks+1,generation=generation+1,
    updated_at=greatest(at_time,updated_at) WHERE ledger_id=parent_ledger.ledger_id;
  INSERT INTO agent_control.runtime_budget_ledger(
    ledger_id,schema_revision,scope,scope_id,parent_ledger_id,runtime_policy_owner,runtime_policy_record_type,
    runtime_policy_id,runtime_policy_schema_revision,runtime_policy_generation,runtime_policy_digest,
    limit_model_calls,limit_input_tokens,limit_output_tokens,limit_tool_calls,limit_external_cost_micro_usd,
    limit_wall_time_ms,limit_idle_time_ms,limit_tasks,limit_depth,limit_fanout,limit_parallelism,
    limit_invalid_output_retries,limit_infrastructure_retries,consumed_tasks,generation,state,updated_at
  ) VALUES(
    child_ledger_id_value,1,'task',child_task_id_value,parent_ledger.ledger_id,
    parent_ledger.runtime_policy_owner,parent_ledger.runtime_policy_record_type,parent_ledger.runtime_policy_id,
    parent_ledger.runtime_policy_schema_revision,parent_ledger.runtime_policy_generation,parent_ledger.runtime_policy_digest,
    (request_row.requested_limit->>'max_model_calls')::BIGINT,(request_row.requested_limit->>'max_input_tokens')::BIGINT,
    (request_row.requested_limit->>'max_output_tokens')::BIGINT,(request_row.requested_limit->>'max_tool_calls')::BIGINT,
    (request_row.requested_limit->>'max_external_cost_micro_usd')::BIGINT,(request_row.requested_limit->>'max_wall_time_ms')::BIGINT,
    (request_row.requested_limit->>'max_idle_time_ms')::BIGINT,(request_row.requested_limit->>'max_tasks')::BIGINT,
    (request_row.requested_limit->>'max_depth')::BIGINT,(request_row.requested_limit->>'max_fanout')::BIGINT,
    (request_row.requested_limit->>'max_parallelism')::BIGINT,(request_row.requested_limit->>'max_invalid_output_retries')::BIGINT,
    (request_row.requested_limit->>'max_infrastructure_retries')::BIGINT,1,1,'open',at_time
  );
  INSERT INTO agent_control.runtime_task(
    task_id,schema_revision,run_id,parent_task_id,depth,objective,output_contract_owner,output_contract_record_type,
    output_contract_revision_id,output_contract_schema_revision,output_contract_generation,output_contract_digest,
    budget_ledger_id,state,state_generation,budget_slot_held,created_at,updated_at,deadline_at
  ) VALUES(
    child_task_id_value,1,run_row.run_id,parent_task.task_id,1,request_row.objective,'agent_control',
    'output_contract_revision',output_contract.revision_id,1,output_contract.generation,output_contract.record_digest,
    child_ledger_id_value,'ready',1,false,at_time,at_time,least(run_row.deadline_at,parent_task.deadline_at)
  );
  INSERT INTO agent_control.runtime_task_input_ref(task_id,ordinal,reference) VALUES
    (child_task_id_value,1,jsonb_build_object('owner','agent_control','record_type','user_request',
      'record_id',run_row.origin_source_record_id,'schema_revision',1,'record_digest',run_row.origin_source_record_digest)),
    (child_task_id_value,2,jsonb_build_object('owner','agent_control','record_type','model_call_result',
      'record_id',handoff.source_result_id,'schema_revision',1,'record_digest',(
        SELECT result.record_digest::TEXT FROM agent_control.runtime_model_call_result result WHERE result.result_id=handoff.source_result_id
      )));
  PERFORM blob.bind_reference_internal('agent_control','cortex-session:'||child_session_id_value||':execution',
    (p_execution_binding->>'blob_id')::UUID,p_execution_binding#>>'{origin,record_type}',p_execution_binding#>>'{origin,record_id}',
    p_execution_binding#>>'{origin,record_digest}',invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control','cortex-session:'||child_session_id_value||':execution',invoker.principal_id,
    p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
  PERFORM blob.bind_reference_internal('agent_control','cortex-session:'||child_session_id_value||':context',
    (p_context_manifest->>'blob_id')::UUID,p_context_manifest#>>'{origin,record_type}',p_context_manifest#>>'{origin,record_id}',
    p_context_manifest#>>'{origin,record_digest}',invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control','cortex-session:'||child_session_id_value||':context',invoker.principal_id,
    p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
  PERFORM blob.bind_reference_internal('agent_control','cortex-session:'||child_session_id_value||':raw-input',
    (raw_input->>'blob_id')::UUID,raw_input#>>'{origin,record_type}',raw_input#>>'{origin,record_id}',
    raw_input#>>'{origin,record_digest}',invoker.principal_id,'explicit',run_row.deadline_at,invoker.principal_id);
  PERFORM blob.change_acl_internal('agent_control','cortex-session:'||child_session_id_value||':raw-input',invoker.principal_id,
    p_worker_principal,0,'grant','cortex_worker_session',invoker.principal_id);
  INSERT INTO agent_control.runtime_session(session_id,schema_revision,run_id,task_id,generation,execution_binding,context_manifest,state,created_at)
    VALUES(child_session_id_value,1,run_row.run_id,child_task_id_value,1,p_execution_binding,p_context_manifest,'open',at_time);
  UPDATE agent_control.runtime_task SET session_id=child_session_id_value WHERE task_id=child_task_id_value;
  UPDATE agent_control.runtime_attempt SET state='superseded',state_generation=state_generation+1,
    updated_at=greatest(at_time,updated_at),terminal_at=at_time WHERE attempt_id=parent_attempt.attempt_id;
  PERFORM agent_control.runtime_insert_attempt_release_event(parent_attempt.attempt_id,parent_attempt.lease_generation,
    p_worker_principal,parent_attempt.lease_token,parent_attempt.lease_expires_at,p_request_id,p_request_id,at_time);
  PERFORM agent_control.runtime_insert_event('attempt',parent_attempt.attempt_id,'executing','superseded',
    parent_attempt.state_generation+1,p_worker_principal,p_request_id,p_request_id,'parent_waiting_for_scout',at_time);
  UPDATE agent_control.runtime_session SET state='closed',generation=generation+1,closed_at=at_time
    WHERE session_id=parent_session.session_id;
  PERFORM agent_control.runtime_insert_event('session',parent_session.session_id,'open','closed',parent_session.generation+1,
    p_worker_principal,p_request_id,p_request_id,'parent_session_parked_for_scout',at_time);
  UPDATE agent_control.runtime_task SET state='waiting',state_generation=state_generation+1,
    updated_at=greatest(at_time,updated_at) WHERE task_id=parent_task.task_id;
  PERFORM agent_control.runtime_insert_event('task',parent_task.task_id,'running','waiting',parent_task.state_generation+1,
    p_worker_principal,p_request_id,p_request_id,'parent_waiting_for_scout',at_time);
  PERFORM agent_control.runtime_insert_event('task',child_task_id_value,NULL,'ready',1,p_worker_principal,
    p_request_id,p_request_id,'scout_child_task_ready',at_time);
  response:=jsonb_build_object('status','admitted','request_id',p_request_id,'child_task_id',child_task_id_value,
    'child_session_id',child_session_id_value,'parent_task_state','waiting');
  admission_digest:=agent_control.runtime_contract_digest('agent_control.cortex_scout_child_admission.v1',
    jsonb_build_object('request_id',p_request_id,'state','admitted','reason_code','scout_child_admitted',
      'run_id',run_row.run_id,'parent_task_id',parent_task.task_id,'parent_attempt_id',parent_attempt.attempt_id,
      'handoff_id',handoff.handoff_id,'child_task_id',child_task_id_value,'child_budget_ledger_id',child_ledger_id_value,
      'child_session_id',child_session_id_value,'execution_binding',p_execution_binding,'context_manifest',p_context_manifest,
      'response',response));
  INSERT INTO agent_control.cortex_scout_child_admission(
    request_id,schema_revision,record_digest,state,reason_code,run_id,parent_task_id,parent_attempt_id,handoff_id,
    child_task_id,child_budget_ledger_id,child_session_id,execution_binding,context_manifest,response,created_at
  ) VALUES(
    p_request_id,1,admission_digest,'admitted','scout_child_admitted',run_row.run_id,parent_task.task_id,
    parent_attempt.attempt_id,handoff.handoff_id,child_task_id_value,child_ledger_id_value,child_session_id_value,
    p_execution_binding,p_context_manifest,response,at_time
  );
  RETURN response;
END $$;

-- Worker discovery now derives raw input from the Run origin rather than the
-- context Blob origin.  That permits distinct immutable Scout contexts while
-- preserving the exact original UserRequest bytes.
CREATE OR REPLACE FUNCTION agent_control.next_cortex_task()
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security,blob SET timezone='UTC' AS $$
DECLARE invoker RECORD; selected RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_worker' OR invoker.profile_id<>'worker' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex Worker discovery denied';
  END IF;
  SELECT task.task_id,task.state_generation,task.output_contract_digest,task.deadline_at,session.session_id,
    session.context_manifest,request.raw_input
  INTO selected FROM agent_control.runtime_task task
    JOIN agent_control.runtime_session session ON session.session_id=task.session_id
    JOIN agent_control.runtime_run run ON run.run_id=task.run_id
    JOIN agent_input.user_request request ON request.request_id=run.origin_source_record_id
      AND request.record_digest=run.origin_source_record_digest
  WHERE task.state='ready' AND session.state='open' AND run.state IN ('queued','running','waiting')
    AND task.deadline_at>clock_timestamp()+interval '90 seconds'
    AND run.deadline_at>clock_timestamp()+interval '90 seconds'
  ORDER BY task.created_at,task.task_id LIMIT 1;
  IF NOT FOUND THEN RETURN NULL; END IF;
  RETURN jsonb_build_object('task_id',selected.task_id,'task_state_generation',selected.state_generation,
    'output_contract_digest',selected.output_contract_digest::TEXT,'deadline',agent_control.runtime_utc_text(selected.deadline_at),
    'session_id',selected.session_id,'context_manifest',selected.context_manifest,
    'context_binding_id','cortex-session:'||selected.session_id||':context','raw_input',selected.raw_input,
    'raw_input_binding_id','cortex-session:'||selected.session_id||':raw-input');
END $$;

-- New Runs reserve exactly one bounded child-work slot for the installed
-- Scout.  The Run policy itself remains unchanged and historical ledgers are
-- intentionally not rewritten.
CREATE FUNCTION agent_control.admit_cortex_user_request_run_v3(p_command JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_governance,platform_security,blob SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; request_row agent_input.user_request%ROWTYPE; conversation_row agent_input.conversation%ROWTYPE;
  policy agent_control.runtime_policy_revision%ROWTYPE; owner_policy platform_governance.owner_policy_revision%ROWTYPE;
  output_contract agent_control.output_contract_revision%ROWTYPE; existing agent_control.cortex_run_admission%ROWTYPE;
  objective JSONB:=p_command->'objective'; fingerprint CHAR(64); now_at TIMESTAMPTZ:=clock_timestamp(); deadline_at TIMESTAMPTZ;
  run_id_value TEXT:=gen_random_uuid()::TEXT; task_id_value TEXT:=gen_random_uuid()::TEXT;
  run_ledger_id TEXT:=gen_random_uuid()::TEXT; task_ledger_id TEXT:=gen_random_uuid()::TEXT; response JSONB; event_body JSONB;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR jsonb_typeof(p_command)<>'object'
     OR NOT (p_command ?& ARRAY['request_id','idempotency_key','causation_id','correlation_id','deadline','objective'])
     OR p_command-ARRAY['request_id','idempotency_key','causation_id','correlation_id','deadline','objective']<>'{}'::JSONB
     OR NOT agent_control.runtime_identifier_valid(p_command->>'request_id')
     OR NOT agent_control.runtime_identifier_valid(p_command->>'idempotency_key')
     OR NOT agent_control.runtime_identifier_valid(p_command->>'causation_id')
     OR NOT agent_control.runtime_identifier_valid(p_command->>'correlation_id')
     OR NOT agent_control.runtime_utc_instant_json(p_command->'deadline')
     OR NOT agent_control.runtime_blob_ref_valid(objective,'task_objective','')
     OR objective#>>'{origin,record_id}'<>p_command->>'request_id' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid_cortex_run_admission';
  END IF;
  deadline_at:=(p_command->>'deadline')::TIMESTAMPTZ; fingerprint:=agent_control.runtime_sha256_json(p_command);
  SELECT * INTO existing FROM agent_control.cortex_run_admission
    WHERE request_id=p_command->>'request_id' OR idempotency_key=p_command->>'idempotency_key';
  IF FOUND THEN
    IF existing.request_id<>p_command->>'request_id' OR existing.idempotency_key<>p_command->>'idempotency_key'
      OR existing.body_fingerprint<>fingerprint THEN RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex run admission identity conflict'; END IF;
    RETURN existing.response;
  END IF;
  IF now_at>=deadline_at THEN RAISE EXCEPTION USING ERRCODE='57014',MESSAGE='cortex run admission deadline expired'; END IF;
  SELECT * INTO STRICT request_row FROM agent_input.user_request WHERE request_id=p_command->>'request_id' FOR SHARE;
  IF request_row.request_kind NOT IN ('new_request','continuation','additional_context','clarification_answer','correction') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='user request kind does not create a run'; END IF;
  SELECT * INTO STRICT conversation_row FROM agent_input.conversation
    WHERE conversation_id=request_row.conversation_id AND record_digest=request_row.conversation_digest FOR SHARE;
  IF NOT EXISTS (SELECT 1 FROM blob.blob_object object WHERE object.blob_id=(objective->>'blob_id')::UUID
    AND object.state='committed' AND object.content_digest=objective->>'content_digest'
    AND object.origin_record_digest=objective#>>'{origin,record_digest}') THEN
    RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='task objective blob not committed'; END IF;
  SELECT revision.* INTO STRICT owner_policy FROM platform_governance.owner_policy_head head
    JOIN platform_governance.owner_policy_revision revision ON revision.policy_id=head.head_id
      AND revision.generation=head.generation AND revision.revision_id=head.revision_id AND revision.record_digest=head.revision_digest
    WHERE revision.origin_kind='user_request' AND revision.effect_ceiling='none'
      AND (revision.initiating_principal_id IS NULL OR revision.initiating_principal_id=request_row.subject_principal_id)
    ORDER BY (revision.initiating_principal_id IS NOT NULL) DESC,revision.policy_id LIMIT 1 FOR SHARE OF head;
  SELECT revision.* INTO STRICT policy FROM agent_control.runtime_policy_head head
    JOIN agent_control.runtime_policy_revision revision ON revision.policy_id=head.policy_id
      AND revision.generation=head.generation AND revision.record_digest=head.record_digest
    WHERE head.policy_id='cortex-mvp' FOR SHARE OF head;
  SELECT * INTO STRICT output_contract FROM agent_control.output_contract_revision WHERE revision_id='cortex-workflow-output-v2';
  INSERT INTO agent_control.runtime_run(
    run_id,schema_revision,origin_kind,origin_source_owner,origin_source_record_type,origin_source_record_id,origin_source_schema_revision,origin_source_record_digest,
    origin_conversation_owner,origin_conversation_record_type,origin_conversation_record_id,origin_conversation_schema_revision,origin_conversation_record_digest,
    origin_initiating_principal_id,origin_initiating_kind,origin_initiating_audience,origin_owner_policy_owner,origin_owner_policy_record_type,
    origin_owner_policy_record_id,origin_owner_policy_schema_revision,origin_owner_policy_record_digest,origin_owner_policy_generation,
    origin_occurred_at,origin_observed_at,origin_committed_at,runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,
    runtime_policy_schema_revision,runtime_policy_generation,runtime_policy_digest,budget_ledger_id,root_task_id,state,state_generation,created_at,updated_at,deadline_at
  ) VALUES(
    run_id_value,1,'user_request','agent_control','user_request',request_row.request_id,1,request_row.record_digest,
    'agent_control','conversation',conversation_row.conversation_id,1,conversation_row.record_digest,request_row.subject_principal_id,'user','control_api',
    'platform_governance','owner_policy_revision',owner_policy.revision_id,1,owner_policy.record_digest,owner_policy.generation,
    request_row.created_at,request_row.created_at,request_row.created_at,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
    run_ledger_id,task_id_value,'queued',1,now_at,now_at,deadline_at
  );
  INSERT INTO agent_control.runtime_budget_ledger(
    ledger_id,schema_revision,scope,scope_id,parent_ledger_id,runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,runtime_policy_schema_revision,
    runtime_policy_generation,runtime_policy_digest,limit_model_calls,limit_input_tokens,limit_output_tokens,limit_tool_calls,limit_external_cost_micro_usd,
    limit_wall_time_ms,limit_idle_time_ms,limit_tasks,limit_depth,limit_fanout,limit_parallelism,limit_invalid_output_retries,limit_infrastructure_retries,
    consumed_tasks,generation,state,updated_at
  ) VALUES
    (run_ledger_id,1,'run',run_id_value,NULL,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
      policy.max_model_calls,policy.max_input_tokens,policy.max_output_tokens,policy.max_tool_calls,policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
      policy.max_idle_time_ms,policy.max_tasks,policy.max_depth,policy.max_fanout,policy.max_parallelism,policy.max_invalid_output_retries,policy.max_infrastructure_retries,1,1,'open',now_at),
    (task_ledger_id,1,'task',task_id_value,run_ledger_id,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
      policy.max_model_calls,policy.max_input_tokens,policy.max_output_tokens,policy.max_tool_calls,policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
      policy.max_idle_time_ms,2,policy.max_depth,policy.max_fanout,policy.max_parallelism,policy.max_invalid_output_retries,policy.max_infrastructure_retries,1,1,'open',now_at);
  INSERT INTO agent_control.runtime_task(
    task_id,schema_revision,run_id,depth,objective,output_contract_owner,output_contract_record_type,output_contract_revision_id,
    output_contract_schema_revision,output_contract_generation,output_contract_digest,budget_ledger_id,state,state_generation,budget_slot_held,created_at,updated_at,deadline_at
  ) VALUES(
    task_id_value,1,run_id_value,0,objective,'agent_control','output_contract_revision',output_contract.revision_id,1,
    output_contract.generation,output_contract.record_digest,task_ledger_id,'ready',1,false,now_at,now_at,deadline_at
  );
  INSERT INTO agent_control.runtime_task_input_ref(task_id,ordinal,reference) VALUES(task_id_value,1,jsonb_build_object(
    'owner','agent_control','record_type','user_request','record_id',request_row.request_id,'schema_revision',1,'record_digest',request_row.record_digest));
  event_body:=jsonb_build_object('schema_revision',1,'event_id',gen_random_uuid()::TEXT,'subject','run','subject_id',run_id_value,
    'to_state','queued','generation',1,'actor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'causation_id',p_command->>'causation_id','correlation_id',p_command->>'correlation_id','reason_code','user_request_admitted','occurred_at',agent_control.runtime_utc_text(now_at));
  INSERT INTO agent_control.runtime_event(event_id,schema_revision,record_digest,subject,subject_id,to_state,generation,actor,causation_id,correlation_id,reason_code,occurred_at)
    VALUES(event_body->>'event_id',1,agent_control.runtime_contract_digest('agent-platform.contract.runtime_event.v1',event_body),'run',run_id_value,'queued',1,event_body->'actor',
      p_command->>'causation_id',p_command->>'correlation_id','user_request_admitted',now_at);
  event_body:=jsonb_build_object('schema_revision',1,'event_id',gen_random_uuid()::TEXT,'subject','task','subject_id',task_id_value,
    'to_state','ready','generation',1,'actor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'causation_id',p_command->>'causation_id','correlation_id',p_command->>'correlation_id','reason_code','root_task_ready','occurred_at',agent_control.runtime_utc_text(now_at));
  INSERT INTO agent_control.runtime_event(event_id,schema_revision,record_digest,subject,subject_id,to_state,generation,actor,causation_id,correlation_id,reason_code,occurred_at)
    VALUES(event_body->>'event_id',1,agent_control.runtime_contract_digest('agent-platform.contract.runtime_event.v1',event_body),'task',task_id_value,'ready',1,event_body->'actor',
      p_command->>'causation_id',p_command->>'correlation_id','root_task_ready',now_at);
  response:=jsonb_build_object('status','admitted','request_id',request_row.request_id,'run_id',run_id_value,'root_task_id',task_id_value,'run_state','queued','task_state','ready');
  INSERT INTO agent_control.cortex_run_admission(request_id,idempotency_key,body_fingerprint,response)
    VALUES(p_command->>'request_id',p_command->>'idempotency_key',fingerprint,response);
  RETURN response;
END $$;

REVOKE ALL ON TABLE agent_control.cortex_scout_child_admission FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.ensure_cortex_scout_memo_output_contract(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.record_cortex_scout_child_request(TEXT,TEXT,JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_scout_child_seed(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.admit_cortex_scout_child(TEXT,JSONB,JSONB,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.admit_cortex_user_request_run_v3(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.ensure_cortex_scout_memo_output_contract(JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_scout_child_request(TEXT,TEXT,JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_scout_child_seed(TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.admit_cortex_scout_child(TEXT,JSONB,JSONB,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.admit_cortex_user_request_run_v3(JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task() TO alpheus_agent_worker;
RESET ROLE;
