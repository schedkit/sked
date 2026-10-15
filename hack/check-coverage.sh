#!/usr/bin/env bash
set -euo pipefail
export LC_ALL=C

profile=${1:?Usage: check-coverage.sh PROFILE THRESHOLD}
threshold=${2:?Usage: check-coverage.sh PROFILE THRESHOLD}

if ! awk -v minimum="$threshold" 'BEGIN {
  exit !(minimum ~ /^[0-9]+([.][0-9]+)?$/ && minimum <= 100)
}'; then
  echo "Coverage threshold must be a number between 0 and 100" >&2
  exit 1
fi

go tool cover -func="$profile"

awk -v minimum="$threshold" '
  NR == 1 { next }
  {
    total += $2
    if ($3 > 0) covered += $2
  }
  END {
    print "## Test coverage\n"
    if (total == 0) {
      print "FAIL: Coverage profile contains no statements."
      exit 1
    }
    printf "| Statement coverage | Minimum |\n| --- | --- |\n| %.2f%% | %g%% |\n\n", 100 * covered / total, minimum
    if (100 * covered < minimum * total) {
      print "FAIL: Coverage is below the minimum."
      exit 1
    }
    print "PASS: Coverage meets the minimum."
  }
' "$profile" | tee -a "${GITHUB_STEP_SUMMARY:-/dev/null}"
