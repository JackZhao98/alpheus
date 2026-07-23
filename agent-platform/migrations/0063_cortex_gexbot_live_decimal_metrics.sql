-- Runtime receipt digests use integer-only canonical JSON. Preserve reviewed
-- GEX decimal metrics as exact decimal strings, matching the as_of contract.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.record_research_gexbot_live_receipt(text,jsonb)'::regprocedure) INTO definition;
  IF position('p_observation->''metrics''' IN definition)=0
     OR position('evidence_body JSONB; receipt_body JSONB; evidence_digest' IN definition)=0 THEN
    RAISE EXCEPTION 'expected GEXBOT live receipt metric definition';
  END IF;
  definition:=replace(definition,
    'evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64);',
    'evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64); metrics_value JSONB;');
  definition:=replace(definition,
    'source_value:=(p_observation->>''source_timestamp'')::TIMESTAMPTZ;',
    'SELECT COALESCE(jsonb_object_agg(key,value),''{}''::JSONB) INTO metrics_value FROM jsonb_each_text(p_observation->''metrics''); source_value:=(p_observation->>''source_timestamp'')::TIMESTAMPTZ;');
  definition:=replace(definition,'''metrics'',p_observation->''metrics'',''raw''','''metrics'',metrics_value,''raw''');
  definition:=replace(definition,'p_observation->''metrics'',','metrics_value,');
  IF position('metrics_value' IN definition)=0 OR position('''metrics'',p_observation->''metrics''' IN definition)<>0 THEN
    RAISE EXCEPTION 'GEXBOT live receipt metric normalization replacement failed';
  END IF;
  EXECUTE definition;
END $$;

REVOKE ALL ON FUNCTION agent_control.record_research_gexbot_live_receipt(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.record_research_gexbot_live_receipt(TEXT,JSONB) TO alpheus_research_gateway;

RESET ROLE;
