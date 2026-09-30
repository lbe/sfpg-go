//go:build linux || darwin

package stat

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func copyPathBuf(path string) []byte {
	buf := make([]byte, len(path)+1)
	copy(buf, path)
	return buf
}

func packJoinBuf(parent, name string) ([]byte, int, int) {
	parentLen := len(parent)
	nameLen := len(name)
	buf := make([]byte, parentLen+1+nameLen+1)
	copy(buf[:parentLen], parent)
	copy(buf[parentLen:parentLen+nameLen], name)
	return buf, parentLen, nameLen
}

func assertMetaParity(t *testing.T, got *FileMeta, want os.FileInfo) {
	t.Helper()
	if got.IsDir() != want.IsDir() {
		t.Fatalf("IsDir: got %v want %v", got.IsDir(), want.IsDir())
	}
	if got.Size() != want.Size() {
		t.Fatalf("Size: got %d want %d", got.Size(), want.Size())
	}
	if got.Mode() != want.Mode() {
		t.Fatalf("Mode: got %v want %v", got.Mode(), want.Mode())
	}
	if !got.ModTime().Equal(want.ModTime()) {
		t.Fatalf("ModTime: got %v want %v", got.ModTime(), want.ModTime())
	}
}

func TestLstatAtStatAtParity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "link")
	if err := os.Symlink(filePath, linkPath); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		path string
	}{
		{"file", filePath},
		{"dir", subDir},
		{"symlink", linkPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var meta FileMeta
			want, err := os.Lstat(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			buf := copyPathBuf(tc.path)
			if err := LstatAt(buf, len(tc.path), &meta); err != nil {
				t.Fatal(err)
			}
			assertMetaParity(t, &meta, want)
		})
	}

	t.Run("statFollowsSymlink", func(t *testing.T) {
		t.Parallel()
		var meta FileMeta
		want, err := os.Stat(linkPath)
		if err != nil {
			t.Fatal(err)
		}
		buf := copyPathBuf(linkPath)
		if err := StatAt(buf, len(linkPath), &meta); err != nil {
			t.Fatal(err)
		}
		assertMetaParity(t, &meta, want)
		if meta.Mode()&fs.ModeSymlink != 0 {
			t.Fatal("StatAt on symlink should not report ModeSymlink")
		}
	})

	t.Run("lstatSymlink", func(t *testing.T) {
		t.Parallel()
		var meta FileMeta
		want, err := os.Lstat(linkPath)
		if err != nil {
			t.Fatal(err)
		}
		buf := copyPathBuf(linkPath)
		if err := LstatAt(buf, len(linkPath), &meta); err != nil {
			t.Fatal(err)
		}
		assertMetaParity(t, &meta, want)
		if meta.Mode()&fs.ModeSymlink == 0 {
			t.Fatal("LstatAt on symlink should report ModeSymlink")
		}
	})
}

func TestFileMetaNameEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var meta FileMeta
	buf := copyPathBuf(path)
	if err := LstatAt(buf, len(path), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Name() != "" {
		t.Fatalf("Name() = %q, want empty", meta.Name())
	}
	var asFI fs.FileInfo = &meta
	if asFI.Name() != "" {
		t.Fatalf("fs.FileInfo Name() = %q, want empty", asFI.Name())
	}
}

func TestLstatAtNotExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing")
	var meta FileMeta
	buf := copyPathBuf(path)
	err := LstatAt(buf, len(path), &meta)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *fs.PathError, got %T", err)
	}
	if pe.Op != "Lstat" || pe.Path != path {
		t.Fatalf("PathError: Op=%q Path=%q", pe.Op, pe.Path)
	}
}

func TestStatAtNotExist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "missing")
	var meta FileMeta
	buf := copyPathBuf(path)
	err := StatAt(buf, len(path), &meta)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *fs.PathError, got %T", err)
	}
	if pe.Op != "Stat" || pe.Path != path {
		t.Fatalf("PathError: Op=%q Path=%q", pe.Op, pe.Path)
	}
}

func TestAtNilFileMeta(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	buf := copyPathBuf(path)
	if err := LstatAt(buf, len(path), nil); !errors.Is(err, ErrNilFileMeta) {
		t.Fatalf("LstatAt nil dest: %v", err)
	}
	if err := StatAt(buf, len(path), nil); !errors.Is(err, ErrNilFileMeta) {
		t.Fatalf("StatAt nil dest: %v", err)
	}
}

