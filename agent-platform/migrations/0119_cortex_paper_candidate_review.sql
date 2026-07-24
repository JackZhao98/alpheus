-- Generation-fenced Copilot review. Approval is a durable human decision,
-- not execution authority and not an order.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE TABLE agent_control.cortex_paper_candidate_review_head(
  candidate_id TEXT PRIMARY KEY REFERENCES
    agent_control.cortex_paper_trade_candidate(candidate_id),
  generation BIGINT NOT NULL CHECK(generation>0),
  state TEXT NOT NULL CHECK(state IN ('proposed','approved','rejected')),
  candidate_record_digest CHAR(64) NOT NULL,
  decided_by TEXT,
  decision_reason TEXT,
  decided_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ NOT NULL,
  FOREIGN KEY(candidate_id,candidate_record_digest) REFERENCES
    agent_control.cortex_paper_trade_candidate(candidate_id,record_digest),
  CHECK(
    (state='proposed' AND generation=1 AND decided_by IS NULL
      AND decision_reason IS NULL AND decided_at IS NULL)
    OR
    (state IN ('approved','rejected') AND generation=2
      AND decided_by IS NOT NULL AND decision_reason IS NOT NULL
      AND decided_at IS NOT NULL)
  )
);

CREATE TABLE agent_control.cortex_paper_candidate_review_event(
  event_id TEXT PRIMARY KEY CHECK(
    agent_control.runtime_identifier_valid(event_id)
  ),
  candidate_id TEXT NOT NULL REFERENCES
    agent_control.cortex_paper_trade_candidate(candidate_id),
  generation BIGINT NOT NULL CHECK(generation>0),
  previous_generation BIGINT,
  state TEXT NOT NULL CHECK(state IN ('proposed','approved','rejected')),
  candidate_record_digest CHAR(64) NOT NULL,
  actor_principal_id TEXT NOT NULL CHECK(
    agent_control.runtime_identifier_valid(actor_principal_id)
  ),
  actor_kind TEXT NOT NULL CHECK(actor_kind IN ('user','workload')),
  reason_code TEXT NOT NULL CHECK(
    reason_code IN ('candidate_proposed','copilot_approved','copilot_rejected')
  ),
  record_digest CHAR(64) NOT NULL UNIQUE CHECK(
    agent_control.runtime_digest_valid(record_digest::TEXT)
  ),
  occurred_at TIMESTAMPTZ NOT NULL,
  UNIQUE(candidate_id,generation),
  FOREIGN KEY(candidate_id,candidate_record_digest) REFERENCES
    agent_control.cortex_paper_trade_candidate(candidate_id,record_digest),
  CHECK(
    (generation=1 AND previous_generation IS NULL
      AND state='proposed' AND reason_code='candidate_proposed')
    OR
    (generation=2 AND previous_generation=1
      AND state='approved' AND reason_code='copilot_approved')
    OR
    (generation=2 AND previous_generation=1
      AND state='rejected' AND reason_code='copilot_rejected')
  )
);

CREATE TRIGGER cortex_paper_candidate_review_event_immutable
BEFORE UPDATE OR DELETE ON
  agent_control.cortex_paper_candidate_review_event
FOR EACH ROW EXECUTE FUNCTION
  agent_control.reject_runtime_immutable_mutation();

CREATE FUNCTION agent_control.initialize_cortex_paper_candidate_review()
RETURNS TRIGGER LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control
SET timezone='UTC' AS $$
DECLARE
  body JSONB;
  event_digest CHAR(64);
BEGIN
  INSERT INTO agent_control.cortex_paper_candidate_review_head(
    candidate_id,generation,state,candidate_record_digest,updated_at
  ) VALUES(
    NEW.candidate_id,1,'proposed',NEW.record_digest,NEW.proposed_at
  );
  body:=jsonb_build_object(
    'schema_revision',1,'candidate_id',NEW.candidate_id,
    'generation',1,'previous_generation',NULL,
    'state','proposed','candidate_record_digest',
    NEW.record_digest::TEXT,'actor',jsonb_build_object(
      'principal_id',NEW.proposed_by,'kind','workload'
    ),'reason_code','candidate_proposed',
    'occurred_at',agent_control.runtime_utc_text(NEW.proposed_at)
  );
  event_digest:=agent_control.runtime_contract_digest(
    'agent-platform.contract.paper_candidate_review_event.v1',body
  );
  INSERT INTO agent_control.cortex_paper_candidate_review_event(
    event_id,candidate_id,generation,previous_generation,state,
    candidate_record_digest,actor_principal_id,actor_kind,reason_code,
    record_digest,occurred_at
  ) VALUES(
    gen_random_uuid()::TEXT,NEW.candidate_id,1,NULL,'proposed',
    NEW.record_digest,NEW.proposed_by,'workload','candidate_proposed',
    event_digest,NEW.proposed_at
  );
  RETURN NEW;
