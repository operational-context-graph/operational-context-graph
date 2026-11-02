#!/usr/bin/env bash
# go-test.sh — run go test with a spinner, then report coverage.
#
# Usage: go-test.sh <name> <profile> <threshold> <enforce> <html-out>
#
#   name       service name (e.g. data-ingestion)
#   profile    path for the .out coverage profile
#   threshold  minimum acceptable total coverage percentage (e.g. 95)
#   enforce    1 = exit non-zero when below threshold; 0 = warn only
#   html-out   path for the HTML coverage report

set -uo pipefail

NAME="${1:?name required}"
PROFILE="${2:?profile required}"
THRESHOLD="${3:?threshold required}"
ENFORCE="${4:?enforce required}"
HTML_OUT="${5:?html-out required}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

_spinner() {
  local pid=$1 frames='/-\|' i=0
  while kill -0 "$pid" 2>/dev/null; do
    printf "\r  %s" "${frames:$((i % 4)):1}" >/dev/tty
    i=$(( i + 1 ))
    sleep 0.1
  done
  printf "\r\033[2K" >/dev/tty
}

TMPOUT="$(mktemp)"
go test ./... -race -coverprofile="$PROFILE" >"$TMPOUT" 2>&1 &
TEST_PID=$!
_spinner "$TEST_PID"
wait "$TEST_PID"
test_rc=$?

if [ "$test_rc" -ne 0 ]; then
  cat "$TMPOUT"
fi
rm -f "$TMPOUT"

"$SCRIPT_DIR/go-coverage.sh" "$NAME" "$PROFILE" "$THRESHOLD" "$ENFORCE" "$HTML_OUT"
cov_rc=$?

exit $(( test_rc || cov_rc ))
