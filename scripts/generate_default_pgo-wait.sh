#!/usr/bin/env bash
# Shared wait/parse helpers for PGO generation and smoke testing.
# Source this file — do not execute directly.
# Does NOT start servers, use tee/trap, or check ports.

set -euo pipefail

# Defaults so bare-invocation works (overridden by main script)
BASE_URL="${BASE_URL:-http://localhost:8083}"
COOKIE_JAR="${COOKIE_JAR:-$(mktemp)}"

log() {
  printf '[%s] %s\n' "$(date -Iseconds)" "$*"
}

die() {
  log "ERROR: $*"
  exit 1
}

login_admin() {
  curl -fsS -c "${COOKIE_JAR}" -X POST "${BASE_URL}/login" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -H "Origin: ${BASE_URL}" \
    -d "username=admin&password=admin" > /dev/null
}

dashboard_html() {
  curl -fsS -b "${COOKIE_JAR}" "${BASE_URL}/dashboard"
}

# Extract text content of a dashboard element by element ID.
extract_field() {
  perl -e '
    my $html = shift;
    my $id = shift;
    exit 1 unless defined $html and defined $id;
    if ($html =~ /id="\Q$id\E"[^>]*>\s*(.*?)\s*<\//s) {
      my $val = $1;
      $val =~ s/^\s+|\s+$//g;
      $val =~ s/\s+/ /g;
      print $val;
    }
  ' "$@"
}

discovery_complete() {
  local html="$1"
  local total inflight
  total="$(extract_field "$html" "fp-total")"
  inflight="$(extract_field "$html" "fp-inflight")"
  # Strip commas from numbers (e.g. "5,000" -> "5000")
  total="${total//,/}"
  inflight="${inflight//,/}"
  [[ -n "$total" && -n "$inflight" && "$total" -gt 0 && "$inflight" -eq 0 ]] && echo 1 || echo 0
}

in_flight() {
  local html="$1"
  local val
  val="$(extract_field "$html" "fp-inflight")"
  val="${val//,/}"
  echo "${val:-0}"
}

cache_batch_complete() {
  local html="$1"
  local http_status
  http_status="$(extract_field "$html" "http-status")"
  # If HTTP cache is not enabled, no batch load needed
  if [[ "${http_status}" != "Enabled" ]]; then
    echo 1
    return
  fi
  local batch_status progress
  batch_status="$(extract_field "$html" "batch-status")"
  progress="$(extract_field "$html" "batch-progress")"
  # progress format: "NNN / NNN" (may contain commas, newlines, extra spaces)
  local done_str total_str
  done_str="$(echo "$progress" | sed 's/[, ]//g' | cut -d/ -f1)"
  total_str="$(echo "$progress" | sed 's/[, ]//g' | cut -d/ -f2)"
  if [[ "$batch_status" != "Running" && -n "$total_str" && "$total_str" -gt 0 && "${done_str:-0}" -ge "$total_str" ]]; then
    echo 1
  else
    echo 0
  fi
}

cache_batch_running() {
  local html="$1"
  local status
  status="$(extract_field "$html" "batch-status")"
  [[ "${status}" == "Running" ]] && echo 1 || echo 0
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
    local html
    html="$(dashboard_html)"
    if [[ "$(discovery_complete "${html}")" == "1" ]]; then
      log "Discovery complete"
      return 0
    fi
    if [[ "$(in_flight "${html}")" == "0" && "${triggered}" -eq 0 ]]; then
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
    local html
    html="$(dashboard_html)"
    if [[ "$(cache_batch_complete "${html}")" == "1" ]]; then
      local http_status
      http_status="$(extract_field "$html" "http-status")"
      if [[ "${http_status}" != "Enabled" ]]; then
        log "Cache disabled; skipping cache batch load"
      else
        log "Cache batch load complete"
      fi
      return 0
    fi
    if [[ "$(cache_batch_running "${html}")" == "0" && "${triggered}" -eq 0 ]]; then
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
