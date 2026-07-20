#!/bin/sh
# Verifies a permanently non-money, historical Agent Platform certification.
# Current-head gates belong to the stage currently being built; they must not
# silently redefine an already sealed historical stage.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STAGE=${1:-}
SEED=ap0-contract-v1
ARTIFACT_DIR=${AGENT_CERT_ARTIFACT_DIR:-${TMPDIR:-/tmp}/alpheus-agent-certification/$STAGE-$SEED}
GOCACHE=${GOCACHE:-${TMPDIR:-/tmp}/alpheus-agent-go-cache}
export GOCACHE

for wrapper in scripts/certify-agent.sh scripts/verify-agent-release.sh; do
	if ! git -C "$ROOT" ls-files --error-unmatch "$wrapper" >/dev/null 2>&1; then
		echo "FAIL stage=${STAGE:-unknown} reason=historical-verifier-not-protected" >&2
		exit 1
	fi
done
if ! git -C "$ROOT" diff --quiet -- scripts/certify-agent.sh scripts/verify-agent-release.sh ||
	! git -C "$ROOT" diff --cached --quiet -- scripts/certify-agent.sh scripts/verify-agent-release.sh; then
	echo "FAIL stage=${STAGE:-unknown} reason=historical-verifier-dirty" >&2
	exit 1
fi

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

if ! "$ROOT/scripts/verify-agent-release.sh" ap0 >"$ARTIFACT_DIR/release-verification.json" 2>&1; then
	fail release-verification release-verification
fi

printf '{"stage":"%s","status":"PASS","seed":"%s","verification_mode":"historical","effect_ceiling":"none","completed_checks":["release_verification"]}\n' \
	"$STAGE" "$SEED" >"$ARTIFACT_DIR/summary.json"
printf '<testsuite name="%s-historical" tests="1" failures="0"><testcase name="release-verification"/></testsuite>\n' \
	"$STAGE" >"$ARTIFACT_DIR/junit.xml"
echo "PASS stage=$STAGE seed=$SEED artifacts=$ARTIFACT_DIR verification_mode=historical effect_ceiling=none"
