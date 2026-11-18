#!/usr/bin/env bash
# Write component version labels into .ocm/component-constructor.yaml.
#
# Reads the resolved component versions from the environment and upserts a
# labels block on the first component entry. Any existing labels are replaced.
#
# Usage:
#   V_DATA_INGESTION=1.2.3 \
#   V_INVENTORY_CONNECTOR=0.4.1 \
#   V_INVENTORY_STORE=0.4.1 \
#   V_TELEMETRY_STORE=0.3.0 \
#   scripts/update-ocm-component-labels.sh
#
# Required env:
#   V_DATA_INGESTION
#   V_INVENTORY_CONNECTOR
#   V_INVENTORY_STORE
#   V_TELEMETRY_STORE
#
# Stages the modified file with git add; caller is responsible for committing.

set -euo pipefail

CONSTRUCTOR=".ocm/component-constructor.yaml"

: "${V_DATA_INGESTION:?V_DATA_INGESTION is required}"
: "${V_INVENTORY_CONNECTOR:?V_INVENTORY_CONNECTOR is required}"
: "${V_INVENTORY_STORE:?V_INVENTORY_STORE is required}"
: "${V_TELEMETRY_STORE:?V_TELEMETRY_STORE is required}"

[ -f "$CONSTRUCTOR" ] || { echo "error: $CONSTRUCTOR not found" >&2; exit 1; }

# Replace (or insert) the labels block under components[0].
# yq path: .components[0].labels
yq -i '
  .components[0].labels = [
    {"name": "component.version/data-ingestion",      "value": env(V_DATA_INGESTION)},
    {"name": "component.version/inventory-connector", "value": env(V_INVENTORY_CONNECTOR)},
    {"name": "component.version/inventory-store",     "value": env(V_INVENTORY_STORE)},
    {"name": "component.version/telemetry-store",     "value": env(V_TELEMETRY_STORE)}
  ]
' "$CONSTRUCTOR"

echo "OCM labels updated:"
yq '.components[0].labels' "$CONSTRUCTOR"

git add "$CONSTRUCTOR"
