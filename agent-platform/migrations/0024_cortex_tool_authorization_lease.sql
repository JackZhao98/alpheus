-- An authorization read or an idempotent retry may never outlive the Worker
-- lease that owns its source Model call.  Recovery of an interrupted Tool
-- needs an explicit reconciler in a later slice; a stale Worker cannot revive
-- the intent by replaying an old source call.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE OR REPLACE FUNCTION agent_control.authorize_cortex_web_fetch(
    p_source_call_id TEXT,p_attempt_id TEXT,p_lease_generation BIGINT,p_lease_token UUID,
    p_url TEXT,p_max_chars INTEGER
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; source_row RECORD; existing agent_control.cortex_tool_call_intent%ROWTYPE;
  intent_id TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp(); body JSONB; intent_digest CHAR(64); request_body JSONB; request_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id) OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
    OR p_lease_generation<1 OR p_lease_token IS NULL OR NOT agent_control.cortex_web_fetch_url_valid(p_url)
    OR p_max_chars NOT BETWEEN 1 AND 12000 THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid cortex web fetch authorization';
  END IF;
  SELECT manifest.call_id,result.result_id,result.record_digest AS result_digest,turn.run_id,turn.task_id,turn.turn_id,
         attempt.attempt_id,task.budget_ledger_id
    INTO STRICT source_row
    FROM agent_control.runtime_model_call_manifest manifest
    JOIN agent_control.runtime_model_call_result result ON result.call_id=manifest.call_id
    JOIN agent_control.runtime_turn turn ON turn.turn_id=result.turn_id
    JOIN agent_control.runtime_attempt attempt ON attempt.attempt_id=result.attempt_id
    JOIN agent_control.runtime_task task ON task.task_id=turn.task_id AND task.run_id=turn.run_id
    JOIN agent_control.runtime_run run ON run.run_id=turn.run_id
   WHERE manifest.call_id=p_source_call_id FOR UPDATE OF attempt,task,run;
  IF source_row.attempt_id<>p_attempt_id
     OR NOT EXISTS (SELECT 1 FROM agent_control.runtime_attempt attempt WHERE attempt.attempt_id=p_attempt_id
       AND attempt.state='executing' AND attempt.lease_generation=p_lease_generation AND attempt.lease_token=p_lease_token
       AND attempt.lease_expires_at>at_time AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
       AND attempt.lease_worker->>'kind'='workload' AND attempt.lease_worker->>'audience'='worker') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex tool authorization lease denied';
  END IF;
  SELECT * INTO existing FROM agent_control.cortex_tool_call_intent WHERE source_call_id=p_source_call_id;
  IF FOUND THEN
    IF existing.request_url<>p_url OR existing.request_max_chars<>p_max_chars THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex tool intent identity conflict';
    END IF;
    RETURN jsonb_build_object('status','authorized','tool_call_id',existing.tool_call_id,'tool_id',existing.tool_id,
      'request_digest',existing.request_digest::TEXT,'url',existing.request_url,'max_chars',existing.request_max_chars);
  END IF;
  request_body:=jsonb_build_object('url',p_url,'max_chars',p_max_chars);
  request_digest:=agent_control.runtime_contract_digest('agent-platform.contract.web_fetch_request.v1',request_body);
  IF NOT agent_control.runtime_charge_tool_budget_ancestors(source_row.run_id,source_row.budget_ledger_id,invoker.principal_id,
       p_source_call_id,p_source_call_id,at_time) THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='cortex tool budget denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'tool_call_id',intent_id,'tool_id','research_web_fetch',
    'source_result',jsonb_build_object('owner','agent_control','record_type','model_call_result','record_id',source_row.result_id,
      'schema_revision',1,'record_digest',source_row.result_digest::TEXT),'request',request_body,'request_digest',request_digest::TEXT,
    'authorized_by',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'authorized_at',agent_control.runtime_utc_text(at_time));
  intent_digest:=agent_control.runtime_contract_digest('agent-platform.contract.tool_call_intent.v1',body);
  INSERT INTO agent_control.cortex_tool_call_intent(tool_call_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,
      tool_id,request_digest,request_url,request_max_chars,record_digest,authorized_by,authorized_at)
    VALUES(intent_id,source_row.call_id,source_row.result_id,source_row.run_id,source_row.task_id,source_row.attempt_id,source_row.turn_id,
      'research_web_fetch',request_digest,p_url,p_max_chars,intent_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','authorized','tool_call_id',intent_id,'tool_id','research_web_fetch',
    'request_digest',request_digest::TEXT,'url',p_url,'max_chars',p_max_chars);
END $$;

RESET ROLE;
