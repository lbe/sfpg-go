#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

PLAN_FILE=""
REQUESTED_PLAN_FILE=""
TEST_DIR=""
FAILURES=0

fail() {
  echo "FAIL: $*" >&2
  FAILURES=$((FAILURES + 1))
}

pass() {
  echo "PASS: $*"
}

create_test_plan() {
  TEST_DIR=$(mktemp -d)
  PLAN_FILE="$TEST_DIR/plan-remediation-selftest.md"
  cat > "$PLAN_FILE" <<'EOF'
#### WP-1 · Re-tag misclassified integration tests
**Status:** `pending` · **Phase:** 1 · **Depends on:** —

| File | Change |
|------|--------|
| `internal/server/app_test.go` | Fixture |

#### WP-2 · Fixture status update
**Status:** `pending` · **Phase:** 1 · **Depends on:** —

#### WP-3 · Hard dependency
**Status:** `done` · **Phase:** 1 · **Depends on:** —

#### WP-4 · Dependency fixture
**Status:** `pending` · **Phase:** 1 · **Depends on:** WP-3; WP-1 (soft/parallel); WP-2 (soft/parallel)

#### WP-6 · On-hold fixture
**Status:** `on hold` · **Phase:** 1 · **Depends on:** —

#### WP-39 · Phase 4 fixture
**Status:** `pending` · **Phase:** 4A · **Depends on:** —

#### WP-48 · Main-worktree e2e fixture
**Status:** `pending` · **Phase:** 6 · **Depends on:** — · **Execute in:** main worktree
EOF
}

cleanup() {
  if [[ -n "$TEST_DIR" ]]; then
    rm -rf "$TEST_DIR"
  fi
}

test_normalize_wp() {
  local cases=("wp-1:WP-1" "WP-1:WP-1" "Wp-1:WP-1" "wp-42:WP-42")
  for c in "${cases[@]}"; do
    local input="${c%%:*}"
    local expected="${c##*:}"
    local got
    got=$(normalize_wp "$input") || {
      fail "normalize_wp $input"
      continue
    }
    if [[ "$got" == "$expected" ]]; then
      pass "normalize_wp $input -> $got"
    else
      fail "normalize_wp $input expected $expected got $got"
    fi
  done

  if normalize_wp "bad" > /dev/null 2>&1; then
    fail "normalize_wp bad should have failed"
  else
    pass "normalize_wp bad correctly rejects invalid input"
  fi
}

test_wp_branch_name() {
  local got
  got=$(wp_branch_name "$PLAN_FILE" "WP-1")
  if [[ "$got" == "wp-1-re-tag-misclassified-integration-tests" ]]; then
    pass "wp_branch_name WP-1 -> $got"
  else
    fail "wp_branch_name WP-1 expected wp-1-re-tag-misclassified-integration-tests got $got"
  fi
}

test_wp_deps() {
  local hard
  hard=$(wp_deps "$PLAN_FILE" "WP-4")
  if [[ "$hard" == "WP-3" ]]; then
    pass "wp_deps WP-4 hard -> WP-3"
  else
    fail "wp_deps WP-4 hard expected WP-3 got '$hard'"
  fi

  local soft
  soft=$(wp_soft_deps "$PLAN_FILE" "WP-4")
  if [[ "$soft" == $'WP-1\nWP-2' || "$soft" == $'WP-2\nWP-1' ]]; then
    pass "wp_soft_deps WP-4 -> $soft"
  else
    fail "wp_soft_deps WP-4 expected WP-1 and WP-2 got '$soft'"
  fi
}

test_wp_is_phase4() {
  if wp_is_phase4 "$PLAN_FILE" "WP-39"; then
    pass "wp_is_phase4 WP-39"
  else
    fail "wp_is_phase4 WP-39 should be true"
  fi

  if ! wp_is_phase4 "$PLAN_FILE" "WP-1"; then
    pass "wp_is_phase4 WP-1 is false"
  else
    fail "wp_is_phase4 WP-1 should be false"
  fi
}

test_wp_uses_main_worktree() {
  if wp_uses_main_worktree "$PLAN_FILE" "WP-39"; then
    pass "wp_uses_main_worktree WP-39 (Phase 4)"
  else
    fail "wp_uses_main_worktree WP-39 should be true"
  fi

  if wp_uses_main_worktree "$PLAN_FILE" "WP-48"; then
    pass "wp_uses_main_worktree WP-48 (e2e)"
  else
    fail "wp_uses_main_worktree WP-48 should be true"
  fi

  if ! wp_uses_main_worktree "$PLAN_FILE" "WP-1"; then
    pass "wp_uses_main_worktree WP-1 is false"
  else
    fail "wp_uses_main_worktree WP-1 should be false"
  fi
}

test_wp_verify_script() {
  if bash -n "$SCRIPT_DIR/wp-verify.sh"; then
    pass "wp-verify.sh bash -n"
  else
    fail "wp-verify.sh bash -n"
  fi

  if grep -q '^changed_doc_files()' "$SCRIPT_DIR/wp-verify.sh"; then
    pass "wp-verify defines changed_doc_files"
  else
    fail "wp-verify missing changed_doc_files"
  fi

  if grep -q '^changed_web_testsuite_files()' "$SCRIPT_DIR/wp-verify.sh"; then
    pass "wp-verify defines changed_web_testsuite_files"
  else
    fail "wp-verify missing changed_web_testsuite_files"
  fi
}

