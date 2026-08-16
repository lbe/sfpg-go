# Thumbnail Benchmark — Phase 0 Baseline Inventory

**Branch:** `bench/thumbnail-perf-phase-0-1`
**Date:** 2026-08-04
**Task:** 0.2 — Write baseline inventory (committed)

All claims below were verified against the current source on this branch (file paths and line numbers cited). This document is an inventory of what the production code does today; it makes no claims about performance.

> **Refreshed 2026-08-05 (post `feat/thumbnail-xdraw-phash-pool`):** the pipeline
> steps, resize table, and pooling section below now describe current production
> — pooled `draw.ApproxBiLinear` destinations and pHash from the gallery thumb.
> Historical Phase 0 claims that conflict (nfnt thumb/pHash over the decoded
> source, unpooled destinations) were superseded; see
> [`production-xdraw-phash-pool.md`](production-xdraw-phash-pool.md).

> **Refreshed (post EXIF fast path removal):** the embedded-EXIF-thumbnail
> shortcut was **removed** — `GenerateThumbnailAndHashes` now always decodes the
> full source image via `fullImageDecodeHook` (JPEG through go-scaled-jpeg at
> an adaptive DCT scale, other formats through stdlib `image.Decode`). The
> pipeline
> steps, pooling, and fixture sections below describe this current single-path
> decode — see [`docs/ARCHITECTURE.md`](../ARCHITECTURE.md) for current behavior
> and `phase-1-results.md` for the pre-change numbers.

---

## 1. Pipeline steps — `GenerateThumbnailAndHashes` (`internal/thumbnail/thumbnail.go`)

Entry point: `func GenerateThumbnailAndHashes(r io.ReadSeeker, srcW, srcH int) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error)` — `thumbnail.go:247`. `srcW`/`srcH` are the source image dimensions in pixels, supplied by the caller — production passes the `image.DecodeConfig` dims already read during discovery (`processor.go:231` → `processor.go:244`).

1. Seek to the start of the reader, hard-failing on error: `r.Seek(0, io.SeekStart)` — `thumbnail.go:250`. There is no embedded-EXIF-thumbnail extraction step.
2. Decode the full source image: `srcImg, _, decodeErr := fullImageDecodeHook(r, srcW, srcH)` — `thumbnail.go:253`. The production default `decodeFullImage` (`scaled_jpeg_decode.go:48`) wraps `r` in a `bufio.Reader`, sniffs JPEG magic via peek (non-consuming), and decodes JPEG via go-scaled-jpeg on that same buffered reader at an **adaptive DCT scale** (`scaled_jpeg_decode.go:53`): `chooseJPEGDCTSize(srcW, srcH)` (`thumbnail.go:210`) picks the coarsest scale in [1,8] whose decoded size (dct/8 of the source) is still at least the 200×150 gallery-thumb fit, so large JPEGs stay at 1/8 and small ones decode closer to 1:1; non-JPEG goes to `image.Decode` on the buffered reader (`scaled_jpeg_decode.go:50-51`). A JPEG scaled-decode error is returned as-is — hard fail, no stdlib `jpeg.Decode` fallback. `srcImg` = full image.
3. Resize the source to the thumbnail box: `thumbImg, releaseThumb := acquireGalleryThumb(srcImg)` — `thumbnail.go:225`. The production path gets a **pooled** 200×150 `*image.RGBA` canvas (`thumbRGBAPool`, `thumbnail.go:97`), fits the source inside the box via `fitInsideBox(200, 150)` (`thumbnail.go:138`), scales the resulting sub-rectangle with `draw.ApproxBiLinear.Scale` (`thumbnail.go:140`), and defers release of the full canvas back to the pool (`thumbnail.go:226`). When tests/benches replace `thumbResizeHook`, the hook result is used without pooling; the default hook is `defaultThumbResize` → `resizeThumbApproxBiLinear` (`thumbnail.go:55`, `thumbnail.go:195`), which allocates a fresh `*image.RGBA` for that (non-production-default) path.
4. Get a pooled `*bytes.Buffer` for the output: `thumbnailBytesBuffer := GetBytesBuffer()` — `thumbnail.go:230`; JPEG-encode: `jpegEncodeHook(thumbnailBytesBuffer, thumbImg, nil)` — `thumbnail.go:231` (hook default `jpeg.Encode`, `thumbnail.go:29`).
5. MD5 over the **full file bytes**: seek to start (`thumbnail.go:237`), pooled hasher `md5Hasher := GetMD5()` — `thumbnail.go:242` (`defer PutMD5(md5Hasher)` at `thumbnail.go:243`), stream copy `ioCopyHook(md5Hasher, r)` — `thumbnail.go:244` (hook default `io.Copy`, `thumbnail.go:32`), hex string via `fmt.Sprintf("%x", md5Hasher.Sum(nil))` into a pooled `*sql.NullString` — `thumbnail.go:249-251`.
6. pHash over the **gallery thumb** (the step-3 thumb image, before JPEG encode): `phashRGBA, releasePH := acquirePHashRGBA(thumbImg)` — `thumbnail.go:256`; a pooled 64×64 `*image.RGBA` canvas from `phashRGBAPool` (`thumbnail.go:112`) scaled with `draw.ApproxBiLinear` (`thumbnail.go:148`); pooled `phash := GetNullInt64()` — `thumbnail.go:258`; `phash64, err := newPHash64Hook(phashRGBA)` — `thumbnail.go:259` (hook default `imagehash.NewPHash64`, `thumbnail.go:35`); pHash error is logged but **not** fatal; result stored in the pooled `*sql.NullInt64`.
7. Return `(thumbnailBytesBuffer, md5, phash, nil)` — `thumbnail.go:266`.

