package thumbnail

// Helpers and shared sinks for characterization benches. Not used by production.
//
// Large synthetic fixtures (1920x1080 / 4000x3000 / 5000x5000) are generated
// on first use and cached on disk under <repo>/tmp/thumbnail_bench_fixtures/
// (gitignored via tmp*/*); they are never committed as binary files.

import (
	"bytes"
	"database/sql"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// Shared sinks keep bench results observable and prevent the compiler from
// eliminating work. Tasks 1.1-1.4 reuse these; do not redefine them elsewhere.
var (
	sinkImage  image.Image
	sinkBuf    *bytes.Buffer
	sinkN      int64
	sinkErr    error
	sinkString string
)

// benchModuleRoot returns the absolute module root, found by walking up from
// the working directory until go.mod is present.
func benchModuleRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	t.Fatalf("could not locate go.mod from %s", wd)
	return ""
}

// benchFixtureDir returns the absolute path to testdata/thumbnail, resolving
// relative to the module root so it works under `go test ./internal/thumbnail`.
func benchFixtureDir(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(benchModuleRoot(t), "testdata", "thumbnail")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("bench fixture dir %s: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("bench fixture path %s is not a directory", dir)
	}
	return dir
}

// openBenchFile opens path for the duration of the test/benchmark and
// registers close via t.Cleanup.
func openBenchFile(t testing.TB, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// seekStart rewinds r to the beginning or fails the test/benchmark.
func seekStart(t testing.TB, r io.Seeker) {
	t.Helper()
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
}

// ensureBenchJPEG returns the path to a w x h JPEG in dir, creating it on
// first use via image.NewRGBA + jpeg.Encode with a deterministic x/y mod fill.
// Existing non-empty files are reused.
func ensureBenchJPEG(t testing.TB, dir string, name string, w, h int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return path
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8((x + y) % 256), A: 255})
		}
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		_ = f.Close()
		t.Fatalf("jpeg encode %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
	return path
}

// ensureLargeFixtures returns paths to cached 1920x1080, 4000x3000, and
// 5000x5000 synthetic JPEGs under <repo>/tmp/thumbnail_bench_fixtures/,
// generating them once on first use.
func ensureLargeFixtures(t testing.TB) (path2mp, path12mp, path25mp string) {
	t.Helper()
	dir := filepath.Join(benchModuleRoot(t), "tmp", "thumbnail_bench_fixtures")
	path2mp = ensureBenchJPEG(t, dir, "1920x1080.jpg", 1920, 1080)
	path12mp = ensureBenchJPEG(t, dir, "4000x3000.jpg", 4000, 3000)
	path25mp = ensureBenchJPEG(t, dir, "5000x5000.jpg", 5000, 5000)
	return path2mp, path12mp, path25mp
}

// benchPutResults returns success-path buffers to their pools. Error returns
// from GenerateThumbnailAndHashes must NOT be passed here: they use non-pooled
// &sql.NullString{} / &sql.NullInt64{} literals.
func benchPutResults(buf *bytes.Buffer, md5 *sql.NullString, phash *sql.NullInt64) {
	PutBytesBuffer(buf)
	PutNullString(md5)
	PutNullInt64(phash)
}
