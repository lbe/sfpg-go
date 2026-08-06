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

---

## 1. Pipeline steps — `GenerateThumbnailAndHashes` (`internal/thumbnail/thumbnail.go`)

Entry point: `func GenerateThumbnailAndHashes(r io.ReadSeeker) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error)` — `thumbnail.go:125`.

1. Get a pooled `*bytes.Buffer` for the EXIF extract: `embBuf := GetBytesBuffer()` — `thumbnail.go:215`.
2. Try the embedded-EXIF fast path: `extractEXIFThumbnailHook(r, embBuf)` — `thumbnail.go:216`. On success, `jpeg.Decode(bytes.NewReader(embBuf.Bytes()))` — `thumbnail.go:217`; if the decode succeeds, `srcImg` is the embedded thumbnail; if it fails, log-and-fall-through (`thumbnail.go:218-220`).
3. Return the extract buffer to the pool: `PutBytesBuffer(embBuf)` — `thumbnail.go:223`.
4. If `srcImg == nil` (no usable EXIF thumbnail), fall back to full decode: seek to start (`thumbnail.go:226-228`) then `image.Decode(r)` — `thumbnail.go:229`. `srcImg` = full image.
5. Resize the source to the thumbnail box: `thumbImg, releaseThumb := acquireGalleryThumb(srcImg)` — `thumbnail.go:237`. The production path gets a **pooled** 200×150 `*image.RGBA` canvas (`thumbRGBAPool`, `thumbnail.go:93`), fits the source inside the box via `fitInsideBox(200, 150)` (`thumbnail.go:135`), scales the resulting sub-rectangle with `draw.ApproxBiLinear.Scale` (`thumbnail.go:137`), and defers release of the full canvas back to the pool (`thumbnail.go:238`). When tests/benches replace `thumbResizeHook`, the hook result is used without pooling (`thumbnail.go:131`); the default hook is `defaultThumbResize` → `resizeThumbApproxBiLinear` (`thumbnail.go:51`, `thumbnail.go:192`), which allocates a fresh `*image.RGBA` for that (non-production-default) path.
6. Get a pooled `*bytes.Buffer` for the output: `thumbnailBytesBuffer := GetBytesBuffer()` — `thumbnail.go:242`; JPEG-encode: `jpegEncodeHook(thumbnailBytesBuffer, thumbImg, nil)` — `thumbnail.go:243` (hook default `jpeg.Encode`, `thumbnail.go:30`).
7. MD5 over the **full file bytes**: seek to start (`thumbnail.go:249-251`), pooled hasher `md5Hasher := GetMD5()` — `thumbnail.go:254` (`defer PutMD5(md5Hasher)` at `thumbnail.go:255`), stream copy `ioCopyHook(md5Hasher, r)` — `thumbnail.go:256` (hook default `io.Copy`, `thumbnail.go:33`), hex string via `fmt.Sprintf("%x", md5Hasher.Sum(nil))` into a pooled `*sql.NullString` — `thumbnail.go:262-263`.
8. pHash over the **gallery thumb** (the step-5 thumb image, before JPEG encode): `phashRGBA, releasePH := acquirePHashRGBA(thumbImg)` — `thumbnail.go:268`; a pooled 64×64 `*image.RGBA` canvas from `phashRGBAPool` (`thumbnail.go:108`) scaled with `draw.ApproxBiLinear` (`thumbnail.go:145`); pooled `phash := GetNullInt64()` — `thumbnail.go:270`; `phash64, err := newPHash64Hook(phashRGBA)` — `thumbnail.go:271` (hook default `imagehash.NewPHash64`, `thumbnail.go:36`); pHash error is logged but **not** fatal (`thumbnail.go:272-274`); result stored in the pooled `*sql.NullInt64` — `thumbnail.go:275-276`.
9. Return `(thumbnailBytesBuffer, md5, phash, nil)` — `thumbnail.go:278`.

Note: there are two `Seek(0, io.SeekStart)` calls on the success path (before full decode at `thumbnail.go:226` and before MD5 at `thumbnail.go:249`), plus one implicit seek inside `extractEXIFThumbnail` (`exifthumb.go:24-26`).

---

## 2. Resize parameters

| Use                 | Call site                                                                                                               | Target box                                  | Interpolation                           |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------- | ------------------------------------------- | --------------------------------------- |
| Display thumbnail   | `acquireGalleryThumb(srcImg)` — `thumbnail.go:237` (pooled 200×150 RGBA canvas from `thumbRGBAPool`, `thumbnail.go:93`) | fit inside **200×150** px, aspect preserved | **ApproxBiLinear** (`thumbnail.go:137`) |
| pHash source squash | `acquirePHashRGBA(thumbImg)` — `thumbnail.go:268` (pooled 64×64 RGBA canvas from `phashRGBAPool`, `thumbnail.go:108`)   | fixed **64×64**                             | **ApproxBiLinear** (`thumbnail.go:145`) |

