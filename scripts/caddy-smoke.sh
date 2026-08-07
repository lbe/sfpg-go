#!/usr/bin/env bash
# Smoke-test sfpg-go behind local Caddy (edge contract).
# Prerequisites: Caddy running with deploy/Caddyfile.local; Go backend up.
#
# Usage:
#   ./scripts/caddy-smoke.sh
#   BASE_URL=https://localhost:8443 ./scripts/caddy-smoke.sh
#
# Uses curl -k because tls internal is not in the system trust store.

set -euo pipefail

BASE_URL="${BASE_URL:-https://localhost:8443}"
COOKIE_JAR="${COOKIE_JAR:-./tmp/caddy-smoke/cookies.txt}"
ORIGIN="${ORIGIN:-$BASE_URL}"
PASS=0
FAIL=0

mkdir -p "$(dirname "$COOKIE_JAR")"
rm -f "$COOKIE_JAR"

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
info() { printf '  %s\n' "$*"; }

check() {
  local name="$1"
  shift
  if "$@"; then
    green "PASS: $name"
    PASS=$((PASS + 1))
  else
    red "FAIL: $name"
    FAIL=$((FAIL + 1))
  fi
}

hdr_file="$(mktemp)"
body_file="$(mktemp)"
trap 'rm -f "$hdr_file" "$body_file"' EXIT

echo "Caddy smoke against $BASE_URL"
echo

# 1. Gallery loads
code="$(curl -sk -o /dev/null -w '%{http_code}' "$BASE_URL/gallery/1")"
check "GET /gallery/1 → 200 (got $code)" test "$code" = "200"

# 2. HSTS
curl -sk -D "$hdr_file" -o /dev/null "$BASE_URL/gallery/1"
check "Strict-Transport-Security present" \
  grep -qi 'strict-transport-security:.*max-age=' "$hdr_file"

# 3. HTML wire compression from Caddy
curl -sk -D "$hdr_file" -o "$body_file" \
  -H 'Accept-Encoding: gzip' \
  "$BASE_URL/gallery/1"
enc="$(awk -F': ' 'tolower($1)=="content-encoding"{print tolower($2)}' "$hdr_file" | tr -d '\r' | head -1)"
check "HTML Content-Encoding is gzip or zstd (got '${enc:-none}')" \
  bash -c "[[ \"$enc\" == gzip || \"$enc\" == zstd ]]"
# Body should decode / look like HTML when gzip
if [[ "$enc" == gzip ]]; then
  check "gzip HTML body contains DOCTYPE/html" \
    bash -c "gzip -dc '$body_file' 2>/dev/null | tr -d '\\n\\r\\t ' | head -c 20 | grep -qiE '^<!DOCTYPE|^<html'"
elif [[ "$enc" == zstd ]]; then
  if command -v zstd > /dev/null 2>&1; then
    check "zstd HTML body contains DOCTYPE/html" \
      bash -c "zstd -dc '$body_file' 2>/dev/null | tr -d '\\n\\r\\t ' | head -c 20 | grep -qiE '^<!DOCTYPE|^<html'"
  else
    info "(skip zstd body decode — zstd CLI not installed)"
  fi
fi

# 4. Binary image path should not be recompressed by encode
curl -sk -D "$hdr_file" -o /dev/null \
  -H 'Accept-Encoding: gzip' \
  "$BASE_URL/raw-image/1"
img_code="$(curl -sk -o /dev/null -w '%{http_code}' "$BASE_URL/raw-image/1")"
img_enc="$(awk -F': ' 'tolower($1)=="content-encoding"{print tolower($2)}' "$hdr_file" | tr -d '\r' | head -1)"
check "GET /raw-image/1 → 200 (got $img_code)" test "$img_code" = "200"
check "raw-image has no Content-Encoding (got '${img_enc:-none}')" \
  bash -c "[[ -z \"$img_enc\" ]]"

# 5. Same-origin login (COP allow)
login_code="$(curl -sk -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/login" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -H "Origin: $ORIGIN" \
  -H 'Sec-Fetch-Site: same-origin' \
  -d 'username=admin' -d 'password=admin' \
  -c "$COOKIE_JAR")"
check "same-origin POST /login → 200 (got $login_code)" test "$login_code" = "200"

# 6. Authenticated dashboard
dash_code="$(curl -sk -o /dev/null -w '%{http_code}' -b "$COOKIE_JAR" "$BASE_URL/dashboard")"
check "GET /dashboard with session → 200 (got $dash_code)" test "$dash_code" = "200"

# 7. Cross-site login rejected by COP
cross_code="$(curl -sk -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/login" \
  -H 'Content-Type: application/x-www-form-urlencoded' \
  -H 'Origin: https://evil.example' \
  -H 'Sec-Fetch-Site: cross-site' \
  -d 'username=admin' -d 'password=admin')"
check "cross-site POST /login → 403 (got $cross_code)" test "$cross_code" = "403"

# 8. Warm gallery still 200
warm_code="$(curl -sk -o /dev/null -w '%{http_code}' "$BASE_URL/gallery/1")"
check "warm GET /gallery/1 → 200 (got $warm_code)" test "$warm_code" = "200"

echo
echo "Results: $PASS passed, $FAIL failed"
if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
