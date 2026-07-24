-- Serialize replay evaluation on the Trigger head row, matching the live
-- sampler without granting advisory-lock primitives to application roles.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $migration$
DECLARE
  definition TEXT;
  original TEXT;
  advisory_fragment CONSTANT TEXT :=
'  PERFORM pg_advisory_xact_lock(hashtextextended(p_trigger_id,0));
';
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.record_cortex_moody_blues_replay_trigger_sample(text,numeric,timestamp with time zone,uuid,bigint,text,text,jsonb)'::regprocedure
  ) INTO definition;
  original:=definition;
  definition:=replace(definition,advisory_fragment,'');
  definition:=replace(
    definition,'  FOR SHARE OF head;','  FOR UPDATE OF head;'
  );
  IF definition=original
     OR position('pg_advisory_xact_lock' IN definition)<>0
     OR position('FOR UPDATE OF head' IN definition)=0 THEN
    RAISE EXCEPTION
      'expected Moody Blues replay Trigger row lock boundary';
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
