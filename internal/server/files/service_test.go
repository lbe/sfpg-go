package files

import (
	"context"
	"errors"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// discoveryFields returns the mtime and size a walk reports for the file at fullPath.
func discoveryFields(t *testing.T, fullPath string) (mtime, size int64) {
	t.Helper()
	info, err := os.Stat(fullPath)
	if err != nil {
		t.Fatalf("stat %s: %v", fullPath, err)
	}
	return info.ModTime().Unix(), info.Size()
}

func TestFileProcessor_ProcessDiscoveryFile(t *testing.T) {
	t.Run("cancelled context", func(t *testing.T) {
		processor := NewFileProcessor(t.TempDir())
		t.Cleanup(func() { _ = processor.Close() })

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := processor.ProcessDiscoveryFile(ctx, "test.jpg", 1, 1)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("empty file path", func(t *testing.T) {
		processor := NewFileProcessor(t.TempDir())
		t.Cleanup(func() { _ = processor.Close() })

		if _, err := processor.ProcessDiscoveryFile(context.Background(), "", 1, 1); err == nil {
			t.Fatal("expected error for empty path")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		processor := NewFileProcessor(t.TempDir())
		t.Cleanup(func() { _ = processor.Close() })

		_, err := processor.ProcessDiscoveryFile(context.Background(), "missing.jpg", 1, 1)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		if got := InvalidReason(err); got != "open" {
			t.Errorf("InvalidReason = %q, want %q", got, "open")
		}
	})

}

// TestFileProcessor_ProcessDiscoveryFileSucceeds processes a small real JPEG.
func TestFileProcessor_ProcessDiscoveryFileSucceeds(t *testing.T) {
	imagesDir := t.TempDir()
	processor := NewFileProcessor(imagesDir)
	t.Cleanup(func() { _ = processor.Close() })

	path := createTestImage(t, imagesDir, "process-discovery-success.jpg")
	mtime, size := discoveryFields(t, filepath.Join(imagesDir, path))

	file, err := processor.ProcessDiscoveryFile(context.Background(), path, mtime, size)
	if err != nil {
		t.Fatalf("ProcessDiscoveryFile: %v", err)
	}
	if file.ImagesDir != imagesDir || file.Path != path {
		t.Errorf("ImagesDir, Path = %q, %q; want %q, %q", file.ImagesDir, file.Path, imagesDir, path)
	}
	if !file.File.Mtime.Valid || file.File.Mtime.Int64 != mtime {
		t.Errorf("Mtime = %+v, want %d", file.File.Mtime, mtime)
	}
	if !file.File.SizeBytes.Valid || file.File.SizeBytes.Int64 != size {
		t.Errorf("SizeBytes = %+v, want %d", file.File.SizeBytes, size)
	}
	if file.File.MimeType.String != "image/jpeg" {
		t.Errorf("MimeType = %q, want image/jpeg", file.File.MimeType.String)
	}
	if !file.File.Width.Valid || file.File.Width.Int64 != 1 || !file.File.Height.Valid || file.File.Height.Int64 != 1 {
		t.Errorf("dimensions = %+v x %+v, want 1 x 1", file.File.Width, file.File.Height)
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
}

// TestFileProcessor_Close verifies Close returns nil, also when called twice.
func TestFileProcessor_Close(t *testing.T) {
	t.Run("close returns nil", func(t *testing.T) {
		processor := NewFileProcessor(t.TempDir())

		if err := processor.Close(); err != nil {
			t.Errorf("Close returned error: %v", err)
		}
	})

	t.Run("multiple close is safe", func(t *testing.T) {
		processor := NewFileProcessor(t.TempDir())

		err1 := processor.Close()
		err2 := processor.Close()

		if err1 != nil {
			t.Errorf("First Close returned error: %v", err1)
		}
		if err2 != nil {
			t.Errorf("Second Close returned error: %v", err2)
		}
	})
}

// createTestImage writes a minimal 1x1 JPEG at dir/name and returns the relative path.
func createTestImage(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create test image: %v", err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		f.Close()
		t.Fatalf("encode test image: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close test image: %v", err)
	}
	return name
}

// TestFileProcessor_ProcessDiscoveryFileReadsMetadata processes a real photo
// with EXIF, IPTC and XMP and checks what the processor fills in.
func TestFileProcessor_ProcessDiscoveryFileReadsMetadata(t *testing.T) {
	imagesDir := t.TempDir()
	input, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "Metadata_test_file_-_includes_data_in_IIM,_XMP,_and_Exif.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(imagesDir, "photo.jpg"), input, 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewFileProcessor(imagesDir)
	t.Cleanup(func() { _ = p.Close() })

	f, err := p.ProcessDiscoveryFile(context.Background(), "photo.jpg", 1700000000, int64(len(input)))
	if err != nil {
		t.Fatal(err)
	}
	if f.File.MimeType.String != "image/jpeg" {
		t.Errorf("MimeType = %q, want image/jpeg", f.File.MimeType.String)
	}
	if f.Metadata.Exif.CameraMake != "samsung" {
		t.Errorf("CameraMake = %q, want samsung", f.Metadata.Exif.CameraMake)
	}
	if f.File.Md5.String == "" || f.File.Phash.Int64 == 0 || f.Thumbnail == nil || f.Thumbnail.Len() == 0 {
		t.Errorf("md5 %q, phash %d, thumbnail set %v: want all set", f.File.Md5.String, f.File.Phash.Int64, f.Thumbnail != nil)
	}
}

// TestFileProcessor_PoisonJPEGIsDecodeFailure checks that a file with a JPEG
// signature and a broken body fails with reason decode.
func TestFileProcessor_PoisonJPEGIsDecodeFailure(t *testing.T) {
	imagesDir := t.TempDir()
	data := []byte{0xFF, 0xD8, 0xFF, 0x00}
	if err := os.WriteFile(filepath.Join(imagesDir, "poison.jpg"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	p := NewFileProcessor(imagesDir)
	t.Cleanup(func() { _ = p.Close() })

	_, err := p.ProcessDiscoveryFile(context.Background(), "poison.jpg", 1, int64(len(data)))
	if err == nil {
		t.Fatal("ProcessDiscoveryFile accepted a broken JPEG")
	}
	if got := InvalidReason(err); got != "decode" {
		t.Errorf("InvalidReason = %q, want decode (err %v)", got, err)
	}
}
