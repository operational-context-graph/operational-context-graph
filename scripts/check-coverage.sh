#!/usr/bin/env bash
set -euo pipefail

PROFILE="${1:?usage: check-coverage.sh <profile> <threshold>}"
THRESHOLD="${2:?usage: check-coverage.sh <profile> <threshold>}"

if [ ! -f "$PROFILE" ]; then
  echo "ERROR: coverage profile not found: $PROFILE" >&2
  echo "       Run 'make test' first to generate it." >&2
  exit 1
fi

coverage=$(go tool cover -func="$PROFILE" | awk '/^total/{print $3+0}')

echo "Coverage: ${coverage}%"

if awk "BEGIN {exit !(${coverage} < ${THRESHOLD})}"; then
  echo "FAIL: coverage ${coverage}% is below threshold ${THRESHOLD}%"
  exit 1
fi
