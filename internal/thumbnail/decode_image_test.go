package thumbnail

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// thumbnailFixture returns the bytes of a committed fixture in testdata/thumbnail.
func thumbnailFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "thumbnail", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

// TestDecodeImageJPEGUsesChosenDCTScale verifies that a JPEG decodes at the
// scale chooseJPEGDCTSize picks for its source dimensions: the 800×600
// fixture decodes at 1/4 to 200×150, the exact gallery box.
func TestDecodeImageJPEGUsesChosenDCTScale(t *testing.T) {
	data := thumbnailFixture(t, "no-exif-thumb.jpg")
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("jpeg.DecodeConfig: %v", err)
	}
	if cfg.Width != 800 || cfg.Height != 600 {
		t.Fatalf("no-exif-thumb.jpg source dimensions %dx%d, want 800x600", cfg.Width, cfg.Height)
	}

	img, err := decodeImage(data, true, cfg.Width, cfg.Height)
	if err != nil {
		t.Fatalf("decodeImage: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 200 || got.Dy() != 150 {
		t.Fatalf("decoded bounds %v, want 200x150 (adaptive dct=2)", got)
	}
}

// TestDecodeImageSmallJPEGDecodesAtFullSize verifies that a JPEG no larger
// than the gallery box decodes at full size, so it is never enlarged.
func TestDecodeImageSmallJPEGDecodesAtFullSize(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 75))
	for i := range img.Pix {
		img.Pix[i] = uint8(i * 7)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}

	got, err := decodeImage(buf.Bytes(), true, 100, 75)
	if err != nil {
		t.Fatalf("decodeImage: %v", err)
	}
	if b := got.Bounds(); b.Dx() != 100 || b.Dy() != 75 {
		t.Fatalf("decoded bounds %v, want 100x75 (full size)", b)
	}
}

// TestDecodeImageNonJPEGUsesRegisteredDecoder verifies that a PNG is decoded
// by image.Decode at its full size.
func TestDecodeImageNonJPEGUsesRegisteredDecoder(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	if !bytes.Equal(buf.Bytes()[:4], []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("encoded PNG lacks the PNG signature")
	}

	got, err := decodeImage(buf.Bytes(), false, 8, 8)
	if err != nil {
		t.Fatalf("decodeImage(PNG): %v", err)
	}
	if b := got.Bounds(); b.Dx() != 8 || b.Dy() != 8 {
		t.Fatalf("decoded bounds %v, want 8x8", b)
	}
	if _, _, _, a := got.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("decoded pixel alpha %d, want 0 (the source is transparent)", a)
	}
}

// TestDecodeImageTruncatedJPEGFails verifies that a JPEG whose APP1 segment is
// truncated is rejected with an error. There is no fallback decoder.
func TestDecodeImageTruncatedJPEGFails(t *testing.T) {
	data := thumbnailFixture(t, "truncated-app1.jpg")
	if _, err := decodeImage(data, true, 800, 600); err == nil {
		t.Fatal("decodeImage on a truncated APP1 JPEG returned nil error")
	}
}
