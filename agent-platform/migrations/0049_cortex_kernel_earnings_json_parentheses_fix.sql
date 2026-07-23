-- PostgreSQL gives JSON operators the same precedence as subtraction. Keep
-- every allow-listed object key check explicitly parenthesized so a key name
-- is never parsed as a JSON value.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef(
    'agent_control.record_cortex_kernel_earnings_results(text,jsonb)'::regprocedure
  ) INTO definition;

  IF position('p_observation-ARRAY' IN definition)=0
     OR position('item.value-ARRAY' IN definition)=0
     OR position('item.value->''eps''-ARRAY' IN definition)=0
     OR position('item.value->''report''-ARRAY' IN definition)=0 THEN
    RAISE EXCEPTION 'expected Kernel earnings observation validation definition';
  END IF;

  definition:=replace(
    definition,
    'p_observation-ARRAY[''schema_revision'',''tool_call_id'',''tool_id'',''request_digest'',''provider'',''symbol'',''found'',''results'',''observed_at'',''available_at'']<>''{}''::JSONB',
    '(p_observation-ARRAY[''schema_revision'',''tool_call_id'',''tool_id'',''request_digest'',''provider'',''symbol'',''found'',''results'',''observed_at'',''available_at''])<>''{}''::JSONB'
  );
  definition:=replace(
    definition,
    'item.value-ARRAY[''symbol'',''year'',''quarter'',''eps'',''report'']<>''{}''::JSONB',
    '(item.value-ARRAY[''symbol'',''year'',''quarter'',''eps'',''report''])<>''{}''::JSONB'
  );
  definition:=replace(
    definition,
    'item.value->''eps''-ARRAY[''estimate'',''actual'']<>''{}''::JSONB',
    '((item.value->''eps'')-ARRAY[''estimate'',''actual''])<>''{}''::JSONB'
  );
  definition:=replace(
    definition,
    'item.value->''report''-ARRAY[''date'',''timing'',''verified'']<>''{}''::JSONB',
    '((item.value->''report'')-ARRAY[''date'',''timing'',''verified''])<>''{}''::JSONB'
  );

  EXECUTE definition;
END $$;

RESET ROLE;
