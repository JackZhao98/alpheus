-- Project admitted TaskGraph nodes whose immutable Session preparation was
-- interrupted.  The projection returns only Control-owned node snapshots and
-- the exact origin-specific raw input; callers cannot choose graph identity,
-- objective, role, Tool grants, budget, output contract, or input provenance.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.list_cortex_task_graph_nodes_pending_session(
  p_limit INTEGER
) RETURNS SETOF JSONB LANGUAGE plpgsql SECURITY DEFINER
SET search_path=pg_catalog,agent_control,agent_input,platform_security
SET timezone='UTC' AS $$
DECLARE
  invoker RECORD;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.profile_id<>'control-api'
     OR invoker.owner_id<>'agent_control'
     OR p_limit NOT BETWEEN 1 AND 64 THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='TaskGraph Session recovery projection denied';
  END IF;

  RETURN QUERY
  SELECT jsonb_build_object(
    'graph_id',graph.graph_id,
    'node',jsonb_build_object(
      'task_id',node.task_id,
      'role_id',node.role_id,
      'role_revision',node.role_revision,
      'depth',node.depth,
      'objective',node.objective,
      'input_refs',node.input_refs,
      'output_contract_name',node.output_contract_name,
      'output_contract',jsonb_build_object(
        'owner',node.output_contract_owner,
        'record_type',node.output_contract_record_type,
        'record_id',node.output_contract_revision_id,
        'schema_revision',node.output_contract_schema_revision,
        'record_digest',node.output_contract_digest::TEXT,
        'generation',node.output_contract_generation
      ),
      'tool_grants',COALESCE((
        SELECT jsonb_agg(jsonb_build_object(
          'tool_id',tool.tool_id,
          'tool_revision',tool.tool_revision,
          'effect',tool.effect
        ) ORDER BY tool.ordinal)
        FROM agent_control.cortex_task_graph_tool_grant AS tool
        WHERE tool.graph_id=node.graph_id
          AND tool.task_id=node.task_id
      ),'[]'::JSONB),
      'limit',node.task_limit,
      'deadline_at',agent_control.runtime_utc_text(node.deadline_at)
    ),
    'raw_input',COALESCE(request.raw_input,wake.raw_input)
  )
  FROM agent_control.cortex_task_graph_node AS node
  JOIN agent_control.cortex_task_graph AS graph
    ON graph.graph_id=node.graph_id
  JOIN agent_control.cortex_task_graph_schedule AS schedule
    ON schedule.graph_id=graph.graph_id
  JOIN agent_control.runtime_task AS task
    ON task.task_id=node.task_id
   AND task.run_id=graph.run_id
  JOIN agent_control.runtime_task AS parent
    ON parent.task_id=graph.parent_task_id
   AND parent.run_id=graph.run_id
  JOIN agent_control.runtime_run AS run
    ON run.run_id=graph.run_id
  LEFT JOIN agent_input.user_request AS request
    ON run.origin_kind='user_request'
   AND request.request_id=run.origin_source_record_id
   AND request.record_digest=run.origin_source_record_digest
  LEFT JOIN agent_control.cortex_decision_trigger_wake_admission AS wake
    ON run.origin_kind='external_event'
   AND wake.run_id=run.run_id
   AND wake.occurrence_id=run.occurrence_id
  WHERE task.session_id IS NULL
    AND task.state IN ('ready','blocked')
    AND parent.state='waiting'
    AND run.state IN ('running','waiting')
    AND schedule.state='open'
    AND task.deadline_at>clock_timestamp()
    AND run.deadline_at>clock_timestamp()
    AND (
      (request.request_id IS NOT NULL AND wake.occurrence_id IS NULL)
      OR
      (request.request_id IS NULL AND wake.occurrence_id IS NOT NULL)
    )
  ORDER BY graph.created_at,node.ordinal
  LIMIT p_limit;
END
$$;

REVOKE ALL ON FUNCTION
agent_control.list_cortex_task_graph_nodes_pending_session(INTEGER)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.list_cortex_task_graph_nodes_pending_session(INTEGER)
TO alpheus_agent_control_api;

RESET ROLE;
