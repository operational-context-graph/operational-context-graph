#!/usr/bin/env bash
# Prepend a new versioned section to CHANGELOG.md, inserting after the
# git-cliff header marker if present.
#
# Usage: prepend-changelog.sh <version> <content>
#
# The content argument should be the full markdown section to prepend,
# including the version heading.
#
# Example:
#   scripts/prepend-changelog.sh "1.0.0" "## [1.0.0] - 2026-11-04
#
#   | Component | Tag |
#   | --- | --- |
#   | \`services/data-ingestion\` | services/data-ingestion/v0.4.0 |"

set -euo pipefail

VERSION="${1:?version required}"
SECTION="${2:?section content required}"
CHANGELOG="CHANGELOG.md"
MARKER="<!-- git-cliff: end of header -->"

if [ ! -f "$CHANGELOG" ]; then
  echo "error: $CHANGELOG not found" >&2
  exit 1
fi

if grep -qF "$MARKER" "$CHANGELOG"; then
  awk -v section="$SECTION" -v marker="$MARKER" \
    '$0 == marker { print; print ""; print section; print ""; next } { print }' \
    "$CHANGELOG" > "${CHANGELOG}.tmp" && mv "${CHANGELOG}.tmp" "$CHANGELOG"
else
  printf '%s\n\n%s' "$SECTION" "$(cat "$CHANGELOG")" > "$CHANGELOG"
fi