Note: there are **two** `Seek(0, io.SeekStart)` calls on the success path — before the full-image decode (`thumbnail.go:250`) and before MD5 (`thumbnail.go:271`). There is no extract seek and no fallback decode.

> **Geometry note:** the adaptive JPEG DCT decode changes **new** thumbs/pHash geometry vs old rows — a 400×300 JPEG now decodes at dct 4 (1/2) to exactly 200×150 and renders **200×150**, not the upscaled **200×148** produced from the old fixed 1/8 decode (50×37). Existing `thumbs.db` rows and stored pHash are unchanged until rediscovery/regeneration.

---

## 2. Resize parameters

| Use                 | Call site                                                                                                               | Target box                                  | Interpolation                           |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------- | --------------------------------------- |
| Display thumbnail   | `acquireGalleryThumb(srcImg)` — `thumbnail.go:225` (pooled 200×150 RGBA canvas from `thumbRGBAPool`, `thumbnail.go:97`) | fit inside **200×150** px, aspect preserved | **ApproxBiLinear** (`thumbnail.go:140`) |
| pHash source squash | `acquirePHashRGBA(thumbImg)` — `thumbnail.go:256` (pooled 64×64 RGBA canvas from `phashRGBAPool`, `thumbnail.go:112`)   | fixed **64×64**                             | **ApproxBiLinear** (`thumbnail.go:148`) |

The former production `thumbnail()` helper (nfnt) moved to
`internal/thumbnail/nfnt_resize_test.go:16` — tests/benches only. Production
geometry is `fitInsideBox` (`thumbnail.go:169`):

- Chooses width- or height-constrained dimensions so the result fits in the box: `maxW*origHeight <= maxH*origWidth` → width-constrained (`thumbnail.go:177-179`).
- Clamps `newWidth`/`newHeight` to ≥ 1 (`thumbnail.go:186-191`).
- **Upscales** smaller sources (unlike `resize.Thumbnail`), per the doc comment at `thumbnail.go:166-168`. The source is always the full-image decode (JPEG via go-scaled-jpeg at an adaptive DCT scale — large JPEGs at 1/8, small ones closer to 1:1 — other formats via `image.Decode`).

