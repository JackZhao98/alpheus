-- Install the on-demand official GEXBOT read as a distinct Tool. It never
-- aliases the archive/as_of contract: its evidence preserves provider source
-- time, request observation time, fetch time, and database availability time.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

INSERT INTO agent_control.cortex_specialist_tool_grant(role_id,tool_id,effect,granted_at)
VALUES('options_scout','market_gexbot_live','read_only',clock_timestamp());

CREATE TABLE agent_control.cortex_gexbot_live_tool_call_intent (
  tool_call_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(tool_call_id)),
  source_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_manifest(call_id),
  source_result_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_result(result_id),
  run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
  task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
  attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
  turn_id TEXT NOT NULL REFERENCES agent_control.runtime_turn(turn_id),
  tool_id TEXT NOT NULL CHECK (tool_id='market_gexbot_live'),
  request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
  request_symbol TEXT NOT NULL CHECK (request_symbol='SPX'),
  request_category TEXT NOT NULL CHECK (request_category IN ('gex_full','gex_zero','gex_one')),
  record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
  authorized_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(authorized_by)),
  authorized_at TIMESTAMPTZ NOT NULL,
  UNIQUE(source_call_id,tool_id,request_digest)
);
CREATE TRIGGER cortex_gexbot_live_tool_call_intent_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_gexbot_live_tool_call_intent
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.research_gexbot_live_evidence (
  evidence_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(evidence_id)),
  tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_gexbot_live_tool_call_intent(tool_call_id),
  record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
  provider TEXT NOT NULL CHECK (provider='gexbot_classic'),
  symbol TEXT NOT NULL CHECK (symbol='SPX'),
  category TEXT NOT NULL CHECK (category IN ('gex_full','gex_zero','gex_one')),
  observation_id UUID NOT NULL,
  observation_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(observation_digest::TEXT)),
  source_timestamp TIMESTAMPTZ NOT NULL,
  observed_at TIMESTAMPTZ NOT NULL,
  fetched_at TIMESTAMPTZ NOT NULL,
  available_at TIMESTAMPTZ NOT NULL,
  metrics JSONB NOT NULL CHECK (jsonb_typeof(metrics)='object'),
  raw_blob_id UUID NOT NULL,
  raw_content_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(raw_content_digest::TEXT)),
  raw_size_bytes BIGINT NOT NULL CHECK (raw_size_bytes BETWEEN 1 AND 2097152),
  body JSONB NOT NULL CHECK (jsonb_typeof(body)='object'),
  CHECK (fetched_at>=observed_at AND available_at>=fetched_at)
);
CREATE TRIGGER research_gexbot_live_evidence_immutable BEFORE UPDATE OR DELETE ON agent_control.research_gexbot_live_evidence
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.research_gexbot_live_tool_receipt (
  receipt_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(receipt_id)),
  tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_gexbot_live_tool_call_intent(tool_call_id),
  evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_gexbot_live_evidence(evidence_id),
  record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
  request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
  executor_principal_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(executor_principal_id)),
  completed_at TIMESTAMPTZ NOT NULL,
  body JSONB NOT NULL CHECK (jsonb_typeof(body)='object')
);
CREATE TRIGGER research_gexbot_live_tool_receipt_immutable BEFORE UPDATE OR DELETE ON agent_control.research_gexbot_live_tool_receipt
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.cortex_gexbot_live_tool_receipt_ack (
  tool_call_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_gexbot_live_tool_call_intent(tool_call_id),
  receipt_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_gexbot_live_tool_receipt(receipt_id),
  receipt_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(receipt_digest::TEXT)),
  evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_gexbot_live_evidence(evidence_id),
  evidence_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(evidence_digest::TEXT)),
  acknowledged_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(acknowledged_by)),
  acknowledged_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER cortex_gexbot_live_tool_receipt_ack_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_gexbot_live_tool_receipt_ack
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.authorize_cortex_gexbot_live(
  p_source_call_id TEXT,p_attempt_id TEXT,p_lease_generation BIGINT,p_lease_token UUID,p_symbol TEXT,p_category TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; source_row RECORD; existing agent_control.cortex_gexbot_live_tool_call_intent%ROWTYPE;
  intent_id TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp(); request_body JSONB; request_digest CHAR(64); body JSONB; intent_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id) OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
    OR p_lease_generation<1 OR p_lease_token IS NULL OR p_symbol<>'SPX'
    OR p_category NOT IN ('gex_full','gex_zero','gex_one') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Cortex GEXBOT live authorization';
  END IF;
  PERFORM agent_control.enforce_cortex_specialist_tool_grant(p_source_call_id,'market_gexbot_live');
  SELECT manifest.call_id,result.result_id,result.record_digest AS result_digest,turn.run_id,turn.task_id,turn.turn_id,
         attempt.attempt_id,task.budget_ledger_id INTO STRICT source_row
    FROM agent_control.runtime_model_call_manifest manifest
    JOIN agent_control.runtime_model_call_result result ON result.call_id=manifest.call_id
    JOIN agent_control.runtime_turn turn ON turn.turn_id=result.turn_id
    JOIN agent_control.runtime_attempt attempt ON attempt.attempt_id=result.attempt_id
    JOIN agent_control.runtime_task task ON task.task_id=turn.task_id AND task.run_id=turn.run_id
    JOIN agent_control.runtime_run run ON run.run_id=turn.run_id
    WHERE manifest.call_id=p_source_call_id FOR UPDATE OF attempt,task,run;
  SELECT * INTO existing FROM agent_control.cortex_gexbot_live_tool_call_intent WHERE source_call_id=p_source_call_id;
  IF FOUND THEN
    IF existing.request_symbol<>p_symbol OR existing.request_category<>p_category THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='Cortex GEXBOT live intent identity conflict';
    END IF;
    RETURN jsonb_build_object('status','authorized','tool_call_id',existing.tool_call_id,'tool_id',existing.tool_id,
      'request_digest',existing.request_digest::TEXT,'symbol',existing.request_symbol,'category',existing.request_category);
  END IF;
  IF source_row.attempt_id<>p_attempt_id OR NOT EXISTS (
    SELECT 1 FROM agent_control.runtime_attempt attempt WHERE attempt.attempt_id=p_attempt_id
      AND attempt.state='executing' AND attempt.lease_generation=p_lease_generation AND attempt.lease_token=p_lease_token
      AND attempt.lease_expires_at>at_time AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
      AND attempt.lease_worker->>'kind'='workload' AND attempt.lease_worker->>'audience'='worker'
  ) THEN RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex GEXBOT live lease denied'; END IF;
  request_body:=jsonb_build_object('symbol',p_symbol,'category',p_category);
  request_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_live_request.v1',request_body);
  IF NOT agent_control.runtime_charge_tool_budget_ancestors(source_row.run_id,source_row.budget_ledger_id,invoker.principal_id,p_source_call_id,p_source_call_id,at_time) THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='Cortex tool budget denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'tool_call_id',intent_id,'tool_id','market_gexbot_live',
    'source_result',jsonb_build_object('owner','agent_control','record_type','model_call_result','record_id',source_row.result_id,'schema_revision',1,'record_digest',source_row.result_digest::TEXT),
    'request',request_body,'request_digest',request_digest::TEXT,
    'authorized_by',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'authorized_at',agent_control.runtime_utc_text(at_time));
  intent_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_live_tool_call_intent.v1',body);
  INSERT INTO agent_control.cortex_gexbot_live_tool_call_intent(
    tool_call_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,tool_id,request_digest,
    request_symbol,request_category,record_digest,authorized_by,authorized_at
  ) VALUES(intent_id,source_row.call_id,source_row.result_id,source_row.run_id,source_row.task_id,source_row.attempt_id,
    source_row.turn_id,'market_gexbot_live',request_digest,p_symbol,p_category,intent_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','authorized','tool_call_id',intent_id,'tool_id','market_gexbot_live',
    'request_digest',request_digest::TEXT,'symbol',p_symbol,'category',p_category);
