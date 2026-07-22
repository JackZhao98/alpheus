-- AP3's first real cross-plane Tool: an explicitly bounded public web fetch.
-- Cortex Control owns authorization and the durable intent/acknowledgement;
-- Research Gateway owns connector execution, normalized untrusted Evidence,
-- and the durable Tool receipt.  No credential, generic HTTP primitive, or
-- external mutation is added here.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';


CREATE FUNCTION agent_control.cortex_web_fetch_url_valid(p_value TEXT)
RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
  SELECT p_value IS NOT NULL AND p_value=btrim(p_value)
     AND octet_length(p_value) BETWEEN 12 AND 4000
     AND p_value ~ '^https?://[^[:space:]@/]+(/|$)'
     AND p_value !~ '^https?://(localhost|127[.]|0[.]|10[.]|192[.]168[.]|169[.]254[.]|172[.](1[6-9]|2[0-9]|3[0-1])[.])'
     AND p_value !~ '[[:space:]]'
$$;

CREATE TABLE agent_control.cortex_tool_call_intent (
    tool_call_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(tool_call_id)),
    source_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_manifest(call_id),
    source_result_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_result(result_id),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
    attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
    turn_id TEXT NOT NULL REFERENCES agent_control.runtime_turn(turn_id),
    tool_id TEXT NOT NULL CHECK (tool_id='research_web_fetch'),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    request_url TEXT NOT NULL CHECK (agent_control.cortex_web_fetch_url_valid(request_url)),
    request_max_chars INTEGER NOT NULL CHECK (request_max_chars BETWEEN 1 AND 12000),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    authorized_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(authorized_by)),
    authorized_at TIMESTAMPTZ NOT NULL,
    UNIQUE (source_call_id, tool_id, request_digest)
);
CREATE TRIGGER cortex_tool_call_intent_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_tool_call_intent
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.research_web_fetch_evidence (
    evidence_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(evidence_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_tool_call_intent(tool_call_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    source TEXT NOT NULL CHECK (source='web-page-untrusted'),
    source_url TEXT NOT NULL CHECK (agent_control.cortex_web_fetch_url_valid(source_url)),
    title TEXT NOT NULL DEFAULT '' CHECK (octet_length(title)<=1000),
    content_type TEXT NOT NULL CHECK (content_type IN ('text/html','application/xhtml+xml','text/plain','application/json')),
    text_content TEXT NOT NULL CHECK (octet_length(text_content) BETWEEN 1 AND 12000),
    truncated BOOLEAN NOT NULL,
    content_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(content_digest::TEXT)),
    observed_at TIMESTAMPTZ NOT NULL,
    available_at TIMESTAMPTZ NOT NULL,
    archived_at TIMESTAMPTZ NOT NULL,
    body JSONB NOT NULL,
    CHECK (available_at>=observed_at AND archived_at>=available_at)
);
CREATE TRIGGER research_web_fetch_evidence_immutable BEFORE UPDATE OR DELETE ON agent_control.research_web_fetch_evidence
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.research_tool_receipt (
    receipt_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(receipt_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_tool_call_intent(tool_call_id),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_web_fetch_evidence(evidence_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    executor_principal_id TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(executor_principal_id)),
    completed_at TIMESTAMPTZ NOT NULL,
    body JSONB NOT NULL
);
CREATE TRIGGER research_tool_receipt_immutable BEFORE UPDATE OR DELETE ON agent_control.research_tool_receipt
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.cortex_tool_receipt_ack (
    tool_call_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_tool_call_intent(tool_call_id),
    receipt_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_tool_receipt(receipt_id),
    receipt_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(receipt_digest::TEXT)),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.research_web_fetch_evidence(evidence_id),
    evidence_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(evidence_digest::TEXT)),
    acknowledged_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(acknowledged_by)),
    acknowledged_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER cortex_tool_receipt_ack_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_tool_receipt_ack
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- Tool charging follows the same root-to-leaf lock order as Model-call
-- accounting.  The Task's own ledger budgets children, so work performed by a
-- Task charges its parent ancestry only.
CREATE FUNCTION agent_control.runtime_charge_tool_budget_ancestors(
    p_run_id TEXT, p_task_ledger_id TEXT, p_principal_id TEXT,
    p_causation_id TEXT, p_correlation_id TEXT, p_updated_at TIMESTAMPTZ
) RETURNS BOOLEAN LANGUAGE plpgsql VOLATILE STRICT AS $$
DECLARE ledger_ids TEXT[]; current_id TEXT; row_value agent_control.runtime_budget_ledger%ROWTYPE;
  next_state TEXT; root_seen BOOLEAN:=false; changed_count INTEGER;