The former production `thumbnail()` helper (nfnt) moved to
`internal/thumbnail/nfnt_resize_test.go:16` — tests/benches only. Production
geometry is `fitInsideBox` (`thumbnail.go:166`):

- Chooses width- or height-constrained dimensions so the result fits in the box: `maxW*origHeight <= maxH*origWidth` → width-constrained (`thumbnail.go:172-174`).
- Clamps `newWidth`/`newHeight` to ≥ 1 (`thumbnail.go:180-185`).
- **Upscales** smaller sources (unlike `resize.Thumbnail`), per the doc comment at `thumbnail.go:125-128`; the embedded 160×120 EXIF thumbnail in `exif-thumb.jpg` is upscaled to 200×150.

---

## 3. Pooling

Pooled (all backed by `internal/gensyncpool`, reset on `Put`; pool declarations span `thumbnail.go:56-160`):

| Pool              | Element                                                        | Get / Put                                                  |
| ----------------- | -------------------------------------------------------------- | ---------------------------------------------------------- |
| `bytesBufferPool` | `*bytes.Buffer` (Reset on Put)                                 | `GetBytesBuffer` / `PutBytesBuffer` — `thumbnail.go:56-65` |
| `nullStringPool`  | `*sql.NullString` (String+Valid cleared)                       | `GetNullString` / `PutNullString` — `thumbnail.go:68-77`   |
| `nullInt64Pool`   | `*sql.NullInt64` (Int64+Valid zeroed)                          | `GetNullInt64` / `PutNullInt64` — `thumbnail.go:80-89`     |
| `thumbRGBAPool`   | 200×150 `*image.RGBA` canvas (cleared + geometry reset on Put) | `acquireGalleryThumb` — `thumbnail.go:93-105`              |
| `phashRGBAPool`   | 64×64 `*image.RGBA` canvas (cleared on Put)                    | `acquirePHashRGBA` — `thumbnail.go:108-116`                |
| `md5Pool`         | `hash.Hash` from `md5.New()` (Reset on Put)                    | `GetMD5` / `PutMD5` — `thumbnail.go:151-160`               |

Pooled RGBA destinations (added by `feat/thumbnail-xdraw-phash-pool`):

- **Thumb canvas** — `acquireGalleryThumb` (`thumbnail.go:130`) draws only the `fitInsideBox` sub-rectangle of the pooled 200×150 canvas; the release func returns the full canvas (`resetThumbRGBA` clears `Pix` and restores the 200×150 geometry, `thumbnail.go:100-105`).
- **pHash canvas** — `acquirePHashRGBA` (`thumbnail.go:143`) scales into the pooled 64×64 canvas; `resetPHashRGBA` clears it on Put (`thumbnail.go:114-116`).

Not pooled:

