package parallelwalkdir

import (
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"testing"
)

// Representative discovery basename and reported path lengths for bench input.
var (
	benchHashBasename  = []byte("DSC_2847.JPG")
	benchHashPath      = []byte("/gallery/2024/vacation/album/DSC_2847.JPG")
	hashBenchBlackhole PathHash
)

func hashPathBytesStdlibPreRefactor(path []byte) PathHash {
	h := fnv.New128a()
	_, _ = h.Write(path)
	sum := h.Sum(nil)
	return PathHash{
		Hi: binary.BigEndian.Uint64(sum[0:8]),
		Lo: binary.BigEndian.Uint64(sum[8:16]),
	}
}

func TestHashPathBytesEmpty(t *testing.T) {
	t.Parallel()
	want := PathHash{Hi: 0x6c62272e07bb0142, Lo: 0x62b821756295c58d}
	got := HashPathBytes(nil)
	if got != want {
		t.Fatalf("HashPathBytes(nil) = %+v, want %+v", got, want)
	}
	got = HashPathBytes([]byte{})
	if got != want {
		t.Fatalf("HashPathBytes(empty) = %+v, want %+v", got, want)
	}
}

func TestHashPathBytesVectorA(t *testing.T) {
	t.Parallel()
	// Single-byte input "a" (0x61); hex from hash/fnv.New128a (FNV-1a 128 reference).
	const wantHex = "d228cb696f1a8caf78912b704e4a8964"
	sum, err := hex.DecodeString(wantHex)
	if err != nil {
		t.Fatal(err)
	}
	want := PathHash{
		Hi: binary.BigEndian.Uint64(sum[0:8]),
		Lo: binary.BigEndian.Uint64(sum[8:16]),
	}
	got := HashPathBytes([]byte("a"))
	if got != want {
		t.Fatalf("HashPathBytes(\"a\") = %+v, want %+v (hex %s)", got, want, wantHex)
	}
}

func TestHashPathBytesDistinctPaths(t *testing.T) {
	t.Parallel()
	a := HashPathBytes([]byte("/photos/2024/a"))
	b := HashPathBytes([]byte("/photos/2024/b"))
	if a == b {
		t.Fatalf("expected different hashes for different paths, both %+v", a)
	}
}

func TestHashPathBytes_matchesStdlib(t *testing.T) {
	t.Parallel()
	for _, path := range [][]byte{nil, benchHashBasename, benchHashPath} {
		inline := HashPathBytes(path)
		stdlib := hashPathBytesStdlibPreRefactor(path)
		if inline != stdlib {
			t.Fatalf("path %q: inline %+v stdlib %+v", path, inline, stdlib)
		}
	}
}

// Single-input ns/op only; see fnv128_allocs_test.go for catalog-walk allocs/op vs stdlib.
//
//	go test ./internal/parallelwalkdir -bench=BenchmarkHashPathBytes_single -benchmem -run=^$ -count=5

func BenchmarkHashPathBytes_single_inline(b *testing.B) {
	benchmarkHashPathBytes(b, benchHashBasename, HashPathBytes)
}

func BenchmarkHashPathBytes_single_stdlib(b *testing.B) {
	benchmarkHashPathBytes(b, benchHashBasename, hashPathBytesStdlibPreRefactor)
}

func BenchmarkHashPathBytes_single_inline_longPath(b *testing.B) {
	benchmarkHashPathBytes(b, benchHashPath, HashPathBytes)
}

func BenchmarkHashPathBytes_single_stdlib_longPath(b *testing.B) {
	benchmarkHashPathBytes(b, benchHashPath, hashPathBytesStdlibPreRefactor)
}

func benchmarkHashPathBytes(b *testing.B, path []byte, hash func([]byte) PathHash) {
	b.ReportMetric(float64(len(path)), "bytes/input")
	var sink PathHash
	for b.Loop() {
		sink = hash(path)
	}
	b.SetBytes(int64(len(path)))
	_ = sink
}
