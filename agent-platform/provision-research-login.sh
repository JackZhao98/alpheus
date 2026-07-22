#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${CORTEX_RESEARCH_DATABASE_PASSWORD_FILE:?CORTEX_RESEARCH_DATABASE_PASSWORD_FILE is required}"

password=$(tr -d '\r\n' <"$CORTEX_RESEARCH_DATABASE_PASSWORD_FILE")
if ! printf '%s' "$password" | grep -Eq '^[0-9a-f]{64}$'; then
  echo "Research Gateway database password must be 64 lowercase hexadecimal characters" >&2
  exit 1
fi
psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname="$DATABASE_URL" \
  --set=research_login=research-gateway-1 --set=research_password="$password" \
  --file="${AGENT_PLATFORM_ROOT:-/workspace}/agent-platform/provision-research-login.sql"
if [ -n "${CORTEX_RESEARCH_DATABASE_URL_FILE:-}" ]; then
  umask 077
  printf 'postgresql://research-gateway-1:%s@db:5432/alpheus?sslmode=disable' "$password" >"$CORTEX_RESEARCH_DATABASE_URL_FILE"
fi
