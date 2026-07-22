package bodycodec_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	gziplib "compress/gzip"

	zstdlib "github.com/klauspost/compress/zstd"
	_ "github.com/lbe/sfpg-go/internal/cachelite/bodycodec"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/fixtures"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/gzip"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/zstd"
)

var benchFixtures = []string{
	"gallery_small_1.html", "gallery_small_2.html", "gallery_small_3.html",
	"gallery_med_1.html", "gallery_med_2.html", "gallery_med_3.html",
	"gallery_large_1.html", "gallery_large_2.html", "gallery_large_3.html",
}

var (
	pooledZstd = zstd.NewCodec()
	pooledGzip = gzip.NewCodec()
)

func loadFixture(b *testing.B, name string) []byte {
	b.Helper()
	data, err := fixtures.Read(name)
	if err != nil {
		b.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// Alloc helpers mirror production zstd codec options (concurrency=1) so G1
// pooled-vs-alloc compares like-for-like. Do not use NewWriter/NewReader defaults.
func compressAllocZstd(src []byte) ([]byte, error) {
	enc, err := zstdlib.NewWriter(nil,
		zstdlib.WithEncoderLevel(zstdlib.SpeedFastest),
		zstdlib.WithEncoderConcurrency(1),
		zstdlib.WithLowerEncoderMem(true),
	)
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	return enc.EncodeAll(src, nil), nil
}

func decompressAllocZstd(src []byte) ([]byte, error) {
	dec, err := zstdlib.NewReader(nil, zstdlib.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	return dec.DecodeAll(src, nil)
}

func compressAllocGzip(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gziplib.NewWriterLevel(&buf, 6)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(src); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressAllocGzip(src []byte) ([]byte, error) {
	zr, err := gziplib.NewReader(bytes.NewReader(src))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func BenchmarkCompressPooled(b *testing.B) {
	for _, codecName := range []string{"zstd", "gzip"} {
		for _, fixture := range benchFixtures {
			for _, mode := range []string{"Serial", "Parallel8", "ParallelCPUs"} {
				b.Run(codecName+"/"+strings.TrimSuffix(fixture, ".html")+"/"+mode, func(b *testing.B) {
					plain := loadFixture(b, fixture)
					b.SetBytes(int64(len(plain)))
					run := func() {
						switch codecName {
						case "zstd":
							if _, err := pooledZstd.Compress(plain); err != nil {
								panic(err)
							}
						case "gzip":
							if _, err := pooledGzip.Compress(plain); err != nil {
								panic(err)
							}
						}
					}
					switch mode {
					case "Serial":
						for b.Loop() {
							run()
						}
					case "Parallel8":
						b.SetParallelism(8)
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					case "ParallelCPUs":
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					}
				})
			}
		}
	}
}

func BenchmarkCompressAlloc(b *testing.B) {
	for _, codecName := range []string{"zstd", "gzip"} {
		for _, fixture := range benchFixtures {
			for _, mode := range []string{"Serial", "Parallel8", "ParallelCPUs"} {
				b.Run(codecName+"/"+strings.TrimSuffix(fixture, ".html")+"/"+mode, func(b *testing.B) {
					plain := loadFixture(b, fixture)
					b.SetBytes(int64(len(plain)))
					run := func() {
						switch codecName {
						case "zstd":
							if _, err := compressAllocZstd(plain); err != nil {
								panic(err)
							}
						case "gzip":
							if _, err := compressAllocGzip(plain); err != nil {
								panic(err)
							}
						}
					}
					switch mode {
					case "Serial":
						for b.Loop() {
							run()
						}
					case "Parallel8":
						b.SetParallelism(8)
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					case "ParallelCPUs":
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					}
				})
			}
		}
	}
}

func BenchmarkDecompressPooled(b *testing.B) {
	for _, codecName := range []string{"zstd", "gzip"} {
		for _, fixture := range benchFixtures {
			for _, mode := range []string{"Serial", "Parallel8", "ParallelCPUs"} {
				b.Run(codecName+"/"+strings.TrimSuffix(fixture, ".html")+"/"+mode, func(b *testing.B) {
					plain := loadFixture(b, fixture)
					b.SetBytes(int64(len(plain)))
					var compressed []byte
					var err error
					switch codecName {
					case "zstd":
						compressed, err = pooledZstd.Compress(plain)
					case "gzip":
						compressed, err = pooledGzip.Compress(plain)
					}
					if err != nil {
						b.Fatal(err)
					}
					uncompressedLen := len(plain)
					run := func() {
						switch codecName {
						case "zstd":
							if _, err := pooledZstd.Decompress(compressed, uncompressedLen); err != nil {
								panic(err)
							}
						case "gzip":
							if _, err := pooledGzip.Decompress(compressed, uncompressedLen); err != nil {
								panic(err)
							}
						}
					}
					switch mode {
					case "Serial":
						for b.Loop() {
							run()
						}
					case "Parallel8":
						b.SetParallelism(8)
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					case "ParallelCPUs":
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					}
				})
			}
		}
	}
}

func BenchmarkDecompressAlloc(b *testing.B) {
	for _, codecName := range []string{"zstd", "gzip"} {
		for _, fixture := range benchFixtures {
			for _, mode := range []string{"Serial", "Parallel8", "ParallelCPUs"} {
				b.Run(codecName+"/"+strings.TrimSuffix(fixture, ".html")+"/"+mode, func(b *testing.B) {
					plain := loadFixture(b, fixture)
					b.SetBytes(int64(len(plain)))
					var compressed []byte
					var err error
					switch codecName {
					case "zstd":
						compressed, err = pooledZstd.Compress(plain)
					case "gzip":
						compressed, err = pooledGzip.Compress(plain)
					}
					if err != nil {
						b.Fatal(err)
					}
					run := func() {
						switch codecName {
						case "zstd":
							if _, err := decompressAllocZstd(compressed); err != nil {
								panic(err)
							}
						case "gzip":
							if _, err := decompressAllocGzip(compressed); err != nil {
								panic(err)
							}
						}
					}
					switch mode {
					case "Serial":
						for b.Loop() {
							run()
						}
					case "Parallel8":
						b.SetParallelism(8)
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					case "ParallelCPUs":
						b.RunParallel(func(pb *testing.PB) {
							for pb.Next() {
								run()
							}
						})
					}
				})
			}
		}
	}
}
