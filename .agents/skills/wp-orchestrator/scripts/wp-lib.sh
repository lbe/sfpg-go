#!/usr/bin/env bash
# Shared library for wp-orchestrator scripts.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SKILL_DIR="$(dirname "$SCRIPT_DIR")"
# Resolve the main repository root, even when this script is invoked from a linked worktree.
git_common_dir=$(git -C "$SCRIPT_DIR" rev-parse --git-common-dir 2>/dev/null || true)
if [[ -n "$git_common_dir" && "$git_common_dir" = /* ]]; then
  REPO_ROOT="$(cd "$git_common_dir/.." && pwd)"
else
  REPO_ROOT="$(cd "$SKILL_DIR/../../.." && pwd)"
fi

# Convert any WP identifier form to canonical uppercase WP-N.
normalize_wp() {
  local input="$1"
  local normalized
  normalized=$(echo "$input" | tr '[:lower:]' '[:upper:]')
  if [[ ! "$normalized" =~ ^WP-[0-9]+$ ]]; then
    echo "ERROR: Invalid WP identifier: $input (expected WP-N)" >&2
    return 1
  fi
  echo "$normalized"
}

# Resolve plan file path. Accepts explicit path or newest tmp/plan-remediation-*.md.
find_plan() {
  local explicit="${1:-}"
  if [[ -n "$explicit" ]]; then
    if [[ ! -f "$explicit" ]]; then
      echo "ERROR: Plan file not found: $explicit" >&2
      return 1
    fi
    echo "$explicit"
    return 0
  fi
  local newest
  newest=$(ls -1 "$REPO_ROOT"/tmp/plan-remediation-*.md 2> /dev/null | sort | tail -n1 || true)
  if [[ -z "$newest" || ! -f "$newest" ]]; then
    echo "ERROR: No plan-remediation file found in tmp/" >&2
    return 1
  fi
  echo "$newest"
}

# Extract the WP section as a block of text starting with "#### WP-N".
wp_section() {
  local plan="$1"
  local wp="$2"
  awk -v wp="$wp" '
        /^#### / { in_section=0 }
        /^#### WP-[0-9]+/ {
            match($0, /^#### WP-[0-9]+/)
            section_wp = substr($0, RSTART, RLENGTH)
            gsub(/^#### +/, "", section_wp)
            if (section_wp == wp) in_section=1
        }
        in_section { print }
    ' "$plan"
}

wp_status() {
  local plan="$1"
  local wp="$2"
  local section
  section=$(wp_section "$plan" "$wp")
  local status
  status=$(echo "$section" | grep -m1 '^\*\*Status:\*\*' | sed -E "s/.*\*\*Status:\*\* \`([^\`]+)\`.*/\1/")
  if [[ -z "$status" ]]; then
    echo "ERROR: Could not determine status for $wp" >&2
    return 1
  fi
  echo "$status"
}

wp_phase() {
  local plan="$1"
  local wp="$2"
  local section
  section=$(wp_section "$plan" "$wp")
  echo "$section" | grep -m1 '^\*\*Phase:\*\*' | sed -E "s/.*\*\*Phase:\*\* ([^·]+).*/\1/" | tr -d ' '
}

# Extract the short title of the WP (text after the bullet).
wp_title() {
  local plan="$1"
  local wp="$2"
  local header
  header=$(grep -m1 "^#### $wp" "$plan" || true)
  if [[ -z "$header" ]]; then
    return 0
  fi
  # Remove "#### WP-N · " prefix and trim
  echo "$header" | sed -E "s/^#### $wp *· *//" | sed -E 's/ *\(.*\).*//'
}

# Generate branch name: wp-N-short-desc (lowercase, safe chars).
wp_branch_name() {
  local plan="$1"
  local wp="$2"
  local num
  num=$(echo "$wp" | grep -oE '[0-9]+')
  local title
  title=$(wp_title "$plan" "$wp" | tr '[:upper:]' '[:lower:]')
  # Replace non-alphanumeric with hyphens, collapse multiple hyphens, trim
  local desc
  desc=$(echo "$title" | sed -E 's/[^a-z0-9]+/-/g' | sed -E 's/-+/-/g' | sed -E 's/^-|-$//g')
  # Limit length
  if [[ -n "$desc" ]]; then
    desc="-${desc:0:50}"
  fi
  echo "wp-${num}${desc}"
}

# Worktree directory name (lowercase wp-N).
wp_worktree_dir() {
  local wp="$1"
  local num
  num=$(echo "$wp" | grep -oE '[0-9]+')
  echo ".worktrees/wp-${num}"
}

# Phase 4 template WPs run in the main worktree (WP-39 … WP-42 or phase 4A–4D).
wp_is_phase4() {
  local plan="$1"
  local wp="$2"
  local num
  num=$(echo "$wp" | grep -oE '[0-9]+')
  if [[ "$num" -ge 39 && "$num" -le 42 ]]; then
    return 0
  fi
  local phase
  phase=$(wp_phase "$plan" "$wp")
  if [[ "$phase" == "4" || "$phase" == "4A" || "$phase" == "4B" || "$phase" == "4C" || "$phase" == "4D" ]]; then
    return 0
  fi
  return 1
}

# WPs that run on a feature branch in the main worktree (air serves localhost:8083).
# Includes Phase 4 templates, e2e/e2eweb dev-server WPs (48, 50), and explicit plan markers.
wp_uses_main_worktree() {
  local plan="$1"
  local wp="$2"
  if wp_is_phase4 "$plan" "$wp"; then
    return 0
  fi
  local num
  num=$(echo "$wp" | grep -oE '[0-9]+')
  case "$num" in
    48 | 50) return 0 ;;
  esac
  local section
  section=$(wp_section "$plan" "$wp")
  if echo "$section" | grep -q '\*\*Execute in:\*\* main worktree'; then
    return 0
  fi
  return 1
}

