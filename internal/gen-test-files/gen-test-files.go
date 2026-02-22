// Package gentestfiles provides utilities for generating test files with
// valid content based on their file extensions. It supports creating test
// images (JPEG, PNG, GIF), text files, and other file types for testing purposes.
package gentestfiles

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
)

const (
	imageWidth  = 200
	imageHeight = 190
)

// CreateTestFiles creates test files with valid content based on their
// extensions in the specified directory.
func CreateTestFiles(dirName string, filePaths []string) error {
	// Ensure base directory exists
	if err := os.MkdirAll(dirName, 0755); err != nil {
		return fmt.Errorf("failed to create base directory: %w", err)
	}

	for _, filePath := range filePaths {
		fullPath := filepath.Join(dirName, filePath)

		// Ensure parent directory exists
		parentDir := filepath.Dir(fullPath)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			slog.Error("failed to create directory",
				"path", parentDir,
				"error", err)
			continue
		}

		// Generate and write content
		if err := generateFile(fullPath); err != nil {
			slog.Error("failed to generate file",
				"path", fullPath,
				"error", err)
			continue
		}
	}

	return nil
}

// generateFile creates a file with appropriate content based on extension
func generateFile(filePath string) error {
	ext := strings.ToLower(filepath.Ext(filePath))

	switch ext {
	case ".txt":
		return generateTextFile(filePath)
	case ".html":
		return generateHTMLFile(filePath)
	case ".jpg", ".jpeg":
		return generateJPEGFile(filePath)
	case ".gif":
		return generateGIFFile(filePath)
	case ".png":
		return generatePNGFile(filePath)
	default:
		return fmt.Errorf("unsupported file extension: %s", ext)
	}
}

// generateTextFile creates a simple text file
func generateTextFile(filePath string) error {
	content := fmt.Sprintf("Test file: %s\n\nThis is a test text file "+
		"created for testing purposes.\n", filepath.Base(filePath))
	return os.WriteFile(filePath, []byte(content), 0644)
}

// generateHTMLFile creates a valid HTML5 file.
// HTML content is kept as a string literal (rather than a template) because:
// - This is a test utility function, not production HTML generation
// - Simple, single-use template with minimal dynamic content (just the filename)
// - No need for template infrastructure for test fixture generation
// - Utility function focuses on file I/O, not HTML rendering complexity
func generateHTMLFile(filePath string) error {
	fileName := filepath.Base(filePath)
	content := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>%s</title>
</head>
<body>
    <h1>Test HTML File</h1>
    <p>This is a test HTML file: <strong>%s</strong></p>
    <p>Created for testing purposes.</p>
</body>
</html>
`, fileName, fileName)
	return os.WriteFile(filePath, []byte(content), 0644)
}

// createGradientImage creates an image with a random gradient
func createGradientImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, imageWidth, imageHeight))

	// Random start and end colors
	startColor := color.RGBA{
		R: uint8(rand.Intn(256)),
		G: uint8(rand.Intn(256)),
		B: uint8(rand.Intn(256)),
	}
	endColor := color.RGBA{
		R: uint8(rand.Intn(256)),
		G: uint8(rand.Intn(256)),
		B: uint8(rand.Intn(256)),
	}

	// Create vertical gradient
	for y := range imageHeight {
		ratio := float64(y) / float64(imageHeight)
		r := uint8(float64(startColor.R)*(1-ratio) +
			float64(endColor.R)*ratio)
		g := uint8(float64(startColor.G)*(1-ratio) +
			float64(endColor.G)*ratio)
		b := uint8(float64(startColor.B)*(1-ratio) +
			float64(endColor.B)*ratio)

		lineColor := color.RGBA{R: r, G: g, B: b, A: 255}
		draw.Draw(img,
			image.Rect(0, y, imageWidth, y+1),
			&image.Uniform{lineColor},
			image.Point{},
			draw.Src)
	}

	return img
}

// generateJPEGFile creates a valid JPEG image file
func generateJPEGFile(filePath string) error {
	img := createGradientImage()

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return jpeg.Encode(file, img, &jpeg.Options{Quality: 90})
}

// generateGIFFile creates a valid GIF image file
func generateGIFFile(filePath string) error {
	img := createGradientImage()

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return gif.Encode(file, img, nil)
}

// generatePNGFile creates a valid PNG image file
func generatePNGFile(filePath string) error {
	img := createGradientImage()

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	return png.Encode(file, img)
}
