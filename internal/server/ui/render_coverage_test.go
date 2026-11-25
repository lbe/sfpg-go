package ui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/web"
)

// TestRenderPage_CacheBatchLoadStarted tests rendering the cache-batch-load-started standalone page.
func TestRenderPage_CacheBatchLoadStarted(t *testing.T) {
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var buf bytes.Buffer
	err := RenderPage(&buf, "cache-batch-load-started", map[string]any{
		"AlertClass": "alert-info",
		"Message":    "batch load started",
	}, false)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("RenderPage wrote no output")
	}
}

// TestRenderPage_GalleryPartialExecuteError tests the gallery partial Execute error path.
func TestRenderPage_GalleryPartialExecuteError(t *testing.T) {
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	err := RenderPage(errorWriter{}, "gallery", nil, true)
	if err == nil {
		t.Fatal("Expected error for gallery partial with failing writer")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Errorf("Error should contain 'write failed', got: %v", err)
	}
}

// TestRenderPage_DashboardPartialExecuteError tests the dashboard partial Execute error path.
func TestRenderPage_DashboardPartialExecuteError(t *testing.T) {
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	err := RenderPage(errorWriter{}, "dashboard", nil, true)
	if err == nil {
		t.Fatal("Expected error for dashboard partial with failing writer")
	}
	if !strings.Contains(err.Error(), "write failed") {
		t.Errorf("Error should contain 'write failed', got: %v", err)
	}
}

// TestRenderPage_NilTemplateFallback tests the defensive nil-template fallback.
func TestRenderPage_NilTemplateFallback(t *testing.T) {
	if err := ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	origGallery := galleryTemplate
	galleryTemplate = nil
	defer func() { galleryTemplate = origGallery }()

	var buf bytes.Buffer
	err := RenderPage(&buf, "gallery", nil, false)
	if err == nil {
		t.Fatal("Expected error when gallery template is nil")
	}
	if !strings.Contains(err.Error(), "template not initialized") {
		t.Errorf("Error should contain 'template not initialized', got: %v", err)
	}
}
