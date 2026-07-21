#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
directory="$ROOT/.secrets/cortex"
mkdir -p "$directory"
chmod 700 "$ROOT/.secrets" "$directory"

if [ ! -s "$directory/database-password" ]; then
    umask 077
    openssl rand -hex 32 >"$directory/database-password"
fi
if [ ! -s "$directory/input-token" ]; then
    umask 077
    openssl rand -hex 32 >"$directory/input-token"
fi
if [ ! -s "$directory/activator-database-password" ]; then
    umask 077
    openssl rand -hex 32 >"$directory/activator-database-password"
fi
password=$(tr -d '\r\n' <"$directory/database-password")
activator_password=$(tr -d '\r\n' <"$directory/activator-database-password")
umask 077
printf 'postgresql://cortex-control-1:%s@db:5432/alpheus?sslmode=disable\n' "$password" >"$directory/database-url"
printf 'postgresql://cortex-activator-1:%s@db:5432/alpheus?sslmode=disable\n' "$activator_password" >"$directory/activator-database-url"
chmod 600 "$directory/database-password" "$directory/database-url" "$directory/input-token" \
    "$directory/activator-database-password" "$directory/activator-database-url"
printf 'Cortex local secrets are ready in %s\n' "$directory"
