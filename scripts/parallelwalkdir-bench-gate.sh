#!/bin/bash
# Alloc gate for parallelwalkdir discovery walk (plan-discovery-reported-path-arena).
# Requires SFPG_WALK_BENCH_ROOT — no default path.
#
# Usage:
#   export SFPG_WALK_BENCH_ROOT=/path/to/Images
#   scripts/parallelwalkdir-bench-gate.sh
set -euo pipefail

root="${SFPG_WALK_BENCH_ROOT:-}"
if [[ -z "$root" ]] || [[ ! -r "$root" ]]; then
  echo 'SFPG_WALK_BENCH_ROOT unset or not readable' >&2
  exit 1
fi

cd "$(dirname "$0")/.."
mkdir -p tmp

out=tmp/parallelwalkdir_bench_after.txt
printf '%s\n' 'discovery_path_arena_gate=1201 allocs/op median' > "$out"

SFPG_WALK_BENCH_ROOT="$root" go test ./internal/parallelwalkdir/ -timeout=0 -run '^$' \
  -bench='BenchmarkParallelWalkDiscoveryImages$' -benchmem -benchtime=1s -count=5 \
  >> "$out" 2>&1

bench_lines=$(grep -E 'BenchmarkParallelWalkDiscoveryImages-[0-9]+' "$out" | grep -c 'allocs/op' || true)
if [[ "$bench_lines" -ne 5 ]]; then
  echo "expected 5 bench lines with allocs/op, got $bench_lines" >&2
  exit 1
fi

file_lines=$(grep -E 'BenchmarkParallelWalkDiscoveryImages-[0-9]+' "$out" | grep -c '3314 files' || true)
if [[ "$file_lines" -ne 5 ]]; then
  echo "expected 3314 files on each of 5 bench lines, got $file_lines matching lines" >&2
  exit 1
fi

mapfile -t allocs < <(
  grep -E 'BenchmarkParallelWalkDiscoveryImages-[0-9]+' "$out" | grep 'allocs/op' \
    | awk '{for (i = 1; i <= NF; i++) if ($i == "allocs/op") print $(i - 1)}' | sort -n
)
median="${allocs[2]}"
gate=1201

if [[ "$median" -lt "$gate" ]]; then
  printf 'median_allocs_op=%s gate=%s pass\n' "$median" "$gate" >> "$out"
  echo "median_allocs_op=$median pass (gate $gate)"
else
  printf 'median_allocs_op=%s gate=%s FAIL\n' "$median" "$gate" >> "$out"
  echo "median_allocs_op=$median FAIL (gate $gate)" >&2
  exit 1
fi

printf '%s\n' 'dirent_unchanged_gate=501 allocs/op median' >> "$out"

SFPG_WALK_BENCH_ROOT="$root" go test ./internal/parallelwalkdir/ -timeout=0 -run '^$' \
  -bench='BenchmarkParallelWalkDiscoveryImagesDirEntUnchanged$' -benchmem -benchtime=1s -count=5 \
  >> "$out" 2>&1

unchanged_bench_lines=$(
  grep -E 'BenchmarkParallelWalkDiscoveryImagesDirEntUnchanged-[0-9]+' "$out" | grep -c 'allocs/op' || true
)
if [[ "$unchanged_bench_lines" -ne 5 ]]; then
  echo "expected 5 DirEnt unchanged bench lines with allocs/op, got $unchanged_bench_lines" >&2
  exit 1
fi

unchanged_file_lines=$(
  grep -E 'BenchmarkParallelWalkDiscoveryImagesDirEntUnchanged-[0-9]+' "$out" | grep -c '0 files' || true
)
if [[ "$unchanged_file_lines" -ne 5 ]]; then
  echo "expected 0 files on each of 5 DirEnt unchanged bench lines, got $unchanged_file_lines matching lines" >&2
  exit 1
fi

mapfile -t unchanged_allocs < <(
  grep -E 'BenchmarkParallelWalkDiscoveryImagesDirEntUnchanged-[0-9]+' "$out" | grep 'allocs/op' \
    | awk '{for (i = 1; i <= NF; i++) if ($i == "allocs/op") print $(i - 1)}' | sort -n
)
unchanged_median="${unchanged_allocs[2]}"
unchanged_gate=501

if [[ "$unchanged_median" -lt "$unchanged_gate" ]]; then
  printf 'dirent_unchanged_median_allocs_op=%s gate=%s pass\n' "$unchanged_median" "$unchanged_gate" >> "$out"
  echo "dirent_unchanged_median_allocs_op=$unchanged_median pass (gate $unchanged_gate)"
else
  printf 'dirent_unchanged_median_allocs_op=%s gate=%s FAIL\n' "$unchanged_median" "$unchanged_gate" >> "$out"
  echo "dirent_unchanged_median_allocs_op=$unchanged_median FAIL (gate $unchanged_gate)" >&2
  exit 1
fi

SFPG_WALK_BENCH_ROOT="$root" go test ./internal/server -run=^$ -bench=BenchmarkDiscoveryWalk_memoryEnqueue -benchmem -benchtime=1s -count=1 \
  >> "$out" 2>&1
SFPG_WALK_BENCH_ROOT="$root" go test ./internal/server -run=^$ -bench=BenchmarkDiscoveryWalk_diskDqueEnqueueOnly -benchmem -benchtime=1s -count=1 \
  >> "$out" 2>&1
SFPG_WALK_BENCH_ROOT="$root" go test ./internal/server -run=^$ -bench=BenchmarkDiscoveryWalk_diskDqueWithDrainWorkers -benchmem -benchtime=1s -count=1 \
  >> "$out" 2>&1

echo "discovery walk benches ok"
