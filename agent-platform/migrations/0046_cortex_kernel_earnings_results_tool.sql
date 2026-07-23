-- Kernel publishes one narrow, receipt-backed Robinhood earnings fact. Cortex
-- never receives an MCP session, an account identifier, a generic MCP method,
-- or the upstream response guide.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_kernel_earnings_tool_call_intent (
    tool_call_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(tool_call_id)),
    source_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_manifest(call_id),
    source_result_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_result(result_id),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
    attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
    turn_id TEXT NOT NULL REFERENCES agent_control.runtime_turn(turn_id),
    tool_id TEXT NOT NULL CHECK (tool_id='kernel_earnings_results'),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    request_symbol TEXT NOT NULL CHECK (request_symbol ~ '^[A-Z0-9._-]{1,16}$'),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    authorized_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(authorized_by)),
    authorized_at TIMESTAMPTZ NOT NULL,
    UNIQUE (source_call_id,tool_id,request_digest)
);
CREATE TRIGGER cortex_kernel_earnings_tool_call_intent_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_kernel_earnings_tool_call_intent
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.kernel_earnings_results_evidence (
    evidence_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(evidence_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_kernel_earnings_tool_call_intent(tool_call_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    provider TEXT NOT NULL CHECK (provider='kernel_robinhood_mcp'),
    symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9._-]{1,16}$'),
    found BOOLEAN NOT NULL,
    results JSONB NOT NULL CHECK (jsonb_typeof(results)='array' AND jsonb_array_length(results)<=8),
    observed_at TIMESTAMPTZ NOT NULL,
    available_at TIMESTAMPTZ NOT NULL,
    body JSONB NOT NULL CHECK (jsonb_typeof(body)='object'),
    CHECK (available_at>=observed_at),
    CHECK (found OR results='[]'::JSONB)
);
CREATE TRIGGER kernel_earnings_results_evidence_immutable BEFORE UPDATE OR DELETE ON agent_control.kernel_earnings_results_evidence
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.kernel_earnings_tool_receipt (
    receipt_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(receipt_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_kernel_earnings_tool_call_intent(tool_call_id),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.kernel_earnings_results_evidence(evidence_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    executor_principal_id TEXT NOT NULL CHECK (executor_principal_id='kernel-1'),
    completed_at TIMESTAMPTZ NOT NULL,
    body JSONB NOT NULL CHECK (jsonb_typeof(body)='object')
);
CREATE TRIGGER kernel_earnings_tool_receipt_immutable BEFORE UPDATE OR DELETE ON agent_control.kernel_earnings_tool_receipt
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.cortex_kernel_earnings_tool_receipt_ack (
    tool_call_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_kernel_earnings_tool_call_intent(tool_call_id),
    receipt_id TEXT NOT NULL UNIQUE REFERENCES agent_control.kernel_earnings_tool_receipt(receipt_id),
    receipt_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(receipt_digest::TEXT)),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.kernel_earnings_results_evidence(evidence_id),
    evidence_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(evidence_digest::TEXT)),
    acknowledged_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(acknowledged_by)),
    acknowledged_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER cortex_kernel_earnings_tool_receipt_ack_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_kernel_earnings_tool_receipt_ack
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.authorize_cortex_kernel_earnings_results(
    p_source_call_id TEXT,p_attempt_id TEXT,p_lease_generation BIGINT,p_lease_token UUID,p_symbol TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; source_row RECORD; existing agent_control.cortex_kernel_earnings_tool_call_intent%ROWTYPE;
  intent_id TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp(); body JSONB; intent_digest CHAR(64); request_body JSONB; request_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id) OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
    OR p_lease_generation<1 OR p_lease_token IS NULL OR p_symbol !~ '^[A-Z0-9._-]{1,16}$' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Cortex Kernel earnings authorization';
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
  SELECT * INTO existing FROM agent_control.cortex_kernel_earnings_tool_call_intent WHERE source_call_id=p_source_call_id;
  IF FOUND THEN
    IF existing.request_symbol<>p_symbol THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='Cortex Kernel earnings intent identity conflict';
    END IF;
    RETURN jsonb_build_object('status','authorized','tool_call_id',existing.tool_call_id,'tool_id',existing.tool_id,
      'request_digest',existing.request_digest::TEXT,'symbol',existing.request_symbol);
  END IF;
  IF source_row.attempt_id<>p_attempt_id OR NOT EXISTS (SELECT 1 FROM agent_control.runtime_attempt attempt WHERE attempt.attempt_id=p_attempt_id
      AND attempt.state='executing' AND attempt.lease_generation=p_lease_generation AND attempt.lease_token=p_lease_token
      AND attempt.lease_expires_at>at_time AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
      AND attempt.lease_worker->>'kind'='workload' AND attempt.lease_worker->>'audience'='worker') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Kernel earnings authorization lease denied';
  END IF;
  request_body:=jsonb_build_object('symbol',p_symbol);
  request_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_earnings_results_request.v1',request_body);
  IF NOT agent_control.runtime_charge_tool_budget_ancestors(source_row.run_id,source_row.budget_ledger_id,invoker.principal_id,
       p_source_call_id,p_source_call_id,at_time) THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='Cortex tool budget denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'tool_call_id',intent_id,'tool_id','kernel_earnings_results',
    'source_result',jsonb_build_object('owner','agent_control','record_type','model_call_result','record_id',source_row.result_id,
      'schema_revision',1,'record_digest',source_row.result_digest::TEXT),'request',request_body,'request_digest',request_digest::TEXT,
    'authorized_by',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'authorized_at',agent_control.runtime_utc_text(at_time));
  intent_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_earnings_results_tool_call_intent.v1',body);
  INSERT INTO agent_control.cortex_kernel_earnings_tool_call_intent(tool_call_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,
      tool_id,request_digest,request_symbol,record_digest,authorized_by,authorized_at)
    VALUES(intent_id,source_row.call_id,source_row.result_id,source_row.run_id,source_row.task_id,source_row.attempt_id,source_row.turn_id,
      'kernel_earnings_results',request_digest,p_symbol,intent_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','authorized','tool_call_id',intent_id,'tool_id','kernel_earnings_results',
    'request_digest',request_digest::TEXT,'symbol',p_symbol);
