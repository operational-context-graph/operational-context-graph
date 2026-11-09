#!/usr/bin/env bash
# Resolve component versions for a system release and emit a Markdown table.
#
# For each component, uses the provided override version if given, otherwise
# resolves the latest published image tag on ghcr.io for that service.
#
# Usage: resolve-component-versions.sh \
#          [data_ingestion_override] \
#          [inventory_store_override] \
#          [telemetry_store_override] \
#          [connectors_inventory_override]
#
# All arguments are optional. Pass an empty string to use the latest tag.
# Requires: skopeo on PATH, GHCR_TOKEN env var (read:packages scope).
#
# Outputs a Markdown table to stdout.

set -euo pipefail

REGISTRY="ghcr.io/operational-context-graph"

# Resolve the latest semver tag for an image on ghcr.
latest_image_tag() {
  local image="$1"
  local token="${GHCR_TOKEN:?GHCR_TOKEN must be set}"
  # List tags via ghcr OCI registry API, pick highest semver that matches X.Y.Z
  skopeo list-tags \
    --creds "${token}:${token}" \
    "docker://${REGISTRY}/${image}" \
    | python3 -c '
import sys, json, re
tags = json.load(sys.stdin).get("Tags", [])
semver = [t for t in tags if re.match(r"^[0-9]+\.[0-9]+\.[0-9]+$", t)]
if not semver:
    sys.exit("no semver tags found")
from functools import cmp_to_key
def cmp(a, b):
    av = list(map(int, a.split(".")))
    bv = list(map(int, b.split(".")))
    return (av > bv) - (av < bv)
print(sorted(semver, key=cmp_to_key(cmp))[-1])
'
}

# Verify an image:version exists on ghcr and return its sha256 digest.
resolve_image() {
  local slug="$1"
  local override="$2"
  local token="${GHCR_TOKEN:?GHCR_TOKEN must be set}"

  local version
  if [ -n "$override" ]; then
    version="$override"
  else
    version=$(latest_image_tag "$slug")
  fi

  # Confirm the image:version exists and fetch its digest.
  local digest
  digest=$(skopeo inspect \
    --creds "${token}:${token}" \
    --format "{{.Digest}}" \
    "docker://${REGISTRY}/${slug}:${version}" 2>/dev/null) || {
    echo "::error::Image ${REGISTRY}/${slug}:${version} not found on ghcr" >&2
    exit 1
  }

  echo "${version} ${digest}"
}

read -r di_ver di_digest <<< "$(resolve_image "data-ingestion"       "${1:-}")"
read -r is_ver is_digest <<< "$(resolve_image "inventory-store"      "${2:-}")"
read -r ts_ver ts_digest <<< "$(resolve_image "telemetry-store"      "${3:-}")"
read -r ci_ver ci_digest <<< "$(resolve_image "inventory-connector"  "${4:-}")"

release_url() {
  local slug="$1"
  local version="$2"
  local tag="services/${slug}/v${version}"
  local encoded="${tag//\//%2F}"
  echo "https://github.com/operational-context-graph/operational-context-graph/releases/tag/${encoded}"
}

printf '| Component | Version | Digest |\n'
printf '| --- | --- | --- |\n'
printf '| `services/data-ingestion` | [%s](%s) | `%s` |\n'       "$di_ver" "$(release_url "data-ingestion" "$di_ver")"      "$di_digest"
printf '| `services/inventory-store` | [%s](%s) | `%s` |\n'      "$is_ver" "$(release_url "inventory-store" "$is_ver")"     "$is_digest"
printf '| `services/telemetry-store` | [%s](%s) | `%s` |\n'      "$ts_ver" "$(release_url "telemetry-store" "$ts_ver")"     "$ts_digest"
printf '| `services/connectors/inventory` | [%s](%s) | `%s` |\n' "$ci_ver" "$(release_url "inventory-connector" "$ci_ver")" "$ci_digest"

# Emit structured output for downstream steps.
# Format: "VERSION DIGEST" lines, one per service, in OCM_SERVICES order.
# Read by pin-image-digests.sh and the release workflow.
cat <<EOF >&2
RESOLVED_VERSIONS:
data-ingestion ${di_ver} ${di_digest}
inventory-store ${is_ver} ${is_digest}
telemetry-store ${ts_ver} ${ts_digest}
inventory-connector ${ci_ver} ${ci_digest}
EOF
