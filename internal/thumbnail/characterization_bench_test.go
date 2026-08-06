package thumbnail

// Phase 0 + Phase 1 characterization benchmarks. Smoke bench lives here in
// package thumbnail (same package as production) so later phase benches can
// call unexported extractEXIFThumbnail / thumbnail helpers.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/nfnt/resize"
)

// BenchmarkGenerateThumbnailAndHashes is the Phase 0 smoke bench: full
// GenerateThumbnailAndHashes over the committed EXIF-miss fixture
// testdata/thumbnail/no-exif-thumb.jpg. It rewinds the reader each iteration
// and returns success-path buffers to their pools. On error it fails without
// returning anything to the pools (error paths hand back non-pooled
// &sql.NullString{} / &sql.NullInt64{} literals).
func BenchmarkGenerateThumbnailAndHashes(b *testing.B) {
	path := filepath.Join(benchFixtureDir(b), "no-exif-thumb.jpg")
	file := openBenchFile(b, path)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seekStart(b, file)
		buf, md5, phash, err := GenerateThumbnailAndHashes(file)
		if err != nil {
			b.Fatal(err)
		}
		benchPutResults(buf, md5, phash)
		sinkBuf = buf
	}
}

// BenchmarkFull_EXIFHit runs the full GenerateThumbnailAndHashes path over
// the committed EXIF-hit fixture testdata/thumbnail/exif-thumb.jpg (embedded
// EXIF thumbnail present). Same hygiene as the smoke bench: rewind each
// iteration, return success-path buffers to their pools, and fail without
// returning on error.
func BenchmarkFull_EXIFHit(b *testing.B) {
	path := filepath.Join(benchFixtureDir(b), "exif-thumb.jpg")
	file := openBenchFile(b, path)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seekStart(b, file)
		buf, md5, phash, err := GenerateThumbnailAndHashes(file)
		if err != nil {
			b.Fatal(err)
		}
		benchPutResults(buf, md5, phash)
		sinkBuf = buf
	}
}

// BenchmarkFull_EXIFMiss runs the full GenerateThumbnailAndHashes path over
// the committed EXIF-miss fixture testdata/thumbnail/no-exif-thumb.jpg. Same
// hygiene as the smoke bench.
func BenchmarkFull_EXIFMiss(b *testing.B) {
	path := filepath.Join(benchFixtureDir(b), "no-exif-thumb.jpg")
	file := openBenchFile(b, path)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seekStart(b, file)
		buf, md5, phash, err := GenerateThumbnailAndHashes(file)
		if err != nil {
			b.Fatal(err)
		}
		benchPutResults(buf, md5, phash)
		sinkBuf = buf
	}
}

// BenchmarkPhase_EXIFExtract isolates extractEXIFThumbnail (embedded EXIF
// thumbnail scan + copy) on both the EXIF-hit and EXIF-miss committed
// fixtures. extractEXIFThumbnail seeks internally, so no explicit rewind is
// needed between iterations.
func BenchmarkPhase_EXIFExtract(b *testing.B) {
	b.ReportAllocs()
	dir := benchFixtureDir(b)
	for _, tc := range []struct {
		name string
		file string
	}{
		{"hit", "exif-thumb.jpg"},
		{"miss", "no-exif-thumb.jpg"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			file := openBenchFile(b, filepath.Join(dir, tc.file))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf := GetBytesBuffer()
				sinkErr = extractEXIFThumbnail(file, buf)
				PutBytesBuffer(buf)
			}
		})
	}
}

// BenchmarkPhase_Decode isolates image.Decode of the EXIF-miss fixture after
// rewinding to the start each iteration. Phase 1 intentionally does not add an
// embedded-thumbnail decode sub-bench (out of scope).
func BenchmarkPhase_Decode(b *testing.B) {
	b.ReportAllocs()
	path := filepath.Join(benchFixtureDir(b), "no-exif-thumb.jpg")
	file := openBenchFile(b, path)
	for i := 0; i < b.N; i++ {
		seekStart(b, file)
		img, _, err := image.Decode(file)
		sinkImage = img
		sinkErr = err
	}
}

