-- A retried parent Task can own more than one immutable handoff. Bind a
-- Specialist trace label to the handoff from the same Attempt so events do not
-- multiply across retries.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT; old_join TEXT; new_join TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.get_cortex_run_trace(text)'::regprocedure) INTO definition;
  old_join:='LEFT JOIN agent_control.cortex_handoff specialist_handoff ON specialist_handoff.task_id=turn.task_id AND EXISTS';
  new_join:='LEFT JOIN agent_control.cortex_handoff specialist_handoff ON specialist_handoff.task_id=turn.task_id AND specialist_handoff.attempt_id=turn.attempt_id AND EXISTS';
  IF position(old_join IN definition)=0 THEN
    RAISE EXCEPTION 'Cortex Specialist trace Attempt join point missing';
  END IF;
  EXECUTE replace(definition,old_join,new_join);
END $$;

RESET ROLE;
