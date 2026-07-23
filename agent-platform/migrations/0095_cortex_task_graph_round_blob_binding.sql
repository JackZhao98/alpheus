-- bind_reference_internal receives the owning record type, id and digest; its
-- first argument already identifies the binding owner. Remove the duplicated
-- agent_control value from the round-decision binding.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
    definition TEXT;
    original_definition TEXT;
    old_fragment TEXT:=$old$
      (result_row.output->>'blob_id')::UUID,
      'agent_control','model_call_result',result_row.result_id,
      result_row.record_digest::TEXT,invoker.principal_id,
$old$;
    new_fragment TEXT:=$new$
      (result_row.output->>'blob_id')::UUID,
      'model_call_result',result_row.result_id,
      result_row.record_digest::TEXT,invoker.principal_id,
$new$;
BEGIN
    definition:=pg_get_functiondef(
      'agent_control.prepare_cortex_task_graph_next_round(text,text,bigint,uuid,jsonb,jsonb,text)'::REGPROCEDURE);
    original_definition:=definition;
    definition:=replace(definition,old_fragment,new_fragment);
    IF definition=original_definition
       OR position(new_fragment IN definition)=0 THEN
        RAISE EXCEPTION 'Unexpected TaskGraph next round binding definition';
    END IF;
    EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT)
TO alpheus_agent_control_api;

RESET ROLE;
