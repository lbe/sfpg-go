#!/usr/bin/env bash
# Generate default.pgo by profiling the server while running tests.
#
# Flow: discovery → e2eweb + Playwright → cache batch load (Playwright teardown)
#       → SIGTERM → write default.pgo
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

log() {
  printf '[%s] %s\n' "$(date -Iseconds)" "$*"
}

die() {
  log "ERROR: $*"
  exit 1
}

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

login_admin() {
  curl -fsS -c "${COOKIE_JAR}" -X POST "${BASE_URL}/login" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -H "Origin: ${BASE_URL}" \
    -d "username=admin&password=admin" > /dev/null
}

metrics_json() {
  curl -fsS -b "${COOKIE_JAR}" "${BASE_URL}/api/metrics"
}

discovery_complete() {
  local metrics="$1"
  perl -MJSON::PP=decode_json -e '
    my $data = decode_json($ARGV[0]);
    my $fp = $data->{file_processing} // {};
    print(($fp->{total_found} // 0) > 0 && ($fp->{in_flight} // 0) == 0 ? 1 : 0);
  ' "${metrics}"
}

in_flight() {
  local metrics="$1"
  perl -MJSON::PP=decode_json -e '
    my $data = decode_json($ARGV[0]);
    print $data->{file_processing}{in_flight} // 0;
  ' "${metrics}"
}

cache_batch_complete() {
  local metrics="$1"
  perl -MJSON::PP=decode_json -e '
    my $data = decode_json($ARGV[0]);
    my $hc = $data->{http_cache} // {};
    if (!($hc->{enabled} // 0)) { print 1; exit }
    my $c = $data->{cache_batch_load} // {};
    my $done = ($c->{targets_completed} // 0)
             + ($c->{targets_failed} // 0)
             + ($c->{targets_skipped} // 0);
    my $total = $c->{targets_total} // 0;
    my $running = $c->{is_running} ? 1 : 0;
    print(($total > 0 && $done >= $total && !$running) ? 1 : 0);
  ' "${metrics}"
}

cache_batch_running() {
  local metrics="$1"
  perl -MJSON::PP=decode_json -e '
    my $data = decode_json($ARGV[0]);
    print $data->{cache_batch_load}{is_running} ? 1 : 0;
  ' "${metrics}"
}

wait_for_health() {
  local deadline=$((SECONDS + 30))
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    if curl -fsS "${BASE_URL}/health" > /dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  die "/health did not return 200 within 30s"
}

wait_for_discovery() {
  login_admin
  local deadline=$((SECONDS + 120))
  local triggered=0
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    local metrics
    metrics="$(metrics_json)"
    if [[ "$(discovery_complete "${metrics}")" == "1" ]]; then
      log "Discovery complete"
      return 0
    fi
    if [[ "$(in_flight "${metrics}")" == "0" && "${triggered}" -eq 0 ]]; then
      log "POST /server/discovery"
      curl -fsS -b "${COOKIE_JAR}" -X POST "${BASE_URL}/server/discovery" \
        -H "Origin: ${BASE_URL}" > /dev/null || true
      triggered=1
    fi
    sleep 1
  done
  die "Discovery did not complete within 120s"
}

wait_for_cache_batch_load() {
  login_admin
  local deadline=$((SECONDS + 120))
  local triggered=0
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    local metrics
    metrics="$(metrics_json)"
    if [[ "$(cache_batch_complete "${metrics}")" == "1" ]]; then
      log "Cache batch load complete"
      return 0
    fi
    if [[ "$(cache_batch_running "${metrics}")" == "0" && "${triggered}" -eq 0 ]]; then
      log "POST /server/cache-batch-load"
      local code
      code="$(curl -sS -o /dev/null -w '%{http_code}' -b "${COOKIE_JAR}" \
        -X POST "${BASE_URL}/server/cache-batch-load" \
        -H "Origin: ${BASE_URL}")"
      if [[ "${code}" == "200" ]]; then
        triggered=1
      elif [[ "${code}" == "409" ]]; then
        sleep 1
        continue
      else
        die "POST /server/cache-batch-load returned ${code}"
      fi
    fi
    sleep 1
  done
  die "Cache batch load did not complete within 120s"
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
