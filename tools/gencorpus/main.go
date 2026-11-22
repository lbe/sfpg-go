// Gencorpus writes the image corpus used to compare discovery and thumbnail
// output between builds of sfpg-go.
//
// The corpus is the same on every machine. It holds the sample images from a
// pinned ExifTool release, the image test data from the pinned Go release,
// generated images and edge cases. Files and directories get fixed
// modification times. The run fails unless every path, hash and modification
// time matches corpus.manifest, and it removes the output directory on
// failure.
//
// Run it from this directory:
//
//	go run . -out ../../tmp/corpus
//
// The -j flag sets how many images are encoded at once; it does not change
// the output.
//
// After an intended change to the generator, rewrite the manifest with
//
//	go run . -out ../../tmp/corpus -update
package main

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// goVersion is the Go release whose encoders and test data produced
// corpus.manifest. Other releases may encode images differently.
const goVersion = "go1.27.4"

// epoch is 2020-01-01T00:00:00Z. Files get modification times one minute
// apart from epoch in lexical order. Directories follow in reverse lexical
// order, ending with the output directory, so each directory is newer than
// everything in it.
const epoch = 1577836800

//go:embed corpus.manifest
var manifest string

func main() {
	log.SetFlags(0)
	log.SetPrefix("gencorpus: ")
	out := flag.String("out", "", "directory to create; must not exist")
	update := flag.Bool("update", false, "rewrite corpus.manifest instead of checking against it")
	workers := flag.Int("j", 4, "images to encode at once; each can use about 200 MB")
	flag.Parse()
	if *out == "" || *workers < 1 || flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	if v := runtime.Version(); v != goVersion {
		log.Fatalf("built with %s; corpus.manifest needs %s (set GOTOOLCHAIN=%s)", v, goVersion, goVersion)
	}
	if err := os.Mkdir(*out, 0o755); err != nil {
		log.Fatal(err)
	}
	if err := run(*out, *update, *workers); err != nil {
		os.RemoveAll(*out)
		log.Fatal(err)
	}
}

func run(out string, update bool, workers int) error {
	if err := exiftool(filepath.Join(out, "exiftool")); err != nil {
		return err
	}
	if err := goSource(filepath.Join(out, "goroot")); err != nil {
		return err
	}
	if err := synthetic(filepath.Join(out, "synthetic"), workers); err != nil {
		return err
	}
	if err := edge(filepath.Join(out, "edge")); err != nil {
		return err
	}
	if err := setTimes(out); err != nil {
		return err
	}

	got, err := list(out)
	if err != nil {
		return err
	}
	if update {
		return os.WriteFile("corpus.manifest", []byte(got), 0o644)
	}
	return compare(manifest, got)
}

// isImage reports whether name has an extension discovery treats as an image.
func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".gif", ".png":
		return true
	}
	return false
}

// writeFile writes data to name, creating parent directories as needed.
func writeFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0o644)
}

// setTimes gives every path under root its modification time; see epoch.
func setTimes(root string) error {
	var files, dirs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
		} else {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	slices.Reverse(dirs)
	for i, path := range append(files, dirs...) {
		t := time.Unix(epoch+int64(i)*60, 0)
		if err := os.Chtimes(path, t, t); err != nil {
			return err
		}
	}
	return nil
}

// list returns the manifest for root: one line per path, in lexical order,
// holding the SHA-256 of a file's contents or "-" for a directory, the
// modification time in Unix seconds, and the slash-separated path relative to
// root, which ends in a slash for a directory. Root itself is "./".
func list(root string) (string, error) {
	var b strings.Builder
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mtime := info.ModTime()
		if mtime.Nanosecond() != 0 {
			return fmt.Errorf("%s: modification time %v is not a whole second", path, mtime)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		sum := "-"
		if d.IsDir() {
			rel += "/"
		} else {
			if sum, err = digest(h, path); err != nil {
				return err
			}
		}
		fmt.Fprintf(&b, "%s  %d  %s\n", sum, mtime.Unix(), rel)
		return nil
	})
	return b.String(), err
}

// digest returns the hex SHA-256 of the file at path, reusing h.
func digest(h hash.Hash, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h.Reset()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// compare reports how got differs from want.
func compare(want, got string) error {
	if want == got {
		return nil
	}
	w, g := parse(want), parse(got)
	var missing, extra, changed []string
	for path, entry := range w {
		switch ge, ok := g[path]; {
		case !ok:
			missing = append(missing, path)
		case ge != entry:
			changed = append(changed, path)
		}
	}
	for path := range g {
		if _, ok := w[path]; !ok {
			extra = append(extra, path)
		}
	}
	var b strings.Builder
	for _, d := range []struct {
		name  string
		paths []string
	}{{"missing", missing}, {"extra", extra}, {"changed", changed}} {
		if len(d.paths) == 0 {
			continue
		}
		slices.Sort(d.paths)
		fmt.Fprintf(&b, "\n%d %s, first: %s", len(d.paths), d.name, d.paths[0])
	}
	if b.Len() == 0 {
		b.WriteString(" same entries, but corpus.manifest has duplicate, misordered or malformed lines")
	}
	return errors.New("corpus does not match corpus.manifest:" + b.String())
}

// parse maps each path in a manifest to the rest of its line.
func parse(manifest string) map[string]string {
	m := make(map[string]string)
	for line := range strings.Lines(manifest) {
		f := strings.SplitN(strings.TrimRight(line, "\r\n"), "  ", 3)
		if len(f) == 3 {
			m[f[2]] = f[0] + "  " + f[1]
		}
	}
	return m
}
