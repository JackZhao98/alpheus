#!/bin/sh
# Non-money Agent Platform certification entrypoint. This command never loads
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

mkdir -p "$ARTIFACT_DIR"
cd "$ROOT"

find agent-platform -type f -name '*.go' -exec gofmt -l {} + >"$ARTIFACT_DIR/gofmt.txt"
if [ -s "$ARTIFACT_DIR/gofmt.txt" ]; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"gofmt"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="gofmt"><failure>dirty formatting</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=gofmt" >&2
	exit 1
fi

if ! go -C agent-platform vet ./... >"$ARTIFACT_DIR/go-vet.txt" 2>&1; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"go-vet"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="go-vet"><failure>go vet failed</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=go-vet" >&2
	exit 1
fi

if ! go -C agent-platform test -json ./contractvalidate >"$ARTIFACT_DIR/contract-pack.json" 2>&1; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"contract-pack"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="contract-pack"><failure>Schema Freeze Pack validation failed</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=contract-pack" >&2
	exit 1
fi

if ! "$ROOT/scripts/check-agent-secret-leaks.sh" >"$ARTIFACT_DIR/secret-leaks.txt" 2>&1; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"secret-leaks"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="secret-leaks"><failure>secret leak probe failed</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=secret-leaks" >&2
	exit 1
fi

if ! AGENT_DB_PROBE_ARTIFACT_DIR="$ARTIFACT_DIR/db-role-probe" \
	"$ROOT/scripts/test-agent-db-roles.sh" >"$ARTIFACT_DIR/db-role-probe.txt" 2>&1; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"db-role-probe"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="db-role-probe"><failure>database role/delivery probe failed</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=db-role-probe" >&2
	exit 1
fi

if ! AGENT_BLOB_PROBE_ARTIFACT_DIR="$ARTIFACT_DIR/blob-probe" \
	"$ROOT/scripts/test-agent-blob.sh" >"$ARTIFACT_DIR/blob-probe.txt" 2>&1; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"blob-probe"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="blob-probe"><failure>Blob contract/storage probe failed</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=blob-probe" >&2
	exit 1
fi

if ! AGENT_GOVERNANCE_PROBE_ARTIFACT_DIR="$ARTIFACT_DIR/governance-probe" \
	"$ROOT/scripts/test-agent-governance.sh" >"$ARTIFACT_DIR/governance-probe.txt" 2>&1; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"governance-probe"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="governance-probe"><failure>Governance contract/role/CAS probe failed</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=governance-probe" >&2
	exit 1
fi

if ! go -C agent-platform test -race -json ./... >"$ARTIFACT_DIR/go-test.json" 2>&1; then
	printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"go-test-race"}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
	printf '<testsuite name="%s" tests="1" failures="1"><testcase name="go-test-race"><failure>tests failed</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
	echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=go-test-race" >&2
	exit 1
fi

printf '{"stage":"%s","status":"FAIL","seed":"%s","reason":"mandatory-ap0-probes-not-implemented","completed_checks":["gofmt","go-vet","contract-pack","secret-leaks","db-role-probe","blob-probe","governance-probe","go-test-race"]}\n' "$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="%s" tests="9" failures="1"><testcase name="gofmt"/><testcase name="go-vet"/><testcase name="contract-pack"/><testcase name="secret-leaks"/><testcase name="db-role-probe"/><testcase name="blob-probe"/><testcase name="governance-probe"/><testcase name="go-test-race"/><testcase name="ap0-mandatory-probes"><failure>AP0 remains incomplete</failure></testcase></testsuite>\n' "$STAGE" >"$ARTIFACT_DIR/junit.xml"
echo "FAIL stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR reason=mandatory-ap0-probes-not-implemented" >&2
exit 1
