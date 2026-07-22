#!/bin/bash
set -euo pipefail
mkdir -p tmp
cd "$(dirname "$0")/.."
go test ./internal/cachelite/bodycodec/... \
  -bench='Benchmark(Compress|Decompress)' \
  -benchtime=3s \
  -count=5 \
  -benchmem \
  -run='^$' \
  > ./tmp/bodycodec_bench.txt 2>&1
echo "Wrote ./tmp/bodycodec_bench.txt"
wc -l ./tmp/bodycodec_bench.txt
