package gentestfiles

import (
	"bytes"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateTestFiles(t *testing.T) {
	tempDir := t.TempDir()

	filePaths := []string{
		"file1.txt",
		"file2.html",
		"images/photo.jpg",
		"images/icon.png",
		"graphics/animation.gif",
		"nested/deep/path/document.txt",
		"test.jpeg",
	}

	err := CreateTestFiles(tempDir, filePaths)
	if err != nil {
		t.Fatalf("CreateTestFiles failed: %v", err)
	}

	// Verify all files exist
	for _, filePath := range filePaths {
		fullPath := filepath.Join(tempDir, filePath)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			t.Errorf("File not created: %s", fullPath)
		}
	}
}

func TestCreateTestFiles_Overwrite(t *testing.T) {
	tempDir := t.TempDir()

	filePaths := []string{
		"duplicate.txt",
		"duplicate.txt",
	}

	err := CreateTestFiles(tempDir, filePaths)
	if err != nil {
		t.Fatalf("CreateTestFiles failed: %v", err)
	}

	fullPath := filepath.Join(tempDir, "duplicate.txt")
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Errorf("File not created: %s", fullPath)
	}
}

func TestCreateTestFiles_UnsupportedExtension(t *testing.T) {
	tempDir := t.TempDir()

	filePaths := []string{
		"file.txt",
		"unsupported.xyz",
		"another.html",
	}

	err := CreateTestFiles(tempDir, filePaths)
	if err != nil {
		t.Fatalf("CreateTestFiles failed: %v", err)
	}

	// Supported files should exist
	txtPath := filepath.Join(tempDir, "file.txt")
	if _, err := os.Stat(txtPath); os.IsNotExist(err) {
		t.Errorf("Supported file not created: %s", txtPath)
	}

	// Unsupported file should not exist
	xyzPath := filepath.Join(tempDir, "unsupported.xyz")
	if _, err := os.Stat(xyzPath); !os.IsNotExist(err) {
		t.Errorf("Unsupported file should not be created: %s", xyzPath)
	}

	// Another supported file should exist
	htmlPath := filepath.Join(tempDir, "another.html")
	if _, err := os.Stat(htmlPath); os.IsNotExist(err) {
		t.Errorf("Supported file not created: %s", htmlPath)
	}
}

func TestTextFileGeneration(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.txt")

	err := generateTextFile(filePath)
	if err != nil {
		t.Fatalf("generateTextFile failed: %v", err)
	}

	// Verify file exists and has content
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read generated text file: %v", err)
	}

	if len(content) == 0 {
		t.Error("Text file is empty")
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "test.txt") {
		t.Error("Text file doesn't contain filename")
	}
}

func TestHTMLFileGeneration(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.html")

	err := generateHTMLFile(filePath)
	if err != nil {
		t.Fatalf("generateHTMLFile failed: %v", err)
	}

	// Verify file exists and is valid HTML
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read generated HTML file: %v", err)
	}

	contentStr := string(content)

	// Check for essential HTML elements
	requiredElements := []string{
		"<!DOCTYPE html>",
		"<html",
		"<head>",
		"<body>",
		"</html>",
		"test.html",
	}

	for _, elem := range requiredElements {
		if !strings.Contains(contentStr, elem) {
			t.Errorf("HTML file missing required element: %s", elem)
		}
	}
}

func TestJPEGFileGeneration(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.jpg")

	err := generateJPEGFile(filePath)
	if err != nil {
		t.Fatalf("generateJPEGFile failed: %v", err)
	}

	// Verify file exists and is a valid JPEG
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read generated JPEG file: %v", err)
	}

	// Decode the JPEG to verify it's valid
	img, err := jpeg.Decode(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Failed to decode JPEG: %v", err)
	}

	// Check dimensions
	bounds := img.Bounds()
	if bounds.Dx() != imageWidth {
		t.Errorf("JPEG width = %d, want %d", bounds.Dx(), imageWidth)
	}
	if bounds.Dy() != imageHeight {
		t.Errorf("JPEG height = %d, want %d", bounds.Dy(), imageHeight)
	}
}