- **Source decode result** — the decoded `srcImg` (embedded EXIF thumb or full image) is a plain `image.Image` allocation.
- On error paths, `GenerateThumbnailAndHashes` returns non-pooled literals `&sql.NullString{}` / `&sql.NullInt64{}` (e.g. `thumbnail.go:227,231`) — these must **not** be returned to the pools (the plan's bench helpers honor this).

---

## 4. EXIF path

**When taken:** `srcImg` comes from the embedded thumbnail only when _both_ conditions hold (`thumbnail.go:130-131`):

1. `extractEXIFThumbnail(r, embBuf)` returns `err == nil`, **and**
2. `jpeg.Decode(bytes.NewReader(embBuf.Bytes()))` succeeds (embedded data must be a decodable JPEG).

Otherwise (no embedded thumb, extraction error, or decode error) it falls back to `image.Decode` of the full file (`thumbnail.go:138-146`).

**Container formats supported by `extractEXIFThumbnail`** (`internal/thumbnail/exifthumb.go:23-72`), chosen by the first 12 signature bytes:

- **JPEG** (`0xFF 0xD8` SOI) — scans APP1 segments for an `Exif\x00\x00` ID via `findJPEGExif` (`exifthumb.go:34-36`, decl at `exifthumb.go:77`).
- **TIFF** (`II*\0` little-endian or `MM\0*` big-endian) — `tiffBase = 0` (`exifthumb.go:38-41`).
- **WebP** (`RIFF....WEBP`) — walks RIFF chunks for an `EXIF` chunk via `findWebPExif`, skipping an optional `Exif\x00\x00` prefix (`exifthumb.go:43-45`, decl at `exifthumb.go:115`).
- Anything else → `errNoThumb` (`exifthumb.go:46-49`).

Extraction then parses IFD1 for `JPEGInterchangeFormat` (0x0201) / `JPEGInterchangeFormatLength` (0x0202) via `findIFD1Thumb` (`exifthumb.go:53`, decl at `exifthumb.go:147`), copies up to `maxThumbSize` = 4 MiB (`exifthumb.go:12`, length cap at `exifthumb.go:149`), and requires the extracted bytes to start with a JPEG SOI (`exifthumb.go:66-70`). `maxIFDEntries = 512` guards malformed IFDs (`exifthumb.go:14`).

---

## 5. Caller concurrency

- Call site: `thumbnail.GenerateThumbnailAndHashes(imageFile)` inside `processFileContents` — `internal/server/files/processor.go:244` (enclosing func declared `processor.go:198`). This runs under the file-processing worker pool: `runPoolWorkerWithProcessor` (`processor.go:265`) wrapped by `NewPoolFuncWithProcessor` (`processor.go:259`), started via `Pool.StartWorkerPool` (`workerpool.go:166`). On success the caller returns the pooled `phash`/`md5` to the pools — `processor.go:252-253`.
- Pool construction: `m.pool = workerpool.NewPool(ctx, maxWorkers, minIdle, maxIdleTime)` — `internal/server/subsystem_manager.go:142`. When config values are 0/0 ("auto-calculate based on CPU", comment at `subsystem_manager.go:126-127`), `NewPool` calls `getMinMaxPoolWorkers` — `workerpool.go:60`.
- Default formulas, `getMinMaxPoolWorkers` — `internal/workerpool/workerpool.go:91-112` (uses `runtimeNumCPU()` hook, `workerpool.go:20-21`):
  - **Max workers** (`workerpool.go:95-100`): `numCPU > 4` → `numCPU - 2`; `2 < numCPU <= 4` → `2`; else `1`.
  - **Min workers** (`workerpool.go:102-108`): `(numCPU - 2) > 4` (i.e. `numCPU > 6`) → `4`; `2 < numCPU <= 4` → `2`; else `1`.
  - So on a typical 8-core host: min 4, max 6 (`numCPU-2`). The plan's "default max roughly NumCPU−2" is confirmed.

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
- **Two `Resize` calls per full generation:** (1) `thumbnail(200,150,srcImg,Lanczos3)` → `resize.Resize` at `thumbnail.go:114`; (2) `resize.Resize(64,64,srcImg,Bilinear)` at `thumbnail.go:180`. Each performs the full parallel two-pass fan-out on the same `srcImg` and allocates its own intermediate + result images.
- Net effect: each file-processor worker that performs a full (EXIF-miss) generation forks up to `2 × GOMAXPROCS` goroutines per `Resize` call, and two such calls per image, on top of the outer worker pool's ~`NumCPU-2` workers — the oversubscription the plan hypothesizes about.

---

## 7. Smoke bench — `BenchmarkGenerateThumbnailAndHashes` (`internal/thumbnail/characterization_bench_test.go:26`, `package thumbnail`)

Current state at HEAD (post Task 0.3 hygiene fix):

- **Location / package:** the smoke bench moved out of `package thumbnail_test` (`thumbnail_test.go`) into `characterization_bench_test.go` with `package thumbnail` (same package as production), so it can call unexported helpers. Task 0.3 deleted the old `thumbnail_test.go` copy and the dead commented-out `BenchmarkGenerateThumbnailAlt1/2` stubs (`thumbnail_test.go:406-435` pre-fix).
- **Fixture:** exercises the committed EXIF-miss fixture `testdata/thumbnail/no-exif-thumb.jpg` via `benchFixtureDir` (`characterization_bench_test.go:27`).
- **Hygiene (fixed):** per-iteration `seekStart` (line 33), success-path pool return via `benchPutResults` → `PutBytesBuffer` / `PutNullString` / `PutNullInt64` (line 38), `b.ReportAllocs()` (line 30). The pre-fix bench reused a single `*os.File` without rewinding, discarded results without pool return, and did not report allocs.
- **Coverage gaps closed by Phase 1 benches** (same file, `package thumbnail`):
  - Phase split: `BenchmarkPhase_*` — EXIF extract hit/miss, decode, resize thumb (Lanczos3) / pHash (Bilinear), JPEG encode, MD5, full generate.
  - EXIF ±: `BenchmarkFull_EXIFHit` / `BenchmarkFull_EXIFMiss`.
  - Size matrix: `BenchmarkFull_Size` / `BenchmarkResize_Size` at 2 / 12 / 25 MP.
  - Concurrency: `BenchmarkFull_Parallel_EXIFMiss_2mp` / `_12mp` / `BenchmarkFull_Parallel_EXIFHit`.
  - Peak RSS: captured by `scripts/bench_thumbnail_characterization.sh` (`/usr/bin/time -v` on parallel 12 MP).
- **Remaining smoke-bench limitation:** the smoke bench alone still measures only the full pipeline on one EXIF-miss fixture — it is a regression/smoke gate, not a characterization tool; phase attribution lives in `BenchmarkPhase_*`.

---

## 8. Fixture inventory — `testdata/thumbnail/` (all files, `ls -la`)

| File                        | Size    | Role (verified against `generate.go` and tests)                                                                                                                                                                             |
| --------------------------- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `exif-thumb.jpg`            | 9,259 B | JPEG with embedded EXIF (IFD1) thumbnail — 800×600 main + 160×120 embedded thumb, written via exiftool (`generate.go:39-42`, `generate.go:79+`). EXIF hit.                                                                  |
| `no-exif-thumb.jpg`         | 8,195 B | Plain 800×600 JPEG, **no** EXIF thumbnail (`generate.go:33-37`). EXIF miss → full decode path.                                                                                                                              |
| `exif-thumb.tiff`           | 959 B   | Minimal TIFF: empty IFD0 pointing at IFD1 with JPEGInterchangeFormat/Length tags (`generate.go:44-52`, `generate.go:88-93`). EXIF hit (TIFF container).                                                                     |
| `exif-thumb.webp`           | 986 B   | WebP with `EXIF` chunk containing the standard `Exif\x00\x00` prefix (`generate.go:54-57`). EXIF hit (WebP container).                                                                                                      |
| `exif-thumb-no-prefix.webp` | 980 B   | WebP with `EXIF` chunk but **no** `Exif\x00\x00` prefix (`generate.go:59-62`). EXIF hit (prefix-skip path).                                                                                                                 |
| `truncated-app1.jpg`        | 16 B    | SOI + APP1 marker claiming 16 bytes with only 10 payload bytes (`generate.go:64-68`). Edge case: extraction fails and full decode also fails → `GenerateThumbnailAndHashes` returns an error (`thumbnail_test.go:322-328`). |
| `generate.go`               | 6,119 B | Regenerator with `//go:build ignore` (`generate.go:1-3`); requires `exiftool` for the JPEG fixture (`generate.go:79+`). Not compiled into tests.                                                                            |

Tests resolve these via `filepath.Join("..", "..", "testdata", "thumbnail")` — `internal/thumbnail/exifthumb_test.go:15` and `internal/thumbnail/thumbnail_test.go:247`.

---

## 9. Hypotheses assessed by Phase 1 (see `docs/thumbnail-bench/phase-1-results.md` §7)

1. **EXIF-hit path may dominate so hard that large-resize cost is invisible on EXIF-hit fixtures.** Supported: full path ≈ 4.9 ms on hit vs ≈ 33.5 ms on miss (`BenchmarkFull_EXIFHit` vs `BenchmarkFull_EXIFMiss`); on the hit path the big-image `Decode` and Lanczos3 `ResizeThumb` phases do not run (embedded thumbnail is used instead). The big-image resize cost is indeed invisible on EXIF-hit fixtures.
2. **On EXIF-miss large JPEGs, decode + Lanczos3 may dominate.** Supported for the resize part: `Resize_Size` is 50.7–60.1% of the full pipeline at 2–25 MP (medians); on the small committed miss fixture the phase split attributes ≈ 60.6% to `ResizeThumb` and ≈ 21.3% to the pHash squash, with decode ≈ 15.1%. Resize (thumb + pHash ≈ 82%) dominates; decode is secondary.
3. **Under N concurrent full generations, peak RSS may be dominated by concurrent decoded sources, not 200×150 output.** Supported: each 12 MP op allocates ≈ 92.8 MB (B/op) — decoded 4000×3000 image plus nfnt pass buffers — while thumbnail buffers are pooled and reused. Parallel 12 MP peak RSS 456.9 MiB is consistent with ~4–5 concurrent in-flight working sets dominated by the decoded full-resolution image.
4. **Outer workers × nfnt `GOMAXPROCS` may hurt throughput vs serial-per-image resize.** Mixed: `RunParallel` per-op wall time _decreases_ ~2.9–3.9× at `-cpu=4` (≈3× throughput scaling), so concurrent throughput scales well; but serial benches at `-cpu=4` are mostly _slower_ (decode 2.19×, full EXIF-hit 1.25×, full EXIF-miss 1.27×) while resize-only 12/25 MP benches get 1.5–1.6× faster from nfnt's internal fan-out. Whether a serial-per-image resize beats the current nesting was **not** tested (no A/B — see results §8).

Phase 1 measured these with phase-split, EXIF ±, size-matrix, parallel, and peak-RSS benches; findings above are from the actual artifacts (results doc §7). Items still unmeasured are called out in results §8 (non-conclusions): filter A/B, `x/image/draw`, disabling nfnt fan-out, quality.
