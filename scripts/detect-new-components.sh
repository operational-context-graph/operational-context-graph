#!/usr/bin/env bash
# Detect newly added component manifests relative to a base ref.
#
# Usage: detect-new-components.sh <base-ref>
# Example: detect-new-components.sh origin/main
#
# Prints one component path per line (the directory containing the manifest).
# Exits 0 with no output if no new manifests are found.

set -euo pipefail

BASE_REF="${1:?base ref required}"

git diff --name-status "${BASE_REF}...HEAD" \
  -- '**/go.mod' '**/pyproject.toml' '**/package.json' \
  | awk '$1 == "A" { print $2 }' \
  | xargs -r -I{} dirname {}
