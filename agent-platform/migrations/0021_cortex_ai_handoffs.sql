SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- The workflow contract is deliberately narrow: an interpreter may either
-- produce a final user-facing answer or propose the one currently installed
-- specialist, Decision Desk. Scout is not advertised until its cross-plane
-- Research Tool capability has a separately activated contract.
CREATE FUNCTION agent_control.ensure_cortex_workflow_output_contract(p_schema JSONB)
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
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex workflow output contract denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'revision_id','cortex-workflow-output-v1','generation',1,
    'artifact_type','assistant_response','schema',p_schema,'effect_class','none',
    'author',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'reason_code','cortex_workflow_output','created_at',agent_control.runtime_utc_text(at_time));
  contract_digest:=agent_control.runtime_contract_digest('agent-platform.contract.output_contract_revision.v1',body);
  INSERT INTO agent_control.output_contract_revision(
    revision_id,schema_revision,generation,record_digest,artifact_type,
    schema_blob_schema_revision,schema_blob_id,schema_blob_content_digest,schema_blob_media_type,
    schema_blob_size_bytes,schema_origin_owner,schema_origin_record_type,schema_origin_record_id,
    schema_origin_schema_revision,schema_origin_record_digest,schema_blob_committed_at,effect_class,
    author_principal_id,author_kind,author_audience,reason_code,created_at)
  VALUES('cortex-workflow-output-v1',1,1,contract_digest,'assistant_response',1,
    (p_schema->>'blob_id')::UUID,p_schema->>'content_digest',p_schema->>'media_type',
    (p_schema->>'size_bytes')::BIGINT,'agent_control','output_contract_schema',p_schema#>>'{origin,record_id}',1,
    p_schema#>>'{origin,record_digest}',(p_schema->>'committed_at')::TIMESTAMPTZ,'none',
    invoker.principal_id,'workload','control_api','cortex_workflow_output',at_time)
  ON CONFLICT(revision_id) DO NOTHING;
  IF NOT EXISTS (SELECT 1 FROM agent_control.output_contract_revision
     WHERE revision_id='cortex-workflow-output-v1' AND schema_blob_id=(p_schema->>'blob_id')::UUID) THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex workflow output contract identity conflict';
  END IF;
  SELECT record_digest INTO contract_digest FROM agent_control.output_contract_revision
    WHERE revision_id='cortex-workflow-output-v1';
  RETURN jsonb_build_object('status','ready','output_contract_digest',contract_digest);
END $$;

