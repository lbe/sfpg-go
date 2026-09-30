package osza_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lbe/sfpg-go/internal/osza"
)

func statFacadeBenchSetup(b *testing.B) ([]byte, int) {
	b.Helper()
	dir := b.TempDir()
	filePath := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(filePath, []byte("hello"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		b.Fatal(err)
	}

	pathBuf := make([]byte, len(filePath)+1)
	copy(pathBuf, filePath)
	return pathBuf, len(filePath)
}

func BenchmarkFacadeLstatAt(b *testing.B) {
	pathBuf, pathLen := statFacadeBenchSetup(b)
	var meta osza.FileMeta
	if err := osza.LstatAt(pathBuf, pathLen, &meta); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := osza.LstatAt(pathBuf, pathLen, &meta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFacadeOSLstat(b *testing.B) {
	pathBuf, pathLen := statFacadeBenchSetup(b)
	filePath := string(pathBuf[:pathLen])
	if _, err := os.Lstat(filePath); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := os.Lstat(filePath); err != nil {
			b.Fatal(err)
		}
	}
}
