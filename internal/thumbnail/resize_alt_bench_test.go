package thumbnail

// Phase 2 resize-only alternative benches. Measurement only: does not change
// production defaults (draw.ApproxBiLinear / 200x150 / pHash ApproxBiLinear
// 64x64 from the gallery thumb) and does not pick a resize winner. The nfnt
// Lanczos3 variant is retained as a characterization baseline for historical
// comparison; gallery thumbs now use draw.ApproxBiLinear via
// defaultThumbResize.

import (
	"bytes"
	"image"
	"math"
	"os"
	"testing"

	"github.com/nfnt/resize"
	"golang.org/x/image/draw"
)

// thumbFitSize returns the width/height that fit src inside the 200x150 box,
// delegating to the shared production fit math (fitInsideBox) so bench
// geometry cannot drift from production.
func thumbFitSize(src image.Image) (w, h int) {
	return fitInsideBox(galleryThumbMaxW, galleryThumbMaxH, src)
}

// resizeAltNFNTLanczos3 is the nfnt Lanczos3 variant: characterization
// baseline for historical comparison. It no longer matches the production
// thumbnail resize (fit 200x150), which now uses draw.ApproxBiLinear.
func resizeAltNFNTLanczos3(img image.Image) image.Image {
	return thumbnail(galleryThumbMaxW, galleryThumbMaxH, img, resize.Lanczos3)
}

// resizeAltNFNTBilinear is the nfnt Bilinear variant: thumbnail resize (fit
// 200x150) with Bilinear interpolation.
func resizeAltNFNTBilinear(img image.Image) image.Image {
	return thumbnail(galleryThumbMaxW, galleryThumbMaxH, img, resize.Bilinear)
}

// resizeAltXDrawApproxBiLinear is the x/image/draw ApproxBiLinear variant. It
// delegates to the shared production helper resizeThumbApproxBiLinear so the
// bench measures exactly the production thumb resize (fit 200x150).
func resizeAltXDrawApproxBiLinear(img image.Image) image.Image {
	return resizeThumbApproxBiLinear(img)
}

// resizeAltXDrawCatmullRom is the x/image/draw CatmullRom variant: allocate a
// fit-size RGBA and scale the source into it.
func resizeAltXDrawCatmullRom(img image.Image) image.Image {
	w, h := thumbFitSize(img)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	return dst
}

// resizeAltByID maps the locked variant IDs (used as b.Run suffixes) to the
// resize implementations. Locked: reused by Tasks 2.3-2.5.
var resizeAltByID = map[string]func(image.Image) image.Image{
	"nfnt_lanczos3":         resizeAltNFNTLanczos3,
	"nfnt_bilinear":         resizeAltNFNTBilinear,
	"xdraw_approx_bilinear": resizeAltXDrawApproxBiLinear,
	"xdraw_catmullrom":      resizeAltXDrawCatmullRom,
}

// resizeAltIDs is the locked variant iteration order.
var resizeAltIDs = []string{
	"nfnt_lanczos3",
	"nfnt_bilinear",
	"xdraw_approx_bilinear",
	"xdraw_catmullrom",
}

// BenchmarkResizeAlt_Only isolates the thumb resize for each variant across
// the 2/12/25 MP fixtures. The source is decoded once per size outside the
// timed sub-benchmarks so only the resize itself is measured.
func BenchmarkResizeAlt_Only(b *testing.B) {
	path2mp, path12mp, path25mp := ensureLargeFixtures(b)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"2mp", path2mp},
		{"12mp", path12mp},
		{"25mp", path25mp},
	} {
		img := decodeBenchImage(b, tc.path) // outside timed sub-benches
		for _, id := range resizeAltIDs {
			fn := resizeAltByID[id]
			b.Run(tc.name+"/"+id, func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					sinkImage = fn(img)
				}
			})
		}
	}
}

// withThumbResize runs body with thumbResizeHook replaced by fn, restoring the
// previous hook via b.Cleanup. Used by Phase 2 benches to swap the 200x150
// resize step for a measurement variant; never changes production defaults.
func withThumbResize(b *testing.B, fn func(image.Image) image.Image, body func(b *testing.B)) {
	b.Helper()
	prev := thumbResizeHook
	thumbResizeHook = &fn
	b.Cleanup(func() { thumbResizeHook = prev })
	body(b)
}

