package handlers

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

func TestConfigHandlers_ImportPreview_MissingYAML(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportCommit_Success(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	var updateConfigCalled bool
	mockSvc := &mockConfigServiceForConfig{}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	_ = updateConfigCalled
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	// UpdateConfigWithPrecedence handled by deps
	// ApplyConfig handled by deps
}

func TestConfigHandlers_ImportPreview_InvalidExtension(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("yaml", "config.txt")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write([]byte("site_name: Test")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_InvalidYAML(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader("yaml=listener-port:%20["))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if !strings.HasPrefix(body, "Invalid YAML content") {
		t.Errorf("expected Invalid YAML content error, got %s", body)
	}
}

func TestConfigHandlers_ImportCommit_MissingYAML(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportCommit_ImportError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=:"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != "Import failed" {
		t.Errorf("expected Import failed error, got %s", body)
	}
}

func TestConfigHandlers_ImportPreview_MissingFile(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportCommit_ParseFormError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("%zz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_MultipartParseError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", strings.NewReader("invalid"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=invalid")
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestConfigHandlers_ImportPreview_MultipartSuccess(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("yaml", "config.yaml")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err = part.Write([]byte("site_name: Test")); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/config/import/preview", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	ch.ImportConfigPreviewHandler(w, req)

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
	if got := testutil.GetTextContent(heading); got != "Import Configuration Preview" {
		t.Errorf("heading = %q, want %q", got, "Import Configuration Preview")
	}
	if testutil.FindElementByID(doc, "import-yaml-content") == nil {
		t.Fatal("missing #import-yaml-content")
	}

	// Verify modal-box element exists (import preview is rendered in a modal)
	modalBox := testutil.FindElementByClass(doc, "modal-box")
	if modalBox == nil {
		t.Error("response should contain modal-box element")
	}

	// Verify diff content headings exist
	h4Elements := testutil.FindAllElements(doc, func(n *html.Node) bool {
		return n.Type == html.ElementNode && n.Data == "h4"
	})
	if len(h4Elements) < 2 {
		t.Error("expected at least 2 h4 headings in preview modal")
	} else {
		foundCurrent := false
		foundImported := false
		for _, h4 := range h4Elements {
			if testutil.GetTextContent(h4) == "Current Configuration" {
				foundCurrent = true
			}
			if testutil.GetTextContent(h4) == "Imported Configuration" {
				foundImported = true
			}
		}
		if !foundCurrent {
			t.Error("response should contain 'Current Configuration' heading")
		}
		if !foundImported {
			t.Error("response should contain 'Imported Configuration' heading")
		}
	}
}

func TestConfigHandlers_ImportCommit_ApplyValidationError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		validateFunc: func(cfg *config.Config) error {
			return errors.New("invalid config")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != "Import failed" {
		t.Errorf("expected Import failed error, got %s", body)
	}
}

func TestConfigHandlers_ImportCommit_ApplyError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return config.DefaultConfig(), nil
		},
		saveFunc: func(ctx context.Context, cfg *config.Config) error {
			return errors.New("persist failed")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != "Import failed: unable to persist config" {
		t.Errorf("expected persist error, got %s", body)
	}
}

func TestConfigHandlers_ImportCommit_RestartRequired(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	current := config.DefaultConfig()
	current.ListenerPort = 8081
	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return current, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=listener-port: 9090"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	if testutil.FindElementByID(doc, "config-restart-badge") == nil {
		t.Error("expected restart-initiated alert #config-restart-badge")
	}
	success := testutil.FindElementByID(doc, "config-success-message")
	if success == nil {
		t.Fatal("missing #config-success-message in restart-required import response")
	}
	if got := testutil.GetAttr(success, "hx-swap-oob"); got != "outerHTML" {
		t.Errorf("expected #config-success-message hx-swap-oob=outerHTML, got %q", got)
	}
	successText := testutil.FindElementByTag(success, "span")
	if successText == nil {
		t.Fatal("missing success message text span")
	}
	if got := testutil.GetTextContent(successText); got != "Settings saved. Server restart required for changes to take effect." {
		t.Errorf("unexpected restart-required message: %q", got)
	}
}

func TestConfigModal_ImportButtonDoesNotSubmitForm(t *testing.T) {
	raw, err := web.FS.ReadFile("templates/config-modal.html.tmpl")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	doc, err := testutil.ParseHTML(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	fileInput := testutil.FindElementByID(doc, "import-file-input")
	if fileInput == nil {
		t.Fatal("missing #import-file-input")
	}
	importBtn := previousElementSibling(fileInput)
	if importBtn == nil || importBtn.Data != "button" {
		t.Fatal("expected Import button immediately before #import-file-input")
	}
	if got := testutil.GetAttr(importBtn, "type"); got != "button" {
		t.Errorf("Import button type = %q, want button", got)
	}
}

func TestConfigImportModal_CommitButtonTargetsSuccessAlert(t *testing.T) {
	raw, err := web.FS.ReadFile("templates/config-ui/config-import-modal.html.tmpl")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	doc, err := testutil.ParseHTML(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}
	commitBtn := testutil.FindElement(doc, func(n *html.Node) bool {
		if n.Type != html.ElementNode || n.Data != "button" {
			return false
		}
		return testutil.GetAttr(n, "hx-post") == "/config/import/commit"
	})
	if commitBtn == nil {
		t.Fatal("missing import commit button")
	}
	if got := testutil.GetAttr(commitBtn, "type"); got != "button" {
		t.Errorf("commit button type = %q, want button", got)
	}
	if got := testutil.GetAttr(commitBtn, "hx-target"); got != "#config-success-message" {
		t.Errorf("commit hx-target = %q, want #config-success-message", got)
	}
	if got := testutil.GetAttr(commitBtn, "hx-swap"); got != "outerHTML" {
		t.Errorf("commit hx-swap = %q, want outerHTML", got)
	}
}

func previousElementSibling(n *html.Node) *html.Node {
	for s := n.PrevSibling; s != nil; s = s.PrevSibling {
		if s.Type == html.ElementNode {
			return s
		}
	}
	return nil
}

func TestConfigHandlers_ImportCommit_LoadError(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return nil, errors.New("load error")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodPost, "/config/import/commit", strings.NewReader("yaml=site_name: Test"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	ch.ImportConfigCommitHandler(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
	body := strings.TrimSpace(w.Body.String())
	expected := "Internal Server Error"
	if body != expected {
		t.Errorf("expected %q error, got %s", expected, body)
	}
}
