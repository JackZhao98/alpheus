-- Runtime canonical JSON rejects non-integer JSON numbers. Preserve GEX
-- decimals as exponent-free strings in the normalized frame and cast them
-- explicitly to NUMERIC only at deterministic Trigger evaluation.
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
    $old$)<>'number'$old$,
    $new$)<>'string'$new$
  );
  IF definition=original
     OR (
       length(definition)-
       length(replace(definition,')<>''string''',''))
     )/length(')<>''string''')<>4 THEN
    RAISE EXCEPTION
      'expected four Moody Blues decimal metric boundaries';
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
