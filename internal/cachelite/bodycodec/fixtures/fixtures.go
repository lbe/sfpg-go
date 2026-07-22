// Package fixtures lazily extracts bodycodec gallery HTML fixtures from
// testdata/galleries.tar.gz on first use.
package fixtures

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	archiveName = "galleries.tar.gz"
	markerName  = ".galleries-extracted"
)

var (
	extractOnce sync.Once
	extractErr  error
)

// Read returns the contents of a gallery HTML fixture, extracting the archive on
// first access when needed files are not yet present.
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

func validateFixtureName(name string) error {
	base := filepath.Base(name)
	if base != name || !strings.HasPrefix(base, "gallery_") || !strings.HasSuffix(base, ".html") {
		return fmt.Errorf("fixtures: invalid fixture name %q", name)
	}
	return nil
}

func testdataDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("fixtures: runtime.Caller failed")
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
		return fmt.Errorf("fixtures: %s: %w (run scripts/extract_bodycodec_testdata.sh to regenerate)", archivePath, err)
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
		return fmt.Errorf("fixtures: gzip open: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("fixtures: tar read: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if strings.Contains(name, string(os.PathSeparator)) || name == "." || name == ".." {
			return fmt.Errorf("fixtures: unsafe tar entry %q", hdr.Name)
		}
		if err := validateFixtureName(name); err != nil {
			continue
		}
		dest := filepath.Join(destDir, name)
		if err := writeFile(dest, tr, hdr.Mode); err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, r io.Reader, mode int64) error {
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