END $$;

CREATE FUNCTION agent_control.record_cortex_kernel_earnings_results(p_tool_call_id TEXT,p_observation JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_kernel_earnings_tool_call_intent%ROWTYPE; existing agent_control.kernel_earnings_tool_receipt%ROWTYPE;
  evidence_id_value TEXT:=gen_random_uuid()::TEXT; receipt_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
  evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64); observed_value TIMESTAMPTZ; available_value TIMESTAMPTZ;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_observation)<>'object' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Kernel earnings receipt denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_kernel_earnings_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.kernel_earnings_tool_receipt WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN
    RETURN jsonb_build_object('receipt',existing.body,'evidence',(SELECT body FROM agent_control.kernel_earnings_results_evidence WHERE evidence_id=existing.evidence_id));
  END IF;
  IF p_observation-ARRAY['schema_revision','tool_call_id','tool_id','request_digest','provider','symbol','found','results','observed_at','available_at']<>'{}'::JSONB
     OR p_observation->>'schema_revision'<>'1' OR p_observation->>'tool_call_id'<>p_tool_call_id
     OR p_observation->>'tool_id'<>'kernel_earnings_results' OR p_observation->>'request_digest'<>intent.request_digest::TEXT
     OR p_observation->>'provider'<>'kernel_robinhood_mcp' OR p_observation->>'symbol'<>intent.request_symbol
     OR jsonb_typeof(p_observation->'found')<>'boolean' OR jsonb_typeof(p_observation->'results')<>'array'
     OR jsonb_array_length(p_observation->'results')>8 OR NOT agent_control.runtime_utc_instant_json(p_observation->'observed_at')
     OR NOT agent_control.runtime_utc_instant_json(p_observation->'available_at') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Kernel earnings observation';
  END IF;
  observed_value:=(p_observation->>'observed_at')::TIMESTAMPTZ;
  available_value:=(p_observation->>'available_at')::TIMESTAMPTZ;
  IF observed_value>available_value OR (p_observation->>'found'='false' AND p_observation->'results'<>'[]'::JSONB)
     OR EXISTS (SELECT 1 FROM jsonb_array_elements(p_observation->'results') AS item(value)
       WHERE jsonb_typeof(item.value)<>'object' OR item.value-ARRAY['symbol','year','quarter','eps','report']<>'{}'::JSONB
          OR item.value->>'symbol'<>intent.request_symbol OR jsonb_typeof(item.value->'year')<>'number' OR (item.value->>'year')::INTEGER NOT BETWEEN 1900 AND 2200
          OR jsonb_typeof(item.value->'quarter')<>'number' OR (item.value->>'quarter')::INTEGER NOT BETWEEN 1 AND 4
          OR jsonb_typeof(item.value->'eps')<>'object' OR item.value->'eps'-ARRAY['estimate','actual']<>'{}'::JSONB
          OR jsonb_typeof(item.value->'eps'->'estimate') NOT IN ('null','string') OR jsonb_typeof(item.value->'eps'->'actual') NOT IN ('null','string')
          OR jsonb_typeof(item.value->'report') NOT IN ('null','object')
          OR (jsonb_typeof(item.value->'report')='object' AND (item.value->'report'-ARRAY['date','timing','verified']<>'{}'::JSONB
             OR jsonb_typeof(item.value->'report'->'date') NOT IN ('null','string') OR jsonb_typeof(item.value->'report'->'timing') NOT IN ('null','string')
             OR jsonb_typeof(item.value->'report'->'verified')<>'boolean'
             OR (jsonb_typeof(item.value->'report'->'date')='string' AND item.value->'report'->>'date' !~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}$')
             OR (jsonb_typeof(item.value->'report'->'timing')='string' AND item.value->'report'->>'timing' NOT IN ('am','pm'))))) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Kernel earnings result item';
  END IF;
  evidence_body:=jsonb_build_object('schema_revision',1,'evidence_id',evidence_id_value,'tool_call_id',p_tool_call_id,
    'provider','kernel_robinhood_mcp','symbol',intent.request_symbol,'found',(p_observation->>'found')::BOOLEAN,'results',p_observation->'results',
    'observed_at',agent_control.runtime_utc_text(observed_value),'available_at',agent_control.runtime_utc_text(available_value));
  evidence_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_earnings_results_evidence.v1',evidence_body);
  INSERT INTO agent_control.kernel_earnings_results_evidence(evidence_id,tool_call_id,record_digest,provider,symbol,found,results,observed_at,available_at,body)
    VALUES(evidence_id_value,p_tool_call_id,evidence_digest,'kernel_robinhood_mcp',intent.request_symbol,(p_observation->>'found')::BOOLEAN,p_observation->'results',observed_value,available_value,evidence_body);
  receipt_body:=jsonb_build_object('schema_revision',1,'receipt_id',receipt_id_value,'tool_call_id',p_tool_call_id,
    'tool_id','kernel_earnings_results','request_digest',intent.request_digest::TEXT,'state','succeeded',
    'evidence',jsonb_build_object('owner','kernel','record_type','kernel_earnings_results_evidence','record_id',evidence_id_value,'schema_revision',1,'record_digest',evidence_digest::TEXT),
    'executor',jsonb_build_object('principal_id','kernel-1','kind','kernel','audience','kernel'),'completed_at',agent_control.runtime_utc_text(at_time));
  receipt_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_earnings_results_tool_receipt.v1',receipt_body);
  INSERT INTO agent_control.kernel_earnings_tool_receipt(receipt_id,tool_call_id,evidence_id,record_digest,request_digest,executor_principal_id,completed_at,body)
    VALUES(receipt_id_value,p_tool_call_id,evidence_id_value,receipt_digest,intent.request_digest,'kernel-1',at_time,receipt_body);
  INSERT INTO agent_control.cortex_kernel_earnings_tool_receipt_ack(tool_call_id,receipt_id,receipt_digest,evidence_id,evidence_digest,acknowledged_by,acknowledged_at)
    VALUES(p_tool_call_id,receipt_id_value,receipt_digest,evidence_id_value,evidence_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('receipt',receipt_body,'evidence',evidence_body);
END $$;

-- Extend the operator trace without exposing account data or upstream bytes.
CREATE OR REPLACE FUNCTION agent_control.get_cortex_run_trace(p_run_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_run_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex trace read denied';
  END IF;
  RETURN COALESCE((
    SELECT jsonb_agg(event.payload || jsonb_build_object('sequence',event.sequence)
      ORDER BY event.occurred_at,event.order_key,event.event_id)
    FROM (
      SELECT raw.occurred_at,raw.order_key,raw.event_id,raw.payload,
        row_number() OVER (ORDER BY raw.occurred_at,raw.order_key,raw.event_id) AS sequence
      FROM (
        SELECT COALESCE(turn.finished_at,turn.updated_at,turn.created_at) AS occurred_at,10 AS order_key,'turn:'||turn.turn_id AS event_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(COALESCE(turn.finished_at,turn.updated_at,turn.created_at)),
            'stage',CASE WHEN scout_admission.request_id IS NOT NULL THEN CASE turn.state WHEN 'result_committed' THEN 'scout_research_completed' WHEN 'failed' THEN 'scout_research_failed' ELSE 'scout_research_in_progress' END
              WHEN continuation.admission_request_id IS NOT NULL OR turn.ordinal>1 THEN CASE turn.state WHEN 'result_committed' THEN 'decision_desk_completed' WHEN 'failed' THEN 'decision_desk_failed' ELSE 'decision_desk_in_progress' END
              ELSE CASE turn.state WHEN 'result_committed' THEN 'intent_interpreter_completed' WHEN 'failed' THEN 'intent_interpreter_failed' ELSE 'intent_interpreter_in_progress' END END,
            'turn_id',turn.turn_id,'task_id',turn.task_id,'state',turn.state) AS payload
        FROM agent_control.runtime_turn turn
        LEFT JOIN agent_control.cortex_scout_child_admission scout_admission ON scout_admission.child_task_id=turn.task_id AND scout_admission.state='admitted'
        LEFT JOIN agent_control.cortex_parent_continuation continuation ON continuation.parent_task_id=turn.task_id AND continuation.parent_session_id=turn.session_id
        WHERE turn.run_id=p_run_id
        UNION ALL SELECT handoff.created_at,20,'handoff:'||handoff.handoff_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(handoff.created_at),'stage','handoff_to_'||handoff.target_role,'target_role',handoff.target_role,'handoff_id',handoff.handoff_id,'task_id',handoff.task_id) FROM agent_control.cortex_handoff handoff WHERE handoff.run_id=p_run_id
        UNION ALL SELECT admission.created_at,30,'scout-admission:'||admission.request_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(admission.created_at),'stage','scout_task_admitted','request_id',admission.request_id,'parent_task_id',admission.parent_task_id,'child_task_id',admission.child_task_id,'state',admission.state) FROM agent_control.cortex_scout_child_admission admission WHERE admission.run_id=p_run_id
        UNION ALL SELECT continuation.created_at,40,'desk-continuation:'||continuation.admission_request_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(continuation.created_at),'stage','desk_continuation_ready','request_id',continuation.admission_request_id,'parent_task_id',continuation.parent_task_id,'parent_session_id',continuation.parent_session_id,'state',continuation.state) FROM agent_control.cortex_parent_continuation continuation WHERE continuation.run_id=p_run_id
        UNION ALL SELECT failure.created_at,45,'scout-parent-failure:'||failure.admission_request_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(failure.created_at),'stage','scout_parent_failed','request_id',failure.admission_request_id,'parent_task_id',failure.parent_task_id,'child_task_id',failure.scout_task_id,'state',failure.scout_task_state) FROM agent_control.cortex_parent_scout_failure failure WHERE failure.run_id=p_run_id
        UNION ALL SELECT intent.authorized_at,50,'tool-authorized:'||intent.tool_call_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(intent.authorized_at),'stage','tool_call_authorized','tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id) FROM agent_control.cortex_tool_call_intent intent WHERE intent.run_id=p_run_id
        UNION ALL SELECT ack.acknowledged_at,60,'tool-receipt:'||ack.tool_call_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(ack.acknowledged_at),'stage','tool_receipt_succeeded','tool_call_id',ack.tool_call_id,'receipt_id',ack.receipt_id) FROM agent_control.cortex_tool_receipt_ack ack JOIN agent_control.cortex_tool_call_intent intent ON intent.tool_call_id=ack.tool_call_id WHERE intent.run_id=p_run_id
        UNION ALL SELECT intent.authorized_at,50,'gexbot-tool-authorized:'||intent.tool_call_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(intent.authorized_at),'stage','tool_call_authorized','tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id) FROM agent_control.cortex_gexbot_tool_call_intent intent WHERE intent.run_id=p_run_id
        UNION ALL SELECT ack.acknowledged_at,60,'gexbot-tool-receipt:'||ack.tool_call_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(ack.acknowledged_at),'stage','tool_receipt_succeeded','tool_call_id',ack.tool_call_id,'receipt_id',ack.receipt_id) FROM agent_control.cortex_gexbot_tool_receipt_ack ack JOIN agent_control.cortex_gexbot_tool_call_intent intent ON intent.tool_call_id=ack.tool_call_id WHERE intent.run_id=p_run_id
        UNION ALL SELECT intent.authorized_at,50,'kernel-earnings-tool-authorized:'||intent.tool_call_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(intent.authorized_at),'stage','tool_call_authorized','tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id) FROM agent_control.cortex_kernel_earnings_tool_call_intent intent WHERE intent.run_id=p_run_id
        UNION ALL SELECT ack.acknowledged_at,60,'kernel-earnings-tool-receipt:'||ack.tool_call_id,jsonb_build_object('created_at',agent_control.runtime_utc_text(ack.acknowledged_at),'stage','tool_receipt_succeeded','tool_call_id',ack.tool_call_id,'receipt_id',ack.receipt_id) FROM agent_control.cortex_kernel_earnings_tool_receipt_ack ack JOIN agent_control.cortex_kernel_earnings_tool_call_intent intent ON intent.tool_call_id=ack.tool_call_id WHERE intent.run_id=p_run_id
      ) raw
    ) event
  ),'[]'::JSONB);
END $$;

REVOKE ALL ON TABLE agent_control.cortex_kernel_earnings_tool_call_intent,agent_control.kernel_earnings_results_evidence,
  agent_control.kernel_earnings_tool_receipt,agent_control.cortex_kernel_earnings_tool_receipt_ack FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.authorize_cortex_kernel_earnings_results(TEXT,TEXT,BIGINT,UUID,TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.record_cortex_kernel_earnings_results(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.authorize_cortex_kernel_earnings_results(TEXT,TEXT,BIGINT,UUID,TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_kernel_earnings_results(TEXT,JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_trace(TEXT) TO alpheus_agent_control_api;

RESET ROLE;
