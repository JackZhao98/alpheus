#!/bin/sh
# Verifies one protected, digest-bound Agent release against the exact trusted
# checkout files and stable check evidence. It never infers a digest from the
# manifest being verified.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STAGE=${1:-}

case "$STAGE" in
	ap0) ;;
	*)
		echo "usage: $0 <ap0>" >&2
		exit 2
		;;
esac

MANIFEST="$ROOT/audit/agent/$STAGE/release-manifest.json"
APPROVED_DIGEST="$ROOT/audit/agent/$STAGE/release-manifest.sha256"
if [ ! -f "$MANIFEST" ] || [ ! -f "$APPROVED_DIGEST" ]; then
	echo "FAIL stage=$STAGE reason=release-manifest-missing" >&2
	exit 1
fi
if ! git -C "$ROOT" ls-files --error-unmatch \
	"audit/agent/$STAGE/release-manifest.json" \
	"audit/agent/$STAGE/release-manifest.sha256" >/dev/null 2>&1; then
	echo "FAIL stage=$STAGE reason=release-approval-not-protected" >&2
	exit 1
fi

digest=$(tr -d '[:space:]' <"$APPROVED_DIGEST")
case "$digest" in
	*[!0-9a-f]*|'')
		echo "FAIL stage=$STAGE reason=invalid-approved-digest" >&2
		exit 1
		;;
esac
if [ "${#digest}" -ne 64 ]; then
	echo "FAIL stage=$STAGE reason=invalid-approved-digest" >&2
	exit 1
fi

source_commit=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["source_commit"])' "$MANIFEST")
if ! git -C "$ROOT" cat-file -e "$source_commit^{commit}" 2>/dev/null ||
	! git -C "$ROOT" merge-base --is-ancestor "$source_commit" HEAD; then
	echo "FAIL stage=$STAGE reason=release-source-not-ancestor" >&2
	exit 1
fi
if ! git -C "$ROOT" diff --quiet "$source_commit"..HEAD -- \
	agent-platform contracts scripts audit/repro; then
	echo "FAIL stage=$STAGE reason=release-code-changed-after-source" >&2
	exit 1
fi

exec go -C "$ROOT/agent-platform" run ./cmd/agent-platform verify-release \
	--file "$MANIFEST" --root "$ROOT" \
	--expect-stage AP0 --expect-digest "$digest"
