-- Enable only TaskGraph Specialist nodes whose exact read-only Tool snapshot
-- and two-call budget survived Control admission. Decision Desk remains
-- Join-gated and Tool-free.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    old_filter TEXT:=$old$
        graph_grant.tool_id IS NULL
        AND graph_schedule.state='open'
        AND graph_schedule.active_tasks<graph_schedule.limit_parallelism
        AND (
          graph_node.role_id<>'decision_desk'
          OR (
            graph_node.role_id='decision_desk'
            AND join_resolution.outcome='ready'
            AND jsonb_array_length(join_resolution.inputs)>0
          )
        )
$old$;
    new_filter TEXT:=$new$
        graph_schedule.state='open'
        AND graph_schedule.active_tasks<graph_schedule.limit_parallelism
        AND (
          (
            graph_node.role_id<>'decision_desk'
            AND (
              graph_grant.tool_id IS NULL
              OR (
                graph_grant.tool_revision=1
                AND graph_grant.effect='read_only'
                AND (
                  ledger.limit_model_calls-ledger.consumed_model_calls-
                    ledger.reserved_model_calls
                )>=2
              )
            )
          )
          OR (
            graph_node.role_id='decision_desk'
            AND graph_grant.tool_id IS NULL
            AND join_resolution.outcome='ready'
            AND jsonb_array_length(join_resolution.inputs)>0
          )
        )
$new$;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.next_cortex_task()'::REGPROCEDURE);
    IF position(old_filter IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected TaskGraph Tool discovery filter';
    END IF;
    EXECUTE replace(definition,old_filter,new_filter);
END
$migration$;

REVOKE ALL ON FUNCTION agent_control.next_cortex_task() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.next_cortex_task()
TO alpheus_agent_worker;

RESET ROLE;
