package ui

import (
	"bytes"
	"strings"
	"testing"

	"go.local/sfpg/web"
)

// TestParseTemplates verifies that all embedded templates can be parsed
// successfully without panicking. This ensures there are no syntax errors
// in the Go templates themselves.
func TestParseTemplates(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ParseTemplates panicked: %v", r)
		}
	}()

	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}
}

// TestBasenameWithoutExt tests the basenameWithoutExt function with various inputs.
func TestBasenameWithoutExt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple file with extension",
			input:    "/path/to/file.txt",
			expected: "file",
		},
		{
			name:     "file with multiple dots",
			input:    "/path/to/file.tar.gz",
			expected: "file.tar",
		},
		{
			name:     "file without extension",
			input:    "/path/to/file",
			expected: "file",
		},
		{
			name:     "file with only basename",
			input:    "image.jpg",
			expected: "image",
		},
		{
			name:     "hidden file with extension",
			input:    "/path/.hidden.txt",
			expected: ".hidden",
		},
		{
			name:     "hidden file without extension",
			input:    "/path/.hidden",
			expected: "", // filepath.Ext(".hidden") returns ".hidden"
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "just extension",
			input:    ".txt",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := basenameWithoutExt(tt.input)
			if result != tt.expected {
				t.Errorf("basenameWithoutExt(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestEscapeHash tests the EscapeHash function with various inputs.
func TestEscapeHash(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "string with single hash",
			input:    "test#value",
			expected: "test%23value",
		},
		{
			name:     "string with multiple hashes",
			input:    "test#value#another",
			expected: "test%23value%23another",
		},
		{
			name:     "string without hash",
			input:    "testvalue",
			expected: "testvalue",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only hash",
			input:    "#",
			expected: "%23",
		},
		{
			name:     "hash at start",
			input:    "#start",
			expected: "%23start",
		},
		{
			name:     "hash at end",
			input:    "end#",
			expected: "end%23",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EscapeHash(tt.input)
			if result != tt.expected {
				t.Errorf("EscapeHash(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestRenderTemplate tests the RenderTemplate function with various template names.
func TestRenderTemplate(t *testing.T) {
	// Initialize templates before testing
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name         string
		templateName string
		data         any
		expectError  bool
		errorMsg     string
	}{
		{
			name:         "lightbox-content template",
			templateName: "lightbox-content.html.tmpl",
			data:         nil,
			expectError:  true, // requires specific data structure
		},
		{
			name:         "config-success template",
			templateName: "config-success.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "admin-credentials-success template",
			templateName: "admin-credentials-success.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "config-validation-error template",
			templateName: "config-validation-error.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "config-generic-error template",
			templateName: "config-generic-error.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "config-database-error template",
			templateName: "config-database-error.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "config-modal template",
			templateName: "config-modal.html.tmpl",
			data:         nil,
			expectError:  true, // requires specific data structure
		},
		{
			name:         "login-form template",
			templateName: "login-form.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "infobox-folder template",
			templateName: "infobox-folder.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "infobox-image template",
			templateName: "infobox-image.html.tmpl",
			data:         nil,
			expectError:  true, // requires specific data structure
		},
		{
			name:         "hamburger-menu-items template",
			templateName: "hamburger-menu-items.html.tmpl",
			data:         nil,
			expectError:  false,
		},
		{
			name:         "unknown template",
			templateName: "non-existent.html.tmpl",
			data:         nil,
			expectError:  true,
			errorMsg:     "unknown template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderTemplate(&buf, tt.templateName, tt.data)

			if tt.expectError {
				if err == nil {
					t.Errorf("RenderTemplate(%q) expected error but got none", tt.templateName)
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("RenderTemplate(%q) error = %v, want error containing %q", tt.templateName, err, tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("RenderTemplate(%q) unexpected error: %v", tt.templateName, err)
				}
				// Verify that something was written
				if buf.Len() == 0 {
					t.Errorf("RenderTemplate(%q) wrote no output", tt.templateName)
				}
			}
		})
	}
}

// TestRenderPage tests the RenderPage function with various page names and partial settings.
func TestRenderPage(t *testing.T) {
	// Initialize templates before testing
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name        string
		pageName    string
		data        any
		partial     bool
		expectError bool
		errorMsg    string
	}{
		{
			name:        "gallery page full",
			pageName:    "gallery",
			data:        nil,
			partial:     false,
			expectError: false,
		},
		{
			name:        "gallery page partial",
			pageName:    "gallery",
			data:        nil,
			partial:     true,
			expectError: false,
		},
		{
			name:        "image page full",
			pageName:    "image",
			data:        nil,
			partial:     false,
			expectError: false,
		},
		{
			name:        "unknown page full",
			pageName:    "unknown",
			data:        nil,
			partial:     false,
			expectError: false, // returns nil without error
		},
		{
			name:        "unknown page partial",
			pageName:    "unknown",
			data:        nil,
			partial:     true,
			expectError: true,
			errorMsg:    "no partial definition",
		},
		{
			name:        "image page partial not supported",
			pageName:    "image",
			data:        nil,
			partial:     true,
			expectError: true,
			errorMsg:    "no partial definition",
		},
		{
			name:        "dashboard page full",
			pageName:    "dashboard",
			data:        nil,
			partial:     false,
			expectError: false, // may error due to nil data, but should not panic
		},
		{
			name:        "dashboard page partial",
			pageName:    "dashboard",
			data:        nil,
			partial:     true,
			expectError: false, // may error due to nil data, but should not panic
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderPage(&buf, tt.pageName, tt.data, tt.partial)

			if tt.expectError {
				if err == nil {
					t.Errorf("RenderPage(%q, partial=%v) expected error but got none", tt.pageName, tt.partial)
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("RenderPage(%q, partial=%v) error = %v, want error containing %q", tt.pageName, tt.partial, err, tt.errorMsg)
				}
			} else {
				// Check specifically for "no partial definition" error which indicates
				// the partial case is not implemented for this page
				if err != nil && strings.Contains(err.Error(), "no partial definition") {
					t.Errorf("RenderPage(%q, partial=%v) missing partial definition: %v", tt.pageName, tt.partial, err)
				}
				// For known pages that should render without errors, verify output
				// Note: nil data may cause template execution errors, which is acceptable
				// as long as it's not a "no partial definition" error
				if tt.pageName == "gallery" || tt.pageName == "image" {
					if err == nil && buf.Len() == 0 {
						t.Errorf("RenderPage(%q, partial=%v) wrote no output", tt.pageName, tt.partial)
					}
				}
				// For dashboard, we mainly check that partial definition exists (not "no partial definition" error)
				// The template may fail with nil data, which is expected
			}
		})
	}
}

// TestRenderPageWithNilData tests rendering pages with nil data to ensure
// the functions execute without panicking even with nil data.
func TestRenderPageWithNilData(t *testing.T) {
	// Initialize templates before testing
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	t.Run("gallery with nil data", func(t *testing.T) {
		var buf bytes.Buffer
		// With nil data, templates may fail on field access, but RenderPage should not panic
		err := RenderPage(&buf, "gallery", nil, false)
		// We expect this might error due to nil data, but should not panic
		if err == nil {
			if buf.Len() == 0 {
				t.Error("RenderPage with nil data wrote no output")
			}
		}
	})

	t.Run("image with nil data", func(t *testing.T) {
		var buf bytes.Buffer
		err := RenderPage(&buf, "image", nil, false)
		// With nil data, might error, but should not panic
		if err == nil {
			if buf.Len() == 0 {
				t.Error("RenderPage with nil data wrote no output")
			}
		}
	})
}

// TestRenderTemplateWithSimpleData tests rendering templates with simple data.
func TestRenderTemplateWithSimpleData(t *testing.T) {
	// Initialize templates before testing
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	type simpleData struct {
		Message string
		Count   int
	}

	data := simpleData{
		Message: "Test message",
		Count:   42,
	}

	// Test templates that can handle simple or nil data
	t.Run("config-success with data", func(t *testing.T) {
		var buf bytes.Buffer
		err := RenderTemplate(&buf, "config-success.html.tmpl", data)
		if err != nil {
			t.Errorf("RenderTemplate with data failed: %v", err)
		}
		if buf.Len() == 0 {
			t.Error("RenderTemplate with data wrote no output")
		}
	})

	// Test with empty struct to ensure template executes
	t.Run("config-generic-error with data", func(t *testing.T) {
		var buf bytes.Buffer
		err := RenderTemplate(&buf, "config-generic-error.html.tmpl", data)
		if err != nil {
			t.Errorf("RenderTemplate with data failed: %v", err)
		}
		if buf.Len() == 0 {
			t.Error("RenderTemplate with data wrote no output")
		}
	})
}
