#!/usr/bin/env bash
# Smoke-test sfpg-go behind local Caddy (edge contract).
#
# If Caddy already answers at BASE_URL (default https://localhost:8443), runs smoke
# checks only. Otherwise requires the Go backend on SFPG_BACKEND_PORT (default 8083),
# starts Caddy with deploy/Caddyfile.local, runs checks, then stops Caddy if this
# script started it.
#
# Usage:
#   ./scripts/caddy-smoke.sh
#   BASE_URL=https://localhost:8443 ./scripts/caddy-smoke.sh
#   SFPG_BACKEND_PORT=8083 ./scripts/caddy-smoke.sh
#   CADDY_CONFIG=/path/to/Caddyfile.local ./scripts/caddy-smoke.sh
#
# Uses curl -k for BASE_URL because tls internal is not in the system trust store.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
SMOKE_DIR="${REPO_ROOT}/tmp/caddy-smoke"
CADDY_CONFIG="${CADDY_CONFIG:-${REPO_ROOT}/deploy/Caddyfile.local}"
SFPG_BACKEND_PORT="${SFPG_BACKEND_PORT:-8083}"
BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:${SFPG_BACKEND_PORT}}"

BASE_URL="${BASE_URL:-https://localhost:8443}"
COOKIE_JAR="${COOKIE_JAR:-${SMOKE_DIR}/cookies.txt}"
CADDY_LOG="${SMOKE_DIR}/caddy.log"
CADDY_PID_FILE="${SMOKE_DIR}/caddy.pid"
ORIGIN="${ORIGIN:-$BASE_URL}"
CURL_CONNECT_TIMEOUT="${CURL_CONNECT_TIMEOUT:-5}"
CURL_MAX_TIME="${CURL_MAX_TIME:-30}"
CADDY_READY_ATTEMPTS="${CADDY_READY_ATTEMPTS:-30}"
PASS=0
FAIL=0
WE_STARTED_CADDY=0

mkdir -p "$SMOKE_DIR"
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

CURL_LAST_ERR=""
HTTP_CODE="000"

curl_explain_failure() {
  local exit_code="$1"
  local err_file="$2"
  local target="${3:-$BASE_URL}"
  if [[ -s "$err_file" ]]; then
    CURL_LAST_ERR="$(tr '\n' ' ' < "$err_file" | sed 's/  */ /g' | head -c 240)"
    return
  fi
  case "$exit_code" in
    7) CURL_LAST_ERR="Failed to connect to host (curl exit 7 — ${target})" ;;
    28) CURL_LAST_ERR="Connection timed out (curl exit 28)" ;;
    *) CURL_LAST_ERR="curl exit ${exit_code} (no HTTP response)" ;;
  esac
}

# curl_http_code URL [curl args...] — sets HTTP_CODE; 000 on transport failure.
curl_http_code() {
  local url="$1"
  shift
  local err_file curl_exit=0
  err_file="$(mktemp)"
  CURL_LAST_ERR=""
  HTTP_CODE="000"
  HTTP_CODE="$(curl -sk \
    --connect-timeout "$CURL_CONNECT_TIMEOUT" \
    --max-time "$CURL_MAX_TIME" \
    -o /dev/null -w '%{http_code}' \
    "$@" \
    "$url" 2> "$err_file")" || curl_exit=$?
  if [[ -z "$HTTP_CODE" || "$HTTP_CODE" == "000" ]]; then
    HTTP_CODE="000"
    curl_explain_failure "$curl_exit" "$err_file" "$url"
  fi
  rm -f "$err_file"
}

# curl_backend_code PATH — plain HTTP to BACKEND_URL (no -k).
curl_backend_code() {
  local path="$1"
  shift
  local url="${BACKEND_URL}${path}"
  local err_file curl_exit=0
  err_file="$(mktemp)"
  CURL_LAST_ERR=""
  HTTP_CODE="000"
  HTTP_CODE="$(curl -s \
    --connect-timeout "$CURL_CONNECT_TIMEOUT" \
    --max-time "$CURL_MAX_TIME" \
    -o /dev/null -w '%{http_code}' \
    "$@" \
    "$url" 2> "$err_file")" || curl_exit=$?
  if [[ -z "$HTTP_CODE" || "$HTTP_CODE" == "000" ]]; then
    HTTP_CODE="000"
    curl_explain_failure "$curl_exit" "$err_file" "$url"
  fi
  rm -f "$err_file"
}

