#!/usr/bin/env bash
# Install git-cliff and verify its PGP signature.
#
# Usage: install-git-cliff.sh <version>
# Example: install-git-cliff.sh v2.14.2
#
# Installs the git-cliff binary to /usr/local/bin.
# Requires curl and gpg.

set -euo pipefail

VERSION="${1:?version required}"
BASE_URL="https://github.com/orhun/git-cliff/releases/download/${VERSION}"
ARCHIVE="git-cliff-${VERSION#v}-x86_64-unknown-linux-gnu.tar.gz"

echo "Downloading ${ARCHIVE}..."
curl -sSfL -o "/tmp/${ARCHIVE}"     "${BASE_URL}/${ARCHIVE}"
curl -sSfL -o "/tmp/${ARCHIVE}.sig" "${BASE_URL}/${ARCHIVE}.sig"

echo "Verifying PGP signature..."
gpg --keyserver keyserver.ubuntu.com \
    --recv-keys 1D2D410A741137EBC544826F4A92FA17B6619297
gpg --verify "/tmp/${ARCHIVE}.sig" "/tmp/${ARCHIVE}"

echo "Installing git-cliff to /usr/local/bin..."
tar -xz --strip-components=1 -C /usr/local/bin \
  -f "/tmp/${ARCHIVE}" \
  "git-cliff-${VERSION#v}/git-cliff"

echo "Installed $(git cliff --version)"