-- New Runs use the workflow contract. This is a new admission function so
-- historical runs retain their exact immutable output contract and replay path.
CREATE FUNCTION agent_control.admit_cortex_user_request_run_v2(p_command JSONB)
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
    SELECT * INTO existing FROM agent_control.cortex_run_admission WHERE request_id=p_command->>'request_id' OR idempotency_key=p_command->>'idempotency_key';
    IF FOUND THEN
      IF existing.request_id<>p_command->>'request_id' OR existing.idempotency_key<>p_command->>'idempotency_key' OR existing.body_fingerprint<>fingerprint THEN
        RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex run admission identity conflict';
      END IF;
      RETURN existing.response;
    END IF;
    IF now_at>=deadline_at THEN RAISE EXCEPTION USING ERRCODE='57014',MESSAGE='cortex run admission deadline expired'; END IF;
    SELECT * INTO STRICT request_row FROM agent_input.user_request WHERE request_id=p_command->>'request_id' FOR SHARE;
    IF request_row.request_kind NOT IN ('new_request','continuation','additional_context','clarification_answer','correction') THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='user request kind does not create a run';
    END IF;
    SELECT * INTO STRICT conversation_row FROM agent_input.conversation
      WHERE conversation_id=request_row.conversation_id AND record_digest=request_row.conversation_digest FOR SHARE;
    IF NOT EXISTS (SELECT 1 FROM blob.blob_object object WHERE object.blob_id=(objective->>'blob_id')::UUID
      AND object.state='committed' AND object.content_digest=objective->>'content_digest'
      AND object.origin_record_digest=objective#>>'{origin,record_digest}') THEN
      RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='task objective blob not committed';
    END IF;
    SELECT revision.* INTO STRICT owner_policy FROM platform_governance.owner_policy_head head
      JOIN platform_governance.owner_policy_revision revision ON revision.policy_id=head.head_id AND revision.generation=head.generation
       AND revision.revision_id=head.revision_id AND revision.record_digest=head.revision_digest
      WHERE revision.origin_kind='user_request' AND revision.effect_ceiling='none'
       AND (revision.initiating_principal_id IS NULL OR revision.initiating_principal_id=request_row.subject_principal_id)
      ORDER BY (revision.initiating_principal_id IS NOT NULL) DESC,revision.policy_id LIMIT 1 FOR SHARE OF head;
    SELECT revision.* INTO STRICT policy FROM agent_control.runtime_policy_head head JOIN agent_control.runtime_policy_revision revision
      ON revision.policy_id=head.policy_id AND revision.generation=head.generation AND revision.record_digest=head.record_digest
      WHERE head.policy_id='cortex-mvp' FOR SHARE OF head;
    SELECT * INTO STRICT output_contract FROM agent_control.output_contract_revision WHERE revision_id='cortex-workflow-output-v1';
    INSERT INTO agent_control.runtime_run(
      run_id,schema_revision,origin_kind,origin_source_owner,origin_source_record_type,origin_source_record_id,origin_source_schema_revision,origin_source_record_digest,
      origin_conversation_owner,origin_conversation_record_type,origin_conversation_record_id,origin_conversation_schema_revision,origin_conversation_record_digest,
      origin_initiating_principal_id,origin_initiating_kind,origin_initiating_audience,origin_owner_policy_owner,origin_owner_policy_record_type,
      origin_owner_policy_record_id,origin_owner_policy_schema_revision,origin_owner_policy_record_digest,origin_owner_policy_generation,
      origin_occurred_at,origin_observed_at,origin_committed_at,runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,
      runtime_policy_schema_revision,runtime_policy_generation,runtime_policy_digest,budget_ledger_id,root_task_id,state,state_generation,created_at,updated_at,deadline_at)
    VALUES(run_id_value,1,'user_request','agent_control','user_request',request_row.request_id,1,request_row.record_digest,
      'agent_control','conversation',conversation_row.conversation_id,1,conversation_row.record_digest,request_row.subject_principal_id,'user','control_api',
      'platform_governance','owner_policy_revision',owner_policy.revision_id,1,owner_policy.record_digest,owner_policy.generation,
      request_row.created_at,request_row.created_at,request_row.created_at,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
      run_ledger_id,task_id_value,'queued',1,now_at,now_at,deadline_at);
    INSERT INTO agent_control.runtime_budget_ledger(
      ledger_id,schema_revision,scope,scope_id,parent_ledger_id,runtime_policy_owner,runtime_policy_record_type,runtime_policy_id,runtime_policy_schema_revision,
      runtime_policy_generation,runtime_policy_digest,limit_model_calls,limit_input_tokens,limit_output_tokens,limit_tool_calls,limit_external_cost_micro_usd,
      limit_wall_time_ms,limit_idle_time_ms,limit_tasks,limit_depth,limit_fanout,limit_parallelism,limit_invalid_output_retries,limit_infrastructure_retries,
      consumed_tasks,generation,state,updated_at)
    VALUES(run_ledger_id,1,'run',run_id_value,NULL,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
      policy.max_model_calls,policy.max_input_tokens,policy.max_output_tokens,policy.max_tool_calls,policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
      policy.max_idle_time_ms,policy.max_tasks,policy.max_depth,policy.max_fanout,policy.max_parallelism,policy.max_invalid_output_retries,policy.max_infrastructure_retries,1,1,'open',now_at),
      (task_ledger_id,1,'task',task_id_value,run_ledger_id,'agent_control','runtime_policy',policy.policy_id,1,policy.generation,policy.record_digest,
      policy.max_model_calls,policy.max_input_tokens,policy.max_output_tokens,policy.max_tool_calls,policy.max_external_cost_micro_usd,policy.max_wall_time_ms,
      policy.max_idle_time_ms,1,policy.max_depth,policy.max_fanout,policy.max_parallelism,policy.max_invalid_output_retries,policy.max_infrastructure_retries,1,1,'open',now_at);
    INSERT INTO agent_control.runtime_task(task_id,schema_revision,run_id,depth,objective,output_contract_owner,output_contract_record_type,
      output_contract_revision_id,output_contract_schema_revision,output_contract_generation,output_contract_digest,budget_ledger_id,state,state_generation,
      budget_slot_held,created_at,updated_at,deadline_at)
    VALUES(task_id_value,1,run_id_value,0,objective,'agent_control','output_contract_revision',output_contract.revision_id,1,output_contract.generation,
      output_contract.record_digest,task_ledger_id,'ready',1,false,now_at,now_at,deadline_at);
    INSERT INTO agent_control.runtime_task_input_ref(task_id,ordinal,reference) VALUES(task_id_value,1,jsonb_build_object('owner','agent_control',
      'record_type','user_request','record_id',request_row.request_id,'schema_revision',1,'record_digest',request_row.record_digest));
    event_body:=jsonb_build_object('schema_revision',1,'event_id',gen_random_uuid()::TEXT,'subject','run','subject_id',run_id_value,'to_state','queued','generation',1,
      'actor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),'causation_id',p_command->>'causation_id',
      'correlation_id',p_command->>'correlation_id','reason_code','user_request_admitted','occurred_at',agent_control.runtime_utc_text(now_at));
    INSERT INTO agent_control.runtime_event(event_id,schema_revision,record_digest,subject,subject_id,to_state,generation,actor,causation_id,correlation_id,reason_code,occurred_at)
      VALUES(event_body->>'event_id',1,agent_control.runtime_contract_digest('agent-platform.contract.runtime_event.v1',event_body),'run',run_id_value,'queued',1,event_body->'actor',
      p_command->>'causation_id',p_command->>'correlation_id','user_request_admitted',now_at);
    event_body:=jsonb_build_object('schema_revision',1,'event_id',gen_random_uuid()::TEXT,'subject','task','subject_id',task_id_value,'to_state','ready','generation',1,
      'actor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),'causation_id',p_command->>'causation_id',
      'correlation_id',p_command->>'correlation_id','reason_code','root_task_ready','occurred_at',agent_control.runtime_utc_text(now_at));
    INSERT INTO agent_control.runtime_event(event_id,schema_revision,record_digest,subject,subject_id,to_state,generation,actor,causation_id,correlation_id,reason_code,occurred_at)
      VALUES(event_body->>'event_id',1,agent_control.runtime_contract_digest('agent-platform.contract.runtime_event.v1',event_body),'task',task_id_value,'ready',1,event_body->'actor',
      p_command->>'causation_id',p_command->>'correlation_id','root_task_ready',now_at);
    response:=jsonb_build_object('status','admitted','request_id',request_row.request_id,'run_id',run_id_value,'root_task_id',task_id_value,'run_state','queued','task_state','ready');
    INSERT INTO agent_control.cortex_run_admission(request_id,idempotency_key,body_fingerprint,response) VALUES(p_command->>'request_id',p_command->>'idempotency_key',fingerprint,response);
    RETURN response;
END $$;

CREATE TABLE agent_control.cortex_handoff (
    handoff_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(handoff_id)),
    source_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_manifest(call_id),
    source_result_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_result(result_id),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
    attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
    turn_id TEXT NOT NULL REFERENCES agent_control.runtime_turn(turn_id),
    target_role TEXT NOT NULL CHECK (target_role = 'desk'),
    objective TEXT NOT NULL CHECK (octet_length(objective) BETWEEN 1 AND 4000 AND objective=btrim(objective)),
    rationale TEXT NOT NULL CHECK (octet_length(rationale) BETWEEN 1 AND 4000 AND rationale=btrim(rationale)),
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (source_call_id,target_role,objective,rationale)
);
CREATE TRIGGER cortex_handoff_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_handoff
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.record_cortex_handoff(p_call_id TEXT,p_target_role TEXT,p_objective TEXT,p_rationale TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; source_row RECORD; existing agent_control.cortex_handoff%ROWTYPE; handoff_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_call_id) OR p_target_role<>'desk'
    OR p_objective IS NULL OR p_objective<>btrim(p_objective) OR octet_length(p_objective) NOT BETWEEN 1 AND 4000
    OR p_rationale IS NULL OR p_rationale<>btrim(p_rationale) OR octet_length(p_rationale) NOT BETWEEN 1 AND 4000 THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cortex handoff';
  END IF;
  SELECT manifest.call_id,result.result_id,result.attempt_id,result.turn_id,turn.run_id,turn.task_id INTO STRICT source_row
    FROM agent_control.runtime_model_call_manifest manifest JOIN agent_control.runtime_model_call_result result ON result.call_id=manifest.call_id
    JOIN agent_control.runtime_turn turn ON turn.turn_id=result.turn_id
    WHERE manifest.call_id=p_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.cortex_handoff WHERE source_call_id=p_call_id;
  IF FOUND THEN
    IF existing.target_role<>p_target_role OR existing.objective<>p_objective OR existing.rationale<>p_rationale THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex handoff identity conflict';
    END IF;
    RETURN jsonb_build_object('status','recorded','handoff_id',existing.handoff_id);
  END IF;
  INSERT INTO agent_control.cortex_handoff(handoff_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,target_role,objective,rationale,created_at)
    VALUES(handoff_id_value,source_row.call_id,source_row.result_id,source_row.run_id,source_row.task_id,source_row.attempt_id,source_row.turn_id,p_target_role,p_objective,p_rationale,at_time);
  RETURN jsonb_build_object('status','recorded','handoff_id',handoff_id_value);