// BenchmarkPhase_ResizeThumb isolates the nfnt Lanczos3 thumb resize for
// historical/characterization comparison only: production gallery thumbs now
// use draw.ApproxBiLinear (defaultThumbResize). The source is decoded once
// before the timer so only the resize is measured.
func BenchmarkPhase_ResizeThumb(b *testing.B) {
	b.ReportAllocs()
	img := decodePhaseSource(b, "no-exif-thumb.jpg")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkImage = thumbnail(200, 150, img, resize.Lanczos3)
	}
}

// BenchmarkPhase_ResizePHash isolates the nfnt 64x64 Bilinear squashing for
// historical/characterization comparison only: production pHash now scales the
// gallery thumbnail to 64x64 with draw.ApproxBiLinear (acquirePHashRGBA).
// Source decoded once before the timer.
func BenchmarkPhase_ResizePHash(b *testing.B) {
	b.ReportAllocs()
	img := decodePhaseSource(b, "no-exif-thumb.jpg")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkImage = resize.Resize(64, 64, img, resize.Bilinear)
	}
}

// BenchmarkPhase_JPEGEncode encodes a pre-built ~200x150 thumbnail-sized
// image into a pooled bytes.Buffer. The image is built once before the timer.
func BenchmarkPhase_JPEGEncode(b *testing.B) {
	b.ReportAllocs()
	img := image.NewRGBA(image.Rect(0, 0, 200, 150))
	for y := 0; y < 150; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x + y), A: 255})
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := GetBytesBuffer()
		if err := jpegEncodeHook(buf, img, nil); err != nil {
			b.Fatal(err)
		}
		sinkBuf = buf
		PutBytesBuffer(buf)
	}
}

// BenchmarkPhase_MD5 isolates the seek + full-file MD5 copy over the EXIF-miss
// fixture, mirroring the hashing step inside GenerateThumbnailAndHashes.
func BenchmarkPhase_MD5(b *testing.B) {
	b.ReportAllocs()
	path := filepath.Join(benchFixtureDir(b), "no-exif-thumb.jpg")
	file := openBenchFile(b, path)
	for i := 0; i < b.N; i++ {
		seekStart(b, file)
		h := GetMD5()
		n, err := io.Copy(h, file)
		if err != nil {
			PutMD5(h)
			b.Fatal(err)
		}
		sinkN = n
		sinkString = fmt.Sprintf("%x", h.Sum(nil))
		PutMD5(h)
	}
}

// BenchmarkPhase_FullGenerate isolates the entire GenerateThumbnailAndHashes
// path on the EXIF-miss fixture with success-path pool returns.
func BenchmarkPhase_FullGenerate(b *testing.B) {
	path := filepath.Join(benchFixtureDir(b), "no-exif-thumb.jpg")
	file := openBenchFile(b, path)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seekStart(b, file)
		buf, md5, phash, err := GenerateThumbnailAndHashes(file)
		if err != nil {
			b.Fatal(err)
		}
		benchPutResults(buf, md5, phash)
		sinkBuf = buf
	}
}

// decodePhaseSource decodes a fixture once for reuse across iterations by the
// resize-only phase benches.
func decodePhaseSource(t testing.TB, name string) image.Image {
	t.Helper()
	path := filepath.Join(benchFixtureDir(t), name)
	return decodeBenchImage(t, path)
}

