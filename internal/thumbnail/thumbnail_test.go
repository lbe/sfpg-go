package thumbnail_test

import (
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/thumbnail"
)

// createTestImage creates a dummy image file for testing.
func createTestImage(dir, filename string, width, height int) (string, error) {
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("failed to create test image %s: %w", path, err)
	}
	defer f.Close()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// Fill image with a color to make it non-uniform
	for x := range width {
		for y := range height {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 100, A: 255})
		}
	}

	switch filepath.Ext(filename) {
	case ".jpg", ".jpeg":
		err = jpeg.Encode(f, img, nil)
	case ".png":
		err = png.Encode(f, img)
	case ".gif":
		err = gif.Encode(f, img, nil)
	default:
		return "", fmt.Errorf("unsupported image type for test creation: %s", filepath.Ext(filename))
	}

	if err != nil {
		return "", fmt.Errorf("failed to encode test image %s: %w", path, err)
	}

	return path, nil
}

// createStaticGradientTestImage creates a deterministic gradient image for hash testing.
func createStaticGradientTestImage(t *testing.T, dir, filename string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create static test image %s: %v", path, err)
	}
	defer f.Close()

	width, height := 100, 50
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Use fixed colors for a deterministic gradient
	//nolint:govet // Alpha value is part of the color definition
	startColor := color.RGBA{R: 255, G: 0, B: 0, A: 255} // Red
	//nolint:govet // Alpha value is part of the color definition
	endColor := color.RGBA{R: 0, G: 0, B: 255, A: 255} // Blue

	for y := range height {
		for x := range width {
			ratio := float64(x) / float64(width-1)
			r := uint8(float64(startColor.R)*(1-ratio) + float64(endColor.R)*ratio)
			g := uint8(float64(startColor.G)*(1-ratio) + float64(endColor.G)*ratio)
			b := uint8(float64(startColor.B)*(1-ratio) + float64(endColor.B)*ratio)
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}

	if err := png.Encode(f, img); err != nil {
		t.Fatalf("failed to encode static test image: %v", err)
	}
	return path
}

// TestGenerateThumbnailAndHashes uses a table-driven approach to test the GenerateThumbnailAndHashes function.
// It covers successful generation for various image types (JPEG, PNG, GIF)
// and expected failure cases like file not found, invalid image data, and empty files.
func TestGenerateThumbnailAndHashes(t *testing.T) {
	tempDir := t.TempDir()

	testCases := []struct {
		name        string
		setup       func(t *testing.T) string // returns path
		expectedW   int
		expectedH   int
		expectErr   bool
		errContains string
	}{
		{
			name: "Valid JPEG",
			setup: func(t *testing.T) string {
				s, err := createTestImage(tempDir, "test.jpg", 400, 300)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}
				return s
			},
			expectedW: 200,
			expectedH: 150,
			expectErr: false,
		},
		{
			name: "Valid PNG",
			setup: func(t *testing.T) string {
				s, err := createTestImage(tempDir, "test.png", 300, 400)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}
				return s
			},
			expectedW: 112, // 150 * (300/400)
			expectedH: 150,
			expectErr: false,
		},
		{
			name: "Valid GIF",
			setup: func(t *testing.T) string {
				s, err := createTestImage(tempDir, "test.gif", 200, 200)
				if err != nil {
					t.Fatalf("failed to create test image: %v", err)
				}
				return s
			},
			expectedW: 150,
			expectedH: 150,
			expectErr: false,
		},
		// {
		// 	name: "File not found",
		// 	setup: func(t *testing.T) string {
		// 		return filepath.Join(tempDir, "nonexistent.jpg")
		// 	},
		// 	expectErr:   true,
		// 	errContains: "no such file or directory",
		// },
		{
			name: "Not an image file",
			setup: func(t *testing.T) string {
				path := filepath.Join(tempDir, "not_an_image.jpg")
				err := os.WriteFile(path, []byte("this is not an image"), 0o644)
				if err != nil {
					t.Fatalf("failed to create dummy file: %v", err)
				}
				return path
			},
			expectErr:   true,
			errContains: "image: unknown format", // image.Decode error
		},
		{
			name: "Empty file",
			setup: func(t *testing.T) string {
				path := filepath.Join(tempDir, "empty.jpg")
				f, err := os.Create(path)
				if err != nil {
					t.Fatalf("failed to create empty file: %v", err)
				}
				f.Close()
				return path
			},
			expectErr:   true,
			errContains: "image: unknown format", // image.Decode error on empty file
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			imagePath := tc.setup(t)
			file, err := os.Open(imagePath)
			if err != nil {
				t.Fatalf("failed to open image file: %v", err)
			}
			defer func() {
				if closeErr := file.Close(); closeErr != nil {
					slog.Error("failed to close image file", "error", err)
				}
			}()
			thumbBytesBuffer, md5, phash, err := thumbnail.GenerateThumbnailAndHashes(file)

			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected an error, but got nil")
				}
				if !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error to contain '%s', but got '%v'", tc.errContains, err)
				}
				if thumbBytesBuffer != nil {
					t.Error("expected nil thumbBytes on error")
				}
				if md5.Valid {
					t.Error("expected invalid md5 on error")
				}
				if phash.Valid {
					t.Error("expected invalid phash on error")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if thumbBytesBuffer.Len() == 0 {
				t.Fatal("generateThumbnail returned empty byte slice")
			}

			if !md5.Valid || md5.String == "" {
				t.Error("expected a valid, non-empty md5 hash")
			}

			if !phash.Valid || phash.Int64 == 0 {
				t.Error("expected a valid, non-zero phash")
			}

			// Decode the resulting thumbnail to check its dimensions
			thumbImg, format, err := image.Decode(thumbBytesBuffer)
			if err != nil {
				t.Fatalf("failed to decode generated thumbnail bytes: %v", err)
			}

			if format != "jpeg" {
				t.Errorf("expected thumbnail format to be jpeg, but got %s", format)
			}

			bounds := thumbImg.Bounds()
			if bounds.Dx() != tc.expectedW || bounds.Dy() != tc.expectedH {
				t.Errorf("expected thumbnail dimensions to be %dx%d, but got %dx%d", tc.expectedW, tc.expectedH, bounds.Dx(), bounds.Dy())
			}
			thumbnail.PutBytesBuffer(thumbBytesBuffer)
			thumbnail.PutNullInt64(phash)
			thumbnail.PutNullString(md5)
		})
	}

	t.Run("Valid static image with hash check", func(t *testing.T) {
		imagePath := createStaticGradientTestImage(t, tempDir, "static_gradient.png")
		expectedMD5 := "08d35dda2e5ab91773f4238f9b0120e7"
		// This value overflows int64, which is the expected behavior based on the user's direction.
		// Updated 2025-12-26: New value reflects the deterministic integer-arithmetic grayscale conversion
		// that ensures consistent pHash results between x86-64 and ARM64 architectures.
		expectedPHashInt64 := int64(-2954361355555045376)

		file, err := os.Open(imagePath)
		if err != nil {
			t.Fatalf("failed to open image file: %v", err)
		}
		defer func() {
			if closeErr := file.Close(); closeErr != nil {
				slog.Error("failed to close image file", "error", err)
			}
		}()

		_, md5, phash, err := thumbnail.GenerateThumbnailAndHashes(file)
		if err != nil {
			t.Fatalf("GenerateThumbnailAndHashes failed: %v", err)
		}

		if !md5.Valid || md5.String != expectedMD5 {
			t.Errorf("MD5 mismatch:\nGot:  %q\nWant: %q", md5.String, expectedMD5)
		}
		// Compare against the overflowed int64 value
		if !phash.Valid || phash.Int64 != expectedPHashInt64 {
			t.Errorf("pHash mismatch:\nGot:  %d\nWant: %d", phash.Int64, expectedPHashInt64)
		}
	})
}

