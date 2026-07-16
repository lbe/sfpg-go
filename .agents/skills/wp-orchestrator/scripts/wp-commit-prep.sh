#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

PLAN_FILE=""
WP=""

usage() {
  echo "Usage: wp-commit-prep.sh --plan FILE wp-N"
  echo ""
  echo "Generates tmp/commit_message.txt in the WP worktree (or main for Phase 4)"
  echo "and stages changed files. Does not commit."
  exit 1
}

prepare_commit() {
  local plan="$1"
  local wp="$2"

  local wt
  if wp_uses_main_worktree "$plan" "$wp"; then
    wt="$REPO_ROOT"
  else
    wt="$REPO_ROOT/$(wp_worktree_dir "$wp")"
  fi

  if [[ ! -d "$wt" ]]; then
    echo "ERROR: Worktree $wt does not exist" >&2
    exit 1
  fi

  local branch
  branch=$(wp_branch_name "$plan" "$wp")
  local title
  title=$(wp_title "$plan" "$wp")
  local status
  status=$(wp_status "$plan" "$wp")

  mkdir -p "$wt/tmp"

  local msg_file="$wt/tmp/commit_message.txt"

  cat > "$msg_file" << EOF
$wp: $title

Status from plan: $status
Branch: $branch
EOF

  echo "Wrote commit message to $msg_file"

  cd "$wt"
  local changed
  changed=$(git diff --name-only HEAD || true)
  if [[ -n "$changed" ]]; then
    echo "$changed" | xargs -r git add
    echo "Staged changed files:"
    echo "$changed"
  else
    echo "WARNING: No changed files to stage." >&2
  fi
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --plan)
      PLAN_FILE="$2"
      shift 2
      ;;
    -h | --help)
      usage
      ;;
    *)
      WP="$1"
      shift
      ;;
  esac
done

if [[ -z "${WP:-}" ]]; then
  usage
fi

PLAN_FILE=$(find_plan "${PLAN_FILE:-}")
WP=$(normalize_wp "$WP") || exit 1

prepare_commit "$PLAN_FILE" "$WP"
