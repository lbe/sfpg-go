#!/usr/bin/env bash
# Generate default.pgo by profiling the server while running tests.
#
# Flow: discovery → e2eweb + Playwright → cache batch load (Playwright teardown)
#       → SIGTERM → write default.pgo
#
# Shared wait/parse helpers are in generate_default_pgo-wait.sh (sourced below).
#
# Defaults (override via environment variables):
#   SFPG_PGO_ROOT        Root directory (default: <module>/tmp)
#   SFPG_PGO_PORT        Listen port (default: 8083)
#   SFPG_PGO_EXECUTABLE  Server binary (default: <root>/main)
#   SFPG_PGO_DELETE_DB   Set to 1 to offer deleting <root>/DB (requires confirm)
#
# Example:
#   ./scripts/generate_default_pgo.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULE_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
ROOT_DIR="${SFPG_PGO_ROOT:-${MODULE_ROOT}/tmp}"
PORT="${SFPG_PGO_PORT:-8083}"
EXECUTABLE="${SFPG_PGO_EXECUTABLE:-${ROOT_DIR}/main}"
DELETE_DB="${SFPG_PGO_DELETE_DB:-0}"
BASE_URL="http://localhost:${PORT}"
OUTPUT_PGO="${MODULE_ROOT}/default.pgo"
BACKUP_DIR="${MODULE_ROOT}/tmp"

RUN_STAMP="$(date +%Y%m%d-%H%M%S)"
LOG_DIR="${ROOT_DIR}/logs"
MAIN_LOG="${LOG_DIR}/generate-pgo-${RUN_STAMP}.log"
SERVER_LOG="${LOG_DIR}/generate-pgo-${RUN_STAMP}.server.log"
COOKIE_JAR="$(mktemp)"
SERVER_PID=""

mkdir -p "${LOG_DIR}" "${BACKUP_DIR}"
exec > >(tee -a "${MAIN_LOG}") 2>&1

# Source shared helpers (log, die, login_admin, dashboard_html, wait functions)
source "${SCRIPT_DIR}/generate_default_pgo-wait.sh"

port_in_use() {
  if command -v ss > /dev/null 2>&1; then
    ss -ltn "( sport = :${PORT} )" 2> /dev/null | grep -q LISTEN
    return $?
  fi
  (echo > /dev/tcp/127.0.0.1/"${PORT}") > /dev/null 2>&1
}

maybe_delete_db() {
  if [[ "${DELETE_DB}" != "1" ]]; then
    return 0
  fi
  local db_dir="${ROOT_DIR}/DB"
  if [[ ! -d "${db_dir}" ]]; then
    log "SFPG_PGO_DELETE_DB=1 but ${db_dir} does not exist; skipping"
    return 0
  fi
  printf 'Delete %s before profiling? [y/N] ' "${db_dir}"
  local answer
  read -r answer
  case "${answer}" in
    y | Y | yes | YES)
      log "Removing ${db_dir}"
      rm -rf "${db_dir}"
      ;;
    *)
      log "DB delete declined; continuing with existing DB"
      ;;
  esac
}

backup_existing_pgo() {
  if [[ ! -f "${OUTPUT_PGO}" ]]; then
    return 0
  fi
  local ts backup
  ts="$(date -r "${OUTPUT_PGO}" +%Y%m%d-%H%M%S)"
  backup="${BACKUP_DIR}/default.pgo.${ts}"
  cp -a "${OUTPUT_PGO}" "${backup}"
  log "Backed up ${OUTPUT_PGO} to ${backup}"
}

execute_tests() {
  cd "${MODULE_ROOT}"
  log "Running web-testsuite (skip TestRestart)"
  SERVER_URL="${BASE_URL}" DB_PATH="${ROOT_DIR}/DB/sfpg.db" \
    go test -tags e2eweb ./web-testsuite/ -count=1 -skip '^TestRestart$'
  log "Running Playwright (chromium, workers=1)"
  npx playwright test --project=chromium --workers=1 --reporter=list
}

