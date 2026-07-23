-- GEXBOT is a separate Research Provider, but Cortex may consume one bounded
-- archived observation through an immutable, read-only Tool receipt.  This is
-- deliberately parallel to the early web-fetch slice: it does not pretend to
-- be the general AP3 registry and it grants no collection or raw-payload
-- authority to Cortex.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_gexbot_tool_call_intent (
    tool_call_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(tool_call_id)),
    source_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_manifest(call_id),
    source_result_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_result(result_id),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
    attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
    turn_id TEXT NOT NULL REFERENCES agent_control.runtime_turn(turn_id),
    tool_id TEXT NOT NULL CHECK (tool_id='research_gexbot_as_of'),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    request_symbol TEXT NOT NULL CHECK (request_symbol ~ '^[A-Z0-9._-]{1,16}$'),
    request_category TEXT NOT NULL CHECK (request_category IN ('gex_full','gex_zero','gex_one')),
    request_as_of TIMESTAMPTZ NOT NULL,
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    authorized_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(authorized_by)),
    authorized_at TIMESTAMPTZ NOT NULL,
    CHECK (request_as_of <= authorized_at),
    UNIQUE (source_call_id,tool_id,request_digest)
);
CREATE TRIGGER cortex_gexbot_tool_call_intent_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_gexbot_tool_call_intent
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.research_gexbot_as_of_evidence (
    evidence_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(evidence_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_gexbot_tool_call_intent(tool_call_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    provider TEXT NOT NULL CHECK (provider='gexbot_classic'),
    available BOOLEAN NOT NULL,
    symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z0-9._-]{1,16}$'),
    category TEXT NOT NULL CHECK (category IN ('gex_full','gex_zero','gex_one')),
    as_of TIMESTAMPTZ NOT NULL,
    observation_id UUID,
    observation_digest CHAR(64),
    observed_at TIMESTAMPTZ,
    available_at TIMESTAMPTZ,
    metrics JSONB NOT NULL CHECK (jsonb_typeof(metrics)='object'),
    raw_blob_id UUID,
    raw_content_digest CHAR(64),
    raw_size_bytes BIGINT,
    body JSONB NOT NULL CHECK (jsonb_typeof(body)='object'),
    CHECK ((NOT available AND observation_id IS NULL AND observation_digest IS NULL AND observed_at IS NULL AND available_at IS NULL AND raw_blob_id IS NULL AND raw_content_digest IS NULL AND raw_size_bytes IS NULL AND metrics='{}'::JSONB)
       OR (available AND observation_id IS NOT NULL AND observation_digest IS NOT NULL AND observed_at IS NOT NULL AND available_at IS NOT NULL AND raw_blob_id IS NOT NULL AND raw_content_digest IS NOT NULL AND raw_size_bytes BETWEEN 1 AND 2097152 AND available_at >= observed_at AND available_at <= as_of))
);
CREATE TRIGGER research_gexbot_as_of_evidence_immutable BEFORE UPDATE OR DELETE ON agent_control.research_gexbot_as_of_evidence
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.research_gexbot_tool_receipt (
    receipt_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(receipt_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_gexbot_tool_call_intent(tool_call_id),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_gexbot_as_of_evidence(evidence_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    executor_principal_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(executor_principal_id)),
    completed_at TIMESTAMPTZ NOT NULL,
    body JSONB NOT NULL
);
CREATE TRIGGER research_gexbot_tool_receipt_immutable BEFORE UPDATE OR DELETE ON agent_control.research_gexbot_tool_receipt
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.cortex_gexbot_tool_receipt_ack (
    tool_call_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_gexbot_tool_call_intent(tool_call_id),
    receipt_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_gexbot_tool_receipt(receipt_id),
    receipt_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(receipt_digest::TEXT)),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_gexbot_as_of_evidence(evidence_id),
    evidence_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(evidence_digest::TEXT)),
    acknowledged_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(acknowledged_by)),
    acknowledged_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER cortex_gexbot_tool_receipt_ack_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_gexbot_tool_receipt_ack
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.authorize_cortex_gexbot_as_of(
    p_source_call_id TEXT,p_attempt_id TEXT,p_lease_generation BIGINT,p_lease_token UUID,
    p_symbol TEXT,p_category TEXT,p_as_of TIMESTAMPTZ
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; source_row RECORD; existing agent_control.cortex_gexbot_tool_call_intent%ROWTYPE;
  intent_id TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp(); body JSONB; intent_digest CHAR(64); request_body JSONB; request_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id) OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
    OR p_lease_generation<1 OR p_lease_token IS NULL OR p_symbol !~ '^[A-Z0-9._-]{1,16}$'
    OR p_category NOT IN ('gex_full','gex_zero','gex_one') OR p_as_of IS NULL OR p_as_of>at_time THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Cortex GEXBOT authorization';
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
  SELECT * INTO existing FROM agent_control.cortex_gexbot_tool_call_intent WHERE source_call_id=p_source_call_id;
  IF FOUND THEN
    IF existing.request_symbol<>p_symbol OR existing.request_category<>p_category OR existing.request_as_of<>p_as_of THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='Cortex GEXBOT tool intent identity conflict';
    END IF;
    RETURN jsonb_build_object('status','authorized','tool_call_id',existing.tool_call_id,'tool_id',existing.tool_id,
      'request_digest',existing.request_digest::TEXT,'symbol',existing.request_symbol,'category',existing.request_category,
      'as_of',agent_control.runtime_utc_text(existing.request_as_of));
  END IF;
  IF source_row.attempt_id<>p_attempt_id OR NOT EXISTS (SELECT 1 FROM agent_control.runtime_attempt attempt WHERE attempt.attempt_id=p_attempt_id
      AND attempt.state='executing' AND attempt.lease_generation=p_lease_generation AND attempt.lease_token=p_lease_token
      AND attempt.lease_expires_at>at_time AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
      AND attempt.lease_worker->>'kind'='workload' AND attempt.lease_worker->>'audience'='worker') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex GEXBOT authorization lease denied';
  END IF;
  request_body:=jsonb_build_object('symbol',p_symbol,'category',p_category,'as_of',agent_control.runtime_utc_text(p_as_of));
  request_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_as_of_request.v1',request_body);
  IF NOT agent_control.runtime_charge_tool_budget_ancestors(source_row.run_id,source_row.budget_ledger_id,invoker.principal_id,
       p_source_call_id,p_source_call_id,at_time) THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='Cortex tool budget denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'tool_call_id',intent_id,'tool_id','research_gexbot_as_of',
    'source_result',jsonb_build_object('owner','agent_control','record_type','model_call_result','record_id',source_row.result_id,
      'schema_revision',1,'record_digest',source_row.result_digest::TEXT),'request',request_body,'request_digest',request_digest::TEXT,
    'authorized_by',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'authorized_at',agent_control.runtime_utc_text(at_time));
  intent_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_as_of_tool_call_intent.v1',body);
  INSERT INTO agent_control.cortex_gexbot_tool_call_intent(tool_call_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,
      tool_id,request_digest,request_symbol,request_category,request_as_of,record_digest,authorized_by,authorized_at)
    VALUES(intent_id,source_row.call_id,source_row.result_id,source_row.run_id,source_row.task_id,source_row.attempt_id,source_row.turn_id,
      'research_gexbot_as_of',request_digest,p_symbol,p_category,p_as_of,intent_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','authorized','tool_call_id',intent_id,'tool_id','research_gexbot_as_of',
    'request_digest',request_digest::TEXT,'symbol',p_symbol,'category',p_category,'as_of',agent_control.runtime_utc_text(p_as_of));