// decodeBenchImage decodes an arbitrary fixture path once for reuse across
// iterations by the resize-only benches. It is not timed by the caller.
func decodeBenchImage(t testing.TB, path string) image.Image {
	t.Helper()
	file := openBenchFile(t, path)
	img, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

// BenchmarkFull_Size runs the full GenerateThumbnailAndHashes path (EXIF-miss
// synthetic JPEGs, so no embedded thumbnail exists) across the cached
// 2/12/25 MP fixtures from ensureLargeFixtures. Fixture generation happens in
// the parent before the timed sub-benchmark loops, so it is never measured.
func BenchmarkFull_Size(b *testing.B) {
	path2mp, path12mp, path25mp := ensureLargeFixtures(b)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"2mp", path2mp},
		{"12mp", path12mp},
		{"25mp", path25mp},
	} {
		b.Run(tc.name, func(b *testing.B) {
			file := openBenchFile(b, tc.path)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seekStart(b, file)
				buf, md5, phash, err := GenerateThumbnailAndHashes(file)
				if err != nil {
					b.Fatal(err)
				}
				benchPutResults(buf, md5, phash)
				sinkBuf = buf
			}
		})
	}
}

// BenchmarkResize_Size isolates thumbnail(200, 150, img, resize.Lanczos3) for
// each size. It exists for historical/characterization comparison only:
// production gallery thumbs now use draw.ApproxBiLinear (defaultThumbResize).
// The source is decoded once per size outside the timed region (StopTimer
// around the decode) so only the Lanczos3 resize is measured.
func BenchmarkResize_Size(b *testing.B) {
	path2mp, path12mp, path25mp := ensureLargeFixtures(b)
	for _, tc := range []struct {
		name string
		path string
	}{
		{"2mp", path2mp},
		{"12mp", path12mp},
		{"25mp", path25mp},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.StopTimer()
			img := decodeBenchImage(b, tc.path)
			b.ReportAllocs()
			b.StartTimer()
			for i := 0; i < b.N; i++ {
				sinkImage = thumbnail(200, 150, img, resize.Lanczos3)
			}
		})
	}
}

// BenchmarkFull_Parallel_EXIFMiss_2mp models concurrent discovery workers
// sharing the CPU with nfnt/resize's inner GOMAXPROCS fan-out (resize.go:106
// and :351 each spawn GOMAXPROCS goroutines for the horizontal and vertical
// passes of every Resize call). It runs the full GenerateThumbnailAndHashes
// path in parallel over the 2 MP (1920x1080) EXIF-miss fixture.
func BenchmarkFull_Parallel_EXIFMiss_2mp(b *testing.B) {
	path2mp, _, _ := ensureLargeFixtures(b)
	parallelFullGenerate(b, path2mp)
}

// BenchmarkFull_Parallel_EXIFMiss_12mp is BenchmarkFull_Parallel_EXIFMiss_2mp
// for the 12 MP (4000x3000) EXIF-miss fixture.
func BenchmarkFull_Parallel_EXIFMiss_12mp(b *testing.B) {
	_, path12mp, _ := ensureLargeFixtures(b)
	parallelFullGenerate(b, path12mp)
}

// BenchmarkFull_Parallel_EXIFHit runs the full GenerateThumbnailAndHashes
// path in parallel over an in-memory copy of the committed EXIF-hit fixture
// testdata/thumbnail/exif-thumb.jpg (embedded EXIF thumbnail present).
func BenchmarkFull_Parallel_EXIFHit(b *testing.B) {
	path := filepath.Join(benchFixtureDir(b), "exif-thumb.jpg")
	parallelFullGenerate(b, path)
}

// withEXIFExtractDisabled runs fn with extractEXIFThumbnailHook forced to
// return errNoThumb, then restores the previous hook.
func withEXIFExtractDisabled(b *testing.B, fn func(b *testing.B)) {
	b.Helper()
	prev := extractEXIFThumbnailHook
	// errNoThumb is the existing sentinel in exifthumb.go — do not invent a new error.
	extractEXIFThumbnailHook = func(io.ReadSeeker, *bytes.Buffer) error { return errNoThumb }
	b.Cleanup(func() { extractEXIFThumbnailHook = prev })
	fn(b)
}

