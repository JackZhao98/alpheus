-- The Provider's startup identity probe is intentionally explicit. It needs
-- only the narrow identity function, not broad access to platform security.
SET ROLE alpheus_agent_migrator;
GRANT USAGE ON SCHEMA platform_security TO alpheus_gexbot_provider;
GRANT EXECUTE ON FUNCTION platform_security.gexbot_provider_identity() TO alpheus_gexbot_provider;
RESET ROLE;