BEGIN
  WITH RECURSIVE chain AS (
    SELECT ledger.ledger_id,ledger.parent_ledger_id,0 AS depth,ARRAY[ledger.ledger_id]::TEXT[] AS path
      FROM agent_control.runtime_budget_ledger ledger WHERE ledger.ledger_id=(
        SELECT task_ledger.parent_ledger_id FROM agent_control.runtime_budget_ledger task_ledger
        WHERE task_ledger.ledger_id=p_task_ledger_id AND task_ledger.scope='task')
    UNION ALL
    SELECT parent.ledger_id,parent.parent_ledger_id,child.depth+1,child.path||parent.ledger_id
      FROM chain child JOIN agent_control.runtime_budget_ledger parent ON parent.ledger_id=child.parent_ledger_id
      WHERE child.depth<4096 AND NOT parent.ledger_id=ANY(child.path)
  ) SELECT array_agg(ledger_id ORDER BY depth DESC,ledger_id) INTO ledger_ids FROM chain;
  IF ledger_ids IS NULL OR cardinality(ledger_ids)<1 THEN RETURN false; END IF;
  FOREACH current_id IN ARRAY ledger_ids LOOP
    SELECT * INTO STRICT row_value FROM agent_control.runtime_budget_ledger WHERE ledger_id=current_id FOR UPDATE;
    IF row_value.scope='run' AND row_value.scope_id=p_run_id AND row_value.parent_ledger_id IS NULL THEN root_seen:=true; END IF;
    IF row_value.state<>'open' OR row_value.consumed_tool_calls>=row_value.limit_tool_calls-row_value.reserved_tool_calls THEN RETURN false; END IF;
  END LOOP;
  IF NOT root_seen THEN RETURN false; END IF;
  FOREACH current_id IN ARRAY ledger_ids LOOP
    SELECT * INTO STRICT row_value FROM agent_control.runtime_budget_ledger WHERE ledger_id=current_id;
    next_state:=CASE WHEN row_value.consumed_tool_calls+1=row_value.limit_tool_calls THEN 'exhausted' ELSE row_value.state END;
    UPDATE agent_control.runtime_budget_ledger SET consumed_tool_calls=consumed_tool_calls+1,
      generation=generation+1,state=next_state,updated_at=greatest(p_updated_at,updated_at) WHERE ledger_id=current_id;
    GET DIAGNOSTICS changed_count=ROW_COUNT;
    IF changed_count<>1 THEN RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='locked tool budget ancestry changed'; END IF;
    IF next_state<>row_value.state THEN
      PERFORM agent_control.runtime_insert_event('budget',row_value.ledger_id,row_value.state,next_state,row_value.generation+1,
        p_principal_id,p_causation_id,p_correlation_id,'tool_budget_exhausted',p_updated_at);
    END IF;
  END LOOP;
  RETURN true;
EXCEPTION WHEN NO_DATA_FOUND THEN RETURN false;
END $$;

