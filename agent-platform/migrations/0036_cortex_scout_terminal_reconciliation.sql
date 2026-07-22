SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- An admitted Scout is bounded child work, not an optional background note.
-- When it reaches a terminal failure there is no valid memo from which Desk
-- may continue.  Preserve that outcome and close the parked root Task/Run
-- instead of leaving the user-visible Run waiting forever.
CREATE TABLE agent_control.cortex_parent_scout_failure (
    admission_request_id TEXT PRIMARY KEY REFERENCES agent_control.cortex_scout_child_admission(request_id),
    schema_revision SMALLINT NOT NULL CHECK (schema_revision = 1),
    record_digest CHAR(64) NOT NULL UNIQUE CHECK (agent_control.runtime_digest_valid(record_digest::TEXT)),
    run_id TEXT NOT NULL REFERENCES agent_control.runtime_run(run_id),
    parent_task_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_task(task_id),
    scout_task_id TEXT NOT NULL UNIQUE REFERENCES agent_control.runtime_task(task_id),
    scout_task_state TEXT NOT NULL CHECK (scout_task_state IN ('failed', 'dead_lettered')),
    failure JSONB NOT NULL CHECK (agent_control.runtime_failure_valid(failure)),
    state TEXT NOT NULL CHECK (state = 'failed'),
    response JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);
CREATE TRIGGER cortex_parent_scout_failure_immutable
  BEFORE UPDATE OR DELETE ON agent_control.cortex_parent_scout_failure
  FOR EACH ROW EXECUTE FUNCTION agent_control.reject_runtime_immutable_mutation();

-- Runtime's original event helper is deliberately Worker-shaped.  This
-- Control-owned reconciler must record its true control-api actor instead of
-- impersonating a Worker while terminalizing a parent it owns.
CREATE FUNCTION agent_control.cortex_insert_control_event(
    p_subject TEXT,p_subject_id TEXT,p_from_state TEXT,p_to_state TEXT,
    p_generation BIGINT,p_principal_id TEXT,p_causation_id TEXT,
    p_correlation_id TEXT,p_reason_code TEXT,p_occurred_at TIMESTAMPTZ
) RETURNS TEXT LANGUAGE plpgsql VOLATILE AS $$
DECLARE event_id_value TEXT:=gen_random_uuid()::TEXT; event_body JSONB;
BEGIN
  event_body:=jsonb_build_object(
    'schema_revision',1,'event_id',event_id_value,'subject',p_subject,
    'subject_id',p_subject_id,'from_state',p_from_state,'to_state',p_to_state,
    'generation',p_generation,'actor',jsonb_build_object(
      'principal_id',p_principal_id,'kind','workload','audience','control_api'),
    'causation_id',p_causation_id,'correlation_id',p_correlation_id,
    'reason_code',p_reason_code,'occurred_at',agent_control.runtime_utc_text(p_occurred_at));
  IF p_from_state IS NULL THEN event_body:=event_body-'from_state'; END IF;
  INSERT INTO agent_control.runtime_event(
    event_id,schema_revision,record_digest,subject,subject_id,from_state,to_state,
    generation,actor,causation_id,correlation_id,reason_code,occurred_at
  ) VALUES(
    event_id_value,1,agent_control.runtime_contract_digest(
      'agent-platform.contract.runtime_event.v1',event_body),
    p_subject,p_subject_id,p_from_state,p_to_state,p_generation,
    jsonb_build_object('principal_id',p_principal_id,'kind','workload','audience','control_api'),
    p_causation_id,p_correlation_id,p_reason_code,p_occurred_at);
  RETURN event_id_value;
END $$;

CREATE FUNCTION agent_control.list_cortex_scout_failure_candidates(p_limit INTEGER)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR p_limit IS NULL OR p_limit NOT BETWEEN 1 AND 32 THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex scout failure list denied';
  END IF;
  RETURN COALESCE((SELECT jsonb_agg(jsonb_build_object('request_id',candidate.request_id)
      ORDER BY candidate.updated_at,candidate.request_id)
    FROM (
      SELECT admission.request_id,child.updated_at
      FROM agent_control.cortex_scout_child_admission admission
      JOIN agent_control.runtime_task child ON child.task_id=admission.child_task_id
      JOIN agent_control.runtime_task parent ON parent.task_id=admission.parent_task_id
      LEFT JOIN agent_control.cortex_parent_continuation continuation
        ON continuation.admission_request_id=admission.request_id
      LEFT JOIN agent_control.cortex_parent_scout_failure failure
        ON failure.admission_request_id=admission.request_id
      WHERE admission.state='admitted' AND child.state IN ('failed','dead_lettered')
        AND parent.state='waiting' AND continuation.admission_request_id IS NULL
        AND failure.admission_request_id IS NULL
      ORDER BY child.updated_at,admission.request_id LIMIT p_limit
    ) AS candidate),'[]'::JSONB);
END $$;

