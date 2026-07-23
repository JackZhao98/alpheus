-- PostgreSQL gives JSON operators the same precedence as subtraction.  Keep
-- the raw-object key check explicitly parenthesized so it subtracts keys from
-- the JSON object rather than trying to parse the key name as JSON.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.record_research_gexbot_as_of_receipt(text,jsonb)'::regprocedure) INTO definition;
  IF position('p_observation->''raw''-ARRAY' IN definition)=0 THEN
    RAISE EXCEPTION 'expected GEXBOT raw-object validation definition';
  END IF;
  definition:=replace(definition,'p_observation->''raw''-ARRAY','(p_observation->''raw'')-ARRAY');
  EXECUTE definition;
END $$;

RESET ROLE;
