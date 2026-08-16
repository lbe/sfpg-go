# Phase 1 Thumbnail Characterization — Results

Numbers from `tmp/thumbnail_characterization_bench.txt` (`-benchtime=2s -count=3 -benchmem -cpu=1,4`, no OOM/FAIL, 406s) and `tmp/thumbnail_rss_parallel_12mp.txt`. All times ns/op; "median" = my median of the 3 runs (Go prints none). `-4` suffix = `GOMAXPROCS=4`, not NumCPU.

> **Phase 1b — EXIF-ignored:** results with the embedded-EXIF-thumbnail shortcut disabled — forcing full decode even when an embedded EXIF thumb is present — are in [`phase-1-exif-ignored-results.md`](phase-1-exif-ignored-results.md); summary in the Phase 1b section below.

> **Phase 2 — resize alternatives:** measurement-only comparison of thumb-resize alternatives (nfnt vs `x/image/draw`) on the full-decode / EXIF-ignored path lives in [`phase-2-results.md`](phase-2-results.md).

> **Current production filter:** gallery thumbs now use `draw.ApproxBiLinear` via `defaultThumbResize` / `resizeThumbApproxBiLinear` — see [`production-xdraw-approx.md`](production-xdraw-approx.md).

> **Current production pipeline (follow-up):** pooled `draw.ApproxBiLinear`
> destinations and pHash computed from the gallery thumb — see
> [`production-xdraw-phash-pool.md`](production-xdraw-phash-pool.md). The tables
> below remain the pre-change measurement record.

> **Current production decode (adaptive DCT scale):** JPEG decode in
> `GenerateThumbnailAndHashes` now uses `github.com/m8rge/go-scaled-jpeg` at an
> adaptive DCT scale chosen from the source dimensions: large JPEGs stay at 1/8,
> small ones decode closer to 1:1 so they are not upscaled; hard-fail with no
> stdlib `image/jpeg.Decode` fallback. See
> [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md). The `Phase_Decode` numbers below
> pre-date that change.

## 1. Environment

- go1.26.5 linux/amd64; Xeon E5-2680 v3 @ 2.50GHz; 24 logical CPUs (companion runs show `-24`); GOMAXPROCS unset.

## 2. Phase split (cpu=1, latest run; median in parens)

| Bench                    | ns/op                   | B/op      | allocs |
| ------------------------ | ----------------------- | --------- | ------ |
| `Phase_EXIFExtract/hit`  | 90 675 (90 510)         | 80        | 5      |
| `Phase_EXIFExtract/miss` | 36 320 (36 303)         | 32        | 2      |
| `Phase_Decode`           | 4 594 316 (5 113 248)   | 755 332   | 8      |
| `Phase_ResizeThumb`      | 20 239 135 (20 478 428) | 2 004 184 | 20     |
| `Phase_ResizePHash`      | 7 220 439               | 1 597 144 | 20     |
| `Phase_JPEGEncode`       | 915 650                 | 4 544     | 7      |
| `Phase_MD5`              | 53 152 (55 001)         | 32 843    | 4      |
| `Phase_FullGenerate`     | 43 245 980 (42 090 643) | 4 411 993 | 77     |

Historical sub-bench labels `…/hit` and `…/miss` above mean **has EXIF thumbnail** and **no EXIF thumbnail** respectively; current code names those arms `has-exif-thumb` / `no-exif-thumb`.

Phase sum (excl. FullGenerate) ≈ 33.8 ms vs `Full_EXIFMiss` 33.5 ms (99% match). `FullGenerate` median 42.1 ms ≈ +25% — run variance (its own runs span 39.4–43.2 ms).

## 3. Has EXIF thumbnail vs no EXIF thumbnail (cpu=1)

| Bench           | ns/op      | B/op      | allocs |
| --------------- | ---------- | --------- | ------ |
| `Full_EXIFHit`  | 4 893 167  | 530 030   | 64     |
| `Full_EXIFMiss` | 33 524 588 | 4 395 449 | 71     |

**no-EXIF / has-EXIF = 6.85×**; no-EXIF allocates 8.3× more bytes.

