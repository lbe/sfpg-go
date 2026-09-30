// Package testfixture provides gallery HTML fixtures for bodycodec tests.
package testfixture

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const htmlScanLimit = 4 << 10

const (
	archiveName = "galleries.tar.gz"
	markerName  = ".galleries-extracted"
)

var (
	extractOnce sync.Once
	extractErr  error
)

// Read returns gallery HTML fixture bytes, extracting testdata on first use.
func Read(name string) ([]byte, error) {
	if err := validateFixtureName(name); err != nil {
		return nil, err
	}
	extractOnce.Do(func() {
		extractErr = ensureExtracted()
	})
	if extractErr != nil {
		return nil, extractErr
	}
	path := filepath.Join(testdataDir(), name)
	return os.ReadFile(path)
}

// LooksLikeHTML reports whether b appears to be an HTML document.
func LooksLikeHTML(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	scan := b
	if len(scan) > htmlScanLimit {
		scan = scan[:htmlScanLimit]
	}
	i := 0
	for i < len(scan) {
		c := scan[i]
		if c != 0x20 && c != 0x09 && c != 0x0A && c != 0x0D {
			break
		}
		i++
	}
	scan = scan[i:]
	if len(scan) == 0 {
		return false
	}
	if len(scan) >= 9 && bytes.EqualFold(scan[:9], []byte("<!DOCTYPE")) {
		return true
	}
	if len(scan) >= 5 && bytes.EqualFold(scan[:5], []byte("<html")) {
		return true
	}
	if scan[0] == '<' && len(scan) >= 2 &&
		((scan[1] >= 'a' && scan[1] <= 'z') || (scan[1] >= 'A' && scan[1] <= 'Z')) {
		return true
	}
	return false
}

func validateFixtureName(name string) error {
	base := filepath.Base(name)
	if base != name || !strings.HasPrefix(base, "gallery_") || !strings.HasSuffix(base, ".html") {
		return fmt.Errorf("testfixture: invalid fixture name %q", name)
	}
	return nil
}

func testdataDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("testfixture: runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "..", "testdata")
}

func ensureExtracted() error {
	dir := testdataDir()
	marker := filepath.Join(dir, markerName)
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if allFixturesPresent(dir) {
		return os.WriteFile(marker, []byte("ok\n"), 0o644)
	}
	archivePath := filepath.Join(dir, archiveName)
	if _, err := os.Stat(archivePath); err != nil {
		return fmt.Errorf("testfixture: %s: %w (run scripts/extract_bodycodec_testdata.sh to regenerate)", archivePath, err)
	}
	if err := extractArchive(archivePath, dir); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte("ok\n"), 0o644)
}

var fixtureNames = []string{
	"gallery_small_1.html", "gallery_small_2.html", "gallery_small_3.html",
	"gallery_med_1.html", "gallery_med_2.html", "gallery_med_3.html",
	"gallery_large_1.html", "gallery_large_2.html", "gallery_large_3.html",
}

func allFixturesPresent(dir string) bool {
	for _, name := range fixtureNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return false
		}
	}
	return true
}

func extractArchive(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("testfixture: gzip open: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("testfixture: tar read: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if strings.Contains(name, string(os.PathSeparator)) || name == "." || name == ".." {
			return fmt.Errorf("testfixture: unsafe tar entry %q", hdr.Name)
		}
		if err := validateFixtureName(name); err != nil {
			continue
		}
		dest := filepath.Join(destDir, name)
		if err := writeFixtureFile(dest, tr, hdr.Mode); err != nil {
			return err
		}
	}
	return nil
}

func writeFixtureFile(path string, r io.Reader, mode int64) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gallery-extract-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, os.FileMode(mode)); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}
