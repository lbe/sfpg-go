#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

PLAN_FILE=""
WP=""

usage() {
  echo "Usage: wp-verify.sh --plan FILE wp-N"
  exit 1
}

changed_files() {
  local wt="$1"
  {
    git -C "$wt" diff --name-only HEAD
    git -C "$wt" ls-files --others --exclude-standard
  } | sort -u
}

changed_go_files() {
  changed_files "$1" | grep -E '\.go$' || true
}

changed_template_files() {
  changed_files "$1" | grep -E '\.html\.tmpl$' || true
}

changed_doc_files() {
  changed_files "$1" | grep -E '\.(md|txt)$' || true
}

changed_web_testsuite_files() {
  changed_files "$1" | grep -E '^web-testsuite/' || true
}

changed_playwright_files() {
  changed_files "$1" | grep -E '^tests/.*\.spec\.ts$' || true
}

changed_packages() {
  local wt="$1"
  changed_go_files "$wt" | xargs -r dirname | sort -u | sed 's|^|./|' || true
}

run_verify() {
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

  if ! "$SCRIPT_DIR/wp-phase2-gate.sh" --worktree "$wt" --structural-only "$wp"; then
    echo "Phase 2 structural migration gate FAILED." >&2
    exit 1
  fi

  echo "=== format-check (gofmt + goimports + prettier) ==="
  cd "$wt"
  if [[ -f "$REPO_ROOT/Makefile" ]]; then
    make format-check
  else
    echo "WARNING: Makefile not found; skipping make format-check" >&2
  fi

  local go_files
  go_files=$(changed_go_files "$wt")
  local template_files
  template_files=$(changed_template_files "$wt")
  local doc_files
  doc_files=$(changed_doc_files "$wt")

  if [[ -n "$go_files" ]]; then
    echo "=== golangci-lint ==="
    local pkgs
    pkgs=$(changed_packages "$wt")
    if [[ -n "$pkgs" ]] && command -v golangci-lint > /dev/null 2>&1; then
      local lockfile="$REPO_ROOT/tmp/.golangci-lint.lock"
      mkdir -p "$REPO_ROOT/tmp"
      exec 9> "$lockfile"
      if ! flock -w 120 9; then
        echo "ERROR: Timed out waiting for golangci-lint lock (another instance running?)" >&2
        exit 1
      fi
      # Stale linter cache from deleted worktrees can cause false failures.
      golangci-lint cache clean
      # shellcheck disable=SC2086
      local lint_rc
      if golangci-lint run $pkgs; then
        lint_rc=0
      else
        lint_rc=$?
      fi
      exec 9>&- # Release lock
      if [[ "$lint_rc" -ne 0 ]]; then
        echo "golangci-lint FAILED" >&2
        exit "$lint_rc"
      fi
      echo "golangci-lint OK"
    else
      echo "Skipping golangci-lint (no packages or linter unavailable)"
    fi

    echo "=== Targeted package tests ==="
    pkgs=$(changed_packages "$wt")
    if [[ -n "$pkgs" ]]; then
      # shellcheck disable=SC2086
      go test $pkgs -count=1
    fi

    echo "=== Build ==="
    go build -o /dev/null .

    echo "=== Integration tests ==="
    mkdir -p tmp
    go test ./... -tags=integration -count=1 > ./tmp/test_output.txt 2>&1
    echo "=== Integration test summary ==="
    grep -E "FAIL|PASS|ERROR|ok\s+|---" ./tmp/test_output.txt | tail -50
    echo "Integration tests passed. Output: ./tmp/test_output.txt"
  fi

  if [[ -n "$template_files" ]]; then
    echo "=== Template validation ==="
    if [[ -f "$wt/Makefile" ]]; then
      cd "$wt"
      make validate-templates
      make validate-hyperscript
    else
      echo "WARNING: Makefile not found; skipping make validate-templates" >&2
    fi
  fi

  if [[ -n "$doc_files" && -z "$go_files" && -z "$template_files" ]]; then
    echo "=== Doc-only WP ==="
    echo "Format-check passed; no Go or template changes to verify."
  fi

  local web_testsuite_files
  web_testsuite_files=$(changed_web_testsuite_files "$wt")
  local playwright_files
  playwright_files=$(changed_playwright_files "$wt")
  if [[ -n "$web_testsuite_files" || -n "$playwright_files" ]]; then
    echo "=== Dev-server tests (e2eweb / Playwright; requires air on :8083) ==="
    if ! curl -sf http://localhost:8083/health >/dev/null 2>&1; then
      echo "ERROR: Dev server not reachable at http://localhost:8083/health. Start air in the main worktree before verifying e2e/e2eweb changes." >&2
      exit 1
    fi
    if [[ -n "$web_testsuite_files" ]]; then
      go test -tags e2eweb -count=1 ./web-testsuite/...
    fi
    if [[ -n "$playwright_files" && -f "$wt/Makefile" ]]; then
      make test-browser
    fi
  fi

  if [[ -z "$go_files" && -z "$template_files" && -z "$doc_files" && -z "$web_testsuite_files" && -z "$playwright_files" ]]; then
    echo "WARNING: No changed files detected." >&2
  fi

  if ! "$SCRIPT_DIR/wp-phase2-gate.sh" --worktree "$wt" "$wp"; then
    echo "Phase 2 coverage migration gate FAILED." >&2
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

run_verify "$PLAN_FILE" "$WP"
