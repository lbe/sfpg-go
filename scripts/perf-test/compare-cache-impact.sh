#!/bin/bash
#
# compare-cache-impact.sh
# Compares performance with HTTP cache ON vs OFF.
#
# Usage:
#   ./scripts/perf-test/compare-cache-impact.sh
#
# Workflow:
#   1. Runs baseline tests with cache ON  → results/baseline-*-cache-on.*
#   2. Prompts user to disable HTTP cache via the web UI
#   3. Runs baseline tests with cache OFF → results/baseline-*-cache-off.*
#   4. Prints a side-by-side comparison table
#
# Prerequisites:
#   - vegeta installed
#   - Dev server running on localhost:8083
#   - Attack files generated: make perf-test-setup
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RESULTS_DIR="${PROJECT_ROOT}/results"
ATTACKS_DIR="$SCRIPT_DIR/attacks"
SERVER_URL="${SERVER_URL:-http://localhost:8083}"

WARMUP_DURATION="20s"
TEST_DURATION="60s"
WARMUP_RATE="10/s"
TEST_RATE="50/s"
WORKERS="8"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

mkdir -p "$RESULTS_DIR"

# Verify tools
for cmd in vegeta jq curl; do
  if ! command -v "$cmd" &> /dev/null; then
    echo -e "${RED}✗ Required tool not found: $cmd${NC}"
    exit 1
  fi
done

# Verify attack files exist
for f in gallery image thumbnail; do
  if [ ! -f "$ATTACKS_DIR/${f}.txt" ]; then
    echo -e "${RED}✗ Attack file missing: $ATTACKS_DIR/${f}.txt${NC}"
    echo "  Run: make perf-test-setup"
    exit 1
  fi
done

# Verify server running
if ! curl -sf "$SERVER_URL/health" > /dev/null 2>&1; then
  echo -e "${RED}✗ Server not responding at $SERVER_URL${NC}"
  exit 1
fi

run_tests() {
  local label="$1"  # "cache-on" or "cache-off"
  local suffix="$2" # display label

  echo -e "${CYAN}Running tests: $suffix${NC}"
  echo ""

  for scenario in gallery image thumbnail; do
    local attack_file="$ATTACKS_DIR/${scenario}.txt"
    local result_bin="$RESULTS_DIR/baseline-${scenario}-${label}.bin"
    local result_json="$RESULTS_DIR/baseline-${scenario}-${label}.json"
    local result_txt="$RESULTS_DIR/baseline-${scenario}-${label}.txt"

    echo -e "  ${YELLOW}→ ${scenario} (${suffix})${NC}"

    echo "    Warm-up (${WARMUP_DURATION} @ ${WARMUP_RATE})..."
    vegeta attack \
      -duration="$WARMUP_DURATION" \
      -rate="$WARMUP_RATE" \
      -timeout="5s" \
      -workers="$WORKERS" \
      < "$attack_file" \
      | vegeta report > /dev/null

    echo "    Test (${TEST_DURATION} @ ${TEST_RATE})..."
    vegeta attack \
      -duration="$TEST_DURATION" \
      -rate="$TEST_RATE" \
      -timeout="5s" \
      -workers="$WORKERS" \
      < "$attack_file" \
      | tee "$result_bin" \
      | vegeta report -type=text > "$result_txt"

    vegeta report -type=json "$result_bin" > "$result_json"

    local rps p50 p95 success
    rps=$(jq -r '.throughput | . * 100 | round | . / 100 | tostring' "$result_json")
    p50=$(jq -r '(.latencies["50th"] / 1e6 * 100 | round | . / 100 | tostring) + "ms"' "$result_json")
    p95=$(jq -r '(.latencies["95th"] / 1e6 * 100 | round | . / 100 | tostring) + "ms"' "$result_json")
    success=$(jq -r '(.success * 100 | . * 100 | round | . / 100 | tostring) + "%"' "$result_json")
    echo -e "    ${GREEN}✓ throughput=${rps}/s  p50=${p50}  p95=${p95}  success=${success}${NC}"
    echo ""
  done
}

