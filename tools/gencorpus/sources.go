package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// The source archives are pinned by URL and hash. CPAN and go.dev serve
// release files that never change once published.
const (
	exiftoolURL    = "https://cpan.metacpan.org/authors/id/E/EX/EXIFTOOL/Image-ExifTool-13.55.tar.gz"
	exiftoolSHA256 = "5f4c81d34ad406538c2871ad72dbfceb5d9b412b2f16cbbeb4d712d270846667"
	goSourceURL    = "https://go.dev/dl/" + goVersion + ".src.tar.gz"
	goSourceSHA256 = "73cfb8571aec0c01177687db87163d46aa20e9779672772891aa17654d9a6099"
	maxDownload    = 64 << 20
)

// exiftool writes the images in the t/images directory of the ExifTool
// release to dir.
func exiftool(dir string) error {
	return extract(exiftoolURL, exiftoolSHA256, dir, func(name string) string {
		if path.Base(path.Dir(name)) != "images" || path.Base(path.Dir(path.Dir(name))) != "t" {
			return ""
		}
		return path.Base(name)
	})
}

// goSource writes the images in the testdata directories under src/image of
// the Go source release to dir, keeping their paths below src/image.
func goSource(dir string) error {
	return extract(goSourceURL, goSourceSHA256, dir, func(name string) string {
		rel, ok := strings.CutPrefix(name, "go/src/image/")
		if !ok || !strings.Contains(rel, "testdata/") {
			return ""
		}
		return rel
	})
}

// extract downloads the gzipped tar archive at url, checks its hash, and
// writes each regular image file for which dest returns a nonempty path to
// that path below dir.
func extract(url, sum, dir string, dest func(name string) string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return err
	}
	if len(data) > maxDownload {
		return fmt.Errorf("%s: larger than %d bytes", url, maxDownload)
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != sum {
		return fmt.Errorf("%s: sha256 %x, want %s", url, got, sum)
	}

	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !isImage(h.Name) {
			continue
		}
		rel := dest(h.Name)
		if rel == "" {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(rel)), b); err != nil {
			return err
		}
	}
}
