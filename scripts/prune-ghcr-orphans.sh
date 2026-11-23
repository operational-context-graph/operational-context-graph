#!/usr/bin/env bash
# Delete untagged GHCR orphan manifests that no tagged image still references.
#
# A multi-arch image is an index that points to child manifests (per-platform
# images plus provenance and SBOM attestation manifests). Those children carry
# no tag of their own. Deleting the tagged index does NOT cascade to them, so
# they survive as untagged orphans and accumulate.
#
# This script keeps every untagged manifest that a currently tagged index still
# references and deletes only the rest, older than the retention window.
#
# Usage: prune-ghcr-orphans.sh <org> <package> <retention-days>
#
#   org             GitHub org that owns the package
#   package         package identifier as the API expects it; a nested OCI path
#                   is percent-encoded (e.g. foo%2Fbar). Decoded for the crane ref.
#   retention-days  delete orphans whose updated_at is older than this many days
#
# Requires: gh (authenticated with packages read+delete), jq, and crane.
# Set CRANE_BIN to override the crane binary path. crane must be able to read the
# registry, so authenticate it (docker login ghcr.io) before calling.
#
# Fail-closed: if crane cannot read a tagged index manifest, the script aborts
# without deleting anything, because the keep set would be incomplete.

set -euo pipefail

ORG="${1:?org required}"
PACKAGE="${2:?package required}"
RETENTION_DAYS="${3:?retention-days required}"

CRANE_BIN="${CRANE_BIN:-crane}"
REGISTRY="ghcr.io"
# Decode %2F back to / for the crane image reference.
PACKAGE_PATH="${PACKAGE//%2F//}"
IMAGE="${REGISTRY}/${ORG}/${PACKAGE_PATH}"

cutoff=$(date -u -d "${RETENTION_DAYS} days ago" +%Y-%m-%dT%H:%M:%SZ)

echo "Pruning orphans for ${IMAGE} (cutoff ${cutoff})"

# One combined array across pages; gh concatenates array responses with --paginate.
versions=$(gh api "orgs/${ORG}/packages/container/${PACKAGE}/versions" --paginate)

# Keep set: every tagged index manifest plus all manifests it references.
keep=$(mktemp)
trap 'rm -f "${keep}"' EXIT

while IFS= read -r digest; do
  [ -n "${digest}" ] || continue
  echo "${digest}" >> "${keep}"
  # A tagged plain image manifest has no .manifests[]; jq yields nothing then.
  # A crane failure here is fatal (set -o pipefail) so we never delete against
  # an incomplete keep set.
  "${CRANE_BIN}" manifest "${IMAGE}@${digest}" \
    | jq -r '.manifests[]?.digest' >> "${keep}"
done < <(jq -r '.[] | select(.metadata.container.tags | length > 0) | .name' <<<"${versions}")

sort -u "${keep}" -o "${keep}"

# Delete untagged versions, older than the cutoff, that no tagged index references.
deleted=0
kept=0
while IFS=$'\t' read -r id digest updated; do
  [ -n "${id}" ] || continue
  if grep -qxF "${digest}" "${keep}"; then
    kept=$((kept + 1))
    continue
  fi
  echo "Deleting orphan ${digest} (version ${id}, last updated ${updated})"
  gh api --method DELETE "orgs/${ORG}/packages/container/${PACKAGE}/versions/${id}"
  deleted=$((deleted + 1))
done < <(jq -r --arg cutoff "${cutoff}" \
  '.[] | select(.metadata.container.tags | length == 0) | select(.updated_at < $cutoff) | [.id, .name, .updated_at] | @tsv' \
  <<<"${versions}")

echo "Done: ${deleted} orphan(s) deleted, ${kept} referenced untagged manifest(s) kept."
