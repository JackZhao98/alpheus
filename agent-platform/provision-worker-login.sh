#!/bin/sh
set -eu
: "${DATABASE_URL:?DATABASE_URL is required}"
: "${CORTEX_WORKER_DATABASE_PASSWORD_FILE:?CORTEX_WORKER_DATABASE_PASSWORD_FILE is required}"
password=$(tr -d '\r\n' <"$CORTEX_WORKER_DATABASE_PASSWORD_FILE")
if ! printf '%s' "$password" | grep -Eq '^[0-9a-f]{64}$'; then
  echo "Cortex Worker database password must be 64 lowercase hexadecimal characters" >&2; exit 1
fi
psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname="$DATABASE_URL" \
  --set=cortex_login=cortex-worker-1 --set=cortex_password="$password" \
  --file="${AGENT_PLATFORM_ROOT:-/workspace}/agent-platform/provision-worker-login.sql"
if [ -n "${CORTEX_WORKER_DATABASE_URL_FILE:-}" ]; then
  umask 077
  printf 'postgresql://cortex-worker-1:%s@db:5432/alpheus?sslmode=disable' "$password" >"$CORTEX_WORKER_DATABASE_URL_FILE"
fi
