SET ROLE alpheus_agent_migrator;

-- These validation triggers are deferred until transaction commit. A narrow
-- SECURITY DEFINER admission function has already returned by then, so the
-- trigger otherwise inherits the application LOGIN and cannot read the
-- protected definition/state tables. Keep the checks owner-executed with a
-- pinned search path; callers still receive no table privilege.
ALTER FUNCTION agent_control.validate_runtime_run_binding() SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_runtime_run_binding()
    SET search_path = pg_catalog, agent_control, platform_governance;
ALTER FUNCTION agent_control.validate_runtime_budget_structure() SECURITY DEFINER;
ALTER FUNCTION agent_control.validate_runtime_budget_structure()
    SET search_path = pg_catalog, agent_control;

REVOKE ALL ON FUNCTION agent_control.validate_runtime_run_binding() FROM PUBLIC;
REVOKE ALL ON FUNCTION agent_control.validate_runtime_budget_structure() FROM PUBLIC;

RESET ROLE;
