#!/usr/bin/env bash
# Usage: go-test.sh <name> <coverage_dir>
set -euo pipefail

name="${1:?name required}"
coverage_dir="${2:?coverage_dir required}"

mkdir -p "$coverage_dir"

go test ./... -race -count=1 -json -coverprofile="$coverage_dir/$name.out" \
  | go tool tparse
test_rc=${PIPESTATUS[0]}

go tool cover -html="$coverage_dir/$name.out" -o "$coverage_dir/$name.html" 2>/dev/null
printf "  coverage report: \033[4mfile://%s/%s.html\033[0m\n" "$coverage_dir" "$name"

exit "$test_rc"
