package fixtures_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/cachelite/bodycodec/fixtures"
)

func TestRead_extractsGalleryFixture(t *testing.T) {
	data, err := fixtures.Read("gallery_small_1.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty fixture")
	}
	if !strings.Contains(string(data), "<") {
		t.Fatal("expected HTML-like fixture content")
	}
}

func TestRead_invalidName(t *testing.T) {
	_, err := fixtures.Read("../escape.html")
	if err == nil {
		t.Fatal("expected error for invalid fixture name")
	}
}

func TestRead_markerPresentAfterExtract(t *testing.T) {
	if _, err := fixtures.Read("gallery_small_2.html"); err != nil {
		t.Fatal(err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	marker := filepath.Join(filepath.Dir(file), "..", "testdata", ".galleries-extracted")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected extraction marker: %v", err)
	}
}
