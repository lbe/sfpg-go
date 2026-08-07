#!/usr/bin/env bash
set -euo pipefail

# Format changed Go files: always gofmt; goimports only when imports may have changed.
#
# Usage: scripts/format-go-changed.sh file1.go [file2.go ...]
#
# Set FORMAT_GO_IMPORTS=1 to run goimports on every listed file.

usage() {
  echo "Usage: $0 file.go [file.go ...]" >&2
  exit 1
}

import_diff_touches() {
  local f="$1"
  local diff
  diff=$(
    {
      git diff -- "$f" 2> /dev/null || true
      git diff --cached -- "$f" 2> /dev/null || true
    } | grep -E '^[+-]' | grep -Ev '^[+-]{3}' || true
  )
  if [[ -z "$diff" ]]; then
    return 1
  fi
  echo "$diff" | grep -Eq '^[+-](import |[[:space:]]+"|import \()'
}

file_needs_goimports() {
  local f="$1"

  if [[ "${FORMAT_GO_IMPORTS:-}" == "1" ]]; then
    return 0
  fi

  if [[ ! -f "$f" ]]; then
    return 1
  fi

  if ! git ls-files --error-unmatch "$f" > /dev/null 2>&1; then
    grep -Eq '^(import |[[:space:]]+"|import \()' "$f"
    return
  fi

  import_diff_touches "$f"
}

if [[ $# -eq 0 ]]; then
  usage
fi

files=("$@")
gofmt -w "${files[@]}"

needs_imports=()
for f in "${files[@]}"; do
  if file_needs_goimports "$f"; then
    needs_imports+=("$f")
  fi
done

if [[ ${#needs_imports[@]} -eq 0 ]]; then
  exit 0
fi

if ! command -v goimports > /dev/null 2>&1; then
  echo "goimports not found; skipped for: ${needs_imports[*]}" >&2
  exit 0
fi

goimports -w "${needs_imports[@]}"
