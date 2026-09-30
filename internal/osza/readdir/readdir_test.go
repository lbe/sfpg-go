package readdir_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza/readdir"
	"github.com/lbe/sfpg-go/internal/osza/stat"
)

func setupDir(t testing.TB, files []string, dirs []string) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range dirs {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func testJoinBuf() []byte {
	return make([]byte, 64<<10)
}

func readDirRoot(dir string, entries []readdir.Entry, nameBuf, scratch, joinBuf []byte) (int, error) {
	dirPath := make([]byte, len(dir)+1)
	dirLen, err := readdir.CopyDirPath(dirPath, dir)
	if err != nil {
		return 0, err
	}
	return readdir.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
}

func names(entries []readdir.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = string(e.Name)
	}
	sort.Strings(out)
	return out
}

func readDirRetry(root string) (int, error) {
	entries := make([]readdir.Entry, 1)
	nameBuf := make([]byte, 8)
	scratch := make([]byte, 8192)
	joinBuf := testJoinBuf()
	for {
		n, err := readDirRoot(root, entries, nameBuf, scratch, joinBuf)
		if err == nil {
			return n, nil
		}
		if errors.Is(err, readdir.ErrOverflow) {
			entries = make([]readdir.Entry, len(entries)*2)
			nameBuf = make([]byte, len(nameBuf)*2)
			continue
		}
		if errors.Is(err, stat.ErrPathBuffer) {
			joinBuf = make([]byte, len(joinBuf)*2)
			continue
		}
		return n, err
	}
}

func TestReadDirBasic(t *testing.T) {
	root := setupDir(t, []string{"a.txt", "b.txt"}, []string{"sub"})

	entries := make([]readdir.Entry, 16)
	nameBuf := make([]byte, 4096)
	scratch := make([]byte, 8192)

	n, err := readDirRoot(root, entries, nameBuf, scratch, testJoinBuf())
	if err != nil {
		t.Fatal(err)
	}
	got := names(entries[:n])
	want := []string{"a.txt", "b.txt", "sub"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	for _, e := range entries[:n] {
		wantDir := string(e.Name) == "sub"
		if e.IsDir() != wantDir {
			t.Errorf("%s: IsDir=%v, want %v", e.Name, e.IsDir(), wantDir)
		}
	}
}

func TestReadDirEmpty(t *testing.T) {
	root := t.TempDir()
	entries := make([]readdir.Entry, 4)
	n, err := readDirRoot(root, entries, make([]byte, 256), make([]byte, 8192), testJoinBuf())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("n=%d, want 0", n)
	}
}

func TestReadDirNotExist(t *testing.T) {
	_, err := readDirRoot(filepath.Join(t.TempDir(), "missing"),
		make([]readdir.Entry, 1), make([]byte, 64), make([]byte, 8192), testJoinBuf())
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestReadDirShortScratch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scratch not required on windows")
	}
	root := setupDir(t, []string{"one"}, nil)
	dirPath := make([]byte, len(root)+1)
	dirLen, err := readdir.CopyDirPath(dirPath, root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readdir.ReadDir(dirPath, dirLen, make([]readdir.Entry, 4), make([]byte, 256), make([]byte, 64), testJoinBuf())
	if !errors.Is(err, readdir.ErrShortScratch) {
		t.Fatalf("err=%v, want ErrShortScratch", err)
	}
}

func TestReadDirEntriesOverflow(t *testing.T) {
	root := setupDir(t, []string{"a", "b", "c"}, nil)
	entries := make([]readdir.Entry, 1) // too small
	n, err := readDirRoot(root, entries, make([]byte, 4096), make([]byte, 8192), testJoinBuf())
	if !errors.Is(err, readdir.ErrOverflow) {
		t.Fatalf("err=%v, want ErrOverflow", err)
	}
	if n != 1 {
		t.Fatalf("n=%d, want 1 (partial fill)", n)
	}
}

func TestReadDirNameBufOverflow(t *testing.T) {
	root := setupDir(t, []string{"longfilename1.txt", "longfilename2.txt"}, nil)
	n, err := readDirRoot(root, make([]readdir.Entry, 8),
		make([]byte, 4), make([]byte, 8192), testJoinBuf()) // nameBuf too small
	if !errors.Is(err, readdir.ErrOverflow) {
		t.Fatalf("err=%v, want ErrOverflow", err)
	}
	if n != 0 {
		t.Fatalf("n=%d, want 0", n)
	}
}

