-- A Decision Desk node becomes discoverable only after its immutable Join
-- resolution is ready. The Worker receives the exact downstream-session Blob
-- bindings created by Control; it never queries Join tables directly.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_projection TEXT :=
        '    graph_node.objective AS task_graph_objective,' || chr(10) ||
        '    graph_grant.tool_id AS task_graph_tool_id';
    new_projection TEXT :=
        '    graph_node.objective AS task_graph_objective,' || chr(10) ||
        '    graph_grant.tool_id AS task_graph_tool_id,' || chr(10) ||
        '    join_resolution.join_id AS task_graph_join_id,' || chr(10) ||
        '    join_resolution.inputs AS task_graph_join_inputs';
    old_join TEXT :=
        '  LEFT JOIN agent_control.cortex_task_graph_schedule AS graph_schedule' || chr(10) ||
        '    ON graph_schedule.graph_id=graph.graph_id' || chr(10) ||
        '  LEFT JOIN LATERAL (';
    new_join TEXT :=
        '  LEFT JOIN agent_control.cortex_task_graph_schedule AS graph_schedule' || chr(10) ||
        '    ON graph_schedule.graph_id=graph.graph_id' || chr(10) ||
        '  LEFT JOIN agent_control.cortex_task_graph_join_resolution AS join_resolution' || chr(10) ||
        '    ON join_resolution.graph_id=graph.graph_id' || chr(10) ||
        '   AND join_resolution.downstream_task_id=graph_node.task_id' || chr(10) ||
        '  LEFT JOIN LATERAL (';
    old_filter TEXT :=
        '        graph_node.role_id<>''decision_desk''' || chr(10) ||
        '        AND graph_grant.tool_id IS NULL' || chr(10) ||
        '        AND graph_schedule.state=''open''' || chr(10) ||
        '        AND graph_schedule.active_tasks<graph_schedule.limit_parallelism';
    new_filter TEXT :=
        '        graph_grant.tool_id IS NULL' || chr(10) ||
        '        AND graph_schedule.state=''open''' || chr(10) ||
        '        AND graph_schedule.active_tasks<graph_schedule.limit_parallelism' || chr(10) ||
        '        AND (' || chr(10) ||
        '          graph_node.role_id<>''decision_desk''' || chr(10) ||
        '          OR (' || chr(10) ||
        '            graph_node.role_id=''decision_desk''' || chr(10) ||
        '            AND join_resolution.outcome=''ready''' || chr(10) ||
        '            AND jsonb_array_length(join_resolution.inputs)>0' || chr(10) ||
        '          )' || chr(10) ||
        '        )';
    old_result TEXT :=
        '    ''task_graph_tool_id'',selected.task_graph_tool_id' || chr(10) ||
        '  );';
    new_result TEXT :=
        '    ''task_graph_tool_id'',selected.task_graph_tool_id,' || chr(10) ||
        '    ''task_graph_join_id'',selected.task_graph_join_id,' || chr(10) ||
        '    ''task_graph_join_inputs'',selected.task_graph_join_inputs' || chr(10) ||
        '  );';
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.next_cortex_task()'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,old_projection,new_projection);
    definition:=replace(definition,old_join,new_join);
    definition:=replace(definition,old_filter,new_filter);
    definition:=replace(definition,old_result,new_result);
    IF definition=original_definition
       OR position(new_projection IN definition)=0
       OR position(new_join IN definition)=0
       OR position(new_filter IN definition)=0
       OR position(new_result IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected Cortex Worker discovery definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task()
TO alpheus_agent_worker;

RESET ROLE;
