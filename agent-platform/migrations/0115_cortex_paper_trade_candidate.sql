-- Effect-free Paper Candidate proposals. A Candidate is immutable analysis
-- output bound to one resolved model result and active Worker lease. It is not
-- an approval, order, or execution authority.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_paper_trade_candidate (
  candidate_id TEXT PRIMARY KEY CHECK (
    agent_control.runtime_identifier_valid(candidate_id)
  ),
  source_call_id TEXT NOT NULL UNIQUE REFERENCES
    agent_control.runtime_model_call_manifest(call_id),
  source_result_id TEXT NOT NULL UNIQUE REFERENCES
    agent_control.runtime_model_call_result(result_id),
  run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
  task_id TEXT NOT NULL REFERENCES agent_control.runtime_task(task_id),
  attempt_id TEXT NOT NULL REFERENCES agent_control.runtime_attempt(attempt_id),
  turn_id TEXT NOT NULL REFERENCES agent_control.runtime_turn(turn_id),
  strategy_id TEXT NOT NULL CHECK (
    strategy_id ~ '^[a-z][a-z0-9_-]{0,63}$'
  ),
  symbol TEXT NOT NULL CHECK (symbol ~ '^[A-Z][A-Z0-9.^-]{0,15}$'),
  kind TEXT NOT NULL CHECK (kind='equity'),
  side TEXT NOT NULL CHECK (side IN ('buy','sell')),
  qty NUMERIC(20,6) NOT NULL CHECK (qty>0 AND qty<=1000),
  thesis TEXT NOT NULL CHECK (
    thesis<>'' AND thesis=btrim(thesis) AND octet_length(thesis)<=4000
  ),
  invalidation TEXT NOT NULL CHECK (
    invalidation<>'' AND invalidation=btrim(invalidation)
    AND octet_length(invalidation)<=2000
  ),
  confidence_bps INTEGER NOT NULL CHECK (confidence_bps BETWEEN 0 AND 10000),
  proposal JSONB NOT NULL CHECK (jsonb_typeof(proposal)='object'),
  record_digest CHAR(64) NOT NULL UNIQUE CHECK (
    agent_control.runtime_digest_valid(record_digest::TEXT)
  ),
  proposed_by TEXT NOT NULL CHECK (
    agent_control.runtime_identifier_valid(proposed_by)
  ),
  proposed_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY (task_id,run_id) REFERENCES
    agent_control.runtime_task(task_id,run_id),
  UNIQUE (candidate_id,record_digest)
);

