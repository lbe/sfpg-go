package pathutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoveImagesDirPrefix(t *testing.T) {
	normalizedImagesDir := "/var/images"

	tests := []struct {
		name          string
		normalizedDir string
		path          string
		want          string
		wantErr       bool
		errContains   string
	}{
		{
			name:          "removes prefix correctly",
			normalizedDir: normalizedImagesDir,
			path:          "/var/images/file.jpg",
			want:          "file.jpg",
			wantErr:       false,
		},
		{
			name:          "handles nested path",
			normalizedDir: normalizedImagesDir,
			path:          "/var/images/folder/subfolder/file.jpg",
			want:          "folder/subfolder/file.jpg",
			wantErr:       false,
		},
		{
			name:          "handles path with backslashes",
			normalizedDir: normalizedImagesDir,
			path:          `\var\images\file.jpg`,
			want:          `\var\images\file.jpg`, // Unix: backslashes not converted by filepath.ToSlash
			wantErr:       false,
		},
		{
			name:          "empty normalized dir returns original",
			normalizedDir: "",
			path:          "/some/path/file.jpg",
			want:          "/some/path/file.jpg",
			wantErr:       false,
		},
		{
			name:          "path outside images dir",
			normalizedDir: "/var/images",
			path:          "/other/path/file.jpg",
			want:          "/other/path/file.jpg",
			wantErr:       false,
		},
		{
			name:          "path with double slashes",
			normalizedDir: normalizedImagesDir,
			path:          "/var/images//folder//file.jpg",
			want:          "/folder//file.jpg", // TrimPrefix removes /var/images/ leaving //folder//file.jpg
			wantErr:       false,
		},
		{
			name:          "root level file",
			normalizedDir: "/var/images",
			path:          "/var/images/file.jpg",
			want:          "file.jpg",
			wantErr:       false,
		},
		{
			name:          "trailing slash in path",
			normalizedDir: normalizedImagesDir,
			path:          "/var/images/folder/",
			want:          "folder/",
			wantErr:       false,
		},
		{
			name:          "windows-style path",
			normalizedDir: "C:/Images",
			path:          `C:\Images\folder\file.jpg`,
			want:          `C:\Images\folder\file.jpg`, // Unix: backslashes not converted
			wantErr:       false,
		},
		{
			name:          "mixed slashes in path",
			normalizedDir: normalizedImagesDir,
			path:          "/var/images\\folder/subfolder\\file.jpg",
			want:          "/var/images\\folder/subfolder\\file.jpg", // Prefix doesn't match due to backslash
			wantErr:       false,
		},
		{
			name:          "file with same name as directory",
			normalizedDir: "/var/images",
			path:          "/var/images/images.jpg",
			want:          "images.jpg",
			wantErr:       false,
		},
		{
			name:          "path traversal - double dot in middle",
			normalizedDir: "/var/images",
			path:          "/var/images/../etc/passwd",
			want:          "",
			wantErr:       true,
			errContains:   "traversal",
		},
		{
			name:          "path traversal - double dot at start",
			normalizedDir: "/var/images",
			path:          "../etc/passwd",
			want:          "",
			wantErr:       true,
			errContains:   "traversal",
		},
		{
			name:          "path traversal - encoded double dot",
			normalizedDir: "/var/images",
			path:          "/var/images/%2e%2e/etc/passwd",
			want:          "%2e%2e/etc/passwd", // URL encoding not decoded, just prefix removed
			wantErr:       false,
		},
		{
			name:          "path with only images dir",
			normalizedDir: "/var/images",
			path:          "/var/images",
			want:          "/var/images", // Prefix "/var/images/" doesn't match "/var/images"
			wantErr:       false,
		},
		{
			name:          "empty path",
			normalizedDir: "/var/images",
			path:          "",
			want:          "",
			wantErr:       false,
		},
		{
			name:          "relative path in images dir",
			normalizedDir: "/var/images",
			path:          "folder/file.jpg",
			want:          "folder/file.jpg",
			wantErr:       false,
		},
		{
			name:          "absolute path without prefix",
			normalizedDir: "/data/images",
			path:          "/tmp/other.jpg",
			want:          "/tmp/other.jpg",
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RemoveImagesDirPrefix(tt.normalizedDir, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("RemoveImagesDirPrefix() expected error containing %q, got nil", tt.errContains)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("RemoveImagesDirPrefix() error = %q, want error containing %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("RemoveImagesDirPrefix() unexpected error: %v", err)
				return
			}
			if got != tt.want {
				t.Errorf("RemoveImagesDirPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRemoveImagesDirPrefix_EdgeCases(t *testing.T) {
	t.Run("path with multiple consecutive slashes", func(t *testing.T) {
		got, err := RemoveImagesDirPrefix("/var/images", "/var/images///folder///file.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "//folder///file.jpg" // TrimPrefix only removes one instance of the prefix
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("very long path", func(t *testing.T) {
		longPath := "/var/images/" + strings.Repeat("folder/", 100) + "file.jpg"
		got, err := RemoveImagesDirPrefix("/var/images", longPath)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := strings.Repeat("folder/", 100) + "file.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("path with dot (not double dot)", func(t *testing.T) {
		got, err := RemoveImagesDirPrefix("/var/images", "/var/images/folder/file.v1.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "folder/file.v1.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("path starting with dot", func(t *testing.T) {
		got, err := RemoveImagesDirPrefix("/var/images", "/var/images/.hidden/file.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := ".hidden/file.jpg"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("normalized dir with trailing slash", func(t *testing.T) {
		// The function expects normalized dir without trailing slash
		// This test documents current behavior
		got, err := RemoveImagesDirPrefix("/var/images/", "/var/images/file.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// With trailing slash in normalized dir, it won't match correctly
		// /var/images/ + / = /var/images// which doesn't match /var/images/
		want := "/var/images/file.jpg" // Path not modified since prefix doesn't match
		if got != want {
			t.Errorf("got %q, want %q (note: normalized dir should not have trailing slash)", got, want)
		}
	})
}

func TestSafeImagePath(t *testing.T) {
	t.Run("valid file within images dir", func(t *testing.T) {
		dir := t.TempDir()
		absPath, err := SafeImagePath(dir, "folder/file.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(dir, "folder/file.jpg")
		if absPath != want {
			t.Errorf("SafeImagePath() = %q, want %q", absPath, want)
		}
	})

	t.Run("traversal with ../ escapes", func(t *testing.T) {
		dir := t.TempDir()
		_, err := SafeImagePath(dir, "../etc/passwd")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrPathTraversal) {
			t.Errorf("expected ErrPathTraversal, got %v", err)
		}
	})

	t.Run("traversal with embedded ../", func(t *testing.T) {
		dir := t.TempDir()
		_, err := SafeImagePath(dir, "folder/../../../etc/passwd")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, ErrPathTraversal) {
			t.Errorf("expected ErrPathTraversal, got %v", err)
		}
	})

	t.Run("valid nested path", func(t *testing.T) {
		dir := t.TempDir()
		absPath, err := SafeImagePath(dir, "deeply/nested/path/to/image.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(dir, "deeply/nested/path/to/image.jpg")
		if absPath != want {
			t.Errorf("SafeImagePath() = %q, want %q", absPath, want)
		}
	})

	t.Run("file at root of images dir", func(t *testing.T) {
		dir := t.TempDir()
		absPath, err := SafeImagePath(dir, "image.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(dir, "image.jpg")
		if absPath != want {
			t.Errorf("SafeImagePath() = %q, want %q", absPath, want)
		}
	})

	t.Run("path with dots not traversal", func(t *testing.T) {
		dir := t.TempDir()
		absPath, err := SafeImagePath(dir, "file.v1.2.jpg")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := filepath.Join(dir, "file.v1.2.jpg")
		if absPath != want {
			t.Errorf("SafeImagePath() = %q, want %q", absPath, want)
		}
	})

	t.Run("empty filePath resolves to images dir itself", func(t *testing.T) {
		dir := t.TempDir()
		absPath, err := SafeImagePath(dir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if absPath != dir {
			t.Errorf("SafeImagePath() = %q, want %q", absPath, dir)
		}
	})

	t.Run("empty imagesDir returns error", func(t *testing.T) {
		_, err := SafeImagePath("", "image.jpg")
		if err == nil {
			t.Fatal("expected error for empty imagesDir, got nil")
		}
		if !errors.Is(err, ErrInvalidImagesDir) {
			t.Errorf("expected ErrInvalidImagesDir, got %v", err)
		}
	})

	t.Run("path exactly equals images dir (no subpath)", func(t *testing.T) {
		dir := t.TempDir()
		absPath, err := SafeImagePath(dir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if absPath != dir {
			t.Errorf("SafeImagePath() = %q, want %q", absPath, dir)
		}
	})
}

func TestSafeImagePath_SymlinkEscape_IsAllowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on Windows")
	}

	// Symlinks inside the images directory that point outside (e.g., to external
	// storage mounts) are intentionally allowed — the admin controls the images
	// directory. SafeImagePath does not resolve symlinks; it only checks for
	// "../" path traversal, which filepath.Join normalizes.
	dir := t.TempDir()
	imagesDir := filepath.Join(dir, "images")
	externalDir := filepath.Join(dir, "external")
	if err := os.MkdirAll(imagesDir, 0o755); err != nil {
		t.Fatalf("failed to create images dir: %v", err)
	}
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatalf("failed to create external dir: %v", err)
	}

	// Create a file in the external dir
	externalFile := filepath.Join(externalDir, "photo.jpg")
	if err := os.WriteFile(externalFile, []byte("photo-data"), 0o644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Create a symlink inside images dir that points to the external dir
	linkPath := filepath.Join(imagesDir, "storage")
	if err := os.Symlink(externalDir, linkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	// Access the file via the symlink — SafeImagePath checks prefix on the
	// unresolved path (images/storage/photo.jpg), which still starts with
	// imagesDir. The symlink is followed by the OS at serving time.
	got, err := SafeImagePath(imagesDir, "storage/photo.jpg")
	if err != nil {
		t.Fatalf("unexpected error for symlink escape: %v", err)
	}
	// SafeImagePath returns the unresolved absolute path, not the resolved path.
	// The symlink is followed by the OS when the path is used for serving.
	want := filepath.Join(imagesDir, "storage/photo.jpg")
	if got != want {
		t.Errorf("SafeImagePath() = %q, want %q", got, want)
	}
	// Verify the file is actually reachable through the symlink
	if _, err := os.Stat(got); err != nil {
		t.Errorf("path %q should be reachable: %v", got, err)
	}
}
