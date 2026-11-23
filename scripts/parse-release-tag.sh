#!/usr/bin/env bash
# Parse a release git tag and emit structured outputs for the release workflows.
#
# The tag is the release contract: the publish workflows trigger on tag push and
# read intent directly from the tag ref, with no PR or branch reverse-lookup.
#
# Usage: parse-release-tag.sh <tag>
#
# Tag formats:
#   v<version>:                      system release   (e.g. v1.2.3)
#   <component-path>/v<version>:     component release (e.g. services/data-ingestion/v1.2.3)
#   <anything else>:                 not a release; all outputs are empty
#
# Outputs (written to $GITHUB_OUTPUT, or stdout when unset):
#   release_type   "component" | "system" | ""
#   component      full path relative to repo root (e.g. services/data-ingestion)
#   slug           last path segment of component (e.g. data-ingestion)
#   version        semver string without leading v (e.g. 1.2.3)
#   tag            the tag, echoed back unchanged
#   has_dockerfile "true" | "false"

set -euo pipefail

TAG="${1:-}"
OUTPUT="${GITHUB_OUTPUT:-/dev/stdout}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

emit() {
  echo "$1=$2" >> "$OUTPUT"
}

emit_empty() {
  emit "release_type"   ""
  emit "component"      ""
  emit "slug"           ""
  emit "version"        ""
  emit "tag"            ""
  emit "has_dockerfile" "false"
}

if [[ -z "$TAG" ]]; then
  emit_empty
  exit 0
fi

# System release: v<version>. The segment after the leading "v" must be valid semver.
if [[ "$TAG" == v* && "$TAG" != */* ]]; then
  version="${TAG#v}"
  if "$SCRIPT_DIR/validate-version.sh" "$version" 2>/dev/null; then
    emit "release_type"   "system"
    emit "component"      ""
    emit "slug"           ""
    emit "version"        "$version"
    emit "tag"            "$TAG"
    emit "has_dockerfile" "false"
    exit 0
  fi
fi

# Component release: <component-path>/v<version>.
# The last segment is v<version>; everything before it is the component path.
last="${TAG##*/}"
component="${TAG%/*}"

if [[ "$last" == v* ]]; then
  version="${last#v}"
  if [[ "$component" != "$TAG" ]] && "$SCRIPT_DIR/validate-version.sh" "$version" 2>/dev/null; then
    if [[ ! -d "$component" ]]; then
      echo "::error::Component path '$component' from tag '$TAG' does not exist" >&2
      exit 1
    fi
    slug="${component##*/}"
    has_dockerfile="false"
    if [[ -f "$component/Dockerfile" ]]; then
      has_dockerfile="true"
    fi
    emit "release_type"   "component"
    emit "component"      "$component"
    emit "slug"           "$slug"
    emit "version"        "$version"
    emit "tag"            "$TAG"
    emit "has_dockerfile" "$has_dockerfile"
    exit 0
  fi
fi

# Not a recognized release tag.
emit_empty
