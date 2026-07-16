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
	"github.com/nfnt/resize"
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
)

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

// (Note) Image object pooling was removed from the codebase.

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

// thumbnail scales img to fit inside maxWidth x maxHeight while preserving
// aspect ratio. Unlike resize.Thumbnail, it also upscales images that are
// smaller than the target box, ensuring embedded EXIF thumbnails fill the
// requested thumbnail dimensions.
func thumbnail(maxWidth, maxHeight uint, img image.Image, interp resize.InterpolationFunction) image.Image {
	origBounds := img.Bounds()
	origWidth := uint(origBounds.Dx())
	origHeight := uint(origBounds.Dy())

	var newWidth, newHeight uint
	if maxWidth*origHeight <= maxHeight*origWidth {
		newWidth = maxWidth
		newHeight = origHeight * maxWidth / origWidth
	} else {
		newHeight = maxHeight
		newWidth = origWidth * maxHeight / origHeight
	}

	if newWidth < 1 {
		newWidth = 1
	}
	if newHeight < 1 {
		newHeight = 1
	}

	return resize.Resize(newWidth, newHeight, img, interp)
}

// GenerateThumbnailAndHashes creates a thumbnail for the image read from r.
// If the image (JPEG, TIFF, or WebP) contains an embedded EXIF thumbnail, that
// thumbnail is decoded instead of the full image, dramatically reducing memory
// use and CPU time. The source (embedded thumbnail or full image) is resized to
// fit within a 200x150 pixel box while maintaining aspect ratio and encoded as
// JPEG. It also calculates MD5 (over the full file bytes) and pHash (over the
// decoded source). It returns the JPEG data as a bytes.Buffer, sql.NullString
// for MD5, and sql.NullInt64 for pHash, or an error if generation fails.
func GenerateThumbnailAndHashes(r io.ReadSeeker) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	var srcImg image.Image

	// Try the embedded EXIF thumbnail first; fall back to full decode.
	embBuf := GetBytesBuffer()
	if err := extractEXIFThumbnail(r, embBuf); err == nil {
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

	// Generate thumbnail image
	thumbImg := thumbnail(200, 150, srcImg, resize.Lanczos3)
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

	// Compute pHash from the (possibly embedded-thumbnail) source image.
	// Squashing to 64x64 is the standard pHash normalization step.
	resized := resize.Resize(64, 64, srcImg, resize.Bilinear)
	phash := GetNullInt64()
	phash64, err := newPHash64Hook(resized)
	if err != nil {
		slog.Error("GenerateThumbnailAndHashes imagehash.NewPHash64", "err", err)
	}
	phash.Valid = true
	phash.Int64 = int64(phash64)

	return thumbnailBytesBuffer, md5, phash, nil
}