// BenchmarkResizeAlt_Parallel_FullDecode_12mp runs the full
// GenerateThumbnailAndHashes path in parallel (b.RunParallel) over the 12 MP
// fixture with the thumb resize swapped per variant (withThumbResize). The
// source bytes are read once outside the timed sub-benchmark; each parallel
// iteration builds its own bytes.Reader over the shared read-only data, so no
// *os.File is shared across goroutines. Success-path pool returns go back via
// benchPutResults. Matches Phase 1 parallelFullGenerate (no sinkBuf sink).
func BenchmarkResizeAlt_Parallel_FullDecode_12mp(b *testing.B) {
	_, path12, _ := ensureLargeFixtures(b) // 2nd return value: 12mp path
	data, err := os.ReadFile(path12)       // outside timer; Fatal on error
	if err != nil {
		b.Fatalf("read %s: %v", path12, err)
	}
	for _, id := range resizeAltIDs {
		fn := resizeAltByID[id]
		b.Run(id, func(b *testing.B) {
			withThumbResize(b, fn, func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						buf, md5, phash, err := GenerateThumbnailAndHashes(bytes.NewReader(data), 4000, 3000)
						if err != nil {
							b.Fatal(err)
						}
						benchPutResults(buf, md5, phash)
						// Do not assign sinkBuf here (matches Phase 1 parallelFullGenerate).
					}
				})
			})
		})
	}
}

// TestResizeAlt_SampleMetrics logs per-variant output bounds and mean absolute
// error (MAE) of the RGB channels versus the nfnt Lanczos3 baseline on the 2 MP
// fixture, for human review of resize-alternative quality. Informational only:
// it fails only on decode/resize errors or on bounds mismatch versus the
// baseline fit size; the baseline variant must be bit-identical to itself (MAE
// 0). It never gates on MAE thresholds for non-baseline variants and never
// labels any variant better.
func TestResizeAlt_SampleMetrics(t *testing.T) {
	path2mp, _, _ := ensureLargeFixtures(t)
	img := decodeBenchImage(t, path2mp)

	baseline := resizeAltNFNTLanczos3(img)
	baselineBounds := baseline.Bounds()

	for _, id := range resizeAltIDs {
		out := resizeAltByID[id](img)
		outBounds := out.Bounds()
		if outBounds != baselineBounds {
			t.Fatalf("%s bounds %v != baseline %v", id, outBounds, baselineBounds)
		}
		var mae float64
		var n int64
		for y := outBounds.Min.Y; y < outBounds.Max.Y; y++ {
			for x := outBounds.Min.X; x < outBounds.Max.X; x++ {
				br, bg, bb, _ := baseline.At(x, y).RGBA()
				or, og, ob, _ := out.At(x, y).RGBA()
				mae += math.Abs(float64(int64(br) - int64(or)))
				mae += math.Abs(float64(int64(bg) - int64(og)))
				mae += math.Abs(float64(int64(bb) - int64(ob)))
				n += 3
			}
		}
		mae /= float64(n)
		if id == "nfnt_lanczos3" && mae != 0 {
			t.Fatalf("%s mae=%f != 0: baseline variant must match itself", id, mae)
		}
		t.Logf("%s bounds=%dx%d mae=%.4f", id, outBounds.Dx(), outBounds.Dy(), mae)
	}
}

// BenchmarkResizeAlt_Full_FullDecode runs the full GenerateThumbnailAndHashes
// path with the thumb resize swapped per variant (withThumbResize) across the
// 2/12/25 MP fixture matrix (synthetic full-path benches). Source bytes are
// read once per size outside the timed sub-benchmark loops; each iteration
// builds a fresh bytes.Reader over the shared read-only data. Success-path
// pool returns go back via benchPutResults.
func BenchmarkResizeAlt_Full_FullDecode(b *testing.B) {
	path2mp, path12mp, path25mp := ensureLargeFixtures(b)
	for _, tc := range []struct {
		name string
		path string
		w, h int
	}{
		{"2mp", path2mp, 1920, 1080},
		{"12mp", path12mp, 4000, 3000},
		{"25mp", path25mp, 5000, 5000},
	} {
		data, err := os.ReadFile(tc.path) // outside timer; Fatal on error
		if err != nil {
			b.Fatalf("read %s: %v", tc.path, err)
		}
		for _, id := range resizeAltIDs {
			fn := resizeAltByID[id]
			b.Run(tc.name+"/"+id, func(b *testing.B) {
				withThumbResize(b, fn, func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						buf, md5, phash, err := GenerateThumbnailAndHashes(bytes.NewReader(data), tc.w, tc.h)
						if err != nil {
							b.Fatal(err)
						}
						benchPutResults(buf, md5, phash)
						sinkBuf = buf
					}
				})
			})
		}
	}
}