> **Geometry note (all JPEGs):** the fit math itself is unchanged, but the
> full-image JPEG decode now runs at an **adaptive** DCT scale via
> go-scaled-jpeg — chosen so the decoded source covers the 200×150 fit box —
> so the decoded source differs from the old fixed 1/8 decode. A 400×300 JPEG
> decodes at dct 4 (1/2) to 200×150 and fits as **200×150** — new
> thumbs/pHash can change until rediscovery/regeneration.

---

## 3. Pooling

Pooled (all backed by `internal/gensyncpool`, reset on `Put`; pool declarations span `thumbnail.go:60-155`):

| Pool              | Element                                                        | Get / Put                                                  |
| ----------------- | -------------------------------------------------------------- | ---------------------------------------------------------- |
| `bytesBufferPool` | `*bytes.Buffer` (Reset on Put)                                 | `GetBytesBuffer` / `PutBytesBuffer` — `thumbnail.go:66-69` |
| `nullStringPool`  | `*sql.NullString` (String+Valid cleared)                       | `GetNullString` / `PutNullString` — `thumbnail.go:78-81`   |
| `nullInt64Pool`   | `*sql.NullInt64` (Int64+Valid zeroed)                          | `GetNullInt64` / `PutNullInt64` — `thumbnail.go:90-93`     |
| `thumbRGBAPool`   | 200×150 `*image.RGBA` canvas (cleared + geometry reset on Put) | `acquireGalleryThumb` — `thumbnail.go:133-141`             |
| `phashRGBAPool`   | 64×64 `*image.RGBA` canvas (cleared on Put)                    | `acquirePHashRGBA` — `thumbnail.go:146-149`                |
| `md5Pool`         | `hash.Hash` from `md5.New()` (Reset on Put)                    | `GetMD5` / `PutMD5` — `thumbnail.go:160-163`               |

Pooled RGBA destinations (added by `feat/thumbnail-xdraw-phash-pool`):

- **Thumb canvas** — `acquireGalleryThumb` (`thumbnail.go:133`) draws only the `fitInsideBox` sub-rectangle of the pooled 200×150 canvas; the release func returns the full canvas (`resetThumbRGBA` clears `Pix` and restores the 200×150 geometry, `thumbnail.go:104-108`).
- **pHash canvas** — `acquirePHashRGBA` (`thumbnail.go:146`) scales into the pooled 64×64 canvas; `resetPHashRGBA` clears it on Put (`thumbnail.go:118-121`).

Not pooled:

