-- A TaskGraph parks its parent while child lanes execute. The graph admission
-- transaction has already charged the descendants and must release the
-- parent's active slot so the admitted max_parallelism is actually usable.
-- No other nonterminal Task may release its slot.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER TABLE agent_control.runtime_task
  DROP CONSTRAINT runtime_task_check4;
ALTER TABLE agent_control.runtime_task
  ADD CONSTRAINT runtime_task_check4 CHECK (
    (state IN ('running','result_committed') AND budget_slot_held)
    OR state='ready'
    OR (state='waiting')
    OR (
      state IN (
        'blocked','succeeded','failed','canceled','superseded','dead_lettered'
      )
      AND NOT budget_slot_held
    )
  );

CREATE OR REPLACE FUNCTION agent_control.guard_runtime_task_budget_slot()
RETURNS trigger
LANGUAGE plpgsql
SET search_path=pg_catalog,agent_control
AS $$
BEGIN
  IF TG_OP='INSERT' THEN
    IF NEW.budget_slot_held THEN
      RAISE EXCEPTION USING ERRCODE='23514',
        MESSAGE='initial Task cannot hold a budget slot';
    END IF;
    RETURN NEW;
  END IF;

  IF NOT OLD.budget_slot_held AND NEW.budget_slot_held
     AND NOT (OLD.state='ready' AND NEW.state='running') THEN
    RAISE EXCEPTION USING ERRCODE='40001',
      MESSAGE='Task budget slot may only be acquired on ready to running';
  END IF;
  IF OLD.budget_slot_held AND NOT NEW.budget_slot_held
     AND NOT agent_control.runtime_terminal_state('task',NEW.state)
     AND NOT (
       OLD.state='running'
       AND NEW.state='waiting'
       AND EXISTS (
         SELECT 1
         FROM agent_control.cortex_task_graph AS graph
         WHERE graph.run_id=NEW.run_id
           AND graph.parent_task_id=NEW.task_id
       )
     ) THEN
    RAISE EXCEPTION USING ERRCODE='40001',
      MESSAGE='Task budget slot must remain held until terminal';
  END IF;
  RETURN NEW;
END
$$;

RESET ROLE;
