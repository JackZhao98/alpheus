#!/bin/sh
# Verifies one protected, digest-bound historical Agent release from immutable
# Git objects. Later-stage files at HEAD are deliberately outside AP0's scope.
# The approved digest is never inferred from the manifest being verified.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
STAGE=${1:-}

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
	ap0)
		# This is the reviewed AP0 seal, not an adjustable input. The seal binds
		# its own manifest/evidence and names the separately reviewed source
		# commit whose documents and verifier implementation are reconstructed.
		SEAL_COMMIT=628b71754bd9223aca51f6b55ab10f7ec0c02fcf
		;;
	*)
		echo "usage: $0 <ap0>" >&2
		exit 2
		;;
esac

MANIFEST_REL="audit/agent/$STAGE/release-manifest.json"
APPROVED_DIGEST_REL="audit/agent/$STAGE/release-manifest.sha256"
AUDIT_REL="audit/agent/$STAGE"

if ! git -C "$ROOT" cat-file -e "$SEAL_COMMIT^{commit}" 2>/dev/null; then
	echo "FAIL stage=$STAGE reason=release-seal-missing" >&2
	exit 1
fi
if ! git -C "$ROOT" merge-base --is-ancestor "$SEAL_COMMIT" HEAD; then
	echo "FAIL stage=$STAGE reason=release-seal-not-ancestor" >&2
	exit 1
fi
if ! git -C "$ROOT" cat-file -e "$SEAL_COMMIT:$MANIFEST_REL" 2>/dev/null ||
	! git -C "$ROOT" cat-file -e "$SEAL_COMMIT:$APPROVED_DIGEST_REL" 2>/dev/null; then
	echo "FAIL stage=$STAGE reason=release-manifest-missing" >&2
	exit 1
fi
# The frozen approval remains byte-for-byte identical at HEAD and in the
# worktree; later-stage changes elsewhere are allowed.
if ! git -C "$ROOT" diff --quiet "$SEAL_COMMIT" -- "$AUDIT_REL"; then
	echo "FAIL stage=$STAGE reason=release-approval-changed-after-seal" >&2
	exit 1
fi

VERIFY_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/alpheus-$STAGE-history.XXXXXX")
# macOS commonly supplies TMPDIR with a trailing slash. Normalize the path
# because the frozen verifier intentionally rejects non-canonical roots.
VERIFY_ROOT=$(CDPATH= cd -- "$VERIFY_ROOT" && pwd -P)
cleanup() {
	rm -rf "$VERIFY_ROOT"
}
trap cleanup EXIT INT TERM

git -C "$ROOT" archive "$SEAL_COMMIT" -- "$AUDIT_REL" |
	LC_ALL=C tar -xf - -C "$VERIFY_ROOT"
MANIFEST="$VERIFY_ROOT/$MANIFEST_REL"
APPROVED_DIGEST="$VERIFY_ROOT/$APPROVED_DIGEST_REL"

source_commit=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1], encoding="utf-8"))["source_commit"])' "$MANIFEST")
if ! git -C "$ROOT" cat-file -e "$source_commit^{commit}" 2>/dev/null ||
	! git -C "$ROOT" merge-base --is-ancestor "$source_commit" "$SEAL_COMMIT"; then
	echo "FAIL stage=$STAGE reason=release-source-not-ancestor" >&2
	exit 1
fi

# Reconstruct the exact verifier and bound documents from the certified source
# commit. Evidence/approval comes from the later seal commit. No current-HEAD
# Agent Platform file participates in this historical result.
git -C "$ROOT" archive "$source_commit" -- agent-platform docs |
	LC_ALL=C tar -xf - -C "$VERIFY_ROOT"

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

GOCACHE=${GOCACHE:-${TMPDIR:-/tmp}/alpheus-agent-go-cache}
export GOCACHE

go -C "$VERIFY_ROOT/agent-platform" run ./cmd/agent-platform verify-release \
	--file "$MANIFEST" --root "$VERIFY_ROOT" \
	--expect-stage AP0 --expect-digest "$digest"
