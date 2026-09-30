//go:build linux || darwin

package evalsymlinks_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza"
	"github.com/lbe/sfpg-go/internal/osza/evalsymlinks"
)

// evalSymlinksBenchFixture builds temp-dir paths for EvalSymlinks benchmarks.
// pathBuf templates are copied back into pathBuf each timed iteration when the
// walk may rewrite pathBuf (symlink cases).
type evalSymlinksBenchFixture struct {
	plainPath string
	plainLen  int
	plainOrig []byte
	plainBuf  []byte

	linkPath string
	linkLen  int
	linkOrig []byte
	linkBuf  []byte

	multiPath string
	multiLen  int
	multiOrig []byte
	multiBuf  []byte

	dest []byte
}

func evalSymlinksBenchFixtureSetup(b *testing.B) evalSymlinksBenchFixture {
	b.Helper()
	root := b.TempDir()
	real := filepath.Join(root, "real")
	if err := mkdirBench(b, real); err != nil {
		b.Fatal(err)
	}

	plainPath := real
	linkPath := filepath.Join(root, "linkdir")
	if err := symlinkBench(b, real, linkPath); err != nil {
		b.Fatal(err)
	}

	hop1 := filepath.Join(root, "hop1")
	hop2 := filepath.Join(root, "hop2")
	if err := symlinkBench(b, hop2, hop1); err != nil {
		b.Fatal(err)
	}
	if err := symlinkBench(b, real, hop2); err != nil {
		b.Fatal(err)
	}
	multiPath := hop1

	destCap := 4096
	plainBuf, plainOrig := pathBufPair(plainPath, destCap)
	linkBuf, linkOrig := pathBufPair(linkPath, destCap)
	multiBuf, multiOrig := pathBufPair(multiPath, destCap)
	dest := make([]byte, destCap)

	warmEval(b, plainBuf, plainOrig, len(plainPath), dest)
	warmEval(b, linkBuf, linkOrig, len(linkPath), dest)
	warmEval(b, multiBuf, multiOrig, len(multiPath), dest)
	warmOS(b, plainPath)
	warmOS(b, linkPath)
	warmOS(b, multiPath)

	return evalSymlinksBenchFixture{
		plainPath: plainPath,
		plainLen:  len(plainPath),
		plainOrig: plainOrig,
		plainBuf:  plainBuf,
		linkPath:  linkPath,
		linkLen:   len(linkPath),
		linkOrig:  linkOrig,
		linkBuf:   linkBuf,
		multiPath: multiPath,
		multiLen:  len(multiPath),
		multiOrig: multiOrig,
		multiBuf:  multiBuf,
		dest:      dest,
	}
}

func mkdirBench(b *testing.B, path string) error {
	b.Helper()
	return os.Mkdir(path, 0o755)
}

func symlinkBench(b *testing.B, target, link string) error {
	b.Helper()
	return os.Symlink(target, link)
}

func pathBufPair(path string, cap int) (work []byte, orig []byte) {
	if cap < len(path)+1 {
		cap = len(path) + 1
	}
	work = make([]byte, cap)
	orig = make([]byte, len(path))
	copy(orig, path)
	copy(work, path)
	return work, orig
}

func warmEval(b *testing.B, pathBuf, orig []byte, pathLen int, dest []byte) {
	b.Helper()
	if _, err := evalsymlinks.EvalSymlinksAt(pathBuf, pathLen, dest); err != nil {
		b.Fatal(err)
	}
	resetPathBuf(pathBuf, orig, pathLen)
}

func warmOS(b *testing.B, path string) {
	b.Helper()
	if _, err := filepath.EvalSymlinks(path); err != nil {
		b.Fatal(err)
	}
}

func resetPathBuf(pathBuf []byte, orig []byte, pathLen int) {
	copy(pathBuf[:pathLen], orig)
}

// BenchmarkEvalSymlinksAt_* measure osza/evalsymlinks with reused pathBuf and dest.
// Timed loops expect stable pathBuf/dest capacity (no ErrPathBuffer); success paths
// should report 0 B/op and 0 allocs/op. Grow/retry and error paths may allocate.

func BenchmarkEvalSymlinksAt_NoSymlink(b *testing.B) {
	fix := evalSymlinksBenchFixtureSetup(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := evalsymlinks.EvalSymlinksAt(fix.plainBuf, fix.plainLen, fix.dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalSymlinksAt_DirSymlink(b *testing.B) {
	fix := evalSymlinksBenchFixtureSetup(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		resetPathBuf(fix.linkBuf, fix.linkOrig, fix.linkLen)
		if _, err := evalsymlinks.EvalSymlinksAt(fix.linkBuf, fix.linkLen, fix.dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvalSymlinksAt_MultiHop(b *testing.B) {
	fix := evalSymlinksBenchFixtureSetup(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		resetPathBuf(fix.multiBuf, fix.multiOrig, fix.multiLen)
		if _, err := evalsymlinks.EvalSymlinksAt(fix.multiBuf, fix.multiLen, fix.dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSZAEvalSymlinksAt_DirSymlink(b *testing.B) {
	fix := evalSymlinksBenchFixtureSetup(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		resetPathBuf(fix.linkBuf, fix.linkOrig, fix.linkLen)
		if _, err := osza.EvalSymlinksAt(fix.linkBuf, fix.linkLen, fix.dest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSFilepathEvalSymlinks_NoSymlink(b *testing.B) {
	fix := evalSymlinksBenchFixtureSetup(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := filepath.EvalSymlinks(fix.plainPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSFilepathEvalSymlinks_DirSymlink(b *testing.B) {
	fix := evalSymlinksBenchFixtureSetup(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := filepath.EvalSymlinks(fix.linkPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSFilepathEvalSymlinks_MultiHop(b *testing.B) {
	fix := evalSymlinksBenchFixtureSetup(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := filepath.EvalSymlinks(fix.multiPath); err != nil {
			b.Fatal(err)
		}
	}
}
