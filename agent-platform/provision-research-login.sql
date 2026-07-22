\set ON_ERROR_STOP on

SELECT format('CREATE ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS', :'research_login', :'research_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=:'research_login')
\gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS', :'research_login', :'research_password')
\gexec
SELECT format('GRANT alpheus_research_gateway TO %I', :'research_login')
\gexec

SELECT EXISTS (
  SELECT 1 FROM pg_catalog.pg_auth_members membership
  JOIN pg_catalog.pg_roles granted ON granted.oid=membership.roleid
  JOIN pg_catalog.pg_roles member ON member.oid=membership.member
  WHERE granted.rolname='alpheus_research_gateway' AND member.rolname=:'research_login' AND NOT membership.admin_option
) AS research_login_provisioned
\gset
\if :research_login_provisioned
\else
  \echo 'Research Gateway LOGIN provisioning failed'
  \quit 1
\endif
