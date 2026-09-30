//go:build !race

package parallelwalkdir

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"
	"sync"
	"testing"
)

const catalogHashLookupsPerWalk = 3314

var (
	benchCatalogLookupNames     [][]byte
	benchCatalogLookupNamesOnce sync.Once
	// catalogWalkStdlibHasherSink forces each fnv.New128a to escape so AllocsPerRun
	// matches discovery-scale catalog lookup cost under default compiler opts.
	catalogWalkStdlibHasherSink []hash.Hash
)

// hashPathBytesStdlibCatalogWalk models pre-refactor HashPathBytes: heap hasher per
// basename plus Sum(nil) allocation. Used in allocs tests and catalog-walk benchmarks.
//
//go:noinline
func hashPathBytesStdlibCatalogWalk(path []byte) PathHash {
	h := fnv.New128a()
	catalogWalkStdlibHasherSink = append(catalogWalkStdlibHasherSink, h)
	_, _ = h.Write(path)
	sum := h.Sum(nil)
	r := PathHash{
		Hi: binary.BigEndian.Uint64(sum[0:8]),
		Lo: binary.BigEndian.Uint64(sum[8:16]),
	}
	hashBenchBlackhole = r
	return r
}

func benchCatalogLookupNames3314() [][]byte {
	benchCatalogLookupNamesOnce.Do(func() {
		if names := discoveryWalkFileBasenamesFromEnv(); len(names) == catalogHashLookupsPerWalk {
			benchCatalogLookupNames = names
			return
		}
		benchCatalogLookupNames = make([][]byte, catalogHashLookupsPerWalk)
		for i := range benchCatalogLookupNames {
			benchCatalogLookupNames[i] = []byte(fmt.Sprintf("img_%04d.jpg", i))
		}
	})
	return benchCatalogLookupNames
}

func TestHashPathBytes_catalogWalkBatchAllocs(t *testing.T) {
	names := benchCatalogLookupNames3314()
	if len(names) != catalogHashLookupsPerWalk {
		t.Fatalf("names=%d want %d", len(names), catalogHashLookupsPerWalk)
	}
	warmCatalogHashBatch(names)
	catalogWalkStdlibHasherSink = catalogWalkStdlibHasherSink[:0]
	inlineAllocs := testing.AllocsPerRun(5, func() {
		catalogHashBatch(names, HashPathBytes)
	})
	catalogWalkStdlibHasherSink = catalogWalkStdlibHasherSink[:0]
	stdlibAllocs := testing.AllocsPerRun(5, func() {
		catalogHashBatch(names, hashPathBytesStdlibCatalogWalk)
	})
	if stdlibAllocs <= inlineAllocs {
		t.Fatalf("stdlib allocs/op=%v inline=%v (want stdlib > inline)", stdlibAllocs, inlineAllocs)
	}
	const minStdlibAllocsPerLookup = 1.0
	if stdlibAllocs < minStdlibAllocsPerLookup*float64(len(names)) {
		t.Fatalf("stdlib allocs/op=%v want at least %v (~1 alloc per fnv.New128a/Sum per lookup)",
			stdlibAllocs, minStdlibAllocsPerLookup*float64(len(names)))
	}
	t.Logf("%d lookups: inline %v allocs/op; stdlib %v allocs/op", len(names), inlineAllocs, stdlibAllocs)
}

//go:noinline
func catalogHashBatch(names [][]byte, hash func([]byte) PathHash) {
	for _, name := range names {
		hashBenchBlackhole = hash(name)
	}
}

func warmCatalogHashBatch(names [][]byte) {
	for range 3 {
		catalogHashBatch(names, HashPathBytes)
		catalogWalkStdlibHasherSink = catalogWalkStdlibHasherSink[:0]
		catalogHashBatch(names, hashPathBytesStdlibCatalogWalk)
	}
}

// BenchmarkHashPathBytes_catalogWalkPerIter hashes 3314 basenames per iteration.
// stdlib uses hashPathBytesStdlibCatalogWalk (heap hasher + Sum(nil)); inline uses bits.Mul64.
//
//	go test ./internal/parallelwalkdir -bench=BenchmarkHashPathBytes_catalogWalkPerIter -benchmem -run=^$ -count=5

func BenchmarkHashPathBytes_catalogWalkPerIter_inline(b *testing.B) {
	benchmarkHashPathBytesCatalogWalkPerIter(b, HashPathBytes, false)
}

func BenchmarkHashPathBytes_catalogWalkPerIter_stdlib(b *testing.B) {
	benchmarkHashPathBytesCatalogWalkPerIter(b, hashPathBytesStdlibCatalogWalk, true)
}

func benchmarkHashPathBytesCatalogWalkPerIter(b *testing.B, hash func([]byte) PathHash, resetStdlibSink bool) {
	names := benchCatalogLookupNames3314()
	keys := make([]PathHash, len(names))
	b.ReportAllocs()
	b.ReportMetric(float64(len(names)), "lookups/iter")
	for b.Loop() {
		if resetStdlibSink {
			catalogWalkStdlibHasherSink = catalogWalkStdlibHasherSink[:0]
		}
		for i, name := range names {
			h := hash(name)
			keys[i] = h
			hashBenchBlackhole = h
		}
	}
	_ = keys
}
