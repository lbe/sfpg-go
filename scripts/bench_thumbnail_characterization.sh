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
#   3. tmp/thumbnail_exif_ignored_bench.txt — production full-decode
#      suite on the EXIF-bearing fixture and large synthetics
#      (BenchmarkFull_HasEXIFMetadata, BenchmarkFull_Size_FullDecode,
#      BenchmarkFull_Parallel_FullDecode), -benchmem, -count=3,
#      -cpu=1,4. The output filename is kept for historical continuity
#      (was "EXIF-ignored").
#   4. tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt — best-effort
#      peak RSS (Maximum resident set size) for the fixed parallel 12MP
#      full-decode stress via /usr/bin/time -v. If /usr/bin/time is
#      missing, a one-line note is written and the same go test still
#      runs (appended).
#
# Bench naming: *HasEXIFMetadata* = the production path on the committed
# exif-thumb.jpg fixture only; *FullDecode* = synthetic full-path benches on
# the large synthetic fixtures.
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

# 3. HasEXIFMetadata/FullDecode suite (Phase 1b primary artifact): the
#    production full-decode path on the EXIF-bearing fixture and on the large
#    synthetic fixtures.
go test ./internal/thumbnail/ \
  -run='^$' \
  -bench='BenchmarkFull_HasEXIFMetadata|BenchmarkFull_Size_FullDecode|BenchmarkFull_Parallel_FullDecode' \
  -benchtime=2s \
  -count=3 \
  -benchmem \
  -cpu=1,4 \
  > ./tmp/thumbnail_exif_ignored_bench.txt 2>&1

# 4. Peak RSS for the fixed parallel 12MP full-decode stress (best-effort).
if command -v /usr/bin/time > /dev/null 2>&1; then
  /usr/bin/time -v go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkFull_Parallel_FullDecode_12mp' \
    -benchtime=5s \
    -count=1 \
    -cpu=4 \
    > ./tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt 2>&1
else
  echo "/usr/bin/time unavailable; RSS capture skipped (same go test run appended below)" \
    > ./tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt
  go test ./internal/thumbnail/ \
    -run='^$' \
    -bench='BenchmarkFull_Parallel_FullDecode_12mp' \
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
