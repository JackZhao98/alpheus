#!/bin/sh
# High-confidence secret scan for Agent Platform source, contracts and probes.
# It never prints matching secret text.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

targets="agent-platform contracts audit/repro audit/agent docs/agent-plan \
scripts/certify-agent.sh scripts/test-agent-db-roles.sh scripts/test-agent-blob.sh \
scripts/test-agent-governance.sh scripts/test-agent-migrations.sh \
scripts/test-agent-runtime-definitions.sh \
scripts/validate-agent-contracts.sh scripts/validate_agent_contracts.py \
scripts/check-agent-nonmoney-boundary.sh scripts/check-agent-secret-leaks.sh"
pattern='(sk-ant-[A-Za-z0-9_-]{20,}|sk-proj-[A-Za-z0-9_-]{20,}|github_pat_[A-Za-z0-9_]{20,}|ghp_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----|postgres(ql)?://[^:/[:space:]]+:[^@[:space:]]+@)'

if rg --files-with-matches --ignore-case "$pattern" $targets >/dev/null 2>&1; then
	echo "FAIL probe=agent-secret-leaks reason=high-confidence-secret-pattern" >&2
	exit 1
fi

if find agent-platform contracts audit/repro \
	-type f \( -name '*.pem' -o -name '*.key' -o -name '*.p12' -o -name '.env' \) \
	-print -quit | grep -q .; then
	echo "FAIL probe=agent-secret-leaks reason=secret-file-type" >&2
	exit 1
fi

echo "PASS probe=agent-secret-leaks"
