#!/bin/sh
set -eu

: "${GEXBOT_PROVIDER_SECRET_DIR:?GEXBOT_PROVIDER_SECRET_DIR is required}"
umask 077
mkdir -p "$GEXBOT_PROVIDER_SECRET_DIR"

for name in gexbot-provider-database-password gexbot-provider-ingest-token gexbot-provider-read-token; do
  target="$GEXBOT_PROVIDER_SECRET_DIR/$name"
  if [ ! -s "$target" ]; then
    tmp="$target.tmp"
    dd if=/dev/urandom bs=32 count=1 2>/dev/null | od -An -tx1 | tr -d ' \n' >"$tmp"
    chmod 600 "$tmp"
    mv "$tmp" "$target"
  fi
  chmod 600 "$target"
done
