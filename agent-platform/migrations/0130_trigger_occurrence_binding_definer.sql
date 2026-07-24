-- The deferred TriggerOccurrence binding guard reads the separately owned
-- governance tables at transaction commit. It must run as its narrow
-- migrator-owned definer; otherwise the default-deny Control API role can
-- create the row inside an authorized command but cannot commit it.
SET ROLE alpheus_agent_migrator;
SET TIME ZONE 'UTC';

ALTER FUNCTION agent_control.validate_trigger_occurrence_binding()
  SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_trigger_occurrence_binding()
  SET search_path=pg_catalog,agent_control,platform_governance;

REVOKE ALL ON FUNCTION
agent_control.validate_trigger_occurrence_binding()
FROM PUBLIC;

RESET ROLE;
