#!/bin/bash
#
# setup-test-data.sh
# Generates vegeta attack files from real database IDs and warms the HTTP cache.
#
# Usage:
#   ./scripts/perf-test/setup-test-data.sh
#
# Prerequisites:
#   - Air dev server running on localhost:8083 (with data already discovered)
#   - sqlite3 available
#   - vegeta available (go install github.com/tsenart/vegeta@latest)
#

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DB_PATH="${DB_PATH:-${PROJECT_ROOT}/tmp/DB/sfpg.db}"
RESULTS_DIR="${PROJECT_ROOT}/results"
SERVER_URL="${SERVER_URL:-http://localhost:8083}"

mkdir -p "$RESULTS_DIR"

echo -e "${YELLOW}→ Performance test setup${NC}"
echo ""

# Step 1: Verify server is running
echo -e "${YELLOW}[1/3]${NC} Checking server..."
if ! curl -sf "$SERVER_URL/health" > /dev/null 2>&1; then
  echo -e "${RED}✗ Server not responding at $SERVER_URL${NC}"
  echo "  Start with: air"
  exit 1
fi
echo -e "${GREEN}✓ Server is running${NC}"
echo ""

# Step 2: Verify vegeta installed
echo -e "${YELLOW}[2/3]${NC} Checking tools..."
if ! command -v vegeta &> /dev/null; then
  echo -e "${RED}✗ vegeta not installed${NC}"
  echo "  Install: go install github.com/tsenart/vegeta@latest"
  exit 1
fi
if ! command -v sqlite3 &> /dev/null; then
  echo -e "${RED}✗ sqlite3 not found${NC}"
  exit 1
fi
echo -e "${GREEN}✓ vegeta and sqlite3 available${NC}"
echo ""

# Step 3: Generate attack files from real DB IDs, then warm the cache
echo -e "${YELLOW}[3/3]${NC} Generating attack files..."
"$SCRIPT_DIR/gen-attack-files.sh"
echo ""

echo -e "${YELLOW}→ Warming up HTTP cache...${NC}"
echo "  Requesting each URL once to seed the cache..."

# Use the generated attack files to warm the cache in a single pass
# We don't care about the output, just that the server processes and caches the responses.
if [ -f "$SCRIPT_DIR/attacks/gallery.txt" ]; then
  vegeta attack -duration=1s -rate=0 -timeout=10s \
    < "$SCRIPT_DIR/attacks/gallery.txt" \
    | vegeta report > /dev/null 2>&1 || true
fi

if [ -f "$SCRIPT_DIR/attacks/image.txt" ]; then
  vegeta attack -duration=1s -rate=0 -timeout=10s \
    < "$SCRIPT_DIR/attacks/image.txt" \
    | vegeta report > /dev/null 2>&1 || true
fi

if [ -f "$SCRIPT_DIR/attacks/thumbnail.txt" ]; then
  vegeta attack -duration=1s -rate=0 -timeout=10s \
    < "$SCRIPT_DIR/attacks/thumbnail.txt" \
    | vegeta report > /dev/null 2>&1 || true
fi

if [ -f "$SCRIPT_DIR/attacks/lightbox.txt" ]; then
  vegeta attack -duration=1s -rate=0 -timeout=10s \
    < "$SCRIPT_DIR/attacks/lightbox.txt" \
    | vegeta report > /dev/null 2>&1 || true
fi

echo -e "${GREEN}✓ Cache warmed up${NC}"
echo ""

echo -e "${GREEN}✓ Setup complete. Ready to run performance tests.${NC}"
echo ""
echo "  make perf-test                  # Run all tests"
echo "  ./scripts/perf-test/run-baseline.sh"
echo "  ./scripts/perf-test/run-sustained.sh"
echo "  ./scripts/perf-test/run-mixed-workload.sh"
echo "  ./scripts/perf-test/analyze-results.sh"
echo ""
