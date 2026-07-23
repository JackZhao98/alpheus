-- The dispatch and deferred-guard functions call this predicate while serving
-- an unprivileged Worker. It owns no mutation and remains non-callable by the
-- Worker or PUBLIC, but must read Control tables under the migrator identity.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER FUNCTION
  agent_control.cortex_intermediate_output_contract_allowed(TEXT,TEXT,TEXT)
  SECURITY DEFINER;
ALTER FUNCTION
  agent_control.cortex_intermediate_output_contract_allowed(TEXT,TEXT,TEXT)
  SET search_path=pg_catalog,agent_control;
ALTER FUNCTION
  agent_control.cortex_intermediate_output_contract_allowed(TEXT,TEXT,TEXT)
  SET timezone='UTC';

REVOKE ALL ON FUNCTION
  agent_control.cortex_intermediate_output_contract_allowed(TEXT,TEXT,TEXT)
  FROM PUBLIC;
REVOKE ALL ON FUNCTION
  agent_control.cortex_intermediate_output_contract_allowed(TEXT,TEXT,TEXT)
  FROM alpheus_agent_worker;

RESET ROLE;
