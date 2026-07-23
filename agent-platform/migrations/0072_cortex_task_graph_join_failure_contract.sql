-- Preserve the runtime terminal-state contract when a failed Join closes its
-- downstream Task, parked parent Task, and Run. Migration 0071 is immutable in
-- the ledger, so this migration replaces only the installed function body.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    terminal_fragment TEXT :=
        'state_generation=state_generation+1,' || chr(10) ||
        '                updated_at=greatest(updated_at,at_time),' || chr(10) ||
        '                terminal_at=at_time';
    terminal_replacement TEXT :=
        'state_generation=state_generation+1,' || chr(10) ||
        '                failure=jsonb_build_object(' || chr(10) ||
        '                    ''code'',''task_graph_join_failed'',' || chr(10) ||
        '                    ''message'',''TaskGraph Join threshold was not met'',' || chr(10) ||
        '                    ''retryable'',false),' || chr(10) ||
        '                updated_at=greatest(updated_at,at_time),' || chr(10) ||
        '                terminal_at=at_time';
    parent_fragment TEXT :=
        'state_generation=state_generation+1,' || chr(10) ||
        '                budget_slot_held=false,' || chr(10) ||
        '                updated_at=greatest(updated_at,at_time),' || chr(10) ||
        '                terminal_at=at_time';
    parent_replacement TEXT :=
        'state_generation=state_generation+1,' || chr(10) ||
        '                budget_slot_held=false,' || chr(10) ||
        '                failure=jsonb_build_object(' || chr(10) ||
        '                    ''code'',''task_graph_join_failed'',' || chr(10) ||
        '                    ''message'',''TaskGraph Join threshold was not met'',' || chr(10) ||
        '                    ''retryable'',false),' || chr(10) ||
        '                updated_at=greatest(updated_at,at_time),' || chr(10) ||
        '                terminal_at=at_time';
BEGIN
    definition:=pg_get_functiondef(
        'agent_control.reconcile_cortex_task_graph_joins(text)'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,terminal_fragment,terminal_replacement);
    definition:=replace(definition,parent_fragment,parent_replacement);
    IF definition=original_definition
       OR (length(definition)-length(replace(
            definition,'''code'',''task_graph_join_failed''','')))
          / length('''code'',''task_graph_join_failed''')<>3 THEN
        RAISE EXCEPTION 'Unexpected TaskGraph Join reconciler definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.reconcile_cortex_task_graph_joins(TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
