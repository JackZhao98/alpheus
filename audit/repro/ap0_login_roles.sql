\set ON_ERROR_STOP on

-- Disposable LOGIN fixtures prove the production identity boundary with a
-- real session_user. SET ROLE alone is insufficient because it leaves the
-- bootstrap account as session_user inside SECURITY DEFINER functions.
DO $$
DECLARE
    login_name TEXT;
BEGIN
    FOREACH login_name IN ARRAY ARRAY[
        'control-1', 'worker-1', 'research-1', 'dispatcher-1', 'repair-1',
        'blob-gc-1', 'blob-diagnostics-1', 'owner-1', 'activator-1', 'halt-1', 'diagnostics-1',
        'unbound-attacker', 'multi-role-attacker', 'migrator-attacker', 'admin-option-attacker'
    ] LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = login_name) THEN
            EXECUTE format('CREATE ROLE %I LOGIN', login_name);
        END IF;
        EXECUTE format(
            'ALTER ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
            login_name
        );
    END LOOP;
END
$$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = 'ap0_test_role_grantor') THEN
        CREATE ROLE ap0_test_role_grantor NOLOGIN;
    END IF;
    ALTER ROLE ap0_test_role_grantor NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
        NOINHERIT NOREPLICATION NOBYPASSRLS;
END
$$;

GRANT alpheus_agent_control_api TO "control-1";
GRANT alpheus_agent_worker TO "worker-1";
GRANT alpheus_research_gateway TO "research-1";
GRANT alpheus_agent_delivery_dispatcher TO "dispatcher-1";
GRANT alpheus_agent_delivery_repair TO "repair-1";
GRANT alpheus_blob_gc TO "blob-gc-1";
GRANT alpheus_blob_diagnostics TO "blob-diagnostics-1";
GRANT alpheus_platform_owner TO "owner-1";
GRANT alpheus_agent_activator TO "activator-1";
GRANT alpheus_platform_halt TO "halt-1";
GRANT alpheus_agent_diagnostics TO "diagnostics-1";
GRANT alpheus_agent_control_api, alpheus_agent_worker TO "multi-role-attacker";
GRANT alpheus_agent_worker, alpheus_agent_migrator TO "migrator-attacker";
GRANT alpheus_agent_worker TO ap0_test_role_grantor WITH ADMIN OPTION;
GRANT alpheus_agent_control_api TO "admin-option-attacker";
GRANT alpheus_agent_worker TO "admin-option-attacker";
SET ROLE ap0_test_role_grantor;
GRANT alpheus_agent_worker TO "admin-option-attacker" WITH ADMIN OPTION;
RESET ROLE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_catalog.pg_roles
        WHERE rolname IN (
            'control-1', 'worker-1', 'research-1', 'dispatcher-1', 'repair-1',
            'blob-gc-1', 'blob-diagnostics-1', 'owner-1', 'activator-1', 'halt-1', 'diagnostics-1',
            'unbound-attacker', 'multi-role-attacker', 'migrator-attacker', 'admin-option-attacker'
        )
          AND (NOT rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR
               rolinherit OR rolreplication OR rolbypassrls)
    ) THEN
        RAISE EXCEPTION 'unsafe AP0 LOGIN fixture';
    END IF;
    IF pg_catalog.has_function_privilege(
        'control-1', 'platform_security.invoker_identity()', 'EXECUTE'
    ) THEN
        RAISE EXCEPTION 'internal invoker helper is directly executable by application LOGIN';
    END IF;
    IF (SELECT count(*)
        FROM pg_catalog.pg_proc AS routine
        WHERE routine.pronamespace = 'pg_catalog'::regnamespace
          AND routine.proname LIKE 'pg%advisory%') <> 21 THEN
        RAISE EXCEPTION 'PostgreSQL advisory-lock function inventory changed';
    END IF;
    IF EXISTS (
        WITH app_login(login_name) AS (
            VALUES
                ('control-1'::NAME), ('worker-1'::NAME), ('research-1'::NAME),
                ('dispatcher-1'::NAME), ('repair-1'::NAME), ('blob-gc-1'::NAME),
                ('blob-diagnostics-1'::NAME), ('owner-1'::NAME), ('activator-1'::NAME),
                ('halt-1'::NAME), ('diagnostics-1'::NAME), ('unbound-attacker'::NAME),
                ('multi-role-attacker'::NAME), ('migrator-attacker'::NAME),
                ('admin-option-attacker'::NAME)
        )
        SELECT 1
        FROM pg_catalog.pg_proc AS routine
        CROSS JOIN app_login
        WHERE routine.pronamespace = 'pg_catalog'::regnamespace
          AND routine.proname LIKE 'pg%advisory%'
          AND pg_catalog.has_function_privilege(
              app_login.login_name, routine.oid, 'EXECUTE'
          )
    ) THEN
        RAISE EXCEPTION 'application LOGIN retained advisory-lock capability';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_catalog.pg_auth_members AS membership
        JOIN pg_catalog.pg_roles AS granted_role ON granted_role.oid = membership.roleid
        JOIN pg_catalog.pg_roles AS member_role ON member_role.oid = membership.member
        WHERE granted_role.rolname = 'alpheus_agent_worker'
          AND member_role.rolname = 'admin-option-attacker'
        GROUP BY membership.roleid, membership.member
        HAVING count(*) >= 2 AND bool_or(membership.admin_option)
           AND bool_or(NOT membership.admin_option)
    ) THEN
        RAISE EXCEPTION 'multi-grantor ADMIN OPTION fixture was not established';
    END IF;
