# Production Switch — Gallery Thumb Filter `draw.ApproxBiLinear`

**Branch:** `feat/thumbnail-xdraw-approx` (switch); follow-up `feat/thumbnail-xdraw-phash-pool`
**Date:** 2026-08-05
**Status:** shipped in code; **not** treated as accepted until visual QC on the Dev Server (see [Visual QC required](#visual-qc-required)).

> **Follow-up (`feat/thumbnail-xdraw-phash-pool`):** current production is now
> **Thumb** = `draw.ApproxBiLinear` into a **pooled** 200×150 canvas; **pHash** =
> `draw.ApproxBiLinear` 64×64 from the **gallery thumb**, pooled; **nfnt** is
> used by **tests/benches only**. Visual QC is still recommended for this
> follow-up. See [`production-xdraw-phash-pool.md`](production-xdraw-phash-pool.md).

---

## What changed

The gallery display thumbnail is now produced with `golang.org/x/image/draw`'s
**`draw.ApproxBiLinear`** instead of nfnt **Lanczos3**:

- Call path **at the time of this switch**: `GenerateThumbnailAndHashes` →
  `thumbResizeHook(srcImg)` → `defaultThumbResize` → `resizeThumbApproxBiLinear`
  (`internal/thumbnail/thumbnail.go`). Current production wiring is
  `acquireGalleryThumb` (pooled canvas) — see the follow-up note above and
  [`production-xdraw-phash-pool.md`](production-xdraw-phash-pool.md).
- `defaultThumbResize` is the production default of `thumbResizeHook`
  (`thumbnail.go:46`, `thumbnail.go:51-52`).
- `resizeThumbApproxBiLinear` fits the source inside the 200×150 box
  (`fitInsideBox`) and scales with `draw.ApproxBiLinear.Scale` into a new
  `*image.RGBA` (`thumbnail.go:136-140`).

## What did NOT change

- **EXIF thumbnail extraction stays ON** — the embedded-EXIF fast path
  (`extractEXIFThumbnail`) is untouched; ApproxBiLinear only runs when a full
  decode is needed.
- **200×150 fit geometry unchanged** — same `fitInsideBox(200, 150)` math the
  previous `thumbnail(200, 150, ...)` helper used.
- **pHash squash size unchanged** — still an exact 64×64 squash; the follow-up
  switched its filter to `draw.ApproxBiLinear` and its input to the gallery
  thumb (see [`production-xdraw-phash-pool.md`](production-xdraw-phash-pool.md)).
- **Destination pooling added by the follow-up** — pooled 200×150 and 64×64
  RGBA canvases now back the thumb and pHash destinations; at the time of this
  switch `resizeThumbApproxBiLinear` allocated a fresh `*image.RGBA` per call.
- **nfnt removed from production by the follow-up** — nfnt now lives in
  tests/benches only (`nfnt_resize_test.go`).

## Numbers

Measurement record for the resize alternatives (resize-only, full-path,
parallel, RSS, sample metrics) is in
[`phase-2-results.md`](phase-2-results.md) and the Phase 1/1b characterization
in [`phase-1-results.md`](phase-1-results.md). Those tables are the
pre-switch record; this document only records the production filter switch.

## Visual QC required

ApproxBiLinear is a faster but lower-quality interpolation than Lanczos3.
**Visual QC on the Dev Server is required before this switch is treated as
accepted**: eyeball gallery thumbnails (small/medium/large source images,
portrait and landscape, upscaled embedded EXIF thumbs) for acceptable
sharpness and artifacts, and compare against a Lanczos3 reference if desired.
Until that QC sign-off, treat the switch as a performance change under review.
