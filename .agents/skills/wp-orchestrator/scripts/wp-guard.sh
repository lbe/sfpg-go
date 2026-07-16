#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

PLAN_FILE=""
MODE=""
WPS=()
DRY_RUN=0

dry_run_note() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "[DRY-RUN] $*"
  fi
}

usage() {
  echo "Usage: wp-guard.sh --plan FILE [--dry-run] --create wp-1 [wp-2 ...]"
  echo "       wp-guard.sh --plan FILE --verify wp-1 [wp-2 ...]"
  echo "       wp-guard.sh --plan FILE --deps wp-N          # list hard deps"
  echo "       wp-guard.sh --plan FILE --soft-deps wp-N     # list soft/parallel deps"
  echo "       wp-guard.sh --plan FILE --check-deps wp-N    # verify hard deps done"
  echo "       wp-guard.sh --plan FILE --overlap wp-1 [wp-2 ...]"
  echo "       wp-guard.sh --plan FILE --overlap-git wp-1 [wp-2 ...]"
  echo "       wp-guard.sh --plan FILE --status wp-N"
  exit 1
}

create_one_worktree() {
  local plan="$1"
  local wp="$2"
  local wt
  wt=$(wp_worktree_dir "$wp")
  local branch
  branch=$(wp_branch_name "$plan" "$wp")

  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "[DRY-RUN] Would create branch $branch and worktree $wt for $wp"
    return
  fi

  if git show-ref --verify --quiet "refs/heads/$branch"; then
    echo "Branch $branch already exists."
  else
    echo "Creating branch $branch from main..."
    git branch "$branch" main
  fi

  if [[ -d "$wt" ]]; then
    echo "Worktree $wt already exists."
    local actual_branch
    actual_branch=$(git -C "$wt" rev-parse --abbrev-ref HEAD)
    if [[ "$actual_branch" != "$branch" ]]; then
      echo "ERROR: Worktree $wt is on branch $actual_branch, expected $branch" >&2
      exit 1
    fi
  else
    echo "Creating worktree $wt for branch $branch..."
    git worktree add "$wt" "$branch"
    echo "Initializing submodules in $wt..."
    git -C "$wt" submodule update --init --recursive > /dev/null 2>&1 || true
  fi
}

create_one_main() {
  local plan="$1"
  local wp="$2"
  local branch
  branch=$(wp_branch_name "$plan" "$wp")

  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "[DRY-RUN] Would create branch $branch in main worktree for $wp"
    return
  fi

  if git show-ref --verify --quiet "refs/heads/$branch"; then
    echo "Branch $branch already exists."
  else
    echo "Creating branch $branch from main..."
    git branch "$branch" main
  fi

  local current_branch
  current_branch=$(git rev-parse --abbrev-ref HEAD)
  if [[ "$current_branch" != "$branch" ]]; then
    echo "Switching main worktree to branch $branch..."
    git checkout "$branch"
  fi
}

cmd_create() {
  local plan="$1"
  shift
  local wps=("$@")

  if [[ "$DRY_RUN" -eq 0 ]]; then
    main_is_clean
  else
    echo "[DRY-RUN] Would check main is clean (except version.go)."
  fi

  cd "$REPO_ROOT"

  # If a previous Phase 4 WP left the main worktree on its feature branch,
  # switch back to main before creating the next branch/worktree.
  local current_branch
  current_branch=$(git rev-parse --abbrev-ref HEAD)
  if [[ "$current_branch" != "main" ]]; then
    if [[ "$DRY_RUN" -eq 0 ]]; then
      echo "Main worktree is on $current_branch; switching back to main..."
      git checkout main
    else
      echo "[DRY-RUN] Would switch main worktree from $current_branch back to main."
    fi
  fi

  for wp in "${wps[@]}"; do
    if wp_uses_main_worktree "$plan" "$wp"; then
      create_one_main "$plan" "$wp"
    else
      create_one_worktree "$plan" "$wp"
    fi
  done
}

cmd_verify() {
  local plan="$1"
  shift
  local wps=("$@")

  cd "$REPO_ROOT"

  local verify_worktree_only=1
  for wp in "${wps[@]}"; do
    if wp_uses_main_worktree "$plan" "$wp"; then
      verify_worktree_only=0
      break
    fi
  done

  local current_branch
  current_branch=$(git rev-parse --abbrev-ref HEAD)

  # Worktree-only verify: require a clean main checkout only when main is still on
  # branch main (detects leaks from isolated worktrees). Skip when main is on a
  # feature branch because a main-worktree WP may be active in parallel.
  if [[ "$verify_worktree_only" -eq 1 && "$current_branch" == "main" ]]; then
    local main_dirty
    main_dirty=$(git status --porcelain | awk '$1 != "??" && $2 != "version.go" { print }' || true)
    if [[ -n "$main_dirty" ]]; then
      echo "ERROR: Main worktree has unexpected tracked changes:" >&2
      echo "$main_dirty" >&2
      exit 1
    fi
  fi

  for wp in "${wps[@]}"; do
    if wp_uses_main_worktree "$plan" "$wp"; then
      local branch
      branch=$(wp_branch_name "$plan" "$wp")
      if [[ "$current_branch" != "$branch" ]]; then
        echo "ERROR: Main worktree is on branch $current_branch, expected $branch for $wp" >&2
        exit 1
      fi
      echo "Main-worktree WP $wp verified on branch $branch."
      continue
    fi

    local wt
    wt=$(wp_worktree_dir "$wp")
    local branch
    branch=$(wp_branch_name "$plan" "$wp")
    if [[ ! -d "$wt" ]]; then
      echo "ERROR: Worktree $wt does not exist" >&2
      exit 1
    fi
    local actual_branch
    actual_branch=$(git -C "$wt" rev-parse --abbrev-ref HEAD)
    if [[ "$actual_branch" != "$branch" ]]; then
      echo "ERROR: Worktree $wt is on branch $actual_branch, expected $branch" >&2
      exit 1
    fi
  done

  echo "Boundary OK: main untouched, worktrees/branches valid."
}

