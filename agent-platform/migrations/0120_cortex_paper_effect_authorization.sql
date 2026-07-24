-- One immutable Control authorization and one immutable Kernel receipt per
-- Candidate. Authorization is necessary but never sufficient: Kernel also
-- verifies its current local autonomy mode before settling a Paper order.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_paper_effect_authorization(
  authorization_id TEXT PRIMARY KEY CHECK(
    agent_control.runtime_identifier_valid(authorization_id)
  ),
  candidate_id TEXT NOT NULL UNIQUE,
  candidate_record_digest CHAR(64) NOT NULL,
  effect_id TEXT NOT NULL UNIQUE CHECK(
    agent_control.runtime_identifier_valid(effect_id)
  ),
  authorization_kind TEXT NOT NULL CHECK(
    authorization_kind IN ('copilot','agentic')
  ),
  review_generation BIGINT NOT NULL CHECK(
    review_generation IN (1,2)
  ),
  kernel_mode_generation BIGINT NOT NULL CHECK(
    kernel_mode_generation>0
  ),
  proposal JSONB NOT NULL CHECK(jsonb_typeof(proposal)='object'),
  authorized_by TEXT NOT NULL CHECK(
    agent_control.runtime_identifier_valid(authorized_by)
  ),
  record_digest CHAR(64) NOT NULL UNIQUE CHECK(
    agent_control.runtime_digest_valid(record_digest::TEXT)
  ),
  authorized_at TIMESTAMPTZ NOT NULL,
  UNIQUE(authorization_id,effect_id),
  FOREIGN KEY(candidate_id,candidate_record_digest) REFERENCES
    agent_control.cortex_paper_trade_candidate(candidate_id,record_digest),
  CHECK(
    (authorization_kind='copilot' AND review_generation=2)
    OR
    (authorization_kind='agentic' AND review_generation=1)
  )
);

CREATE TABLE agent_control.cortex_paper_effect_receipt(
  receipt_id TEXT PRIMARY KEY CHECK(
    agent_control.runtime_identifier_valid(receipt_id)
  ),
  authorization_id TEXT NOT NULL UNIQUE REFERENCES
    agent_control.cortex_paper_effect_authorization(authorization_id),
  candidate_id TEXT NOT NULL UNIQUE REFERENCES
    agent_control.cortex_paper_trade_candidate(candidate_id),
  effect_id TEXT NOT NULL UNIQUE,
  outcome TEXT NOT NULL CHECK(outcome IN ('succeeded','failed')),
  http_status INTEGER NOT NULL CHECK(http_status BETWEEN 100 AND 599),
  kernel_response JSONB,
  failure_code TEXT,
  record_digest CHAR(64) NOT NULL UNIQUE CHECK(
    agent_control.runtime_digest_valid(record_digest::TEXT)
  ),
  recorded_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY(authorization_id,effect_id) REFERENCES
    agent_control.cortex_paper_effect_authorization(
      authorization_id,effect_id
    ),
  CHECK(
    kernel_response IS NULL
    OR (
      jsonb_typeof(kernel_response)='object'
      AND octet_length(kernel_response::TEXT)<=65536
    )
  ),
  CHECK(
    (outcome='succeeded' AND http_status BETWEEN 200 AND 299
      AND jsonb_typeof(kernel_response)='object'
      AND failure_code IS NULL)
    OR
    (outcome='failed' AND (http_status<200 OR http_status>299)
      AND failure_code IS NOT NULL
      AND failure_code~'^[a-z][a-z0-9_]{0,63}$')
  )
);

CREATE TRIGGER cortex_paper_effect_authorization_immutable
BEFORE UPDATE OR DELETE ON
  agent_control.cortex_paper_effect_authorization
FOR EACH ROW EXECUTE FUNCTION
  agent_control.reject_runtime_immutable_mutation();

CREATE TRIGGER cortex_paper_effect_receipt_immutable
BEFORE UPDATE OR DELETE ON
  agent_control.cortex_paper_effect_receipt