func TestLstatJoinStatJoinParity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fileName := "data.bin"
	filePath := filepath.Join(dir, fileName)
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	subName := "subdir"
	subDir := filepath.Join(dir, subName)
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkName := "link"
	linkPath := filepath.Join(dir, linkName)
	if err := os.Symlink(filePath, linkPath); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		base string
		leaf string
	}{
		{"file", dir, fileName},
		{"dir", dir, subName},
		{"symlink", dir, linkName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var meta FileMeta
			full := filepath.Join(tc.base, tc.leaf)
			want, err := os.Lstat(full)
			if err != nil {
				t.Fatal(err)
			}
			joinBuf, parentLen, nameLen := packJoinBuf(tc.base, tc.leaf)
			if err := LstatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
				t.Fatal(err)
			}
			assertMetaParity(t, &meta, want)
		})
	}

	t.Run("statFollowsSymlink", func(t *testing.T) {
		t.Parallel()
		var meta FileMeta
		want, err := os.Stat(linkPath)
		if err != nil {
			t.Fatal(err)
		}
		joinBuf, parentLen, nameLen := packJoinBuf(dir, linkName)
		if err := StatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
			t.Fatal(err)
		}
		assertMetaParity(t, &meta, want)
		if meta.Mode()&fs.ModeSymlink != 0 {
			t.Fatal("StatJoin on symlink should not report ModeSymlink")
		}
	})

	t.Run("lstatSymlink", func(t *testing.T) {
		t.Parallel()
		var meta FileMeta
		want, err := os.Lstat(linkPath)
		if err != nil {
			t.Fatal(err)
		}
		joinBuf, parentLen, nameLen := packJoinBuf(dir, linkName)
		if err := LstatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
			t.Fatal(err)
		}
		assertMetaParity(t, &meta, want)
		if meta.Mode()&fs.ModeSymlink == 0 {
			t.Fatal("LstatJoin on symlink should report ModeSymlink")
		}
	})
}

func TestLstatJoinErrPathBuffer(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	joinBuf, parentLen, nameLen := packJoinBuf(dir, "missing")
	joinBuf = joinBuf[:parentLen+nameLen] // drop room for separator and NUL
	var meta FileMeta
	err := LstatJoin(joinBuf, parentLen, nameLen, &meta)
	if !errors.Is(err, ErrPathBuffer) {
		t.Fatalf("err = %v, want ErrPathBuffer", err)
	}
}

func TestJoinNilFileMeta(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	joinBuf, parentLen, nameLen := packJoinBuf(dir, "f")
	if err := LstatJoin(joinBuf, parentLen, nameLen, nil); !errors.Is(err, ErrNilFileMeta) {
		t.Fatalf("LstatJoin nil dest: %v", err)
	}
	if err := StatJoin(joinBuf, parentLen, nameLen, nil); !errors.Is(err, ErrNilFileMeta) {
		t.Fatalf("StatJoin nil dest: %v", err)
	}
}

func TestLstatJoinAllocsPerRun(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	joinBuf, parentLen, nameLen := packJoinBuf(dir, "data.bin")
	var meta FileMeta
	if err := LstatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if nameLen > 0 {
			copy(joinBuf[parentLen:parentLen+nameLen], joinBuf[parentLen+1:parentLen+1+nameLen])
		}
		if err := LstatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("LstatJoin allocs per run = %v, want 0", allocs)
	}
}

func TestStatJoinAllocsPerRun(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	joinBuf, parentLen, nameLen := packJoinBuf(dir, "data.bin")
	var meta FileMeta
	if err := StatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if nameLen > 0 {
			copy(joinBuf[parentLen:parentLen+nameLen], joinBuf[parentLen+1:parentLen+1+nameLen])
		}
		if err := StatJoin(joinBuf, parentLen, nameLen, &meta); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("StatJoin allocs per run = %v, want 0", allocs)
	}
}

func TestLstatAtAllocsPerRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pathBuf := copyPathBuf(path)
	var meta FileMeta
	if err := LstatAt(pathBuf, len(path), &meta); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if err := LstatAt(pathBuf, len(path), &meta); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("LstatAt allocs per run = %v, want 0", allocs)
	}
}

func TestStatAtAllocsPerRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pathBuf := copyPathBuf(path)
	var meta FileMeta
	if err := StatAt(pathBuf, len(path), &meta); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		if err := StatAt(pathBuf, len(path), &meta); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("StatAt allocs per run = %v, want 0", allocs)
	}
}
