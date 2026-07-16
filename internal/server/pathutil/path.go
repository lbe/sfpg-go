// Package pathutil provides path manipulation utilities for the server package.
package pathutil

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Sentinel errors returned by SafeImagePath.
var (
	// ErrInvalidPath is returned when the resolved file path cannot be made absolute.
	ErrInvalidPath = errors.New("invalid file path")
	// ErrInvalidImagesDir is returned when the images directory path cannot be made absolute.
	ErrInvalidImagesDir = errors.New("invalid images directory")
	// ErrPathTraversal is returned when the file path escapes the images directory.
	ErrPathTraversal = errors.New("path traversal detected")
)

// SafeImagePath resolves a file path within the images directory and ensures
// it does not escape the images root via "../" path traversal. It returns the
// absolute safe path or one of the sentinel errors ErrInvalidPath,
// ErrInvalidImagesDir, or ErrPathTraversal.
//
// The check is simple: filepath.Join normalizes "../" components, so comparing
// the joined absolute path against the images directory prefix catches any
// traversal attempt. Symlinks inside the images directory are not resolved —
// the OS kernel handles them transparently when the path is used for serving.
func SafeImagePath(imagesDir, filePath string) (string, error) {
	if imagesDir == "" {
		return "", fmt.Errorf("%w: images directory is empty", ErrInvalidImagesDir)
	}

	absImagesDir, err := filepath.Abs(imagesDir)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidImagesDir, err.Error())
	}

	fullPath := filepath.Join(absImagesDir, filePath)
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidPath, err.Error())
	}

	prefix := absImagesDir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if !strings.HasPrefix(absPath+string(filepath.Separator), prefix) {
		return "", fmt.Errorf("%w: path %q escapes images directory %q", ErrPathTraversal, filePath, imagesDir)
	}

	return absPath, nil
}

// RemoveImagesDirPrefix removes images dir prefix with path traversal check.
// normalizedImagesDir should be the pre-normalized result of filepath.ToSlash(imagesDir).
// Returns an error if the resulting path contains path traversal sequences (..)
// or if an absolute path lies outside the images directory.
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

	// Strip trailing slash so prefix matching is consistent regardless of
	// whether the caller passed a trailing slash or not.
	cleanDir := strings.TrimSuffix(normalizedImagesDir, "/")

	// Reject absolute paths that are not under the images directory.
	// Relative paths are implicitly under the images dir (they will be
	// resolved relative to it at processing time).
	if filepath.IsAbs(normalizedPath) &&
		normalizedPath != cleanDir &&
		!strings.HasPrefix(normalizedPath, cleanDir+"/") {
		return "", fmt.Errorf("invalid path: %q is outside images directory %q", path, normalizedImagesDir)
	}

	result := strings.TrimPrefix(normalizedPath, cleanDir+"/")

	return result, nil
}
