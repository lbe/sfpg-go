// Package thumbnail provides functionality for generating image thumbnails
// and computing image hashes (MD5, pHash). It uses lightweight pooling for
// buffers and hashers to minimize allocations and improve performance.
package thumbnail

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"hash"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"

	"github.com/evanoberholster/imagemeta/imagehash"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

// galleryThumbMaxW and galleryThumbMaxH are the untyped dimensions of the
// gallery thumbnail box. They are untyped so they implicitly convert to both
// int and uint at every use site (no casts).
const (
	galleryThumbMaxW = 200
	galleryThumbMaxH = 150
)

var (
	// jpegEncodeHook is a testable hook for image/jpeg.Encode.
	jpegEncodeHook = jpeg.Encode

	// ioCopyHook is a testable hook for io.Copy.
	ioCopyHook = io.Copy

	// newPHash64Hook is a testable hook for imagehash.NewPHash64.
	newPHash64Hook = imagehash.NewPHash64

	// fullImageDecodeHook is a testable hook for the full-image source decode.
	// Production default: decodeFullImage (go-scaled-jpeg at an adaptive DCT
	// scale for JPEGs, stdlib image.Decode otherwise; see scaled_jpeg_decode.go).
	// Tests may replace it; they must restore the default (e.g. via t.Cleanup).
	fullImageDecodeHook func(r io.Reader, srcW, srcH int) (image.Image, string, error) = decodeFullImage
)

// defaultThumbResizeFn holds the production default as an addressable func value.
// Go forbids &defaultThumbResize on a function identifier; the var is required.
var defaultThumbResizeFn = defaultThumbResize

// thumbResizeHook points at the active gallery-thumb resizer.
// Production default: &defaultThumbResizeFn.
// Tests/benches may point it at another func variable; restore via Cleanup.
var thumbResizeHook = &defaultThumbResizeFn

// defaultThumbResize resizes the decoded source to the gallery thumbnail.
// It fits inside a 200×150 box using draw.ApproxBiLinear (golang.org/x/image/draw).
func defaultThumbResize(img image.Image) image.Image {
	return resizeThumbApproxBiLinear(img)
}

// Buffers are Reset on Put (return) for cleanliness, matching conventional pattern.
var bytesBufferPool = gensyncpool.New(
	func() *bytes.Buffer { return &bytes.Buffer{} },
	func(b *bytes.Buffer) { b.Reset() },
)

// GetBytesBuffer retrieves a bytes.Buffer from the pool.
func GetBytesBuffer() *bytes.Buffer { return bytesBufferPool.Get() }

// PutBytesBuffer returns a bytes.Buffer to the pool, resetting it first.
func PutBytesBuffer(buf *bytes.Buffer) { bytesBufferPool.Put(buf) }

// nullStringPool is a gensyncpool-backed pool for *sql.NullString.
var nullStringPool = gensyncpool.New(
	func() *sql.NullString { return &sql.NullString{} },
	func(ns *sql.NullString) { ns.String = ""; ns.Valid = false },
)

// GetNullString retrieves an sql.NullString from the pool.
func GetNullString() *sql.NullString { return nullStringPool.Get() }

// PutNullString returns an sql.NullString to the pool, resetting it first.
func PutNullString(ns *sql.NullString) { nullStringPool.Put(ns) }

// nullInt64Pool is a gensyncpool-backed pool for *sql.NullInt64.
var nullInt64Pool = gensyncpool.New(
	func() *sql.NullInt64 { return &sql.NullInt64{} },
	func(ni *sql.NullInt64) { ni.Int64 = 0; ni.Valid = false },
)

// GetNullInt64 retrieves an sql.NullInt64 from the pool.
func GetNullInt64() *sql.NullInt64 { return nullInt64Pool.Get() }

// PutNullInt64 returns an sql.NullInt64 to the pool, resetting it first.
func PutNullInt64(ni *sql.NullInt64) { nullInt64Pool.Put(ni) }

// thumbRGBAPool is a gensyncpool-backed pool of 200×150 *image.RGBA canvases
// used as the destination for gallery thumbnail scaling.
var thumbRGBAPool = gensyncpool.New(
	func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, galleryThumbMaxW, galleryThumbMaxH)) },
	resetThumbRGBA,
)

