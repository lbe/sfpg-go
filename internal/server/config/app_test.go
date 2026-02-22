package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestApplyConfig_CreatesImageDirectory_IfMissing verifies that ApplyImageDirectory creates
// image directory if it doesn't exist.
func TestApplyConfig_CreatesImageDirectory_IfMissing(t *testing.T) {
	tempDir := t.TempDir()
	customImageDir := filepath.Join(tempDir, "new-images")

	// Verify directory doesn't exist yet
	if _, err := os.Stat(customImageDir); !os.IsNotExist(err) {
		t.Fatalf("directory should not exist before ApplyImageDirectory, but it does: %v", err)
	}

	imagesDir, normalized, err := ApplyImageDirectory(customImageDir)
	if err != nil {
		t.Fatalf("ApplyImageDirectory failed: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(customImageDir); os.IsNotExist(err) {
		t.Errorf("ApplyImageDirectory should have created image directory at %q, but it doesn't exist", customImageDir)
	}

	// Verify imagesDir is set correctly
	if imagesDir != customImageDir {
		t.Errorf("expected imagesDir to be %q, got %q", customImageDir, imagesDir)
	}

	// Verify normalized path is set correctly
	expectedNormalized := filepath.ToSlash(customImageDir)
	if normalized != expectedNormalized {
		t.Errorf("expected normalizedImagesDir to be %q, got %q", expectedNormalized, normalized)
	}
}

// TestApplyConfig_UpdatesNormalizedPath verifies that normalizedImagesDir is updated
// when imagesDir changes.
func TestApplyConfig_UpdatesNormalizedPath(t *testing.T) {
	tempDir := t.TempDir()
	firstImageDir := filepath.Join(tempDir, "images1")
	secondImageDir := filepath.Join(tempDir, "images2")

	imagesDir1, normalized1, err := ApplyImageDirectory(firstImageDir)
	if err != nil {
		t.Fatalf("ApplyImageDirectory failed for first image dir: %v", err)
	}

	expectedNormalized1 := filepath.ToSlash(firstImageDir)
	if normalized1 != expectedNormalized1 {
		t.Errorf("expected normalizedImagesDir to be %q, got %q", expectedNormalized1, normalized1)
	}
	if imagesDir1 != firstImageDir {
		t.Errorf("expected imagesDir to be %q, got %q", firstImageDir, imagesDir1)
	}

	imagesDir2, normalized2, err := ApplyImageDirectory(secondImageDir)
	if err != nil {
		t.Fatalf("ApplyImageDirectory failed for second image dir: %v", err)
	}

	expectedNormalized2 := filepath.ToSlash(secondImageDir)
	if normalized2 != expectedNormalized2 {
		t.Errorf("expected normalizedImagesDir to be %q after change, got %q", expectedNormalized2, normalized2)
	}
	if imagesDir2 != secondImageDir {
		t.Errorf("expected imagesDir to be %q, got %q", secondImageDir, imagesDir2)
	}
}

// TestValidateImageDirectory_Exists verifies that validation passes for existing directory.
func TestValidateImageDirectory_Exists(t *testing.T) {
	tempDir := t.TempDir()
	testDir := filepath.Join(tempDir, "test-dir")

	// Create the directory
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	// Validation should pass
	if err := ValidateImageDirectory(testDir); err != nil {
		t.Errorf("ValidateImageDirectory should pass for existing directory, got error: %v", err)
	}
}

// TestValidateImageDirectory_NotExists verifies that validation fails for non-existent directory.
func TestValidateImageDirectory_NotExists(t *testing.T) {
	tempDir := t.TempDir()
	nonExistentDir := filepath.Join(tempDir, "does-not-exist")

	// Validation should fail
	if err := ValidateImageDirectory(nonExistentDir); err == nil {
		t.Error("ValidateImageDirectory should fail for non-existent directory, but got no error")
	}
}

// TestValidateImageDirectory_IsFile verifies that validation fails when path is a file, not directory.
func TestValidateImageDirectory_IsFile(t *testing.T) {
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test-file.txt")

	// Create a file instead of directory
	if err := os.WriteFile(testFile, []byte("test"), 0o644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Validation should fail
	if err := ValidateImageDirectory(testFile); err == nil {
		t.Error("ValidateImageDirectory should fail when path is a file, but got no error")
	}
}

// TestValidateImageDirectory_NotReadable verifies that validation fails when directory is not readable.
// Note: This test may be platform-specific and may not work on all systems.
func TestValidateImageDirectory_NotReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Skipping permission test on Windows")
	}

	tempDir := t.TempDir()
	testDir := filepath.Join(tempDir, "no-read")

	// Create directory with no read permissions
	if err := os.MkdirAll(testDir, 0o000); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}
	defer func() {
		// Restore permissions for cleanup
		_ = os.Chmod(testDir, 0o755)
	}()

	// Validation should fail
	if err := ValidateImageDirectory(testDir); err == nil {
		t.Error("ValidateImageDirectory should fail when directory is not readable, but got no error")
	}
}
