-- A Task's immutable output contract remains its final Artifact contract.
-- Permit only two reviewed intermediate model contracts: the argument planner
-- of an exact Tool-granted graph node, and one proposal after a root Intent
-- result. Both outputs still require normal schema validation and persistence.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

CREATE FUNCTION agent_control.cortex_intermediate_output_contract_allowed(
  p_task_id TEXT,
  p_attempt_id TEXT,
  p_output_contract_digest TEXT
)
RETURNS BOOLEAN
LANGUAGE sql
STABLE
STRICT
SET search_path=pg_catalog,agent_control
SET timezone='UTC'
AS $$
  SELECT
    EXISTS (
      SELECT 1
      FROM agent_control.runtime_attempt AS attempt
      JOIN agent_control.cortex_task_graph_node AS node
        ON node.task_id=attempt.task_id
      JOIN agent_control.cortex_task_graph_tool_grant AS grant_row
        ON grant_row.graph_id=node.graph_id
       AND grant_row.task_id=node.task_id
       AND grant_row.role_id=node.role_id
       AND grant_row.tool_revision=1
       AND grant_row.effect='read_only'
      JOIN agent_control.cortex_task_graph_schedule AS schedule
        ON schedule.graph_id=node.graph_id
       AND schedule.state='open'
      JOIN agent_control.output_contract_revision AS contract
        ON contract.revision_id='cortex-workflow-output-v8'
       AND contract.generation=1
       AND contract.record_digest::TEXT=p_output_contract_digest
       AND contract.effect_class='none'
      WHERE attempt.attempt_id=p_attempt_id
        AND attempt.task_id=p_task_id
        AND node.role_id<>'decision_desk'
        AND NOT EXISTS (
          SELECT 1
          FROM agent_control.runtime_turn AS prior_turn
          JOIN agent_control.runtime_model_call_result AS prior_result
            ON prior_result.turn_id=prior_turn.turn_id
           AND prior_result.attempt_id=prior_turn.attempt_id
          JOIN agent_control.runtime_model_call_manifest AS prior_manifest
            ON prior_manifest.call_id=prior_result.call_id
          WHERE prior_turn.attempt_id=attempt.attempt_id
            AND prior_manifest.output_contract_digest=
              contract.record_digest
        )
    )
    OR EXISTS (
      SELECT 1
      FROM agent_control.runtime_attempt AS attempt
      JOIN agent_control.runtime_task AS task
        ON task.task_id=attempt.task_id
       AND task.parent_task_id IS NULL
       AND task.output_contract_revision_id='cortex-workflow-output-v8'
      JOIN agent_control.output_contract_revision AS proposal_contract
        ON proposal_contract.revision_id=
          'cortex-task-graph-proposal-output-v1'
       AND proposal_contract.generation=1
       AND proposal_contract.record_digest::TEXT=p_output_contract_digest
       AND proposal_contract.effect_class='none'
      WHERE attempt.attempt_id=p_attempt_id
        AND attempt.task_id=p_task_id
        AND NOT EXISTS (
          SELECT 1
          FROM agent_control.cortex_task_graph AS graph
          WHERE graph.parent_task_id=task.task_id
        )
        AND EXISTS (
          SELECT 1
          FROM agent_control.runtime_turn AS intent_turn
          JOIN agent_control.runtime_model_call_result AS intent_result
            ON intent_result.turn_id=intent_turn.turn_id
           AND intent_result.attempt_id=intent_turn.attempt_id
          JOIN agent_control.runtime_model_call_manifest AS intent_manifest
            ON intent_manifest.call_id=intent_result.call_id
          WHERE intent_turn.attempt_id=attempt.attempt_id
            AND intent_turn.state='result_committed'
            AND intent_manifest.output_contract_digest=
              task.output_contract_digest
        )
        AND NOT EXISTS (
          SELECT 1
          FROM agent_control.runtime_turn AS proposal_turn
          JOIN agent_control.runtime_model_call_result AS proposal_result
            ON proposal_result.turn_id=proposal_turn.turn_id
           AND proposal_result.attempt_id=proposal_turn.attempt_id
          JOIN agent_control.runtime_model_call_manifest AS proposal_manifest
            ON proposal_manifest.call_id=proposal_result.call_id
          WHERE proposal_turn.attempt_id=attempt.attempt_id
            AND proposal_manifest.output_contract_digest=
              proposal_contract.record_digest
        )
    )
$$;

DO $$
DECLARE
  definition TEXT;
  old_check TEXT;
  new_check TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.runtime_dispatch_model_call(jsonb)'::regprocedure
  ) INTO definition;
  old_check:='    IF manifest->>''output_contract_digest''' || chr(10) ||
    '       <> task_row.output_contract_digest::TEXT THEN';
  new_check:='    IF manifest->>''output_contract_digest''' || chr(10) ||
    '       <> task_row.output_contract_digest::TEXT' || chr(10) ||
    '       AND NOT agent_control.' ||
    'cortex_intermediate_output_contract_allowed(' || chr(10) ||
    '         task_row.task_id,attempt_row.attempt_id,' || chr(10) ||
    '         manifest->>''output_contract_digest'') THEN';
  IF position(old_check IN definition)=0 THEN
    RAISE EXCEPTION
      'unexpected Runtime model dispatch output-contract definition';
  END IF;
  EXECUTE replace(definition,old_check,new_check);
END $$;

REVOKE ALL ON FUNCTION
  agent_control.cortex_intermediate_output_contract_allowed(TEXT,TEXT,TEXT)
  FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.runtime_dispatch_model_call(JSONB)
  FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.runtime_dispatch_model_call(JSONB)
  TO alpheus_agent_worker;

RESET ROLE;
