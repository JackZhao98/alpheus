-- The candidate-aware v2 round contract was enabled for seed selection and
-- Worker decoding in 0126, but the atomic continuation command still accepted
-- only v1. Keep both immutable contracts explicit at the state transition.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
  definition TEXT;
  original TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.prepare_cortex_task_graph_next_round(text,text,bigint,uuid,jsonb,jsonb,text)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(
    definition,
    '           ON contract.revision_id=''cortex-task-graph-round-output-v1'''||
    chr(10),
    '           ON contract.revision_id IN ('||chr(10)||
    '             ''cortex-task-graph-round-output-v1'','||chr(10)||
    '             ''cortex-task-graph-round-output-v2'''||chr(10)||
    '           )'||chr(10)
  );
  IF definition=original
     OR position('cortex-task-graph-round-output-v2' IN definition)=0 THEN
    RAISE EXCEPTION
      'expected Candidate TaskGraph next round contract boundary';
  END IF;
  EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT
)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.prepare_cortex_task_graph_next_round(
  TEXT,TEXT,BIGINT,UUID,JSONB,JSONB,TEXT
)
TO alpheus_agent_control_api;

RESET ROLE;
