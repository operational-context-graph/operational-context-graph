#!/usr/bin/env bash
# Resolve component versions for a system release and emit a Markdown table.
#
# For each component, uses the provided override version if given, otherwise
# resolves the latest git tag for that component path.
#
# Usage: resolve-component-versions.sh \
#          [data_ingestion_override] \
#          [inventory_store_override] \
#          [telemetry_store_override] \
#          [connectors_inventory_override] \
#          [gen_go_override]
#
# All arguments are optional. Pass an empty string to use the latest tag.
#
# Outputs a Markdown table to stdout.
#
# TODO: Drive the component list dynamically (e.g. via `make go-modules-json`)
# so new components do not require edits here and in the workflow inputs.

set -euo pipefail

resolve_version() {
  local module_path="$1"
  local override="$2"
  if [ -n "$override" ]; then
    local tag="${module_path}/v${override}"
    if ! git tag --list | grep -qxF "$tag"; then
      echo "::error::Override tag '$tag' does not exist" >&2
      exit 1
    fi
    echo "$tag"
  else
    local latest
    latest=$(git tag --list "${module_path}/v*" --sort=-version:refname | head -1)
    if [ -z "$latest" ]; then
      echo "::error::No tag found for '${module_path}'" >&2
      exit 1
    fi
    echo "$latest"
  fi
}

di=$(resolve_version "services/data-ingestion"       "${1:-}")
is=$(resolve_version "services/inventory-store"      "${2:-}")
ts=$(resolve_version "services/telemetry-store"      "${3:-}")
ci=$(resolve_version "services/connectors/inventory" "${4:-}")
gg=$(resolve_version "gen/go"                        "${5:-}")

release_url() {
  local tag="$1"
  local encoded="${tag//\//%2F}"
  echo "https://github.com/operational-context-graph/operational-context-graph/releases/tag/${encoded}"
}

printf '| Component | Tag |\n'
printf '| --- | --- |\n'
printf '| `services/data-ingestion` | [%s](%s) |\n'       "$di" "$(release_url "$di")"
printf '| `services/inventory-store` | [%s](%s) |\n'      "$is" "$(release_url "$is")"
printf '| `services/telemetry-store` | [%s](%s) |\n'      "$ts" "$(release_url "$ts")"
printf '| `services/connectors/inventory` | [%s](%s) |\n' "$ci" "$(release_url "$ci")"
printf '| `gen/go` | [%s](%s) |\n'                        "$gg" "$(release_url "$gg")"
