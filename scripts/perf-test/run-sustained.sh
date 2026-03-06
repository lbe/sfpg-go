#!/bin/bash
#
# run-sustained.sh
# Runs sustained load tests at multiple request rates
#
# Usage:
#   ./scripts/perf-test/run-sustained.sh
#
# Tests each scenario at multiple load levels:
# - 50 req/s (moderate)
# - 100 req/s (high)
# - 200 req/s (stress)
#
# Each test: 120 seconds sustained load
#

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Configuration
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RESULTS_DIR="${PROJECT_ROOT}/results"
ATTACKS_DIR="$SCRIPT_DIR/attacks"
SERVER_URL="http://localhost:8083"

# Vegeta configuration
TEST_DURATION="120s"
RATES=("50/s" "100/s" "200/s")
WORKERS="16"

# Create results directory
mkdir -p "$RESULTS_DIR"

test_scenario_at_rate() {
  local name="$1"
  local attack_file="$2"
  local rate="$3"

  if [ ! -f "$attack_file" ]; then
    echo -e "${RED}✗ Attack file not found: $attack_file${NC}"
    return 1
  fi

  local rate_label="${rate%/*}"
  echo -e "${YELLOW}Testing: $name @ $rate_label req/s${NC}"

  local result_bin="$RESULTS_DIR/sustained-${name}-${rate_label}rps.bin"
  local result_json="$RESULTS_DIR/sustained-${name}-${rate_label}rps.json"
  local result_text="$RESULTS_DIR/sustained-${name}-${rate_label}rps.txt"

  echo "  Running for 120s..."
  vegeta attack \
    -duration="$TEST_DURATION" \
    -rate="$rate" \
    -timeout="5s" \
    -workers="$WORKERS" \
    < "$attack_file" \
    | tee "$result_bin" \
    | vegeta report -type=text > "$result_text"

  # Generate JSON for analysis
  vegeta report -type=json "$result_bin" > "$result_json"

  # Display summary
  echo "  Results:"
  grep -E "Requests|Latencies" "$result_text" | sed 's/^/    /'

  echo ""
}

echo -e "${BLUE}╭─ SFPG-Go Sustained Load Test (Cache Enabled) ─╮${NC}"
echo ""

# Verify server running
echo -e "${YELLOW}Verifying server connection...${NC}"
if ! curl -s "$SERVER_URL/gallery/1" > /dev/null 2>&1; then
  echo -e "${RED}✗ Server not responding at $SERVER_URL${NC}"
  exit 1
fi
echo -e "${GREEN}✓ Server is available${NC}"
echo ""

# Verify vegeta installed
if ! command -v vegeta &> /dev/null; then
  echo -e "${RED}✗ vegeta not installed${NC}"
  exit 1
fi
echo ""

# Run tests
echo -e "${YELLOW}Running sustained load tests (cache enabled)...${NC}"
echo ""

for scenario in "gallery" "image" "thumbnail"; do
  attack_file="$ATTACKS_DIR/${scenario}.txt"
  [ -f "$attack_file" ] || continue

  for rate in "${RATES[@]}"; do
    test_scenario_at_rate "$scenario" "$attack_file" "$rate"
  done
done

# Summary
echo -e "${GREEN}✓ Sustained load tests complete!${NC}"
echo ""
echo "Results saved to:"
echo "  $RESULTS_DIR/sustained-*.bin   (binary results)"
echo "  $RESULTS_DIR/sustained-*.json  (JSON metrics)"
echo "  $RESULTS_DIR/sustained-*.txt   (text reports)"
echo ""
echo "Analyze results:"
echo "  ./scripts/perf-test/analyze-results.sh"
echo ""
