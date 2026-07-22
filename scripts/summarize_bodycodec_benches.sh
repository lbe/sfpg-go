#!/bin/bash
set -euo pipefail
BENCH_FILE="${1:-./tmp/bodycodec_bench.txt}"
[[ -f "$BENCH_FILE" ]] || {
  echo "missing $BENCH_FILE" >&2
  exit 2
}

awk '
/^Benchmark(Compress|Decompress)(Pooled|Alloc)\// {
  name = $1
  sub(/-[0-9]+$/, "", name)
  ns = 0
  for (i = 2; i <= NF; i++) if ($i == "ns/op") { ns = $(i-1) + 0; break }
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
    if (n % 2 == 1) med[k] = arr[(n+1)/2]
    else med[k] = (arr[n/2] + arr[n/2+1]) / 2
    for (i = 1; i <= n; i++) delete arr[i]
    if (k ~ /\/Serial$/) serial[k] = med[k]
    else if (k ~ /\/Parallel8$/) par8[k] = med[k]
    else if (k ~ /\/ParallelCPUs$/) parcpu[k] = med[k]
  }
  print "## Serial"
  print "| benchmark | median_ns | median_ms |"
  print "| --- | ---: | ---: |"
  for (k in serial) printf "| %s | %.0f | %.3f |\n", k, serial[k], serial[k]/1e6
  print ""
  print "## Parallel8"
  print "| benchmark | median_ns | median_ms |"
  print "| --- | ---: | ---: |"
  for (k in par8) printf "| %s | %.0f | %.3f |\n", k, par8[k], par8[k]/1e6
  print ""
  print "## ParallelCPUs"
  print "| benchmark | median_ns | median_ms |"
  print "| --- | ---: | ---: |"
  for (k in parcpu) printf "| %s | %.0f | %.3f |\n", k, parcpu[k], parcpu[k]/1e6
}
' "$BENCH_FILE"
