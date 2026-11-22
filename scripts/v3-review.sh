#!/usr/bin/env bash
# v3-review.sh runs the final adversarial review of a refactorv3 task with
# OpenAI Sol through Codex, read-only. It runs scripts/v3-check.sh first and
# gives the reviewer its output.
#
# Usage: scripts/v3-review.sh NAME BASE TASK_FILE
#
# NAME names the output directory tmp/reviews/NAME. BASE is the commit the
# task started from. TASK_FILE describes the task: its purpose, requirements,
# and, on a later round, the earlier findings and what was done with each.
#
# The findings are written to tmp/reviews/NAME/findings.json, which follows
# scripts/v3-review-schema.json.
set -euo pipefail

if (($# != 3)); then
  echo "usage: $0 NAME BASE TASK_FILE" >&2
  exit 2
fi
name=$1
base=$(git rev-parse --verify "$2^{commit}")
[[ -f $3 ]] || {
  echo "no task file $3" >&2
  exit 2
}
# Resolve the task file before changing to the repository root.
task=$(realpath "$3")
root=$(git rev-parse --show-toplevel)
cd "$root"
dir=tmp/reviews/$name
mkdir -p "$dir"
# One review per name at a time: the review files are shared.
exec 9> "$dir/.lock"
flock -n 9 || {
  echo "review $name is already running" >&2
  exit 2
}
rm -f "$dir/findings.json"

# The review relies on the check having run: it tells the reviewer to leave
# mechanical findings to it. Stop if the check could not run.
check=0
scripts/v3-check.sh > "$dir/check.txt" 2>&1 || check=$?
if ((check > 1)); then
  echo "v3-check.sh could not run; see $dir/check.txt" >&2
  exit 2
fi

{
  sed -e "s|{{BASE}}|$base|g" -e "s|{{CHECK}}|$dir/check.txt|g" -e '/{{TASK}}/,$d' scripts/v3-review-prompt.md
  echo
  cat "$task"
} > "$dir/prompt.md"

codex exec -m gpt-6.2-sol -c model_reasoning_effort='"high"' -s read-only -C "$root" --ephemeral \
  --output-schema scripts/v3-review-schema.json -o "$dir/findings.json" - \
  < "$dir/prompt.md" > "$dir/stdout.txt" 2> "$dir/stderr.txt"

# The session header names the model and settings that actually ran.
grep -E '^(model|reasoning effort|sandbox):' "$dir/stderr.txt"

# Exactly one result object, every finding complete, and a verdict that agrees
# with the findings.
if ! jq -se 'length == 1 and (.[0] | (.verdict | IN("must-findings", "no-must-findings")) and (.findings | type == "array") and all(.findings[]; (.severity | IN("MUST", "SHOULD")) and (["rule", "file", "evidence", "scenario", "fix"] - keys == []) and (.line | type == "number")) and ((.verdict == "must-findings") == any(.findings[]; .severity == "MUST")))' "$dir/findings.json" > /dev/null 2>&1; then
  echo "no valid review result in $dir/findings.json" >&2
  exit 2
fi
echo "check: $(tail -1 "$dir/check.txt")"
jq -r '"verdict: \(.verdict)", (.findings | group_by(.severity)[] | "\(.[0].severity): \(length)")' "$dir/findings.json"
