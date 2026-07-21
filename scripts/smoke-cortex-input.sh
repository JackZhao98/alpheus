#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
endpoint=${CORTEX_INPUT_URL:-http://127.0.0.1:8400}
token_file=${CORTEX_INPUT_TOKEN_FILE:-$ROOT/.secrets/cortex/input-token}
token=$(tr -d '\r\n' <"$token_file")
conversation_id="cortex-smoke-$(uuidgen | tr '[:upper:]' '[:lower:]')"
request_id="cortex-smoke-$(uuidgen | tr '[:upper:]' '[:lower:]')"
idempotency_key="cortex-smoke-$(uuidgen | tr '[:upper:]' '[:lower:]')"
causation_id="cortex-smoke-$(uuidgen | tr '[:upper:]' '[:lower:]')"
correlation_id="cortex-smoke-$(uuidgen | tr '[:upper:]' '[:lower:]')"
created_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
deadline=$(date -u -v+5M '+%Y-%m-%dT%H:%M:%SZ')
first=$(mktemp "${TMPDIR:-/tmp}/cortex-smoke-first.XXXXXX")
second=$(mktemp "${TMPDIR:-/tmp}/cortex-smoke-second.XXXXXX")
body=$(mktemp "${TMPDIR:-/tmp}/cortex-smoke-body.XXXXXX")
cleanup() { rm -f "$first" "$second" "$body"; }
trap cleanup EXIT INT TERM

printf '%s\n' "{\"conversation_id\":\"$conversation_id\",\"conversation_created_at\":\"$created_at\",\"request_id\":\"$request_id\",\"kind\":\"new_request\",\"text\":\"Cortex immutable input smoke\",\"idempotency_key\":\"$idempotency_key\",\"causation_id\":\"$causation_id\",\"correlation_id\":\"$correlation_id\",\"deadline\":\"$deadline\"}" >"$body"

curl --fail-with-body --silent --show-error \
    --header "Authorization: Bearer $token" --header 'Content-Type: application/json' \
    --data-binary "@$body" "$endpoint/v1/user-requests" >"$first"
curl --fail-with-body --silent --show-error \
    --header "Authorization: Bearer $token" --header 'Content-Type: application/json' \
    --data-binary "@$body" "$endpoint/v1/user-requests" >"$second"

if ! cmp -s "$first" "$second" || ! grep -q '"status":"accepted"' "$first"; then
    echo "Cortex input replay smoke failed" >&2
    exit 1
fi
cat "$first"
