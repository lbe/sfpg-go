# Phase 1b Thumbnail Characterization — EXIF-Ignored Results

Numbers from `tmp/thumbnail_exif_ignored_bench.txt` (`-count=3 -benchmem -cpu=1,4`, no OOM/FAIL, 121.5s) and `tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt` (`-benchtime=5s -count=1 -cpu=4`). All times ns/op; "median" = my median of the 3 runs (Go prints none). `-4` suffix = `GOMAXPROCS=4`, not NumCPU.

## 1. Environment

- go1.26.5 linux/amd64; `go env` GOMAXPROCS unset, GOOS=linux, GOARCH=amd64.
- `uname -a`: Linux pm1-lxc-deb08-01 7.0.6-2-pve #1 SMP PREEMPT_DYNAMIC PMX 7.0.6-2 (2026-05-29T11:08Z) x86_64 GNU/Linux.
- CPU (bench header): Intel Xeon E5-2680 v3 @ 2.50GHz; 24 logical CPUs.

## 2. Method

- `extractEXIFThumbnailHook` forced to return `errNoThumb` via `withEXIFExtractDisabled` (`internal/thumbnail/characterization_bench_test.go`); hook restored with `b.Cleanup`.
- Production default unchanged: `extractEXIFThumbnailHook = extractEXIFThumbnail` (`thumbnail.go:40`).
- `BenchmarkFull_EXIFIgnored` runs the full `GenerateThumbnailAndHashes` path on the committed EXIF-hit fixture `testdata/thumbnail/exif-thumb.jpg` with the embedded-thumbnail shortcut disabled, so it measures full decode+resize of the EXIF-bearing source.
- `BenchmarkFull_Size_EXIFIgnored` / `BenchmarkFull_Parallel_EXIFIgnored_12mp` run the same forced path over the cached 2/12/25 MP synthetic fixtures (which carry no EXIF; the hook is a no-op there).

## 3. BenchmarkFull_EXIFIgnored (exif-thumb.jpg, cpu=1)

| Run            | ns/op      | B/op      | allocs |
| -------------- | ---------- | --------- | ------ |
| 1st            | 31 615 159 | 4 395 381 | 69     |
| 2nd            | 31 729 969 | 4 395 384 | 69     |
| 3rd            | 31 797 280 | 4 395 397 | 69     |
| median         | 31 729 969 | 4 395 384 | 69     |
| cpu=4 (median) | 42 878 387 | 4 428 533 | 105    |

cpu=4 lines: 43 905 397 / 42 798 201 / 42 878 387 ns/op; B/op 4 428 533 / 4 427 787 / 4 431 535; allocs 105/104/105.

## 4. Side-by-side: EXIF-on vs EXIF-ignored (same exif-thumb.jpg fixture, cpu=1)

| Bench              | ns/op (median) | B/op      | allocs | Source                             |
| ------------------ | -------------- | --------- | ------ | ---------------------------------- |
| `Full_EXIFHit`     | 4 310 298      | 530 029   | 64     | Phase 1 file, cpu=1 lines (median) |
| `Full_EXIFIgnored` | 31 729 969     | 4 395 384 | 69     | this run                           |

- `Full_EXIFHit` runs: 4 310 298 / 3 940 331 / 4 586 709 ns/op (`tmp/thumbnail_characterization_bench.txt`). `docs/thumbnail-bench/phase-1-results.md` §3 lists 4 893 167 from an earlier run of the same file.
- **ignored/hit = 7.36×** (medians); B/op 8.29×.
- Sanity check: forced-ignored (31.7 ms) ≈ real `Full_EXIFMiss` (median 32 686 608 ns/op, same file) within run noise — the hook behaves like a genuine miss.

## 5. Size matrix EXIF-ignored (cpu=1; latest; median in parens)

| Size  | ns/op                         | B/op        | allocs |
| ----- | ----------------------------- | ----------- | ------ |
| 2 MP  | 119 911 741 (130 475 899)     | 16 787 959  | 79     |
| 12 MP | 642 356 914 (642 356 914)     | 92 894 098  | 80     |
| 25 MP | 1 453 105 024 (1 453 105 024) | 191 324 628 | 83     |

Scaling (medians): 2→12 MP ×4.92, 12→25 MP ×2.26, 2→25 MP ×11.14; B/op ≈ linear in pixels. Tracks the Phase 1 EXIF-on `BenchmarkFull_Size` medians (129 194 333 / 676 961 288 / 1 473 920 471, same file) within run noise, as expected — the synthetics carry no EXIF.

## 6. Parallel EXIF-ignored 12 MP (RunParallel, medians)

| cpu | ns/op       | B/op       | allocs | Δ vs cpu=1   |
| --- | ----------- | ---------- | ------ | ------------ |
| 1   | 677 281 221 | 92 861 418 | 82     | —            |
| 4   | 193 319 466 | 92 853 207 | 98     | 3.50× faster |

cpu=4 runs: 236 101 533 / 193 319 466 / 174 792 733 ns/op.

RSS (`tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt`, `-benchtime=5s -count=1 -cpu=4`): bench line 185 859 571 ns/op, 92 851 691 B/op, 96 allocs; **Maximum resident set size 505 264 kB ≈ 493.4 MiB**; job CPU 374%. Phase 1 EXIF-miss parallel 12 MP RSS was 467 908 kB ≈ 456.9 MiB (+8% this run, same order).

## 7. Decision inputs

- **Does resize still dominate with the EXIF shortcut removed?** Yes. Forced-ignored ≈ real miss (31.7 vs 32.7 ms medians). Phase 1 phase-split on the full-decode path: resize (thumb + pHash) ≈ 82% of phase time, resize-only = 50.7–60.1% of full pipeline at 2/12/25 MP. Removing the shortcut re-opens the full decode+resize cost; resize remains the largest share.
- **Is Phase 2 filter work justified on this path?** Yes. The miss/ignored path is the common real-world case and costs 7.36× the hit path (31.7 ms vs 4.3 ms); resize is its largest single cost, so filter work targets the dominant phase. Caveat: EXIF hits bypass resize entirely (~4.3 ms), so filter gains apply only to full-decode paths.

## 8. Non-conclusions

- Not a filter A/B — no filter variants compared.
- Not `x/image/draw` vs nfnt.
- Not a production EXIF-disable proposal — the production default is unchanged; the hook is test-only.