// resetThumbRGBA clears a pooled gallery-thumbnail canvas and restores its
// geometry to the full 200×150 canvas (Stride 200*4) before it is reused.
func resetThumbRGBA(img *image.RGBA) {
	clear(img.Pix)
	img.Rect = image.Rect(0, 0, galleryThumbMaxW, galleryThumbMaxH)
	img.Stride = galleryThumbMaxW * 4
}

// phashRGBAPool is a gensyncpool-backed pool of 64×64 *image.RGBA canvases
// used as the destination for pHash normalization scaling.
var phashRGBAPool = gensyncpool.New(
	func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 64, 64)) },
	resetPHashRGBA,
)

// resetPHashRGBA clears a pooled pHash canvas before it is reused.
func resetPHashRGBA(img *image.RGBA) {
	clear(img.Pix)
}

// thumbResizeIsDefault reports whether thumbResizeHook still points at the
// production default func variable (defaultThumbResizeFn) via pointer identity.
func thumbResizeIsDefault() bool {
	return thumbResizeHook == &defaultThumbResizeFn
}

// acquireGalleryThumb returns a gallery thumbnail fitted inside a 200×150 box
// via draw.ApproxBiLinear, plus a release func that returns its destination
// canvas to the pool. When thumbResizeHook is replaced (non-default), the hook
// result is returned unchanged and release is a no-op so stub images never
// enter the pool.
func acquireGalleryThumb(src image.Image) (img image.Image, release func()) {
	if !thumbResizeIsDefault() {
		return (*thumbResizeHook)(src), func() {}
	}
	full := thumbRGBAPool.Get()
	w, h := fitInsideBox(galleryThumbMaxW, galleryThumbMaxH, src)
	view := full.SubImage(image.Rect(0, 0, w, h)).(*image.RGBA)
	draw.ApproxBiLinear.Scale(view, view.Bounds(), src, src.Bounds(), draw.Src, nil)
	return view, func() { thumbRGBAPool.Put(full) }
}

// acquirePHashRGBA returns a 64×64 ApproxBiLinear scale of src in a pooled
// *image.RGBA, plus a release func that returns the canvas to the pool.
func acquirePHashRGBA(src image.Image) (dst *image.RGBA, release func()) {
	dst = phashRGBAPool.Get()
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	return dst, func() { phashRGBAPool.Put(dst) }
}

// md5Pool is a gensyncpool-backed pool for hash.Hash implementations (md5.New()).
// Each hash is Reset on Put.
var md5Pool = gensyncpool.New(
	md5.New,
	func(h hash.Hash) { h.Reset() },
)

// GetMD5 retrieves an MD5 hash.Hash from the pool.
func GetMD5() hash.Hash { return md5Pool.Get() }

// PutMD5 returns an MD5 hash.Hash to the pool, resetting it first.
func PutMD5(h hash.Hash) { md5Pool.Put(h) }

// fitInsideBoxDims returns width and height that fit a srcW×srcH source inside
// a maxW×maxH box while preserving aspect ratio, with a minimum of 1 in each
// dimension. It upscales sources smaller than the box, matching the geometry
// historically used by thumbnail(). Integer math only; the caller provides the
// source dimensions so no image.Image is needed.
func fitInsideBoxDims(maxW, maxH, srcW, srcH int) (int, int) {
	var newW, newH int
	if maxW*srcH <= maxH*srcW {
		newW = maxW
		newH = srcH * maxW / srcW
	} else {
		newH = maxH
		newW = srcW * maxH / srcH
	}
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	return newW, newH
}

// fitInsideBox returns width and height that fit img inside a maxW×maxH box
// while preserving aspect ratio, with a minimum of 1 in each dimension. It
// upscales images smaller than the box, matching the geometry historically
// used by thumbnail().
func fitInsideBox(maxW, maxH uint, img image.Image) (int, int) {
	b := img.Bounds()
	return fitInsideBoxDims(int(maxW), int(maxH), b.Dx(), b.Dy())
}