test_analyze_topo() {
  local out
  out=$("$SCRIPT_DIR/wp-analyze.sh" --plan "$PLAN_FILE" WP-4 WP-3 2> /dev/null)
  if echo "$out" | grep -q "WP-3" && echo "$out" | grep -q "WP-4"; then
    pass "wp-analyze WP-4 WP-3 produces output"
  else
    fail "wp-analyze WP-4 WP-3 missing expected WPs"
  fi
}

test_plan_update_roundtrip() {
  local original
  original=$(wp_status "$PLAN_FILE" "WP-2")

  "$SCRIPT_DIR/wp-plan-update.sh" --plan "$PLAN_FILE" WP-2 in-progress > /dev/null
  local got
  got=$(wp_status "$PLAN_FILE" "WP-2")
  if [[ "$got" == "in-progress" ]]; then
    pass "wp-plan-update WP-2 -> in-progress"
  else
    fail "wp-plan-update WP-2 expected in-progress got $got"
  fi

  "$SCRIPT_DIR/wp-plan-update.sh" --plan "$PLAN_FILE" WP-2 "$original" > /dev/null
  got=$(wp_status "$PLAN_FILE" "WP-2")
  if [[ "$got" == "$original" ]]; then
    pass "wp-plan-update WP-2 restored to $original"
  else
    fail "wp-plan-update WP-2 restore expected $original got $got"
  fi
}

test_review_parse() {
  if echo "REVIEW: PASS" | "$SCRIPT_DIR/wp-review-parse.sh" - > /dev/null 2>&1; then
    pass "wp-review-parse PASS"
  else
    fail "wp-review-parse PASS should exit 0"
  fi

  if echo "REVIEW: FAIL" | "$SCRIPT_DIR/wp-review-parse.sh" - > /dev/null 2>&1; then
    fail "wp-review-parse FAIL should exit non-zero"
  else
    pass "wp-review-parse FAIL"
  fi
}

test_phase4_create_routing() {
  local out
  out=$("$SCRIPT_DIR/wp-guard.sh" --plan "$PLAN_FILE" --dry-run --create WP-39 2> /dev/null)
  if echo "$out" | grep -q "main worktree"; then
    pass "Phase 4 create routes to main worktree"
  else
    fail "Phase 4 create should route to main worktree; got: $out"
  fi

  if echo "$out" | grep -q ".worktrees/wp-39"; then
    fail "Phase 4 create should not mention .worktrees/wp-39"
  else
    pass "Phase 4 create does not mention .worktrees/wp-39"
  fi
}

test_on_hold_blocked() {
  if "$SCRIPT_DIR/wp-guard.sh" --plan "$PLAN_FILE" --check-deps WP-6 > /dev/null 2>&1; then
    fail "WP-6 (on hold) should be blocked by --check-deps"
  else
    pass "WP-6 (on hold) correctly blocked by --check-deps"
  fi
}

test_phase2_gate_helpers() {
  # shellcheck source=wp-phase2-gate.sh
  source "$SCRIPT_DIR/wp-phase2-gate.sh"

  local wp
  local selection_ok=1
  for wp in WP-16 WP-51 WP-52 WP-53 WP-54; do
    if ! is_phase2_gate_wp "$wp"; then
      selection_ok=0
    fi
  done
  if is_phase2_gate_wp "WP-17"; then
    selection_ok=0
  fi

  if [[ "$selection_ok" -eq 1 ]]; then
    pass "phase2 gate WP selection"
  else
    fail "phase2 gate WP selection"
  fi

  if printf '%s\n' '+//go:build integration' '-//go:build unit' | has_substantive_test_diff; then
    fail "tag-only diff should not be substantive"
  else
    pass "tag-only diff rejected"
  fi

  if printf '%s\n' '+func TestMoved(t *testing.T) {}' '-func TestOld(t *testing.T) {}' | has_substantive_test_diff; then
    pass "test body diff accepted as substantive"
  else
    fail "test body diff should be substantive"
  fi
}

usage() {
  echo "Usage: wp-selftest.sh --plan FILE"
  exit 1
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
      usage
      ;;
  esac
done

REQUESTED_PLAN_FILE=$(find_plan "${PLAN_FILE:-}")
create_test_plan
trap cleanup EXIT

echo "Running wp-orchestrator self-tests (requested plan: $REQUESTED_PLAN_FILE)"
echo ""

test_normalize_wp
test_wp_branch_name
test_wp_deps
test_wp_is_phase4
test_wp_uses_main_worktree
test_wp_verify_script
test_analyze_topo
test_plan_update_roundtrip
test_review_parse
test_phase4_create_routing
test_on_hold_blocked
test_phase2_gate_helpers

echo ""
if [[ "$FAILURES" -eq 0 ]]; then
  echo "All tests passed."
  exit 0
else
  echo "$FAILURES test(s) failed."
  exit 1
fi
