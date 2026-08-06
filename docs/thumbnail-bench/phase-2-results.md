# Phase 2 Thumbnail Resize Alternatives — Results

Phase 2 measures four thumb-resize alternatives on the full-decode / EXIF-ignored path; it is measurement only and does not pick a production winner.

Sources: `tmp/thumbnail_phase2_resize_only_bench.txt`, `tmp/thumbnail_phase2_full_exifignored_bench.txt`, `tmp/thumbnail_phase2_parallel_bench.txt`, `tmp/thumbnail_phase2_rss_parallel_12mp_nfnt_lanczos3.txt`, `tmp/thumbnail_phase2_sample_metrics.txt`. All times ns/op; "median" = my median of the 3 runs recorded in the artifact (Go prints none). `-4` suffix = `GOMAXPROCS=4`, not NumCPU.

## 1. Scope

This document reports resize-alternative numbers only. It does **not** change production defaults and does **not** select a production filter. See § 9 for explicit non-conclusions.

> **Current production (as of this follow-up):** gallery thumbs now use `draw.ApproxBiLinear` via `defaultThumbResize` / `resizeThumbApproxBiLinear` on branch `feat/thumbnail-xdraw-approx` — see [`production-xdraw-approx.md`](production-xdraw-approx.md). The Phase 2 tables below remain the **pre-switch** measurement record.

> **Follow-up (`feat/thumbnail-xdraw-phash-pool`):** production now pools the
> thumb and pHash RGBA destinations and computes pHash from the gallery thumb —
> see [`production-xdraw-phash-pool.md`](production-xdraw-phash-pool.md). The
> Phase 2 tables below remain the pre-change measurement record and are not
> rewritten.

## 2. Environment

- `go version go1.26.5 linux/amd64`
- `uname -a`: `Linux pm1-lxc-deb08-01 7.0.6-2-pve #1 SMP PREEMPT_DYNAMIC PMX 7.0.6-2 (2026-05-29T11:08Z) x86_64 GNU/Linux`
- `go env GOMAXPROCS`: unset; `GOOS=linux`; `GOARCH=amd64`
- Bench header CPU: `Intel(R) Xeon(R) CPU E5-2680 v3 @ 2.50GHz` (same machine as Phase 1)

## 3. Method

- `thumbResizeHook` override selects the thumb-resize implementation per bench; `withEXIFExtractDisabled` forces the embedded-EXIF-thumbnail miss (full decode).
- Production defaults unchanged: `extractEXIFThumbnailHook` production path intact, thumb resize still `thumbnail(200, 150, img, resize.Lanczos3)` (`nfnt_lanczos3`), pHash still 64×64 `resize.Bilinear`, JPEG encode / MD5 production path.
- No destination-buffer pooling for `x/image/draw` variants (naive allocate-per-call).
- Target geometry: fit inside the 200×150 box via the same width/height math as production `thumbnail()`.

Locked variant table (exact IDs):

| Variant ID              | Backend                   | Thumb resize implementation                                          |
| ----------------------- | ------------------------- | -------------------------------------------------------------------- |
| `nfnt_lanczos3`         | `github.com/nfnt/resize`  | `thumbnail(200, 150, img, resize.Lanczos3)` — current production     |
| `nfnt_bilinear`         | `github.com/nfnt/resize`  | `thumbnail(200, 150, img, resize.Bilinear)`                          |
| `xdraw_approx_bilinear` | `golang.org/x/image/draw` | `draw.ApproxBiLinear.Scale` into a new `*image.RGBA` of the fit size |
| `xdraw_catmullrom`      | `golang.org/x/image/draw` | `draw.CatmullRom.Scale` into a new `*image.RGBA` of the fit size     |

## 4. Resize-only matrix (cpu=1, medians)

`BenchmarkResizeAlt_Only` — thumb resize only, decode done once outside the timed region.