// BenchmarkGenerateThumbnailAndHashes benchmarks the GenerateThumbnailAndHashes function.
func BenchmarkGenerateThumbnailAndHashes(b *testing.B) {
	tempDir := b.TempDir()
	imagePath, err := createTestImage(tempDir, "benchmark.jpg", 1920, 1080)
	if err != nil {
		b.Fatalf("failed to create test image: %v", err)
	}

	file, err := os.Open(imagePath)
	if err != nil {
		b.Fatalf("failed to open image file: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("failed to close image file", "error", err)
		}
	}()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, err := thumbnail.GenerateThumbnailAndHashes(file)
		if err != nil {
			b.Fatalf("GenerateThumbnailAndHashes failed: %v", err)
		}
	}
}

// // BenchmarkGenerateThumbnailAlt1 benchmarks the GenerateThumbnailAlt1 function.
// func BenchmarkGenerateThumbnailAlt1(b *testing.B) {
// 	tempDir := b.TempDir()
// 	imagePath, err := createTestImage(tempDir, "benchmark.jpg", 1920, 1080)
// 	if err != nil {
// 		b.Fatalf("failed to create test image: %v", err)
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, err := thumbnail.GenerateThumbnailAlt1(imagePath)
// 		if err != nil {
// 			b.Fatalf("GenerateThumbnailAlt1 failed: %v", err)
// 		}
// 	}
// }

// // BenchmarkGenerateThumbnailAlt2 benchmarks the GenerateThumbnailAlt2 function.
// func BenchmarkGenerateThumbnailAlt2(b *testing.B) {
// 	tempDir := b.TempDir()
// 	imagePath, err := createTestImage(tempDir, "benchmark.jpg", 1920, 1080)
// 	if err != nil {
// 		b.Fatalf("failed to create test image: %v", err)
// 	}

// 	b.ResetTimer()
// 	for i := 0; i < b.N; i++ {
// 		_, err := thumbnail.GenerateThumbnailAlt2(imagePath)
// 		if err != nil {
// 			b.Fatalf("GenerateThumbnailAlt2 failed: %v", err)
// 		}
// 	}
// }

func TestThumbnailPools(t *testing.T) {
	t.Run("ImagePhash64", func(t *testing.T) {
		p := thumbnail.GetImagePhash64()
		if p == nil {
			t.Fatal("expected a pointer to a phash, but got nil")
		}
		thumbnail.PutImagePhash64(p)
	})
	t.Run("NullString", func(t *testing.T) {
		ns := thumbnail.GetNullString()
		if ns == nil {
			t.Fatal("expected a pointer to a NullString, but got nil")
		}
		thumbnail.PutNullString(ns)
	})
	t.Run("NullInt64", func(t *testing.T) {
		ni := thumbnail.GetNullInt64()
		if ni == nil {
			t.Fatal("expected a pointer to a NullInt64, but got nil")
		}
		thumbnail.PutNullInt64(ni)
	})
}
