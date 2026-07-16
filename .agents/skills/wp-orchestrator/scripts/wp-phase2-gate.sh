#!/usr/bin/env bash
# Enforce Phase 2 test-migration metrics and coverage gates.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

is_phase2_gate_wp() {
  case "$1" in
    WP-16 | WP-51 | WP-52 | WP-53 | WP-54) return 0 ;;
    *) return 1 ;;
  esac
}

has_substantive_test_diff() {
  awk '
    /^(\+\+\+|---)/ { next }
    /^[+-]/ {
      line = substr($0, 2)
      if (line ~ /^[[:space:]]*$/) next
      if (line ~ /^\/\/go:build[[:space:]]/) next
      if (line ~ /^\/\/[[:space:]]+\+build[[:space:]]/) next
      found = 1
    }
    END { exit(found ? 0 : 1) }
  '
}

count_current_root_tests() {
  local wt="$1"
  local files=()
  shopt -s nullglob
  files=("$wt"/internal/server/*_test.go)
  shopt -u nullglob
  echo "${#files[@]}"
}

count_base_root_tests() {
  local wt="$1"
  git -C "$wt" ls-tree -r --name-only HEAD -- internal/server |
    awk '/^internal\/server\/[^/]+_test\.go$/ { count++ } END { print count + 0 }'
}

count_current_create_app() {
  local wt="$1"
  local scope="$2"
  local output=""
  if [[ "$scope" == "root" ]]; then
    output=$(rg -o --no-filename 'CreateApp' "$wt"/internal/server/*_test.go 2> /dev/null || true)
  else
    output=$(rg -o --no-filename -g '*_test.go' 'CreateApp' "$wt" 2> /dev/null || true)
  fi
  if [[ -z "$output" ]]; then
    echo 0
  else
    printf '%s\n' "$output" | wc -l
  fi
}

count_base_create_app() {
  local wt="$1"
  local scope="$2"
  local pathspec
  if [[ "$scope" == "root" ]]; then
    pathspec='internal/server/*_test.go'
  else
    pathspec='**/*_test.go'
  fi
  local output
  output=$(git -C "$wt" grep -o 'CreateApp' HEAD -- "$pathspec" 2> /dev/null || true)
  if [[ -z "$output" ]]; then
    echo 0
  else
    printf '%s\n' "$output" | wc -l
  fi
}

check_modified_root_tests() {
  local wt="$1"
  local issues=0
  local status old_path new_path

  while IFS=$'\t' read -r status old_path new_path; do
    [[ -n "$status" ]] || continue
    case "$status" in
      M)
        if [[ "$old_path" =~ ^internal/server/[^/]+_test\.go$ ]] &&
          ! git -C "$wt" diff --unified=0 HEAD -- "$old_path" | has_substantive_test_diff; then
          echo "ERROR: Tag-only root test change is forbidden: $old_path" >&2
          issues=1
        fi
        ;;
      R*)
        if [[ "$old_path" =~ ^internal/server/[^/]+_test\.go$ &&
          "$new_path" =~ ^internal/server/[^/]+_test\.go$ ]]; then
          echo "ERROR: Rename-in-place does not migrate a root test: $old_path -> $new_path" >&2
          issues=1
        fi
        ;;
    esac
  done < <(git -C "$wt" diff --name-status HEAD)

  return "$issues"
}

write_phase2_evidence() {
  local wt="$1"
  local wp="$2"
  local base_root="$3"
  local current_root="$4"
  local base_root_create="$5"
  local current_root_create="$6"
  local base_all_create="$7"
  local current_all_create="$8"
  local artifact_prefix="${wp,,}"

  mkdir -p "$wt/tmp"
  {
    echo "base_root_test_files=$base_root"
    echo "current_root_test_files=$current_root"
    echo "base_root_create_app=$base_root_create"
    echo "current_root_create_app=$current_root_create"
    echo "base_repository_create_app=$base_all_create"
    echo "current_repository_create_app=$current_all_create"
  } > "$wt/tmp/$artifact_prefix-metrics.txt"

  {
    git -C "$wt" diff --name-status HEAD -- internal/server
    git -C "$wt" ls-files --others --exclude-standard -- internal/server |
      awk '{ print "A\t" $0 }'
  } > "$wt/tmp/$artifact_prefix-moves.txt"
}

