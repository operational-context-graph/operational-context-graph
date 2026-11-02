#!/usr/bin/env bash
# go-coverage.sh — run Go tests and print a per-package coverage table.
#
# Usage: go-coverage.sh <name> <profile> <threshold> <enforce> <html-out>
#
#   name       service name shown in the header (e.g. data-ingestion)
#   profile    path to the .out coverage profile written by go test
#   threshold  minimum acceptable total coverage percentage (e.g. 95)
#   enforce    1 = exit non-zero when coverage is below threshold; 0 = warn only
#   html-out   path to write the HTML coverage report

set -euo pipefail

NAME="${1:?name required}"
PROFILE="${2:?profile required}"
THRESHOLD="${3:?threshold required}"
ENFORCE="${4:?enforce required}"
HTML_OUT="${5:?html-out required}"

go tool cover -func="$PROFILE" 2>/dev/null \
  | awk \
      -v thr="$THRESHOLD" \
      -v name="$NAME" \
      '
      /^total:/  { total = $NF; next }
      /^github/  {
        path = $1
        sub(/^.*\/services\/[^\/]+\//, "", path)
        sub(/\/[^\/]+\.go:[0-9]+:.*/, "", path)
        if (path ~ /^cmd\//) next
        pct = $NF; sub(/%/, "", pct)
        sum[path] += pct; cnt[path]++
      }
      END {
        for (p in sum) printf "%s %.1f\n", p, sum[p] / cnt[p]
        sub(/%/, "", total); printf "TOTAL %s\n", total
      }
      ' \
  | sort \
  | awk \
      -v thr="$THRESHOLD" \
      -v enforce="$ENFORCE" \
      -v name="$NAME" \
      '
      BEGIN { fail = 0; printf "\ncoverage: %s\n", name }

      /^TOTAL/ {
        color = ($2 + 0 < thr) ? "\033[31m" : "\033[32m"
        printf "  \033[1m%-50s %5s%%\033[0m\n\n", "total", $2
        if ($2 + 0 < thr) {
          printf "\033[31mERROR: total coverage %.1f%% is below threshold %d%%\033[0m\n", $2 + 0, thr
          fail = 1
        }
        next
      }

      {
        color  = ($2 + 0 < thr) ? "\033[31m" : "\033[32m"
        marker = ($2 + 0 < thr) ? " !" : "  "
        printf "  %s%-50s %5.1f%%\033[0m%s\n", color, $1, $2, marker
      }

      END { exit (enforce ? fail : 0) }
      '

go tool cover -html="$PROFILE" -o "$HTML_OUT" 2>/dev/null
printf "  coverage report: \033[4mfile://%s\033[0m\n\n" "$HTML_OUT"
