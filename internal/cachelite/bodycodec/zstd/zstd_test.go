package zstd

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/fixtures"
)

func TestConcurrentCompress_MedFixture(t *testing.T) {
	data, err := fixtures.Read("gallery_med_1.html")
	if err != nil {
		t.Fatalf("read gallery_med_1.html: %v", err)
	}
	c := NewCodec()
	const goroutines = 32
	const iters = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				compressed, err := c.Compress(data)
				if err != nil {
					t.Error(err)
					return
				}
				plain, err := c.Decompress(compressed, len(data))
				if err != nil {
					t.Error(err)
					return
				}
				if !bytes.Equal(plain, data) {
					t.Error("roundtrip mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestRoundtrip(t *testing.T) {
	c := NewCodec()

	t.Run("repeated_html", func(t *testing.T) {
		src := []byte(strings.Repeat("<p>x</p>\n", 1024))
		compressed, err := c.Compress(src)
		if err != nil {
			t.Fatal(err)
		}
		if len(compressed) >= len(src) {
			t.Fatal("expected compression to shrink payload")
		}
		plain, err := c.Decompress(compressed, len(src))
		if err != nil {
			t.Fatal(err)
		}
		if string(plain) != string(src) {
			t.Fatal("roundtrip mismatch")
		}
	})

	t.Run("gallery_med_1", func(t *testing.T) {
		src, err := fixtures.Read("gallery_med_1.html")
		if err != nil {
			t.Fatalf("read gallery_med_1.html: %v", err)
		}
		compressed, err := c.Compress(src)
		if err != nil {
			t.Fatal(err)
		}
		if len(compressed) >= len(src) {
			t.Fatal("expected compression to shrink payload")
		}
		plain, err := c.Decompress(compressed, len(src))
		if err != nil {
			t.Fatal(err)
		}
		if string(plain) != string(src) {
			t.Fatal("roundtrip mismatch")
		}
	})
}