// BenchmarkFull_EXIFIgnored runs the full GenerateThumbnailAndHashes path over
// the committed EXIF-hit fixture testdata/thumbnail/exif-thumb.jpg with the
// embedded-EXIF-thumbnail shortcut forced off, so the bench measures full
// decode+resize of the EXIF-bearing source. Same hygiene as
// BenchmarkFull_EXIFHit (*os.File + seekStart each iteration, success-path
// benchPutResults).
func BenchmarkFull_EXIFIgnored(b *testing.B) {
	withEXIFExtractDisabled(b, func(b *testing.B) {
		path := filepath.Join(benchFixtureDir(b), "exif-thumb.jpg")
		file := openBenchFile(b, path)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			seekStart(b, file)
			buf, md5, phash, err := GenerateThumbnailAndHashes(file)
			if err != nil {
				b.Fatal(err)
			}
			benchPutResults(buf, md5, phash)
			sinkBuf = buf
		}
	})
}

// BenchmarkFull_Size_EXIFIgnored runs the full GenerateThumbnailAndHashes path
// across the cached 2/12/25 MP synthetic fixtures with the EXIF hook disabled.
// The synthetics carry no EXIF, but they run under withEXIFExtractDisabled so
// the suite is labeled consistently. ensureLargeFixtures runs once in the
// parent and the whole body is wrapped in a single withEXIFExtractDisabled (the
// hook is not re-installed per sub-benchmark); fixture generation happens
// before the timed sub-benchmark loops, so it is never measured.
func BenchmarkFull_Size_EXIFIgnored(b *testing.B) {
	path2mp, path12mp, path25mp := ensureLargeFixtures(b)
	withEXIFExtractDisabled(b, func(b *testing.B) {
		for _, tc := range []struct {
			name string
			path string
		}{
			{"2mp", path2mp},
			{"12mp", path12mp},
			{"25mp", path25mp},
		} {
			b.Run(tc.name, func(b *testing.B) {
				file := openBenchFile(b, tc.path)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					seekStart(b, file)
					buf, md5, phash, err := GenerateThumbnailAndHashes(file)
					if err != nil {
						b.Fatal(err)
					}
					benchPutResults(buf, md5, phash)
					sinkBuf = buf
				}
			})
		}
	})
}

// BenchmarkFull_Parallel_EXIFIgnored_12mp is BenchmarkFull_Parallel_EXIFMiss_12mp
// with the EXIF hook disabled: parallel full generate on 12 MP bytes via
// per-goroutine bytes.Reader over shared read-only data (no shared *os.File),
// modeling concurrent discovery workers without the embedded-thumbnail
// shortcut.
func BenchmarkFull_Parallel_EXIFIgnored_12mp(b *testing.B) {
	_, path12mp, _ := ensureLargeFixtures(b)
	withEXIFExtractDisabled(b, func(b *testing.B) {
		parallelFullGenerate(b, path12mp)
	})
}

// parallelFullGenerate runs the full GenerateThumbnailAndHashes path with
// b.RunParallel over the fixture at path. The file bytes are read into memory
// exactly once before the timer starts, and each parallel iteration builds its
// own bytes.Reader (an io.ReadSeeker) over the shared read-only data, so no
// *os.File is shared across goroutines. On success the pooled returns are put
// back via benchPutResults; on error the iteration fails without any Put* so
// the non-pooled &sql.NullString{} / &sql.NullInt64{} error literals never
// pollute the pools. This approximates concurrent discovery workers (file
// processor pool) sharing the CPU with nfnt's inner GOMAXPROCS fan-out.
func parallelFullGenerate(b *testing.B, path string) {
	b.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatalf("read %s: %v", path, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf, md5, phash, err := GenerateThumbnailAndHashes(bytes.NewReader(data))
			if err != nil {
				b.Fatal(err)
			}
			benchPutResults(buf, md5, phash)
		}
	})
}