curl_run() {
  local url="$1"
  shift
  local err_file curl_exit=0
  err_file="$(mktemp)"
  CURL_LAST_ERR=""
  if curl -sk \
    --connect-timeout "$CURL_CONNECT_TIMEOUT" \
    --max-time "$CURL_MAX_TIME" \
    "$@" \
    "$url" 2> "$err_file"; then
    rm -f "$err_file"
    return 0
  fi
  curl_exit=$?
  curl_explain_failure "$curl_exit" "$err_file" "$url"
  rm -f "$err_file"
  return 1
}

stop_managed_caddy() {
  if [[ "$WE_STARTED_CADDY" != "1" ]]; then
    return 0
  fi
  if [[ -f "$CADDY_PID_FILE" ]]; then
    local pid
    pid="$(cat "$CADDY_PID_FILE" 2> /dev/null || true)"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2> /dev/null; then
      info "Stopping Caddy (pid $pid) started by this script"
      kill "$pid" 2> /dev/null || true
      wait "$pid" 2> /dev/null || true
    fi
    rm -f "$CADDY_PID_FILE"
  fi
  WE_STARTED_CADDY=0
}

fail_prerequisites() {
  red "$1"
  shift
  while [[ $# -gt 0 ]]; do
    info "$1"
    shift
  done
  echo
  red "SMOKE FAILED (prerequisites not met)"
  exit 1
}

ensure_caddy_for_smoke() {
  curl_http_code "$BASE_URL/gallery/1"
  if [[ "$HTTP_CODE" != "000" ]]; then
    green "Caddy already reachable at $BASE_URL — using existing edge (no backend check)"
    return 0
  fi

  info "Caddy not reachable at $BASE_URL — will start Caddy after backend check"

  curl_backend_code "/health"
  if [[ "$HTTP_CODE" != "200" ]]; then
    fail_prerequisites \
      "FAIL: backend not listening on :${SFPG_BACKEND_PORT} (GET ${BACKEND_URL}/health → ${HTTP_CODE})" \
      "curl: ${CURL_LAST_ERR:-none}" \
      "Start the Go backend on :${SFPG_BACKEND_PORT} (e.g. air), then re-run this script."
  fi
  green "Backend OK on :${SFPG_BACKEND_PORT}"

  if ! command -v caddy > /dev/null 2>&1; then
    fail_prerequisites \
      "FAIL: caddy not found in PATH (needed to start edge on ${BASE_URL})" \
      "Install Caddy or start it manually:" \
      "  cd ${REPO_ROOT}" \
      "  SFPG_BACKEND_PORT=${SFPG_BACKEND_PORT} caddy run --config ${CADDY_CONFIG}"
  fi

  if [[ ! -f "$CADDY_CONFIG" ]]; then
    fail_prerequisites "FAIL: Caddy config not found: ${CADDY_CONFIG}"
  fi

  : > "$CADDY_LOG"
  info "Starting Caddy (backend :${SFPG_BACKEND_PORT}, log ${CADDY_LOG})"
  (
    cd "$REPO_ROOT" || exit 1
    export SFPG_BACKEND_PORT
    exec caddy run --config "$CADDY_CONFIG"
  ) >> "$CADDY_LOG" 2>&1 &
  local caddy_pid=$!
  echo "$caddy_pid" > "$CADDY_PID_FILE"
  WE_STARTED_CADDY=1

  local attempt=1
  while [[ "$attempt" -le "$CADDY_READY_ATTEMPTS" ]]; do
    curl_http_code "$BASE_URL/gallery/1"
    if [[ "$HTTP_CODE" != "000" ]]; then
      green "Caddy ready at $BASE_URL (attempt ${attempt})"
      return 0
    fi
    sleep 0.5
    attempt=$((attempt + 1))
  done

  fail_prerequisites \
    "FAIL: Caddy did not become reachable at $BASE_URL" \
    "See log: ${CADDY_LOG}" \
    "Last curl: ${CURL_LAST_ERR:-none}"
}

run_smoke_checks() {
  curl_http_code "$BASE_URL/gallery/1"
  check "GET /gallery/1 → 200 (got $HTTP_CODE)" test "$HTTP_CODE" = "200"

  if curl_run "$BASE_URL/gallery/1" -D "$hdr_file" -o /dev/null; then
    check "Strict-Transport-Security present" \
      grep -qi 'strict-transport-security:.*max-age=' "$hdr_file"
  else
    check "Strict-Transport-Security present (curl: ${CURL_LAST_ERR:-failed})" false
  fi

  if curl_run "$BASE_URL/gallery/1" -D "$hdr_file" -o "$body_file" -H 'Accept-Encoding: gzip'; then
    enc="$(awk -F': ' 'tolower($1)=="content-encoding"{print tolower($2)}' "$hdr_file" | tr -d '\r' | head -1)"
    check "HTML Content-Encoding is gzip or zstd (got '${enc:-none}')" \
      bash -c "[[ \"$enc\" == gzip || \"$enc\" == zstd ]]"
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
  else
    check "HTML compression probe (curl: ${CURL_LAST_ERR:-failed})" false
  fi

  if curl_run "$BASE_URL/raw-image/1" -D "$hdr_file" -o /dev/null -H 'Accept-Encoding: gzip'; then
    curl_http_code "$BASE_URL/raw-image/1"
    img_code="$HTTP_CODE"
    img_enc="$(awk -F': ' 'tolower($1)=="content-encoding"{print tolower($2)}' "$hdr_file" | tr -d '\r' | head -1)"
    check "GET /raw-image/1 → 200 (got $img_code)" test "$img_code" = "200"
    check "raw-image has no Content-Encoding (got '${img_enc:-none}')" \
      bash -c "[[ -z \"$img_enc\" ]]"
  else
    check "GET /raw-image/1 headers (curl: ${CURL_LAST_ERR:-failed})" false
  fi

  curl_http_code "$BASE_URL/login" -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -H "Origin: $ORIGIN" \
    -H 'Sec-Fetch-Site: same-origin' \
    -d 'username=admin' -d 'password=admin' \
    -c "$COOKIE_JAR"
  login_code="$HTTP_CODE"
  if [[ "$login_code" == "000" ]]; then
    check "same-origin POST /login (curl: ${CURL_LAST_ERR:-failed})" false
  else
    check "same-origin POST /login → 200 (got $login_code)" test "$login_code" = "200"
  fi

  curl_http_code "$BASE_URL/dashboard" -b "$COOKIE_JAR"
  dash_code="$HTTP_CODE"
  if [[ "$dash_code" == "000" ]]; then
    check "GET /dashboard with session (curl: ${CURL_LAST_ERR:-failed})" false
  else
    check "GET /dashboard with session → 200 (got $dash_code)" test "$dash_code" = "200"
  fi

  curl_http_code "$BASE_URL/login" -X POST \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -H 'Origin: https://evil.example' \
    -H 'Sec-Fetch-Site: cross-site' \
    -d 'username=admin' -d 'password=admin'
  cross_code="$HTTP_CODE"
  if [[ "$cross_code" == "000" ]]; then
    check "cross-site POST /login (curl: ${CURL_LAST_ERR:-failed})" false
  else
    check "cross-site POST /login → 403 (got $cross_code)" test "$cross_code" = "403"
  fi

  curl_http_code "$BASE_URL/gallery/1"
  warm_code="$HTTP_CODE"
  if [[ "$warm_code" == "000" ]]; then
    check "warm GET /gallery/1 (curl: ${CURL_LAST_ERR:-failed})" false
  else
    check "warm GET /gallery/1 → 200 (got $warm_code)" test "$warm_code" = "200"
  fi

  curl_http_code "$BASE_URL/debug/pprof/"
  pprof_code="$HTTP_CODE"
  if [[ "$pprof_code" == "000" ]]; then
    check "GET /debug/pprof/ unauthenticated (curl: ${CURL_LAST_ERR:-failed})" false
  else
    check "GET /debug/pprof/ unauthenticated → 404 (got $pprof_code)" test "$pprof_code" = "404"
  fi

  curl_http_code "$BASE_URL/debug/pprof/" -b "$COOKIE_JAR"
  pprof_auth_code="$HTTP_CODE"
  if [[ "$pprof_auth_code" == "000" ]]; then
    check "GET /debug/pprof/ authenticated (curl: ${CURL_LAST_ERR:-failed})" false
  else
    check "GET /debug/pprof/ authenticated → 404 (got $pprof_auth_code)" test "$pprof_auth_code" = "404"
  fi
}

finish() {
  echo
  echo "Results: $PASS passed, $FAIL failed"
  if [[ "$FAIL" -eq 0 ]]; then
    green "SMOKE PASSED"
    return 0
  fi
  red "SMOKE FAILED"
  return 1
}

hdr_file="$(mktemp)"
body_file="$(mktemp)"
cleanup() {
  rm -f "$hdr_file" "$body_file"
  stop_managed_caddy
}
trap cleanup EXIT

echo "Caddy smoke against $BASE_URL (backend :${SFPG_BACKEND_PORT} when starting Caddy)"
echo

ensure_caddy_for_smoke
echo
run_smoke_checks

if finish; then
  exit 0
fi
exit 1
