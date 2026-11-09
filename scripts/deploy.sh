#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and Operational Context Graph contributors
#
# SPDX-License-Identifier: Apache-2.0
#
# Deploy the OCG OCM bundle from a registry.
#
# Usage:
#   IMAGE_REPOSITORY=<registry>/<namespace> ./scripts/deploy.sh <registry-ref> [<namespace>]
#
# Example:
#   IMAGE_REPOSITORY=ghcr.io/myorg ./scripts/deploy.sh \
#     ghcr.io/myorg/ocg/operational-context-graph:1.2.3 ocg
#
# Prerequisites: ocm >= 0.22.1 (v2), helm, envsubst (gettext), yq

set -euo pipefail

REGISTRY_REF="${1:?Usage: $0 <registry-ref> [<namespace>]}"
NAMESPACE="${2:-ocg}"

OCM_COMPONENT="github.com/operational-context-graph/operational-context-graph"
IMAGE_VALUES="bin/image-values.yaml"
IMAGE_VALUES_SUBST="/tmp/ocg-image-values-$$.yaml"

# ── 1. Verify bundle signature against registry ───────────────────────────────
echo "==> Verifying OCM bundle at ${REGISTRY_REF}..."
ocm verify component-version \
  --signature ocg-sig \
  "${REGISTRY_REF}"
echo "    verify: OK"

# ── 2. Substitute IMAGE_REPOSITORY placeholder ───────────────────────────────
if [ -z "${IMAGE_REPOSITORY:-}" ]; then
  echo "ERROR: IMAGE_REPOSITORY is not set" >&2
  exit 1
fi

if ! grep -q '\${IMAGE_REPOSITORY}' "${IMAGE_VALUES}"; then
  echo "ERROR: ${IMAGE_VALUES} does not contain the \${IMAGE_REPOSITORY} placeholder — substitution was not run or the file is corrupt" >&2
  exit 1
fi

envsubst '${IMAGE_REPOSITORY}' < "${IMAGE_VALUES}" > "${IMAGE_VALUES_SUBST}"
trap 'rm -f "${IMAGE_VALUES_SUBST}"' EXIT

# Verify placeholder was actually substituted
if grep -q '\${IMAGE_REPOSITORY}' "${IMAGE_VALUES_SUBST}"; then
  echo "ERROR: \${IMAGE_REPOSITORY} placeholder was not substituted in ${IMAGE_VALUES_SUBST}" >&2
  exit 1
fi

echo "    image-values substitution: OK (IMAGE_REPOSITORY=${IMAGE_REPOSITORY})"

# ── 3. Check access.localReference matches expected digests per resource ──────
echo "==> Checking resource digest references..."

# Download the component descriptor to inspect resource access entries
ocm download componentdescriptor \
  "${REGISTRY_REF}" \
  -O /tmp/ocg-descriptor-$$.yaml 2>/dev/null || \
  ocm get component-version \
  "${REGISTRY_REF}" \
  -o yaml > /tmp/ocg-descriptor-$$.yaml

SERVICES="schema-registry data-ingestion telemetry-store inventory-store inventory-connector"
mismatches=""

for svc in ${SERVICES}; do
  expected_digest=$(yq ".spec.resources[] | select(.name == \"${svc}\") | .access.localReference" /tmp/ocg-descriptor-$$.yaml 2>/dev/null || echo "")
  values_digest=$(yq ".images.${svc}.digest" "${IMAGE_VALUES_SUBST}" 2>/dev/null | tr -d '"' || echo "")

  if [ -z "${expected_digest}" ]; then
    echo "  WARNING: no localReference found for ${svc} in component descriptor — skipping check"
    continue
  fi

  if [ "${values_digest}" != "${expected_digest}" ]; then
    echo "ERROR: ${svc}: digest mismatch" >&2
    echo "  image-values: ${values_digest}" >&2
    echo "  descriptor:   ${expected_digest}" >&2
    mismatches="${mismatches} ${svc}"
  else
    echo "  ${svc}: OK (${expected_digest:0:20}...)"
  fi
done

rm -f /tmp/ocg-descriptor-$$.yaml

if [ -n "${mismatches}" ]; then
  echo "ERROR: digest mismatches for:${mismatches}" >&2
  echo "       Aborting helm install." >&2
  exit 1
fi

echo "    digest checks: OK"

# ── 4. Helm install ───────────────────────────────────────────────────────────
echo "==> Running helm install..."
helm install ocg helm/ocg/ \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  --values "${IMAGE_VALUES_SUBST}"

echo "==> Deployment complete."
