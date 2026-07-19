-- Group roles have NOLOGIN. Deployment provisions one distinct, non-elevated
-- LOGIN per process profile, names that LOGIN exactly as the profile's
-- principal_id, and grants it exactly one application group role directly,
-- without ADMIN OPTION or migrator membership. Definer
-- functions resolve their invoker from session_user through
-- platform_security.invoker_identity(); caller-supplied actor/owner strings
-- are never an identity root. No application receives migrator.
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
        'alpheus_platform_owner',
        'alpheus_platform_halt',
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

-- Advisory locks share a database-wide, caller-chosen key space and PostgreSQL
-- grants these functions to PUBLIC by default. Agent profiles do not need that
-- capability: leaving it public would let any compromised read-only LOGIN hold
-- a known Kernel lock indefinitely. A future least-privilege Kernel role must
-- receive its own explicit grants during Kernel provisioning.
REVOKE EXECUTE ON FUNCTION
    pg_catalog.pg_advisory_lock(BIGINT),
    pg_catalog.pg_advisory_lock(INTEGER, INTEGER),
    pg_catalog.pg_advisory_lock_shared(BIGINT),
    pg_catalog.pg_advisory_lock_shared(INTEGER, INTEGER),
    pg_catalog.pg_advisory_unlock(BIGINT),
    pg_catalog.pg_advisory_unlock(INTEGER, INTEGER),
    pg_catalog.pg_advisory_unlock_all(),
    pg_catalog.pg_advisory_unlock_shared(BIGINT),
    pg_catalog.pg_advisory_unlock_shared(INTEGER, INTEGER),
    pg_catalog.pg_advisory_xact_lock(BIGINT),
    pg_catalog.pg_advisory_xact_lock(INTEGER, INTEGER),
    pg_catalog.pg_advisory_xact_lock_shared(BIGINT),
    pg_catalog.pg_advisory_xact_lock_shared(INTEGER, INTEGER),
    pg_catalog.pg_try_advisory_lock(BIGINT),
    pg_catalog.pg_try_advisory_lock(INTEGER, INTEGER),
    pg_catalog.pg_try_advisory_lock_shared(BIGINT),
    pg_catalog.pg_try_advisory_lock_shared(INTEGER, INTEGER),
    pg_catalog.pg_try_advisory_xact_lock(BIGINT),
    pg_catalog.pg_try_advisory_xact_lock(INTEGER, INTEGER),
    pg_catalog.pg_try_advisory_xact_lock_shared(BIGINT),
    pg_catalog.pg_try_advisory_xact_lock_shared(INTEGER, INTEGER)
FROM PUBLIC;

CREATE SCHEMA IF NOT EXISTS platform_security AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA platform_security FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS agent_control AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA agent_control FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS blob AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA blob FROM PUBLIC;
CREATE SCHEMA IF NOT EXISTS platform_governance AUTHORIZATION alpheus_agent_migrator;
REVOKE ALL ON SCHEMA platform_governance FROM PUBLIC;

SET ROLE alpheus_agent_migrator;

-- invoker_identity is the single AP0 database identity root. SECURITY DEFINER
-- changes current_user to the function owner, so only session_user identifies
-- the authenticated LOGIN. The fixed VALUES list is code-owned authority, not
-- a caller-writable mapping table. A LOGIN with no direct non-admin known group,
-- with multiple application groups, or with migrator membership fails closed.
CREATE OR REPLACE FUNCTION platform_security.invoker_identity()
RETURNS TABLE (
    principal_id TEXT,
    profile_id TEXT,
    group_role NAME,
    owner_id TEXT
)
LANGUAGE plpgsql
STABLE
SECURITY DEFINER
SET search_path = pg_catalog, platform_security
AS $$
DECLARE
    login_is_safe BOOLEAN := false;
    membership_count INTEGER := 0;
    resolved_profile TEXT;
    resolved_group NAME;
    resolved_owner TEXT;
    has_admin_membership BOOLEAN := false;
