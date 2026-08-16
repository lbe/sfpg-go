package thumbnail

// This file asserts that the full-image source decode is injectable via a
// package hook: replacing the hook is observed from GenerateThumbnailAndHashes
// directly (every generation decodes through it), and restoring the default
// recovers the production decodeFullImage behavior (go-scaled-jpeg at an
// adaptive DCT scale for JPEGs, stdlib image.Decode otherwise).

import (
	"image"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestFullImageDecodeHookCanBeReplacedAndRestored verifies the full-image
// decode hook contract: with the production default (decodeFullImage) a JPEG
// generates successfully; replacing the hook is observed by
// GenerateThumbnailAndHashes; and restoring the default recovers production
// behavior.
func TestFullImageDecodeHookCanBeReplacedAndRestored(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "thumbnail", "no-exif-thumb.jpg")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	// Sanity: with the production default hook (decodeFullImage) generation
	// succeeds.
	mustGenerate(t, f, "default hook")

	// Replace the full-image decode hook with a recorder wrapping the
	// production default; restore the production default on cleanup.
	decoded := false
	fullImageDecodeHook = func(r io.Reader, srcW, srcH int) (image.Image, string, error) {
		decoded = true
		return decodeFullImage(r, srcW, srcH)
	}
	t.Cleanup(func() { fullImageDecodeHook = decodeFullImage })

	// The replacement must be invoked by GenerateThumbnailAndHashes, and
	// generation must still succeed.
	mustGenerate(t, f, "stubbed hook")
	if !decoded {
		t.Fatal("full-image decode hook stub was not invoked")
	}

	// Restoring the default must recover production behavior: the hook points
	// at decodeFullImage again and generation still succeeds.
	fullImageDecodeHook = decodeFullImage
	mustGenerate(t, f, "restored default hook")
}

// mustGenerate seeks r back to the start and asserts that
// GenerateThumbnailAndHashes completes without error.
func mustGenerate(t *testing.T, r io.ReadSeeker, msg string) {
	t.Helper()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("%s: seek: %v", msg, err)
	}
	if _, _, _, err := GenerateThumbnailAndHashes(r, 800, 600); err != nil {
		t.Fatalf("%s: GenerateThumbnailAndHashes: %v", msg, err)
	}
}
