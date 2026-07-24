-- A request event describes the cancellation boundary at request time. Keep
-- its state as canceling even after the same durable record reaches canceled.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
  definition TEXT;
  original_definition TEXT;
  old_fragment TEXT:=$old$
            'state',CASE WHEN cancellation.state='pending'
              THEN 'canceling' ELSE cancellation.state END,
$old$;
  new_fragment TEXT:=$new$
            'state','canceling',
$new$;
BEGIN
  definition:=pg_get_functiondef(
    'agent_control.get_cortex_run_trace(text)'::REGPROCEDURE);
  original_definition:=definition;
  definition:=replace(definition,old_fragment,new_fragment);
  IF definition=original_definition THEN
    RAISE EXCEPTION
      'Unexpected Cortex cancellation trace definition';
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
