-- Group roles have NOLOGIN. Deployment provisions distinct LOGIN identities
-- and grants each exactly one group role; no application receives migrator.
DO $$
DECLARE
    role_name text;
BEGIN
    FOREACH role_name IN ARRAY ARRAY[
        'alpheus_agent_migrator',
        'alpheus_agent_control_api',
        'alpheus_agent_worker',
        'alpheus_agent_delivery_dispatcher',
        'alpheus_agent_delivery_repair',
        'alpheus_agent_validator',
        'alpheus_agent_activator',
        'alpheus_research_gateway',
        'alpheus_grace_intake',
        'alpheus_grace_engine',
        'alpheus_delegation_engine',
        'alpheus_agent_web',
        'alpheus_agent_diagnostics',
        'alpheus_blob_gc',
        'alpheus_blob_diagnostics'
    ] LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = role_name) THEN
            EXECUTE format('CREATE ROLE %I NOLOGIN', role_name);
        END IF;
        EXECUTE format(
            'ALTER ROLE %I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
            role_name
        );
    END LOOP;
END
$$;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS agent_control AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA agent_control FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS blob AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA blob FROM PUBLIC;
GRANT USAGE ON SCHEMA agent_control TO
    alpheus_agent_control_api,
    alpheus_agent_delivery_dispatcher,
    alpheus_agent_delivery_repair,
    alpheus_agent_diagnostics;

ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA agent_control
    REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA agent_control
    REVOKE ALL ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA agent_control
    REVOKE ALL ON SEQUENCES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA blob
    REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA blob
    REVOKE ALL ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA blob
    REVOKE ALL ON SEQUENCES FROM PUBLIC;
