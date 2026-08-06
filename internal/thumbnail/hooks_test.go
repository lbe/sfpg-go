package thumbnail

import (
	"bytes"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestExtractEXIFThumbnailHook_SkipForcesFullDecode verifies that replacing
// extractEXIFThumbnailHook with an errNoThumb stub forces GenerateThumbnailAndHashes
// down the full-decode path while still succeeding. This is the mechanism
// characterization benches use to measure full-decode cost on EXIF-bearing files.
func TestExtractEXIFThumbnailHook_SkipForcesFullDecode(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "thumbnail", "exif-thumb.jpg")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	// Sanity: with the production default hook the embedded EXIF thumbnail is
	// used and generation succeeds.
	if _, _, _, err := GenerateThumbnailAndHashes(f); err != nil {
		t.Fatalf("default hook: GenerateThumbnailAndHashes: %v", err)
	}

	// Stub the hook to skip embedded-thumbnail extraction; restore on cleanup.
	extractEXIFThumbnailHook = func(io.ReadSeeker, *bytes.Buffer) error { return errNoThumb }
	t.Cleanup(func() { extractEXIFThumbnailHook = extractEXIFThumbnail })

	// The hook now reports no embedded thumbnail, so the full-decode path must
	// run and still produce a valid result.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if _, _, _, err := GenerateThumbnailAndHashes(f); err != nil {
		t.Fatalf("hook skipping EXIF: GenerateThumbnailAndHashes: %v", err)
	}
}

// TestThumbResizeHook_OverrideIsInvoked verifies that replacing thumbResizeHook
// with a stub forces GenerateThumbnailAndHashes to use the stub for the gallery
// thumbnail resize while still succeeding. This is the mechanism Phase 2
// characterization benches use to swap the 200x150 resize step.
func TestThumbResizeHook_OverrideIsInvoked(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "thumbnail", "no-exif-thumb.jpg")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	// Sanity: with the production default hook generation succeeds.
	if _, _, _, err := GenerateThumbnailAndHashes(f); err != nil {
		t.Fatalf("default hook: GenerateThumbnailAndHashes: %v", err)
	}

	// Stub the hook to record the call and return a fixed-size canvas;
	// restore the production default on cleanup.
	called := false
	stubFn := func(img image.Image) image.Image {
		called = true
		return image.NewRGBA(image.Rect(0, 0, 200, 150))
	}
	thumbResizeHook = &stubFn
	t.Cleanup(func() { thumbResizeHook = &defaultThumbResizeFn })

	// The stub must be invoked during generation and the result must succeed.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if _, _, _, err := GenerateThumbnailAndHashes(f); err != nil {
		t.Fatalf("stubbed hook: GenerateThumbnailAndHashes: %v", err)
	}
	if !called {
		t.Fatal("thumbResizeHook stub was not invoked")
	}
}

// TestFitInsideBoxKnownSizes verifies fitInsideBox geometry against fixed
// inputs covering the width-limited, height-limited, and already-fitting
// cases. The expected values are computed from the historical thumbnail() math
// (e.g. 10x200 → newWidth = 10*150/200 = 7), not by calling thumbnail().
func TestFitInsideBoxKnownSizes(t *testing.T) {
	tests := []struct {
		name         string
		maxW, maxH   uint
		img          image.Image
		wantW, wantH int
	}{
		{
			name: "small landscape fills box",
			maxW: 200, maxH: 150,
			img:   image.NewRGBA(image.Rect(0, 0, 40, 30)),
			wantW: 200, wantH: 150,
		},
		{
			name: "tall portrait is height-limited",
			maxW: 200, maxH: 150,
			img:   image.NewRGBA(image.Rect(0, 0, 10, 200)),
			wantW: 7, wantH: 150,
		},
		{
			name: "already fitting is unchanged",
			maxW: 200, maxH: 150,
			img:   image.NewRGBA(image.Rect(0, 0, 200, 100)),
			wantW: 200, wantH: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotW, gotH := fitInsideBox(tt.maxW, tt.maxH, tt.img)
			if gotW != tt.wantW || gotH != tt.wantH {
				t.Fatalf("fitInsideBox(%d, %d) = (%d, %d), want (%d, %d)", tt.maxW, tt.maxH, gotW, gotH, tt.wantW, tt.wantH)
			}
		})
	}
}

// TestResizeThumbApproxBiLinearBoundsAndType verifies that
// resizeThumbApproxBiLinear returns a non-nil *image.RGBA whose bounds match
// fitInsideBox(200, 150, img).
func TestResizeThumbApproxBiLinearBoundsAndType(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 200))

	got := resizeThumbApproxBiLinear(img)
	if got == nil {
		t.Fatal("resizeThumbApproxBiLinear returned nil")
	}
	if _, ok := got.(*image.RGBA); !ok {
		t.Fatalf("resizeThumbApproxBiLinear returned %T, want *image.RGBA", got)
	}

	wantW, wantH := fitInsideBox(200, 150, img)
	if b := got.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("resizeThumbApproxBiLinear bounds %v != fitInsideBox(200,150) = (%d, %d)", b, wantW, wantH)
	}
}

