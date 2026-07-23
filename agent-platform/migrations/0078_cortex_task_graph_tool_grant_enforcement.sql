-- Tool authorization accepts either the legacy immutable handoff or the exact
-- TaskGraph node grant snapshot. A source model call alone never grants a Tool.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE OR REPLACE FUNCTION agent_control.enforce_cortex_specialist_tool_grant(
    p_source_call_id TEXT,p_tool_id TEXT
) RETURNS VOID LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security SET timezone='UTC' AS $$
DECLARE
    invoker RECORD;
    target TEXT;
    graph_granted BOOLEAN:=false;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
    OR invoker.owner_id<>'agent_control'
    OR NOT agent_control.runtime_identifier_valid(p_source_call_id)
    OR NOT agent_control.runtime_identifier_valid(p_tool_id) THEN
    RAISE EXCEPTION USING ERRCODE='22023',
      MESSAGE='invalid Cortex Specialist Tool grant check';
  END IF;

  SELECT handoff.target_role INTO target
  FROM agent_control.cortex_handoff AS handoff
  WHERE handoff.source_call_id=p_source_call_id
  FOR SHARE;

  IF NOT FOUND THEN
    SELECT node.role_id,true INTO target,graph_granted
    FROM agent_control.runtime_model_call_manifest AS manifest
    JOIN agent_control.runtime_turn AS turn
      ON turn.turn_id=manifest.turn_id
     AND turn.attempt_id=manifest.attempt_id
    JOIN agent_control.cortex_task_graph_node AS node
      ON node.task_id=turn.task_id
    JOIN agent_control.cortex_task_graph_tool_grant AS graph_grant
      ON graph_grant.graph_id=node.graph_id
     AND graph_grant.task_id=node.task_id
     AND graph_grant.role_id=node.role_id
     AND graph_grant.tool_id=p_tool_id
     AND graph_grant.tool_revision=1
     AND graph_grant.effect='read_only'
    JOIN agent_control.cortex_task_graph_schedule AS schedule
      ON schedule.graph_id=node.graph_id AND schedule.state='open'
    WHERE manifest.call_id=p_source_call_id
    FOR SHARE OF node,graph_grant,schedule;
  END IF;

  IF target IS NULL THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Tool handoff or TaskGraph grant is missing';
  END IF;
  IF p_tool_id IN (
      'kernel_review_equity_order','kernel_review_option_order'
  ) THEN
    IF target<>'desk' OR graph_granted THEN
      RAISE EXCEPTION USING ERRCODE='42501',
        MESSAGE='Cortex preflight is Decision Desk-only';
    END IF;
  ELSIF NOT EXISTS (
    SELECT 1
    FROM agent_control.cortex_specialist_tool_grant AS grant_row
    WHERE grant_row.role_id=target
      AND grant_row.tool_id=p_tool_id
      AND grant_row.effect='read_only'
  ) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='Cortex Specialist Tool grant denied';
  END IF;
END
$$;

REVOKE ALL ON FUNCTION
agent_control.enforce_cortex_specialist_tool_grant(TEXT,TEXT)
FROM PUBLIC;

RESET ROLE;
