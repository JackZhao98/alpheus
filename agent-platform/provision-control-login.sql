\set ON_ERROR_STOP on

SELECT format(
    'CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
    :'cortex_login', :'cortex_password'
)
WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname = :'cortex_login')
\gexec

SELECT format(
    'ALTER ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS',
    :'cortex_login', :'cortex_password'
)
\gexec

SELECT format('GRANT alpheus_agent_control_api TO %I', :'cortex_login')
\gexec

SELECT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_auth_members AS membership
    JOIN pg_catalog.pg_roles AS granted ON granted.oid = membership.roleid
    JOIN pg_catalog.pg_roles AS member ON member.oid = membership.member
    WHERE granted.rolname = 'alpheus_agent_control_api'
      AND member.rolname = :'cortex_login'
      AND NOT membership.admin_option
) AS cortex_login_provisioned
\gset
\if :cortex_login_provisioned
\else
    \echo 'Cortex Control LOGIN provisioning failed'
    \quit 1
\endif
