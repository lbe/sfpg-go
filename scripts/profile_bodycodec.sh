#!/bin/bash
set -euo pipefail
mkdir -p tmp
cd "$(dirname "$0")/.."

set +e
go test ./internal/cachelite/bodycodec/ \
  -bench='^BenchmarkProfile' \
  -benchtime=2s \
  -count=1 \
  -benchmem \
  -cpuprofile=./tmp/bodycodec_cpu.prof \
  -memprofile=./tmp/bodycodec_mem.prof \
  -run='^$' \
  > ./tmp/bodycodec_profile_bench.txt 2>&1
TEST_EXIT=$?
set -e

if grep -q 'signal: killed' ./tmp/bodycodec_profile_bench.txt; then
  echo 'PROCEDURE FAIL: OOM during profile bench' >&2
  exit 1
fi
[[ "$TEST_EXIT" -eq 0 ]] || {
  echo "PROCEDURE FAIL: go test exit=$TEST_EXIT" >&2
  exit 1
}
[[ -f ./tmp/bodycodec_cpu.prof ]] || {
  echo 'missing cpu profile' >&2
  exit 1
}
[[ -f ./tmp/bodycodec_mem.prof ]] || {
  echo 'missing mem profile' >&2
  exit 1
}
BENCH_N=$(grep -c '^BenchmarkProfile' ./tmp/bodycodec_profile_bench.txt || true)
[[ "$BENCH_N" -eq 6 ]] || {
  echo "PROCEDURE FAIL: expected 6 BenchmarkProfile lines, got $BENCH_N" >&2
  exit 1
}

{
  echo "=== CPU top ==="
  go tool pprof -top -nodecount=25 ./tmp/bodycodec_cpu.prof
  echo ""
  echo "=== CPU top (bodycodec|htmlsniff|gensyncpool|klauspost) ==="
  go tool pprof -top -nodecount=25 -focus='bodycodec|htmlsniff|gensyncpool|klauspost' ./tmp/bodycodec_cpu.prof
  echo ""
  echo "=== Mem inuse_space ==="
  go tool pprof -top -nodecount=20 -sample_index=inuse_space ./tmp/bodycodec_mem.prof
  echo ""
  echo "=== Mem alloc_space ==="
  go tool pprof -top -nodecount=20 -sample_index=alloc_space ./tmp/bodycodec_mem.prof
  echo ""
  echo "=== Bench lines ==="
  grep '^BenchmarkProfile' ./tmp/bodycodec_profile_bench.txt
} > ./tmp/bodycodec_profile_summary.txt 2>&1

echo "Wrote tmp/bodycodec_profile_summary.txt"
