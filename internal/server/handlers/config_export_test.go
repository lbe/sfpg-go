package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

func TestConfigHandlers_ExportConfigDownload_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func(_ context.Context) (string, error) {
			return "site_name: TestExport\n", nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config/export/download", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigDownloadHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Error("expected Content-Disposition header with attachment")
	}
}

func TestConfigHandlers_ExportConfigToFileHandler_Error(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func(_ context.Context) (string, error) {
			return "", errors.New("export error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/export/to-file", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigToFileHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ExportConfigDownload_Error(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func(_ context.Context) (string, error) {
			return "", errors.New("export error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config/export/download", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigDownloadHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ExportConfigToFileHandler_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		exportFunc: func(_ context.Context) (string, error) {
			return "site_name: TestExport\n", nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/export/to-file", nil)
	w := httptest.NewRecorder()

	ch.ExportConfigToFileHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	heading := testutil.FindElementByTag(doc, "h3")
	if heading == nil {
		t.Fatal("missing h3 heading")
	}
	if got := testutil.GetTextContent(heading); got != "Export Configuration" {
		t.Errorf("heading = %q, want %q", got, "Export Configuration")
	}
	pre := testutil.FindElementByTag(doc, "pre")
	if pre == nil {
		t.Fatal("missing pre element")
	}
	if got := testutil.GetTextContent(pre); !strings.Contains(got, "site_name: TestExport") {
		t.Errorf("expected YAML content, got %q", got)
	}
}
