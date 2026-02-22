package pathutil

import (
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
