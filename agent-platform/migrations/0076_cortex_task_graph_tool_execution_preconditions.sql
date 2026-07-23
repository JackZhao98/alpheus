-- Tool-granted graph nodes require one bounded planning Turn and one
-- receipt-grounded memo Turn. Preserve the frozen v1 wire shape while
-- rejecting newly admitted plans that cannot pay for both model calls.
-- Discovery also carries the exact grant and planner-contract identities;
-- Tool nodes remain undiscoverable until the Worker execution migration.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $admission$
DECLARE
    definition TEXT;
    old_check TEXT:=
        '           OR (node#>>''{limit,max_model_calls}'')::BIGINT<1' ||
        chr(10) ||
        '           OR (node#>>''{limit,max_tasks}'')::BIGINT<>1';
    new_check TEXT:=
        '           OR (node#>>''{limit,max_model_calls}'')::BIGINT<1' ||
        chr(10) ||
        '           OR (' || chr(10) ||
        '               jsonb_array_length(node->''tool_grants'')>0' ||
        chr(10) ||
        '               AND (node#>>''{limit,max_model_calls}'')::BIGINT<2' ||
        chr(10) ||
        '           )' || chr(10) ||
        '           OR (node#>>''{limit,max_tasks}'')::BIGINT<>1';
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.cortex_task_graph_plan_valid(jsonb)'::REGPROCEDURE);
    IF position(old_check IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected TaskGraph admission validator';
    END IF;
    EXECUTE replace(definition,old_check,new_check);
END
$admission$;

DO $discovery$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_budget TEXT:=$old_budget$
    ) AS max_output_tokens,
    session.session_id,
$old_budget$;
    new_budget TEXT:=$new_budget$
    ) AS max_output_tokens,
    greatest(
      0,
      ledger.limit_model_calls-ledger.consumed_model_calls-
        ledger.reserved_model_calls
    ) AS max_model_calls,
    session.session_id,
$new_budget$;
    old_grant_projection TEXT:=$old_projection$
    graph_node.objective AS task_graph_objective,
    graph_grant.tool_id AS task_graph_tool_id,
$old_projection$;
    new_grant_projection TEXT:=$new_projection$
    graph_node.objective AS task_graph_objective,
    graph_grant.tool_id AS task_graph_tool_id,
    graph_grant.tool_revision AS task_graph_tool_revision,
    graph_grant.effect AS task_graph_tool_effect,
    CASE WHEN graph_grant.tool_id IS NULL THEN NULL ELSE
      (
        SELECT contract.record_digest::TEXT
        FROM agent_control.output_contract_revision AS contract
        WHERE contract.revision_id='cortex-workflow-output-v8'
          AND contract.generation=1
      )
    END AS task_graph_tool_planner_output_contract_digest,
$new_projection$;
    old_lateral TEXT:=$old_lateral$
  LEFT JOIN LATERAL (
    SELECT grant_row.tool_id
$old_lateral$;
    new_lateral TEXT:=$new_lateral$
  LEFT JOIN LATERAL (
    SELECT grant_row.tool_id,grant_row.tool_revision,grant_row.effect
$new_lateral$;
    old_result TEXT:=$old_result$
    'max_output_tokens',selected.max_output_tokens,
    'deadline',agent_control.runtime_utc_text(selected.deadline_at),
$old_result$;
    new_result TEXT:=$new_result$
    'max_output_tokens',selected.max_output_tokens,
    'max_model_calls',selected.max_model_calls,
    'deadline',agent_control.runtime_utc_text(selected.deadline_at),
$new_result$;
    old_tool_result TEXT:=$old_tool_result$
    'task_graph_tool_id',selected.task_graph_tool_id,
    'task_graph_join_id',selected.task_graph_join_id,
$old_tool_result$;
    new_tool_result TEXT:=$new_tool_result$
    'task_graph_tool_id',selected.task_graph_tool_id,
    'task_graph_tool_revision',selected.task_graph_tool_revision,
    'task_graph_tool_effect',selected.task_graph_tool_effect,
    'task_graph_tool_planner_output_contract_digest',
      selected.task_graph_tool_planner_output_contract_digest,
    'task_graph_join_id',selected.task_graph_join_id,
$new_tool_result$;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.next_cortex_task()'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,old_budget,new_budget);
    definition:=replace(
        definition,old_grant_projection,new_grant_projection);
    definition:=replace(definition,old_lateral,new_lateral);
    definition:=replace(definition,old_result,new_result);
    definition:=replace(definition,old_tool_result,new_tool_result);
    IF definition=original_definition
       OR position(new_budget IN definition)=0
       OR position(new_grant_projection IN definition)=0
       OR position(new_lateral IN definition)=0
       OR position(new_result IN definition)=0
       OR position(new_tool_result IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected Cortex Tool-node discovery definition';
    END IF;
    EXECUTE definition;
END
$discovery$;

REVOKE ALL ON FUNCTION
agent_control.cortex_task_graph_plan_valid(JSONB),
agent_control.next_cortex_task()
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task()
TO alpheus_agent_worker;

RESET ROLE;