END $$;

CREATE FUNCTION agent_control.get_cortex_gexbot_as_of_authorization(p_tool_call_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_gexbot_tool_call_intent%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Research GEXBOT authorization read denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_gexbot_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  RETURN jsonb_build_object('tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id,'request_digest',intent.request_digest::TEXT,
    'symbol',intent.request_symbol,'category',intent.request_category,'as_of',agent_control.runtime_utc_text(intent.request_as_of));
END $$;

CREATE FUNCTION agent_control.get_research_gexbot_as_of_receipt(p_tool_call_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; receipt agent_control.research_gexbot_tool_receipt%ROWTYPE; evidence agent_control.research_gexbot_as_of_evidence%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Research GEXBOT receipt read denied';
  END IF;
  SELECT * INTO receipt FROM agent_control.research_gexbot_tool_receipt WHERE tool_call_id=p_tool_call_id FOR SHARE;
  IF NOT FOUND THEN RETURN NULL; END IF;
  SELECT * INTO STRICT evidence FROM agent_control.research_gexbot_as_of_evidence WHERE evidence_id=receipt.evidence_id FOR SHARE;
  RETURN jsonb_build_object('receipt',receipt.body,'evidence',evidence.body);
END $$;

CREATE FUNCTION agent_control.record_research_gexbot_as_of_receipt(p_tool_call_id TEXT,p_observation JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_gexbot_tool_call_intent%ROWTYPE; existing agent_control.research_gexbot_tool_receipt%ROWTYPE;
  evidence_id_value TEXT:=gen_random_uuid()::TEXT; receipt_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
  evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64); observed_value TIMESTAMPTZ; available_value TIMESTAMPTZ;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_observation)<>'object' THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid normalized GEXBOT evidence';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_gexbot_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.research_gexbot_tool_receipt WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN
    RETURN jsonb_build_object('receipt',existing.body,'evidence',(SELECT body FROM agent_control.research_gexbot_as_of_evidence WHERE evidence_id=existing.evidence_id));
  END IF;
  IF p_observation->>'available'='false' THEN
    IF p_observation-ARRAY['available','symbol','category','as_of']<>'{}'::JSONB OR jsonb_typeof(p_observation->'available')<>'boolean'
       OR p_observation->>'symbol'<>intent.request_symbol OR p_observation->>'category'<>intent.request_category
       OR NOT agent_control.runtime_utc_instant_json(p_observation->'as_of') OR (p_observation->>'as_of')::TIMESTAMPTZ<>intent.request_as_of THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid unavailable GEXBOT evidence';
    END IF;
    evidence_body:=jsonb_build_object('schema_revision',1,'evidence_id',evidence_id_value,'tool_call_id',p_tool_call_id,
      'provider','gexbot_classic','available',false,'symbol',intent.request_symbol,'category',intent.request_category,
      'as_of',agent_control.runtime_utc_text(intent.request_as_of));
    evidence_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_as_of_evidence.v1',evidence_body);
    INSERT INTO agent_control.research_gexbot_as_of_evidence(evidence_id,tool_call_id,record_digest,provider,available,symbol,category,as_of,metrics,body)
      VALUES(evidence_id_value,p_tool_call_id,evidence_digest,'gexbot_classic',false,intent.request_symbol,intent.request_category,intent.request_as_of,'{}'::JSONB,evidence_body);
  ELSE
    IF p_observation->>'available'<>'true' OR p_observation-ARRAY['available','schema_revision','observation_id','provider','provider_revision','source_kind','symbol','category','source_timestamp','observed_at','fetched_at','available_at','ingested_at','raw','metrics','quality_state','record_digest']<>'{}'::JSONB
       OR p_observation->>'schema_revision'<>'1' OR p_observation->>'provider'<>'gexbot_classic' OR p_observation->>'provider_revision'<>'gexbot_classic_v1'
       OR p_observation->>'quality_state'<>'accepted' OR p_observation->>'symbol'<>intent.request_symbol OR p_observation->>'category'<>intent.request_category
       OR p_observation->>'observation_id' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       OR p_observation->>'record_digest' !~ '^[0-9a-f]{64}$' OR jsonb_typeof(p_observation->'metrics')<>'object'
       OR jsonb_typeof(p_observation->'raw')<>'object' OR p_observation->'raw'-ARRAY['blob_id','content_digest','size_bytes']<>'{}'::JSONB
       OR p_observation#>>'{raw,blob_id}' !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
       OR p_observation#>>'{raw,content_digest}' !~ '^[0-9a-f]{64}$' OR jsonb_typeof(p_observation#>'{raw,size_bytes}')<>'number'
       OR (p_observation#>>'{raw,size_bytes}')::BIGINT NOT BETWEEN 1 AND 2097152
       OR NOT agent_control.runtime_utc_instant_json(p_observation->'observed_at') OR NOT agent_control.runtime_utc_instant_json(p_observation->'available_at') THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid available GEXBOT evidence';
    END IF;
    observed_value:=(p_observation->>'observed_at')::TIMESTAMPTZ;
    available_value:=(p_observation->>'available_at')::TIMESTAMPTZ;
    IF observed_value>available_value OR available_value>intent.request_as_of THEN
      RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='GEXBOT evidence violates as_of fence';
    END IF;
    evidence_body:=jsonb_build_object('schema_revision',1,'evidence_id',evidence_id_value,'tool_call_id',p_tool_call_id,
      'provider','gexbot_classic','available',true,'symbol',intent.request_symbol,'category',intent.request_category,
      'as_of',agent_control.runtime_utc_text(intent.request_as_of),'observation_id',p_observation->>'observation_id',
      'observation_digest',p_observation->>'record_digest','observed_at',agent_control.runtime_utc_text(observed_value),
      'available_at',agent_control.runtime_utc_text(available_value),'metrics',p_observation->'metrics','raw',p_observation->'raw');
    evidence_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_as_of_evidence.v1',evidence_body);
    INSERT INTO agent_control.research_gexbot_as_of_evidence(
      evidence_id,tool_call_id,record_digest,provider,available,symbol,category,as_of,observation_id,observation_digest,observed_at,available_at,
      metrics,raw_blob_id,raw_content_digest,raw_size_bytes,body
    ) VALUES(
      evidence_id_value,p_tool_call_id,evidence_digest,'gexbot_classic',true,intent.request_symbol,intent.request_category,intent.request_as_of,
      (p_observation->>'observation_id')::UUID,p_observation->>'record_digest',observed_value,available_value,p_observation->'metrics',
      (p_observation#>>'{raw,blob_id}')::UUID,p_observation#>>'{raw,content_digest}',(p_observation#>>'{raw,size_bytes}')::BIGINT,evidence_body
    );
  END IF;
  receipt_body:=jsonb_build_object('schema_revision',1,'receipt_id',receipt_id_value,'tool_call_id',p_tool_call_id,
    'tool_id','research_gexbot_as_of','request_digest',intent.request_digest::TEXT,'state','succeeded',
    'evidence',jsonb_build_object('owner','research_gateway','record_type','gexbot_as_of_evidence','record_id',evidence_id_value,'schema_revision',1,'record_digest',evidence_digest::TEXT),
    'executor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','research_gateway'),
    'completed_at',agent_control.runtime_utc_text(at_time));
  receipt_digest:=agent_control.runtime_contract_digest('agent-platform.contract.gexbot_as_of_tool_receipt.v1',receipt_body);
  INSERT INTO agent_control.research_gexbot_tool_receipt(receipt_id,tool_call_id,evidence_id,record_digest,request_digest,executor_principal_id,completed_at,body)
    VALUES(receipt_id_value,p_tool_call_id,evidence_id_value,receipt_digest,intent.request_digest,invoker.principal_id,at_time,receipt_body);
  RETURN jsonb_build_object('receipt',receipt_body,'evidence',evidence_body);
END $$;

CREATE FUNCTION agent_control.record_cortex_gexbot_tool_receipt(p_tool_call_id TEXT,p_receipt JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_gexbot_tool_call_intent%ROWTYPE; received agent_control.research_gexbot_tool_receipt%ROWTYPE;
  evidence agent_control.research_gexbot_as_of_evidence%ROWTYPE; existing agent_control.cortex_gexbot_tool_receipt_ack%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_receipt)<>'object' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex GEXBOT receipt denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_gexbot_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO STRICT received FROM agent_control.research_gexbot_tool_receipt WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO STRICT evidence FROM agent_control.research_gexbot_as_of_evidence WHERE evidence_id=received.evidence_id FOR SHARE;
  IF received.request_digest<>intent.request_digest OR received.body<>p_receipt OR p_receipt->>'tool_call_id'<>p_tool_call_id
     OR p_receipt->>'tool_id'<>'research_gexbot_as_of' OR p_receipt->>'state'<>'succeeded'
     OR p_receipt->>'request_digest'<>intent.request_digest::TEXT OR p_receipt#>>'{evidence,record_id}'<>evidence.evidence_id
     OR p_receipt#>>'{evidence,record_digest}'<>evidence.record_digest::TEXT THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='Cortex GEXBOT receipt identity conflict';
  END IF;
  SELECT * INTO existing FROM agent_control.cortex_gexbot_tool_receipt_ack WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN
    IF existing.receipt_id<>received.receipt_id OR existing.receipt_digest<>received.record_digest
       OR existing.evidence_id<>evidence.evidence_id OR existing.evidence_digest<>evidence.record_digest THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='Cortex GEXBOT acknowledgement conflict';
    END IF;
    RETURN jsonb_build_object('status','recorded','receipt_id',existing.receipt_id,'evidence_id',existing.evidence_id);
  END IF;
  INSERT INTO agent_control.cortex_gexbot_tool_receipt_ack(tool_call_id,receipt_id,receipt_digest,evidence_id,evidence_digest,acknowledged_by,acknowledged_at)
    VALUES(p_tool_call_id,received.receipt_id,received.record_digest,evidence.evidence_id,evidence.record_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','recorded','receipt_id',received.receipt_id,'evidence_id',evidence.evidence_id);
END $$;

-- The trace must expose the exact authorized GEXBOT call and receipt.  It
-- never exposes Provider payload bytes or secret/provider identifiers.
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
      ) raw
    ) event
  ),'[]'::JSONB);
END $$;

REVOKE ALL ON TABLE agent_control.cortex_gexbot_tool_call_intent,agent_control.research_gexbot_as_of_evidence,
  agent_control.research_gexbot_tool_receipt,agent_control.cortex_gexbot_tool_receipt_ack FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.authorize_cortex_gexbot_as_of(TEXT,TEXT,BIGINT,UUID,TEXT,TEXT,TIMESTAMPTZ) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_gexbot_as_of_authorization(TEXT),agent_control.get_research_gexbot_as_of_receipt(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.record_research_gexbot_as_of_receipt(TEXT,JSONB),agent_control.record_cortex_gexbot_tool_receipt(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.authorize_cortex_gexbot_as_of(TEXT,TEXT,BIGINT,UUID,TEXT,TEXT,TIMESTAMPTZ) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_gexbot_tool_receipt(TEXT,JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_gexbot_as_of_authorization(TEXT) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION agent_control.get_research_gexbot_as_of_receipt(TEXT) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION agent_control.record_research_gexbot_as_of_receipt(TEXT,JSONB) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_trace(TEXT) TO alpheus_agent_control_api;

RESET ROLE;
