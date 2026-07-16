#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

PLAN_FILE=""
WP=""
STATUS=""

usage() {
  echo "Usage: wp-plan-update.sh --plan FILE wp-N status"
  echo "Valid statuses: pending, in-progress, implemented, verified, reviewed, done, on hold"
  exit 1
}

update_status() {
  local plan="$1"
  local wp="$2"
  local status="$3"

  local lock_file="$REPO_ROOT/tmp/.plan-remediation.lock"
  mkdir -p "$REPO_ROOT/tmp"

  exec 200> "$lock_file"
  flock -x 200

  awk -v wp="$wp" -v status="$status" '
        /^#### WP-[0-9]+/ {
            in_section=0
            match($0, /^#### WP-[0-9]+/)
            section_wp = substr($0, RSTART, RLENGTH)
            gsub(/^#### +/, "", section_wp)
            if (section_wp == wp) in_section=1
        }
        in_section && /^\*\*Status:\*\* / {
            sub(/`[^`]+`/, "`" status "`")
            print
            next
        }
        { print }
    ' "$plan" > "$plan.tmp"

  mv "$plan.tmp" "$plan"

  flock -u 200
  exec 200>&-

  echo "Updated $wp status to '$status' in $plan"
}

write_sidecar() {
  local wp="$1"
  local status="$2"
  local note="${3:-}"
  local sidecar="$REPO_ROOT/tmp/$wp-status.md"
  cat > "$sidecar" << EOF
# $wp Status

**Status:** \`$status\`
**Updated:** $(date -Iseconds)
**Note:** $note
EOF
  echo "Wrote sidecar $sidecar"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --plan)
      PLAN_FILE="$2"
      shift 2
      ;;
    --sidecar)
      WRITESIDECAR=1
      shift
      ;;
    -h | --help)
      usage
      ;;
    *)
      if [[ -z "${WP:-}" ]]; then
        WP="$1"
      elif [[ -z "${STATUS:-}" ]]; then
        STATUS="$1"
      else
        echo "ERROR: Unexpected argument: $1" >&2
        usage
      fi
      shift
      ;;
  esac
done

if [[ -z "${WP:-}" || -z "${STATUS:-}" ]]; then
  usage
fi

PLAN_FILE=$(find_plan "${PLAN_FILE:-}")
WP=$(normalize_wp "$WP") || exit 1

case "$STATUS" in
  pending | in-progress | implemented | verified | reviewed | done | on-hold | "on hold") ;;
  *)
    echo "ERROR: Invalid status: $STATUS" >&2
    usage
    ;;
esac

update_status "$PLAN_FILE" "$WP" "$STATUS"

if [[ "${WRITESIDECAR:-0}" -eq 1 ]]; then
  write_sidecar "$WP" "$STATUS" ""
fi
