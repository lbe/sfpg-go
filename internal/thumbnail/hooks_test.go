package thumbnail

import (
	"bytes"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanoberholster/imagemeta/imagehash"
)

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
	if _, _, _, err := GenerateThumbnailAndHashes(f, 800, 600); err != nil {
		t.Fatalf("default hook: GenerateThumbnailAndHashes: %v", err)
	}

	// Stub the hook to record the call and return a fixed-size canvas;
	// restore the production default on cleanup.
	called := false
	stubFn := func(img image.Image) image.Image {
		called = true
		return image.NewRGBA(image.Rect(0, 0, galleryThumbMaxW, galleryThumbMaxH))
	}
	thumbResizeHook = &stubFn
	t.Cleanup(func() { thumbResizeHook = &defaultThumbResizeFn })

	// The stub must be invoked during generation and the result must succeed.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	if _, _, _, err := GenerateThumbnailAndHashes(f, 800, 600); err != nil {
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
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH,
			img:   image.NewRGBA(image.Rect(0, 0, 40, 30)),
			wantW: galleryThumbMaxW, wantH: galleryThumbMaxH,
		},
		{
			name: "tall portrait is height-limited",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH,
			img:   image.NewRGBA(image.Rect(0, 0, 10, 200)),
			wantW: 7, wantH: galleryThumbMaxH,
		},
		{
			name: "already fitting is unchanged",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH,
			img:   image.NewRGBA(image.Rect(0, 0, 200, 100)),
			wantW: galleryThumbMaxW, wantH: 100,
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

// TestFitInsideBoxDims verifies fitInsideBoxDims against the same geometry as
// fitInsideBox, using explicit source dimensions instead of an image.Image.
func TestFitInsideBoxDims(t *testing.T) {
	tests := []struct {
		name         string
		maxW, maxH   int
		srcW, srcH   int
		wantW, wantH int
	}{
		{
			name: "small landscape fills box",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH, srcW: 40, srcH: 30,
			wantW: galleryThumbMaxW, wantH: galleryThumbMaxH,
		},
		{
			name: "tall portrait is height-limited",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH, srcW: 10, srcH: 200,
			wantW: 7, wantH: galleryThumbMaxH,
		},
		{
			name: "already fitting is unchanged",
			maxW: galleryThumbMaxW, maxH: galleryThumbMaxH, srcW: 200, srcH: 100,
			wantW: galleryThumbMaxW, wantH: 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotW, gotH := fitInsideBoxDims(tt.maxW, tt.maxH, tt.srcW, tt.srcH)
			if gotW != tt.wantW || gotH != tt.wantH {
				t.Fatalf("fitInsideBoxDims(%d, %d, %d, %d) = (%d, %d), want (%d, %d)", tt.maxW, tt.maxH, tt.srcW, tt.srcH, gotW, gotH, tt.wantW, tt.wantH)
			}
		})
	}
}

