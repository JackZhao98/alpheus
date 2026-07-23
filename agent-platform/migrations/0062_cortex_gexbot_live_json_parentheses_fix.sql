-- PostgreSQL binds JSON extraction less tightly than JSONB key subtraction.
-- Parenthesize the extracted raw object so the closed-key check cannot parse
-- the string literal "raw" as JSON input.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT; fixed TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.record_research_gexbot_live_receipt(text,jsonb)'::regprocedure) INTO definition;
  IF position('p_observation->''raw''-ARRAY[' IN definition)=0 THEN
    RAISE EXCEPTION 'expected GEXBOT live raw-object validation definition';
  END IF;
  fixed:=replace(definition,
    'p_observation->''raw''-ARRAY[',
    '(p_observation->''raw'')-ARRAY[');
  EXECUTE fixed;
END $$;

REVOKE ALL ON FUNCTION agent_control.record_research_gexbot_live_receipt(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.record_research_gexbot_live_receipt(TEXT,JSONB) TO alpheus_research_gateway;

RESET ROLE;
