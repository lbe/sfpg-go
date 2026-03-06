#!/bin/bash
#
# gen-attack-files.sh
# Queries the live database for real IDs and generates vegeta attack files.
#
# Usage:
#   ./scripts/perf-test/gen-attack-files.sh
#
# Respects environment variables:
#   DB_PATH       Override database path (default: ./tmp/DB/sfpg.db)
#   SERVER_URL    Override server URL (default: http://localhost:8083)
#   GALLERY_COUNT Max number of gallery (folder) URLs (default: 30)
#   IMAGE_COUNT   Max number of image URLs (default: 50)
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DB_PATH="${DB_PATH:-${PROJECT_ROOT}/tmp/DB/sfpg.db}"
ATTACKS_DIR="$SCRIPT_DIR/attacks"
SERVER_URL="${SERVER_URL:-http://localhost:8083}"
GALLERY_COUNT="${GALLERY_COUNT:-30}"
IMAGE_COUNT="${IMAGE_COUNT:-50}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${YELLOW}→ Generating vegeta attack files from database${NC}"
echo "  DB:     $DB_PATH"
echo "  Server: $SERVER_URL"
echo "  Output: $ATTACKS_DIR/"
echo ""

# Verify sqlite3 available
if ! command -v sqlite3 &> /dev/null; then
  echo -e "${RED}✗ sqlite3 not found.${NC}"
  exit 1
fi

# Verify DB exists
if [ ! -f "$DB_PATH" ]; then
  echo -e "${RED}✗ Database not found: $DB_PATH${NC}"
  echo "  Run the dev server (air) at least once to initialize the database."
  exit 1
fi

mkdir -p "$ATTACKS_DIR"

# Query real IDs
folder_ids=$(sqlite3 "$DB_PATH" "SELECT id FROM folders ORDER BY id LIMIT ${GALLERY_COUNT};")
file_ids=$(sqlite3 "$DB_PATH" "SELECT id FROM files ORDER BY id LIMIT ${IMAGE_COUNT};")

if [ -z "$folder_ids" ]; then
  echo -e "${RED}✗ No folders found in database. Has file discovery run?${NC}"
  exit 1
fi

if [ -z "$file_ids" ]; then
  echo -e "${RED}✗ No files found in database. Has file discovery run?${NC}"
  exit 1
fi

folder_count=$(echo "$folder_ids" | wc -l | tr -d ' ')
file_count=$(echo "$file_ids" | wc -l | tr -d ' ')
echo "  Found: $folder_count folders, $file_count files in DB"
echo ""

# gallery.txt — GET /gallery/{folder_id}
{
  while IFS= read -r id; do
    [[ -n "$id" ]] && echo "GET $SERVER_URL/gallery/$id"
  done <<< "$folder_ids"
} > "$ATTACKS_DIR/gallery.txt"
echo -e "${GREEN}  ✓ gallery.txt     ($folder_count URLs)${NC}"

# image.txt — GET /image/{file_id}
{
  while IFS= read -r id; do
    [[ -n "$id" ]] && echo "GET $SERVER_URL/image/$id"
  done <<< "$file_ids"
} > "$ATTACKS_DIR/image.txt"
echo -e "${GREEN}  ✓ image.txt       ($file_count URLs)${NC}"

# thumbnail.txt — GET /thumbnail/file/{file_id}
{
  while IFS= read -r id; do
    [[ -n "$id" ]] && echo "GET $SERVER_URL/thumbnail/file/$id"
  done <<< "$file_ids"
} > "$ATTACKS_DIR/thumbnail.txt"
echo -e "${GREEN}  ✓ thumbnail.txt   ($file_count URLs)${NC}"

# lightbox.txt — GET /lightbox/{file_id} (HTMX partial, cacheable)
lightbox_ids=$(sqlite3 "$DB_PATH" "SELECT id FROM files ORDER BY id LIMIT 20;")
{
  while IFS= read -r id; do
    [[ -n "$id" ]] && echo "GET $SERVER_URL/lightbox/$id"
  done <<< "$lightbox_ids"
} > "$ATTACKS_DIR/lightbox.txt"
lightbox_count=$(echo "$lightbox_ids" | wc -l | tr -d ' ')
echo -e "${GREEN}  ✓ lightbox.txt    ($lightbox_count URLs)${NC}"

# mixed-workload.txt — 60% gallery, 20% image, 10% thumbnail, 10% lightbox
# Use first N folder/file IDs scaled to the proportions
{
  # 60% gallery — all folder IDs up to GALLERY_COUNT
  while IFS= read -r id; do
    [[ -n "$id" ]] && echo "GET $SERVER_URL/gallery/$id"
  done <<< "$folder_ids"

  # 20% image — folder_count * 20/60 file IDs
  img_mix=$((folder_count * 20 / 60))
  ((img_mix < 1)) && img_mix=1
  sqlite3 "$DB_PATH" "SELECT id FROM files ORDER BY id LIMIT ${img_mix};" \
    | while IFS= read -r id; do
      [[ -n "$id" ]] && echo "GET $SERVER_URL/image/$id"
    done

  # 10% thumbnail
  thumb_mix=$((folder_count * 10 / 60))
  ((thumb_mix < 1)) && thumb_mix=1
  sqlite3 "$DB_PATH" "SELECT id FROM files ORDER BY id LIMIT ${thumb_mix};" \
    | while IFS= read -r id; do
      [[ -n "$id" ]] && echo "GET $SERVER_URL/thumbnail/file/$id"
    done

  # 10% lightbox
  sqlite3 "$DB_PATH" "SELECT id FROM files ORDER BY id LIMIT ${thumb_mix};" \
    | while IFS= read -r id; do
      [[ -n "$id" ]] && echo "GET $SERVER_URL/lightbox/$id"
    done
} > "$ATTACKS_DIR/mixed-workload.txt"

mixed_count=$(wc -l < "$ATTACKS_DIR/mixed-workload.txt" | tr -d ' ')
echo -e "${GREEN}  ✓ mixed-workload.txt ($mixed_count URLs, ~60/20/10/10 distribution)${NC}"

echo ""
echo -e "${GREEN}✓ Attack files ready in $ATTACKS_DIR/${NC}"
