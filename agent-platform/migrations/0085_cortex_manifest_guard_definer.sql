-- Deferred constraint triggers execute when the caller's transaction commits,
-- after the enclosing dispatch function's security context may have unwound.
-- The read-only manifest guard therefore needs its table-owning identity.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER FUNCTION agent_control.validate_runtime_manifest_contract()
  SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_runtime_manifest_contract()
  SET search_path=pg_catalog,agent_control;
ALTER FUNCTION agent_control.validate_runtime_manifest_contract()
  SET timezone='UTC';

REVOKE ALL ON FUNCTION
  agent_control.validate_runtime_manifest_contract()
  FROM PUBLIC;

RESET ROLE;
