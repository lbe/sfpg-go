package osza_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza"
)

func TestStatFacadeSmoke(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "data.bin")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	pathBuf := make([]byte, len(filePath)+1)
	copy(pathBuf, filePath)

	var meta osza.FileMeta
	if err := osza.LstatAt(pathBuf, len(filePath), &meta); err != nil {
		t.Fatal(err)
	}

	var info fs.FileInfo = &meta
	if info.Name() != "" {
		t.Fatalf("Name()=%q, want empty", info.Name())
	}
	if info.Size() != 1 {
		t.Fatalf("Size()=%d, want 1", info.Size())
	}

	if err := osza.StatAt(pathBuf, len(filePath), &meta); err != nil {
		t.Fatal(err)
	}
	info = &meta
	if info.Name() != "" {
		t.Fatalf("StatAt Name()=%q, want empty", info.Name())
	}
}

func TestReadDirFacade(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries := make([]osza.Entry, 8)
	dirPath := make([]byte, len(root)+1)
	dirLen, err := osza.CopyDirPath(dirPath, root)
	if err != nil {
		t.Fatal(err)
	}
	nameBuf := make([]byte, 4096)
	scratch := make([]byte, 8192)
	joinBuf := make([]byte, 4096)

	n, err := osza.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("n=%d, want 1", n)
	}
	if string(entries[0].Name) != "a.txt" {
		t.Fatalf("name=%q, want a.txt", entries[0].Name)
	}
	if entries[0].Type() != 0 {
		t.Fatalf("type=%v want regular file", entries[0].Type())
	}
	info, err := entries[0].Info(joinBuf)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 1 {
		t.Fatalf("size=%d want 1", info.Size())
	}
}

func TestSentinelErrorsIs(t *testing.T) {
	if !errors.Is(osza.ErrOverflow, osza.ErrOverflow) {
		t.Fatal("ErrOverflow")
	}
	if !errors.Is(osza.ErrPathBuffer, osza.ErrPathBuffer) {
		t.Fatal("ErrPathBuffer")
	}
	if !errors.Is(osza.ErrNilFileMeta, osza.ErrNilFileMeta) {
		t.Fatal("ErrNilFileMeta")
	}
	if !errors.Is(osza.ErrShortScratch, osza.ErrShortScratch) {
		t.Fatal("ErrShortScratch")
	}
	if !errors.Is(osza.ErrTooManySymlinks, osza.ErrTooManySymlinks) {
		t.Fatal("ErrTooManySymlinks")
	}
}

func TestReadDirErrOverflowFacade(t *testing.T) {
	root := t.TempDir()
	for _, base := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(root, base), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dirPath := make([]byte, len(root)+1)
	dirLen, err := osza.CopyDirPath(dirPath, root)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]osza.Entry, 1)
	nameBuf := make([]byte, 4096)
	scratch := make([]byte, 8192)
	joinBuf := make([]byte, 4096)
	n, err := osza.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
	if !errors.Is(err, osza.ErrOverflow) {
		t.Fatalf("err=%v want ErrOverflow", err)
	}
	if n != 1 {
		t.Fatalf("n=%d want 1 partial entry", n)
	}
}

func TestReadDirErrShortScratchFacade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scratch not required on windows")
	}
	root := t.TempDir()
	dirPath := make([]byte, len(root)+1)
	dirLen, err := osza.CopyDirPath(dirPath, root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = osza.ReadDir(dirPath, dirLen, make([]osza.Entry, 4), make([]byte, 256), make([]byte, 64), make([]byte, 256))
	if !errors.Is(err, osza.ErrShortScratch) {
		t.Fatalf("err=%v want ErrShortScratch", err)
	}
}

func TestLstatJoinFacade(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "child.dat")
	if err := os.WriteFile(filePath, []byte("ab"), 0o644); err != nil {
		t.Fatal(err)
	}
	joinBuf := make([]byte, len(root)+len("child.dat")+2)
	copy(joinBuf, root)
	copy(joinBuf[len(root):], "child.dat")
	parentLen := len(root)
	nameLen := len("child.dat")
	var meta osza.FileMeta
	if err := osza.LstatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Size() != 2 {
		t.Fatalf("size=%d want 2", meta.Size())
	}
}
