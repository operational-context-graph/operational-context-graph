#!/usr/bin/env bash
# Parse a release branch name and emit structured outputs for the release workflow.
#
# Usage: parse-release-branch.sh <branch>
#
# Branch formats:
#   release/<slug>/<version>: component release
#   release/<version>:system release
#   <anything else>: not a release; all outputs are empty
#
# Outputs:
#   release_type   "component" | "system" | ""
#   component      full path relative to repo root (e.g. services/data-ingestion)
#   slug           last path segment of component (e.g. data-ingestion)
#   version        semver string without leading v (e.g. 1.0.0)
#   tag            full git tag (e.g. services/data-ingestion/v1.0.0 or v1.0.0)
#   has_dockerfile "true" | "false"
#

set -euo pipefail

BRANCH="${1:?branch name required}"
OUTPUT="${GITHUB_OUTPUT:-/dev/stdout}"

emit() {
  echo "$1=$2" >> "$OUTPUT"
}

emit_empty() {
  emit "release_type" ""
  emit "component"    ""
  emit "slug"         ""
  emit "version"      ""
  emit "tag"          ""
  emit "has_dockerfile" "false"
}

# Strip leading "release/" prefix. If the branch doesn't start with it, exit early.
if [[ "$BRANCH" != release/* ]]; then
  emit_empty
  exit 0
fi

remainder="${BRANCH#release/}"

# Determine if this is a component or system release.
# A system release has a single semver segment: release/<version>
# A component release has at least two segments: release/<slug>/<version>
# Use validate-version.sh to distinguish, if the remainder is a valid version,
# it's a system release. Otherwise treat it as component/<version>.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if "$SCRIPT_DIR/validate-version.sh" "$remainder" 2>/dev/null; then
  # System release: release/<version>
  version="$remainder"
  emit "release_type"    "system"
  emit "component"       ""
  emit "slug"            ""
  emit "version"         "$version"
  emit "tag"             "v${version}"
  emit "has_dockerfile"  "false"
  exit 0
fi

# Component release: release/<slug>/<version>
# The version is the last segment; everything before it (after "release/") is the slug path.
version="${remainder##*/}"
slug_path="${remainder%/*}"

if ! "$SCRIPT_DIR/validate-version.sh" "$version" 2>/dev/null; then
  echo "::error::Could not parse branch '$BRANCH': '$version' is not a valid semver" >&2
  exit 1
fi

# The slug is the last segment of the slug path (handles nested paths like connectors/inventory).
slug="${slug_path##*/}"

# Resolve the full component path by searching the entire repo for a directory
# whose basename matches the slug and contains a recognisable module manifest.
# Excludes hidden directories and node_modules.
component=""
while IFS= read -r manifest; do
  dir="$(dirname "$manifest")"
  if [[ "$(basename "$dir")" == "$slug" ]]; then
    component="$dir"
    break
  fi
done < <(find . \
           -not \( -path '*/.*' -prune \) \
           -not \( -path '*/node_modules/*' -prune \) \
           \( -name "go.mod" -o -name "pyproject.toml" -o -name "package.json" \) \
           2>/dev/null \
           | sed 's|^\./||' \
           | sort)

if [[ -z "$component" ]]; then
  echo "::error::Could not resolve component path for slug '$slug' from branch '$BRANCH'" >&2
  exit 1
fi

has_dockerfile="false"
if [[ -f "$component/Dockerfile" ]]; then
  has_dockerfile="true"
fi

emit "release_type"    "component"
emit "component"       "$component"
emit "slug"            "$slug"
emit "version"         "$version"
emit "tag"             "${component}/v${version}"
emit "has_dockerfile"  "$has_dockerfile"