run_coverage_gate() {
  local wt="$1"
  local wp="$2"
  local max_zero=1
  local artifact_prefix="${wp,,}"
  local out_dir="$wt/tmp"

  mkdir -p "$out_dir"

  if ! (
    cd "$wt"
    go test -count=1 -coverpkg=./internal/server/... \
      -coverprofile="tmp/$artifact_prefix-coverage.out" ./internal/server/... \
      > "tmp/$artifact_prefix-coverage-test.txt" 2>&1 &&
      go test -count=1 -tags=integration -coverpkg=./internal/server/... \
      -coverprofile="tmp/$artifact_prefix-coverage-integration.out" ./internal/server/... \
      > "tmp/$artifact_prefix-coverage-integration-test.txt" 2>&1
  ); then
    echo "ERROR: Phase 2 coverage test command failed; inspect tmp/$artifact_prefix-coverage*-test.txt" >&2
    return 1
  fi

  local default_zero="$out_dir/$artifact_prefix-zero-default.txt"
  local integration_zero="$out_dir/$artifact_prefix-zero-integration.txt"
  go tool cover -func="$out_dir/$artifact_prefix-coverage.out" |
    awk '$1 ~ /internal\/server\// && $3 == "0.0%"' | sort > "$default_zero"
  go tool cover -func="$out_dir/$artifact_prefix-coverage-integration.out" |
    awk '$1 ~ /internal\/server\// && $3 == "0.0%"' | sort > "$integration_zero"

  local default_count integration_count
  default_count=$(wc -l < "$default_zero")
  integration_count=$(wc -l < "$integration_zero")
  {
    echo "maximum_allowed_0_percent_functions=$max_zero"
    echo "default_0_percent_functions=$default_count"
    echo "integration_0_percent_functions=$integration_count"
    echo ""
    echo "=== Default uncovered functions ==="
    while IFS= read -r line; do
      printf '%s\n' "$line"
    done < "$default_zero"
    echo ""
    echo "=== Integration uncovered functions ==="
    while IFS= read -r line; do
      printf '%s\n' "$line"
    done < "$integration_zero"
  } > "$out_dir/$artifact_prefix-cover-diff.txt"

  if ((default_count > max_zero || integration_count > max_zero)); then
    echo "ERROR: Coverage regression: default=$default_count integration=$integration_count; maximum=$max_zero" >&2
    return 1
  fi
}

run_phase2_gate() {
  local wt="$1"
  local wp="$2"
  local structural_only="$3"
  local write_evidence="${4:-1}"
  local issues=0

  if ! is_phase2_gate_wp "$wp"; then
    return 0
  fi

  local base_root current_root base_root_create current_root_create base_all_create current_all_create
  base_root=$(count_base_root_tests "$wt")
  current_root=$(count_current_root_tests "$wt")
  base_root_create=$(count_base_create_app "$wt" root)
  current_root_create=$(count_current_create_app "$wt" root)
  base_all_create=$(count_base_create_app "$wt" all)
  current_all_create=$(count_current_create_app "$wt" all)

  if [[ "$write_evidence" -eq 1 ]]; then
    write_phase2_evidence \
      "$wt" "$wp" \
      "$base_root" "$current_root" \
      "$base_root_create" "$current_root_create" \
      "$base_all_create" "$current_all_create"
  fi

  if ! check_modified_root_tests "$wt"; then
    issues=1
  fi

  if [[ "$wp" =~ ^WP-(52|54)$ ]] && ((current_root >= base_root)); then
    echo "ERROR: $wp must reduce root test files (base=$base_root current=$current_root)" >&2
    issues=1
  fi

  if [[ "$wp" == "WP-53" &&
    "$current_root" -ge "$base_root" &&
    "$current_root_create" -ge "$base_root_create" ]]; then
    echo "ERROR: WP-53 must reduce root tests or root CreateApp references" >&2
    issues=1
  fi

  if [[ "$wp" == "WP-51" && current_root -gt base_root ]]; then
    echo "ERROR: WP-51 coverage recovery must not add root test files (base=$base_root current=$current_root)" >&2
    issues=1
  fi

  if ((current_all_create > base_all_create)); then
    echo "ERROR: Repository-wide CreateApp references increased (base=$base_all_create current=$current_all_create)" >&2
    issues=1
  fi

  if [[ "$wp" =~ ^WP-5[2-4]$ && ! -s "$wt/docs/phase2-test-ownership.md" ]]; then
    echo "ERROR: $wp requires tracked docs/phase2-test-ownership.md" >&2
    issues=1
  fi

  if [[ "$wp" == "WP-54" && ! -s "$wt/docs/phase2-test-merge-map.md" ]]; then
    echo "ERROR: WP-54 requires tracked docs/phase2-test-merge-map.md" >&2
    issues=1
  fi

  if [[ "$wp" == "WP-54" || "$wp" == "WP-16" ]]; then
    if ((current_root > 20)); then
      echo "ERROR: Phase 2 root test target missed: $current_root > 20" >&2
      issues=1
    fi
    if ((current_root_create > 70)); then
      echo "ERROR: Phase 2 root CreateApp target missed: $current_root_create > 70" >&2
      issues=1
    fi
    if ((current_all_create > 110)); then
      echo "ERROR: Phase 2 repository CreateApp target missed: $current_all_create > 110" >&2
      issues=1
    fi
  fi

  if [[ "$structural_only" -eq 0 ]] && ! run_coverage_gate "$wt" "$wp"; then
    issues=1
  fi

  return "$issues"
}

usage() {
  echo "Usage: wp-phase2-gate.sh --worktree DIR [--structural-only] [--no-evidence] WP-N"
  exit 1
}

main() {
  local wt=""
  local wp=""
  local structural_only=0
  local write_evidence=1

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --worktree)
        [[ $# -ge 2 ]] || usage
        wt="$2"
        shift 2
        ;;
      --structural-only)
        structural_only=1
        shift
        ;;
      --no-evidence)
        write_evidence=0
        shift
        ;;
      -h | --help)
        usage
        ;;
      *)
        [[ -z "$wp" ]] || usage
        wp="$1"
        shift
        ;;
    esac
  done

  [[ -n "$wt" && -d "$wt" && -n "$wp" ]] || usage
  wp=$(normalize_wp "$wp")
  run_phase2_gate "$wt" "$wp" "$structural_only" "$write_evidence"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
