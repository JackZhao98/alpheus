#!/bin/sh
set -eu
target=/var/run/cortex-worker-secrets
mkdir -p "$target"
chmod 700 "$target"
cp "$CORTEX_WORKER_DATABASE_URL_FILE" "$target/database-url"
cp "$CORTEX_WORKER_CONTROL_TOKEN_FILE" "$target/control-token"
cp "$OPENAI_API_KEY_FILE" "$target/openai-api-key"
chown -R cortex:cortex "$target"
chmod 600 "$target"/*
export CORTEX_WORKER_DATABASE_URL_FILE="$target/database-url"
export CORTEX_WORKER_CONTROL_TOKEN_FILE="$target/control-token"
export OPENAI_API_KEY_FILE="$target/openai-api-key"
exec su-exec cortex:cortex /usr/local/bin/cortex-worker
