#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

PLAN_FILE=""
WP=""
ISSUES=0

usage() {
  echo "Usage: wp-review-auto.sh --plan FILE wp-N"
  exit 1
}

changed_files() {
  local wt="$1"
  {
    git -C "$wt" diff --diff-filter=d --name-only HEAD
    git -C "$wt" ls-files --others --exclude-standard
  } | sort -u
}

changed_go_files() {
  changed_files "$1" | grep -E '\.go$' || true
}

changed_template_files() {
  changed_files "$1" | grep -E '\.html\.tmpl$' || true
}

changed_packages() {
  local wt="$1"
  changed_go_files "$wt" | xargs -r dirname | sort -u | sed 's|^|./|' || true
}

run_auto_review() {
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

  if ! "$SCRIPT_DIR/wp-phase2-gate.sh" \
    --worktree "$wt" --structural-only --no-evidence "$wp"; then
    echo "Phase 2 structural migration gate found issues." >&2
    ISSUES=1
  fi

  cd "$wt"

  local go_files
  go_files=$(changed_go_files "$wt")
  local template_files
  template_files=$(changed_template_files "$wt")

  if [[ -n "$go_files" ]]; then
    echo "=== gofmt check ==="
    local fmt_out
    fmt_out=$(echo "$go_files" | xargs -r gofmt -l)
    if [[ -n "$fmt_out" ]]; then
      echo "gofmt issues found in:"
      echo "$fmt_out"
      echo "Run: scripts/format-go-changed.sh on the listed files"
      ISSUES=1
    else
      echo "gofmt OK"
    fi

    echo "=== golangci-lint check ==="
    local pkgs
    pkgs=$(changed_packages "$wt")
    if [[ -n "$pkgs" ]]; then
      if command -v golangci-lint > /dev/null 2>&1; then
        local lockfile="$REPO_ROOT/tmp/.golangci-lint.lock"
        mkdir -p "$REPO_ROOT/tmp"
        exec 9> "$lockfile"
        if ! flock -w 120 9; then
          echo "ERROR: Timed out waiting for golangci-lint lock (another instance running?)" >&2
          exit 1
        fi
        # shellcheck disable=SC2086
        if golangci-lint run $pkgs; then
          echo "golangci-lint OK"
        else
          echo "golangci-lint found issues" >&2
          ISSUES=1
        fi
        exec 9>&- # Release lock
      else
        echo "WARNING: golangci-lint not installed; skipping" >&2
      fi
    else
      echo "No Go packages changed."
    fi
  fi

  if [[ -n "$template_files" ]]; then
    echo "=== prettier check ==="
    if command -v npx > /dev/null 2>&1; then
      if ! echo "$template_files" | xargs -r npx prettier --check 2>&1; then
        echo "prettier issues found. Run: npx prettier --write on the listed files"
        ISSUES=1
      else
        echo "prettier OK"
      fi
    else
      echo "WARNING: npx/prettier not available; skipping" >&2
    fi
  fi

  echo "=== Diff scope ==="
  git -C "$wt" diff --stat HEAD
  git -C "$wt" ls-files --others --exclude-standard | sed 's/^/?? /'

  if [[ "$ISSUES" -eq 1 ]]; then
    echo ""
    echo "=== Issues found (read-only report) ==="
    echo "The coder subagent must fix these before review can pass."
    exit 1
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

run_auto_review "$PLAN_FILE" "$WP"
