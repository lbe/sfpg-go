#!/bin/bash
#
# run-mixed-workload.sh
# Runs realistic mixed workload test
#
# Usage:
#   ./scripts/perf-test/run-mixed-workload.sh
#
# Request distribution (realistic):
# - 60% GET /gallery/{id}
# - 20% GET /image/{id}
# - 10% GET /thumbnail/file/{id}
# - 10% GET /lightbox/{id}
#
# Load ramp: 10 → 50 → 100 req/s (3 stages of 30s each)
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
TEST_DURATION="90s" # 30s at each rate: 10, 50, 100 req/s
WORKERS="12"

# Create required directories
mkdir -p "$RESULTS_DIR"
mkdir -p "${PROJECT_ROOT}/tmp"

echo -e "${BLUE}╭─ SFPG-Go Mixed Workload Test (Cache Enabled) ─╮${NC}"
echo ""

# Verify server running
echo -e "${YELLOW}Verifying server connection...${NC}"
if ! curl -sf "$SERVER_URL/health" > /dev/null 2>&1; then
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

# Use the pre-generated mixed workload attack file (from gen-attack-files.sh)
mixed_attack_file="$ATTACKS_DIR/mixed-workload.txt"

if [ ! -f "$mixed_attack_file" ]; then
  echo -e "${RED}✗ Attack file not found: $mixed_attack_file${NC}"
  echo "  Run: make perf-test-setup"
  exit 1
fi

url_count=$(wc -l < "$mixed_attack_file" | tr -d ' ')
echo -e "${GREEN}✓ Using $mixed_attack_file ($url_count URLs)${NC}"
echo ""

# Run tests
echo -e "${YELLOW}Running mixed workload test (90s total)...${NC}"
echo ""

echo -e "${YELLOW}[1/3] Warm-up (10 req/s for 30s)${NC}"
vegeta attack \
  -duration=30s \
  -rate=10/s \
  -timeout=5s \
  -workers="$WORKERS" \
  < "$mixed_attack_file" \
  | vegeta report -type=text | head -20
echo ""

echo -e "${YELLOW}[2/3] Light load (50 req/s for 30s)${NC}"
vegeta attack \
  -duration=30s \
  -rate=50/s \
  -timeout=5s \
  -workers="$WORKERS" \
  < "$mixed_attack_file" \
  | tee "${PROJECT_ROOT}/tmp/mixed-light.bin" \
  | vegeta report -type=text | head -20
echo ""

echo -e "${YELLOW}[3/3] High load (100 req/s for 30s)${NC}"
vegeta attack \
  -duration=30s \
  -rate=100/s \
  -timeout=5s \
  -workers="$WORKERS" \
  < "$mixed_attack_file" \
  | tee "$RESULTS_DIR/mixed-workload-high.bin" \
  | vegeta report -type=text > "$RESULTS_DIR/mixed-workload-high.txt"

echo "  Results (100 req/s):"
grep -E "Requests|Latencies|Errors" "$RESULTS_DIR/mixed-workload-high.txt" | sed 's/^/    /'
echo ""

# Generate analysis
vegeta report -type=json "$RESULTS_DIR/mixed-workload-high.bin" > "$RESULTS_DIR/mixed-workload-high.json"

# Summary
echo -e "${GREEN}✓ Mixed workload test complete!${NC}"
echo ""
echo "Results saved to:"
echo "  $RESULTS_DIR/mixed-workload-high.bin  (binary results)"
echo "  $RESULTS_DIR/mixed-workload-high.json (JSON metrics)"
echo "  $RESULTS_DIR/mixed-workload-high.txt  (text report)"
echo ""