END $$;

CREATE FUNCTION agent_control.get_cortex_gexbot_live_authorization(p_tool_call_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_gexbot_live_tool_call_intent%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
    OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Research GEXBOT live authorization denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_gexbot_live_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  RETURN jsonb_build_object('tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id,'request_digest',intent.request_digest::TEXT,
    'symbol',intent.request_symbol,'category',intent.request_category);
END $$;

CREATE FUNCTION agent_control.get_research_gexbot_live_receipt(p_tool_call_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; receipt agent_control.research_gexbot_live_tool_receipt%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
    OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Research GEXBOT live receipt denied';
  END IF;
  SELECT * INTO receipt FROM agent_control.research_gexbot_live_tool_receipt WHERE tool_call_id=p_tool_call_id FOR SHARE;
  IF NOT FOUND THEN RETURN NULL; END IF;
  RETURN jsonb_build_object('receipt',receipt.body,'evidence',
    (SELECT body FROM agent_control.research_gexbot_live_evidence WHERE evidence_id=receipt.evidence_id));
END $$;

CREATE FUNCTION agent_control.record_research_gexbot_live_receipt(p_tool_call_id TEXT,p_observation JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_gexbot_live_tool_call_intent%ROWTYPE;
  existing agent_control.research_gexbot_live_tool_receipt%ROWTYPE;
  evidence_id_value TEXT:=gen_random_uuid()::TEXT; receipt_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
  evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64);
  source_value TIMESTAMPTZ; observed_value TIMESTAMPTZ; fetched_value TIMESTAMPTZ; available_value TIMESTAMPTZ;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
    OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_observation)<>'object' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid normalized GEXBOT live evidence';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_gexbot_live_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.research_gexbot_live_tool_receipt WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN RETURN jsonb_build_object('receipt',existing.body,'evidence',
    (SELECT body FROM agent_control.research_gexbot_live_evidence WHERE evidence_id=existing.evidence_id)); END IF;
  IF p_observation->>'available'<>'true'
    OR p_observation-ARRAY['available','schema_revision','observation_id','provider','provider_revision','source_kind','symbol','category',
      'source_timestamp','observed_at','fetched_at','available_at','ingested_at','raw','metrics','quality_state','record_digest']<>'{}'::JSONB
    OR p_observation->>'schema_revision'<>'1' OR p_observation->>'provider'<>'gexbot_classic'
    OR p_observation->>'provider_revision'<>'gexbot_classic_v1' OR p_observation->>'source_kind'<>'provider_poll'
    OR p_observation->>'quality_state'<>'accepted' OR p_observation->>'symbol'<>intent.request_symbol
    OR p_observation->>'category'<>intent.request_category
    OR p_observation->>'observation_id' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    OR p_observation->>'record_digest' !~ '^[0-9a-f]{64}$' OR jsonb_typeof(p_observation->'metrics')<>'object'
    OR jsonb_typeof(p_observation->'raw')<>'object' OR p_observation->'raw'-ARRAY['blob_id','content_digest','size_bytes']<>'{}'::JSONB
    OR p_observation#>>'{raw,blob_id}' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    OR p_observation#>>'{raw,content_digest}' !~ '^[0-9a-f]{64}$' OR jsonb_typeof(p_observation#>'{raw,size_bytes}')<>'number'
    OR (p_observation#>>'{raw,size_bytes}')::BIGINT NOT BETWEEN 1 AND 2097152
    OR NOT agent_control.runtime_utc_instant_json(p_observation->'source_timestamp')
    OR NOT agent_control.runtime_utc_instant_json(p_observation->'observed_at')
    OR NOT agent_control.runtime_utc_instant_json(p_observation->'fetched_at')
    OR NOT agent_control.runtime_utc_instant_json(p_observation->'available_at') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid GEXBOT live observation';
  END IF;
  source_value:=(p_observation->>'source_timestamp')::TIMESTAMPTZ;
  observed_value:=(p_observation->>'observed_at')::TIMESTAMPTZ;
  fetched_value:=(p_observation->>'fetched_at')::TIMESTAMPTZ;
  available_value:=(p_observation->>'available_at')::TIMESTAMPTZ;
  IF fetched_value<observed_value OR available_value<fetched_value OR available_value>at_time THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='GEXBOT live observation time order invalid';
  END IF;
  evidence_body:=jsonb_build_object('schema_revision',1,'evidence_id',evidence_id_value,'tool_call_id',p_tool_call_id,
    'provider','gexbot_classic','symbol',intent.request_symbol,'category',intent.request_category,
    'observation_id',p_observation->>'observation_id','observation_digest',p_observation->>'record_digest',
    'source_timestamp',agent_control.runtime_utc_text(source_value),'observed_at',agent_control.runtime_utc_text(observed_value),
    'fetched_at',agent_control.runtime_utc_text(fetched_value),'available_at',agent_control.runtime_utc_text(available_value),
    'metrics',p_observation->'metrics','raw',p_observation->'raw');
  evidence_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_live_evidence.v1',evidence_body);
  INSERT INTO agent_control.research_gexbot_live_evidence(
    evidence_id,tool_call_id,record_digest,provider,symbol,category,observation_id,observation_digest,
    source_timestamp,observed_at,fetched_at,available_at,metrics,raw_blob_id,raw_content_digest,raw_size_bytes,body
  ) VALUES(evidence_id_value,p_tool_call_id,evidence_digest,'gexbot_classic',intent.request_symbol,intent.request_category,
    (p_observation->>'observation_id')::UUID,p_observation->>'record_digest',source_value,observed_value,fetched_value,available_value,
    p_observation->'metrics',(p_observation#>>'{raw,blob_id}')::UUID,p_observation#>>'{raw,content_digest}',
    (p_observation#>>'{raw,size_bytes}')::BIGINT,evidence_body);
  receipt_body:=jsonb_build_object('schema_revision',1,'receipt_id',receipt_id_value,'tool_call_id',p_tool_call_id,
    'tool_id','market_gexbot_live','request_digest',intent.request_digest::TEXT,'state','succeeded',
    'evidence',jsonb_build_object('owner','research_gateway','record_type','gexbot_live_evidence','record_id',evidence_id_value,'schema_revision',1,'record_digest',evidence_digest::TEXT),
    'executor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','research_gateway'),
    'completed_at',agent_control.runtime_utc_text(at_time));
  receipt_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_live_tool_receipt.v1',receipt_body);
  INSERT INTO agent_control.research_gexbot_live_tool_receipt(
    receipt_id,tool_call_id,evidence_id,record_digest,request_digest,executor_principal_id,completed_at,body
  ) VALUES(receipt_id_value,p_tool_call_id,evidence_id_value,receipt_digest,intent.request_digest,invoker.principal_id,at_time,receipt_body);
  RETURN jsonb_build_object('receipt',receipt_body,'evidence',evidence_body);
END $$;

CREATE FUNCTION agent_control.record_cortex_gexbot_live_receipt(p_tool_call_id TEXT,p_receipt JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_gexbot_live_tool_call_intent%ROWTYPE;
  receipt agent_control.research_gexbot_live_tool_receipt%ROWTYPE; evidence agent_control.research_gexbot_live_evidence%ROWTYPE;
  existing agent_control.cortex_gexbot_live_tool_receipt_ack%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_receipt)<>'object' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex GEXBOT live receipt denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_gexbot_live_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO STRICT receipt FROM agent_control.research_gexbot_live_tool_receipt WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO STRICT evidence FROM agent_control.research_gexbot_live_evidence WHERE evidence_id=receipt.evidence_id FOR SHARE;
  IF p_receipt<>receipt.body OR receipt.request_digest<>intent.request_digest THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='GEXBOT live receipt mismatch';
  END IF;
  SELECT * INTO existing FROM agent_control.cortex_gexbot_live_tool_receipt_ack WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN
    RETURN jsonb_build_object('status','recorded','receipt_id',existing.receipt_id,'evidence_id',existing.evidence_id);
  END IF;
  INSERT INTO agent_control.cortex_gexbot_live_tool_receipt_ack(
    tool_call_id,receipt_id,receipt_digest,evidence_id,evidence_digest,acknowledged_by,acknowledged_at
  ) VALUES(p_tool_call_id,receipt.receipt_id,receipt.record_digest,evidence.evidence_id,evidence.record_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','recorded','receipt_id',receipt.receipt_id,'evidence_id',evidence.evidence_id);
END $$;

DO $$
DECLARE definition TEXT; marker TEXT; injection TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.get_cortex_run_trace(text)'::regprocedure) INTO definition;
  marker:='      ) raw';
  injection:=E'        UNION ALL SELECT intent.authorized_at,50,''gexbot-live-tool-authorized:''||intent.tool_call_id,jsonb_build_object(''created_at'',agent_control.runtime_utc_text(intent.authorized_at),''stage'',''tool_call_authorized'',''tool_call_id'',intent.tool_call_id,''tool_id'',intent.tool_id) FROM agent_control.cortex_gexbot_live_tool_call_intent intent WHERE intent.run_id=p_run_id\n        UNION ALL SELECT ack.acknowledged_at,60,''gexbot-live-tool-receipt:''||ack.tool_call_id,jsonb_build_object(''created_at'',agent_control.runtime_utc_text(ack.acknowledged_at),''stage'',''tool_receipt_succeeded'',''tool_call_id'',ack.tool_call_id,''receipt_id'',ack.receipt_id) FROM agent_control.cortex_gexbot_live_tool_receipt_ack ack JOIN agent_control.cortex_gexbot_live_tool_call_intent intent ON intent.tool_call_id=ack.tool_call_id WHERE intent.run_id=p_run_id\n'||marker;
  IF position(marker IN definition)=0 THEN RAISE EXCEPTION 'GEXBOT live trace injection point missing'; END IF;
  EXECUTE replace(definition,marker,injection);
END $$;

REVOKE ALL ON TABLE agent_control.cortex_gexbot_live_tool_call_intent,agent_control.research_gexbot_live_evidence,
  agent_control.research_gexbot_live_tool_receipt,agent_control.cortex_gexbot_live_tool_receipt_ack FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.authorize_cortex_gexbot_live(TEXT,TEXT,BIGINT,UUID,TEXT,TEXT),
  agent_control.get_cortex_gexbot_live_authorization(TEXT),agent_control.get_research_gexbot_live_receipt(TEXT),
  agent_control.record_research_gexbot_live_receipt(TEXT,JSONB),agent_control.record_cortex_gexbot_live_receipt(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.authorize_cortex_gexbot_live(TEXT,TEXT,BIGINT,UUID,TEXT,TEXT),
  agent_control.record_cortex_gexbot_live_receipt(TEXT,JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_gexbot_live_authorization(TEXT),
  agent_control.get_research_gexbot_live_receipt(TEXT),agent_control.record_research_gexbot_live_receipt(TEXT,JSONB) TO alpheus_research_gateway;

RESET ROLE;
