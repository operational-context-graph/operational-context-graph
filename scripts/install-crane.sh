#!/usr/bin/env bash
# Install crane (google/go-containerregistry) and verify its SHA256 checksum.
#
# Usage: install-crane.sh <version> <sha256>
# Example: install-crane.sh v0.22.1 0ab7a1d6932a213aed964ce97666c3077fe691c8606413674a8b3e0b9ec4cda0
#
# The caller pins both the version and the expected SHA256 of
# go-containerregistry_Linux_x86_64.tar.gz, so the pin lives next to the
# version at each call site and no per-version map goes stale here.
#
# Installs the crane binary to ~/.local/bin and prints its path on the last line.
# Requires curl, sha256sum, and tar.

set -euo pipefail

VERSION="${1:?version required}"
SHA256="${2:?sha256 required}"

ARCHIVE="go-containerregistry_Linux_x86_64.tar.gz"
URL="https://github.com/google/go-containerregistry/releases/download/${VERSION}/${ARCHIVE}"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

echo "Downloading crane ${VERSION}..."
curl -fsSL -o "${TMP}/${ARCHIVE}" "${URL}"

echo "Verifying SHA256..."
echo "${SHA256}  ${TMP}/${ARCHIVE}" | sha256sum -c -

echo "Installing crane to ~/.local/bin..."
mkdir -p "${HOME}/.local/bin"
tar -xzf "${TMP}/${ARCHIVE}" -C "${HOME}/.local/bin" crane

echo "Installed $("${HOME}/.local/bin/crane" version 2>/dev/null || echo "crane ${VERSION}")"
echo "${HOME}/.local/bin/crane"
