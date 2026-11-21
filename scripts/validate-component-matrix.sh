#!/usr/bin/env bash
# Verify a component version belongs in the system release being assembled.
#
# A system release pins each component at a released version tag. This guard
# ensures the pinned version's source tree matches the component's tree in the
# system release commit (HEAD). It prevents pinning a version whose code is not
# actually part of the release (for example, a tag that was later reverted).
#
# The check compares TREE CONTENT, not commit lineage. The repo squash-merges
# release PRs, so a component tag's commit is NOT an ancestor of main after the
# squash. An ancestry check (git merge-base --is-ancestor) would wrongly reject
# legitimate releases; a path-scoped tree diff is correct under squash.
#
# Usage: validate-component-matrix.sh <component-path> <version>
#
# Example:
#   scripts/validate-component-matrix.sh services/data-ingestion 1.2.3
#
# Exits non-zero (with a ::error:: annotation) if the tag is missing or the
# component tree at the tag differs from the tree at HEAD.

set -euo pipefail

COMPONENT_PATH="${1:?component-path required}"
VERSION="${2:?version required}"

TAG="${COMPONENT_PATH}/v${VERSION}"

if ! git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
  echo "::error::Component tag '${TAG}' does not exist; cannot pin ${COMPONENT_PATH} at ${VERSION}" >&2
  exit 1
fi

# Compare the component's subtree at the tag against the same subtree at HEAD.
# --quiet makes git diff exit 1 when they differ. Scope strictly to the
# component path so unrelated changes elsewhere in the system release do not
# trip the guard.
if ! git diff --quiet "${TAG}" HEAD -- "${COMPONENT_PATH}"; then
  echo "::error::Source tree for '${COMPONENT_PATH}' at tag '${TAG}' differs from the system release tree (HEAD). The pinned version does not match the code being released." >&2
  exit 1
fi

echo "matrix: ${COMPONENT_PATH} v${VERSION} tree matches release (${TAG})"
