#!/usr/bin/env bash
# Build and sign an OCM bundle from pre-built OCI image tarballs.
#
# Runs the shared pipeline stages: build → package → inspect → sign → verify.
# Assumes OCI tarballs are already present in bin/images/. Use make ocm-dev
# to build them locally first, or make ocm-build in CI where they are pulled.
#
# Usage:
#   scripts/ocm-bundle.sh <version>
#
# Required env:
#   SIGN_KEY  — path to RSA private key PEM; copied to /tmp/ocg-signing-key.pem
#               so the .ocm/config credential lookup resolves it (default: /tmp/ocg-signing-key.pem)
#
# Example:
#   SIGN_KEY=.ocm/ocg.key scripts/ocm-bundle.sh 1.2.3
#   SIGN_KEY=.ocm/ocg.key scripts/ocm-bundle.sh 0.0.0

set -euo pipefail

VERSION="${1:?version argument required}"
SIGN_KEY="${SIGN_KEY:-/tmp/ocg-signing-key.pem}"
OCM_SERVICES="data-ingestion telemetry-store inventory-store inventory-connector"
OCM_COMPONENT="github.com/operational-context-graph/operational-context-graph"
OCM_CTF="ctf::./bin/ctf//"

# ---------------------------------------------------------------------------
# Prerequisites
# ---------------------------------------------------------------------------

for tool in ocm helm yq python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "error: $tool: not found" >&2; exit 1; }
done

if ! echo "$VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "error: VERSION '$VERSION' is not valid semver (X.Y.Z)" >&2
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

for svc in $OCM_SERVICES; do
  tar="bin/images/${svc}.oci.tar"
  if [ ! -s "$tar" ]; then
    echo "error: $tar: missing or empty — run 'make ocm-dev-images' or pull images first" >&2
    exit 1
  fi
  count=$(tar -xOf "$tar" index.json 2>/dev/null \
    | python3 -c 'import sys,json; d=json.load(sys.stdin); print(len(d["manifests"]))' 2>/dev/null \
    || echo "ERR")
  if [ "$count" = "ERR" ]; then
    echo "error: $svc: index.json unreadable in $tar" >&2; exit 1
  fi
  if [ "$count" != "1" ]; then
    echo "error: $svc: expected 1 manifest in $tar, found $count" >&2; exit 1
  fi
done

echo "preflight: OK (VERSION=${VERSION})"

# ---------------------------------------------------------------------------
# Stage 1: build — Helm chart + bundle-manifest.yaml + image-values.yaml
# ---------------------------------------------------------------------------

helm dependency update helm/
helm package helm/ -d bin/ --version "$VERSION"

{
  echo "version: \"${VERSION}\""
  echo "sourceCommit: \"$(git rev-parse HEAD)\""
  echo "imageDigests:"
  for svc in $OCM_SERVICES; do
    digest=$(tar -xOf "bin/images/${svc}.oci.tar" index.json 2>/dev/null \
      | python3 -c 'import sys,json; m=json.load(sys.stdin)["manifests"][0]; print(m["digest"])' 2>/dev/null)
    echo "  ${svc}: \"${digest}\""
  done
} > bin/bundle-manifest.yaml

yq . bin/bundle-manifest.yaml >/dev/null
echo "build: bundle-manifest.yaml OK"

{
  printf 'imageDefaults:\n  repository: ${IMAGE_REPOSITORY}\nimages:\n'
  for svc in $OCM_SERVICES; do
    digest=$(tar -xOf "bin/images/${svc}.oci.tar" index.json 2>/dev/null \
      | python3 -c 'import sys,json; m=json.load(sys.stdin)["manifests"][0]; print(m["digest"])' 2>/dev/null)
    printf '  %s:\n    digest: "%s"\n' "$svc" "$digest"
  done
} > bin/image-values.yaml

# Literal placeholder must survive — grep for the unexpanded string
grep -F '${IMAGE_REPOSITORY}' bin/image-values.yaml >/dev/null \
  || { echo "error: IMAGE_REPOSITORY placeholder missing from image-values.yaml" >&2; exit 1; }

echo "build: image-values.yaml OK"

# ---------------------------------------------------------------------------
# Stage 2: package — assemble CTF archive
# ---------------------------------------------------------------------------

rm -rf bin/ctf/
VERSION="$VERSION" ocm add component-version \
  --working-directory . \
  --constructor .ocm/component-constructor.yaml \
  --repository ctf::./bin/ctf/

echo "package: OK"

# ---------------------------------------------------------------------------
# Stage 3: inspect — verify digest round-trip
# ---------------------------------------------------------------------------

echo "inspect: checking resource digests..."
for svc in $OCM_SERVICES; do
  expected=$(yq ".imageDigests.${svc}" bin/bundle-manifest.yaml 2>/dev/null | tr -d '"')
  actual=$(tar -xOf "bin/images/${svc}.oci.tar" index.json 2>/dev/null \
    | python3 -c 'import sys,json; m=json.load(sys.stdin)["manifests"][0]; print(m["digest"])' 2>/dev/null)
  if [ "$expected" != "$actual" ]; then
    echo "error: $svc digest mismatch: expected $expected, actual $actual" >&2
    exit 1
  fi
done
echo "inspect: OK"

# ---------------------------------------------------------------------------
# Stage 4: sign — .ocm/config resolves key from /tmp/ocg-signing-key.pem
# ---------------------------------------------------------------------------

[ "$(realpath "$SIGN_KEY")" != "/tmp/ocg-signing-key.pem" ] && cp "$SIGN_KEY" /tmp/ocg-signing-key.pem || true
ocm sign component-version \
  --signature ocg-sig \
  "${OCM_CTF}${OCM_COMPONENT}:${VERSION}"
echo "sign: OK"

# ---------------------------------------------------------------------------
# Stage 5: verify
# ---------------------------------------------------------------------------

ocm verify component-version \
  --signature ocg-sig \
  "${OCM_CTF}${OCM_COMPONENT}:${VERSION}"

tar -czf "bin/ocg-${VERSION}.ctf.tgz" -C bin ctf/
echo "verify: OK — bin/ocg-${VERSION}.ctf.tgz"
