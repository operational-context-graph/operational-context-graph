#!/usr/bin/env bash
# Pin a subchart image to a specific component version by tag and digest.
#
# Usage: pin-subchart-versions.sh <slug> <component-path> [version-override]
#
#   slug             Helm chart directory name under helm/charts/
#   component-path   Repo-relative path to the component (e.g. services/data-ingestion)
#   version-override Version string to use; if empty, resolves latest released semver tag
#
# Writes both .image.tag (human-readable, cosmetic) and .image.digest
# (sha256:..., authoritative — _helpers.tpl renders repository@digest when set).
# The digest is resolved from the registry for the release-version image tag, so
# the deployed chart pins an immutable content address, not a movable tag.
#
# Requires: yq, crane (for digest resolution), and GHCR auth for crane.
# Set CRANE_BIN to override the crane binary path (default: crane on PATH).
# The registry/repository is read from .image.repository in the chart values,
# so the digest is always resolved from the image the chart actually references.
#
# Exits non-zero if no version resolves, the chart directory is missing,
# .image.tag is absent, or the digest cannot be resolved.
# Stages the modified values.yaml with git add; caller commits.

set -euo pipefail

SLUG="${1:?slug required}"
COMPONENT_PATH="${2:?component-path required}"
OVERRIDE="${3:-}"

CRANE_BIN="${CRANE_BIN:-crane}"

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

# Coordination guard: assert the pinned version's source tree matches the release
# tree (HEAD), for BOTH the override and resolve-latest paths. Tree content, not
# commit ancestry, so it holds under squash merges. This is the common case when
# version inputs are left blank, so it must run here rather than only on explicit
# overrides in the workflow.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"${SCRIPT_DIR}/validate-component-matrix.sh" "${COMPONENT_PATH}" "${VERSION}"

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

REPOSITORY=$(yq e '.image.repository' "${VALUES_FILE}")
if [ -z "${REPOSITORY}" ] || [ "${REPOSITORY}" = "null" ]; then
  echo "::error::Field .image.repository not found in ${VALUES_FILE}"
  exit 1
fi

# Resolve the immutable digest for the release-version image tag. crane digest
# returns the manifest digest the tag currently points at; we record that so the
# chart no longer depends on the tag staying put.
IMAGE_REF="${REPOSITORY}:${VERSION}"
DIGEST=$("${CRANE_BIN}" digest "${IMAGE_REF}" 2>/dev/null || true)
if [ -z "${DIGEST}" ]; then
  echo "::error::Could not resolve digest for ${IMAGE_REF}. Is the component image published at this version?"
  exit 1
fi
if [[ "${DIGEST}" != sha256:* ]]; then
  echo "::error::Resolved digest '${DIGEST}' for ${IMAGE_REF} is not a sha256 reference"
  exit 1
fi

CURRENT_TAG=$(yq e '.image.tag' "${VALUES_FILE}")
CURRENT_DIGEST=$(yq e '.image.digest // ""' "${VALUES_FILE}")
if [ "${CURRENT_TAG}" = "${VERSION}" ] && [ "${CURRENT_DIGEST}" = "${DIGEST}" ]; then
  echo "${SLUG}: already at ${VERSION} (${DIGEST}), no change"
  exit 0
fi

yq -i ".image.tag = \"${VERSION}\" | .image.digest = \"${DIGEST}\"" "${VALUES_FILE}"
echo "${SLUG}: pinned ${CURRENT_TAG} → ${VERSION} @ ${DIGEST}"
git add "${VALUES_FILE}"