CREATE TRIGGER cortex_paper_trade_candidate_immutable
BEFORE UPDATE OR DELETE ON agent_control.cortex_paper_trade_candidate
FOR EACH ROW EXECUTE FUNCTION
  agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.admit_cortex_paper_trade_candidate(
  p_source_call_id TEXT,
  p_attempt_id TEXT,
  p_lease_generation BIGINT,
  p_lease_token UUID,
  p_proposal JSONB
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  source_row RECORD;
  existing agent_control.cortex_paper_trade_candidate%ROWTYPE;
  candidate_id_value TEXT:=gen_random_uuid()::TEXT;
  at_time TIMESTAMPTZ:=clock_timestamp();
  qty_value NUMERIC(20,6);
  confidence_value INTEGER;
  body JSONB;
  record_digest_value CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker
  FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
    OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id)
    OR NOT agent_control.runtime_identifier_valid(p_attempt_id)
    OR p_lease_generation<1 OR p_lease_token IS NULL
    OR jsonb_typeof(p_proposal)<>'object'
    OR p_proposal-ARRAY[
      'schema_revision','strategy_id','symbol','kind','side','qty',
      'thesis','invalidation','confidence_bps'
    ]<>'{}'::JSONB
    OR p_proposal->>'schema_revision'<>'1'
    OR COALESCE(p_proposal->>'strategy_id','')
      !~ '^[a-z][a-z0-9_-]{0,63}$'
    OR COALESCE(p_proposal->>'symbol','')
      !~ '^[A-Z][A-Z0-9.^-]{0,15}$'
    OR p_proposal->>'kind'<>'equity'
    OR p_proposal->>'side' NOT IN ('buy','sell')
    OR jsonb_typeof(p_proposal->'qty')<>'number'
    OR jsonb_typeof(p_proposal->'confidence_bps')<>'number'
    OR COALESCE(p_proposal->>'thesis','')=''
    OR p_proposal->>'thesis'<>btrim(p_proposal->>'thesis')
    OR octet_length(p_proposal->>'thesis')>4000
    OR COALESCE(p_proposal->>'invalidation','')=''
    OR p_proposal->>'invalidation'<>btrim(p_proposal->>'invalidation')
    OR octet_length(p_proposal->>'invalidation')>2000 THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='invalid Cortex Paper Candidate';
  END IF;
  BEGIN
    qty_value:=(p_proposal->>'qty')::NUMERIC(20,6);
    confidence_value:=(p_proposal->>'confidence_bps')::INTEGER;
  EXCEPTION WHEN OTHERS THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='invalid Cortex Paper Candidate numeric value';
  END;
  IF qty_value<=0 OR qty_value>1000
    OR to_jsonb(qty_value)<>p_proposal->'qty'
    OR confidence_value<0 OR confidence_value>10000
    OR to_jsonb(confidence_value)<>p_proposal->'confidence_bps' THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='invalid Cortex Paper Candidate bounds';
  END IF;

  SELECT manifest.call_id,result.result_id,
         result.record_digest AS result_digest,
         turn.run_id,turn.task_id,turn.turn_id,attempt.attempt_id
  INTO STRICT source_row
  FROM agent_control.runtime_model_call_manifest AS manifest
  JOIN agent_control.runtime_model_call_result AS result
    ON result.call_id=manifest.call_id
  JOIN agent_control.runtime_turn AS turn
    ON turn.turn_id=result.turn_id
  JOIN agent_control.runtime_attempt AS attempt
    ON attempt.attempt_id=result.attempt_id
  JOIN agent_control.runtime_task AS task
    ON task.task_id=turn.task_id AND task.run_id=turn.run_id
  JOIN agent_control.runtime_run AS run
    ON run.run_id=turn.run_id
  WHERE manifest.call_id=p_source_call_id
  FOR UPDATE OF attempt,task,run;

  SELECT * INTO existing
  FROM agent_control.cortex_paper_trade_candidate
  WHERE source_call_id=p_source_call_id;
  IF FOUND THEN
    IF existing.attempt_id<>p_attempt_id
      OR existing.proposal<>p_proposal THEN
      RAISE EXCEPTION USING ERRCODE='23505',
        MESSAGE='Cortex Paper Candidate identity conflict';
    END IF;
    RETURN jsonb_build_object(
      'schema_revision',1,'status','proposed',
      'candidate_id',existing.candidate_id,
      'run_id',existing.run_id,'task_id',existing.task_id,
      'attempt_id',existing.attempt_id,'proposal',existing.proposal,
      'record_digest',existing.record_digest::TEXT,
      'proposed_at',agent_control.runtime_utc_text(existing.proposed_at)
    );
  END IF;

  IF source_row.attempt_id<>p_attempt_id OR NOT EXISTS (
    SELECT 1 FROM agent_control.runtime_attempt AS attempt
    WHERE attempt.attempt_id=p_attempt_id
      AND attempt.state='executing'
      AND attempt.lease_generation=p_lease_generation
      AND attempt.lease_token=p_lease_token
      AND attempt.lease_expires_at>at_time
      AND attempt.lease_worker->>'principal_id'='cortex-worker-1'
      AND attempt.lease_worker->>'kind'='workload'
      AND attempt.lease_worker->>'audience'='worker'
  ) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Paper Candidate lease denied';
  END IF;

  body:=jsonb_build_object(
    'schema_revision',1,'candidate_id',candidate_id_value,
    'source_result',jsonb_build_object(
      'owner','agent_control','record_type','model_call_result',
      'record_id',source_row.result_id,'schema_revision',1,
      'record_digest',source_row.result_digest::TEXT
    ),
    'run_id',source_row.run_id,'task_id',source_row.task_id,
    'attempt_id',source_row.attempt_id,'proposal',p_proposal,
    'proposed_by',invoker.principal_id,
    'proposed_at',agent_control.runtime_utc_text(at_time)
  );
  record_digest_value:=agent_control.runtime_contract_digest(
    'agent-platform.contract.paper_trade_candidate.v1',body
  );
  INSERT INTO agent_control.cortex_paper_trade_candidate(
    candidate_id,source_call_id,source_result_id,run_id,task_id,
    attempt_id,turn_id,strategy_id,symbol,kind,side,qty,thesis,
    invalidation,confidence_bps,proposal,record_digest,proposed_by,
    proposed_at
  ) VALUES(
    candidate_id_value,source_row.call_id,source_row.result_id,
    source_row.run_id,source_row.task_id,source_row.attempt_id,
    source_row.turn_id,p_proposal->>'strategy_id',
    p_proposal->>'symbol',p_proposal->>'kind',p_proposal->>'side',
    qty_value,p_proposal->>'thesis',p_proposal->>'invalidation',
    confidence_value,p_proposal,record_digest_value,
    invoker.principal_id,at_time
  );
  RETURN jsonb_build_object(
    'schema_revision',1,'status','proposed',
    'candidate_id',candidate_id_value,'run_id',source_row.run_id,
    'task_id',source_row.task_id,'attempt_id',source_row.attempt_id,
    'proposal',p_proposal,'record_digest',record_digest_value::TEXT,
    'proposed_at',agent_control.runtime_utc_text(at_time)
  );
END $$;

REVOKE ALL ON TABLE
  agent_control.cortex_paper_trade_candidate FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.admit_cortex_paper_trade_candidate(
    TEXT,TEXT,BIGINT,UUID,JSONB
  ) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.admit_cortex_paper_trade_candidate(
    TEXT,TEXT,BIGINT,UUID,JSONB
  ) TO alpheus_agent_control_api;

RESET ROLE;
