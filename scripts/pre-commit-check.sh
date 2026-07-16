#!/usr/bin/env bash
set -u

LOG_FILE="./tmp/pre-commit-check.log"
mkdir -p "$(dirname "$LOG_FILE")"
: > "$LOG_FILE"

RED_X='❌'
GREEN_CHECK='✅'
FAILED_COUNT=0

run_check() {
  local name="$1"
  shift
  local cmd=("$@")
  local output
  local timestamp
  local status

  output=$(mktemp)
  timestamp=$(date '+%Y-%m-%d %H:%M:%S')

  echo "[$timestamp] Running: ${cmd[*]}" >> "$LOG_FILE"

  if "${cmd[@]}" > "$output" 2>&1; then
    status=0
    echo "$timestamp $name: $GREEN_CHECK"
    echo "$timestamp $name: $GREEN_CHECK" >> "$LOG_FILE"
  else
    status=1
    echo "$timestamp $name: $RED_X"
    echo "$timestamp $name: $RED_X" >> "$LOG_FILE"
    echo "--- output ---" >> "$LOG_FILE"
    cat "$output" >> "$LOG_FILE"
    echo "--- end output ---" >> "$LOG_FILE"
    ((FAILED_COUNT++))
  fi

  echo "" >> "$LOG_FILE"
  rm -f "$output"
  return "$status"
}

format_check_failed=0
run_check "format-check" make format-check || format_check_failed=1

if [[ "$format_check_failed" -eq 1 ]]; then
  if run_check "format (auto-fix)" make format; then
    # Auto-fix resolved the formatting issue; clear the original failure.
    format_check_failed=0
    ((FAILED_COUNT--))
  fi
fi

run_check "lint" make lint
run_check "validate-templates" make validate-templates
run_check "validate-html-test-assertions" make validate-html-test-assertions

if [[ "$FAILED_COUNT" -gt 0 ]]; then
  timestamp=$(date '+%Y-%m-%d %H:%M:%S')
  echo ""
  echo "$timestamp Early exit: $FAILED_COUNT prior check(s) failed. Skipping test-all and test-browser."
  echo "$timestamp Early exit: $FAILED_COUNT prior check(s) failed. Skipping test-all and test-browser." >> "$LOG_FILE"
  exit "$FAILED_COUNT"
fi

run_check "test-all" make test-all
run_check "test-browser" make test-browser

timestamp=$(date '+%Y-%m-%d %H:%M:%S')
if [[ "$FAILED_COUNT" -gt 0 ]]; then
  echo ""
  echo "$timestamp $FAILED_COUNT check(s) failed. See $LOG_FILE for details."
  echo "$timestamp $FAILED_COUNT check(s) failed." >> "$LOG_FILE"
  exit "$FAILED_COUNT"
fi

echo ""
echo "$timestamp All checks passed."
echo "$timestamp All checks passed." >> "$LOG_FILE"
exit 0
