-- Correct the operational Tool risk projection: an unacknowledged Tool call
-- on a terminal Run is retained as history but is no longer actionable. Only
-- calls belonging to a non-terminal Run can degrade current health.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.get_cortex_operations_health_v2()
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
    invoker RECORD;
    result JSONB;
    overdue_tools BIGINT;
    other_risk BIGINT;
BEGIN
    SELECT * INTO STRICT invoker
    FROM platform_security.invoker_identity();
    IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
       OR invoker.profile_id<>'control-api'
       OR invoker.owner_id<>'agent_control' THEN
        RAISE EXCEPTION USING ERRCODE='42501',
            MESSAGE='Cortex operations health read denied';
    END IF;

    result:=agent_control.get_cortex_operations_health();

    WITH calls AS (
        SELECT intent.tool_call_id,intent.run_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.run_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_gexbot_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_gexbot_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.run_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_gexbot_live_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_gexbot_live_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.run_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_kernel_earnings_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_kernel_earnings_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
        UNION ALL
        SELECT intent.tool_call_id,intent.run_id,intent.authorized_at,
               ack.acknowledged_at
        FROM agent_control.cortex_kernel_read_tool_call_intent AS intent
        LEFT JOIN agent_control.cortex_kernel_read_tool_receipt_ack AS ack
          ON ack.tool_call_id=intent.tool_call_id
    )
    SELECT count(*) INTO overdue_tools
    FROM calls
    JOIN agent_control.runtime_run AS run ON run.run_id=calls.run_id
    WHERE calls.acknowledged_at IS NULL
      AND calls.authorized_at<clock_timestamp()-interval '45 seconds'
      AND NOT agent_control.runtime_terminal_state('run',run.state);

    result:=jsonb_set(
        result,'{risks,unacknowledged_tool_calls}',
        to_jsonb(overdue_tools),false
    );
    result:=jsonb_set(
        result,'{tools,overdue_unacknowledged}',
        to_jsonb(overdue_tools),false
    );
    other_risk:=
        (result #>> '{risks,stalled_runs}')::BIGINT+
        (result #>> '{risks,expired_runs}')::BIGINT+
        (result #>> '{risks,expired_attempt_leases}')::BIGINT+
        (result #>> '{risks,terminal_open_sessions}')::BIGINT+
        (result #>> '{risks,terminal_slot_leaks}')::BIGINT;
    result:=jsonb_set(
        result,'{status}',
        to_jsonb(CASE
            WHEN other_risk+overdue_tools>0 THEN 'degraded'
            ELSE 'healthy'
        END),
        false
    );
    RETURN result;
END
$$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_operations_health() FROM alpheus_agent_control_api;
REVOKE ALL ON FUNCTION
agent_control.get_cortex_operations_health_v2() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_operations_health_v2()
TO alpheus_agent_control_api;

RESET ROLE;
