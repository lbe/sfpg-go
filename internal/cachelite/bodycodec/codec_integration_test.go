package bodycodec_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/fixtures"
)

func TestRegisterDefaults_roundtripZstd(t *testing.T) {
	r := bodycodec.NewRegistry()
	if err := bodycodec.RegisterDefaults(r); err != nil {
		t.Fatal(err)
	}
	src := []byte(strings.Repeat("<p>x</p>", 512))
	stored, err := r.EncodeWith("zstd-1", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) >= len(src) {
		t.Fatal("expected compression to shrink payload")
	}
	plain, err := r.DecodeForServe(stored, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, src) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestRegisterDefaults_roundtripGzip(t *testing.T) {
	r := bodycodec.NewRegistry()
	if err := bodycodec.RegisterDefaults(r); err != nil {
		t.Fatal(err)
	}
	src := []byte(strings.Repeat("<p>x</p>", 512))
	stored, err := r.EncodeWith("gzip-6", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) >= len(src) {
		t.Fatal("expected compression to shrink payload")
	}
	plain, err := r.DecodeForServe(stored, len(src))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plain, src) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestConcurrentRoundtripZstd(t *testing.T) {
	data, err := fixtures.Read("gallery_med_1.html")
	if err != nil {
		t.Fatal(err)
	}
	r := bodycodec.NewRegistry()
	if err := bodycodec.RegisterDefaults(r); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				stored, err := r.EncodeWith("zstd-1", data)
				if err != nil {
					t.Error(err)
					return
				}
				plain, err := r.DecodeForServe(stored, len(data))
				if err != nil {
					t.Error(err)
					return
				}
				if !bytes.Equal(plain, data) {
					t.Error("roundtrip mismatch")
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestConcurrentRoundtripGzip(t *testing.T) {
	data, err := fixtures.Read("gallery_med_1.html")
	if err != nil {
		t.Fatal(err)
	}
	r := bodycodec.NewRegistry()
	if err := bodycodec.RegisterDefaults(r); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				stored, err := r.EncodeWith("gzip-6", data)
				if err != nil {
					t.Error(err)
					return
				}
				plain, err := r.DecodeForServe(stored, len(data))
				if err != nil {
					t.Error(err)
					return
				}
				if !bytes.Equal(plain, data) {
					t.Error("roundtrip mismatch")
					return
				}
			}
		})
	}
	wg.Wait()
}