END $$;

INSERT INTO agent_control.cortex_paper_candidate_review_head(
  candidate_id,generation,state,candidate_record_digest,updated_at
)
SELECT candidate_id,1,'proposed',record_digest,proposed_at
FROM agent_control.cortex_paper_trade_candidate
ON CONFLICT(candidate_id) DO NOTHING;

INSERT INTO agent_control.cortex_paper_candidate_review_event(
  event_id,candidate_id,generation,previous_generation,state,
  candidate_record_digest,actor_principal_id,actor_kind,reason_code,
  record_digest,occurred_at
)
SELECT
  gen_random_uuid()::TEXT,candidate.candidate_id,1,NULL,'proposed',
  candidate.record_digest,candidate.proposed_by,'workload',
  'candidate_proposed',
  agent_control.runtime_contract_digest(
    'agent-platform.contract.paper_candidate_review_event.v1',
    jsonb_build_object(
      'schema_revision',1,'candidate_id',candidate.candidate_id,
      'generation',1,'previous_generation',NULL,
      'state','proposed','candidate_record_digest',
      candidate.record_digest::TEXT,'actor',jsonb_build_object(
        'principal_id',candidate.proposed_by,'kind','workload'
      ),'reason_code','candidate_proposed',
      'occurred_at',
        agent_control.runtime_utc_text(candidate.proposed_at)
    )
  ),
  candidate.proposed_at
FROM agent_control.cortex_paper_trade_candidate AS candidate
ON CONFLICT(candidate_id,generation) DO NOTHING;

CREATE TRIGGER cortex_paper_candidate_review_initialize
AFTER INSERT ON agent_control.cortex_paper_trade_candidate
FOR EACH ROW EXECUTE FUNCTION
  agent_control.initialize_cortex_paper_candidate_review();

CREATE FUNCTION agent_control.review_cortex_paper_trade_candidate(
  p_subject_principal_id TEXT,
  p_candidate_id TEXT,
  p_expected_generation BIGINT,
  p_decision TEXT
) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  source RECORD;
  review agent_control.cortex_paper_candidate_review_head%ROWTYPE;
  next_state TEXT;
  reason_code_value TEXT;
  at_time TIMESTAMPTZ:=clock_timestamp();
  body JSONB;
  event_digest CHAR(64);
