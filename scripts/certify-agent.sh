#!/bin/sh
# Permanently non-money Agent Platform certification. This command never loads
# production credentials and never invokes Kernel or Provider mutation paths.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STAGE=${1:-}
SEED=ap0-contract-v1
ARTIFACT_DIR=${AGENT_CERT_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-certification/$STAGE-$SEED}
GOCACHE=${GOCACHE:-${TMPDIR:-/tmp}/alpheus-agent-go-cache}
export GOCACHE

case "$STAGE" in
	ap0) ;;
	ap1|ap2|ap3|ap4|ap5|ap6|ap7|ap8|ap9|ap10|ap11|ap12|ap13|ap14|ap15|all)
		echo "FAIL stage=$STAGE reason=mandatory-probes-not-implemented" >&2
		exit 1
		;;
	*)
		echo "usage: $0 <ap0|ap1|...|ap15|all>" >&2
		exit 2
		;;
esac

cd "$ROOT"
dirty=$(git status --porcelain --untracked-files=all)
mkdir -p "$ARTIFACT_DIR"

fail() {
	reason=$1
	label=$2
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"%s"}\n' \
		"$STAGE" "$SEED" "$reason" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="%s"><failure>%s</failure></testcase></testsuite>\n' \
		"$STAGE" "$label" "$reason" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=$reason" >&2
	exit 1
}

if [ -n "$dirty" ]; then
	printf '%s\n' "$dirty" >"$ARTIFACT_DIR/dirty-worktree.txt"
	fail clean-worktree clean-worktree
fi
printf '{"status":"PASS","probe":"clean-worktree"}\n' >"$ARTIFACT_DIR/clean-worktree.json"

find agent-platform agent-runtime kernel -type f -name '*.go' -exec gofmt -l {} + >"$ARTIFACT_DIR/gofmt.txt"
if [ -s "$ARTIFACT_DIR/gofmt.txt" ]; then
	fail gofmt gofmt
fi

if ! go -C agent-platform vet ./... >"$ARTIFACT_DIR/go-vet-agent-platform.txt" 2>&1 ||
	! go -C agent-runtime vet ./... >"$ARTIFACT_DIR/go-vet-agent-runtime.txt" 2>&1 ||
	! go -C kernel vet ./... >"$ARTIFACT_DIR/go-vet-kernel.txt" 2>&1; then
	fail go-vet go-vet
fi

if ! go -C agent-platform test -json ./contractvalidate >"$ARTIFACT_DIR/contract-pack.json" 2>&1 ||
	! "$ROOT/scripts/validate-agent-contracts.sh" >"$ARTIFACT_DIR/contract-schema.json" 2>&1; then
	fail contract-schema contract-schema
fi

if ! "$ROOT/scripts/check-agent-secret-leaks.sh" >"$ARTIFACT_DIR/secret-leaks.txt" 2>&1; then
	fail secret-leaks secret-leaks
fi

if ! AGENT_DB_PROBE_ARTIFACT_DIR="$ARTIFACT_DIR/db-role-probe" \
	"$ROOT/scripts/test-agent-db-roles.sh" >"$ARTIFACT_DIR/db-role-probe.txt" 2>&1; then
	fail db-delivery db-delivery
fi

if ! AGENT_BLOB_PROBE_ARTIFACT_DIR="$ARTIFACT_DIR/blob-probe" \
	"$ROOT/scripts/test-agent-blob.sh" >"$ARTIFACT_DIR/blob-probe.txt" 2>&1; then
	fail blob-store blob-store
fi

if ! AGENT_GOVERNANCE_PROBE_ARTIFACT_DIR="$ARTIFACT_DIR/governance-probe" \
	"$ROOT/scripts/test-agent-governance.sh" >"$ARTIFACT_DIR/governance-probe.txt" 2>&1; then
	fail governance governance
fi

if ! AGENT_MIGRATION_PROBE_ARTIFACT_DIR="$ARTIFACT_DIR/migration-probe" \
	"$ROOT/scripts/test-agent-migrations.sh" >"$ARTIFACT_DIR/migration-probe.txt" 2>&1; then
	fail migration-compatibility migration-compatibility
fi

if ! go -C agent-platform test -race -json ./... >"$ARTIFACT_DIR/go-test-agent-platform.json" 2>&1 ||
	! go -C agent-runtime test -race -json ./... >"$ARTIFACT_DIR/go-test-agent-runtime.json" 2>&1 ||
	! go -C kernel test -race -json ./... >"$ARTIFACT_DIR/go-test-kernel.json" 2>&1; then
	fail go-test-race go-test-race
fi

if ! docker compose --env-file .env.example config --quiet >"$ARTIFACT_DIR/compose-config.txt" 2>&1; then
	fail compose-config compose-config
fi

if ! "$ROOT/scripts/check-agent-nonmoney-boundary.sh" >"$ARTIFACT_DIR/nonmoney-boundary.json" 2>&1; then
	fail nonmoney-boundary nonmoney-boundary
fi

if ! "$ROOT/scripts/verify-agent-release.sh" ap0 >"$ARTIFACT_DIR/release-verification.json" 2>&1; then
	fail release-verification release-verification
fi

printf '{"stage":"%s","status":"PASS","seed":"%s","effect_ceiling":"none","completed_checks":["blob_store","clean_worktree","compose_config","contract_schema","db_delivery","go_test_race","go_vet","gofmt","governance","migration_compatibility","nonmoney_boundary","secret_leaks","release_verification"]}\n' \
	"$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="%s" tests="13" failures="0"><testcase name="blob-store"/><testcase name="clean-worktree"/><testcase name="compose-config"/><testcase name="contract-schema"/><testcase name="db-delivery"/><testcase name="go-test-race"/><testcase name="go-vet"/><testcase name="gofmt"/><testcase name="governance"/><testcase name="migration-compatibility"/><testcase name="nonmoney-boundary"/><testcase name="secret-leaks"/><testcase name="release-verification"/></testsuite>\n' \
	"$STAGE" >"$ARTIFACT_DIR/junit.xml"
echo "PASS stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR effect_ceiling=none"
