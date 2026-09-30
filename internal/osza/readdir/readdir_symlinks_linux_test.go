//go:build linux

package readdir_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza/readdir"
)

func TestReadDirSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkFile := filepath.Join(root, "linkfile")
	if err := os.Symlink("target.txt", linkFile); err != nil {
		t.Fatal(err)
	}
	linkDir := filepath.Join(root, "linkdir")
	if err := os.Symlink("..", linkDir); err != nil {
		t.Fatal(err)
	}

	stdEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		isDir bool
		typ   fs.FileMode
	}{}
	for _, e := range stdEntries {
		want[e.Name()] = struct {
			isDir bool
			typ   fs.FileMode
		}{e.IsDir(), e.Type()}
	}

	entries := make([]readdir.Entry, 16)
	joinBuf := make([]byte, 64<<10)
	dirPath := make([]byte, len(root)+1)
	dirLen, err := readdir.CopyDirPath(dirPath, root)
	if err != nil {
		t.Fatal(err)
	}
	n, err := readdir.ReadDir(dirPath, dirLen, entries, make([]byte, 4096), make([]byte, 8192), joinBuf)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Fatalf("n=%d want %d", n, len(want))
	}
	for i := range entries[:n] {
		e := &entries[i]
		w := want[string(e.Name)]
		if e.IsDir() != w.isDir || e.Type() != w.typ {
			t.Errorf("%s: isDir=%v typ=%v want isDir=%v typ=%v",
				e.Name, e.IsDir(), e.Type(), w.isDir, w.typ)
		}
		info, err := e.Info(joinBuf)
		if err != nil {
			t.Fatal(err)
		}
		stdInfo, err := wantInfo(stdEntries, string(e.Name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Type() != stdInfo.Mode().Type() {
			t.Errorf("%s info type %v want %v", e.Name, info.Mode().Type(), stdInfo.Mode().Type())
		}
	}
}

func wantInfo(std []fs.DirEntry, name string) (fs.FileInfo, error) {
	for _, e := range std {
		if e.Name() == name {
			return e.Info()
		}
	}
	return nil, fs.ErrNotExist
}
