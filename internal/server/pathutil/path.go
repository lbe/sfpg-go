// Package pathutil provides path manipulation utilities for the server package.
package pathutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RemoveImagesDirPrefix removes images dir prefix with path traversal check.
// normalizedImagesDir should be the pre-normalized result of filepath.ToSlash(imagesDir).
// Returns an error if the resulting path contains path traversal sequences (..).
func RemoveImagesDirPrefix(normalizedImagesDir, path string) (string, error) {
	// Normalize path to forward slashes for consistent database storage
	normalizedPath := filepath.ToSlash(path)

	// Check for path traversal attempts:
	// - Starts with "../" means relative traversal
	// - Contains "/../" means traversal in the middle
	if strings.HasPrefix(normalizedPath, "../") || strings.Contains(normalizedPath, "/../") {
		return "", fmt.Errorf("invalid path: contains traversal")
	}

	if normalizedImagesDir == "" {
		return normalizedPath, nil
	}

	result := strings.TrimPrefix(normalizedPath, normalizedImagesDir+"/")

	return result, nil
}