END
$$;

-- Test-only definer wrappers exercise the internal helper without granting
-- direct access to it. They are removed before the probe completes.
SET ROLE alpheus_agent_migrator;

CREATE FUNCTION platform_security.ap0_assert_invoker(
    p_principal_id TEXT,
    p_profile_id TEXT,
    p_group_role NAME,
    p_owner_id TEXT
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform_security
AS $$
DECLARE
    observed RECORD;
BEGIN
    SELECT * INTO STRICT observed
    FROM platform_security.invoker_identity();
    IF observed.principal_id <> p_principal_id
       OR observed.profile_id <> p_profile_id
       OR observed.group_role <> p_group_role
       OR observed.owner_id <> p_owner_id THEN
        RAISE EXCEPTION 'invoker identity mismatch: got %', row_to_json(observed);
    END IF;
END
$$;

CREATE FUNCTION platform_security.ap0_assert_invoker_rejected()
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform_security
AS $$
BEGIN
    BEGIN
        PERFORM * FROM platform_security.invoker_identity();
    EXCEPTION WHEN insufficient_privilege THEN
        RETURN;
    END;
    RAISE EXCEPTION 'ambiguous or missing application group unexpectedly resolved';
END
$$;

REVOKE ALL ON FUNCTION platform_security.ap0_assert_invoker(TEXT, TEXT, NAME, TEXT) FROM PUBLIC;
REVOKE ALL ON FUNCTION platform_security.ap0_assert_invoker_rejected() FROM PUBLIC;

RESET ROLE;

GRANT USAGE ON SCHEMA platform_security TO
    "control-1", "worker-1", "research-1", "dispatcher-1", "repair-1",
    "blob-gc-1", "blob-diagnostics-1", "owner-1", "activator-1", "halt-1", "diagnostics-1",
    "unbound-attacker", "multi-role-attacker", "migrator-attacker", "admin-option-attacker";
GRANT EXECUTE ON FUNCTION platform_security.ap0_assert_invoker(TEXT, TEXT, NAME, TEXT) TO
    "control-1", "worker-1", "research-1", "dispatcher-1", "repair-1",
    "blob-gc-1", "blob-diagnostics-1", "owner-1", "activator-1", "halt-1", "diagnostics-1";
GRANT EXECUTE ON FUNCTION platform_security.ap0_assert_invoker_rejected() TO
    "unbound-attacker", "multi-role-attacker", "migrator-attacker", "admin-option-attacker";

SET SESSION AUTHORIZATION "control-1";
SELECT platform_security.ap0_assert_invoker(
    'control-1', 'control-api', 'alpheus_agent_control_api', 'agent_control'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "worker-1";
SELECT platform_security.ap0_assert_invoker(
    'worker-1', 'worker', 'alpheus_agent_worker', 'worker'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "research-1";
SELECT platform_security.ap0_assert_invoker(
    'research-1', 'research-gateway', 'alpheus_research_gateway', 'research_gateway'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "dispatcher-1";
SELECT platform_security.ap0_assert_invoker(
    'dispatcher-1', 'delivery-dispatcher', 'alpheus_agent_delivery_dispatcher', 'agent_control'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "repair-1";
SELECT platform_security.ap0_assert_invoker(
    'repair-1', 'delivery-repair', 'alpheus_agent_delivery_repair', 'agent_control'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "blob-gc-1";
SELECT platform_security.ap0_assert_invoker(
    'blob-gc-1', 'blob-gc', 'alpheus_blob_gc', 'blob'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "blob-diagnostics-1";
SELECT platform_security.ap0_assert_invoker(
    'blob-diagnostics-1', 'blob-diagnostics', 'alpheus_blob_diagnostics', 'blob'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "owner-1";
SELECT platform_security.ap0_assert_invoker(
    'owner-1', 'platform-owner', 'alpheus_platform_owner', 'platform_governance'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "activator-1";
SELECT platform_security.ap0_assert_invoker(
    'activator-1', 'activator', 'alpheus_agent_activator', 'platform_governance'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "halt-1";
SELECT platform_security.ap0_assert_invoker(
    'halt-1', 'platform-halt', 'alpheus_platform_halt', 'platform_governance'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "diagnostics-1";
SELECT platform_security.ap0_assert_invoker(
    'diagnostics-1', 'diagnostics', 'alpheus_agent_diagnostics', 'agent_control'
);
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "unbound-attacker";
SELECT platform_security.ap0_assert_invoker_rejected();
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "multi-role-attacker";
SELECT platform_security.ap0_assert_invoker_rejected();
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "migrator-attacker";
SELECT platform_security.ap0_assert_invoker_rejected();
RESET SESSION AUTHORIZATION;

SET SESSION AUTHORIZATION "admin-option-attacker";
SELECT platform_security.ap0_assert_invoker_rejected();
RESET SESSION AUTHORIZATION;

REVOKE USAGE ON SCHEMA platform_security FROM
    "control-1", "worker-1", "research-1", "dispatcher-1", "repair-1",
    "blob-gc-1", "blob-diagnostics-1", "owner-1", "activator-1", "halt-1", "diagnostics-1",
    "unbound-attacker", "multi-role-attacker", "migrator-attacker", "admin-option-attacker";

SET ROLE alpheus_agent_migrator;
DROP FUNCTION platform_security.ap0_assert_invoker(TEXT, TEXT, NAME, TEXT);
DROP FUNCTION platform_security.ap0_assert_invoker_rejected();
RESET ROLE;

SELECT 'ap0-login-identity-pass';