print_comparison() {
  echo ""
  echo -e "${BLUE}╔══════════════════════════════════════════════════════════════════════════════╗${NC}"
  echo -e "${BLUE}║                     Cache Impact Comparison (50 req/s)                      ║${NC}"
  echo -e "${BLUE}╚══════════════════════════════════════════════════════════════════════════════╝${NC}"
  echo ""
  printf "%-12s  %-10s  %-12s  %-10s  %-10s  %-10s  %-10s\n" \
    "SCENARIO" "CACHE" "THROUGHPUT" "P50" "P95" "P99" "SUCCESS"
  printf "%-12s  %-10s  %-12s  %-10s  %-10s  %-10s  %-10s\n" \
    "────────────" "──────────" "────────────" "──────────" "──────────" "──────────" "──────────"

  for scenario in gallery image thumbnail; do
    for label in cache-on cache-off; do
      local json="$RESULTS_DIR/baseline-${scenario}-${label}.json"
      if [ -f "$json" ]; then
        local rps p50 p95 p99 success cache_label
        rps=$(jq -r '(.throughput * 100 | round | . / 100 | tostring) + "/s"' "$json")
        p50=$(jq -r '(.latencies["50th"] / 1e6 * 100 | round | . / 100 | tostring) + "ms"' "$json")
        p95=$(jq -r '(.latencies["95th"] / 1e6 * 100 | round | . / 100 | tostring) + "ms"' "$json")
        p99=$(jq -r '(.latencies["99th"] / 1e6 * 100 | round | . / 100 | tostring) + "ms"' "$json")
        success=$(jq -r '(.success * 100 | . * 100 | round | . / 100 | tostring) + "%"' "$json")
        cache_label="${label/cache-/}"
        printf "%-12s  %-10s  %-12s  %-10s  %-10s  %-10s  %-10s\n" \
          "$scenario" "$cache_label" "$rps" "$p50" "$p95" "$p99" "$success"
      else
        printf "%-12s  %-10s  %-12s\n" "$scenario" "${label/cache-/}" "(no results)"
      fi
    done
    echo ""
  done

  # Speedup summary
  echo -e "${CYAN}  Speedup (cache ON vs OFF)${NC}"
  echo ""
  for scenario in gallery image thumbnail; do
    local on_json="$RESULTS_DIR/baseline-${scenario}-cache-on.json"
    local off_json="$RESULTS_DIR/baseline-${scenario}-cache-off.json"
    if [ -f "$on_json" ] && [ -f "$off_json" ]; then
      local on_p50 off_p50 speedup
      on_p50=$(jq -r '.latencies["50th"]' "$on_json")
      off_p50=$(jq -r '.latencies["50th"]' "$off_json")
      speedup=$(jq -n --argjson a "$off_p50" --argjson b "$on_p50" \
        '($a / $b * 100 | round | . / 100 | tostring) + "x"')
      echo "  $scenario: p50 speedup = ${speedup} faster with cache"
    fi
  done
  echo ""
}

# ─── Main ───────────────────────────────────────────────────────────────────

echo -e "${BLUE}╭─ SFPG-Go Cache Impact Comparison ─╮${NC}"
echo ""

# Phase 1: Cache ON
echo -e "${YELLOW}Phase 1: Tests with HTTP cache ON${NC}"
echo ""
run_tests "cache-on" "cache ON"

# Phase 2: Prompt user to disable cache
echo ""
echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo -e "  Phase 2 requires the HTTP cache to be ${RED}DISABLED${NC}."
echo ""
echo "  To disable via the web UI:"
echo "    1. Open $SERVER_URL in your browser"
echo "    2. Log in and open the Configuration modal"
echo "    3. Under 'Cache', uncheck 'Enable HTTP Cache'"
echo "    4. Save configuration"
echo ""
echo -e "  ${CYAN}Press ENTER when the HTTP cache is DISABLED to continue...${NC}"
read -r

echo ""
echo -e "${YELLOW}Phase 2: Tests with HTTP cache OFF${NC}"
echo ""
run_tests "cache-off" "cache OFF"

# Phase 3: Prompt to re-enable cache
echo ""
echo -e "${YELLOW}━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━${NC}"
echo ""
echo -e "  Please ${GREEN}re-enable${NC} the HTTP cache via the Configuration modal."
echo -e "  ${CYAN}Press ENTER when done...${NC}"
read -r

echo ""

# Print comparison
print_comparison

echo -e "${GREEN}✓ Comparison complete. Results saved to $RESULTS_DIR/baseline-*-cache-{on,off}.*${NC}"
echo ""
