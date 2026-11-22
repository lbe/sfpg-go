package files

import (
	"context"
	"database/sql"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/metadata"
	"github.com/lbe/sfpg-go/internal/thumbnail"
)

func TestProcessFileContents(t *testing.T) {
	ctx := context.Background()
	extractor := metadata.New()
	t.Cleanup(func() { _ = extractor.Close() })
	generators := thumbnail.NewPool()
	t.Cleanup(generators.Close)
	t.Run("non-image file", func(t *testing.T) {
		td := t.TempDir()
		fn := filepath.Join(td, "not-image.txt")
		if err := os.WriteFile(fn, []byte("hello world"), 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		file := &File{ImagesDir: td, Path: "not-image.txt", File: File{}.File}
		err := processFileContents(ctx, file, extractor, generators)
		if err == nil {
			t.Fatal("expected non-image error, got nil")
		}
		if !strings.Contains(err.Error(), "non-image") {
			t.Errorf("expected non-image error, got: %v", err)
		}
	})

	t.Run("file open error", func(t *testing.T) {
		file := &File{ImagesDir: "/does/not/exist", Path: "missing.jpg", File: File{}.File}
		err := processFileContents(ctx, file, extractor, generators)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("valid image", func(t *testing.T) {
		td := t.TempDir()
		fn := filepath.Join(td, "valid.png")
		f, err := os.Create(fn)
		if err != nil {
			t.Fatalf("create temp file: %v", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		if encErr := png.Encode(f, img); encErr != nil {
			f.Close()
			t.Fatalf("encode png: %v", encErr)
		}
		if closeErr := f.Close(); closeErr != nil {
			t.Fatalf("close: %v", closeErr)
		}
		info, err := os.Stat(fn)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		file := &File{
			ImagesDir: td,
			Path:      "valid.png",
			File:      gallerydb.File{SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true}},
		}
		if err := processFileContents(ctx, file, extractor, generators); err != nil {
			t.Fatalf("processFileContents: %v", err)
		}
		if !file.File.Width.Valid || file.File.Width.Int64 != 4 {
			t.Errorf("Width = %v, want 4", file.File.Width)
		}
		if !file.File.Height.Valid || file.File.Height.Int64 != 4 {
			t.Errorf("Height = %v, want 4", file.File.Height)
		}
		if !file.File.Md5.Valid || file.File.Md5.String == "" {
			t.Error("expected Md5 populated")
		}
		if !file.File.Phash.Valid {
			t.Error("expected Phash populated")
		}
		if file.Thumbnail == nil || file.Thumbnail.Len() == 0 {
			t.Error("expected Thumbnail populated")
		}
	})

	t.Run("decode config error", func(t *testing.T) {
		td := t.TempDir()
		fn := filepath.Join(td, "corrupt.png")
		// PNG magic followed by invalid chunks.
		if err := os.WriteFile(fn, []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0xFF, 0xFF}, 0o644); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		info, err := os.Stat(fn)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}

		file := &File{
			ImagesDir: td,
			Path:      "corrupt.png",
			File:      gallerydb.File{SizeBytes: sql.NullInt64{Int64: info.Size(), Valid: true}},
		}
		err = processFileContents(ctx, file, extractor, generators)
		if err == nil {
			t.Fatal("expected decode error")
		}
	})
}
