-- Expose the durable Decision Desk continuation boundary between two graph
-- rounds. This event is derived from the immutable continuation record.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_fragment TEXT:=$old$
        UNION ALL

        SELECT
          recovery.recovered_at,
          90,
$old$;
    new_fragment TEXT:=$new$
        UNION ALL

        SELECT
          continuation.created_at,
          45,
          'graph-round-continuation:'||continuation.source_call_id,
          jsonb_build_object(
            'created_at',
              agent_control.runtime_utc_text(continuation.created_at),
            'stage','task_graph_round_continued',
            'state',continuation.state,
            'graph_id',continuation.graph_id,
            'parent_task_id',continuation.parent_task_id,
            'decision_task_id',continuation.decision_task_id,
            'source_call_id',continuation.source_call_id,
            'round',continuation.completed_round,
            'next_round',continuation.completed_round+1,
            'max_rounds',continuation.max_rounds
          )
        FROM agent_control.cortex_task_graph_round_continuation
          AS continuation
        WHERE continuation.run_id=p_run_id

        UNION ALL

        SELECT
          recovery.recovered_at,
          90,
$new$;
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.get_cortex_run_trace(text)'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,old_fragment,new_fragment);
    IF definition=original_definition
       OR position('graph-round-continuation:' IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected Cortex TaskGraph round trace definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.get_cortex_run_trace(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.get_cortex_run_trace(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