CREATE FUNCTION agent_control.fail_cortex_parent_from_scout(p_request_id TEXT)
RETURNS JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE
  invoker RECORD; admission agent_control.cortex_scout_child_admission%ROWTYPE;
  existing agent_control.cortex_parent_scout_failure%ROWTYPE;
  run_row agent_control.runtime_run%ROWTYPE; parent_task agent_control.runtime_task%ROWTYPE;
  child_task agent_control.runtime_task%ROWTYPE; failure_value JSONB;
  response_value JSONB; failure_digest CHAR(64); at_time TIMESTAMPTZ:=clock_timestamp();
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api' OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control' OR NOT agent_control.runtime_identifier_valid(p_request_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',MESSAGE='cortex scout parent failure denied';
  END IF;
  SELECT * INTO admission FROM agent_control.cortex_scout_child_admission
    WHERE request_id=p_request_id AND state='admitted' FOR UPDATE;
  IF NOT FOUND THEN RAISE EXCEPTION USING ERRCODE='23503',MESSAGE='scout admission not found'; END IF;
  SELECT * INTO existing FROM agent_control.cortex_parent_scout_failure
    WHERE admission_request_id=p_request_id;
  IF FOUND THEN RETURN existing.response; END IF;
  SELECT * INTO run_row FROM agent_control.runtime_run WHERE run_id=admission.run_id FOR UPDATE;
  SELECT * INTO parent_task FROM agent_control.runtime_task
    WHERE task_id=admission.parent_task_id AND run_id=admission.run_id FOR UPDATE;
  SELECT * INTO child_task FROM agent_control.runtime_task
    WHERE task_id=admission.child_task_id AND run_id=admission.run_id FOR UPDATE;
  IF parent_task.task_id<>run_row.root_task_id OR parent_task.state<>'waiting'
     OR NOT parent_task.budget_slot_held OR child_task.state NOT IN ('failed','dead_lettered')
     OR run_row.state NOT IN ('running','waiting')
     OR EXISTS (SELECT 1 FROM agent_control.cortex_parent_continuation continuation
        WHERE continuation.admission_request_id=p_request_id)
     OR EXISTS (SELECT 1 FROM agent_control.runtime_task other_task
        WHERE other_task.run_id=run_row.run_id AND other_task.task_id<>parent_task.task_id
          AND NOT agent_control.runtime_terminal_state('task',other_task.state)) THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='scout parent is not terminal-failure-ready';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM agent_control.runtime_session session
      WHERE session.session_id=parent_task.session_id AND session.state='closed') THEN
    RAISE EXCEPTION USING ERRCODE='55000',MESSAGE='scout parent session is not parked';
  END IF;
  failure_value:=jsonb_build_object('code','scout_child_terminal',
    'message','Scout child Task '||child_task.task_id||' ended in '||child_task.state||'; parent cannot continue',
    'retryable',false);
  UPDATE agent_control.runtime_task
    SET state='failed',state_generation=parent_task.state_generation+1,budget_slot_held=false,
      failure=failure_value,updated_at=greatest(at_time,parent_task.updated_at),terminal_at=at_time
    WHERE task_id=parent_task.task_id;
  PERFORM agent_control.cortex_insert_control_event('task',parent_task.task_id,'waiting','failed',
    parent_task.state_generation+1,invoker.principal_id,p_request_id,p_request_id,
    'parent_failed_from_scout',at_time);
  IF NOT agent_control.runtime_release_active_slot_ancestors(
      run_row.run_id,parent_task.budget_ledger_id,at_time) THEN
    RAISE EXCEPTION USING ERRCODE='40001',MESSAGE='active parent Task slot changed during Scout failure';
  END IF;
  UPDATE agent_control.runtime_run
    SET state='failed',state_generation=run_row.state_generation+1,failure=failure_value,
      updated_at=greatest(at_time,run_row.updated_at),terminal_at=at_time
    WHERE run_id=run_row.run_id;
  PERFORM agent_control.cortex_insert_control_event('run',run_row.run_id,run_row.state,'failed',
    run_row.state_generation+1,invoker.principal_id,p_request_id,p_request_id,
    'run_failed_from_scout',at_time);
  response_value:=jsonb_build_object('status','failed','request_id',p_request_id,
    'run_id',run_row.run_id,'parent_task_id',parent_task.task_id,'child_task_id',child_task.task_id);
  failure_digest:=agent_control.runtime_contract_digest('agent_control.cortex_parent_scout_failure.v1',
    jsonb_build_object('admission_request_id',p_request_id,'run_id',run_row.run_id,
      'parent_task_id',parent_task.task_id,'scout_task_id',child_task.task_id,
      'scout_task_state',child_task.state,'failure',failure_value,'state','failed',
      'response',response_value));
  INSERT INTO agent_control.cortex_parent_scout_failure(
    admission_request_id,schema_revision,record_digest,run_id,parent_task_id,scout_task_id,
    scout_task_state,failure,state,response,created_at
  ) VALUES(
    p_request_id,1,failure_digest,run_row.run_id,parent_task.task_id,child_task.task_id,
    child_task.state,failure_value,'failed',response_value,at_time);
  RETURN response_value;
