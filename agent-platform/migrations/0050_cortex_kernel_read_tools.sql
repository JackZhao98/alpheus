-- Register every reviewed Robinhood MCP read/preflight capability behind one
-- versioned Cortex protocol. The registry is an allowlist, not discovery:
-- only these exact Tool/source pairs can be authorized.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.kernel_read_tool_registry (
    tool_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(tool_id)),
    source_tool TEXT NOT NULL UNIQUE CHECK (agent_control.runtime_identifier_valid(source_tool)),
    category TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(category)),
    description TEXT NOT NULL CHECK (description<>'' AND octet_length(description)<=1000),
    target_roles JSONB NOT NULL CHECK (jsonb_typeof(target_roles)='array' AND jsonb_array_length(target_roles)>0),
    effect TEXT NOT NULL CHECK (effect IN ('read_only','read_only_preflight')),
    active BOOLEAN NOT NULL DEFAULT true,
    UNIQUE(tool_id,source_tool)
);
CREATE TRIGGER kernel_read_tool_registry_immutable BEFORE UPDATE OR DELETE ON agent_control.kernel_read_tool_registry
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

INSERT INTO agent_control.kernel_read_tool_registry(tool_id,source_tool,category,description,target_roles,effect) VALUES
('kernel_accounts','get_accounts','portfolio','Read identity and account facts only for the bound brokerage account.','["position_manager","decision_desk"]','read_only'),
('kernel_earnings_calendar','get_earnings_calendar','catalyst','Read a bounded set of upcoming earnings dates.','["intent","decision_desk"]','read_only'),
('kernel_equity_fundamentals','get_equity_fundamentals','fundamentals','Read provider fundamental and valuation fields for explicit equities.','["intent","decision_desk"]','read_only'),
('kernel_equity_historicals','get_equity_historicals','market','Read bounded historical equity-price bars.','["intent","decision_desk"]','read_only'),
('kernel_equity_orders','get_equity_orders','portfolio','Read bounded equity-order history and states for the bound account.','["position_manager","decision_desk"]','read_only'),
('kernel_equity_positions','get_equity_positions','portfolio','Read equity positions for the bound brokerage account.','["position_manager","decision_desk"]','read_only'),
('kernel_equity_price_book','get_equity_price_book','market','Read a point-in-time equity bid, ask, and price-book snapshot.','["intent","decision_desk"]','read_only'),
('kernel_equity_quotes','get_equity_quotes','market','Read a point-in-time equity quote snapshot.','["intent","decision_desk"]','read_only'),
('kernel_equity_tax_lots','get_equity_tax_lots','portfolio','Read equity tax lots for the bound brokerage account.','["position_manager","decision_desk"]','read_only'),
('kernel_equity_technical_indicators','get_equity_technical_indicators','market','Read one explicitly requested technical indicator over a bounded interval.','["intent","decision_desk"]','read_only'),
('kernel_equity_tradability','get_equity_tradability','market','Read equity tradability and market-status facts.','["intent","decision_desk"]','read_only'),
('kernel_financials','get_financials','fundamentals','Read bounded financial-statement data for explicit equities.','["intent","decision_desk"]','read_only'),
('kernel_index_quotes','get_index_quotes','market','Read a point-in-time index quote snapshot.','["intent","decision_desk"]','read_only'),
('kernel_indexes','get_indexes','market','Resolve explicit index symbols to provider index facts.','["intent","decision_desk"]','read_only'),
('kernel_option_chains','get_option_chains','options','Read bounded option-chain metadata for an explicit underlying.','["intent","decision_desk"]','read_only'),
('kernel_option_instruments','get_option_instruments','options','Read terms and provider IDs for bounded option instruments.','["intent","decision_desk"]','read_only'),
('kernel_option_level_upgrade_info','get_option_level_upgrade_info','portfolio','Read option-eligibility facts for the bound account.','["position_manager","decision_desk"]','read_only'),
('kernel_option_orders','get_option_orders','portfolio','Read bounded option-order history and states for the bound account.','["position_manager","decision_desk"]','read_only'),
('kernel_option_positions','get_option_positions','portfolio','Read option positions for the bound brokerage account.','["position_manager","decision_desk"]','read_only'),
('kernel_option_quotes','get_option_quotes','options','Read point-in-time quotes for bounded option instruments.','["intent","decision_desk"]','read_only'),
('kernel_option_watchlist','get_option_watchlist','options','Read the existing option-watchlist snapshot without altering it.','["intent","decision_desk"]','read_only'),
('kernel_pnl_trade_history','get_pnl_trade_history','portfolio','Read a bounded history of realized trade P&L.','["position_manager","decision_desk"]','read_only'),
('kernel_popular_watchlists','get_popular_watchlists','discovery','Read public popular-watchlist metadata.','["intent","decision_desk"]','read_only'),
('kernel_portfolio','get_portfolio','portfolio','Read a portfolio summary for the bound brokerage account.','["position_manager","decision_desk"]','read_only'),
('kernel_realized_pnl','get_realized_pnl','portfolio','Read a bounded realized-P&L summary.','["position_manager","decision_desk"]','read_only'),
('kernel_scanner_filter_specs','get_scanner_filter_specs','discovery','Read supported scanner-filter definitions.','["intent","decision_desk"]','read_only'),
('kernel_scans','get_scans','discovery','Read available scanner definitions.','["intent","decision_desk"]','read_only'),
('kernel_watchlist_items','get_watchlist_items','discovery','Read the contents of one explicit watchlist ID.','["intent","decision_desk"]','read_only'),
('kernel_watchlists','get_watchlists','discovery','Read public or bound-account watchlist metadata.','["intent","decision_desk"]','read_only'),
('kernel_review_equity_order','review_equity_order','preflight','Simulate and validate an explicit equity order; never create it.','["intent","decision_desk"]','read_only_preflight'),
('kernel_review_option_order','review_option_order','preflight','Simulate and validate an explicit option order; never create it.','["intent","decision_desk"]','read_only_preflight'),
('kernel_run_scan','run_scan','discovery','Run one approved scanner ID with bounded filter inputs.','["intent","decision_desk"]','read_only'),
('kernel_search','search','discovery','Resolve an asset name or symbol to bounded provider identifiers.','["intent","decision_desk"]','read_only');

