#!/usr/bin/env bash
# Re-version a signed OCM bundle from a source CTF to a new release version.
#
# Downloads the dev CTF from an OCI registry, verifies its signature, extracts
# all resources, builds a new CTF at the release version from the extracted
# artifacts, signs, and verifies.
#
# Only the component version changes. Resource versions are preserved from the
# dev descriptor — they reflect the actual image tags built and tested in CI.
#
# Stages: fetch → verify-source → extract → build → sign → verify
#
# Usage:
#   scripts/ocm-reversion.sh <dev-version> <release-version> <source-registry>
#
# Args:
#   dev-version       — version of the source dev bundle (e.g. 0.0.0-dev.20260118-abc1234)
#   release-version   — release version to produce (e.g. 1.2.3)
#   source-registry   — OCI registry base URL (e.g. ghcr.io/my-org)
#
# Env:
#   SIGN_KEY          — path to RSA private key PEM (default: /tmp/ocg-signing-key.pem)
#
# Outputs:
#   bin/ctf/          — new CTF at release-version
#   bin/ocg-<release-version>.ctf.tgz

set -euo pipefail

DEV_VERSION="${1:?dev-version argument required}"
RELEASE_VERSION="${2:?release-version argument required}"
SOURCE_REGISTRY="${3:?source-registry argument required}"

SIGN_KEY="${SIGN_KEY:-/tmp/ocg-signing-key.pem}"
OCM_COMPONENT="github.com/operational-context-graph/operational-context-graph"
OCM_SERVICES="data-ingestion telemetry-store inventory-store inventory-connector"
WORKSPACE="bin/reversion-workspace"

# ---------------------------------------------------------------------------
# Prerequisites
# ---------------------------------------------------------------------------

for tool in ocm helm yq python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: $tool: not found" >&2; exit 1; }
done

if ! echo "$RELEASE_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+'; then
  echo "error: RELEASE_VERSION '$RELEASE_VERSION' must start with X.Y.Z semver" >&2
  exit 1
fi

if [ ! -s "$SIGN_KEY" ]; then
  echo "error: SIGN_KEY='$SIGN_KEY' does not exist or is empty" >&2
  exit 1
fi

if [ ! -s .ocm/ocg.pub.pem ]; then
  echo "error: .ocm/ocg.pub.pem: missing or empty" >&2
  exit 1
fi

echo "preflight: OK (DEV_VERSION=${DEV_VERSION} RELEASE_VERSION=${RELEASE_VERSION})"

# ---------------------------------------------------------------------------
# Stage 1: fetch — transfer dev CTF from OCI registry to local CTF
# ---------------------------------------------------------------------------

SOURCE_REF="oci::${SOURCE_REGISTRY}/operational-context-graph-bundle"
DEV_CTF_DIR="bin/dev-ctf"
DEV_CTF="ctf::./bin/dev-ctf//"

echo "fetch: transferring ${SOURCE_REF} (${DEV_VERSION}) → ${DEV_CTF_DIR}/ ..."
rm -rf "${DEV_CTF_DIR}/"
ocm transfer component-version \
  "${SOURCE_REF}//${OCM_COMPONENT}:${DEV_VERSION}" \
  "ctf::./bin/dev-ctf/"
echo "fetch: OK"

# ---------------------------------------------------------------------------
# Stage 2: verify-source — confirm dev CTF signature before trusting content
# ---------------------------------------------------------------------------

DEV_REF="${DEV_CTF}${OCM_COMPONENT}:${DEV_VERSION}"

echo "verify-source: verifying dev CTF signature..."
ocm verify component-version \
  --signature ocg-sig \
  "${DEV_REF}"
echo "verify-source: OK"

# ---------------------------------------------------------------------------
# Stage 3: extract — read resource versions from descriptor, download artifacts
# ---------------------------------------------------------------------------

rm -rf "${WORKSPACE}"
mkdir -p "${WORKSPACE}"

echo "extract: reading resource versions from dev descriptor..."
DESCRIPTOR_JSON=$(ocm get component-version "${DEV_REF}" -o json)

# Read resource version for each named resource; validate they are present
get_resource_version() {
  local name="$1"
  echo "$DESCRIPTOR_JSON" \
    | python3 -c "
import sys, json
d = json.load(sys.stdin)
resources = d.get('component', {}).get('resources', [])
for r in resources:
    if r.get('name') == sys.argv[1]:
        print(r['version'])
        sys.exit(0)
print('', end='')
" "$name"
}

declare -A RES_VERSIONS
for svc in $OCM_SERVICES; do
  v=$(get_resource_version "$svc")
  if [ -z "$v" ]; then
    echo "error: resource '${svc}' not found in dev descriptor" >&2
    exit 1
  fi
  RES_VERSIONS[$svc]="$v"
  echo "  ${svc}: ${v}"
done

V_CHART=$(get_resource_version "chart")
V_BUNDLE_MANIFEST=$(get_resource_version "bundle-manifest")
V_IMAGE_VALUES=$(get_resource_version "image-values")

