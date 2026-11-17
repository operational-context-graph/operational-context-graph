#!/usr/bin/env bash
# Pin subchart image tags to a specific component version.
#
# Usage: pin-subchart-versions.sh <slug> <component-path> [version-override]
#
#   slug             Helm chart directory name under helm/charts/
#   component-path   Repo-relative path to the component (e.g. services/data-ingestion)
#   version-override Version string to use; if empty, resolves latest released semver tag
#
# Exits non-zero if no version can be resolved, the chart directory is missing,
# or .image.tag is absent from the chart's values.yaml.
# Stages the modified values.yaml with git add; caller is responsible for committing.

set -euo pipefail

SLUG="${1:?slug required}"
COMPONENT_PATH="${2:?component-path required}"
OVERRIDE="${3:-}"

resolve_version() {
  if [ -n "${OVERRIDE}" ]; then
    local tag="${COMPONENT_PATH}/v${OVERRIDE}"
    if ! git tag --list | grep -qxF "${tag}"; then
      echo "::error::Override tag '${tag}' does not exist" >&2
      exit 1
    fi
    echo "${OVERRIDE}"
    return
  fi
  git tag --list "${COMPONENT_PATH}/v*" \
    | sed "s|${COMPONENT_PATH}/v||" \
    | sort -V \
    | tail -1
}

VERSION=$(resolve_version)
if [ -z "${VERSION}" ]; then
  echo "::error::No released version found for ${SLUG} (${COMPONENT_PATH}) and no override supplied"
  exit 1
fi

CHART_DIR="helm/charts/${SLUG}"
if [ ! -d "${CHART_DIR}" ]; then
  echo "::error::Helm chart directory ${CHART_DIR} does not exist"
  exit 1
fi

VALUES_FILE="${CHART_DIR}/values.yaml"
if [ "$(yq e '.image.tag // ""' "${VALUES_FILE}")" = "" ]; then
  echo "::error::Field .image.tag not found in ${VALUES_FILE}"
  exit 1
fi

CURRENT=$(yq e '.image.tag' "${VALUES_FILE}")
if [ "${CURRENT}" = "${VERSION}" ]; then
  echo "${SLUG}: already at ${VERSION}, no change"
  exit 0
fi

yq -i ".image.tag = \"${VERSION}\"" "${VALUES_FILE}"
echo "${SLUG}: pinned ${CURRENT} → ${VERSION}"
git add "${VALUES_FILE}"