- **Source decode result** — the decoded `srcImg` (always the full-image decode; there is no embedded-EXIF-thumbnail source) is a plain `image.Image` allocation.
- On error paths, `GenerateThumbnailAndHashes` returns non-pooled literals `&sql.NullString{}` / `&sql.NullInt64{}` (e.g. `thumbnail.go:217,221`) — these must **not** be returned to the pools (the plan's bench helpers honor this).

---

## 4. EXIF path — removed

There is no separate EXIF decode path. The embedded-EXIF-thumbnail shortcut was
removed, and every input decodes through the single full-image path described in
§1 (JPEG via go-scaled-jpeg at an adaptive DCT scale, non-JPEG via stdlib
`image.Decode`; hard-fail on error). Pre-change EXIF-path bench numbers remain in
[`phase-1-results.md`](phase-1-results.md).

---

## 5. Caller concurrency

- Call site: `thumbnail.GenerateThumbnailAndHashes(imageFile, int(config.Width), int(config.Height))` inside `processFileContents` — `internal/server/files/processor.go:244`, where `config` is the `image.DecodeConfig` result already read during discovery (`processor.go:231`). This runs under the file-processing worker pool: `runPoolWorkerWithProcessor` (`processor.go:265`) wrapped by `NewPoolFuncWithProcessor` (`processor.go:259`), started via `Pool.StartWorkerPool` (`workerpool.go:166`). On success the caller returns the pooled `phash`/`md5` to the pools — `processor.go:252-253`.
- Pool construction: `m.pool = workerpool.NewPool(ctx, maxWorkers, minIdle, maxIdleTime)` — `internal/server/subsystem_manager.go:158`. Config values 0/0 mean **min 0 (no idle workers) + auto max**: `NewPool` calls `getMinMaxPoolWorkers` — `workerpool.go:60` — which auto-fills only the max.
- Default formulas, `getMinMaxPoolWorkers` — `internal/workerpool/workerpool.go:92` (uses `runtimeNumCPU()` hook, `workerpool.go:20-21`):
  - **Max workers** (`workerpool.go:93-103`): `numCPU > 4` → `numCPU - 2`; `2 < numCPU <= 4` → `2`; else `1`.
  - **Min workers** is never auto-calculated: 0 is honored as "no idle workers". So on a typical 8-core host: min 0, max 6 (`numCPU-2`).

---

## 6. nfnt inner parallelism

> **Historical note (post `feat/thumbnail-xdraw-phash-pool`):** nfnt is now used
> by **tests/benches only** — the production thumb and pHash squashes both use
> `draw.ApproxBiLinear` into pooled canvases, so the two `Resize` calls below no
> longer run on the production path. This section describes the nfnt library
> behavior the Phase 0 benches exercised.

Source: `github.com/nfnt/resize@v0.0.0-20180221191011-83c6a9932646` (module cache dir resolved via `go list -m`; `resize.go`).

- **Line 106:** `cpus := runtime.GOMAXPROCS(0)` in the generic (non-nearest) `resize()` path — the path used by **both** interpolation functions this app calls (Lanczos3, Bilinear; both have `kernel()` taps ≠ nearest, `resize.go:58-73`).
- **Horizontal pass** spawns `cpus` goroutines (`resize.go:119` `wg.Add(cpus)`; goroutines at `resize.go:120-125`; `wg.Wait()` at `resize.go:127`).
- **Vertical pass** spawns `cpus` goroutines (`resize.go:131` `wg.Add(cpus)`; goroutines at `resize.go:132-137`; `wg.Wait()` at `resize.go:139`).
- Each pass allocates a transposed intermediate `temp` plus the `result` image (`resize.go:109-110`); the doc comment says the algorithm "uses channels for parallel computation" (`resize.go:75`).
- **Second site:** `resizeNearest` also does `cpus := runtime.GOMAXPROCS(0)` at `resize.go:351` with the same `wg.Add(cpus)` horizontal (`resize.go:362`) / vertical (`resize.go:374`) fan-out — but nearest-neighbor is **not** used by the thumbnail path (both app calls are non-nearest), so `resize.go:106` is the operative site.
- **Two `Resize` calls per full generation (historical, Phase 0 production):**
  (1) `thumbnail(200,150,srcImg,Lanczos3)` → `resize.Resize`; (2)
  `resize.Resize(64,64,srcImg,Bilinear)`. Both used the nfnt helper now at
  `internal/thumbnail/nfnt_resize_test.go:16` (tests/benches only; production
  thumb/pHash use `draw.ApproxBiLinear`). Each performs the full parallel
  two-pass fan-out on the same `srcImg` and allocates its own intermediate +
  result images.
- Net effect: each file-processor worker that performs a full generation forks up to `2 × GOMAXPROCS` goroutines per `Resize` call, and two such calls per image, on top of the outer worker pool's ~`NumCPU-2` workers — the oversubscription the plan hypothesizes about.

---

## 7. Smoke bench — `BenchmarkGenerateThumbnailAndHashes` (`internal/thumbnail/characterization_bench_test.go:27`, `package thumbnail`)

Current state at HEAD (post Task 0.3 hygiene fix):

- **Location / package:** the smoke bench moved out of `package thumbnail_test` (`thumbnail_test.go`) into `characterization_bench_test.go` with `package thumbnail` (same package as production), so it can call unexported helpers. Task 0.3 deleted the old `thumbnail_test.go` copy and the dead commented-out `BenchmarkGenerateThumbnailAlt1/2` stubs (`thumbnail_test.go:406-435` pre-fix).
- **Fixture:** exercises the committed no-EXIF-thumbnail fixture `testdata/thumbnail/no-exif-thumb.jpg` via `benchFixtureDir` (`characterization_bench_test.go:28`).
- **Hygiene (fixed):** per-iteration `seekStart` (line 34), success-path pool return via `benchPutResults` → `PutBytesBuffer` / `PutNullString` / `PutNullInt64` (line 39), `b.ReportAllocs()` (line 31). The pre-fix bench reused a single `*os.File` without rewinding, discarded results without pool return, and did not report allocs.
- **Coverage gaps closed by Phase 1 benches** (same file, `package thumbnail`):
  - Phase split: `BenchmarkPhase_*` — decode, resize thumb (Lanczos3) / pHash (Bilinear), JPEG encode, MD5, full generate.
  - Production-path fixture: `BenchmarkFull_HasEXIFMetadata` (on `exif-thumb.jpg` only) and `BenchmarkFull_EXIFMiss` (no-EXIF fixture).
  - Size matrix: `BenchmarkFull_Size` / `BenchmarkFull_Size_FullDecode` / `BenchmarkResize_Size` at 2 / 12 / 25 MP synthetics.
  - Concurrency: `BenchmarkFull_Parallel_EXIFMiss_2mp` / `_12mp` / `BenchmarkFull_Parallel_FullDecode_12mp`.
  - Peak RSS: captured by `scripts/bench_thumbnail_characterization.sh` (`/usr/bin/time -v` on parallel 12 MP).
- **Naming:** `*HasEXIFMetadata*` = the production path on `exif-thumb.jpg` only; `*FullDecode*` = synthetic full-path benches.
- **Remaining smoke-bench limitation:** the smoke bench alone still measures only the full pipeline on one no-EXIF-thumbnail fixture — it is a regression/smoke gate, not a characterization tool; phase attribution lives in `BenchmarkPhase_*`. Historical `*EXIFHit*` / Phase 1 EXIF-extract benches live in [`phase-1-results.md`](phase-1-results.md) (pre-change).

---

## 8. Fixture inventory — `testdata/thumbnail/` (all files, `ls -la`)

| File                        | Size    | Role (verified against `generate.go` and tests)                                                                                                                                                                                                                                                                                                                                   |
| --------------------------- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `exif-thumb.jpg`            | 9,259 B | JPEG with an embedded EXIF thumbnail (800×600 main image), written via exiftool (`generate.go:39-42`, `generate.go:79+`). The full image decodes at an adaptive DCT scale (800×600 → dct 2, i.e. 200×150). Production-path fixture for `BenchmarkFull_HasEXIFMetadata` and `TestGenerateThumbnailAndHashes_HasEXIFMetadata`.                                                      |
| `no-exif-thumb.jpg`         | 8,195 B | Plain 800×600 JPEG, **no** EXIF thumbnail (`generate.go:33-37`). Full-image decode path.                                                                                                                                                                                                                                                                                          |
| `exif-thumb.tiff`           | 959 B   | Minimal TIFF with only an embedded thumbnail (no full image payload) (`generate.go:44-52`, `generate.go:88-93`). TIFF has no decoder on the generate path, so TIFF inputs hard-fail generation.                                                                                                                                                                                   |
| `exif-thumb.webp`           | 986 B   | WebP with `EXIF` chunk containing the standard `Exif\x00\x00` prefix (`generate.go:54-57`). Thumb-only fixture (no full image payload); full-image WebP still decodes, thumb-only WebP may hard-fail.                                                                                                                                                                             |
| `exif-thumb-no-prefix.webp` | 980 B   | WebP with `EXIF` chunk but **no** `Exif\x00\x00` prefix (`generate.go:59-62`). Thumb-only fixture (no full image payload).                                                                                                                                                                                                                                                        |
| `truncated-app1.jpg`        | 16 B    | SOI + APP1 marker claiming 16 bytes with only 10 payload bytes (`generate.go:64-68`). Edge case: the corrupt stream is rejected by the go-scaled-jpeg full-image decode → `GenerateThumbnailAndHashes` **hard-fails** with a clean error (no panic, no stdlib fallback); contract pinned by `TestGenerateThumbnailAndHashesTruncatedAPP1HardFails` (`truncated_app1_test.go:22`). |
| `generate.go`               | 6,119 B | Regenerator with `//go:build ignore` (`generate.go:1-3`); requires `exiftool` for the JPEG fixture (`generate.go:79+`). Not compiled into tests.                                                                                                                                                                                                                                  |

Tests resolve these via `filepath.Join("..", "..", "testdata", "thumbnail")` — `internal/thumbnail/thumbnail_test.go:288`, `internal/thumbnail/hooks_test.go:23`, and `internal/thumbnail/full_decode_hook_test.go:23`.

---

## 9. Hypotheses assessed by Phase 1 (see `docs/thumbnail-bench/phase-1-results.md` §7)

1. **Has-EXIF-thumbnail path may dominate so hard that large-resize cost is invisible on has-EXIF fixtures.** Supported by **pre-change** Phase 1 evidence: full path ≈ 4.9 ms with EXIF thumbnail vs ≈ 33.5 ms with none (historical `BenchmarkFull_EXIFHit` vs `BenchmarkFull_EXIFMiss`); when an EXIF thumbnail was used, the big-image `Decode` and Lanczos3 `ResizeThumb` phases did not run. The embedded-EXIF shortcut no longer exists — every image takes the full-image decode path — so this hypothesis describes the pre-change pipeline only (numbers in [`phase-1-results.md`](phase-1-results.md)).
2. **On large JPEGs with no EXIF thumbnail, decode + Lanczos3 may dominate.** Supported for the resize part: `Resize_Size` is 50.7–60.1% of the full pipeline at 2–25 MP (medians); on the small committed no-EXIF-thumbnail fixture the phase split attributes ≈ 60.6% to `ResizeThumb` and ≈ 21.3% to the pHash squash, with decode ≈ 15.1%. Resize (thumb + pHash ≈ 82%) dominates; decode is secondary.
3. **Under N concurrent full generations, peak RSS may be dominated by concurrent decoded sources, not 200×150 output.** Supported: each 12 MP op allocates ≈ 92.8 MB (B/op) — decoded 4000×3000 image plus nfnt pass buffers — while thumbnail buffers are pooled and reused. Parallel 12 MP peak RSS 456.9 MiB is consistent with ~4–5 concurrent in-flight working sets dominated by the decoded full-resolution image.
4. **Outer workers × nfnt `GOMAXPROCS` may hurt throughput vs serial-per-image resize.** Mixed: `RunParallel` per-op wall time _decreases_ ~2.9–3.9× at `-cpu=4` (≈3× throughput scaling), so concurrent throughput scales well; but serial benches at `-cpu=4` are mostly _slower_ (decode 2.19×, full has-EXIF 1.25× — the has-EXIF arm is pre-change — full no-EXIF 1.27×) while resize-only 12/25 MP benches get 1.5–1.6× faster from nfnt's internal fan-out. Whether a serial-per-image resize beats the current nesting was **not** tested (no A/B — see results §8).

Phase 1 measured these with phase-split, has/no EXIF thumbnail, size-matrix, parallel, and peak-RSS benches; findings above are from the actual artifacts (results doc §7). Items still unmeasured are called out in results §8 (non-conclusions): filter A/B, `x/image/draw`, disabling nfnt fan-out, quality.