## 4. Size matrix (cpu=1, latest; median in parens)

Full pipeline:

| Size  | ns/op                         | B/op        |
| ----- | ----------------------------- | ----------- |
| 2 MP  | 126 624 731                   | 16 787 991  |
| 12 MP | 643 242 182 (675 300 240)     | 92 894 141  |
| 25 MP | 1 320 783 564 (1 473 992 852) | 191 324 660 |

Scaling (medians): 2→12 MP ×5.33, 12→25 MP ×2.18, 2→25 MP ×11.6. B/op ≈ linear in pixels.

Resize only (Lanczos3 200×150, decode excluded):

| Size  | ns/op                     | B/op       |
| ----- | ------------------------- | ---------- |
| 2 MP  | 68 574 392 (69 195 138)   | 7 070 296  |
| 12 MP | 407 303 287 (405 712 026) | 38 080 218 |
| 25 MP | 747 512 704               | 77 540 572 |

Resize share of full pipeline (medians): 54.6% / 60.1% / 50.7% at 2/12/25 MP.

## 5. Parallel (`RunParallel`, medians)

| Bench                         | cpu=1       | cpu=4       | Δ            |
| ----------------------------- | ----------- | ----------- | ------------ |
| `Full_Parallel_EXIFMiss_2mp`  | 140 620 647 | 35 764 143  | 3.93× faster |
| `Full_Parallel_EXIFMiss_12mp` | 675 027 079 | 233 117 267 | 2.90× faster |
| `Full_Parallel_EXIFHit`       | 3 649 800   | 1 232 919   | 2.96× faster |

Serial benches at `-cpu=4` (no RunParallel): mostly **slower** — Decode 5.11→11.18 ms (2.19×), Full_EXIFHit 1.25×, Full_EXIFMiss 1.27×, JPEGEncode 1.36×. **Faster** only for large resizes — Resize_Size/12mp 1.56×, /25mp 1.50×, ResizeThumb 1.06×. nfnt fan-out pays off only at large inputs.

## 6. RSS

`BenchmarkFull_Parallel_EXIFMiss_12mp`, `-benchtime=5s -cpu=4`: **Maximum resident set size 467 908 kB ≈ 456.9 MiB**. Bench line: 176 959 862 ns/op, 92 846 893 B/op, 98 allocs/op.

## 7. Decision inputs

- **Resize vs decode/EXIF when the file has an EXIF thumbnail?** Not material. Has-EXIF path ≈ 4.9 ms (vs 33.5 ms with no EXIF thumbnail): embedded thumbnail used; big-image Decode/ResizeThumb never run. JPEGEncode ≈ 0.92 ms (~19% of the has-EXIF path) is the largest isolated cost.
- **No EXIF thumbnail, 12–25 MP — which phase dominates?** Resize (Lanczos thumb): 50.7–60.1% of full pipeline; thumb + pHash ≈ 82% of phase time (60.6% + 21.3% on the 800×600 fixture). Decode secondary (15.1%). EXIF/MD5/encode negligible.
- **Parallel at -cpu=4 increase ns/op?** No — RunParallel per-op drops 2.9–3.9×. Serial phases get slower (decode 2.19×); large resizes faster via nfnt fan-out.
- **Peak memory under parallel 12 MP?** Decoded source dominates: ~92.8 MB/op working set (36 MB decoded YCbCr + nfnt pass buffers); ~4–5 in flight ≈ 456.9 MiB RSS. Thumbnail buffers are pooled/reused.
- **Apples-to-apples full-decode numbers?** Has-EXIF vs no-EXIF (§3) is not the only decision basis — see **Phase 1b** below for forced full-decode numbers on the EXIF-bearing fixture and an EXIF-ignored 2/12/25 MP size matrix.

## 8. Not measured

Filter A/B; `x/image/draw` vs nfnt; disabling nfnt fan-out; JPEG quality; embedded-thumb decode+resize isolation; has-EXIF-thumbnail fixtures at 12/25 MP; cold-cache I/O; real worker-pool concurrency profile (RunParallel is an approximation).

