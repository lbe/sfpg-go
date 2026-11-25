package ui

import (
	"bytes"
	"testing"

	"github.com/lbe/sfpg-go/web"
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
