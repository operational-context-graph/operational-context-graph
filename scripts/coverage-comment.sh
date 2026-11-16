#!/usr/bin/env bash
# Reads all coverage profiles from COVERAGE_DIR and prints a markdown table
# comparing each service's coverage against THRESHOLD.
# Usage: coverage-comment.sh <coverage-dir> <threshold>
set -euo pipefail

COVERAGE_DIR="${1:?usage: coverage-comment.sh <coverage-dir> <threshold>}"
THRESHOLD="${2:?usage: coverage-comment.sh <coverage-dir> <threshold>}"

any_fail=0

echo "<!-- coverage-report -->"
echo "## Coverage Report"
echo ""
echo "| Service | Coverage | Threshold | Status |"
echo "|---------|----------|-----------|--------|"

for profile in "$COVERAGE_DIR"/*.out; do
    [ -f "$profile" ] || continue
    service=$(basename "$profile" .out)
    coverage=$(go tool cover -func="$profile" | awk '/^total/{print $3+0}')

    if awk "BEGIN {exit !(${coverage} < ${THRESHOLD})}"; then
        status="❌ FAIL"
        any_fail=1
    else
        status="✅ PASS"
    fi

    echo "| \`${service}\` | ${coverage}% | ${THRESHOLD}% | ${status} |"
done

if [ "$any_fail" -eq 1 ]; then
    echo ""
    echo "> [!WARNING]"
    echo "> One or more services are below the ${THRESHOLD}% coverage threshold."
fi
