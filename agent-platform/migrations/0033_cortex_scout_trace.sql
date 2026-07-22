SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

-- The Agent Lab trace is a read-only projection of durable Cortex records.
-- A Scout child is a distinct Task/Session, so Turn ordinals are only local to
-- their Attempt.  Build a globally ordered trace from immutable timestamps
-- instead of inferring roles from Turn ordinal alone.
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
              WHEN scout_admission.request_id IS NOT NULL THEN 'scout_research_completed'
              WHEN continuation.admission_request_id IS NOT NULL OR turn.ordinal>1 THEN 'decision_desk_completed'
              ELSE 'intent_interpreter_completed' END,
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

REVOKE ALL ON FUNCTION agent_control.get_cortex_run_trace(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.get_cortex_run_trace(TEXT) TO alpheus_agent_control_api;
RESET ROLE;