| Size  | Variant                 | ns/op         | B/op       | allocs |
| ----- | ----------------------- | ------------- | ---------- | ------ |
| 2 MP  | `nfnt_lanczos3`         | 74 283 879    | 7 070 296  | 20     |
| 2 MP  | `nfnt_bilinear`         | 35 084 524    | 7 045 208  | 20     |
| 2 MP  | `xdraw_approx_bilinear` | 883 682       | 90 176     | 2      |
| 2 MP  | `xdraw_catmullrom`      | 91 730 120    | 7 216 736  | 8      |
| 12 MP | `nfnt_lanczos3`         | 393 736 430   | 38 080 218 | 20     |
| 12 MP | `nfnt_bilinear`         | 168 307 423   | 38 018 777 | 20     |
| 12 MP | `xdraw_approx_bilinear` | 1 189 945     | 122 944    | 2      |
| 12 MP | `xdraw_catmullrom`      | 496 755 090   | 19 792 864 | 8      |
| 25 MP | `nfnt_lanczos3`         | 796 689 439   | 77 540 570 | 20     |
| 25 MP | `nfnt_bilinear`         | 328 102 525   | 77 450 457 | 20     |
| 25 MP | `xdraw_approx_bilinear` | 1 018 063     | 90 176     | 2      |
| 25 MP | `xdraw_catmullrom`      | 1 050 257 024 | 24 756 448 | 8      |

Ordering at every size (cpu=1): `xdraw_approx_bilinear` < `nfnt_bilinear` < `nfnt_lanczos3` < `xdraw_catmullrom`.

**cpu=4 note (serial bench, no RunParallel):** material for `nfnt_lanczos3`, which speeds up via its internal fan-out at large inputs — 12 MP 274 228 089 ns/op (1.44× vs cpu=1), 25 MP 469 669 873 (1.70×), 2 MP 65 800 729 (1.13×). All other variants run slower at cpu=4: `nfnt_bilinear` 12 MP 187 768 551 (0.90×), `xdraw_approx_bilinear` 12 MP 2 143 482 (0.56×), `xdraw_catmullrom` 12 MP 566 928 223 (0.88×).

## 5. Full EXIF-ignored matrix (cpu=1, medians)

`BenchmarkResizeAlt_Full_EXIFIgnored` — full `GenerateThumbnailAndHashes` with EXIF hook disabled and thumb resize overridden.

| Size  | Variant                 | ns/op         | B/op        | allocs |
| ----- | ----------------------- | ------------- | ----------- | ------ |
| 2 MP  | `nfnt_lanczos3`         | 122 351 101   | 16 755 239  | 79     |
| 2 MP  | `nfnt_bilinear`         | 83 403 394    | 16 730 149  | 79     |
| 2 MP  | `xdraw_approx_bilinear` | 53 567 458    | 9 730 369   | 49     |
| 2 MP  | `xdraw_catmullrom`      | 148 823 170   | 16 901 673  | 67     |
| 12 MP | `nfnt_lanczos3`         | 683 279 773   | 92 861 389  | 80     |
| 12 MP | `nfnt_bilinear`         | 447 887 730   | 92 799 931  | 80     |
| 12 MP | `xdraw_approx_bilinear` | 290 883 040   | 54 846 380  | 51     |
| 12 MP | `xdraw_catmullrom`      | 797 499 710   | 74 532 906  | 62     |
| 25 MP | `nfnt_lanczos3`         | 1 389 111 012 | 191 291 908 | 83     |
| 25 MP | `nfnt_bilinear`         | 941 596 445   | 191 201 773 | 82     |
| 25 MP | `xdraw_approx_bilinear` | 652 077 255   | 113 787 976 | 55     |
| 25 MP | `xdraw_catmullrom`      | 1 645 828 086 | 138 462 688 | 65     |

Ordering at every size: identical to resize-only — `xdraw_approx_bilinear` < `nfnt_bilinear` < `nfnt_lanczos3` < `xdraw_catmullrom` — but the gaps between variants are far smaller (full-path time is dominated by decode + pHash + encode).

## 6. Parallel 12 MP (full path, EXIF-ignored, medians)

`BenchmarkResizeAlt_Parallel_EXIFIgnored_12mp` (`b.RunParallel`).

| Variant                 | cpu=1 ns/op | cpu=4 ns/op | Δ vs cpu=1   |
| ----------------------- | ----------- | ----------- | ------------ |
| `nfnt_lanczos3`         | 681 818 054 | 179 140 646 | 3.81× faster |
| `nfnt_bilinear`         | 502 346 630 | 158 240 260 | 3.17× faster |
| `xdraw_approx_bilinear` | 319 499 987 | 85 074 802  | 3.76× faster |
| `xdraw_catmullrom`      | 841 023 137 | 268 622 871 | 3.13× faster |

