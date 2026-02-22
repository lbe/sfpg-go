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
	"os"

	"github.com/evanoberholster/imagemeta/imagehash"
	"github.com/nfnt/resize"
	"go.local/sfpg/internal/gensyncpool"
	_ "golang.org/x/image/webp"
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

// imagePhash64Pool is a gensyncpool-backed pool for *imagehash.PHash64.
var imagePhash64Pool = gensyncpool.New(
	func() *imagehash.PHash64 { var p imagehash.PHash64; return &p },
	func(p *imagehash.PHash64) { *p = 0 },
)

// GetImagePhash64 retrieves an imagehash.PHash64 from the pool.
func GetImagePhash64() *imagehash.PHash64 { return imagePhash64Pool.Get() }

// PutImagePhash64 returns an imagehash.PHash64 to the pool, resetting it first.
func PutImagePhash64(phash64 *imagehash.PHash64) { imagePhash64Pool.Put(phash64) }

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

// GenerateThumbnailAndHashes creates a thumbnail for the image at the given file.
// It resizes the image to fit within a 200x150 pixel box while maintaining aspect ratio,
// and encodes the resulting thumbnail as a JPEG. It also calculates MD5 and pHash.
// It returns the JPEG data as a bytes.Buffer, and sql.NullString for MD5, and
// sql.NullInt64 for pHash, or an error if generation fails.
func GenerateThumbnailAndHashes(file *os.File) (*bytes.Buffer, *sql.NullString, *sql.NullInt64, error) {
	var err error

	// Decode source image
	srcImg, _, err := image.Decode(file)
	if err != nil {
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}

	// Generate thumbnail image
	thumbImg := resize.Thumbnail(200, 150, srcImg, resize.Lanczos3)
	if thumbImg == nil {
		return nil, &sql.NullString{}, &sql.NullInt64{}, errors.New("resize.Thumbnail returned nil image")
	}

	thumbnailBytesBuffer := GetBytesBuffer()
	err = jpeg.Encode(thumbnailBytesBuffer, thumbImg, nil)
	if err != nil {
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}

	// Reset f read pointer to beginning of f
	if _, seekErr := file.Seek(0, 0); seekErr != nil {
		slog.Error("processFile seek", "err", err)
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}

	// Calculate MD5 hash
	md5Hasher := GetMD5()
	defer PutMD5(md5Hasher)
	if _, copyErr := io.Copy(md5Hasher, file); copyErr != nil {
		slog.Error("processFile", "err", err)
		return nil, &sql.NullString{}, &sql.NullInt64{}, err
	}

	md5 := GetNullString()
	md5.Valid = true
	md5.String = fmt.Sprintf("%x", md5Hasher.Sum(nil))

	// Decode image and compute phash
	resized := resize.Resize(64, 64, srcImg, resize.Bilinear)
	phash := GetNullInt64()
	phash64, err := imagehash.NewPHash64(resized)
	if err != nil {
		slog.Error("getThumbnailAndPhash imagehash.NewPHash64", "err", err)
	}
	phash.Valid = true
	phash.Int64 = int64(phash64)

	return thumbnailBytesBuffer, md5, phash, nil
}
