#!/bin/sh
set -eu

target=/var/run/cortex-secrets
mkdir -p "$CORTEX_BLOB_ROOT"
chown -R cortex:cortex "$CORTEX_BLOB_ROOT"
chmod 700 "$CORTEX_BLOB_ROOT"
mkdir -p "$target"
chmod 700 "$target"
cp "$CORTEX_DATABASE_URL_FILE" "$target/database-url"
cp "$CORTEX_INPUT_TOKEN_FILE" "$target/input-token"
cp "$CORTEX_WORKER_CONTROL_TOKEN_FILE" "$target/worker-control-token"
chown -R cortex:cortex "$target"
chmod 600 "$target/database-url" "$target/input-token" "$target/worker-control-token"
export CORTEX_DATABASE_URL_FILE="$target/database-url"
export CORTEX_INPUT_TOKEN_FILE="$target/input-token"
export CORTEX_WORKER_CONTROL_TOKEN_FILE="$target/worker-control-token"
exec su-exec cortex:cortex /usr/local/bin/cortex-input