BEGIN
    SELECT role.rolcanlogin
           AND NOT role.rolsuper
           AND NOT role.rolcreatedb
           AND NOT role.rolcreaterole
           AND NOT role.rolreplication
           AND NOT role.rolbypassrls
    INTO login_is_safe
    FROM pg_catalog.pg_roles AS role
    WHERE role.rolname = session_user;

    IF NOT coalesce(login_is_safe, false)
       OR session_user::TEXT = ''
       OR session_user::TEXT ~ '[[:space:][:cntrl:]]'
       OR pg_catalog.pg_has_role(session_user, 'alpheus_agent_migrator', 'MEMBER') THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'invalid application login identity';
    END IF;

    WITH known_group(group_role, profile_id, owner_id) AS (
        VALUES
            ('alpheus_agent_control_api'::NAME, 'control-api'::TEXT, 'agent_control'::TEXT),
            ('alpheus_agent_worker'::NAME, 'worker'::TEXT, 'worker'::TEXT),
            ('alpheus_agent_delivery_dispatcher'::NAME, 'delivery-dispatcher'::TEXT, 'agent_control'::TEXT),
            ('alpheus_agent_delivery_repair'::NAME, 'delivery-repair'::TEXT, 'agent_control'::TEXT),
            ('alpheus_agent_validator'::NAME, 'validator'::TEXT, 'agent_control'::TEXT),
            ('alpheus_agent_activator'::NAME, 'activator'::TEXT, 'platform_governance'::TEXT),
            ('alpheus_platform_owner'::NAME, 'platform-owner'::TEXT, 'platform_governance'::TEXT),
            ('alpheus_platform_halt'::NAME, 'platform-halt'::TEXT, 'platform_governance'::TEXT),
            ('alpheus_research_gateway'::NAME, 'research-gateway'::TEXT, 'research_gateway'::TEXT),
            ('alpheus_grace_intake'::NAME, 'grace-intake'::TEXT, 'grace'::TEXT),
            ('alpheus_grace_engine'::NAME, 'grace-engine'::TEXT, 'grace'::TEXT),
            ('alpheus_delegation_engine'::NAME, 'delegation-engine'::TEXT, 'delegation'::TEXT),
            ('alpheus_agent_web'::NAME, 'web'::TEXT, 'agent_control'::TEXT),
            ('alpheus_agent_diagnostics'::NAME, 'diagnostics'::TEXT, 'agent_control'::TEXT),
            ('alpheus_blob_gc'::NAME, 'blob-gc'::TEXT, 'blob'::TEXT),
            ('alpheus_blob_diagnostics'::NAME, 'blob-diagnostics'::TEXT, 'blob'::TEXT)
    ), memberships AS (
        SELECT candidate.group_role, candidate.profile_id, candidate.owner_id,
               pg_catalog.bool_or(membership.admin_option) AS has_admin_option
        FROM known_group AS candidate
        JOIN pg_catalog.pg_roles AS login_role
          ON login_role.rolname = session_user
        JOIN pg_catalog.pg_roles AS granted_role
          ON granted_role.rolname = candidate.group_role
        JOIN pg_catalog.pg_auth_members AS membership
          ON membership.member = login_role.oid
         AND membership.roleid = granted_role.oid
        GROUP BY candidate.group_role, candidate.profile_id, candidate.owner_id
    )
    SELECT count(*)::INTEGER,
           max(memberships.profile_id),
           max(memberships.group_role::TEXT)::NAME,
           max(memberships.owner_id),
           coalesce(pg_catalog.bool_or(memberships.has_admin_option), false)
    INTO membership_count, resolved_profile, resolved_group, resolved_owner, has_admin_membership
    FROM memberships;

    IF membership_count <> 1 OR has_admin_membership THEN
        RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'application login must belong to exactly one application group';
    END IF;

    principal_id := session_user::TEXT;
    profile_id := resolved_profile;
    group_role := resolved_group;
    owner_id := resolved_owner;
    RETURN NEXT;
END
$$;

REVOKE ALL ON FUNCTION platform_security.invoker_identity() FROM PUBLIC;

RESET ROLE;

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
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA platform_governance
    REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA platform_governance
    REVOKE ALL ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA platform_governance
    REVOKE ALL ON SEQUENCES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA platform_security
    REVOKE ALL ON TABLES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA platform_security
    REVOKE ALL ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE alpheus_agent_migrator IN SCHEMA platform_security
    REVOKE ALL ON SEQUENCES FROM PUBLIC;