CREATE FUNCTION agent_control.authorize_cortex_web_fetch(
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
  SELECT * INTO existing FROM agent_control.cortex_tool_call_intent WHERE source_call_id=p_source_call_id;
  IF FOUND THEN
    IF existing.request_url<>p_url OR existing.request_max_chars<>p_max_chars THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex tool intent identity conflict';
    END IF;
    RETURN jsonb_build_object('status','authorized','tool_call_id',existing.tool_call_id,'tool_id',existing.tool_id,
      'request_digest',existing.request_digest::TEXT,'url',existing.request_url,'max_chars',existing.request_max_chars);
  END IF;
  IF source_row.attempt_id<>p_attempt_id OR NOT (source_row.attempt_id=p_attempt_id)
     OR NOT EXISTS (SELECT 1 FROM agent_control.runtime_attempt attempt WHERE attempt.attempt_id=p_attempt_id
       AND attempt.state='executing' AND attempt.lease_generation=p_lease_generation AND attempt.lease_token=p_lease_token
       AND attempt.lease_expires_at>at_time AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
       AND attempt.lease_worker->>'kind'='workload' AND attempt.lease_worker->>'audience'='worker') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex tool authorization lease denied';
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

CREATE FUNCTION agent_control.get_cortex_web_fetch_authorization(p_tool_call_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_tool_call_intent%ROWTYPE;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='research tool authorization read denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  RETURN jsonb_build_object('tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id,'request_digest',intent.request_digest::TEXT,
    'url',intent.request_url,'max_chars',intent.request_max_chars);
END $$;

CREATE FUNCTION agent_control.record_research_web_fetch_receipt(p_tool_call_id TEXT,p_document JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_tool_call_intent%ROWTYPE; existing agent_control.research_tool_receipt%ROWTYPE;
  evidence_id_value TEXT:=gen_random_uuid()::TEXT; receipt_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
  observed TIMESTAMPTZ; evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_research_gateway' OR invoker.owner_id<>'research_gateway'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_document)<>'object'
     OR NOT (p_document ?& ARRAY['available','source','url','content_type','text','truncated','retrieved_at'])
     OR p_document-ARRAY['available','source','url','title','content_type','text','truncated','retrieved_at']<>'{}'::JSONB
     OR jsonb_typeof(p_document->'available')<>'boolean' OR p_document->>'available'<>'true'
     OR p_document->>'source'<>'web-page-untrusted' OR NOT agent_control.cortex_web_fetch_url_valid(p_document->>'url')
     OR octet_length(coalesce(p_document->>'title',''))>1000 OR p_document->>'content_type' NOT IN ('text/html','application/xhtml+xml','text/plain','application/json')
     OR jsonb_typeof(p_document->'text')<>'string' OR octet_length(p_document->>'text') NOT BETWEEN 1 AND 12000
     OR jsonb_typeof(p_document->'truncated')<>'boolean' OR NOT agent_control.runtime_utc_instant_json(p_document->'retrieved_at') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid normalized web evidence';
  END IF;
  observed:=(p_document->>'retrieved_at')::TIMESTAMPTZ;
  IF observed>at_time THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='future web evidence denied'; END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.research_tool_receipt WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN
    RETURN jsonb_build_object('receipt',existing.body,'evidence',(SELECT body FROM agent_control.research_web_fetch_evidence WHERE evidence_id=existing.evidence_id));
  END IF;
  evidence_body:=jsonb_build_object('schema_revision',1,'evidence_id',evidence_id_value,'tool_call_id',p_tool_call_id,
    'source','web-page-untrusted','url',p_document->>'url','title',coalesce(p_document->>'title',''),'content_type',p_document->>'content_type',
    'text',p_document->>'text','truncated',(p_document->>'truncated')::BOOLEAN,
    'content_digest',encode(sha256(convert_to(p_document->>'text','UTF8')),'hex'),
    'observed_at',agent_control.runtime_utc_text(observed),'available_at',agent_control.runtime_utc_text(observed),'archived_at',agent_control.runtime_utc_text(at_time));
  evidence_digest:=agent_control.runtime_contract_digest('agent-platform.contract.web_fetch_evidence.v1',evidence_body);
  INSERT INTO agent_control.research_web_fetch_evidence(evidence_id,tool_call_id,record_digest,source,source_url,title,content_type,text_content,truncated,
      content_digest,observed_at,available_at,archived_at,body)
    VALUES(evidence_id_value,p_tool_call_id,evidence_digest,'web-page-untrusted',p_document->>'url',coalesce(p_document->>'title',''),p_document->>'content_type',
      p_document->>'text',(p_document->>'truncated')::BOOLEAN,evidence_body->>'content_digest',observed,observed,at_time,evidence_body);
  receipt_body:=jsonb_build_object('schema_revision',1,'receipt_id',receipt_id_value,'tool_call_id',p_tool_call_id,
    'tool_id','research_web_fetch','request_digest',intent.request_digest::TEXT,'state','succeeded',
    'evidence',jsonb_build_object('owner','research_gateway','record_type','web_fetch_evidence','record_id',evidence_id_value,'schema_revision',1,'record_digest',evidence_digest::TEXT),
    'executor',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','research_gateway'),
    'completed_at',agent_control.runtime_utc_text(at_time));
  receipt_digest:=agent_control.runtime_contract_digest('agent-platform.contract.tool_receipt.v1',receipt_body);
  INSERT INTO agent_control.research_tool_receipt(receipt_id,tool_call_id,evidence_id,record_digest,request_digest,executor_principal_id,completed_at,body)
    VALUES(receipt_id_value,p_tool_call_id,evidence_id_value,receipt_digest,intent.request_digest,invoker.principal_id,at_time,receipt_body);
  RETURN jsonb_build_object('receipt',receipt_body,'evidence',evidence_body);
END $$;

CREATE FUNCTION agent_control.record_cortex_tool_receipt(p_tool_call_id TEXT,p_receipt JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_tool_call_intent%ROWTYPE; received agent_control.research_tool_receipt%ROWTYPE;
  evidence agent_control.research_web_fetch_evidence%ROWTYPE; existing agent_control.cortex_tool_receipt_ack%ROWTYPE; at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_receipt)<>'object' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex tool receipt denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO STRICT received FROM agent_control.research_tool_receipt WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO STRICT evidence FROM agent_control.research_web_fetch_evidence WHERE evidence_id=received.evidence_id FOR SHARE;
  IF received.request_digest<>intent.request_digest OR received.body<>p_receipt
     OR p_receipt->>'tool_call_id'<>p_tool_call_id OR p_receipt->>'tool_id'<>'research_web_fetch'
     OR p_receipt->>'state'<>'succeeded' OR p_receipt->>'request_digest'<>intent.request_digest::TEXT
     OR p_receipt#>>'{evidence,record_id}'<>evidence.evidence_id OR p_receipt#>>'{evidence,record_digest}'<>evidence.record_digest::TEXT THEN
    RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex tool receipt identity conflict';
  END IF;
  SELECT * INTO existing FROM agent_control.cortex_tool_receipt_ack WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN
    IF existing.receipt_id<>received.receipt_id OR existing.receipt_digest<>received.record_digest
       OR existing.evidence_id<>evidence.evidence_id OR existing.evidence_digest<>evidence.record_digest THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='cortex tool receipt acknowledgement conflict';
    END IF;
    RETURN jsonb_build_object('status','recorded','receipt_id',existing.receipt_id,'evidence_id',existing.evidence_id);
  END IF;
  INSERT INTO agent_control.cortex_tool_receipt_ack(tool_call_id,receipt_id,receipt_digest,evidence_id,evidence_digest,acknowledged_by,acknowledged_at)
    VALUES(p_tool_call_id,received.receipt_id,received.record_digest,evidence.evidence_id,evidence.record_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','recorded','receipt_id',received.receipt_id,'evidence_id',evidence.evidence_id);
END $$;

CREATE OR REPLACE FUNCTION agent_control.get_cortex_run_trace(p_run_id TEXT)
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
    UNION ALL
    SELECT intent.authorized_at,turn.ordinal*10+6,jsonb_build_object('sequence',turn.ordinal*10+6,'created_at',agent_control.runtime_utc_text(intent.authorized_at),
      'stage','tool_call_authorized','tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id) FROM agent_control.cortex_tool_call_intent intent
      JOIN agent_control.runtime_turn turn ON turn.turn_id=intent.turn_id WHERE intent.run_id=p_run_id
    UNION ALL
    SELECT ack.acknowledged_at,turn.ordinal*10+7,jsonb_build_object('sequence',turn.ordinal*10+7,'created_at',agent_control.runtime_utc_text(ack.acknowledged_at),
      'stage','tool_receipt_succeeded','tool_call_id',ack.tool_call_id,'receipt_id',ack.receipt_id) FROM agent_control.cortex_tool_receipt_ack ack
      JOIN agent_control.cortex_tool_call_intent intent ON intent.tool_call_id=ack.tool_call_id
      JOIN agent_control.runtime_turn turn ON turn.turn_id=intent.turn_id WHERE intent.run_id=p_run_id
  ) event),'[]'::JSONB);
END $$;

REVOKE ALL ON TABLE agent_control.cortex_tool_call_intent,agent_control.cortex_tool_receipt_ack FROM PUBLIC;
REVOKE ALL ON TABLE agent_control.research_web_fetch_evidence,agent_control.research_tool_receipt FROM PUBLIC;
GRANT USAGE ON SCHEMA agent_control TO alpheus_research_gateway;
REVOKE ALL ON FUNCTION agent_control.cortex_web_fetch_url_valid(TEXT),agent_control.runtime_charge_tool_budget_ancestors(TEXT,TEXT,TEXT,TEXT,TEXT,TIMESTAMPTZ) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.authorize_cortex_web_fetch(TEXT,TEXT,BIGINT,UUID,TEXT,INTEGER) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_web_fetch_authorization(TEXT),agent_control.record_research_web_fetch_receipt(TEXT,JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.record_cortex_tool_receipt(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.authorize_cortex_web_fetch(TEXT,TEXT,BIGINT,UUID,TEXT,INTEGER) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_tool_receipt(TEXT,JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_web_fetch_authorization(TEXT) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION agent_control.record_research_web_fetch_receipt(TEXT,JSONB) TO alpheus_research_gateway;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_trace(TEXT) TO alpheus_agent_control_api;
RESET ROLE;
