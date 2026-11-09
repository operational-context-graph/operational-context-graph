#!/usr/bin/env bash
# Pin image tag and digest into each service subchart's values.yaml.
#
# Reads resolved versions from stdin, one line per service:
#   <slug> <version> <digest>
#
# Writes:
#   helm/charts/<slug>/values.yaml  image.tag = <version>
#   helm/charts/<slug>/values.yaml  image.digest = <digest>
#
# Requires: yq >= v4 on PATH.
#
# Usage (pipe from resolve-component-versions.sh stderr):
#   scripts/resolve-component-versions.sh 2>&1 | grep -A100 'RESOLVED_VERSIONS:' | tail -n +2 | \
#     scripts/pin-image-digests.sh

set -euo pipefail

CHART_DIR="helm/charts"

pin_service() {
  local slug="$1"
  local version="$2"
  local digest="$3"
  local values="${CHART_DIR}/${slug}/values.yaml"

  if [ ! -f "$values" ]; then
    echo "::error::${values}: not found" >&2
    return 1
  fi

  yq -i ".image.tag = \"${version}\"" "$values"
  yq -i ".image.digest = \"${digest}\"" "$values"
  echo "pinned ${slug}: tag=${version} digest=${digest}"
}

while IFS=' ' read -r slug version digest; do
  [ -z "$slug" ] && continue
  pin_service "$slug" "$version" "$digest"
done
