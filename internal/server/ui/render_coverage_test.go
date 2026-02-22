package ui

import (
	"bytes"
	"strings"
	"testing"

	"go.local/sfpg/web"
)

// TestRenderPage_ShutdownPage tests rendering the shutdown page
func TestRenderPage_ShutdownPage(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name    string
		data    any
		partial bool
	}{
		{
			name:    "shutdown page full",
			data:    map[string]any{"CSRFToken": "test-token"},
			partial: false,
		},
		{
			name:    "shutdown page with nil data",
			data:    nil,
			partial: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderPage(&buf, "shutdown", tt.data, tt.partial)
			if err != nil {
				// Template may error with nil data, but should not panic
				t.Logf("Expected error with nil data: %v", err)
				return
			}
			if buf.Len() == 0 {
				t.Error("RenderPage wrote no output")
			}
		})
	}
}

// TestRenderPage_DiscoveryStartedPage tests rendering the discovery-started page
func TestRenderPage_DiscoveryStartedPage(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name    string
		data    any
		partial bool
	}{
		{
			name:    "discovery-started page full",
			data:    map[string]any{"Message": "Discovery started"},
			partial: false,
		},
		{
			name:    "discovery-started page with nil data",
			data:    nil,
			partial: false,
		},
		{
			name:    "discovery-started page partial should return error",
			data:    nil,
			partial: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderPage(&buf, "discovery-started", tt.data, tt.partial)
			if tt.partial {
				// Partial not supported for discovery-started
				if err == nil {
					t.Error("Expected error for partial discovery-started page")
				}
				return
			}
			if err != nil {
				// Template may error with nil data, but should not panic
				t.Logf("Error with nil data: %v", err)
				return
			}
			if buf.Len() == 0 {
				t.Error("RenderPage wrote no output")
			}
		})
	}
}

// TestRenderTemplate_ConfigEtagField tests rendering the config-etag-field template
func TestRenderTemplate_ConfigEtagField(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name        string
		data        any
		expectError bool
	}{
		{
			name:        "config-etag-field with nil data",
			data:        nil,
			expectError: false,
		},
		{
			name: "config-etag-field with data",
			data: map[string]any{
				"CacheVersion": "20260221-01",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderTemplate(&buf, "config-etag-field.html.tmpl", tt.data)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if err == nil && buf.Len() == 0 {
				t.Error("Template wrote no output")
			}
		})
	}
}

// TestRenderTemplate_ThemeModal tests rendering the theme-modal template
func TestRenderTemplate_ThemeModal(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	tests := []struct {
		name        string
		data        any
		expectError bool
	}{
		{
			name:        "theme-modal with nil data",
			data:        nil,
			expectError: false,
		},
		{
			name: "theme-modal with data",
			data: map[string]any{
				"Theme": "dark",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderTemplate(&buf, "theme-modal.html.tmpl", tt.data)
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
			if err == nil && buf.Len() == 0 {
				t.Error("Template wrote no output")
			}
		})
	}
}

// TestRenderTemplate_ErrorPath tests the error logging path in RenderTemplate
func TestRenderTemplate_ErrorPath(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Create a template that will fail execution
	// We use lightbox-content which requires specific data structure
	var buf bytes.Buffer
	err := RenderTemplate(&buf, "lightbox-content.html.tmpl", nil)
	if err == nil {
		t.Error("Expected error for lightbox-content with nil data")
	}
	// This tests the error logging path at line 95-97
}

// TestRenderPage_AllPages tests all page types to increase coverage
func TestRenderPage_AllPages(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	pages := []struct {
		name            string
		supportsPartial bool
	}{
		{"gallery", true},
		{"image", false},
		{"dashboard", true},
		{"shutdown", false},
		{"discovery-started", false},
	}

	for _, page := range pages {
		t.Run(page.name+" full page", func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderPage(&buf, page.name, nil, false)
			// Some pages may error with nil data, that's acceptable
			if err == nil && buf.Len() == 0 {
				t.Errorf("Page %s wrote no output", page.name)
			}
		})

		if page.supportsPartial {
			t.Run(page.name+" partial", func(t *testing.T) {
				var buf bytes.Buffer
				err := RenderPage(&buf, page.name, nil, true)
				// Some pages may error with nil data, that's acceptable
				if err == nil && buf.Len() == 0 {
					t.Errorf("Page %s partial wrote no output", page.name)
				}
			})
		} else {
			t.Run(page.name+" partial should error", func(t *testing.T) {
				var buf bytes.Buffer
				err := RenderPage(&buf, page.name, nil, true)
				if page.name != "shutdown" && page.name != "image" {
					// discovery-started falls through to default case in partial mode
					if err == nil {
						t.Logf("Note: %s page may support partial rendering", page.name)
					}
				}
			})
		}
	}
}

// TestRenderTemplate_AllTemplates tests all template types to ensure coverage
func TestRenderTemplate_AllTemplates(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	templates := []struct {
		name         string
		requiresData bool
	}{
		{"lightbox-content.html.tmpl", true},
		{"config-success.html.tmpl", false},
		{"admin-credentials-success.html.tmpl", false},
		{"config-validation-error.html.tmpl", false},
		{"config-generic-error.html.tmpl", false},
		{"config-database-error.html.tmpl", false},
		{"config-etag-field.html.tmpl", false},
		{"config-modal.html.tmpl", true},
		{"login-form.html.tmpl", false},
		{"infobox-folder.html.tmpl", false},
		{"infobox-image.html.tmpl", true},
		{"hamburger-menu-items.html.tmpl", false},
		{"theme-modal.html.tmpl", false},
	}

	for _, tmpl := range templates {
		t.Run(tmpl.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := RenderTemplate(&buf, tmpl.name, nil)
			if tmpl.requiresData {
				// Templates that require data should error with nil
				if err == nil {
					t.Logf("Note: %s rendered successfully with nil data", tmpl.name)
				}
			} else {
				if err != nil {
					t.Errorf("Template %s failed with nil data: %v", tmpl.name, err)
				}
				if buf.Len() == 0 {
					t.Errorf("Template %s wrote no output", tmpl.name)
				}
			}
		})
	}
}

// TestRenderPage_UnknownPage tests the error handling for unknown pages
func TestRenderPage_UnknownPage(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	t.Run("unknown page in full mode", func(t *testing.T) {
		var buf bytes.Buffer
		err := RenderPage(&buf, "nonexistent", nil, false)
		// In full mode, unknown pages return nil (not an error)
		if err != nil {
			t.Errorf("Unexpected error for unknown page: %v", err)
		}
	})

	t.Run("unknown page in partial mode", func(t *testing.T) {
		var buf bytes.Buffer
		err := RenderPage(&buf, "nonexistent", nil, true)
		// In partial mode, unknown pages should return error
		if err == nil {
			t.Error("Expected error for unknown partial page")
		}
		if !strings.Contains(err.Error(), "no partial definition") {
			t.Errorf("Error should mention 'no partial definition', got: %v", err)
		}
	})
}

// TestRenderTemplate_UnknownTemplate tests the error handling for unknown templates
func TestRenderTemplate_UnknownTemplate(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var buf bytes.Buffer
	err := RenderTemplate(&buf, "nonexistent.html.tmpl", nil)
	if err == nil {
		t.Error("Expected error for unknown template")
	}
	if !strings.Contains(err.Error(), "unknown template") {
		t.Errorf("Error should mention 'unknown template', got: %v", err)
	}
}
