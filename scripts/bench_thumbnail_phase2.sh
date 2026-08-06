#!/bin/bash
# Phase 2 resize-alternative bench runner (measurement only; does not pick a winner).
set -euo pipefail

cd "$(dirname "$0")/.."

mkdir -p tmp tmp/thumbnail_bench_fixtures

# 1. Resize-only suite: isolates the thumb resize step for each variant.
go test ./internal/thumbnail/ \
  -run='^$' \
  -bench='BenchmarkResizeAlt_Only' \
  -benchtime=2s \
  -count=3 \
  -benchmem \
  -cpu=1,4 \
  > ./tmp/thumbnail_phase2_resize_only_bench.txt 2>&1

# 2. Full EXIF-ignored suite: full GenerateThumbnailAndHashes path per variant.
go test ./internal/thumbnail/ \
  -run='^$' \
  -bench='BenchmarkResizeAlt_Full_EXIFIgnored' \
  -benchtime=2s \
  -count=3 \
  -benchmem \
  -cpu=1,4 \
  > ./tmp/thumbnail_phase2_full_exifignored_bench.txt 2>&1

# 3. Parallel suite: full path in parallel over the 12 MP fixture.
go test ./internal/thumbnail/ \
  -run='^$' \
  -bench='BenchmarkResizeAlt_Parallel_EXIFIgnored_12mp' \
  -benchtime=2s \
  -count=3 \
  -benchmem \
  -cpu=1,4 \
  > ./tmp/thumbnail_phase2_parallel_bench.txt 2>&1

# 4. Best-effort RSS for the parallel 12 MP baseline variant only (nfnt_lanczos3).
if command -v /usr/bin/time > /dev/null 2>&1; then
  /usr/bin/time -v go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkResizeAlt_Parallel_EXIFIgnored_12mp/nfnt_lanczos3$' \
    -benchtime=5s \
    -count=1 \
    -cpu=4 \
    > ./tmp/thumbnail_phase2_rss_parallel_12mp_nfnt_lanczos3.txt 2>&1
else
  echo "/usr/bin/time unavailable; RSS capture skipped (same go test run appended below)" \
    > ./tmp/thumbnail_phase2_rss_parallel_12mp_nfnt_lanczos3.txt
  go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkResizeAlt_Parallel_EXIFIgnored_12mp/nfnt_lanczos3$' \
    -benchtime=5s \
    -count=1 \
    -cpu=4 \
    >> ./tmp/thumbnail_phase2_rss_parallel_12mp_nfnt_lanczos3.txt 2>&1
fi

# 5. Sample metrics: per-variant output bounds and MAE vs nfnt Lanczos3 baseline.
go test ./internal/thumbnail/ -run='TestResizeAlt_SampleMetrics' -count=1 -v \
  > ./tmp/thumbnail_phase2_sample_metrics.txt 2>&1

echo "Wrote:"
echo "  ./tmp/thumbnail_phase2_resize_only_bench.txt"
echo "  ./tmp/thumbnail_phase2_full_exifignored_bench.txt"
echo "  ./tmp/thumbnail_phase2_parallel_bench.txt"
echo "  ./tmp/thumbnail_phase2_rss_parallel_12mp_nfnt_lanczos3.txt"
echo "  ./tmp/thumbnail_phase2_sample_metrics.txt"
