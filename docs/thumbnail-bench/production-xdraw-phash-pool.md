# Production — pHash from Gallery Thumb + Pooled RGBA Destinations

**Branch:** `feat/thumbnail-xdraw-phash-pool`
**Date:** 2026-08-05
**Status:** shipped in code; **not** treated as accepted until visual QC (see
[Visual QC](#visual-qc)). This is a follow-up to the `draw.ApproxBiLinear`
gallery-thumb switch recorded in
[`production-xdraw-approx.md`](production-xdraw-approx.md).

---

## What changed

On top of the production `draw.ApproxBiLinear` gallery thumb (branch
`feat/thumbnail-xdraw-approx`), this follow-up:

1. **Pools the thumb destination** — the gallery thumb is scaled into a pooled
   `*image.RGBA` canvas (`thumbRGBAPool`,
   `internal/thumbnail/thumbnail.go:93`); only the `fitInsideBox(200, 150)`
   sub-rectangle of the canvas is used, and the canvas is returned to the pool
   after the thumbnail pipeline completes.
2. **Pools the pHash destination** — the 64×64 pHash canvas (`phashRGBAPool`,
   `thumbnail.go:108`) is pooled and returned to the pool after hashing.
3. **pHash now comes from the gallery thumb** — `GenerateThumbnailAndHashes`
   squashes the in-memory gallery thumb (before JPEG encode) to 64×64 with
   `draw.ApproxBiLinear` (`acquirePHashRGBA`, `thumbnail.go:143`) instead of
   nfnt `resize.Resize(64, 64, srcImg, resize.Bilinear)` over the decoded
   source.
4. **nfnt removed from production** — `internal/thumbnail/thumbnail.go` has zero
   `github.com/nfnt/resize` imports; the nfnt `thumbnail()` helper now lives in
   `internal/thumbnail/nfnt_resize_test.go` (tests/benches only).

## Behavior

Current production `GenerateThumbnailAndHashes`
(`internal/thumbnail/thumbnail.go:211`):

1. **EXIF fast path unchanged:** the embedded thumbnail is extracted and decoded
   when present (`extractEXIFThumbnailHook`, `thumbnail.go:216-223`); otherwise
   full decode of the file (`thumbnail.go:225-231`).
2. **Thumb:** `acquireGalleryThumb(srcImg)` (`thumbnail.go:130`) gets a pooled
   200×150 RGBA canvas, computes `fitInsideBox(200, 150)` (`thumbnail.go:135`),
   scales the resulting sub-rectangle with `draw.ApproxBiLinear.Scale`
   (`thumbnail.go:137`), and returns a release func that puts the full canvas
   back into `thumbRGBAPool` (`thumbnail.go:138`); the release is deferred at
   the call site (`thumbnail.go:238`).
3. **JPEG encode** of the thumb (`thumbnail.go:242-247`).
4. **MD5** over the full file bytes, unchanged (`thumbnail.go:249-263`).
5. **pHash:** `acquirePHashRGBA(thumbImg)` (`thumbnail.go:143`) scales the
   **gallery thumb** to 64×64 with `draw.ApproxBiLinear` into a pooled canvas
   (`thumbnail.go:268`), then `imagehash.NewPHash64` hashes that canvas
   (`thumbnail.go:271`); the canvas is returned to `phashRGBAPool` via deferred
   release.

## Non-goals (explicitly unchanged / out of scope)

- **EXIF thumbnail extraction stays ON** — unchanged.
- **200×150 fit geometry unchanged** — same `fitInsideBox(200, 150)` math.
- **pHash size unchanged** — still an exact 64×64 aspect-distorting squash; only
  the input (gallery thumb) and filter (`draw.ApproxBiLinear`) changed.
- **MD5 input unchanged** — still over the full file bytes.
- **No history / DB pHash migration** — hashes may change; not migrated.
- **Merge** — out of scope; this branch stops at the QC gate.
- **Bench numbers** — the Phase 1/2 tables are the pre-change measurement record
  and are not rewritten (see the callouts in
  [`phase-1-results.md`](phase-1-results.md) and
  [`phase-2-results.md`](phase-2-results.md)).

## Visual QC

ApproxBiLinear is faster but lower-quality interpolation than Lanczos3, and
pHash now derives from the (already downscaled) gallery thumb rather than the
full decoded source — both change the produced thumbnails and hashes. **Visual
QC on the Dev Server is recommended before this follow-up is treated as
accepted**: eyeball gallery thumbnails (small/medium/large source images,
portrait and landscape, upscaled embedded EXIF thumbs) for acceptable sharpness
and artifacts, and compare against a Lanczos3 reference if desired. Until that
QC sign-off, treat this as a performance change under review.
