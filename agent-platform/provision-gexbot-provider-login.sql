\set ON_ERROR_STOP on

SELECT format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS', :'provider_login', :'provider_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=:'provider_login')
\gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS', :'provider_login', :'provider_password')
\gexec
SELECT format('GRANT alpheus_gexbot_provider TO %I', :'provider_login')
\gexec

SELECT EXISTS (
  SELECT 1 FROM pg_catalog.pg_auth_members membership
  JOIN pg_catalog.pg_roles granted ON granted.oid=membership.roleid
  JOIN pg_catalog.pg_roles member ON member.oid=membership.member
  WHERE granted.rolname='alpheus_gexbot_provider' AND member.rolname=:'provider_login' AND NOT membership.admin_option
) AS provider_login_provisioned
\gset
\if :provider_login_provisioned
\else
  \echo 'GEXBOT Provider LOGIN provisioning failed'
  \quit 1
\endif
