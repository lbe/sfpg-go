//go:build linux || darwin

package stat

import (
	"os"
	"path/filepath"
	"testing"
)

type statBenchFixture struct {
	filePath string
	linkPath string
	dir      string
	fileName string

	pathBuf   []byte
	fileLen   int
	linkBuf   []byte
	linkLen   int
	joinBuf   []byte
	parentLen int
	nameLen   int
}

func statBenchFixtureSetup(b *testing.B) statBenchFixture {
	b.Helper()
	dir := b.TempDir()
	fileName := "data.bin"
	filePath := filepath.Join(dir, fileName)
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		b.Fatal(err)
	}
	subDir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		b.Fatal(err)
	}
	linkPath := filepath.Join(dir, "link")
	if err := os.Symlink(filePath, linkPath); err != nil {
		b.Fatal(err)
	}

	pathBuf := make([]byte, len(filePath)+1)
	copy(pathBuf, filePath)
	linkBuf := make([]byte, len(linkPath)+1)
	copy(linkBuf, linkPath)
	joinBuf, parentLen, nameLen := packJoinBuf(dir, fileName)

	return statBenchFixture{
		filePath:  filePath,
		linkPath:  linkPath,
		dir:       dir,
		fileName:  fileName,
		pathBuf:   pathBuf,
		fileLen:   len(filePath),
		linkBuf:   linkBuf,
		linkLen:   len(linkPath),
		joinBuf:   joinBuf,
		parentLen: parentLen,
		nameLen:   nameLen,
	}
}

func (f *statBenchFixture) repackJoin() {
	if f.nameLen > 0 {
		copy(f.joinBuf[f.parentLen:f.parentLen+f.nameLen], f.joinBuf[f.parentLen+1:f.parentLen+1+f.nameLen])
	}
}

func BenchmarkLstatAt(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	var meta FileMeta
	if err := LstatAt(fix.pathBuf, fix.fileLen, &meta); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := LstatAt(fix.pathBuf, fix.fileLen, &meta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSLstat(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	if _, err := os.Lstat(fix.filePath); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := os.Lstat(fix.filePath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStatAt(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	var meta FileMeta
	if err := StatAt(fix.pathBuf, fix.fileLen, &meta); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := StatAt(fix.pathBuf, fix.fileLen, &meta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSStat(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	if _, err := os.Stat(fix.filePath); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := os.Stat(fix.filePath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStatAtSymlink(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	var meta FileMeta
	if err := StatAt(fix.linkBuf, fix.linkLen, &meta); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := StatAt(fix.linkBuf, fix.linkLen, &meta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSStatSymlink(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	if _, err := os.Stat(fix.linkPath); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := os.Stat(fix.linkPath); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLstatJoin(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	var meta FileMeta
	if err := LstatJoin(fix.joinBuf, fix.parentLen, fix.nameLen, &meta); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		fix.repackJoin()
		if err := LstatJoin(fix.joinBuf, fix.parentLen, fix.nameLen, &meta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSLstatJoin(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	joined := filepath.Join(fix.dir, fix.fileName)
	if _, err := os.Lstat(joined); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := os.Lstat(joined); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStatJoin(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	var meta FileMeta
	if err := StatJoin(fix.joinBuf, fix.parentLen, fix.nameLen, &meta); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		fix.repackJoin()
		if err := StatJoin(fix.joinBuf, fix.parentLen, fix.nameLen, &meta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOSStatJoin(b *testing.B) {
	fix := statBenchFixtureSetup(b)
	joined := filepath.Join(fix.dir, fix.fileName)
	if _, err := os.Stat(joined); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := os.Stat(joined); err != nil {
			b.Fatal(err)
		}
	}
}
