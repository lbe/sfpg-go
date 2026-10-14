package bodycodec_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec"
	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/testfixture"
)

func TestRegisterDefaults_roundtrip(t *testing.T) {
	t.Parallel()
	src := []byte(strings.Repeat("<p>x</p>", 512))
	tests := []struct {
		name    string
		codecID string
	}{
		{name: "zstd", codecID: "zstd-1"},
		{name: "gzip", codecID: "gzip-6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := bodycodec.NewRegistry()
			if err := bodycodec.RegisterDefaults(r); err != nil {
				t.Fatal(err)
			}
			stored, err := r.EncodeWith(tt.codecID, src)
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
		})
	}
}

func TestConcurrentRoundtripZstd(t *testing.T) {
	t.Parallel()
	data, err := testfixture.Read("gallery_med_1.html")
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
	t.Parallel()
	data, err := testfixture.Read("gallery_med_1.html")
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
