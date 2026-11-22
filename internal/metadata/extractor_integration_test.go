//go:build integration

package metadata

import (
	"context"
	"errors"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	exiftool "github.com/lbe/go-exiftool-wasm"
)

// fixture is a licensed sample with EXIF, IPTC-IIM and XMP whose caption,
// creator and copyright differ between the blocks. testdata has ExifTool's
// own listing of it.
const fixture = "../../testdata/Metadata_test_file_-_includes_data_in_IIM,_XMP,_and_Exif.jpg"

// newJPEG writes a small JPEG into a temporary directory and returns its path.
func newJPEG(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = jpeg.Encode(f, image.NewGray(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeTags sets tags on the file at path with ExifTool, for example
// "-EXIF:Make=Canon".
func writeTags(t *testing.T, path string, tags ...string) {
	t.Helper()
	s, err := exiftool.NewServer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown()
	if _, err = s.Command(append(append([]string{"-overwrite_original"}, tags...), path)...); err != nil {
		t.Fatalf("exiftool %q: %v", tags, err)
	}
}

func newExtractor(t *testing.T) *Extractor {
	t.Helper()
	e := New()
	t.Cleanup(func() {
		if err := e.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return e
}

// idleCount returns the number of idle servers.
func (e *Extractor) idleCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.idle)
}

func TestExtractFixture(t *testing.T) {
	info, err := newExtractor(t).Extract(t.Context(), fixture)
	if err != nil {
		t.Fatal(err)
	}

	e := info.Exif
	if e.CameraMake != "samsung" || e.CameraModel != "SM-G930P" || e.ShutterSpeed != "1/1600" ||
		e.Aperture != "1.70" || e.ISO != 40 || e.FocalLength != "4.20mm" {
		t.Errorf("Exif = %+v", e)
	}
	if want := time.Date(2017, 5, 29, 11, 11, 16, 0, time.UTC); !e.CaptureDate.Equal(want) {
		t.Errorf("CaptureDate = %v, want %v", e.CaptureDate, want)
	}
	// 26° 34′ 57.06″ N, 80° 12′ 0.84″ W
	if math.Abs(e.Latitude-26.58251667) > 1e-6 || math.Abs(e.Longitude+80.20023333) > 1e-6 {
		t.Errorf("position = %v, %v", e.Latitude, e.Longitude)
	}

	// XMP wins over IPTC-IIM where both are present.
	i := info.IPTC
	if i.Title != "object name here" || i.Creator != "Carl Seibert (XMP)" ||
		!strings.HasPrefix(i.Description, "(This caption in XMP)") ||
		i.Copyright != "© Copyright 2017 Carl Seibert  metadatamatters.blog (XMP)" ||
		i.Credit != "credit here" || i.Source != "source here" {
		t.Errorf("IPTC = %+v", i)
	}
	if want := time.Date(2017, 5, 29, 17, 19, 21, 0, time.FixedZone("", -4*3600)); !i.Created.Equal(want) {
		t.Errorf("Created = %v, want %v", i.Created, want)
	}
	if len(i.Keywords) == 0 || i.Keywords[0] != "keywords go here" || slices.Contains(i.Keywords, "") {
		t.Errorf("Keywords = %q", i.Keywords)
	}
	if len(info.XMP.Packet) == 0 {
		t.Error("no XMP packet")
	}
}

func TestExtractXMPGPS(t *testing.T) {
	path := newJPEG(t)
	writeTags(t, path, "-XMP-exif:GPSLatitude=10.5N", "-XMP-exif:GPSLongitude=20.25W")

	info, err := newExtractor(t).Extract(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	// The strings are stored as ExifTool writes them into the packet.
	if info.XMP.GPSLatitude != "10,30.0N" || info.XMP.GPSLongitude != "20,15.0W" {
		t.Errorf("XMP GPS = %q, %q", info.XMP.GPSLatitude, info.XMP.GPSLongitude)
	}
	// With no EXIF position, the XMP one is used.
	if info.Exif.Latitude != 10.5 || info.Exif.Longitude != -20.25 {
		t.Errorf("position = %v, %v, want 10.5, -20.25", info.Exif.Latitude, info.Exif.Longitude)
	}
}

func TestExtractErrors(t *testing.T) {
	e := newExtractor(t)
	for _, path := range []string{"", "a\nb.jpg", filepath.Join(t.TempDir(), "missing.jpg")} {
		if _, err := e.Extract(t.Context(), path); err == nil {
			t.Errorf("Extract(%q) succeeded, want an error", path)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := e.Extract(ctx, newJPEG(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("Extract with a canceled context = %v, want %v", err, context.Canceled)
	}
}

func TestExtractReusesAndRetiresServers(t *testing.T) {
	e := newExtractor(t)
	if _, err := e.Extract(t.Context(), newJPEG(t)); err != nil {
		t.Fatal(err)
	}
	if n := e.idleCount(); n != 1 {
		t.Fatalf("after a small file, %d idle servers, want 1", n)
	}

	// A file this large retires its server, even though ExifTool cannot read it.
	big := filepath.Join(t.TempDir(), "big.jpg")
	if err := os.WriteFile(big, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, retireFileBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Extract(t.Context(), big); err == nil {
		t.Error("Extract of a file of zeros succeeded, want an error")
	}
	if n := e.idleCount(); n != 0 {
		t.Errorf("after a %d-byte file, %d idle servers, want 0", retireFileBytes, n)
	}
}

func TestExtractConcurrent(t *testing.T) {
	e := newExtractor(t)
	path := newJPEG(t)
	var wg sync.WaitGroup
	for range 3 * e.slots {
		wg.Go(func() {
			if _, err := e.Extract(t.Context(), path); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := int64(e.idleCount()); n < 1 || n > e.slots {
		t.Errorf("%d idle servers, want 1 to %d", n, e.slots)
	}
}

func TestShutdownIdle(t *testing.T) {
	e := newExtractor(t)
	if _, err := e.Extract(t.Context(), newJPEG(t)); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	since := e.idle[0].since
	e.mu.Unlock()

	// Times are passed explicitly, so the janitor's real ticker, 30 s away,
	// plays no part.
	e.shutdownIdle(since.Add(idleTimeout - time.Second))
	if n := e.idleCount(); n != 1 {
		t.Fatalf("before idleTimeout, %d idle servers, want 1", n)
	}
	e.shutdownIdle(since.Add(idleTimeout))
	if n := e.idleCount(); n != 0 {
		t.Errorf("at idleTimeout, %d idle servers, want 0", n)
	}
}

func TestClose(t *testing.T) {
	e := New()
	if _, err := e.Extract(t.Context(), newJPEG(t)); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if _, err := e.Extract(t.Context(), newJPEG(t)); !errors.Is(err, ErrClosed) {
		t.Errorf("Extract after Close = %v, want %v", err, ErrClosed)
	}
}
