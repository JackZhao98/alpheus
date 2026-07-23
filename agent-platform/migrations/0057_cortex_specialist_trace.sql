-- Render the additional Specialist model Turn explicitly instead of
-- mislabeling every parent Turn after Intent as Decision Desk.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT; old_case TEXT; new_case TEXT; old_join TEXT; new_join TEXT;
BEGIN
  SELECT pg_get_functiondef(p.oid) INTO STRICT definition
    FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
    WHERE n.nspname='agent_control' AND p.proname='get_cortex_run_trace';

  old_case:=E'              WHEN continuation.admission_request_id IS NOT NULL OR turn.ordinal>1 THEN CASE turn.state WHEN ''result_committed'' THEN ''decision_desk_completed'' WHEN ''failed'' THEN ''decision_desk_failed'' ELSE ''decision_desk_in_progress'' END';
  new_case:=E'              WHEN specialist_handoff.handoff_id IS NOT NULL AND turn.ordinal=2 THEN CASE turn.state WHEN ''result_committed'' THEN specialist_handoff.target_role||''_completed'' WHEN ''failed'' THEN specialist_handoff.target_role||''_failed'' ELSE specialist_handoff.target_role||''_in_progress'' END\n              WHEN continuation.admission_request_id IS NOT NULL OR turn.ordinal>1 THEN CASE turn.state WHEN ''result_committed'' THEN ''decision_desk_completed'' WHEN ''failed'' THEN ''decision_desk_failed'' ELSE ''decision_desk_in_progress'' END';
  old_join:=E'        LEFT JOIN agent_control.cortex_parent_continuation continuation ON continuation.parent_task_id=turn.task_id AND continuation.parent_session_id=turn.session_id';
  new_join:=old_join||E'\n        LEFT JOIN agent_control.cortex_handoff specialist_handoff ON specialist_handoff.task_id=turn.task_id AND EXISTS (SELECT 1 FROM agent_control.cortex_agent_role_registry role WHERE role.role_id=specialist_handoff.target_role AND role.active)';

  IF position(old_case IN definition)=0 OR position(old_join IN definition)=0 THEN
    RAISE EXCEPTION 'Cortex Specialist trace injection point missing';
  END IF;
  definition:=replace(definition,old_case,new_case);
  definition:=replace(definition,old_join,new_join);
  EXECUTE definition;
END $$;

RESET ROLE;
