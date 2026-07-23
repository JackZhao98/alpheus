-- Canonical Runtime JSON is deliberately integer-only.  GEXBOT observations
-- legitimately contain decimal values, so receipt evidence keeps Provider
-- metric values as exact decimal strings rather than lossy floats.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.record_research_gexbot_as_of_receipt(text,jsonb)'::regprocedure) INTO definition;
  IF position('p_observation->''metrics''' IN definition)=0
     OR position('evidence_body JSONB; receipt_body JSONB; evidence_digest' IN definition)=0 THEN
    RAISE EXCEPTION 'expected GEXBOT receipt metric definition';
  END IF;
  definition:=replace(definition,
    'evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64); observed_value TIMESTAMPTZ; available_value TIMESTAMPTZ;',
    'evidence_body JSONB; receipt_body JSONB; evidence_digest CHAR(64); receipt_digest CHAR(64); observed_value TIMESTAMPTZ; available_value TIMESTAMPTZ; metrics_value JSONB;');
  definition:=replace(definition,
    'IF observed_value>available_value OR available_value>intent.request_as_of THEN',
    'SELECT COALESCE(jsonb_object_agg(key,value),''{}''::JSONB) INTO metrics_value FROM jsonb_each_text(p_observation->''metrics''); IF observed_value>available_value OR available_value>intent.request_as_of THEN');
  definition:=replace(definition,'''metrics'',p_observation->''metrics'',''raw''','''metrics'',metrics_value,''raw''');
  definition:=replace(definition,'p_observation->''metrics'',','metrics_value,');
  IF position('metrics_value' IN definition)=0 OR position('''metrics'',p_observation->''metrics''' IN definition)<>0 THEN
    RAISE EXCEPTION 'GEXBOT receipt metric normalization replacement failed';
  END IF;
  EXECUTE definition;
END $$;

RESET ROLE;
