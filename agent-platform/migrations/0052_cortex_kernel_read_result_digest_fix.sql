-- The Control function has a deliberately narrow search_path. Use the
-- Control-owned canonical digest helper instead of relying on the extension
-- schema that owns pgcrypto.digest.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

DO $$
DECLARE definition TEXT;
BEGIN
  SELECT pg_get_functiondef('agent_control.record_cortex_kernel_read(text,jsonb)'::regprocedure) INTO definition;
  IF position('result_digest_value:=encode(digest(convert_to(result_text,''UTF8''),''sha256''),''hex'');' IN definition)=0 THEN
    RAISE EXCEPTION 'expected Cortex Kernel read result digest definition';
  END IF;
  definition:=replace(
    definition,
    'result_digest_value:=encode(digest(convert_to(result_text,''UTF8''),''sha256''),''hex'');',
    'result_digest_value:=agent_control.runtime_contract_digest(''agent-platform.contract.kernel_read_result.v1'',jsonb_build_object(''result_json'',result_text));'
  );
  EXECUTE definition;
END $$;

REVOKE ALL ON FUNCTION agent_control.record_cortex_kernel_read(TEXT,JSONB) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION agent_control.record_cortex_kernel_read(TEXT,JSONB) TO alpheus_agent_control_api;

RESET ROLE;
