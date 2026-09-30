//go:build linux || darwin

package evalsymlinks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza/stat"
)

func TestEvalSymlinksLongSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "s")
	target := filepath.Join(root, strings.Repeat("d", 80))
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	path := link
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}

	pathBuf := make([]byte, 512)
	copy(pathBuf, path)
	dest := make([]byte, 4096)
	n, err := EvalSymlinksAt(pathBuf, len(path), dest)
	if err != nil {
		t.Fatalf("EvalSymlinksAt: %v", err)
	}
	if string(dest[:n]) != want {
		t.Fatalf("got %q want %q", dest[:n], want)
	}
}

func TestEvalSymlinksSmallPathBuf(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "s")
	target := filepath.Join(root, strings.Repeat("d", 80))
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	path := link
	pathBuf := make([]byte, len(path)+1)
	copy(pathBuf, path)
	dest := make([]byte, 4096)
	_, err := EvalSymlinksAt(pathBuf, len(path), dest)
	if !errors.Is(err, stat.ErrPathBuffer) {
		t.Fatalf("err=%v want ErrPathBuffer", err)
	}
}
