#!/bin/bash
#
# analyze-results.sh
# Parses all vegeta JSON result files and prints a formatted summary table.
#
# Usage:
#   ./scripts/perf-test/analyze-results.sh
#
# Reads:  results/*.json
# Writes: results/perf-report-final.txt  (also printed to stdout)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RESULTS_DIR="${PROJECT_ROOT}/results"

mkdir -p "$RESULTS_DIR"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# Verify jq available
if ! command -v jq &> /dev/null; then
  echo -e "${RED}✗ jq not found. Install: brew install jq${NC}"
  exit 1
fi

if [ ! -d "$RESULTS_DIR" ]; then
  echo -e "${RED}✗ Results directory not found: $RESULTS_DIR${NC}"
  echo "  Run tests first: make perf-test"
  exit 1
fi

# Check at least one JSON file exists
json_count=$(find "$RESULTS_DIR" -name "*.json" 2> /dev/null | wc -l | tr -d ' ')
if [ "$json_count" -eq 0 ]; then
  echo -e "${RED}✗ No JSON result files found in $RESULTS_DIR${NC}"
  echo "  Run tests first: make perf-test"
  exit 1
fi

# Extract metrics from a vegeta JSON result file.
# Outputs tab-separated: throughput p50_ms p95_ms p99_ms max_ms success_pct errors
parse_json() {
  local file="$1"
  jq -r '[
    (.throughput * 100 | round | . / 100),
    (.latencies["50th"]  / 1e6 * 100 | round | . / 100),
    (.latencies["95th"]  / 1e6 * 100 | round | . / 100),
    (.latencies["99th"]  / 1e6 * 100 | round | . / 100),
    (.latencies.max      / 1e6 * 100 | round | . / 100),
    (.success * 100      * 100 | round | . / 100),
    (if .errors then (.errors | length) else 0 end)
  ] | @tsv' "$file"
}

REPORT_FILE="$RESULTS_DIR/perf-report-final.txt"

echo -e "${BLUE}╭─ SFPG-Go Performance Test Results ─────────────────────────────────────────╮${NC}"
echo ""

{
  echo "SFPG-Go Performance Test Report"
  echo "Generated: $(date)"
  echo ""
  printf "%-34s  %10s  %9s  %9s  %9s  %10s  %8s  %6s\n" \
    "TEST" "THRUPUT/s" "P50" "P95" "P99" "MAX" "SUCCESS" "ERRORS"
  printf "%-34s  %10s  %9s  %9s  %9s  %10s  %8s  %6s\n" \
    "──────────────────────────────────" "──────────" "─────────" "─────────" "─────────" "──────────" "────────" "──────"

  while IFS= read -r json; do
    [ -f "$json" ] || continue
    name=$(basename "$json" .json)
    # Convert filename to a readable label (strip common prefixes/suffixes)
    lbl=$(echo "$name" | sed \
      -e 's/baseline-//;s/-cache-on/ [on]/;s/-cache-off/ [off]/' \
      -e 's/sustained-//;s/rps/ rps/' \
      -e 's/mixed-workload-/mixed /')
    if read -r rps p50 p95 p99 max success errors < <(parse_json "$json"); then
      printf "%-34s  %8s/s  %7sms  %7sms  %7sms  %8sms  %7s%%  %6s\n" \
        "$lbl" "$rps" "$p50" "$p95" "$p99" "$max" "$success" "$errors"
    fi
  done < <(find "$RESULTS_DIR" -name "*.json" | sort)

  echo ""
  echo "Latencies: milliseconds. THRUPUT: actual req/s delivered. SUCCESS: % returning 2xx."
  echo ""
} | tee "$REPORT_FILE"

echo -e "${BLUE}  Full report: $REPORT_FILE${NC}"
echo ""

# Cache comparison section (if both cache-on and cache-off baselines exist)
compared=0
for scenario in gallery image thumbnail; do
  on_json="$RESULTS_DIR/baseline-${scenario}-cache-on.json"
  off_json="$RESULTS_DIR/baseline-${scenario}-cache-off.json"
  if [ -f "$on_json" ] && [ -f "$off_json" ]; then
    if [ "$compared" -eq 0 ]; then
      echo -e "${CYAN}  Cache speedup (ON vs OFF):${NC}"
      compared=1
    fi
    on_p50=$(jq '.latencies["50th"]' "$on_json")
    off_p50=$(jq '.latencies["50th"]' "$off_json")
    speedup=$(jq -n --argjson a "$off_p50" --argjson b "$on_p50" \
      '($a / $b * 10 | round | . / 10 | tostring) + "x"')
    echo "    $scenario: p50 ${speedup} faster with cache"
  fi
done
[ "$compared" -eq 1 ] && echo ""

echo -e "${GREEN}✓ Done${NC}"
echo ""
