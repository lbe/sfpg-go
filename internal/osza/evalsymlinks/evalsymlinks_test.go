//go:build linux || darwin

package evalsymlinks_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza"
	"github.com/lbe/sfpg-go/internal/osza/evalsymlinks"
	"github.com/lbe/sfpg-go/internal/osza/stat"
)

func copyPathBuf(path string) []byte {
	buf := make([]byte, len(path)+1)
	copy(buf, path)
	return buf
}

func evalAt(path string, pathCap int, destCap int) (string, error) {
	pathBuf := make([]byte, pathCap)
	copy(pathBuf, path)
	dest := make([]byte, destCap)
	for {
		n, err := evalsymlinks.EvalSymlinksAt(pathBuf, len(path), dest)
		if errors.Is(err, stat.ErrPathBuffer) {
			if destCap < len(path)*4 {
				destCap *= 2
				dest = make([]byte, destCap)
				continue
			}
			pathCap *= 2
			pathBuf = make([]byte, pathCap)
			copy(pathBuf, path)
			continue
		}
		if err != nil {
			return "", err
		}
		return string(dest[:n]), nil
	}
}

func TestEvalSymlinksAtMatchesStdlib(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "real")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	linkDir := filepath.Join(root, "linkdir")
	if err := os.Symlink(sub, linkDir); err != nil {
		t.Fatal(err)
	}
	relLink := filepath.Join(sub, "up")
	if err := os.Symlink("..", relLink); err != nil {
		t.Fatal(err)
	}

	cases := []string{
		sub,
		linkDir,
		filepath.Join(linkDir, "f"),
		filepath.Join(root, "linkdir", "."),
		relLink,
	}
	for _, path := range cases {
		want, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatalf("stdlib %q: %v", path, err)
		}
		got, err := evalAt(path, len(path)+64, len(path)+64)
		if err != nil {
			t.Fatalf("EvalSymlinksAt %q: %v", path, err)
		}
		if got != want {
			t.Fatalf("path %q: got %q want %q", path, got, want)
		}
	}
}

func TestEvalSymlinksAtRelativePath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "tgt")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tgt", filepath.Join(root, "rel")); err != nil {
		t.Fatal(err)
	}
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWD) }()

	path := "rel"
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := evalAt(path, 256, 256)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEvalSymlinksAtLoop(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}

	pathBuf := copyPathBuf(a)
	dest := make([]byte, 4096)
	_, err := evalsymlinks.EvalSymlinksAt(pathBuf, len(a), dest)
	if err == nil {
		t.Fatal("expected error for symlink loop")
	}
	if !errors.Is(err, osza.ErrTooManySymlinks) {
		_, stdErr := filepath.EvalSymlinks(a)
		if stdErr == nil || stdErr.Error() == "" {
			t.Fatalf("err=%v want ErrTooManySymlinks or stdlib loop error", err)
		}
	}
}

func TestEvalSymlinksAtDestTooSmall(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "d")
	pathBuf := copyPathBuf(path)
	dest := make([]byte, 1)
	_, err := evalsymlinks.EvalSymlinksAt(pathBuf, len(path), dest)
	if !errors.Is(err, stat.ErrPathBuffer) {
		t.Fatalf("err=%v want ErrPathBuffer", err)
	}

	dest = make([]byte, len(path)+8)
	n, err := evalsymlinks.EvalSymlinksAt(pathBuf, len(path), dest)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(dest[:n]) != want {
		t.Fatalf("got %q want %q", dest[:n], want)
	}
}

func TestEvalSymlinksAtGrowPathBufRetry(t *testing.T) {
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
	_, err := evalsymlinks.EvalSymlinksAt(pathBuf, len(path), dest)
	if !errors.Is(err, stat.ErrPathBuffer) {
		t.Fatalf("small pathBuf: err=%v want ErrPathBuffer", err)
	}

	got, err := evalAt(path, len(path)*8, 4096)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEvalSymlinksFacade(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "d")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	pathBuf := copyPathBuf(sub)
	dest := make([]byte, len(sub)+8)
	n, err := osza.EvalSymlinksAt(pathBuf, len(sub), dest)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatal(err)
	}
	if string(dest[:n]) != want {
		t.Fatalf("got %q want %q", dest[:n], want)
	}
}
