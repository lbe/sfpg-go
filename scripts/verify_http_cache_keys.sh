#!/usr/bin/env bash
# Warm-check every cacheable route (full + HTMX request styles) and validate
# http_cache key shape after v3 normalization. See scripts/preload_curl_test.md.
set -euo pipefail

DB="${SFPG_DB:-/home/whgi/src2/sfpg-go/tmp/DB/sfpg.db}"
BASE="${SFPG_BASE:-http://localhost:8083}"
CHUNK_SIZE="${CHUNK_SIZE:-50}"

# ~/.sqliterc may set .timer on; that prints "Run Time: ..." to stdout and breaks $(sqlite3 ...).
sqlite3_q() {
  sqlite3 -cmd '.timer off' "$DB" "$@"
}

if [[ ! -f "$DB" ]]; then
  echo "ERROR: database not found at $DB" >&2
  echo "Set SFPG_DB to the correct path." >&2
  exit 1
fi

V=$(sqlite3_q "SELECT value FROM config WHERE key='etag_version' LIMIT 1")
if [[ -z "$V" ]]; then
  echo "ERROR: etag_version not found in config table" >&2
  exit 1
fi

echo "ETag version: $V"
echo "Base URL:     $BASE"
echo "---"

tmpdir=$(mktemp -d)
jobs_file=$(mktemp)
trap 'rm -rf "$tmpdir"; rm -f "$jobs_file"' EXIT

while read -r path; do
  url="${BASE}${path}?v=${V}"

  printf '%s\t%s\t\n' "${path} (full)" "$url" >> "$jobs_file"

  case "$path" in
    /gallery/*) tgt=gallery-content ;;
    /lightbox/*) tgt=lightbox-ui ;;
    /info/*) tgt=box_info ;;
    *) tgt="" ;;
  esac

  if [[ -n "$tgt" ]]; then
    printf '%s\t%s\t%s\n' "${path} (HTMX→${tgt})" "$url" "$tgt" >> "$jobs_file"
  fi
done < <(sqlite3_q "
SELECT '/gallery/'||id FROM folder_view
UNION ALL SELECT '/info/folder/'||id FROM folder_view
UNION ALL SELECT '/info/image/'||id FROM file_view
UNION ALL SELECT '/lightbox/'||id FROM file_view;
")

run_chunk() {
  local -n _labels=$1
  local -n _urls=$2
  local -n _tgts=$3
  local n=${#_labels[@]}
  local chunk_dir="$tmpdir/chunk_$$_${RANDOM}"
  local -a curl_args=(
    --silent --show-error
    --parallel --parallel-max "$CHUNK_SIZE"
    --connect-timeout 5 --max-time 30
  )
  local k

  mkdir -p "$chunk_dir"

  for ((k = 0; k < n; k++)); do
    if ((k > 0)); then
      curl_args+=(--next)
    fi
    if [[ -n "${_tgts[$k]}" ]]; then
      curl_args+=(-H "HX-Request: true" -H "HX-Target: ${_tgts[$k]}")
    fi
    curl_args+=(
      -o /dev/null
      -D "$chunk_dir/${k}.hdr"
      -w "${k} %{http_code}\n"
      "${_urls[$k]}"
    )
  done

  local curl_out curl_exit=0
  curl_out=$(curl "${curl_args[@]}") || curl_exit=$?

  if [[ $curl_exit -ne 0 ]]; then
    echo "  FATAL: curl exited $curl_exit — aborting" >&2
    rm -rf "$chunk_dir"
    exit 1
  fi

  declare -A http_codes=()
  while read -r idx http_code; do
    [[ -n "$idx" ]] && http_codes[$idx]=$http_code
  done <<< "$curl_out"

  for ((k = 0; k < n; k++)); do
    local http_code="${http_codes[$k]:-}"
    local cache_line

    if [[ -z "$http_code" ]]; then
      echo "  FATAL: curl exited $curl_exit — aborting" >&2
      rm -rf "$chunk_dir"
      exit 1
    fi

    cache_line=$(grep -i '^x-cache:' "$chunk_dir/${k}.hdr" || true)

    if [[ "$http_code" != "200" ]]; then
      echo "  FATAL: HTTP $http_code — aborting" >&2
      rm -rf "$chunk_dir"
      exit 1
    fi

    echo "  ${_labels[$k]} → $http_code | ${cache_line:-no X-Cache header}"
  done

  rm -rf "$chunk_dir"
}

labels=()
urls=()
tgts=()

while IFS=$'\t' read -r label url tgt; do
  labels+=("$label")
  urls+=("$url")
  tgts+=("$tgt")

  if ((${#labels[@]} >= CHUNK_SIZE)); then
    run_chunk labels urls tgts
    labels=()
    urls=()
    tgts=()
  fi
done < "$jobs_file"

if ((${#labels[@]} > 0)); then
  run_chunk labels urls tgts
fi

echo "---"
echo "=== Cache Key Summary ==="

# Dynamic expected entry count: gallery has 2 variants, info/lightbox have 1
expected_entries=$(sqlite3_q "
SELECT (SELECT COUNT(*) FROM folders) * 3 + (SELECT COUNT(*) FROM files) * 2;
")
actual_entries=$(sqlite3_q "SELECT COUNT(*) FROM http_cache;")
echo "Expected: $expected_entries (folders*3 + files*2)"
echo "Actual:   $actual_entries"

exit_code=0
if [[ "$actual_entries" -ne "$expected_entries" ]]; then
  echo "FAIL: cache entry count mismatch"
  exit_code=1
fi

echo ""
echo "Keys-per-path distribution:"
sqlite3_q -header -column "
SELECT keys_per_path, COUNT(*) AS path_count FROM (
  SELECT path, COUNT(*) AS keys_per_path FROM http_cache GROUP BY path
) GROUP BY keys_per_path ORDER BY keys_per_path;
"

# Legacy key component check
legacy_count=$(sqlite3_q "
SELECT COUNT(*) FROM http_cache
WHERE key LIKE '%|HX=%' OR key LIKE '%|HXTarget=%' OR key LIKE '%|IsVariant=%';
")
if [[ "$legacy_count" -ne 0 ]]; then
  echo "FAIL: $legacy_count rows with legacy key components (|HX=, |HXTarget=, |IsVariant=)"
  exit_code=1
else
  echo "OK: zero legacy key components found"
fi

# Paths with >1 key outside /gallery/
non_gallery_multi=$(sqlite3_q "
SELECT COUNT(*) FROM (
  SELECT path FROM http_cache
  WHERE path NOT LIKE '/gallery/%'
  GROUP BY path
  HAVING COUNT(*) > 1
);")
if [[ "$non_gallery_multi" -ne 0 ]]; then
  echo "FAIL: $non_gallery_multi non-gallery paths with >1 key"
  exit_code=1
else
  echo "OK: zero non-gallery paths with >1 key"
fi

exit $exit_code
