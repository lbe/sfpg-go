package thumbnail_test

// This test encodes the corrupt/truncated JPEG contract under always-on
// go-scaled-jpeg: a JPEG whose APP1 segment is truncated is corrupt, so the
// full-image decode (adaptive DCT scale via go-scaled-jpeg) fails cleanly
// with an error (no panic), and GenerateThumbnailAndHashes returns that
// error. There is no embedded-EXIF extract step and no rewind-and-fallback to
// stdlib image/jpeg.Decode.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// TestGenerateThumbnailAndHashesTruncatedAPP1HardFails verifies that a JPEG
// with a truncated APP1 segment makes GenerateThumbnailAndHashes fail with a
// clean error: the adaptive-DCT go-scaled-jpeg full-image decode rejects the
// corrupt stream without panicking.
func TestGenerateThumbnailAndHashesTruncatedAPP1HardFails(t *testing.T) {
	const filename = "truncated-app1.jpg"
	path := filepath.Join("..", "..", "testdata", "thumbnail", filename)
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("failed to open %s: %v", filename, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Logf("failed to close image file: %v", closeErr)
		}
	}()

	// A corrupt JPEG must never panic the decoder: assert a clean error
	// return even on malformed input.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic decoding truncated APP1 JPEG %s: %v", filename, r)
		}
	}()

	if _, _, _, err := thumbnail.GenerateThumbnailAndHashes(file, 800, 600); err == nil {
		t.Fatal("expected an error, got nil")
	}
}