func TestPNGFileGeneration(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.png")

	err := generatePNGFile(filePath)
	if err != nil {
		t.Fatalf("generatePNGFile failed: %v", err)
	}

	// Verify file exists and is a valid PNG
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read generated PNG file: %v", err)
	}

	// Decode the PNG to verify it's valid
	img, err := png.Decode(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Failed to decode PNG: %v", err)
	}

	// Check dimensions
	bounds := img.Bounds()
	if bounds.Dx() != imageWidth {
		t.Errorf("PNG width = %d, want %d", bounds.Dx(), imageWidth)
	}
	if bounds.Dy() != imageHeight {
		t.Errorf("PNG height = %d, want %d", bounds.Dy(), imageHeight)
	}
}

func TestGIFFileGeneration(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test.gif")

	err := generateGIFFile(filePath)
	if err != nil {
		t.Fatalf("generateGIFFile failed: %v", err)
	}

	// Verify file exists and is a valid GIF
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("Failed to read generated GIF file: %v", err)
	}

	// Decode the GIF to verify it's valid
	img, err := gif.Decode(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Failed to decode GIF: %v", err)
	}

	// Check dimensions
	bounds := img.Bounds()
	if bounds.Dx() != imageWidth {
		t.Errorf("GIF width = %d, want %d", bounds.Dx(), imageWidth)
	}
	if bounds.Dy() != imageHeight {
		t.Errorf("GIF height = %d, want %d", bounds.Dy(), imageHeight)
	}
}

func TestJPEGExtensionVariants(t *testing.T) {
	tempDir := t.TempDir()

	tests := []string{"test.jpg", "test.jpeg", "test.JPG", "test.JPEG"}

	for _, filename := range tests {
		t.Run(filename, func(t *testing.T) {
			filePath := filepath.Join(tempDir, filename)
			err := generateFile(filePath)
			if err != nil {
				t.Fatalf("generateFile failed for %s: %v", filename, err)
			}

			// Verify it's a valid JPEG
			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("Failed to read file: %v", err)
			}

			_, err = jpeg.Decode(bytes.NewReader(content))
			if err != nil {
				t.Fatalf("Failed to decode as JPEG: %v", err)
			}
		})
	}
}

func TestCreateGradientImage(t *testing.T) {
	img := createGradientImage()

	if img == nil {
		t.Fatal("createGradientImage returned nil")
	}

	bounds := img.Bounds()
	if bounds.Dx() != imageWidth {
		t.Errorf("Image width = %d, want %d", bounds.Dx(), imageWidth)
	}
	if bounds.Dy() != imageHeight {
		t.Errorf("Image height = %d, want %d", bounds.Dy(), imageHeight)
	}

	// Verify the image has actual color data (not all transparent)
	hasColor := false
	for y := 0; y < imageHeight && !hasColor; y++ {
		for x := 0; x < imageWidth && !hasColor; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a > 0 && (r > 0 || g > 0 || b > 0) {
				hasColor = true
			}
		}
	}

	if !hasColor {
		t.Error("Generated image appears to be empty/transparent")
	}
}

func TestNestedDirectoryCreation(t *testing.T) {
	tempDir := t.TempDir()

	filePaths := []string{
		"a/b/c/d/e/deep.txt",
		"x/y/z/file.html",
	}

	err := CreateTestFiles(tempDir, filePaths)
	if err != nil {
		t.Fatalf("CreateTestFiles failed: %v", err)
	}

	for _, filePath := range filePaths {
		fullPath := filepath.Join(tempDir, filePath)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			t.Errorf("Nested file not created: %s", fullPath)
		}
	}
}

func TestImageFilesAreDifferent(t *testing.T) {
	tempDir := t.TempDir()

	// Generate two images
	filePaths := []string{
		"image1.png",
		"image2.png",
	}

	err := CreateTestFiles(tempDir, filePaths)
	if err != nil {
		t.Fatalf("CreateTestFiles failed: %v", err)
	}

	content1, err := os.ReadFile(filepath.Join(tempDir, "image1.png"))
	if err != nil {
		t.Fatalf("Failed to read image1: %v", err)
	}

	content2, err := os.ReadFile(filepath.Join(tempDir, "image2.png"))
	if err != nil {
		t.Fatalf("Failed to read image2: %v", err)
	}

	// Images should be different due to random gradients
	if bytes.Equal(content1, content2) {
		t.Error("Generated images are identical, expected different gradients")
	}
}
