#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=wp-lib.sh
source "$SCRIPT_DIR/wp-lib.sh"

PLAN_FILE=""
WPS=()

usage() {
  echo "Usage: wp-analyze.sh --plan FILE wp-1 [wp-2 ...]"
  exit 1
}

# Topological sort of WPs based on hard dependencies
topo_sort() {
  local plan="$1"
  shift
  local targets=("$@")
  local -A deps_map
  local -A done_set
  local order=()

  local queue=("${targets[@]}")
  local all=()
  while [[ ${#queue[@]} -gt 0 ]]; do
    local wp="${queue[0]}"
    queue=("${queue[@]:1}")
    if [[ -n "${done_set[$wp]:-}" ]]; then
      continue
    fi
    done_set[$wp]=1
    all+=("$wp")
    local deps
    deps=$(wp_deps "$plan" "$wp" || true)
    for d in $deps; do
      queue+=("$d")
    done
  done

  for wp in "${all[@]}"; do
    local deps
    deps=$(wp_deps "$plan" "$wp" || true)
    deps_map[$wp]="$deps"
  done

  local -a in_degree
  local -a nodes
  declare -A idx
  local i=0
  for wp in "${all[@]}"; do
    nodes+=("$wp")
    idx[$wp]=$i
    in_degree[$i]=0
    i=$((i + 1))
  done

  for wp in "${all[@]}"; do
    local wid=${idx[$wp]}
    for d in ${deps_map[$wp]}; do
      if [[ -n "${idx[$d]:-}" ]]; then
        in_degree[$wid]=$((${in_degree[$wid]} + 1))
      fi
    done
  done

  local -a queue2=()
  for j in "${!nodes[@]}"; do
    if [[ ${in_degree[$j]} -eq 0 ]]; then
      queue2+=("${nodes[$j]}")
    fi
  done

  while [[ ${#queue2[@]} -gt 0 ]]; do
    local wp="${queue2[0]}"
    queue2=("${queue2[@]:1}")
    order+=("$wp")
    for other in "${nodes[@]}"; do
      for d in ${deps_map[$other]}; do
        if [[ "$d" == "$wp" ]]; then
          local oid=${idx[$other]}
          in_degree[$oid]=$((${in_degree[$oid]} - 1))
          if [[ ${in_degree[$oid]} -eq 0 ]]; then
            queue2+=("$other")
          fi
        fi
      done
    done
  done

  if [[ ${#order[@]} -ne ${#all[@]} ]]; then
    echo "ERROR: Dependency cycle detected among ${all[*]}" >&2
    exit 1
  fi

  printf '%s\n' "${order[@]}"
}

# Group independent WPs into waves for parallel execution
group_waves() {
  local plan="$1"
  shift
  local order=("$@")
  local -A completed
  local -A deps_map

  for wp in "${order[@]}"; do
    local deps
    deps=$(wp_deps "$plan" "$wp" || true)
    deps_map[$wp]="$deps"
  done

  local remaining=("${order[@]}")
  local wave_num=1

  while [[ ${#remaining[@]} -gt 0 ]]; do
    local wave=()
    local next_remaining=()

    for wp in "${remaining[@]}"; do
      local ready=1
      for d in ${deps_map[$wp]}; do
        if [[ -z "${completed[$d]:-}" ]]; then
          ready=0
          break
        fi
      done
      if [[ "$ready" -eq 1 ]]; then
        wave+=("$wp")
      else
        next_remaining+=("$wp")
      fi
    done

    if [[ ${#wave[@]} -eq 0 ]]; then
      echo "ERROR: Unable to schedule remaining WPs: ${remaining[*]}" >&2
      exit 1
    fi

    echo "wave-$wave_num: ${wave[*]}"
    for wp in "${wave[@]}"; do
      completed[$wp]=1
    done
    remaining=("${next_remaining[@]}")
    wave_num=$((wave_num + 1))
  done
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
      WPS+=("$1")
      shift
      ;;
  esac
done

if [[ ${#WPS[@]} -eq 0 ]]; then
  usage
fi

PLAN_FILE=$(find_plan "${PLAN_FILE:-}")

# Normalize and deduplicate
declare -A seen
CLEAN_WPS=()
for w in "${WPS[@]}"; do
  cw=$(normalize_wp "$w") || exit 1
  if [[ -z "${seen[$cw]:-}" ]]; then
    seen[$cw]=1
    CLEAN_WPS+=("$cw")
  fi
done

echo "=== Requested WPs ==="
printf '%s\n' "${CLEAN_WPS[@]}"

echo ""
echo "=== Dependency order ==="
ORDER=$(topo_sort "$PLAN_FILE" "${CLEAN_WPS[@]}")
echo "$ORDER"

echo ""
echo "=== Execution waves ==="
mapfile -t ORDER_ARRAY <<< "$ORDER"
group_waves "$PLAN_FILE" "${ORDER_ARRAY[@]}"

echo ""
echo "=== Overlap check for requested WPs ==="
"$SCRIPT_DIR/wp-guard.sh" --plan "$PLAN_FILE" --overlap "${CLEAN_WPS[@]}" || true

echo ""
echo "=== Main worktree detection ==="
for wp in "${CLEAN_WPS[@]}"; do
  if wp_uses_main_worktree "$PLAN_FILE" "$wp"; then
    echo "$wp: main worktree (feature branch on main checkout; air on :8083)"
  fi
done
