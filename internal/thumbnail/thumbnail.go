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

var (
	// jpegEncodeHook is a testable hook for image/jpeg.Encode.
	jpegEncodeHook = jpeg.Encode

	// ioCopyHook is a testable hook for io.Copy.
	ioCopyHook = io.Copy

	// newPHash64Hook is a testable hook for imagehash.NewPHash64.
	newPHash64Hook = imagehash.NewPHash64

	// extractEXIFThumbnailHook is a testable hook for extractEXIFThumbnail.
	// Production default is extractEXIFThumbnail. Tests/benches may replace it;
	// they must restore the default (e.g. via t.Cleanup / b.Cleanup).
	extractEXIFThumbnailHook = extractEXIFThumbnail
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
	func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 200, 150)) },
	resetThumbRGBA,
)

// resetThumbRGBA clears a pooled gallery-thumbnail canvas and restores its
// geometry to the full 200×150 canvas (Stride 200*4) before it is reused.
func resetThumbRGBA(img *image.RGBA) {
	clear(img.Pix)
	img.Rect = image.Rect(0, 0, 200, 150)
	img.Stride = 200 * 4
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
	w, h := fitInsideBox(200, 150, src)
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

// fitInsideBox returns width and height that fit img inside a maxW×maxH box
// while preserving aspect ratio, with a minimum of 1 in each dimension. It
// upscales images smaller than the box, matching the geometry historically
// used by thumbnail().
func fitInsideBox(maxW, maxH uint, img image.Image) (int, int) {
	origBounds := img.Bounds()
	origWidth := uint(origBounds.Dx())
	origHeight := uint(origBounds.Dy())

	var newWidth, newHeight uint
	if maxW*origHeight <= maxH*origWidth {
		newWidth = maxW
		newHeight = origHeight * maxW / origWidth
	} else {
		newHeight = maxH
		newWidth = origWidth * maxH / origHeight
	}

	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}

	return int(newWidth), int(newHeight)
}

// resizeThumbApproxBiLinear fits img inside a 200×150 box and scales it with
// draw.ApproxBiLinear into a new *image.RGBA (no destination pooling).
func resizeThumbApproxBiLinear(img image.Image) image.Image {
	w, h := fitInsideBox(200, 150, img)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

// GenerateThumbnailAndHashes creates a thumbnail for the image read from r.
// If the image (JPEG, TIFF, or WebP) contains an embedded EXIF thumbnail, that
// thumbnail is decoded instead of the full image, dramatically reducing memory
// use and CPU time. The source (embedded thumbnail or full image) is fitted
// inside a 200x150 pixel box with draw.ApproxBiLinear into a pooled RGBA
// canvas and encoded as JPEG. It also calculates MD5 (over the full file bytes)
// and pHash: the gallery thumbnail is squashed to 64x64 with
// draw.ApproxBiLinear into a second pooled RGBA canvas, and
// imagehash.NewPHash64 is computed over that 64x64 canvas. Both pooled
// destinations are returned to their pools before the function returns. It
// returns the JPEG data as a bytes.Buffer, sql.NullString for MD5, and
// sql.NullInt64 for pHash, or an error if generation fails.
func GenerateThumbnailAndHashes(r io.ReadSeeker) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	var srcImg image.Image

	// Try the embedded EXIF thumbnail first; fall back to full decode.
	embBuf := GetBytesBuffer()
	if err := extractEXIFThumbnailHook(r, embBuf); err == nil {
		if img, decErr := jpeg.Decode(bytes.NewReader(embBuf.Bytes())); decErr == nil {
			srcImg = img
		} else {
			slog.Debug("embedded thumbnail decode failed; falling back", "err", decErr)
		}
	}
	PutBytesBuffer(embBuf)

	if srcImg == nil {
		if _, err := r.Seek(0, io.SeekStart); err != nil {
			return nil, &sql.NullString{}, &sql.NullInt64{}, err
		}
		img, _, err := image.Decode(r)
		if err != nil {
			return nil, &sql.NullString{}, &sql.NullInt64{}, err
		}
		srcImg = img
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