## Phase 1b — EXIF-ignored (forced full decode)

Numbers from `tmp/thumbnail_exif_ignored_bench.txt` (`-count=3 -benchmem -cpu=1,4`, no OOM/FAIL, 121.5s) and `tmp/thumbnail_rss_parallel_exif_ignored_12mp.txt` (`-benchtime=5s -count=1 -cpu=4`). All times ns/op; "median" = my median of the 3 runs. `-4` suffix = `GOMAXPROCS=4`, not NumCPU.

Method: `extractEXIFThumbnailHook` forced to `errNoThumb` via `withEXIFExtractDisabled` (`internal/thumbnail/characterization_bench_test.go`); production default unchanged (`extractEXIFThumbnailHook = extractEXIFThumbnail`, `thumbnail.go:40`).

### Side-by-side: EXIF on vs ignored (exif-thumb.jpg, cpu=1, medians)

| Bench              | ns/op (median) | B/op      | allocs |
| ------------------ | -------------- | --------- | ------ |
| `Full_EXIFHit`     | 4 310 298      | 530 029   | 64     |
| `Full_EXIFIgnored` | 31 729 969     | 4 395 384 | 69     |

**ignored / has-EXIF = 7.36×** (medians); B/op 8.29×. `Full_EXIFHit` from `tmp/thumbnail_characterization_bench.txt` (4 310 298 / 3 940 331 / 4 586 709 ns/op).

### Size matrix EXIF-ignored (cpu=1, latest; median in parens)

| Size  | ns/op                         | B/op        | allocs |
| ----- | ----------------------------- | ----------- | ------ |
| 2 MP  | 119 911 741 (130 475 899)     | 16 787 959  | 79     |
| 12 MP | 642 356 914 (642 356 914)     | 92 894 098  | 80     |
| 25 MP | 1 453 105 024 (1 453 105 024) | 191 324 628 | 83     |

Synthetic fixtures carry no embedded EXIF thumb, so forced-ignored equals the default path for them.

### Parallel EXIF-ignored 12 MP (RunParallel, medians)

| cpu | ns/op       | B/op       | allocs | Δ vs cpu=1   |
| --- | ----------- | ---------- | ------ | ------------ |
| 1   | 677 281 221 | 92 861 418 | 82     | —            |
| 4   | 193 319 466 | 92 853 207 | 98     | 3.50× faster |

RSS (`-benchtime=5s -count=1 -cpu=4`): bench line 185 859 571 ns/op, 92 851 691 B/op, 96 allocs; **Maximum resident set size 505 264 kB ≈ 493.4 MiB**.

### Decision inputs (EXIF-ignored)

- **With the EXIF shortcut removed on an EXIF-bearing fixture, what dominates?** Forced full decode of `exif-thumb.jpg` costs 31.7 ms median. The full-decode path's phase split (Phase 1 `BenchmarkPhase_*`, which measures this same decode+resize path) attributes ≈ 82% of phase time to resize — `ResizeThumb` ≈ 60.6% + `ResizePHash` ≈ 21.3% — with decode ≈ 15.1% and encode/MD5 negligible. Resize dominates.
- **Do 12–25 MP EXIF-ignored numbers still show resize dominance?** The EXIF-ignored matrix has no resize-only bench; its full-path medians (642 ms at 12 MP, 1453 ms at 25 MP) scale with the same decode+resize path whose phase split is resize-dominant, so the Phase 1 resize-only share (50.7–60.1% of full pipeline at 2/12/25 MP) carries over unchanged.
- **Is Phase 2 filter/x/image work justified on this path?** Yes. This path is expensive and scales with megapixels: 31.7 ms on a 0.5 MP fixture, 642 ms at 12 MP, 1453 ms at 25 MP. Resize is its largest single cost, so filter work targets the dominant phase of the full-decode path.

Full `-count=3` dumps, cpu=4 lines, and method: [`docs/thumbnail-bench/phase-1-exif-ignored-results.md`](phase-1-exif-ignored-results.md).