// TestDefaultThumbResizeUsesApproxBiLinear verifies that defaultThumbResize
// is exactly resizeThumbApproxBiLinear: equal bounds and pixel identity (every
// 16-bit RGBA component matches, i.e. MAE 0) on a small solid image.
func TestDefaultThumbResizeUsesApproxBiLinear(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		for y := 0; y < 30; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}

	got := defaultThumbResize(img)
	want := resizeThumbApproxBiLinear(img)

	gotBounds, wantBounds := got.Bounds(), want.Bounds()
	if gotBounds != wantBounds {
		t.Fatalf("defaultThumbResize bounds %v != resizeThumbApproxBiLinear bounds %v", gotBounds, wantBounds)
	}

	// Pixel identity: every 16-bit RGBA component must be identical (MAE 0).
	for y := gotBounds.Min.Y; y < gotBounds.Max.Y; y++ {
		for x := gotBounds.Min.X; x < gotBounds.Max.X; x++ {
			gr, gg, gb, ga := got.At(x, y).RGBA()
			wr, wg, wb, wa := want.At(x, y).RGBA()
			if gr != wr || gg != wg || gb != wb || ga != wa {
				t.Fatalf("pixel (%d, %d): got RGBA (%d,%d,%d,%d) != want (%d,%d,%d,%d)", x, y, gr, gg, gb, ga, wr, wg, wb, wa)
			}
		}
	}
}

// TestAcquireGalleryThumb_DefaultUsesPoolRelease verifies that with the default
// hook acquireGalleryThumb returns an *image.RGBA view whose bounds match
// fitInsideBox(200, 150, src), and that acquiring again after release reuses
// the pooled canvas without panicking.
func TestAcquireGalleryThumb_DefaultUsesPoolRelease(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		for y := 0; y < 30; y++ {
			src.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}

	wantW, wantH := fitInsideBox(200, 150, src)

	img1, release1 := acquireGalleryThumb(src)
	defer release1()
	if img1 == nil {
		t.Fatal("acquireGalleryThumb returned nil")
	}
	if _, ok := img1.(*image.RGBA); !ok {
		t.Fatalf("acquireGalleryThumb returned %T, want *image.RGBA", img1)
	}
	if b := img1.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("bounds %v != fitInsideBox(200,150) = (%d, %d)", b, wantW, wantH)
	}

	release1()

	// Second acquire after release must reuse the pooled canvas without panic.
	img2, release2 := acquireGalleryThumb(src)
	defer release2()
	if img2 == nil {
		t.Fatal("second acquireGalleryThumb returned nil")
	}
	if b := img2.Bounds(); b.Dx() != wantW || b.Dy() != wantH {
		t.Fatalf("second acquire bounds %v != fitInsideBox(200,150) = (%d, %d)", b, wantW, wantH)
	}
}

// TestAcquireGalleryThumb_HookBypassNoPoolContract verifies that replacing
// thumbResizeHook routes acquireGalleryThumb through the hook and that the
// returned release is a safe no-op: stub results never enter the pools.
func TestAcquireGalleryThumb_HookBypassNoPoolContract(t *testing.T) {
	stubFn := func(image.Image) image.Image {
		return image.NewRGBA(image.Rect(0, 0, 200, 150))
	}
	thumbResizeHook = &stubFn
	t.Cleanup(func() { thumbResizeHook = &defaultThumbResizeFn })

	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	img, release := acquireGalleryThumb(src)
	if img == nil {
		t.Fatal("acquireGalleryThumb returned nil")
	}
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 150 {
		t.Fatalf("bounds %v, want 200x150 stub canvas", b)
	}

	// The stub result is not pooled, so release must be safe to call (even
	// repeatedly) without putting the stub into a pool.
	release()
	release()
}

// TestAcquirePHashRGBA_SizeAndRelease verifies acquirePHashRGBA returns a
// 64×64 *image.RGBA scaled from the source, and that a second acquire after
// release succeeds (pool reuse) without panicking.
func TestAcquirePHashRGBA_SizeAndRelease(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	for x := 0; x < 40; x++ {
		for y := 0; y < 30; y++ {
			src.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}

	dst, release := acquirePHashRGBA(src)
	defer release()
	if dst == nil {
		t.Fatal("acquirePHashRGBA returned nil")
	}
	if b := dst.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("bounds %v, want 64x64", b)
	}

	release()

	// Second acquire after release must reuse the pooled canvas without panic.
	dst2, release2 := acquirePHashRGBA(src)
	defer release2()
	if b := dst2.Bounds(); b.Dx() != 64 || b.Dy() != 64 {
		t.Fatalf("second acquire bounds %v, want 64x64", b)
	}
}
