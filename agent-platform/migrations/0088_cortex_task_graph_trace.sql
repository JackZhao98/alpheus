-- Preserve the legacy linear trace reader, then layer first-class TaskGraph
-- events over it. Graph Turns are removed from the legacy labels so a
-- Specialist can never appear as the Intent Interpreter or Decision Desk.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER FUNCTION agent_control.get_cortex_run_trace(TEXT)
  RENAME TO get_cortex_run_trace_legacy_v1;

CREATE FUNCTION agent_control.get_cortex_run_trace(p_run_id TEXT)
RETURNS JSONB
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path=pg_catalog,agent_control,platform_security
SET timezone='UTC'
AS $$
DECLARE
  invoker RECORD;
  legacy JSONB;
BEGIN
  SELECT * INTO STRICT invoker FROM platform_security.invoker_identity();
  IF invoker.group_role::TEXT<>'alpheus_agent_control_api'
     OR invoker.owner_id<>'agent_control'
     OR NOT agent_control.runtime_identifier_valid(p_run_id) THEN
    RAISE EXCEPTION USING ERRCODE='42501',
      MESSAGE='cortex trace read denied';
  END IF;

  legacy:=agent_control.get_cortex_run_trace_legacy_v1(p_run_id);
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
          'legacy:'||(item.payload->>'sequence') AS event_id,
          item.payload-'sequence' AS payload
        FROM jsonb_array_elements(legacy) AS item(payload)
        WHERE NOT EXISTS (
          SELECT 1
          FROM agent_control.cortex_task_graph_node AS node
          JOIN agent_control.cortex_task_graph AS graph
            ON graph.graph_id=node.graph_id
          WHERE graph.run_id=p_run_id
            AND node.task_id=item.payload->>'task_id'
        )
        AND NOT EXISTS (
          SELECT 1
          FROM agent_control.runtime_turn AS turn
          JOIN agent_control.runtime_model_call_manifest AS manifest
            ON manifest.turn_id=turn.turn_id
           AND manifest.attempt_id=turn.attempt_id
          JOIN agent_control.output_contract_revision AS contract
            ON contract.record_digest=manifest.output_contract_digest
           AND contract.revision_id=
             'cortex-task-graph-proposal-output-v1'
          WHERE turn.run_id=p_run_id
            AND turn.turn_id=item.payload->>'turn_id'
        )

        UNION ALL

        SELECT
          COALESCE(turn.finished_at,turn.updated_at,turn.created_at),
          15,
          'graph-proposal:'||turn.turn_id,
          jsonb_build_object(
            'created_at',agent_control.runtime_utc_text(
              COALESCE(turn.finished_at,turn.updated_at,turn.created_at)),
            'stage',CASE turn.state
              WHEN 'result_committed' THEN 'task_graph_proposal_completed'
              WHEN 'failed' THEN 'task_graph_proposal_failed'
              ELSE 'task_graph_proposal_in_progress'
            END,
            'turn_id',turn.turn_id,
            'task_id',turn.task_id,
            'state',turn.state
          )
        FROM agent_control.runtime_turn AS turn
        JOIN agent_control.runtime_model_call_manifest AS manifest
          ON manifest.turn_id=turn.turn_id
         AND manifest.attempt_id=turn.attempt_id
        JOIN agent_control.output_contract_revision AS contract
          ON contract.record_digest=manifest.output_contract_digest
         AND contract.revision_id='cortex-task-graph-proposal-output-v1'
        WHERE turn.run_id=p_run_id

        UNION ALL

        SELECT
          graph.created_at,
          20,
          'graph-admitted:'||graph.graph_id,
          jsonb_build_object(
            'created_at',agent_control.runtime_utc_text(graph.created_at),
            'stage','task_graph_admitted',
            'graph_id',graph.graph_id,
            'parent_task_id',graph.parent_task_id,
            'round',graph.round,
            'max_rounds',graph.max_rounds,
            'max_parallelism',
              (graph.authorized_limit->>'max_parallelism')::BIGINT,
            'task_count',(
              SELECT count(*)
              FROM agent_control.cortex_task_graph_node AS counted
              WHERE counted.graph_id=graph.graph_id
            ),
            'nodes',(
              SELECT jsonb_agg(
                jsonb_strip_nulls(jsonb_build_object(
                  'task_id',node.task_id,
                  'role_id',node.role_id,
                  'depth',node.depth,
                  'tool_id',tool_grant.tool_id
                ))
                ORDER BY node.ordinal
              )
              FROM agent_control.cortex_task_graph_node AS node
              LEFT JOIN agent_control.cortex_task_graph_tool_grant AS tool_grant
                ON tool_grant.graph_id=node.graph_id
               AND tool_grant.task_id=node.task_id
              WHERE node.graph_id=graph.graph_id
            )
          )
        FROM agent_control.cortex_task_graph AS graph
        WHERE graph.run_id=p_run_id

        UNION ALL

        SELECT
          COALESCE(turn.finished_at,turn.updated_at,turn.created_at),
          30,
          'graph-turn:'||turn.turn_id,
          jsonb_build_object(
            'created_at',agent_control.runtime_utc_text(
              COALESCE(turn.finished_at,turn.updated_at,turn.created_at)),
            'stage',CASE
              WHEN node.role_id='decision_desk' THEN CASE turn.state
                WHEN 'result_committed'
                  THEN 'task_graph_decision_desk_completed'
                WHEN 'failed' THEN 'task_graph_decision_desk_failed'
                ELSE 'task_graph_decision_desk_in_progress'
              END
              ELSE CASE turn.state
                WHEN 'result_committed' THEN 'task_graph_branch_completed'
                WHEN 'failed' THEN 'task_graph_branch_failed'
                ELSE 'task_graph_branch_in_progress'
              END
            END,
            'graph_id',graph.graph_id,
            'turn_id',turn.turn_id,
            'task_id',turn.task_id,
            'role_id',node.role_id,
            'state',turn.state
          )
        FROM agent_control.runtime_turn AS turn
        JOIN agent_control.cortex_task_graph_node AS node
          ON node.task_id=turn.task_id
        JOIN agent_control.cortex_task_graph AS graph
          ON graph.graph_id=node.graph_id
        WHERE turn.run_id=p_run_id

        UNION ALL

        SELECT
          resolution.resolved_at,
          40,
          'graph-join:'||resolution.graph_id||':'||resolution.join_id,
          jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(resolution.resolved_at),
            'stage',CASE resolution.outcome
              WHEN 'ready' THEN 'task_graph_join_ready'
              ELSE 'task_graph_join_failed'
            END,
            'graph_id',resolution.graph_id,
            'join_id',resolution.join_id,
            'task_id',resolution.downstream_task_id,
            'outcome',resolution.outcome,
            'join_policy',join_row.policy,
            'minimum_success',join_row.minimum_success,
            'successful_task_ids',
              resolution.successful_upstream_task_ids,
            'failed_task_ids',resolution.failed_upstream_task_ids
          )
        FROM agent_control.cortex_task_graph_join_resolution AS resolution
        JOIN agent_control.cortex_task_graph_join AS join_row
          ON join_row.graph_id=resolution.graph_id
         AND join_row.join_id=resolution.join_id
        JOIN agent_control.cortex_task_graph AS graph
          ON graph.graph_id=resolution.graph_id
        WHERE graph.run_id=p_run_id

        UNION ALL

        SELECT
          result.recorded_at,
          50,
          'graph-result:'||result.graph_id,
          jsonb_build_object(
            'created_at',agent_control.runtime_utc_text(result.recorded_at),
            'stage','task_graph_succeeded',
            'graph_id',result.graph_id,
            'parent_task_id',result.parent_task_id,
            'task_id',result.decision_task_id,
            'artifact_id',result.artifact_id,
            'state','succeeded'
          )
        FROM agent_control.cortex_task_graph_result AS result
        WHERE result.run_id=p_run_id
      ) AS raw
    ) AS event
  ),'[]'::JSONB);
END
$$;

REVOKE ALL ON FUNCTION
  agent_control.get_cortex_run_trace_legacy_v1(TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.get_cortex_run_trace(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.get_cortex_run_trace(TEXT)
  TO alpheus_agent_control_api;

RESET ROLE;
