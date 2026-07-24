-- Parenthesize the nested metrics object before applying JSONB key removal.
-- Without it PostgreSQL binds the subtraction before the JSON accessor.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
  definition TEXT;
  original TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.record_cortex_moody_blues_replay_trigger_sample(text,numeric,timestamp with time zone,uuid,bigint,text,text,jsonb)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(
    definition,
    $old$     OR p_normalized->'metrics'-ARRAY[$old$,
    $new$     OR (p_normalized->'metrics')-ARRAY[$new$
  );
  IF definition=original
     OR position(
       '(p_normalized->''metrics'')-ARRAY[' IN definition
     )=0 THEN
    RAISE EXCEPTION
      'expected Moody Blues replay normalized metrics boundary';
  END IF;
  EXECUTE definition;
END
$migration$;

REVOKE ALL ON FUNCTION
agent_control.record_cortex_moody_blues_replay_trigger_sample(
  TEXT,NUMERIC,TIMESTAMPTZ,UUID,BIGINT,TEXT,TEXT,JSONB
)
FROM PUBLIC;
GRANT EXECUTE ON FUNCTION
agent_control.record_cortex_moody_blues_replay_trigger_sample(
  TEXT,NUMERIC,TIMESTAMPTZ,UUID,BIGINT,TEXT,TEXT,JSONB
)
TO alpheus_agent_control_api;

RESET ROLE;
