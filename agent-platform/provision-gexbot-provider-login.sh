#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${GEXBOT_PROVIDER_DATABASE_PASSWORD_FILE:?GEXBOT_PROVIDER_DATABASE_PASSWORD_FILE is required}"

password=$(tr -d '\r\n' <"$GEXBOT_PROVIDER_DATABASE_PASSWORD_FILE")
if ! printf '%s' "$password" | grep -Eq '^[0-9a-f]{64}$'; then
  echo "GEXBOT Provider database password must be 64 lowercase hexadecimal characters" >&2
  exit 1
fi
psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname="$DATABASE_URL" \
  --set=provider_login=gexbot-provider-1 --set=provider_password="$password" \
  --file="${AGENT_PLATFORM_ROOT:-/workspace}/agent-platform/provision-gexbot-provider-login.sql"
if [ -n "${GEXBOT_PROVIDER_DATABASE_URL_FILE:-}" ]; then
  umask 077
  printf 'postgresql://gexbot-provider-1:%s@db:5432/alpheus?sslmode=disable' "$password" >"$GEXBOT_PROVIDER_DATABASE_URL_FILE"
fi
