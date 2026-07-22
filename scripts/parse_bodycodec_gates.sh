#!/bin/bash
set -euo pipefail
BENCH_FILE="${1:-./tmp/bodycodec_bench.txt}"
[[ -f "$BENCH_FILE" ]] || {
  echo "missing $BENCH_FILE" >&2
  exit 2
}

awk '
BEGIN { fail = 0 }
/^Benchmark(Compress|Decompress)(Pooled|Alloc)\// {
  name = $1
  sub(/-[0-9]+$/, "", name)
  if (name !~ /\/Parallel8$/) next
  if (name !~ /gallery_large_[123]/) next
  ns = 0
  for (i = 2; i <= NF; i++) {
    if ($i == "ns/op") { ns = $(i-1) + 0; break }
  }
  if (ns == 0) next
  count[name]++
  vals[name, count[name]] = ns
}
END {
  for (k in count) {
    n = count[k]
    for (i = 1; i <= n; i++) arr[i] = vals[k, i]
    for (i = 1; i <= n; i++)
      for (j = i + 1; j <= n; j++)
        if (arr[i] > arr[j]) { t = arr[i]; arr[i] = arr[j]; arr[j] = t }
    if (n % 2 == 1) median[k] = arr[(n + 1) / 2]
    else median[k] = (arr[n / 2] + arr[n / 2 + 1]) / 2
    for (i = 1; i <= n; i++) delete arr[i]
  }

  print "=== G1 pooled faster than alloc (Parallel8, large) ==="
  split("gallery_large_1 gallery_large_2 gallery_large_3", fixtures, " ")
  split("zstd gzip", codecs, " ")
  split("Compress Decompress", ops, " ")
  for (fi = 1; fi <= 3; fi++) {
    for (ci = 1; ci <= 2; ci++) {
      for (oi = 1; oi <= 2; oi++) {
        pooled = "Benchmark" ops[oi] "Pooled/" codecs[ci] "/" fixtures[fi] "/Parallel8"
        alloc  = "Benchmark" ops[oi] "Alloc/"  codecs[ci] "/" fixtures[fi] "/Parallel8"
        if (!(pooled in median) || !(alloc in median)) {
          printf "FAIL %s missing data\n", pooled
          fail = 1
          continue
        }
        ok = (median[pooled] < median[alloc])
        printf "%s pooled=%.0f alloc=%.0f %s\n", pooled, median[pooled], median[alloc], (ok ? "PASS" : "FAIL")
        if (!ok) fail = 1
      }
    }
  }

  print "=== G2 zstd absolute budgets ms (Parallel8, large) ==="
  for (fi = 1; fi <= 3; fi++) {
    for (oi = 1; oi <= 2; oi++) {
      name = "Benchmark" ops[oi] "Pooled/zstd/" fixtures[fi] "/Parallel8"
      if (!(name in median)) { printf "FAIL %s missing\n", name; fail = 1; continue }
      ms = median[name] / 1000000
      limit = (ops[oi] == "Compress" ? 80 : 40)
      ok = (ms <= limit)
      printf "%s %.2f ms (limit %.0f) %s\n", name, ms, limit, (ok ? "PASS" : "FAIL")
      if (!ok) fail = 1
    }
  }

  if (fail) { print "OVERALL: FAIL"; exit 1 }
  print "OVERALL: PASS"; exit 0
}
' "$BENCH_FILE"
