package thumbnail

// This file encodes the always-on production decode contract for the
// full-image source decode:
//
//   - JPEGs: sniff the JPEG magic with a buffered non-consuming peek BEFORE
//     the go-scaled-jpeg decoder is called, and pass that same buffered
//     reader into the decoder. The DCT scale is chosen adaptively from the
//     caller-supplied source dimensions (chooseJPEGDCTSize) so the decoded
//     JPEG is at least the 200×150 gallery-thumb fit size. On scaled-decode
//     failure the full-image decode returns the error as-is (hard fail) —
//     there is no rewind-and-fallback to image/jpeg.Decode. go-scaled-jpeg
//     does NOT RegisterFormat, so it can never be hijacked by a
//     registered-format dispatch.
//   - Non-JPEGs: use image.Decode without ever calling the scaled decoder.
//   - GenerateThumbnailAndHashes always decodes through fullImageDecodeHook;
//     there is no embedded-EXIF-thumbnail shortcut.

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestFullImageDecodeUsesScaledJPEGForJPEGsAndHardFails verifies the
// production full-image decode contract: the default hook is decodeFullImage;
// a JPEG input decodes through it at an adaptive DCT scale after the JPEG
// sniff buffered-peeks the reader; when the scaled decode returns an error
// the full-image decode hard-fails (no stdlib image/jpeg.Decode fallback);
// non-JPEG inputs bypass the scaled decoder; and even an EXIF-bearing JPEG
// such as exif-thumb.jpg invokes the full-image decode hook.
func TestFullImageDecodeUsesScaledJPEGForJPEGsAndHardFails(t *testing.T) {
	prodDecodeHook := fullImageDecodeHook
	prodScaledHook := decodeJPEGScaledHook
	t.Cleanup(func() {
		fullImageDecodeHook = prodDecodeHook
		decodeJPEGScaledHook = prodScaledHook
	})

	fixture := func(name string) string {
		return filepath.Join(benchFixtureDir(t), name)
	}

	t.Run("full-image decode default decodes JPEGs at the adaptive DCT scale", func(t *testing.T) {
		// no-exif-thumb.jpg is 800×600, so chooseJPEGDCTSize picks dct=2 and
		// the default decode must return exactly 200×150 (800*2/8 by 600*2/8),
		// never the weak "less than full size" 1/8 result. go-scaled-jpeg does
		// not RegisterFormat, so jpeg.DecodeConfig reports the true stdlib
		// source dimensions for the sanity check below.
		f := openBenchFile(t, fixture("no-exif-thumb.jpg"))
		cfg, err := jpeg.DecodeConfig(f)
		if err != nil {
			t.Fatalf("jpeg.DecodeConfig: %v", err)
		}
		if cfg.Width != 800 || cfg.Height != 600 {
			t.Fatalf("no-exif-thumb.jpg source dimensions %dx%d, want 800x600", cfg.Width, cfg.Height)
		}
		seekStart(t, f)
		img, format, err := fullImageDecodeHook(f, cfg.Width, cfg.Height)
		if err != nil {
			t.Fatalf("full-image decode hook: %v", err)
		}
		if img == nil {
			t.Fatal("full-image decode hook returned nil image")
		}
		if format != "jpeg" {
			t.Fatalf("full-image decode hook format %q, want jpeg", format)
		}
		if b := img.Bounds(); b.Dx() != galleryThumbMaxW || b.Dy() != galleryThumbMaxH {
			t.Fatalf("full-image decode hook decoded bounds %v of an %dx%d source; want exactly 200x150 (adaptive dct=2)", b, cfg.Width, cfg.Height)
		}
	})

	t.Run("jpeg no-exif-thumbnail input succeeds via the full-image decode", func(t *testing.T) {
		// GenerateThumbnailAndHashes always takes the full-image path, so
		// no-exif-thumb.jpg must succeed with the production default (real
		// go-scaled-jpeg decoder).
		f := openBenchFile(t, fixture("no-exif-thumb.jpg"))
		if _, _, _, err := GenerateThumbnailAndHashes(f, 800, 600); err != nil {
			t.Fatalf("GenerateThumbnailAndHashes on a JPEG with no EXIF thumbnail: %v", err)
		}
	})

	t.Run("jpeg sniff buffered-peeks before the scaled decoder is called", func(t *testing.T) {
		// The fake scaled decoder decodes at 1:1 and asserts it receives a
		// stream still positioned at the start (full 800×600 bounds). If the
		// JPEG sniff consumed bytes and the raw underlying reader was passed
		// on instead of the buffered reader, the decoder would fail on the
		// truncated stream. Production passes the adaptive dct (2 for
		// 800×600); the fake ignores it and decodes at 1:1 to prove the
		// stream position.
		prevScaled := decodeJPEGScaledHook
		decodeJPEGScaledHook = func(r io.Reader, dct int) (image.Image, error) {
			img, err := decodeJPEGScaled(r, 8)
			if err != nil {
				return nil, err
			}
			if b := img.Bounds(); b.Dx() != 800 || b.Dy() != 600 {
				t.Fatalf("scaled decoder received a mispositioned stream; decoded bounds %v, want full 800x600", b)
			}
			return img, nil
		}
		t.Cleanup(func() { decodeJPEGScaledHook = prevScaled })

		f := openBenchFile(t, fixture("no-exif-thumb.jpg"))
		seekStart(t, f)
		img, format, err := fullImageDecodeHook(f, 800, 600)
		if err != nil {
			t.Fatalf("full-image decode hook: %v", err)
		}
		if img == nil {
			t.Fatal("full-image decode hook returned nil image")
		}
		if format != "jpeg" {
			t.Fatalf("full-image decode hook format %q, want jpeg", format)
		}
	})

	t.Run("scaled decode failure hard-fails with no stdlib fallback", func(t *testing.T) {
		// The fake scaled decoder consumes the whole stream (as a real decoder
		// does) and then fails; the full-image decode must return that error
		// as-is. There is no rewind-and-fallback to image/jpeg.Decode.
		fakeErr := errors.New("scaled-jpeg decode failed (fake)")
		prevScaled := decodeJPEGScaledHook
		decodeJPEGScaledHook = func(r io.Reader, dct int) (image.Image, error) {
			_, _ = io.ReadAll(r)
			return nil, fakeErr
		}
		t.Cleanup(func() { decodeJPEGScaledHook = prevScaled })

		data, err := os.ReadFile(fixture("no-exif-thumb.jpg"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}

		img, format, err := fullImageDecodeHook(bytes.NewReader(data), 800, 600)
		if !errors.Is(err, fakeErr) {
			t.Fatalf("full-image decode hook error %v, want the scaled-decode error %q (no stdlib fallback)", err, fakeErr)
		}
		if img != nil {
			t.Fatal("full-image decode hook returned a non-nil image on scaled-decode failure")
		}
		if format != "" {
			t.Fatalf("full-image decode hook returned format %q on failure, want empty", format)
		}

		// End to end: with the scaled decoder still failing, the JPEG path
		// must hard-fail.
		if _, _, _, err := GenerateThumbnailAndHashes(bytes.NewReader(data), 800, 600); err == nil {
			t.Fatal("GenerateThumbnailAndHashes must return an error when the full-image scaled decode fails")
		}
	})

	t.Run("non-jpeg input bypasses the scaled decoder", func(t *testing.T) {
		prevScaled := decodeJPEGScaledHook
		decodeJPEGScaledHook = func(r io.Reader, dct int) (image.Image, error) {
			t.Fatal("scaled decoder must not be called for a non-JPEG input")
			return nil, nil
		}
		t.Cleanup(func() { decodeJPEGScaledHook = prevScaled })

		pngBytes := encodeTestPNG(t)
		img, format, err := fullImageDecodeHook(bytes.NewReader(pngBytes), 8, 8)
		if err != nil {
			t.Fatalf("full-image decode hook on a PNG: %v (non-JPEG must bypass the scaled decoder)", err)
		}
		if img == nil {
			t.Fatal("non-jpeg decode returned nil image")
		}
		if format != "png" {
			t.Fatalf("non-jpeg decode format %q, want png", format)
		}
		if _, _, _, err := GenerateThumbnailAndHashes(bytes.NewReader(pngBytes), 8, 8); err != nil {
			t.Fatalf("GenerateThumbnailAndHashes on a non-JPEG input: %v", err)
		}
	})

	t.Run("exif thumbnail path calls the full-image decode hook", func(t *testing.T) {
		// The embedded-EXIF shortcut is removed, so the full-image decode
		// hook must be invoked even for an EXIF-bearing JPEG.
		called := false
		prevFull := fullImageDecodeHook
		fullImageDecodeHook = func(r io.Reader, srcW, srcH int) (image.Image, string, error) {
			called = true
			return decodeFullImage(r, srcW, srcH)
		}
		t.Cleanup(func() { fullImageDecodeHook = prevFull })

		f := openBenchFile(t, fixture("exif-thumb.jpg"))
		if _, _, _, err := GenerateThumbnailAndHashes(f, 800, 600); err != nil {
			t.Fatalf("GenerateThumbnailAndHashes on a JPEG with an EXIF thumbnail: %v", err)
		}
		if !called {
			t.Fatal("full-image decode hook must be called for an EXIF-bearing JPEG")
		}
	})

	t.Run("EXIF-bearing and plain JPEGs both decode via the full-image path at the adaptive DCT scale", func(t *testing.T) {
		var scales []int
		prevScaled := decodeJPEGScaledHook
		decodeJPEGScaledHook = func(r io.Reader, dct int) (image.Image, error) {
			scales = append(scales, dct)
			return decodeJPEGScaled(r, dct)
		}
		t.Cleanup(func() { decodeJPEGScaledHook = prevScaled })

		// exif-thumb.jpg carries an embedded EXIF thumbnail, but the
		// full-image path is always used: only the adaptive scaled decode
		// runs. Both committed fixtures are 800×600, so dct must be 2.
		scales = nil
		f := openBenchFile(t, fixture("exif-thumb.jpg"))
		if _, _, _, err := GenerateThumbnailAndHashes(f, 800, 600); err != nil {
			t.Fatalf("GenerateThumbnailAndHashes on an EXIF-thumbnail JPEG: %v", err)
		}
		if len(scales) != 1 || scales[0] != 2 {
			t.Fatalf("exif-thumb.jpg decode scales %v, want [2] (adaptive dct=2 for 800x600)", scales)
		}

		// Plain JPEG: the same single adaptive decode.
		scales = nil
		f = openBenchFile(t, fixture("no-exif-thumb.jpg"))
		if _, _, _, err := GenerateThumbnailAndHashes(f, 800, 600); err != nil {
			t.Fatalf("GenerateThumbnailAndHashes on a plain JPEG: %v", err)
		}
		if len(scales) != 1 || scales[0] != 2 {
			t.Fatalf("no-exif-thumb.jpg decode scales %v, want [2] (adaptive dct=2 for 800x600)", scales)
		}
	})
}

// encodeTestPNG returns a small deterministic PNG in memory.
func encodeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := range 8 {
		for x := range 8 {
			img.Set(x, y, color.RGBA{R: uint8(x * 32), G: uint8(y * 32), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}