// TestChooseJPEGDCTSize verifies the adaptive DCT-scale picker: the chosen
// dct decodes the srcW×srcH JPEG to at least the 200×150 gallery-thumb fit
// size (ceil(need*8/src) clamped to [1,8]).
func TestChooseJPEGDCTSize(t *testing.T) {
	tests := []struct {
		name    string
		srcW    int
		srcH    int
		wantDCT int
	}{
		{name: "12MP stays at 1/8", srcW: 4000, srcH: 3000, wantDCT: 1},
		{name: "800x600 decodes at 1/4", srcW: 800, srcH: 600, wantDCT: 2},
		{name: "400x300 decodes at 1/2", srcW: 400, srcH: 300, wantDCT: 4},
		{name: "320x240 decodes near 1:1", srcW: 320, srcH: 240, wantDCT: 5},
		{name: "zero width guards to 1:1", srcW: 0, srcH: 600, wantDCT: 8},
		{name: "zero height guards to 1:1", srcW: 800, srcH: 0, wantDCT: 8},
		{name: "negative dims guard to 1:1", srcW: -10, srcH: -20, wantDCT: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chooseJPEGDCTSize(tt.srcW, tt.srcH); got != tt.wantDCT {
				t.Fatalf("chooseJPEGDCTSize(%d, %d) = %d, want %d", tt.srcW, tt.srcH, got, tt.wantDCT)
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

	wantW, wantH := fitInsideBox(galleryThumbMaxW, galleryThumbMaxH, img)
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

	wantW, wantH := fitInsideBox(galleryThumbMaxW, galleryThumbMaxH, src)

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
		return image.NewRGBA(image.Rect(0, 0, galleryThumbMaxW, galleryThumbMaxH))
	}
	thumbResizeHook = &stubFn
	t.Cleanup(func() { thumbResizeHook = &defaultThumbResizeFn })

	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	img, release := acquireGalleryThumb(src)
	if img == nil {
		t.Fatal("acquireGalleryThumb returned nil")
	}
	if b := img.Bounds(); b.Dx() != galleryThumbMaxW || b.Dy() != galleryThumbMaxH {
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

// imageFromColor creates an w×h solid-color RGBA image for seek/hook error
// tests.
func imageFromColor(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

// encodeJPEGBytes encodes img as JPEG bytes for seek/hook error tests.
func encodeJPEGBytes(img image.Image) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// failingSeeker wraps an io.ReadSeeker and returns errSeekFail after the
// specified number of successful Seek calls.
type failingSeeker struct {
	inner       io.ReadSeeker
	allowed     int
	calls       int
	errSeekFail error
}

func (f *failingSeeker) Read(p []byte) (int, error) {
	return f.inner.Read(p)
}

func (f *failingSeeker) Seek(offset int64, whence int) (int64, error) {
	f.calls++
	if f.calls > f.allowed {
		return 0, f.errSeekFail
	}
	return f.inner.Seek(offset, whence)
}

// TestGenerateThumbnailAndHashesSeekErrors verifies the single decode path:
// the success path performs exactly two seeks — the rewind before the
// full-image decode, then the rewind before the MD5 read. With allowed=0 the
// first (decode) seek fails; with allowed=1 the decode seek succeeds and the
// second (MD5) seek fails. There is no extract step and no fallback decode.
func TestGenerateThumbnailAndHashesSeekErrors(t *testing.T) {
	img := imageFromColor(800, 600, color.RGBA{R: 0, G: 0, B: 255, A: 255})
	data := encodeJPEGBytes(img)

	// The first seek is the rewind before the full-image decode; with
	// allowed=0 that seek itself fails.
	fs := &failingSeeker{inner: bytes.NewReader(data), allowed: 0, errSeekFail: errors.New("seek failed")}
	if _, _, _, err := GenerateThumbnailAndHashes(fs, 800, 600); err == nil {
		t.Fatal("expected error when the decode seek fails")
	}

	// Allow the first seek (decode rewind) but fail the second seek before
	// the MD5 read.
	fs = &failingSeeker{inner: bytes.NewReader(data), allowed: 1, errSeekFail: errors.New("seek failed")}
	if _, _, _, err := GenerateThumbnailAndHashes(fs, 800, 600); err == nil {
		t.Fatal("expected error when MD5 seek fails")
	}
}

// TestGenerateThumbnailAndHashes_HookedErrors verifies error propagation from
// the jpegEncodeHook, ioCopyHook (MD5 read), and newPHash64Hook along the
// single decode path.
func TestGenerateThumbnailAndHashes_HookedErrors(t *testing.T) {
	img := imageFromColor(400, 300, color.RGBA{R: 128, G: 64, B: 32, A: 255})
	data := encodeJPEGBytes(img)

	cases := []struct {
		name      string
		setup     func()
		cleanup   func()
		wantErr   bool
		checkFunc func(t *testing.T, thumb *bytes.Buffer, md5 *sql.NullString, phash *sql.NullInt64)
	}{
		{
			name: "jpeg_encode_fails",
			setup: func() {
				jpegEncodeHook = func(io.Writer, image.Image, *jpeg.Options) error {
					return errors.New("jpeg encode failed")
				}
			},
			cleanup: func() { jpegEncodeHook = jpeg.Encode },
			wantErr: true,
		},
		{
			name: "md5_copy_fails",
			setup: func() {
				ioCopyHook = func(io.Writer, io.Reader) (int64, error) {
					return 0, errors.New("md5 copy failed")
				}
			},
			cleanup: func() { ioCopyHook = io.Copy },
			wantErr: true,
		},
		{
			name: "phash_fails",
			setup: func() {
				newPHash64Hook = func(image.Image) (imagehash.PHash64, error) { return 0, errors.New("phash failed") }
			},
			cleanup: func() { newPHash64Hook = imagehash.NewPHash64 },
			wantErr: false,
			checkFunc: func(t *testing.T, thumb *bytes.Buffer, md5 *sql.NullString, phash *sql.NullInt64) {
				if thumb == nil || thumb.Len() == 0 {
					t.Fatal("expected non-empty thumbnail")
				}
				if !md5.Valid || md5.String == "" {
					t.Error("expected valid md5")
				}
				if !phash.Valid || phash.Int64 != 0 {
					t.Errorf("expected phash Int64 == 0, got %d", phash.Int64)
				}
				PutBytesBuffer(thumb)
				PutNullString(md5)
				PutNullInt64(phash)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			defer tc.cleanup()

			thumb, md5, phash, err := GenerateThumbnailAndHashes(bytes.NewReader(data), 400, 300)
			if (err != nil) != tc.wantErr {
				t.Fatalf("expected error=%v, got err=%v", tc.wantErr, err)
			}
			if tc.checkFunc != nil {
				tc.checkFunc(t, thumb, md5, phash)
			} else if thumb != nil {
				PutBytesBuffer(thumb)
			}
		})
	}
}
