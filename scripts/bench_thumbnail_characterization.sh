#!/bin/bash
# Characterization bench runner for Phase 1 thumbnail benches.
#
# Produces four artifacts under ./tmp/:
#   1. tmp/thumbnail_characterization_bench.txt — full Phase 1 suite
#      (BenchmarkPhase_*, BenchmarkFull_*, BenchmarkResize_Size),
#      -benchmem, -count=3, -cpu=1,4. Primary source for the Phase 1
#      results summary (§§2-5).
#   2. tmp/thumbnail_rss_parallel_12mp.txt — best-effort peak RSS
#      (Maximum resident set size) for the fixed parallel 12MP stress
#      via /usr/bin/time -v. If /usr/bin/time is missing, a one-line
#      note is written and the same go test still runs (appended).
#   3. tmp/thumbnail_exif_ignored_bench.txt — EXIF-ignored Phase 1b suite
#      (BenchmarkFull_EXIFIgnored, BenchmarkFull_Size_EXIFIgnored,
#      BenchmarkFull_Parallel_EXIFIgnored), -benchmem, -count=3,
#      -cpu=1,4. Primary source for the EXIF-ignored results summary.
#   4. tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt — best-effort
#      peak RSS (Maximum resident set size) for the fixed parallel 12MP
#      EXIF-ignored stress via /usr/bin/time -v. If /usr/bin/time is
#      missing, a one-line note is written and the same go test still
#      runs (appended).
#
# Usage: scripts/bench_thumbnail_characterization.sh
set -euo pipefail

cd "$(dirname "$0")/.."

mkdir -p tmp tmp/thumbnail_bench_fixtures

# 1. Full characterization suite (primary artifact).
#    BenchmarkFull_EXIF* and BenchmarkFull_Parallel_* match BenchmarkFull_;
#    BenchmarkResize_Size matches Resize_Size; BenchmarkPhase_* matches Phase_.
go test ./internal/thumbnail/ \
  -run='^$' \
  -bench='Benchmark(Phase_|Full_|Resize_Size)' \
  -benchtime=2s \
  -count=3 \
  -benchmem \
  -cpu=1,4 \
  > ./tmp/thumbnail_characterization_bench.txt 2>&1

# 2. Peak RSS for a fixed parallel stress (best-effort).
if command -v /usr/bin/time > /dev/null 2>&1; then
  /usr/bin/time -v go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkFull_Parallel_EXIFMiss_12mp' \
    -benchtime=5s \
    -count=1 \
    -cpu=4 \
    > ./tmp/thumbnail_rss_parallel_12mp.txt 2>&1
else
  echo "/usr/bin/time unavailable; RSS capture skipped (same go test run appended below)" \
    > ./tmp/thumbnail_rss_parallel_12mp.txt
  go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkFull_Parallel_EXIFMiss_12mp' \
    -benchtime=5s \
    -count=1 \
    -cpu=4 \
    >> ./tmp/thumbnail_rss_parallel_12mp.txt 2>&1
fi

# 3. EXIF-ignored suite (Phase 1b primary artifact): forced full-decode
#    benches on EXIF-bearing and large fixtures.
go test ./internal/thumbnail/ \
  -run='^$' \
  -bench='BenchmarkFull_EXIFIgnored|BenchmarkFull_Size_EXIFIgnored|BenchmarkFull_Parallel_EXIFIgnored' \
  -benchtime=2s \
  -count=3 \
  -benchmem \
  -cpu=1,4 \
  > ./tmp/thumbnail_exif_ignored_bench.txt 2>&1

# 4. Peak RSS for the fixed parallel 12MP EXIF-ignored stress (best-effort).
if command -v /usr/bin/time > /dev/null 2>&1; then
  /usr/bin/time -v go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkFull_Parallel_EXIFIgnored_12mp' \
    -benchtime=5s \
    -count=1 \
    -cpu=4 \
    > ./tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt 2>&1
else
  echo "/usr/bin/time unavailable; RSS capture skipped (same go test run appended below)" \
    > ./tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt
  go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkFull_Parallel_EXIFIgnored_12mp' \
    -benchtime=5s \
    -count=1 \
    -cpu=4 \
    >> ./tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt 2>&1
fi

echo "Wrote:"
echo "  ./tmp/thumbnail_characterization_bench.txt"
echo "  ./tmp/thumbnail_rss_parallel_12mp.txt"
echo "  ./tmp/thumbnail_exif_ignored_bench.txt"
echo "  ./tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt"
