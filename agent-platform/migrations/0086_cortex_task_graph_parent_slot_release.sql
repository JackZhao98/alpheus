-- Parking the root Task behind a TaskGraph must release its Runtime active
-- slot. Otherwise a four-lane graph can start only three children even though
-- its own graph schedule correctly allows four.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
  definition TEXT;
  old_transition TEXT;
  new_transition TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.admit_cortex_task_graph(jsonb)'::regprocedure
  ) INTO definition;
  old_transition:=
    '    UPDATE agent_control.runtime_task SET' || chr(10) ||
    '        state=''waiting'',state_generation=state_generation+1,' ||
    chr(10) ||
    '        updated_at=greatest(updated_at,at_time)' || chr(10) ||
    '    WHERE task_id=parent_task.task_id;';
  new_transition:=
    '    IF parent_task.budget_slot_held AND NOT' || chr(10) ||
    '       agent_control.runtime_release_active_slot_ancestors(' ||
    chr(10) ||
    '         run_row.run_id,parent_ledger.ledger_id,at_time) THEN' ||
    chr(10) ||
    '        RAISE EXCEPTION USING ERRCODE=''55000'',' || chr(10) ||
    '          MESSAGE=''TaskGraph parent active slot release failed'';' ||
    chr(10) ||
    '    END IF;' || chr(10) ||
    '    UPDATE agent_control.runtime_task SET' || chr(10) ||
    '        state=''waiting'',state_generation=state_generation+1,' ||
    chr(10) ||
    '        budget_slot_held=false,' || chr(10) ||
    '        updated_at=greatest(updated_at,at_time)' || chr(10) ||
    '    WHERE task_id=parent_task.task_id;';
  IF position(old_transition IN definition)=0 THEN
    RAISE EXCEPTION
      'unexpected TaskGraph parent waiting transition definition';
  END IF;
  EXECUTE replace(definition,old_transition,new_transition);
END
$$;

REVOKE ALL ON FUNCTION
  agent_control.admit_cortex_task_graph(JSONB)
  FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
  agent_control.admit_cortex_task_graph(JSONB)
  TO alpheus_agent_control_api;

RESET ROLE;
