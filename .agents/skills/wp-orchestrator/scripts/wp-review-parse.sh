#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

usage() {
  echo "Usage: wp-review-parse.sh <review-output-file>"
  echo "       cat review-output.txt | wp-review-parse.sh -"
  echo ""
  echo "Exits 0 only if the review output contains an exact 'REVIEW: PASS' line."
  exit 1
}

input="${1:-}"
if [[ -z "$input" || "$input" == "-h" || "$input" == "--help" ]]; then
  usage
fi

if [[ "$input" == "-" ]]; then
  review=$(cat)
else
  if [[ ! -f "$input" ]]; then
    echo "ERROR: Review file not found: $input" >&2
    exit 1
  fi
  review=$(cat "$input")
fi

# Trim leading/trailing whitespace around the first line
first_line=$(echo "$review" | sed -n '1p' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')

if [[ "$first_line" == "REVIEW: PASS" ]]; then
  echo "Review passed."
  exit 0
fi

if [[ "$first_line" == "REVIEW: FAIL" ]]; then
  echo "ERROR: Review failed." >&2
  echo "$review" >&2
  exit 1
fi

echo "ERROR: Review output does not start with 'REVIEW: PASS' or 'REVIEW: FAIL'." >&2
echo "First line: $first_line" >&2
exit 1