CREATE TABLE agent_control.cortex_kernel_read_tool_call_intent (
    tool_call_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(tool_call_id)),
    source_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_manifest(call_id),
    source_result_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_model_call_result(result_id),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
    attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
    turn_id TEXT NOT NULL REFERENCES agent_control.runtime_turn(turn_id),
    tool_id TEXT NOT NULL,
    source_tool TEXT NOT NULL,
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    request_args JSONB NOT NULL CHECK (jsonb_typeof(request_args)='object' AND octet_length(request_args::TEXT)<=12288 AND NOT request_args ? 'account_number'),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    authorized_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(authorized_by)),
    authorized_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY(tool_id,source_tool) REFERENCES agent_control.kernel_read_tool_registry(tool_id,source_tool),
    UNIQUE(source_call_id,tool_id,request_digest)
);
CREATE TRIGGER cortex_kernel_read_tool_call_intent_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_kernel_read_tool_call_intent
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.kernel_read_evidence (
    evidence_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(evidence_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_kernel_read_tool_call_intent(tool_call_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    tool_id TEXT NOT NULL,
    source_tool TEXT NOT NULL,
    provider TEXT NOT NULL CHECK (provider='kernel_robinhood_mcp'),
    result_json TEXT NOT NULL CHECK (octet_length(result_json) BETWEEN 2 AND 65536 AND jsonb_typeof(result_json::JSONB) IN ('object','array')),
    result_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(result_digest::TEXT)),
    observed_at TIMESTAMPTZ NOT NULL,
    available_at TIMESTAMPTZ NOT NULL,
    body JSONB NOT NULL CHECK (jsonb_typeof(body)='object'),
    CHECK (available_at>=observed_at),
    FOREIGN KEY(tool_id,source_tool) REFERENCES agent_control.kernel_read_tool_registry(tool_id,source_tool)
);
CREATE TRIGGER kernel_read_evidence_immutable BEFORE UPDATE OR DELETE ON agent_control.kernel_read_evidence
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.kernel_read_tool_receipt (
    receipt_id TEXT PRIMARY KEY CHECK (agent_control.runtime_identifier_valid(receipt_id)),
    tool_call_id TEXT NOT NULL UNIQUE REFERENCES agent_control.cortex_kernel_read_tool_call_intent(tool_call_id),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.kernel_read_evidence(evidence_id),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    request_digest CHAR(64) NOT NULL CHECK (agent_control.runtime_digest_valid(request_digest::TEXT)),
    executor_principal_id TEXT NOT NULL CHECK (executor_principal_id='kernel-1'),
    completed_at TIMESTAMPTZ NOT NULL,
    body JSONB NOT NULL CHECK (jsonb_typeof(body)='object')
);
CREATE TRIGGER kernel_read_tool_receipt_immutable BEFORE UPDATE OR DELETE ON agent_control.kernel_read_tool_receipt
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE TABLE agent_control.cortex_kernel_read_tool_receipt_ack (
    tool_call_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_kernel_read_tool_call_intent(tool_call_id),
    receipt_id TEXT NOT NULL UNIQUE REFERENCES agent_control.kernel_read_tool_receipt(receipt_id),
    receipt_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(receipt_digest::TEXT)),
    evidence_id TEXT NOT NULL UNIQUE REFERENCES agent_control.kernel_read_evidence(evidence_id),
    evidence_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(evidence_digest::TEXT)),
    acknowledged_by TEXT NOT NULL CHECK (agent_control.runtime_identifier_valid(acknowledged_by)),
    acknowledged_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER cortex_kernel_read_tool_receipt_ack_immutable BEFORE UPDATE OR DELETE ON agent_control.cortex_kernel_read_tool_receipt_ack
FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.authorize_cortex_kernel_read(
    p_source_call_id TEXT,p_attempt_id TEXT,p_lease_generation BIGINT,p_lease_token UUID,
    p_tool_id TEXT,p_source_tool TEXT,p_arguments JSONB
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; source_row RECORD; existing agent_control.cortex_kernel_read_tool_call_intent%ROWTYPE;
  intent_id TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp(); body JSONB; intent_digest CHAR(64);
  request_body JSONB; request_digest CHAR(64); arguments_text TEXT;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id) OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
    OR p_lease_generation<1 OR p_lease_token IS NULL OR jsonb_typeof(p_arguments)<>'object'
    OR octet_length(p_arguments::TEXT)>12288 OR p_arguments ? 'account_number'
    OR NOT EXISTS (SELECT 1 FROM agent_control.kernel_read_tool_registry registry
      WHERE registry.tool_id=p_tool_id AND registry.source_tool=p_source_tool AND registry.active) THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Cortex Kernel read authorization';
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
  SELECT * INTO existing FROM agent_control.cortex_kernel_read_tool_call_intent WHERE source_call_id=p_source_call_id;
  IF FOUND THEN
    IF existing.tool_id<>p_tool_id OR existing.source_tool<>p_source_tool OR existing.request_args<>p_arguments THEN
      RAISE EXCEPTION USING ERRCODE='23505',MESSAGE='Cortex Kernel read intent identity conflict';
    END IF;
    RETURN jsonb_build_object('status','authorized','tool_call_id',existing.tool_call_id,'tool_id',existing.tool_id,
      'source_tool',existing.source_tool,'request_digest',existing.request_digest::TEXT,'arguments',existing.request_args);
  END IF;
  IF source_row.attempt_id<>p_attempt_id OR NOT EXISTS (SELECT 1 FROM agent_control.runtime_attempt attempt WHERE attempt.attempt_id=p_attempt_id
      AND attempt.state='executing' AND attempt.lease_generation=p_lease_generation AND attempt.lease_token=p_lease_token
      AND attempt.lease_expires_at>at_time AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
      AND attempt.lease_worker->>'kind'='workload' AND attempt.lease_worker->>'audience'='worker') THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Kernel read authorization lease denied';
  END IF;
  arguments_text:=p_arguments::TEXT;
  request_body:=jsonb_build_object('tool_id',p_tool_id,'source_tool',p_source_tool,'arguments_json',arguments_text);
  request_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_read_request.v1',request_body);
  IF NOT agent_control.runtime_charge_tool_budget_ancestors(source_row.run_id,source_row.budget_ledger_id,invoker.principal_id,
       p_source_call_id,p_source_call_id,at_time) THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='Cortex tool budget denied';
  END IF;
  body:=jsonb_build_object('schema_revision',1,'tool_call_id',intent_id,'tool_id',p_tool_id,'source_tool',p_source_tool,
    'source_result',jsonb_build_object('owner','agent_control','record_type','model_call_result','record_id',source_row.result_id,
      'schema_revision',1,'record_digest',source_row.result_digest::TEXT),'request',request_body,'request_digest',request_digest::TEXT,
    'authorized_by',jsonb_build_object('principal_id',invoker.principal_id,'kind','workload','audience','control_api'),
    'authorized_at',agent_control.runtime_utc_text(at_time));
  intent_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_read_tool_call_intent.v1',body);
  INSERT INTO agent_control.cortex_kernel_read_tool_call_intent(tool_call_id,source_call_id,source_result_id,run_id,task_id,attempt_id,turn_id,
      tool_id,source_tool,request_digest,request_args,record_digest,authorized_by,authorized_at)
    VALUES(intent_id,source_row.call_id,source_row.result_id,source_row.run_id,source_row.task_id,source_row.attempt_id,source_row.turn_id,
      p_tool_id,p_source_tool,request_digest,p_arguments,intent_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('status','authorized','tool_call_id',intent_id,'tool_id',p_tool_id,'source_tool',p_source_tool,
    'request_digest',request_digest::TEXT,'arguments',p_arguments);
END $$;

CREATE FUNCTION agent_control.record_cortex_kernel_read(p_tool_call_id TEXT,p_observation JSONB)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD; intent agent_control.cortex_kernel_read_tool_call_intent%ROWTYPE; existing agent_control.kernel_read_tool_receipt%ROWTYPE;
  evidence_id_value TEXT:=gen_random_uuid()::TEXT; receipt_id_value TEXT:=gen_random_uuid()::TEXT; at_time TIMESTAMPTZ:=clock_timestamp();
  evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64); result_digest_value CHAR(64);
  observed_value TIMESTAMPTZ; available_value TIMESTAMPTZ; result_text TEXT;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_tool_call_id) OR jsonb_typeof(p_observation)<>'object' THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='Cortex Kernel read receipt denied';
  END IF;
  SELECT * INTO STRICT intent FROM agent_control.cortex_kernel_read_tool_call_intent WHERE tool_call_id=p_tool_call_id FOR SHARE;
  SELECT * INTO existing FROM agent_control.kernel_read_tool_receipt WHERE tool_call_id=p_tool_call_id;
  IF FOUND THEN
    RETURN jsonb_build_object('receipt',existing.body,'evidence',(SELECT body FROM agent_control.kernel_read_evidence WHERE evidence_id=existing.evidence_id));
  END IF;
  IF (p_observation-ARRAY['schema_revision','tool_call_id','tool_id','request_digest','provider','source_tool','result_json','observed_at','available_at'])<>'{}'::JSONB
     OR p_observation->>'schema_revision'<>'1' OR p_observation->>'tool_call_id'<>p_tool_call_id
     OR p_observation->>'tool_id'<>intent.tool_id OR p_observation->>'source_tool'<>intent.source_tool
     OR p_observation->>'request_digest'<>intent.request_digest::TEXT OR p_observation->>'provider'<>'kernel_robinhood_mcp'
     OR jsonb_typeof(p_observation->'result_json')<>'string' OR octet_length(p_observation->>'result_json') NOT BETWEEN 2 AND 65536
     OR jsonb_typeof((p_observation->>'result_json')::JSONB) NOT IN ('object','array')
     OR NOT agent_control.runtime_utc_instant_json(p_observation->'observed_at')
     OR NOT agent_control.runtime_utc_instant_json(p_observation->'available_at') THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Kernel read observation';
  END IF;
  observed_value:=(p_observation->>'observed_at')::TIMESTAMPTZ;
  available_value:=(p_observation->>'available_at')::TIMESTAMPTZ;
  IF observed_value>available_value THEN
    RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid Kernel read observation time';
  END IF;
  result_text:=p_observation->>'result_json';
  result_digest_value:=encode(digest(convert_to(result_text,'UTF8'),'sha256'),'hex');
  evidence_body:=jsonb_build_object('schema_revision',1,'evidence_id',evidence_id_value,'tool_call_id',p_tool_call_id,
    'tool_id',intent.tool_id,'provider','kernel_robinhood_mcp','source_tool',intent.source_tool,'result_json',result_text,
    'result_digest',result_digest_value::TEXT,'observed_at',agent_control.runtime_utc_text(observed_value),
    'available_at',agent_control.runtime_utc_text(available_value));
  evidence_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_read_evidence.v1',evidence_body);
  INSERT INTO agent_control.kernel_read_evidence(evidence_id,tool_call_id,record_digest,tool_id,source_tool,provider,result_json,result_digest,observed_at,available_at,body)
    VALUES(evidence_id_value,p_tool_call_id,evidence_digest,intent.tool_id,intent.source_tool,'kernel_robinhood_mcp',result_text,result_digest_value,observed_value,available_value,evidence_body);
  receipt_body:=jsonb_build_object('schema_revision',1,'receipt_id',receipt_id_value,'tool_call_id',p_tool_call_id,
    'tool_id',intent.tool_id,'request_digest',intent.request_digest::TEXT,'state','succeeded',
    'evidence',jsonb_build_object('owner','kernel','record_type','kernel_read_evidence','record_id',evidence_id_value,
      'schema_revision',1,'record_digest',evidence_digest::TEXT),
    'executor',jsonb_build_object('principal_id','kernel-1','kind','kernel','audience','kernel'),
    'completed_at',agent_control.runtime_utc_text(at_time));
  receipt_digest:=agent_control.runtime_contract_digest('agent-platform.contract.kernel_read_tool_receipt.v1',receipt_body);
  INSERT INTO agent_control.kernel_read_tool_receipt(receipt_id,tool_call_id,evidence_id,record_digest,request_digest,executor_principal_id,completed_at,body)
    VALUES(receipt_id_value,p_tool_call_id,evidence_id_value,receipt_digest,intent.request_digest,'kernel-1',at_time,receipt_body);
  INSERT INTO agent_control.cortex_kernel_read_tool_receipt_ack(tool_call_id,receipt_id,receipt_digest,evidence_id,evidence_digest,acknowledged_by,acknowledged_at)
    VALUES(p_tool_call_id,receipt_id_value,receipt_digest,evidence_id_value,evidence_digest,invoker.principal_id,at_time);
  RETURN jsonb_build_object('receipt',receipt_body,'evidence',evidence_body);
