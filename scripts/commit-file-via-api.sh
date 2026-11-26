#!/usr/bin/env bash
# Commit a single file to an existing remote branch via the GitHub Contents API.
#
# Why the API and not `git commit`: a commit created through the API with a
# GitHub App token is signed by GitHub and reports as "Verified". A local
# `git commit` pushed over HTTPS is never signed, so branch protection that
# requires verified signatures rejects it. Release PR commits must use this path.
#
# Usage: commit-file-via-api.sh <repo> <branch> <file-path> <commit-message>
#
#   repo            owner/name (e.g. operational-context-graph/operational-context-graph)
#   branch          target branch; MUST already exist on the remote
#   file-path       repo-relative path, present in the working tree
#   commit-message  commit subject
#
# Requires: gh authenticated as the committing identity. Set GH_TOKEN to the App
# installation token so the resulting commit is signed by GitHub.

set -euo pipefail

REPO="${1:?repo required (owner/name)}"
BRANCH="${2:?branch required}"
FILE="${3:?file path required}"
MESSAGE="${4:?commit message required}"

if [ ! -f "$FILE" ]; then
  echo "::error::File '$FILE' not found in working tree" >&2
  exit 1
fi

CONTENT=$(base64 -w 0 < "$FILE")

# Look up the current blob SHA on the target branch. A new file returns 404, so
# tolerate failure and leave SHA empty; the create path MUST omit sha entirely
# (an empty sha is rejected with 422). Scope the lookup to the release branch
# with ?ref, not the default branch.
SHA=$(gh api "repos/${REPO}/contents/${FILE}?ref=${BRANCH}" --jq '.sha // ""' 2>/dev/null || true)

args=(
  -X PUT
  -H "Accept: application/vnd.github+json"
  -f "message=${MESSAGE}"
  -f "content=${CONTENT}"
  -f "branch=${BRANCH}"
)
if [ -n "$SHA" ]; then
  args+=(-f "sha=${SHA}")
fi

gh api "repos/${REPO}/contents/${FILE}" "${args[@]}" >/dev/null
echo "Committed ${FILE} to ${BRANCH} via API (signed)"
