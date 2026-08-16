package thumbnail

// This file is the end-to-end acceptance gate for the production full-image
// decode path. The committed small fixture testdata/thumbnail/no-exif-thumb.jpg
// and a 12MP synthetic from ensureLargeFixtures must survive the full
// GenerateThumbnailAndHashes path, producing a decodable gallery-thumbnail
// JPEG that fits the 200×150 box plus a valid MD5 NullString and a valid
// pHash NullInt64. MD5 is deliberately NOT compared against a stdlib decode:
// the plan does not treat MD5-vs-stdlib equality as a validation signal, and
// the thumb (and therefore the pHash, which is computed over the thumb)
// differs under the adaptive scaled decode.

import (
	"bytes"
	"path/filepath"
	"testing"

	jpegscaled "github.com/m8rge/go-scaled-jpeg"
)

// TestNoEXIFThumbnailJPEGProducesValidThumbnailMD5AndPHash verifies the
// end-to-end acceptance criteria for the production full-image decode: the
// no-EXIF-thumbnail fixture (small fixture and 12MP smoke) flows through the
// adaptive go-scaled-jpeg decode into a valid gallery thumbnail JPEG plus MD5
// and pHash.
func TestNoEXIFThumbnailJPEGProducesValidThumbnailMD5AndPHash(t *testing.T) {
	t.Run("committed small fixture", func(t *testing.T) {
		assertValidThumbnailAndHashes(t, filepath.Join(benchFixtureDir(t), "no-exif-thumb.jpg"), 800, 600)
	})

	t.Run("12MP synthetic smoke", func(t *testing.T) {
		_, path12mp, _ := ensureLargeFixtures(t)
		assertValidThumbnailAndHashes(t, path12mp, 4000, 3000)
	})
}

// assertValidThumbnailAndHashes runs the production acceptance assertions for
// one JPEG path with caller-supplied source dimensions srcW/srcH:
// GenerateThumbnailAndHashes must return a decodable JPEG thumbnail that fits the 200×150 gallery-thumb box, a valid
// non-empty MD5 NullString, and a valid non-zero pHash NullInt64. Success-path
// buffers are returned to their pools before the helper returns.
func assertValidThumbnailAndHashes(t *testing.T, path string, srcW, srcH int) {
	t.Helper()
	f := openBenchFile(t, path)
	thumb, md5, phash, err := GenerateThumbnailAndHashes(f, srcW, srcH)
	if err != nil {
		t.Fatalf("GenerateThumbnailAndHashes(%s): %v", path, err)
	}

	// The gallery thumb must be a decodable JPEG that fits the 200×150 box.
	// Output gallery-JPEG bytes are decoded at 1:1 via go-scaled-jpeg, the
	// production decoder; it is decode-only, so a successful 1:1 decode also
	// proves the bytes are a JPEG.
	if thumb == nil || thumb.Len() == 0 {
		t.Fatal("expected a non-empty thumbnail buffer")
	}
	img, err := jpegscaled.Decode(bytes.NewReader(thumb.Bytes()), jpegscaled.DecodeOptions{DCTSizeScaled: 8})
	if err != nil {
		t.Fatalf("decoding the generated thumbnail for %s: %v", path, err)
	}
	if b := img.Bounds(); b.Dx() < 1 || b.Dy() < 1 || b.Dx() > galleryThumbMaxW || b.Dy() > galleryThumbMaxH {
		t.Fatalf("thumbnail bounds %v for %s do not fit the 200×150 gallery-thumb box", b, path)
	}

	// MD5 must be a valid, non-empty NullString. No equality check against
	// a stdlib decode: that is not a validation signal.
	if md5 == nil || !md5.Valid || md5.String == "" {
		t.Fatalf("expected a valid, non-empty MD5 NullString for %s, got %+v", path, md5)
	}

	// pHash must be a valid, non-zero NullInt64.
	if phash == nil || !phash.Valid || phash.Int64 == 0 {
		t.Fatalf("expected a valid, non-zero pHash NullInt64 for %s, got %+v", path, phash)
	}

	benchPutResults(thumb, md5, phash)
}
