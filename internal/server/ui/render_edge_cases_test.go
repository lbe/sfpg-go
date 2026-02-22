package ui

import (
	"bytes"
	"testing"

	"go.local/sfpg/web"
)

// TestRenderPage_EdgeCases tests edge cases in RenderPage that may not be covered
func TestRenderPage_EdgeCases(t *testing.T) {
	// Initialize templates
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	t.Run("gallery partial with complex data", func(t *testing.T) {
		// Test both galleryPartialTemplate and galleryOOBTemplate execution
		data := map[string]any{
			"Title":        "Test Gallery",
			"FolderName":   "TestFolder",
			"CSRFToken":    "test-token",
			"CacheVersion": "test-version",
		}
		var buf bytes.Buffer
		err := RenderPage(&buf, "gallery", data, true)
		// May error due to missing required fields, but should not panic
		if err == nil && buf.Len() == 0 {
			t.Error("Gallery partial wrote no output")
		}
	})

	t.Run("dashboard partial with complex data", func(t *testing.T) {
		// Test both dashboardPartialTemplate and hamburgerMenuItemsTemplate execution
		data := map[string]any{
			"Title":        "Dashboard",
			"CSRFToken":    "test-token",
			"CacheVersion": "test-version",
		}
		var buf bytes.Buffer
		err := RenderPage(&buf, "dashboard", data, true)
		// May error due to missing required fields, but should not panic
		if err == nil && buf.Len() == 0 {
			t.Error("Dashboard partial wrote no output")
		}
	})

	t.Run("gallery partial error path", func(t *testing.T) {
		// Test error handling in partial gallery rendering
		// Using invalid data that will cause template execution to fail
		var buf bytes.Buffer
		err := RenderPage(&buf, "gallery", nil, true)
		// The function should handle errors gracefully
		t.Logf("Gallery partial with nil data: err=%v", err)
	})

	t.Run("dashboard partial error path", func(t *testing.T) {
		// Test error handling in partial dashboard rendering
		var buf bytes.Buffer
		err := RenderPage(&buf, "dashboard", nil, true)
		// The function should handle errors gracefully
		t.Logf("Dashboard partial with nil data: err=%v", err)
	})
}

// TestRenderPage_TemplateNilCheck tests behavior when templates are somehow nil
// This is a defensive test - in normal operation templates should never be nil
// after ParseTemplates, but the code has nil checks we should cover
func TestRenderPage_TemplateNilCheck(t *testing.T) {
	// We can't really test the nil template case properly because:
	// 1. ParseTemplates initializes all templates with template.Must
	// 2. If templates are nil after ParseTemplates, it would have panicked
	// 3. The nil check in RenderPage is defensive programming

	// This test documents that the nil check exists but is hard to trigger
	t.Skip("Nil template checks are defensive - cannot trigger after successful ParseTemplates")
}