// chooseJPEGDCTSize picks the go-scaled-jpeg DCTSizeScaled (dct/8 of source
// resolution; 8 is 1:1, 1 is 1/8) that decodes a srcW×srcH JPEG to at least
// the gallery-thumb fit size: the decoded JPEG must cover the 200×150 fit
// box so the subsequent ApproxBiLinear downscale never upscales. Large JPEGs
// resolve to dct 1 (1/8) and small ones to larger dct values, clamped to [1,8].
func chooseJPEGDCTSize(srcW, srcH int) int {
	// Guard against non-positive dimensions so a bad caller cannot trigger
	// a divide-by-zero below. Clamping to 1 resolves to dct 8 (1:1 full
	// decode), so untrusted source sizes never cause an upscale.
	if srcW < 1 {
		srcW = 1
	}
	if srcH < 1 {
		srcH = 1
	}
	needW, needH := fitInsideBoxDims(galleryThumbMaxW, galleryThumbMaxH, srcW, srcH)
	dctW := (needW*8 + srcW - 1) / srcW // ceil(needW*8/srcW)
	dctH := (needH*8 + srcH - 1) / srcH // ceil(needH*8/srcH)
	dct := min(max(max(dctW, dctH), 1), 8)
	return dct
}

// resizeThumbApproxBiLinear fits img inside a 200×150 box and scales it with
// draw.ApproxBiLinear into a new *image.RGBA (no destination pooling).
func resizeThumbApproxBiLinear(img image.Image) image.Image {
	w, h := fitInsideBox(galleryThumbMaxW, galleryThumbMaxH, img)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

// GenerateThumbnailAndHashes creates a thumbnail for the image read from r.
// The full source image is always decoded via fullImageDecodeHook; srcW and
// srcH are the source image dimensions in pixels and drive an adaptive JPEG
// DCT scale: chooseJPEGDCTSize picks a DCTSizeScaled from srcW/srcH so the
// decoded JPEG is at least the 200×150 gallery-thumb fit size (large JPEGs
// stay at 1/8; non-JPEG formats ignore the dims and use stdlib image.Decode).
// The decoded source is fitted inside a 200x150 pixel box with
// draw.ApproxBiLinear into a pooled RGBA canvas and encoded as JPEG. It also
// calculates MD5 (over the full file bytes) and pHash: the gallery thumbnail
// is squashed to 64x64 with draw.ApproxBiLinear into a second pooled RGBA
// canvas, and imagehash.NewPHash64 is computed over that 64x64 canvas. Both
// pooled destinations are returned to their pools before the function
// returns. It returns the JPEG data as a bytes.Buffer, sql.NullString for
// MD5, and sql.NullInt64 for pHash, or an error if generation fails.
func GenerateThumbnailAndHashes(r io.ReadSeeker, srcW, srcH int) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	// Decode the full source image: rewind to the start, then hard-fail on
	// any decode error. There is no embedded-EXIF-thumbnail shortcut.
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}
	srcImg, _, decodeErr := fullImageDecodeHook(r, srcW, srcH)
	if decodeErr != nil {
		return nil, &sql.NullString{}, &sql.NullInt64{}, decodeErr
	}

	// Generate thumbnail image into a pooled 200x150 RGBA canvas.
	thumbImg, releaseThumb := acquireGalleryThumb(srcImg)
	defer releaseThumb()
	if thumbImg == nil {
		return nil, &sql.NullString{}, &sql.NullInt64{}, errors.New("thumbnail returned nil image")
	}
	thumbnailBytesBuffer := GetBytesBuffer()
	if err := jpegEncodeHook(thumbnailBytesBuffer, thumbImg, nil); err != nil {
		PutBytesBuffer(thumbnailBytesBuffer)
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}

	// Calculate MD5 hash over the full file bytes
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		slog.Error("GenerateThumbnailAndHashes seek", "err", err)
		PutBytesBuffer(thumbnailBytesBuffer)
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}
	md5Hasher := GetMD5()
	defer PutMD5(md5Hasher)
	if _, err := ioCopyHook(md5Hasher, r); err != nil {
		slog.Error("GenerateThumbnailAndHashes md5", "err", err)
		PutBytesBuffer(thumbnailBytesBuffer)
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}
	md5 := GetNullString()
	md5.Valid = true
	md5.String = fmt.Sprintf("%x", md5Hasher.Sum(nil))

	// Compute pHash from the gallery thumbnail: squash it to 64x64 with
	// draw.ApproxBiLinear into a pooled RGBA canvas (the standard pHash
	// normalization step), then hash that canvas.
	phashRGBA, releasePH := acquirePHashRGBA(thumbImg)
	defer releasePH()
	phash := GetNullInt64()
	phash64, err := newPHash64Hook(phashRGBA)
	if err != nil {
		slog.Error("GenerateThumbnailAndHashes imagehash.NewPHash64", "err", err)
	}
	phash.Valid = true
	phash.Int64 = int64(phash64)

	return thumbnailBytesBuffer, md5, phash, nil
}
