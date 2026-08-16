package thumbnail

// Characterization benches for the production JPEG decode paths: true stdlib
// image/jpeg.Decode vs the go-scaled-jpeg decoder (decodeJPEGScaled,
// DecodeOptions{DCTSizeScaled: 1}) on the 12 MP (4000x3000) fixture from
// ensureLargeFixtures, calling the decoder functions directly, plus the full
// GenerateThumbnailAndHashes path under the stdlib decode vs the production
// scaled-jpeg full-image decode on the same fixture.
//
// BenchmarkPhase_Decode (characterization_bench_test.go) uses bare
// image.Decode; go-scaled-jpeg does NOT RegisterFormat, so that bench measures
// true stdlib decode and stays a valid baseline. No arm of either bench here
// uses bare image.Decode: the decode-only arms call image/jpeg.Decode and the
// go-scaled-jpeg 1/8 decoder directly (never through the full-image hook), and
// the full-path arms go through GenerateThumbnailAndHashes with
// fullImageDecodeHook temporarily isolated per arm.

import (
	"image"
	"image/jpeg"
	"io"
	"testing"
)

// jpegDecoder decodes one JPEG from r. Both arms of
// BenchmarkPhase_Decode_12MP satisfy it: stdlib image/jpeg.Decode and the
// go-scaled-jpeg 1/8 decoder decodeJPEGScaled.
type jpegDecoder func(r io.Reader) (image.Image, error)

// BenchmarkPhase_Decode_12MP compares stdlib image/jpeg.Decode against the
// go-scaled-jpeg 1/8 decoder on the cached 12 MP (4000x3000) fixture. Each
// arm rewinds the file to the start every iteration. A warmup decode runs
// before b.ResetTimer() so any one-time cold start is excluded from the timed
// region; only steady-state ns/op and B/op are reported. The stdlib arm is a
// characterization baseline only — production full-image JPEG decode is
// go-scaled-jpeg at an adaptive DCT scale (1/8 for the 12MP fixture).
func BenchmarkPhase_Decode_12MP(b *testing.B) {
	_, path12mp, _ := ensureLargeFixtures(b)

	arms := []struct {
		name   string
		decode jpegDecoder
	}{
		{"stdlib-imagejpeg", jpeg.Decode},
		{"scaledjpeg-1-8", func(r io.Reader) (image.Image, error) { return decodeJPEGScaled(r, 1) }},
	}
	for _, arm := range arms {
		benchDecodeArm(b, arm.name, arm.decode, path12mp)
	}
}

// benchDecodeArm runs one decoder arm of BenchmarkPhase_Decode_12MP over the
// fixture at path. A warmup decode runs before b.ReportAllocs/b.ResetTimer so
// any first-call cold start is excluded from the timed region, then each
// timed iteration rewinds the file and decodes via decode.
func benchDecodeArm(b *testing.B, name string, decode jpegDecoder, path string) {
	b.Helper()
	b.Run(name, func(b *testing.B) {
		file := openBenchFile(b, path)

		decodeIteration(b, file, decode, name)

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			decodeIteration(b, file, decode, name)
		}
	})
}

// decodeIteration rewinds file to the start, decodes one JPEG via decode, and
// sinks the result so the compiler cannot eliminate the work. It fails the
// benchmark if the decode errors.
func decodeIteration(t testing.TB, file io.ReadSeeker, decode jpegDecoder, name string) {
	t.Helper()
	seekStart(t, file)
	img, err := decode(file)
	if err != nil {
		t.Fatalf("decode (%s): %v", name, err)
	}
	sinkImage = img
	sinkErr = err
}

// generateIteration rewinds file to the start, runs the full
// GenerateThumbnailAndHashes path, and sinks the thumbnail buffer so the
// compiler cannot eliminate the work. Success-path buffers are returned to
// their pools via benchPutResults; on error it fails without any Put* call so
// the non-pooled &sql.NullString{} / &sql.NullInt64{} error literals never
// pollute the pools.
func generateIteration(t testing.TB, file io.ReadSeeker, name string) {
	t.Helper()
	seekStart(t, file)
	buf, md5, phash, err := GenerateThumbnailAndHashes(file, 4000, 3000)
	if err != nil {
		t.Fatalf("generate (%s): %v", name, err)
	}
	benchPutResults(buf, md5, phash)
	sinkBuf = buf
}

// fullImageDecode is the fullImageDecodeHook signature: decode r to an image
// plus its format name, using the caller-supplied source dimensions srcW/srcH
// to pick the JPEG DCT scale. Both full-path arms of
// BenchmarkFull_NoEXIFThumbnail_12MP satisfy it.
type fullImageDecode func(r io.Reader, srcW, srcH int) (image.Image, string, error)

// stdlibJPEGFullDecode wraps stdlib image/jpeg.Decode in the
// fullImageDecodeHook signature. jpeg.Decode returns only (image.Image,
// error), so it cannot be assigned to the hook directly; the wrapper supplies
// the "jpeg" format name. The srcW/srcH dims are ignored (stdlib decodes at
// 1:1 always).
func stdlibJPEGFullDecode(r io.Reader, srcW, srcH int) (image.Image, string, error) {
	img, err := jpeg.Decode(r)
	return img, "jpeg", err
}

// BenchmarkFull_NoEXIFThumbnail_12MP runs the full GenerateThumbnailAndHashes
// path on the cached 12 MP (4000x3000) fixture from ensureLargeFixtures (a
// synthetic JPEG) under both full-image decode arms:
//
//   - stdlib-imagejpeg-full: fullImageDecodeHook is temporarily forced to a
//     thin wrapper around stdlib image/jpeg.Decode (stdlibJPEGFullDecode) so
//     the stdlib full path is measured as a characterization baseline, then
//     restored via b.Cleanup.
//   - scaledjpeg-adaptive: the production default hook decodeFullImage, which
//     decodes JPEGs at an adaptive DCT scale (1/8 for the 12MP fixture) via
//     the go-scaled-jpeg decoder.
//
// Each arm warms up once before b.ReportAllocs/b.ResetTimer, rewinds the
// fixture every iteration, and returns success-path buffers to their pools
// (benchPutResults), matching the characterization bench hygiene.
func BenchmarkFull_NoEXIFThumbnail_12MP(b *testing.B) {
	_, path12mp, _ := ensureLargeFixtures(b)

	arms := []struct {
		name string
		hook fullImageDecode
	}{
		{"stdlib-imagejpeg-full", stdlibJPEGFullDecode},
		{"scaledjpeg-adaptive", decodeFullImage},
	}
	for _, arm := range arms {
		b.Run(arm.name, func(b *testing.B) {
			prevHook := fullImageDecodeHook
			fullImageDecodeHook = arm.hook
			b.Cleanup(func() { fullImageDecodeHook = prevHook })

			file := openBenchFile(b, path12mp)

			// Warm up once before the timed region so any one-time cold start
			// is excluded from the reported ns/op and B/op.
			generateIteration(b, file, arm.name)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				generateIteration(b, file, arm.name)
			}
		})
	}
}
