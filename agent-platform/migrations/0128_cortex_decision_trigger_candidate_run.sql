-- Trigger wakes remain external-event, effect=none Runs, but their final
-- Decision Desk may now persist one effect-free Paper Candidate. Any later
-- Paper authorization is a separate, mode-fenced Control decision; Live is
-- not enabled by this contract revision.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE
  definition TEXT;
  old_contract TEXT:=$match$    WHERE revision_id='cortex-workflow-output-v8'
      AND effect_class='none'$match$;
  new_contract TEXT:=$match$    WHERE revision_id='cortex-workflow-output-v9'
      AND effect_class='none'$match$;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.admit_cortex_decision_trigger_wake(jsonb)'::regprocedure
  ) INTO definition;
  IF position(old_contract IN definition)=0 THEN
    RAISE EXCEPTION
      'expected Cortex Decision Trigger wake output contract';
  END IF;
  definition:=replace(definition,old_contract,new_contract);
  IF position('cortex-workflow-output-v9' IN definition)=0 THEN
    RAISE EXCEPTION
      'Candidate-aware Trigger wake contract was not installed';
  END IF;
  EXECUTE definition;
END
$$;

REVOKE ALL ON FUNCTION
agent_control.admit_cortex_decision_trigger_wake(JSONB)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.admit_cortex_decision_trigger_wake(JSONB)
TO alpheus_agent_control_api;

RESET ROLE;