func TestReadDirUnicodeNames(t *testing.T) {
	root := setupDir(t, []string{"héllo.txt", "日本語.txt"}, nil)
	entries := make([]readdir.Entry, 8)
	n, err := readDirRoot(root, entries, make([]byte, 4096), make([]byte, 8192), testJoinBuf())
	if err != nil {
		t.Fatal(err)
	}
	got := names(entries[:n])
	want := []string{"héllo.txt", "日本語.txt"}
	sort.Strings(want)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestEntryName(t *testing.T) {
	root := setupDir(t, []string{"a.txt"}, nil)
	entries := make([]readdir.Entry, 4)
	n, err := readDirRoot(root, entries, make([]byte, 256), make([]byte, 8192), testJoinBuf())
	if err != nil || n != 1 {
		t.Fatalf("ReadDir: n=%d err=%v", n, err)
	}
	if string(entries[0].Name) != "a.txt" {
		t.Fatalf("name=%q want a.txt", entries[0].Name)
	}
}

func TestMatchesOSReadDir(t *testing.T) {
	root := setupDir(t,
		[]string{"f1", "f2", "f3"}, []string{"d1", "d2"})

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
	n, err := readDirRoot(root, entries, make([]byte, 4096), make([]byte, 8192), testJoinBuf())
	if err != nil {
		t.Fatal(err)
	}
	if n != len(want) {
		t.Fatalf("n=%d, want %d", n, len(want))
	}
	for _, e := range entries[:n] {
		w, ok := want[string(e.Name)]
		if !ok {
			t.Errorf("unexpected entry %q", e.Name)
			continue
		}
		if e.IsDir() != w.isDir {
			t.Errorf("%s: IsDir=%v, want %v", e.Name, e.IsDir(), w.isDir)
		}
		if e.Type() != w.typ {
			t.Errorf("%s: Type=%v, want %v", e.Name, e.Type(), w.typ)
		}
	}
}

func TestMatchesOSReadDirInfo(t *testing.T) {
	root := setupDir(t,
		[]string{"f1", "f2"}, []string{"d1"})

	stdEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]fs.FileInfo{}
	for _, e := range stdEntries {
		stdInfo, infoErr := e.Info()
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		want[e.Name()] = stdInfo
	}

	entries := make([]readdir.Entry, 16)
	n, err := readDirRoot(root, entries, make([]byte, 4096), make([]byte, 8192), testJoinBuf())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries[:n] {
		w := want[string(e.Name)]
		if w == nil {
			t.Errorf("unexpected entry %q", e.Name)
			continue
		}
		joinBuf := testJoinBuf()
		info, err := e.Info(joinBuf)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Type() != w.Mode().Type() {
			t.Errorf("%s: mode type %v want %v", e.Name, info.Mode().Type(), w.Mode().Type())
		}
		if info.Size() != w.Size() {
			t.Errorf("%s: size %d want %d", e.Name, info.Size(), w.Size())
		}
		if !info.ModTime().Equal(w.ModTime()) {
			t.Errorf("%s: mtime %v want %v", e.Name, info.ModTime(), w.ModTime())
		}
	}
}

func TestReadDirCallerRetry(t *testing.T) {
	root := setupDir(t, []string{"a", "b", "c", "d"}, []string{"e"})

	std, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	n, err := readDirRetry(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != len(std) {
		t.Fatalf("n=%d want %d", n, len(std))
	}
}

func TestZeroAllocs(t *testing.T) {
	root := setupDir(t, []string{"a", "b", "c"}, []string{"d"})
	entries := make([]readdir.Entry, 16)
	nameBuf := make([]byte, 4096)
	scratch := make([]byte, 8192)
	joinBuf := testJoinBuf()
	dirPath := make([]byte, len(root)+1)
	dirLen, err := readdir.CopyDirPath(dirPath, root)
	if err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(100, func() {
		if _, err := readdir.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("allocs=%v, want 0", allocs)
	}
}

func BenchmarkReadDir(b *testing.B) {
	files := make([]string, 100)
	for i := range files {
		files[i] = "file_" + string(rune('a'+i%26)) + "_" +
			string(rune('0'+i/26)) + ".txt"
	}
	root := setupDir(b, files, []string{"sub1", "sub2"})

	entries := make([]readdir.Entry, 256)
	nameBuf := make([]byte, 16*1024)
	scratch := make([]byte, 8192)
	joinBuf := testJoinBuf()
	dirPath := make([]byte, len(root)+1)
	dirLen, err := readdir.CopyDirPath(dirPath, root)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		n, err := readdir.ReadDir(dirPath, dirLen, entries, nameBuf, scratch, joinBuf)
		if err != nil {
			b.Fatal(err)
		}
		if n < 100 {
			b.Fatalf("n=%d", n)
		}
	}
}

func BenchmarkOSReadDir(b *testing.B) {
	files := make([]string, 100)
	for i := range files {
		files[i] = "file_" + string(rune('a'+i%26)) + "_" +
			string(rune('0'+i/26)) + ".txt"
	}
	root := setupDir(b, files, []string{"sub1", "sub2"})

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := os.ReadDir(root); err != nil {
			b.Fatal(err)
		}
	}
}

func benchRoot(b *testing.B) string {
	files := make([]string, 100)
	for i := range files {
		files[i] = "file_" + string(rune('a'+i%26)) + "_" +
			string(rune('0'+i/26)) + ".txt"
	}
	return setupDir(b, files, []string{"sub1", "sub2"})
}

func BenchmarkOSFileReaddir(b *testing.B) {
	root := benchRoot(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f, err := os.Open(root)
		if err != nil {
			b.Fatal(err)
		}
		infos, err := f.Readdir(-1)
		f.Close()
		if err != nil {
			b.Fatal(err)
		}
		if len(infos) < 100 {
			b.Fatalf("n=%d", len(infos))
		}
	}
}

func BenchmarkOSFileReaddirnames(b *testing.B) {
	root := benchRoot(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		f, err := os.Open(root)
		if err != nil {
			b.Fatal(err)
		}
		names, err := f.Readdirnames(-1)
		f.Close()
		if err != nil {
			b.Fatal(err)
		}
		if len(names) < 100 {
			b.Fatalf("n=%d", len(names))
		}
	}
}