FOR EACH ROW EXECUTE FUNCTION
  agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.authorize_cortex_paper_effect(
  p_subject_principal_id TEXT,
  p_candidate_id TEXT,
  p_authorization_kind TEXT,
  p_kernel_mode_generation BIGINT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  source RECORD;
  existing agent_control.cortex_paper_effect_authorization%ROWTYPE;
  authorization_id_value TEXT:=gen_random_uuid()::TEXT;
  effect_id_value TEXT:=gen_random_uuid()::TEXT;
  at_time TIMESTAMPTZ:=clock_timestamp();
  body JSONB;
  authorization_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker
  FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
    OR invoker.profile_id<>'control-api'
    OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_subject_principal_id)
    OR NOT agent_control.runtime_identifier_valid(p_candidate_id)
    OR p_authorization_kind NOT IN ('copilot','agentic')
    OR p_kernel_mode_generation<1 THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Paper effect authorization denied';
  END IF;
  SELECT candidate.candidate_id,candidate.record_digest,
         candidate.run_id,candidate.task_id,candidate.proposal,
         run.state AS run_state,review.generation AS review_generation,
         review.state AS review_state
  INTO STRICT source
  FROM agent_control.cortex_paper_trade_candidate AS candidate
  JOIN agent_control.runtime_run AS run
    ON run.run_id=candidate.run_id
  JOIN agent_control.cortex_paper_candidate_review_head AS review
    ON review.candidate_id=candidate.candidate_id
  WHERE candidate.candidate_id=p_candidate_id
    AND run.origin_initiating_principal_id=p_subject_principal_id
  FOR UPDATE OF run,review;
  SELECT * INTO existing
  FROM agent_control.cortex_paper_effect_authorization
  WHERE candidate_id=p_candidate_id;
  IF FOUND THEN
    IF existing.authorization_kind<>p_authorization_kind THEN
      RAISE EXCEPTION USING ERRCODE='23505',
        MESSAGE='Cortex Paper authorization identity conflict';
    END IF;
    RETURN jsonb_build_object(
      'schema_revision',1,'status','authorized','replay',true,
      'authorization_id',existing.authorization_id,
      'candidate_id',existing.candidate_id,
      'effect_id',existing.effect_id,
      'authorization_kind',existing.authorization_kind,
      'review_generation',existing.review_generation,
      'kernel_mode_generation',existing.kernel_mode_generation,
      'run_id',source.run_id,'task_id',source.task_id,
      'proposal',existing.proposal,
      'record_digest',existing.record_digest::TEXT,
      'authorized_at',
        agent_control.runtime_utc_text(existing.authorized_at)
    );
  END IF;
  IF source.run_state<>'succeeded'
    OR (
      p_authorization_kind='copilot'
      AND (source.review_generation<>2
        OR source.review_state<>'approved')
    )
    OR (
      p_authorization_kind='agentic'
      AND (source.review_generation<>1
        OR source.review_state<>'proposed')
    ) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Paper Candidate is not authorizable';
  END IF;
  body:=jsonb_build_object(
    'schema_revision',1,
    'authorization_id',authorization_id_value,
    'candidate',jsonb_build_object(
      'candidate_id',source.candidate_id,
      'record_digest',source.record_digest::TEXT
    ),
    'effect_id',effect_id_value,
    'authorization_kind',p_authorization_kind,
    'review_generation',source.review_generation,
    'kernel_mode_generation',p_kernel_mode_generation,
    'proposal',source.proposal,
    'authorized_by',p_subject_principal_id,
    'authorized_at',agent_control.runtime_utc_text(at_time)
  );
  authorization_digest:=agent_control.runtime_contract_digest(
    'agent-platform.contract.paper_effect_authorization.v1',body
  );
  INSERT INTO agent_control.cortex_paper_effect_authorization(
    authorization_id,candidate_id,candidate_record_digest,effect_id,
    authorization_kind,review_generation,kernel_mode_generation,
    proposal,authorized_by,record_digest,authorized_at
  ) VALUES(
    authorization_id_value,source.candidate_id,source.record_digest,
    effect_id_value,p_authorization_kind,source.review_generation,
    p_kernel_mode_generation,source.proposal,p_subject_principal_id,
    authorization_digest,at_time
  );
  RETURN jsonb_build_object(
    'schema_revision',1,'status','authorized','replay',false,
    'authorization_id',authorization_id_value,
    'candidate_id',source.candidate_id,'effect_id',effect_id_value,
    'authorization_kind',p_authorization_kind,
    'review_generation',source.review_generation,
    'kernel_mode_generation',p_kernel_mode_generation,
    'run_id',source.run_id,'task_id',source.task_id,
    'proposal',source.proposal,
    'record_digest',authorization_digest::TEXT,
    'authorized_at',agent_control.runtime_utc_text(at_time)
  );
END $$;