END $$;

-- Include the final parent outcome in the read-only UI trace so an operator
-- sees a concrete terminal reason rather than a Run that merely stopped.
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
        SELECT COALESCE(turn.finished_at,turn.updated_at,turn.created_at) AS occurred_at,
          10 AS order_key,'turn:'||turn.turn_id AS event_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(COALESCE(turn.finished_at,turn.updated_at,turn.created_at)),
            'stage',CASE
              WHEN scout_admission.request_id IS NOT NULL THEN CASE turn.state
                WHEN 'result_committed' THEN 'scout_research_completed'
                WHEN 'failed' THEN 'scout_research_failed' ELSE 'scout_research_in_progress' END
              WHEN continuation.admission_request_id IS NOT NULL OR turn.ordinal>1 THEN CASE turn.state
                WHEN 'result_committed' THEN 'decision_desk_completed'
                WHEN 'failed' THEN 'decision_desk_failed' ELSE 'decision_desk_in_progress' END
              ELSE CASE turn.state
                WHEN 'result_committed' THEN 'intent_interpreter_completed'
                WHEN 'failed' THEN 'intent_interpreter_failed' ELSE 'intent_interpreter_in_progress' END END,
            'turn_id',turn.turn_id,'task_id',turn.task_id,'state',turn.state) AS payload
        FROM agent_control.runtime_turn turn
        LEFT JOIN agent_control.cortex_scout_child_admission scout_admission
          ON scout_admission.child_task_id=turn.task_id AND scout_admission.state='admitted'
        LEFT JOIN agent_control.cortex_parent_continuation continuation
          ON continuation.parent_task_id=turn.task_id AND continuation.parent_session_id=turn.session_id
        WHERE turn.run_id=p_run_id
        UNION ALL
        SELECT handoff.created_at,20,'handoff:'||handoff.handoff_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(handoff.created_at),
            'stage','handoff_to_'||handoff.target_role,'target_role',handoff.target_role,
            'handoff_id',handoff.handoff_id,'task_id',handoff.task_id)
        FROM agent_control.cortex_handoff handoff WHERE handoff.run_id=p_run_id
        UNION ALL
        SELECT admission.created_at,30,'scout-admission:'||admission.request_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(admission.created_at),
            'stage','scout_task_admitted','request_id',admission.request_id,
            'parent_task_id',admission.parent_task_id,'child_task_id',admission.child_task_id,
            'state',admission.state)
        FROM agent_control.cortex_scout_child_admission admission WHERE admission.run_id=p_run_id
        UNION ALL
        SELECT continuation.created_at,40,'desk-continuation:'||continuation.admission_request_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(continuation.created_at),
            'stage','desk_continuation_ready','request_id',continuation.admission_request_id,
            'parent_task_id',continuation.parent_task_id,'parent_session_id',continuation.parent_session_id,
            'state',continuation.state)
        FROM agent_control.cortex_parent_continuation continuation WHERE continuation.run_id=p_run_id
        UNION ALL
        SELECT failure.created_at,45,'scout-parent-failure:'||failure.admission_request_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(failure.created_at),
            'stage','scout_parent_failed','request_id',failure.admission_request_id,
            'parent_task_id',failure.parent_task_id,'child_task_id',failure.scout_task_id,
            'state',failure.scout_task_state)
        FROM agent_control.cortex_parent_scout_failure failure WHERE failure.run_id=p_run_id
        UNION ALL
        SELECT intent.authorized_at,50,'tool-authorized:'||intent.tool_call_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(intent.authorized_at),
            'stage','tool_call_authorized','tool_call_id',intent.tool_call_id,'tool_id',intent.tool_id)
        FROM agent_control.cortex_tool_call_intent intent WHERE intent.run_id=p_run_id
        UNION ALL
        SELECT ack.acknowledged_at,60,'tool-receipt:'||ack.tool_call_id,
          jsonb_build_object('created_at',agent_control.runtime_utc_text(ack.acknowledged_at),
            'stage','tool_receipt_succeeded','tool_call_id',ack.tool_call_id,'receipt_id',ack.receipt_id)
        FROM agent_control.cortex_tool_receipt_ack ack
        JOIN agent_control.cortex_tool_call_intent intent ON intent.tool_call_id=ack.tool_call_id
        WHERE intent.run_id=p_run_id
      ) raw
    ) event
  ),'[]'::JSONB);
END $$;

REVOKE ALL ON TABLE agent_control.cortex_parent_scout_failure FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.cortex_insert_control_event(TEXT,TEXT,TEXT,TEXT,BIGINT,TEXT,TEXT,TEXT,TEXT,TIMESTAMPTZ) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.list_cortex_scout_failure_candidates(INTEGER) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.fail_cortex_parent_from_scout(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.get_cortex_run_trace(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.list_cortex_scout_failure_candidates(INTEGER) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.fail_cortex_parent_from_scout(TEXT) TO alpheus_agent_control_api;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_trace(TEXT) TO alpheus_agent_control_api;
RESET ROLE;