END $$;

CREATE FUNCTION agent_control.get_cortex_run_trace(p_run_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control' OR NOT agent_control.runtime_identifier_valid(p_run_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex trace read denied';
  END IF;
  RETURN COALESCE((SELECT jsonb_agg(event.payload ORDER BY event.occurred_at,event.ordinal) FROM (
    SELECT turn.created_at occurred_at,turn.ordinal*10 ordinal,jsonb_build_object('sequence',turn.ordinal*10,'created_at',agent_control.runtime_utc_text(turn.created_at),
      'stage',CASE WHEN turn.ordinal=1 THEN 'intent_interpreter_completed' ELSE 'decision_desk_completed' END,'turn_id',turn.turn_id,'state',turn.state) payload
      FROM agent_control.runtime_turn turn WHERE turn.run_id=p_run_id
    UNION ALL
    SELECT handoff.created_at,turn.ordinal*10+5,jsonb_build_object('sequence',turn.ordinal*10+5,'created_at',agent_control.runtime_utc_text(handoff.created_at),
      'stage','handoff_to_desk','target_role',handoff.target_role,'handoff_id',handoff.handoff_id) FROM agent_control.cortex_handoff handoff
      JOIN agent_control.runtime_turn turn ON turn.turn_id=handoff.turn_id WHERE handoff.run_id=p_run_id
  ) event),'[]'::JSONB);
END $$;

REVOKE ALL ON TABLE agent_control.cortex_handoff FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.ensure_cortex_workflow_output_contract(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.admit_cortex_user_request_run_v2(JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.record_cortex_handoff(TEXT,TEXT,TEXT,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_run_trace(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.ensure_cortex_workflow_output_contract(JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.admit_cortex_user_request_run_v2(JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_handoff(TEXT,TEXT,TEXT,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_trace(TEXT) TO alpheus_agent_control_api;
RESET ROLE;