# Extract raw dependency line from WP section.
wp_deps_raw() {
  local plan="$1"
  local wp="$2"
  local section
  section=$(wp_section "$plan" "$wp")
  echo "$section" | grep -m1 '\*\*Depends on:\*\*' || true
}

# Extract hard dependencies (blockers). Defaults to hard when no marker is present.
wp_deps() {
  local raw
  raw=$(wp_deps_raw "$1" "$2")
  if [[ -z "$raw" ]]; then
    return 0
  fi
  echo "$raw" | awk -F';' '
        {
            for (i = 1; i <= NF; i++) {
                group = $i
                sub(/^\*\*Depends on:\*\* */, "", group)
                type = "hard"
                if (group ~ /\(soft/) type = "soft"
                while (match(group, /WP-[0-9]+/)) {
                    wp = substr(group, RSTART, RLENGTH)
                    after = RSTART + RLENGTH
                    if (after <= length(group)) {
                        next_char = substr(group, after, 1)
                        if (next_char ~ /[a-zA-Z0-9]/) {
                            group = substr(group, after)
                            continue
                        }
                    }
                    print type, wp
                    group = substr(group, after)
                }
            }
        }
    ' | awk '$1 == "hard" { print $2 }' | sort -u
}

# Extract soft/parallel dependencies.
wp_soft_deps() {
  local raw
  raw=$(wp_deps_raw "$1" "$2")
  if [[ -z "$raw" ]]; then
    return 0
  fi
  echo "$raw" | awk -F';' '
        {
            for (i = 1; i <= NF; i++) {
                group = $i
                sub(/^\*\*Depends on:\*\* */, "", group)
                type = "hard"
                if (group ~ /\(soft/) type = "soft"
                while (match(group, /WP-[0-9]+/)) {
                    wp = substr(group, RSTART, RLENGTH)
                    after = RSTART + RLENGTH
                    if (after <= length(group)) {
                        next_char = substr(group, after, 1)
                        if (next_char ~ /[a-zA-Z0-9]/) {
                            group = substr(group, after)
                            continue
                        }
                    }
                    print type, wp
                    group = substr(group, after)
                }
            }
        }
    ' | awk '$1 == "soft" { print $2 }' | sort -u
}

# Extract file paths mentioned in the WP's tables.
wp_files() {
  local plan="$1"
  local wp="$2"
  local section
  section=$(wp_section "$plan" "$wp")
  echo "$section" | awk '
        /^\|/ {
            gsub(/\`/, "")
            gsub(/^\| *| *\| *$/, "")
            split($0, cols, "|")
            for (i in cols) {
                col = cols[i]
                gsub(/^[ \t]+|[ \t]+$/, "", col)
                if (col ~ /^(internal|cmd|web|scripts|migrations|docs)\//) {
                    print col
                }
            }
        }
    ' | sort -u
}

# Check if main worktree is ready to start a WP: on branch main, clean except version.go.
main_is_clean() {
  cd "$REPO_ROOT"
  if ! git rev-parse --abbrev-ref HEAD | grep -q '^main$'; then
    echo "ERROR: Not on main branch. Checkout main before starting a WP." >&2
    return 1
  fi
  local dirty
  dirty=$(git status --porcelain | awk '$1 != "??" && $2 != "version.go" { print }' || true)
  if [[ -n "$dirty" ]]; then
    echo "ERROR: Main worktree has unexpected tracked changes:" >&2
    echo "$dirty" >&2
    return 1
  fi
  return 0
}

# Output the absolute path to the plan file.
plan_abs_path() {
  local plan="$1"
  if [[ "$plan" = /* ]]; then
    echo "$plan"
  else
    echo "$REPO_ROOT/$plan"
  fi
}