BEGIN
  SELECT * INTO STRICT invoker
  FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
    OR invoker.profile_id<>'control-api'
    OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_subject_principal_id)
    OR NOT agent_control.runtime_identifier_valid(p_candidate_id)
    OR p_expected_generation<1
    OR p_decision NOT IN ('approve','reject') THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Paper Candidate review denied';
  END IF;
  SELECT candidate_row.candidate_id,candidate_row.record_digest,
         run.state AS run_state
  INTO STRICT source
  FROM agent_control.cortex_paper_trade_candidate AS candidate_row
  JOIN agent_control.runtime_run AS run
    ON run.run_id=candidate_row.run_id
  WHERE candidate_row.candidate_id=p_candidate_id
    AND run.origin_initiating_principal_id=p_subject_principal_id
  FOR UPDATE OF run;
  SELECT * INTO STRICT review
  FROM agent_control.cortex_paper_candidate_review_head
  WHERE candidate_id=p_candidate_id
  FOR UPDATE;
  next_state:=CASE WHEN p_decision='approve'
    THEN 'approved' ELSE 'rejected' END;
  IF review.state=next_state
    AND p_expected_generation IN (review.generation-1,review.generation) THEN
    RETURN jsonb_build_object(
      'status','reviewed','replay',true,
      'candidate_id',source.candidate_id,
      'generation',review.generation,'state',review.state,
      'decided_by',review.decided_by,
      'decided_at',agent_control.runtime_utc_text(review.decided_at)
    );
  END IF;
  IF review.generation<>p_expected_generation
    OR review.state<>'proposed' THEN
    RETURN jsonb_build_object(
      'status','conflict','reason_code','candidate_review_conflict',
      'candidate_id',source.candidate_id,
      'generation',review.generation,'state',review.state
    );
  END IF;
  IF source.run_state<>'succeeded' THEN
    RETURN jsonb_build_object(
      'status','conflict',
      'reason_code','candidate_source_not_committed',
      'candidate_id',source.candidate_id,
      'generation',review.generation,'state',review.state
    );
  END IF;
  reason_code_value:=CASE WHEN p_decision='approve'
    THEN 'copilot_approved' ELSE 'copilot_rejected' END;
  UPDATE agent_control.cortex_paper_candidate_review_head SET
    generation=2,state=next_state,decided_by=p_subject_principal_id,
    decision_reason=reason_code_value,decided_at=at_time,
    updated_at=at_time
  WHERE candidate_id=p_candidate_id;
  body:=jsonb_build_object(
    'schema_revision',1,'candidate_id',source.candidate_id,
    'generation',2,'previous_generation',1,'state',next_state,
    'candidate_record_digest',source.record_digest::TEXT,
    'actor',jsonb_build_object(
      'principal_id',p_subject_principal_id,'kind','user'
    ),'reason_code',reason_code_value,
    'occurred_at',agent_control.runtime_utc_text(at_time)
  );
  event_digest:=agent_control.runtime_contract_digest(
    'agent-platform.contract.paper_candidate_review_event.v1',body
  );
  INSERT INTO agent_control.cortex_paper_candidate_review_event(
    event_id,candidate_id,generation,previous_generation,state,
    candidate_record_digest,actor_principal_id,actor_kind,reason_code,
    record_digest,occurred_at
  ) VALUES(
    gen_random_uuid()::TEXT,source.candidate_id,2,1,next_state,
    source.record_digest,p_subject_principal_id,'user',
    reason_code_value,event_digest,at_time
  );
  RETURN jsonb_build_object(
    'status','reviewed','replay',false,
    'candidate_id',source.candidate_id,
    'generation',2,'state',next_state,
    'decided_by',p_subject_principal_id,
    'decided_at',agent_control.runtime_utc_text(at_time)
  );
END $$;

CREATE OR REPLACE FUNCTION
  agent_control.list_cortex_paper_trade_candidates(
    p_subject_principal_id TEXT,
    p_limit INTEGER
  ) RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
  result JSONB;
BEGIN
  SELECT * INTO STRICT invoker
  FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
    OR invoker.profile_id<>'control-api'
    OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_subject_principal_id)
    OR p_limit<1 OR p_limit>100 THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Paper Candidate projection denied';
  END IF;
  SELECT COALESCE(jsonb_agg(item ORDER BY proposed_at DESC,candidate_id DESC),
    '[]'::JSONB) INTO result
  FROM (
    SELECT
      candidate.proposed_at,
      candidate.candidate_id,
      jsonb_build_object(
        'schema_revision',1,
        'candidate_id',candidate.candidate_id,
        'run_id',candidate.run_id,
        'task_id',candidate.task_id,
        'generation',review.generation,
        'status',CASE WHEN run.state='succeeded'
          THEN review.state ELSE 'source_not_committed' END,
        'source_run_state',run.state,
        'eligible',run.state='succeeded',
        'proposal',candidate.proposal,
        'record_digest',candidate.record_digest::TEXT,
        'proposed_at',
          agent_control.runtime_utc_text(candidate.proposed_at)
      ) AS item
    FROM agent_control.cortex_paper_trade_candidate AS candidate
    JOIN agent_control.cortex_paper_candidate_review_head AS review
      ON review.candidate_id=candidate.candidate_id
    JOIN agent_control.runtime_run AS run
      ON run.run_id=candidate.run_id
    WHERE run.origin_initiating_principal_id=p_subject_principal_id
    ORDER BY candidate.proposed_at DESC,candidate.candidate_id DESC
    LIMIT p_limit
  ) AS selected;
  RETURN result;
END $$;

REVOKE ALL ON TABLE
  agent_control.cortex_paper_candidate_review_head,
  agent_control.cortex_paper_candidate_review_event FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.initialize_cortex_paper_candidate_review() FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.review_cortex_paper_trade_candidate(
    TEXT,TEXT,BIGINT,TEXT
  ) FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.list_cortex_paper_trade_candidates(TEXT,INTEGER)
  FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.review_cortex_paper_trade_candidate(
    TEXT,TEXT,BIGINT,TEXT
  ) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION
  agent_control.list_cortex_paper_trade_candidates(TEXT,INTEGER)
  TO alpheus_agent_control_api;

RESET ROLE;
