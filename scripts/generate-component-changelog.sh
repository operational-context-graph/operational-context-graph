#!/usr/bin/env bash
# Generate a changelog for a single component and prepend it to the component's CHANGELOG.md.
# Also writes the new section only to <component-path>/CHANGELOG.new for use as PR body content.
#
# Usage: generate-component-changelog.sh <component-path> <tag>
# Example: generate-component-changelog.sh services/data-ingestion services/data-ingestion/v1.2.0
#
# Requires git-cliff to be installed and a cliff.toml at the repository root.
# Set GITHUB_TOKEN in the environment for PR/commit metadata enrichment.

set -euo pipefail

COMPONENT_PATH="${1:?component path required}"
TAG="${2:?tag required}"
CHANGELOG="${COMPONENT_PATH}/CHANGELOG.md"
NEW_SECTION="${COMPONENT_PATH}/CHANGELOG.new"

if [ ! -d "$COMPONENT_PATH" ]; then
  echo "error: component path '${COMPONENT_PATH}' does not exist" >&2
  exit 1
fi

# Write only the new (unreleased) section (used for the PR body).
git cliff \
  --include-path "${COMPONENT_PATH}/**" \
  --tag "$TAG" \
  --unreleased \
  --strip all \
  --output "$NEW_SECTION"

# Prepend the new section to the component changelog.
git cliff \
  --include-path "${COMPONENT_PATH}/**" \
  --tag "$TAG" \
  --prepend "$CHANGELOG"
