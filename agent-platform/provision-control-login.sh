#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${CORTEX_DATABASE_PASSWORD_FILE:?CORTEX_DATABASE_PASSWORD_FILE is required}"

password=$(tr -d '\r\n' <"$CORTEX_DATABASE_PASSWORD_FILE")
if ! printf '%s' "$password" | grep -Eq '^[0-9a-f]{64}$'; then
    echo "Cortex database password must be exactly 64 lowercase hexadecimal characters" >&2
    exit 1
fi

psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname="$DATABASE_URL" \
    --set=cortex_login=cortex-control-1 --set=cortex_password="$password" \
    --file="${AGENT_PLATFORM_ROOT:-/workspace}/agent-platform/provision-control-login.sql"
