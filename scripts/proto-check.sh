#!/bin/sh
# KTMS owns ktms.v1: proto/ktms/v1 must be its copy at KTMS_REF (default: the
# pair in e2e.refs), whole directory, and internal/gen generated from the copy.
set -eu

cd "$(dirname "$0")/.."
# shellcheck source=/dev/null
. ./scripts/e2e.refs
REF=${KTMS_REF:-$E2E_KTMS_REF}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

git clone -q --depth 1 --branch "$REF" https://github.com/ai-process/ktms.git "$WORK/ktms" ||
	{ echo "proto-check: cannot fetch ktms@$REF" >&2; exit 1; }
diff -ru "$WORK/ktms/proto/ktms/v1" proto/ktms/v1 ||
	{ echo "proto-check: proto/ktms/v1 differs from ktms@$REF: copy the directory over and run make proto" >&2; exit 1; }

# From scratch, so the output of a removed proto file shows up as a deletion.
rm -rf internal/gen
buf generate
if [ -n "$(git status --porcelain -- internal/gen)" ]; then
	git status --short -- internal/gen >&2
	echo "proto-check: internal/gen is stale: run make proto and commit" >&2
	exit 1
fi
echo "proto-check: proto/ktms/v1 matches ktms@$REF and internal/gen is current"