END $$;

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.get_cortex_run_trace(text)'::regprocedure) INTO definition;
  IF position('kernel-read-tool-authorized:' IN definition)>0 THEN
    RETURN;
  END IF;
  IF position('kernel-earnings-tool-authorized:' IN definition)=0 OR position('kernel-earnings-tool-receipt:' IN definition)=0 THEN
    RAISE EXCEPTION 'expected Cortex trace Kernel markers';
  END IF;
  definition:=replace(definition,
    '        UNION ALL SELECT intent.authorized_at,50,''kernel-earnings-tool-authorized:''',
    '        UNION ALL SELECT intent.authorized_at,50,''kernel-read-tool-authorized:''||intent.tool_call_id,jsonb_build_object(''created_at'',agent_control.runtime_utc_text(intent.authorized_at),''stage'',''tool_call_authorized'',''tool_call_id'',intent.tool_call_id,''tool_id'',intent.tool_id) FROM agent_control.cortex_kernel_read_tool_call_intent intent WHERE intent.run_id=p_run_id
        UNION ALL SELECT intent.authorized_at,50,''kernel-earnings-tool-authorized:''');
  definition:=replace(definition,
    '        UNION ALL SELECT ack.acknowledged_at,60,''kernel-earnings-tool-receipt:''',
    '        UNION ALL SELECT ack.acknowledged_at,60,''kernel-read-tool-receipt:''||ack.tool_call_id,jsonb_build_object(''created_at'',agent_control.runtime_utc_text(ack.acknowledged_at),''stage'',''tool_receipt_succeeded'',''tool_call_id'',ack.tool_call_id,''receipt_id'',ack.receipt_id) FROM agent_control.cortex_kernel_read_tool_receipt_ack ack JOIN agent_control.cortex_kernel_read_tool_call_intent intent ON intent.tool_call_id=ack.tool_call_id WHERE intent.run_id=p_run_id
        UNION ALL SELECT ack.acknowledged_at,60,''kernel-earnings-tool-receipt:''');
  IF position('kernel-read-tool-authorized:' IN definition)=0 OR position('kernel-read-tool-receipt:' IN definition)=0 THEN
    RAISE EXCEPTION 'Cortex Kernel read trace replacement failed';
  END IF;
  EXECUTE definition;
END $$;

REVOKE ALL ON TABLE agent_control.kernel_read_tool_registry,agent_control.cortex_kernel_read_tool_call_intent,
  agent_control.kernel_read_evidence,agent_control.kernel_read_tool_receipt,agent_control.cortex_kernel_read_tool_receipt_ack FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.authorize_cortex_kernel_read(TEXT,TEXT,BIGINT,UUID,TEXT,TEXT,JSONB) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.record_cortex_kernel_read(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.authorize_cortex_kernel_read(TEXT,TEXT,BIGINT,UUID,TEXT,TEXT,JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_kernel_read(TEXT,JSONB) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_trace(TEXT) TO alpheus_agent_control_api;

RESET ROLE;