CREATE FUNCTION agent_control.record_cortex_paper_effect_receipt(
  p_authorization_id TEXT,
  p_outcome TEXT,
  p_http_status INTEGER,
  p_kernel_response JSONB,
  p_failure_code TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  authorization_row
    agent_control.cortex_paper_effect_authorization%ROWTYPE;
  existing agent_control.cortex_paper_effect_receipt%ROWTYPE;
  receipt_id_value TEXT:=gen_random_uuid()::TEXT;
  at_time TIMESTAMPTZ:=clock_timestamp();
  body JSONB;
  receipt_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker
  FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
    OR invoker.profile_id<>'control-api'
    OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_authorization_id)
    OR p_outcome NOT IN ('succeeded','failed')
    OR p_http_status<100 OR p_http_status>599
    OR (
      p_kernel_response IS NOT NULL
      AND (
        jsonb_typeof(p_kernel_response)<>'object'
        OR octet_length(p_kernel_response::TEXT)>65536
      )
    )
    OR (
      p_outcome='succeeded'
      AND (p_http_status<200 OR p_http_status>299
        OR jsonb_typeof(p_kernel_response)<>'object'
        OR p_failure_code IS NOT NULL)
    )
    OR (
      p_outcome='failed'
      AND (p_http_status BETWEEN 200 AND 299
        OR COALESCE(p_failure_code,'')
          !~ '^[a-z][a-z0-9_]{0,63}$')
    ) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Paper effect receipt denied';
  END IF;
  SELECT * INTO STRICT authorization_row
  FROM agent_control.cortex_paper_effect_authorization
  WHERE authorization_id=p_authorization_id
  FOR SHARE;
  SELECT * INTO existing
  FROM agent_control.cortex_paper_effect_receipt
  WHERE authorization_id=p_authorization_id;
  IF FOUND THEN
    IF existing.outcome<>p_outcome
      OR existing.http_status<>p_http_status
      OR existing.kernel_response IS DISTINCT FROM p_kernel_response
      OR existing.failure_code IS DISTINCT FROM p_failure_code THEN
      RAISE EXCEPTION USING ERRCODE='23505',
        MESSAGE='Cortex Paper receipt identity conflict';
    END IF;
    RETURN jsonb_build_object(
      'schema_revision',1,'status','recorded','replay',true,
      'receipt_id',existing.receipt_id,
      'authorization_id',existing.authorization_id,
      'candidate_id',existing.candidate_id,
      'effect_id',existing.effect_id,'outcome',existing.outcome,
      'http_status',existing.http_status,
      'kernel_response',existing.kernel_response,
      'failure_code',existing.failure_code,
      'record_digest',existing.record_digest::TEXT,
      'recorded_at',
        agent_control.runtime_utc_text(existing.recorded_at)
    );
  END IF;
  body:=jsonb_build_object(
    'schema_revision',1,'receipt_id',receipt_id_value,
    'authorization_id',authorization_row.authorization_id,
    'candidate_id',authorization_row.candidate_id,
    'effect_id',authorization_row.effect_id,'outcome',p_outcome,
    'http_status',p_http_status,'kernel_response',p_kernel_response,
    'failure_code',p_failure_code,
    'recorded_at',agent_control.runtime_utc_text(at_time)
  );
  receipt_digest:=agent_control.runtime_contract_digest(
    'agent-platform.contract.paper_effect_receipt.v1',body
  );
  INSERT INTO agent_control.cortex_paper_effect_receipt(
    receipt_id,authorization_id,candidate_id,effect_id,outcome,
    http_status,kernel_response,failure_code,record_digest,recorded_at
  ) VALUES(
    receipt_id_value,authorization_row.authorization_id,
    authorization_row.candidate_id,authorization_row.effect_id,p_outcome,
    p_http_status,p_kernel_response,p_failure_code,receipt_digest,at_time
  );
  RETURN jsonb_build_object(
    'schema_revision',1,'status','recorded','replay',false,
    'receipt_id',receipt_id_value,
    'authorization_id',authorization_row.authorization_id,
    'candidate_id',authorization_row.candidate_id,
    'effect_id',authorization_row.effect_id,'outcome',p_outcome,
    'http_status',p_http_status,'kernel_response',p_kernel_response,
    'failure_code',p_failure_code,
    'record_digest',receipt_digest::TEXT,
    'recorded_at',agent_control.runtime_utc_text(at_time)
  );
END $$;

REVOKE ALL ON TABLE
  agent_control.cortex_paper_effect_authorization,
  agent_control.cortex_paper_effect_receipt FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.authorize_cortex_paper_effect(TEXT,TEXT,TEXT,BIGINT)
  FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.record_cortex_paper_effect_receipt(
    TEXT,TEXT,INTEGER,JSONB,TEXT
  ) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.authorize_cortex_paper_effect(TEXT,TEXT,TEXT,BIGINT)
  TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION
  agent_control.record_cortex_paper_effect_receipt(
    TEXT,TEXT,INTEGER,JSONB,TEXT
  ) TO alpheus_agent_control_api;

RESET ROLE;
