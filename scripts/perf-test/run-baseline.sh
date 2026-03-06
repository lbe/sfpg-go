#!/bin/bash
#
# run-baseline.sh
# Runs baseline performance tests with cache enabled
#
# Usage:
#   ./scripts/perf-test/run-baseline.sh
#
# Runs vegeta attack on individual scenarios:
# - Gallery (GET /gallery/{id})
# - Image (GET /image/{id})
# - Thumbnail (GET /thumbnail/file/{id})
#
# Each test: 30s warm-up + 60s measured load (10→50 req/s)
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
WARMUP_DURATION="30s"
TEST_DURATION="60s"
WARMUP_RATE="10/s"
TEST_RATE="50/s"
WORKERS="8"

# Create required directories
mkdir -p "$RESULTS_DIR"
mkdir -p "${PROJECT_ROOT}/tmp"

test_scenario() {
  local name="$1"
  local attack_file="$2"

  if [ ! -f "$attack_file" ]; then
    echo -e "${RED}✗ Attack file not found: $attack_file${NC}"
    return 1
  fi

  echo -e "${YELLOW}Testing: $name${NC}"

  local result_bin="$RESULTS_DIR/baseline-${name}-cache-on.bin"
  local result_json="$RESULTS_DIR/baseline-${name}-cache-on.json"
  local result_text="$RESULTS_DIR/baseline-${name}-cache-on.txt"

  echo "  Warm-up phase (30s @ 10 req/s)..."
  vegeta attack \
    -duration="$WARMUP_DURATION" \
    -rate="$WARMUP_RATE" \
    -timeout="5s" \
    -workers="$WORKERS" \
    < "$attack_file" \
    | tee "${PROJECT_ROOT}/tmp/warmup.bin" \
    | vegeta report -type=text > /dev/null

  echo "  Measured phase (60s @ 50 req/s)..."
  vegeta attack \
    -duration="$TEST_DURATION" \
    -rate="$TEST_RATE" \
    -timeout="5s" \
    -workers="$WORKERS" \
    < "$attack_file" \
    | tee "$result_bin" \
    | vegeta report -type=text > "$result_text"

  # Generate JSON for analysis
  vegeta report -type=json "$result_bin" > "$result_json"

  # Display summary
  echo "  Results:"
  grep -E "Throughput|Latencies" "$result_text" | sed 's/^/    /'

  echo ""
}

echo -e "${BLUE}╭─ SFPG-Go Baseline Performance Test (Cache Enabled) ─╮${NC}"
echo ""

# Verify server running
echo -e "${YELLOW}Verifying server connection...${NC}"
if ! curl -s "$SERVER_URL/gallery/1" > /dev/null 2>&1; then
  echo -e "${RED}✗ Server not responding at $SERVER_URL${NC}"
  echo "  Please ensure 'air' is running: air"
  exit 1
fi
echo -e "${GREEN}✓ Server is available${NC}"
echo ""

# Verify vegeta installed
if ! command -v vegeta &> /dev/null; then
  echo -e "${RED}✗ vegeta not installed${NC}"
  echo "  Install: go install github.com/tsenart/vegeta@latest"
  exit 1
fi
echo -e "${GREEN}✓ vegeta is available${NC}"
echo ""

# Run tests
echo -e "${YELLOW}Running baseline tests (cache enabled)...${NC}"
echo ""

test_scenario "gallery" "$ATTACKS_DIR/gallery.txt"
test_scenario "image" "$ATTACKS_DIR/image.txt"
test_scenario "thumbnail" "$ATTACKS_DIR/thumbnail.txt"

# Summary
echo -e "${GREEN}✓ Baseline tests complete!${NC}"
echo ""
echo "Results saved to:"
echo "  $RESULTS_DIR/baseline-*.bin  (binary results)"
echo "  $RESULTS_DIR/baseline-*.json (JSON metrics)"
echo "  $RESULTS_DIR/baseline-*.txt  (text reports)"
echo ""
echo "View detailed report:"
echo "  vegeta plot $RESULTS_DIR/baseline-gallery-cache-on.bin > $RESULTS_DIR/gallery.html"
echo ""