for name_v in "chart:${V_CHART}" "bundle-manifest:${V_BUNDLE_MANIFEST}" "image-values:${V_IMAGE_VALUES}"; do
  name="${name_v%%:*}"
  v="${name_v##*:}"
  if [ -z "$v" ]; then
    echo "error: resource '${name}' not found in dev descriptor" >&2
    exit 1
  fi
  echo "  ${name}: ${v}"
done

echo "extract: downloading resources from dev CTF..."

for svc in $OCM_SERVICES; do
  echo "  ${svc} (OCI image → ${WORKSPACE}/${svc}-oci/)"
  ocm download resource "${DEV_REF}" \
    --identity "name=${svc}" \
    --output "${WORKSPACE}/${svc}-oci"
done

echo "  chart (Helm → ${WORKSPACE}/chart/)"
ocm download resource "${DEV_REF}" \
  --identity "name=chart" \
  --transformer helm \
  --output "${WORKSPACE}/chart"

echo "  bundle-manifest"
ocm download resource "${DEV_REF}" \
  --identity "name=bundle-manifest" \
  --output "${WORKSPACE}/bundle-manifest.yaml"

echo "  image-values"
ocm download resource "${DEV_REF}" \
  --identity "name=image-values" \
  --output "${WORKSPACE}/image-values.yaml"

echo "extract: OK"

# ---------------------------------------------------------------------------
# Stage 4: build — assemble new CTF at release version
# ---------------------------------------------------------------------------

HELM_TGZ=$(find "${WORKSPACE}/chart" -name "*.tgz" | head -1)
if [ -z "$HELM_TGZ" ]; then
  echo "error: no .tgz found under ${WORKSPACE}/chart after helm extraction" >&2
  exit 1
fi

# Read labels from dev descriptor to carry forward
LABELS_JSON=$(echo "$DESCRIPTOR_JSON" \
  | python3 -c 'import sys,json; d=json.load(sys.stdin); print(json.dumps(d.get("component",{}).get("labels",[])))')

CONSTRUCTOR="${WORKSPACE}/component-constructor.yaml"
cat > "$CONSTRUCTOR" <<YAML
components:
  - name: ${OCM_COMPONENT}
    version: "${RELEASE_VERSION}"
    provider:
      name: autonomous-operations
    labels: []
    resources:
YAML

for svc in $OCM_SERVICES; do
  cat >> "$CONSTRUCTOR" <<YAML
      - name: ${svc}
        type: ociImage
        version: "${RES_VERSIONS[$svc]}"
        input:
          type: ociImage
          path: ${WORKSPACE}/${svc}-oci
YAML
done

cat >> "$CONSTRUCTOR" <<YAML
      - name: chart
        type: helmChart
        version: "${V_CHART}"
        input:
          type: helm
          path: ${HELM_TGZ}
      - name: bundle-manifest
        type: blob
        version: "${V_BUNDLE_MANIFEST}"
        input:
          type: file
          mediaType: application/yaml
          path: ${WORKSPACE}/bundle-manifest.yaml
      - name: image-values
        type: blob
        version: "${V_IMAGE_VALUES}"
        input:
          type: file
          mediaType: application/yaml
          path: ${WORKSPACE}/image-values.yaml
YAML

# Carry labels forward from dev descriptor
if [ "$LABELS_JSON" != "[]" ] && [ "$LABELS_JSON" != "null" ]; then
  python3 - "$CONSTRUCTOR" "$LABELS_JSON" <<'PYEOF'
import sys, json
path, labels_json = sys.argv[1], sys.argv[2]
labels = json.loads(labels_json)
with open(path) as f:
    content = f.read()
labels_yaml = "    labels:\n" + "".join(
    f"      - name: \"{l['name']}\"\n        value: \"{l['value']}\"\n"
    for l in labels
)
content = content.replace("    labels: []\n", labels_yaml)
with open(path, "w") as f:
    f.write(content)
PYEOF
  echo "build: labels carried forward from dev descriptor"
fi

rm -rf bin/ctf/
VERSION="$RELEASE_VERSION" ocm add component-version \
  --working-directory . \
  --constructor "$CONSTRUCTOR" \
  --repository "ctf::./bin/ctf/"
echo "build: OK"

# ---------------------------------------------------------------------------
# Stage 5: sign
# ---------------------------------------------------------------------------

[ "$(realpath "$SIGN_KEY")" != "/tmp/ocg-signing-key.pem" ] && cp "$SIGN_KEY" /tmp/ocg-signing-key.pem || true
ocm sign component-version \
  --signature ocg-sig \
  "ctf::./bin/ctf//${OCM_COMPONENT}:${RELEASE_VERSION}"
echo "sign: OK"

# ---------------------------------------------------------------------------
# Stage 6: verify
# ---------------------------------------------------------------------------

ocm verify component-version \
  --signature ocg-sig \
  "ctf::./bin/ctf//${OCM_COMPONENT}:${RELEASE_VERSION}"

tar -czf "bin/ocg-${RELEASE_VERSION}.ctf.tgz" -C bin ctf/
echo "verify: OK — bin/ocg-${RELEASE_VERSION}.ctf.tgz"
