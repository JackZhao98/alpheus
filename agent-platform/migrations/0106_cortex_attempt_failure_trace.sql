-- Project durable, sanitized Attempt failures into the Cortex Run trace.
-- Raw failure messages remain private because they can contain provider or
-- infrastructure detail; the product surface receives only reviewed codes.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER FUNCTION agent_control.get_cortex_run_trace(TEXT)
  RENAME TO get_cortex_run_trace_pre_attempt_failure_v1;

CREATE FUNCTION agent_control.get_cortex_run_trace(p_run_id TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
  invoker RECORD;
  base_trace JSONB;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_run_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='cortex trace read denied';
  END IF;

  base_trace:=
    agent_control.get_cortex_run_trace_pre_attempt_failure_v1(p_run_id);
  RETURN COALESCE((
    SELECT jsonb_agg(
      event.payload||jsonb_build_object('sequence',event.sequence)
      ORDER BY event.occurred_at,event.order_key,event.event_id
    )
    FROM (
      SELECT raw.occurred_at,raw.order_key,raw.event_id,raw.payload,
        row_number() OVER (
          ORDER BY raw.occurred_at,raw.order_key,raw.event_id
        ) AS sequence
      FROM (
        SELECT
          (item.payload->>'created_at')::TIMESTAMPTZ AS occurred_at,
          10 AS order_key,
          'base:'||(item.payload->>'sequence') AS event_id,
          item.payload-'sequence' AS payload
        FROM jsonb_array_elements(base_trace) AS item(payload)

        UNION ALL

        SELECT
          attempt.terminal_at,
          70,
          'attempt-failure:'||attempt.attempt_id,
          jsonb_strip_nulls(jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(attempt.terminal_at),
            'stage',CASE
              WHEN tool_grant.tool_id IS NOT NULL
                THEN 'tool_branch_failed'
              WHEN node.task_id IS NOT NULL
                THEN 'task_graph_branch_failed'
              ELSE 'cortex_attempt_failed'
            END,
            'state',attempt.state,
            'task_id',attempt.task_id,
            'role_id',node.role_id,
            'tool_id',tool_grant.tool_id,
            'error_code',attempt.failure->>'code',
            'retryable',
              (attempt.failure->>'retryable')::BOOLEAN
          ))
        FROM agent_control.runtime_attempt AS attempt
        LEFT JOIN agent_control.cortex_task_graph_node AS node
          ON node.task_id=attempt.task_id
        LEFT JOIN agent_control.cortex_task_graph_tool_grant AS tool_grant
          ON tool_grant.graph_id=node.graph_id
         AND tool_grant.task_id=node.task_id
        WHERE attempt.run_id=p_run_id
          AND attempt.state IN ('failed','timed_out')
          AND attempt.failure IS NOT NULL
          AND attempt.terminal_at IS NOT NULL
      ) AS raw
    ) AS event
  ),'[]'::JSONB);
END
$$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_run_trace_pre_attempt_failure_v1(TEXT),
agent_control.get_cortex_run_trace(TEXT)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_run_trace(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