stop_server() {
  [[ -n "${SERVER_PID}" ]] || return 0
  local pid="${SERVER_PID}"
  SERVER_PID=""
  if ! kill -0 "${pid}" 2> /dev/null; then
    return 0
  fi
  log "Sending SIGTERM to server (pid=${pid})"
  kill -TERM "${pid}" 2> /dev/null || true
  local deadline=$((SECONDS + 60))
  while kill -0 "${pid}" 2> /dev/null && [[ "${SECONDS}" -lt "${deadline}" ]]; do
    sleep 0.5
  done
  if kill -0 "${pid}" 2> /dev/null; then
    die "Server did not exit within 60s after SIGTERM"
  fi
  wait "${pid}" 2> /dev/null || true
}

# Strip ANSI color codes from slog console output.
strip_ansi() {
  sed 's/\x1b\[[0-9;]*m//g'
}

# Resolve cpu.pprof path into CPU_PROF. Do not call from $(...); die must run in
# the main shell so failures exit the script instead of becoming a fake path.
find_cpu_profile() {
  local profile_dir cpu_prof plain
  plain="$(strip_ansi < "${SERVER_LOG}")"

  # Prefer the flushed-artifact line; fall back to startup Profiler dir= line.
  profile_dir="$(printf '%s\n' "${plain}" | grep 'Profile artifacts written' | tail -1 | sed -n 's/.*dir=\([^ ]*\).*/\1/p' || true)"
  if [[ -z "${profile_dir}" ]]; then
    profile_dir="$(printf '%s\n' "${plain}" | grep 'Profiler' | grep 'dir=' | tail -1 | sed -n 's/.*dir=\([^ ]*\).*/\1/p' || true)"
  fi
  if [[ -z "${profile_dir}" ]]; then
    # Starting profiler logs file=/tmp/profileN/cpu.pprof
    cpu_prof="$(printf '%s\n' "${plain}" | grep 'Starting profiler' | tail -1 | sed -n 's/.*file=\([^ ]*\).*/\1/p' || true)"
    if [[ -n "${cpu_prof}" ]]; then
      profile_dir="$(dirname "${cpu_prof}")"
    fi
  fi

  [[ -n "${profile_dir}" ]] || die "Could not find profiler directory in ${SERVER_LOG}"
  CPU_PROF="${profile_dir}/cpu.pprof"
  [[ -f "${CPU_PROF}" ]] || die "Missing ${CPU_PROF}"
  log "Found CPU profile ${CPU_PROF}"
}

convert_profile_to_pgo() {
  local cpu_prof="$1"
  go tool pprof -output="${OUTPUT_PGO}" -proto "${cpu_prof}"
  log "Wrote ${OUTPUT_PGO}"
}

cleanup() {
  if [[ -n "${SERVER_PID}" ]]; then
    stop_server || true
  fi
  rm -f "${COOKIE_JAR}"
}
trap cleanup EXIT

log "generate_default_pgo starting"
log "root_dir=${ROOT_DIR}"
log "port=${PORT}"
log "executable=${EXECUTABLE}"
log "output=${OUTPUT_PGO}"
log "log=${MAIN_LOG}"

if port_in_use; then
  die "Port ${PORT} is already in use"
fi

[[ -x "${EXECUTABLE}" ]] || die "Executable not found or not executable: ${EXECUTABLE}"

maybe_delete_db
backup_existing_pgo

log "Starting server with profiling enabled"
SEPG_SESSION_SECURE=false \
  SEPG_SESSION_SECRET="${SEPG_SESSION_SECRET:-your-very-strong-and-unique-secret-key-v2}" \
  "${EXECUTABLE}" -port="${PORT}" -profile=cpu > "${SERVER_LOG}" 2>&1 &
SERVER_PID=$!
log "Server pid=${SERVER_PID}"

wait_for_health
wait_for_discovery
execute_tests
# Playwright global teardown starts/waits for cache batch load; ensure it finished.
wait_for_cache_batch_load
stop_server
find_cpu_profile
convert_profile_to_pgo "${CPU_PROF}"

log "Done"