cmd_list_deps() {
  local plan="$1"
  shift
  local wp
  wp=$(normalize_wp "$1")
  wp_deps "$plan" "$wp"
}

cmd_list_soft_deps() {
  local plan="$1"
  shift
  local wp
  wp=$(normalize_wp "$1")
  wp_soft_deps "$plan" "$wp"
}

cmd_check_deps() {
  local plan="$1"
  shift
  local wp
  wp=$(normalize_wp "$1")
  local status
  status=$(wp_status "$plan" "$wp")
  if [[ "$status" == "done" ]]; then
    echo "$wp is already done."
    exit 0
  fi
  if [[ "$status" == "on hold" ]]; then
    echo "ERROR: $wp is on hold and cannot be started." >&2
    exit 1
  fi

  local deps
  deps=$(wp_deps "$plan" "$wp" || true)
  local missing=()
  for dep in $deps; do
    local dep_status
    dep_status=$(wp_status "$plan" "$dep")
    if [[ "$dep_status" != "done" && "$dep_status" != "on hold" ]]; then
      missing+=("$dep ($dep_status)")
    fi
  done

  if [[ ${#missing[@]} -gt 0 ]]; then
    echo "ERROR: $wp has unresolved dependencies:" >&2
    for m in "${missing[@]}"; do
      echo "  - $m" >&2
    done
    exit 1
  fi

  echo "$wp dependencies satisfied."
}

cmd_overlap() {
  local plan="$1"
  shift
  local wps=("$@")

  if [[ ${#wps[@]} -lt 2 ]]; then
    echo "Overlap check needs at least two WPs."
    exit 0
  fi

  declare -A file_to_wp
  local overlap=0

  for wp in "${wps[@]}"; do
    local files
    files=$(wp_files "$plan" "$wp" || true)
    for f in $files; do
      if [[ -n "${file_to_wp[$f]:-}" ]]; then
        echo "OVERLAP: $f touched by ${file_to_wp[$f]} and $wp"
        overlap=1
      else
        file_to_wp[$f]="$wp"
      fi
    done
  done

  if [[ "$overlap" -eq 1 ]]; then
    echo "ERROR: File overlap detected. Run sequentially." >&2
    exit 1
  fi

  echo "No file overlap detected."
}

cmd_overlap_git() {
  local plan="$1"
  shift
  local wps=("$@")

  if [[ ${#wps[@]} -lt 2 ]]; then
    echo "Git overlap check needs at least two WPs."
    exit 0
  fi

  declare -A file_to_wp
  local overlap=0

  for wp in "${wps[@]}"; do
    local wt
    wt=$(wp_worktree_dir "$wp")
    local diff_files
    if wp_uses_main_worktree "$plan" "$wp"; then
      diff_files=$(git -C "$REPO_ROOT" diff --name-only HEAD || true)
    else
      diff_files=$(git -C "$wt" diff --name-only HEAD || true)
    fi
    for f in $diff_files; do
      if [[ -n "${file_to_wp[$f]:-}" ]]; then
        echo "OVERLAP: $f changed in ${file_to_wp[$f]} and $wp"
        overlap=1
      else
        file_to_wp[$f]="$wp"
      fi
    done
  done

  if [[ "$overlap" -eq 1 ]]; then
    echo "ERROR: Git diff overlap detected. Run sequentially." >&2
    exit 1
  fi

  echo "No git diff overlap detected."
}

cmd_status() {
  local plan="$1"
  shift
  local wp
  wp=$(normalize_wp "$1")
  wp_status "$plan" "$wp"
}

# Argument parsing
while [[ $# -gt 0 ]]; do
  case "$1" in
    --plan)
      PLAN_FILE="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --create | --verify | --overlap | --overlap-git)
      MODE="${1#--}"
      shift
      while [[ $# -gt 0 && ! "$1" =~ ^-- ]]; do
        WPS+=("$(normalize_wp "$1")")
        shift
      done
      ;;
    --deps | --soft-deps | --check-deps | --status)
      MODE="${1#--}"
      shift
      WPS+=("$(normalize_wp "$1")")
      shift
      ;;
    -h | --help)
      usage
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      ;;
  esac
done

if [[ -z "$MODE" || ${#WPS[@]} -eq 0 ]]; then
  usage
fi

PLAN_FILE=$(find_plan "${PLAN_FILE:-}")

case "$MODE" in
  create)
    cmd_create "$PLAN_FILE" "${WPS[@]}"
    ;;
  verify)
    cmd_verify "$PLAN_FILE" "${WPS[@]}"
    ;;
  deps)
    cmd_list_deps "$PLAN_FILE" "${WPS[@]}"
    ;;
  soft-deps)
    cmd_list_soft_deps "$PLAN_FILE" "${WPS[@]}"
    ;;
  check-deps)
    cmd_check_deps "$PLAN_FILE" "${WPS[@]}"
    ;;
  overlap)
    cmd_overlap "$PLAN_FILE" "${WPS[@]}"
    ;;
  overlap-git)
    cmd_overlap_git "$PLAN_FILE" "${WPS[@]}"
    ;;
  status)
    cmd_status "$PLAN_FILE" "${WPS[@]}"
    ;;
  *)
    usage
    ;;
esac
