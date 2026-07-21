#!/bin/sh
set -eu
: "${CORTEX_ACTIVATOR_DATABASE_URL_FILE:?CORTEX_ACTIVATOR_DATABASE_URL_FILE is required}"
url=$(tr -d '\r\n' <"$CORTEX_ACTIVATOR_DATABASE_URL_FILE")
psql --no-psqlrc --set ON_ERROR_STOP=1 --dbname="$url" \
    --command='SET ROLE alpheus_agent_activator; SELECT platform_governance.activate_cortex_user_request_policy();'

