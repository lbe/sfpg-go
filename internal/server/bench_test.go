package server

import (
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"go.local/sfpg/internal/server/files"
)

// BenchmarkRemoveImagesDirPrefix measures path normalization performance
// with the cached normalized path optimization.
func BenchmarkRemoveImagesDirPrefix(b *testing.B) {
	normalizedImagesDir := "Images"
	path := "Images/gallery/subfolder/photo.jpg"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = removeImagesDirPrefix(normalizedImagesDir, path)
	}
}

// BenchmarkRemoveImagesDirPrefix_WithFilepathJoin benchmarks the cost
// of constructing paths with filepath.Join vs using pre-normalized paths.
func BenchmarkRemoveImagesDirPrefix_WithFilepathJoin(b *testing.B) {
	normalizedImagesDir := "Images"

	b.Run("PreNormalized", func(b *testing.B) {
		path := "Images/gallery/subfolder/photo.jpg"
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = removeImagesDirPrefix(normalizedImagesDir, path)
		}
	})

	b.Run("WithJoin", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			path := filepath.Join("Images", "gallery", "subfolder", "photo.jpg")
			_, _ = removeImagesDirPrefix(normalizedImagesDir, path)
		}
	})
}

// BenchmarkFileOpen measures file open/close performance for MIME detection.
func BenchmarkFileOpen(b *testing.B) {
	// Create a temporary JPEG file for testing
	tempDir := b.TempDir()
	testFile := filepath.Join(tempDir, "test.jpg")

	// Create a simple 1x1 JPEG
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	f, err := os.Create(testFile)
	if err != nil {
		b.Fatalf("Failed to create test file: %v", err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		b.Fatalf("Failed to encode JPEG: %v", err)
	}
	f.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ff, err := os.Open(testFile)
		if err != nil {
			b.Fatalf("Failed to open file: %v", err)
		}
		// Read first 512 bytes (simulating MIME detection)
		buf := make([]byte, 512)
		_, _ = ff.Read(buf)
		ff.Close()
	}
}

// BenchmarkIsImageFile measures image file extension checking performance.
func BenchmarkIsImageFile(b *testing.B) {
	paths := []string{
		"/path/to/image.jpg",
		"/path/to/image.png",
		"/path/to/image.gif",
		"/path/to/image.webp",
		"/path/to/document.pdf",
		"/path/to/video.mp4",
		"/path/to/archive.zip",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, path := range paths {
			_ = files.IsImageFile(path)
		}
	}
}

// BenchmarkPathOperations compares different path manipulation approaches.
func BenchmarkPathOperations(b *testing.B) {
	b.Run("filepath.Join", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = filepath.Join("Images", "gallery", "photo.jpg")
		}
	})

	b.Run("filepath.ToSlash", func(b *testing.B) {
		path := filepath.Join("Images", "gallery", "photo.jpg")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = filepath.ToSlash(path)
		}
	})

	b.Run("CachedNormalization", func(b *testing.B) {
		// Simulates the optimization where we cache filepath.ToSlash(imagesDir)
		normalizedBase := filepath.ToSlash("Images")
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Just string concatenation after pre-normalization
			_ = normalizedBase + "/gallery/photo.jpg"
		}
	})
}
