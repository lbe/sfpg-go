package bodycodec_test

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/fixtures"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/htmlsniff"
)

func profileRegistry(b *testing.B) *bodycodec.Registry {
	b.Helper()
	r := bodycodec.NewRegistry()
	if err := bodycodec.RegisterDefaults(r); err != nil {
		b.Fatal(err)
	}
	return r
}

func loadProfileFixture(b *testing.B, name string) []byte {
	b.Helper()
	data, err := fixtures.Read(name)
	if err != nil {
		b.Fatalf("read %s: %v", name, err)
	}
	return data
}

func BenchmarkProfileEncodeWithZstdLarge(b *testing.B) {
	plain := loadProfileFixture(b, "gallery_large_1.html")
	r := profileRegistry(b)
	b.SetBytes(int64(len(plain)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.EncodeWith("zstd-1", plain); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProfileDecodeForServeZstdLarge(b *testing.B) {
	plain := loadProfileFixture(b, "gallery_large_1.html")
	r := profileRegistry(b)
	stored, err := r.EncodeWith("zstd-1", plain)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(plain)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.DecodeForServe(stored, len(plain)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProfileDecodeForServePlainLarge(b *testing.B) {
	plain := loadProfileFixture(b, "gallery_large_1.html")
	r := profileRegistry(b)
	b.SetBytes(int64(len(plain)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := r.DecodeForServe(plain, len(plain)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProfileMatchZstd(b *testing.B) {
	plain := loadProfileFixture(b, "gallery_large_1.html")
	r := profileRegistry(b)
	stored, err := r.EncodeWith("zstd-1", plain)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if r.Match(stored) == nil {
			b.Fatal("expected zstd match")
		}
	}
}

func BenchmarkProfileHTMLSniffLarge(b *testing.B) {
	plain := loadProfileFixture(b, "gallery_large_1.html")
	// LooksLikeHTML scans at most htmlsniff.HTMLScanLimit (4 KiB) — do not SetBytes(len(plain)).
	b.ReportAllocs()
	for b.Loop() {
		if !htmlsniff.LooksLikeHTML(plain) {
			b.Fatal("expected html")
		}
	}
}

func BenchmarkProfileRoundtripZstdLarge(b *testing.B) {
	plain := loadProfileFixture(b, "gallery_large_1.html")
	r := profileRegistry(b)
	b.SetBytes(int64(len(plain)))
	b.ReportAllocs()
	for b.Loop() {
		stored, err := r.EncodeWith("zstd-1", plain)
		if err != nil {
			b.Fatal(err)
		}
		out, err := r.DecodeForServe(stored, len(plain))
		if err != nil {
			b.Fatal(err)
		}
		if len(out) != len(plain) {
			b.Fatal("length mismatch")
		}
	}
}
