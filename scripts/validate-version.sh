#!/usr/bin/env bash
# Validate that a version string matches semver format X.Y.Z.
#
# Usage: validate-version.sh <version>
#
# Example: validate-version.sh 1.2.3

set -euo pipefail

VERSION="${1:?version required}"

if ! echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "error: '$VERSION' does not match semver format X.Y.Z" >&2
  exit 1
fi
