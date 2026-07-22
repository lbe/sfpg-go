#!/bin/bash
set -euo pipefail

DB_PATH="${DB_PATH:-$HOME/tmp/gallery/DB/sfpg.db}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$REPO_ROOT/internal/cachelite/bodycodec/testdata"
MANIFEST="$OUT_DIR/MANIFEST.md"

mkdir -p "$OUT_DIR"

if [[ ! -f "$DB_PATH" ]]; then
  echo "error: database not found: $DB_PATH" >&2
  exit 1
fi

query_ids() {
  sqlite3 -cmd '.timer off' -noheader "$DB_PATH" "$1"
}

SMALL_IDS=$(query_ids "SELECT id FROM http_cache WHERE content_type LIKE 'text/html%' AND content_length IS NOT NULL ORDER BY ABS(content_length - 3072) ASC, id ASC LIMIT 3;")
SMALL_IN=$(echo "$SMALL_IDS" | paste -sd, -)

MED_IDS=$(query_ids "SELECT id FROM http_cache WHERE content_type LIKE 'text/html%' AND content_length IS NOT NULL AND id NOT IN (${SMALL_IN}) ORDER BY ABS(content_length - 153600) ASC, id ASC LIMIT 3;")
MED_IN=$(echo "$MED_IDS" | paste -sd, -)
USED_IN="${SMALL_IN},${MED_IN}"

LARGE_IDS=$(query_ids "SELECT id FROM http_cache WHERE content_type LIKE 'text/html%' AND content_length IS NOT NULL AND id NOT IN (${USED_IN}) ORDER BY content_length DESC, id ASC LIMIT 3;")

export_body() {
  local id="$1" out="$2"
  # writefile preserves BLOB bytes; SELECT body >file can corrupt binary/NUL
  sqlite3 -cmd '.timer off' -noheader -batch "$DB_PATH" "SELECT writefile('${out}', body) FROM http_cache WHERE id=${id};" > /dev/null
}

manifest_append() {
  local fname="$1" id="$2"
  local key path clen slen
  IFS='|' read -r key path clen slen < <(
    sqlite3 -cmd '.timer off' -noheader -separator '|' "$DB_PATH" \
      "SELECT IFNULL(replace(key,'|','/'),''), IFNULL(replace(path,'|','/'),''), content_length, LENGTH(body) FROM http_cache WHERE id=${id};"
  )
  echo "| ${fname} | ${id} | ${key} | ${path} | ${clen} | ${slen} |" >> "$MANIFEST"
}

emit_band() {
  local band="$1" expected="$2"
  shift 2
  local i=1 n=0
  for id in "$@"; do
    [[ -n "$id" ]] || continue
    n=$((n + 1))
    local fname="gallery_${band}_${i}.html"
    export_body "$id" "$OUT_DIR/${fname}"
    manifest_append "$fname" "$id"
    i=$((i + 1))
  done
  if [[ "$n" -ne "$expected" ]]; then
    echo "error: band ${band}: need ${expected} rows, got ${n}" >&2
    exit 1
  fi
}

{
  echo '# bodycodec testdata manifest'
  echo
  echo '| file | id | key | path | content_length | stored_length |'
  echo '| ---- | -- | --- | ---- | -------------- | ------------- |'
} > "$MANIFEST"

emit_band small 3 $(echo "$SMALL_IDS" | tr '\n' ' ')
emit_band med 3 $(echo "$MED_IDS" | tr '\n' ' ')
emit_band large 3 $(echo "$LARGE_IDS" | tr '\n' ' ')

ARCHIVE="$OUT_DIR/galleries.tar.gz"
rm -f "$ARCHIVE"
tar -czf "$ARCHIVE" -C "$OUT_DIR" \
  gallery_small_1.html gallery_small_2.html gallery_small_3.html \
  gallery_med_1.html gallery_med_2.html gallery_med_3.html \
  gallery_large_1.html gallery_large_2.html gallery_large_3.html
rm -f "$OUT_DIR/.galleries-extracted"

echo "Wrote 9 fixtures and $ARCHIVE"