Relative ordering is unchanged at cpu=4. The `nfnt_lanczos3` vs `nfnt_bilinear` gap narrows at cpu=4 (1.13× vs 1.53× at cpu=1); `xdraw_approx_bilinear` stays ~2.1× faster than `nfnt_lanczos3` at both cpu settings.

**RSS (`nfnt_lanczos3`, `-benchtime=5s -count=1 -cpu=4`):** Maximum resident set size **472 332 kB ≈ 461.3 MiB**. Bench line: 178 513 673 ns/op, 92 854 440 B/op, 98 allocs.

## 7. Sample metrics (2 MP, `TestResizeAlt_SampleMetrics`)

One resize per variant on the 2 MP decoded source; logs per-variant output bounds and mean absolute error (MAE) of RGB versus `nfnt_lanczos3`.

| Variant                 | bounds  | MAE vs `nfnt_lanczos3` |
| ----------------------- | ------- | ---------------------- |
| `nfnt_lanczos3`         | 200x112 | 0.0000                 |
| `nfnt_bilinear`         | 200x112 | 491.3012               |
| `xdraw_approx_bilinear` | 200x112 | 1274.2325              |
| `xdraw_catmullrom`      | 200x112 | 552.7546               |

## 8. Decision inputs for human (numbers only)

- **Full-decode path, 12 MP (cpu=1, medians vs `nfnt_lanczos3` 683.3 ms):** `xdraw_approx_bilinear` 290.9 ms = 0.43× (2.35× faster); `nfnt_bilinear` 447.9 ms = 0.66× (1.53× faster); `xdraw_catmullrom` 797.5 ms = 1.17× (17% slower).
- **Full-decode path, 25 MP (vs `nfnt_lanczos3` 1389.1 ms):** `xdraw_approx_bilinear` 652.1 ms = 0.47× (2.13× faster); `nfnt_bilinear` 941.6 ms = 0.68× (1.47× faster); `xdraw_catmullrom` 1645.8 ms = 1.18× (18% slower).
- **Does resize-only ranking match full-path ranking?** Yes — identical variant order at 2/12/25 MP. Magnitudes differ sharply: resize-only `xdraw_approx_bilinear` is ~331× faster than `nfnt_lanczos3` at 12 MP (1.19 vs 393.7 ms) and ~783× at 25 MP, but only ~2.1–2.3× faster on the full path, because non-resize phases (decode, pHash, encode) dominate once the thumb resize is cheap. The practical full-path ceiling for the fastest variant is bounded by that non-resize time (~285 ms at 12 MP, ~650 ms at 25 MP for this fixture).
- **What does parallel `-cpu=4` do to relative ordering?** Ordering is unchanged; every variant is 3.1–3.8× faster. The `nfnt_lanczos3`↔`nfnt_bilinear` spread shrinks from 1.53× to 1.13×; `xdraw_approx_bilinear` remains ~2.1× faster than `nfnt_lanczos3` at both cpu settings. RSS for `nfnt_lanczos3` under parallel 12 MP: 461.3 MiB.
- **MAE vs Lanczos3 on 2 MP:** `nfnt_bilinear` 491.3012, `xdraw_approx_bilinear` 1274.2325, `xdraw_catmullrom` 552.7546 (all bounds 200x112; baseline MAE 0.0000). Informational quality sample only.

## 9. Explicit non-conclusions

- Phase 2 itself did **not** ship a switch; a later production change landed on branch `feat/thumbnail-xdraw-approx` ([`production-xdraw-approx.md`](production-xdraw-approx.md)).
- It does **not** measure disabling nfnt's `GOMAXPROCS` fan-out.
- It does **not** measure destination-buffer pooling / scaler reuse for `x/image/draw`.
- It does **not** recommend shipping any variant. Ratios and tables only — the winner decision is a human call, informed by visual quality review outside this document's scope.

## 10. Phase 1 pointer

Phase 1 characterization and Phase 1b EXIF-ignored findings remain in [`phase-1-results.md`](phase-1-results.md) (and its [`phase-1-exif-ignored-results.md`](phase-1-exif-ignored-results.md) detail page). Phase 2 extends only the resize-alternative measurement surface on the full-decode / EXIF-ignored path.
