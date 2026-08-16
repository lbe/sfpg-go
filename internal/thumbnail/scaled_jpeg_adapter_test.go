package thumbnail

// This file encodes the acceptance criterion that decodeJPEGScaled decodes a
// JPEG at dctSizeScaled/8 of the source resolution via go-scaled-jpeg
// DecodeOptions{DCTSizeScaled: dctSizeScaled}: DCTSizeScaled 1 is 1/8 and
// DCTSizeScaled 8 is 1:1. These tests exercise the low-level decoder directly
// at dct=1 and dct=8; production source decode does not fix a scale —
// decodeFullImage picks the DCT scale adaptively via chooseJPEGDCTSize.
// go-scaled-jpeg does NOT RegisterFormat, so no registered-format dispatch
// concerns apply; source dimensions are taken from image/jpeg.DecodeConfig
// regardless.

import (
	"image/jpeg"
	"path/filepath"
	"testing"
)

// TestDecodeJPEGScaledAtOneEighth verifies the low-level decoder at
// DCTSizeScaled 1 (1/8 of the source resolution), a characterization baseline:
// production source decode picks the DCT scale adaptively (decodeFullImage →
// chooseJPEGDCTSize) and only uses dct=1 for sources large enough that 1/8
// still covers the 200×150 fit box. The 12MP synthetic fixture (4000×3000)
// must decode to ≈500×375 and the committed no-EXIF-thumbnail fixture
// (800×600) to ≈100×75.
func TestDecodeJPEGScaledAtOneEighth(t *testing.T) {
	_, path12mp, _ := ensureLargeFixtures(t)
	smallPath := filepath.Join(benchFixtureDir(t), "no-exif-thumb.jpg")

	for _, tc := range []struct {
		name string
		path string
	}{
		{"12MP fixture decodes to ≈500×375 (source/8)", path12mp},
		{"small no-EXIF-thumbnail fixture decodes to ≈100×75 (source/8)", smallPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := openBenchFile(t, tc.path)

			// image/jpeg.DecodeConfig reports the true source dimensions
			// independently of any registered-format dispatch.
			cfg, err := jpeg.DecodeConfig(f)
			if err != nil {
				t.Fatalf("jpeg.DecodeConfig(%s): %v", tc.path, err)
			}
			seekStart(t, f)

			img, err := decodeJPEGScaled(f, 1)
			if err != nil {
				t.Fatalf("decodeJPEGScaled(%s, 1): %v", tc.path, err)
			}
			if img == nil {
				t.Fatalf("decodeJPEGScaled(%s, 1) returned a nil image", tc.path)
			}

			if wantW, wantH := cfg.Width/8, cfg.Height/8; img.Bounds().Dx() != wantW || img.Bounds().Dy() != wantH {
				t.Fatalf("scaled bounds %v for %s are not ≈ source/8 (%dx%d)", img.Bounds(), tc.path, wantW, wantH)
			}
		})
	}
}

// TestDecodeJPEGScaledAtFullSize verifies the low-level DCTSizeScaled 8 (1:1)
// decoder API: DCTSizeScaled 8 decodes a JPEG at full source resolution.
// Production source decode picks the DCT scale adaptively (decodeFullImage →
// chooseJPEGDCTSize); tests use dct=8 to validate generated output thumbnail
// JPEG bytes at 1:1. The committed no-EXIF-thumbnail fixture (800×600) must
// decode to exactly 800×600.
func TestDecodeJPEGScaledAtFullSize(t *testing.T) {
	path := filepath.Join(benchFixtureDir(t), "no-exif-thumb.jpg")
	f := openBenchFile(t, path)

	cfg, err := jpeg.DecodeConfig(f)
	if err != nil {
		t.Fatalf("jpeg.DecodeConfig(%s): %v", path, err)
	}
	seekStart(t, f)

	img, err := decodeJPEGScaled(f, 8)
	if err != nil {
		t.Fatalf("decodeJPEGScaled(%s, 8): %v", path, err)
	}
	if img == nil {
		t.Fatalf("decodeJPEGScaled(%s, 8) returned a nil image", path)
	}

	if got := img.Bounds(); got.Dx() != cfg.Width || got.Dy() != cfg.Height {
		t.Fatalf("full-size bounds %v for %s, want %dx%d (source/1)", got, path, cfg.Width, cfg.Height)
	}
}
