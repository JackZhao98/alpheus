#!/bin/sh
# Static AP0 guard: certification and Agent Platform code cannot acquire or
# invoke production brokerage/Live mutation paths.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

if [ -e scripts/run-agent-live-canary.sh ]; then
	echo "FAIL probe=agent-nonmoney-boundary reason=live-canary-runner-present" >&2
	exit 1
fi

if rg --files-with-matches \
	'(^|[^A-Za-z0-9_])(docker-compose\.robinhood|LIVE_TRADING_ENABLED|TRADING_MODE=live|/operations|rh (order|cancel)|curl .*(operations|orders))([^A-Za-z0-9_]|$)' \
	scripts/certify-agent.sh scripts/test-agent-*.sh scripts/validate-agent-contracts.sh \
	>/dev/null 2>&1; then
	echo "FAIL probe=agent-nonmoney-boundary reason=certification-mutation-path" >&2
	exit 1
fi

if rg --files-with-matches '"net/http"|alpheus/kernel|internal/(broker|rhmcp)|robinhood' \
	agent-platform --glob '*.go' >/dev/null 2>&1; then
	echo "FAIL probe=agent-nonmoney-boundary reason=agent-platform-provider-link" >&2
	exit 1
fi

if rg '^require[[:space:](]' agent-platform/go.mod >/dev/null 2>&1; then
	echo "FAIL probe=agent-nonmoney-boundary reason=unexpected-ap0-runtime-dependency" >&2
	exit 1
fi

echo '{"status":"PASS","probe":"agent-nonmoney-boundary","effect_ceiling":"none","provider_links":0,"live_runners":0}'
