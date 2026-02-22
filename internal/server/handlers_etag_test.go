package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/lbe/sfpg-go/internal/testutil"
)

func TestConfigIncrementETag_Unauthenticated(t *testing.T) {
	app := CreateApp(t, false)
	h := app.configHandlers

	req := httptest.NewRequest("POST", "/config/increment-etag", nil)
	w := httptest.NewRecorder()

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestConfigIncrementETag_MissingCSRF(t *testing.T) {
	app := CreateApp(t, false)
	h := app.configHandlers

	req := httptest.NewRequest("POST", "/config/increment-etag", nil)
	w := httptest.NewRecorder()

	// Add authentication but no CSRF
	addAuthToRequest(t, h.SessionManager, req)

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Status = %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestConfigIncrementETag_Success(t *testing.T) {
	app := CreateApp(t, false)
	h := app.configHandlers
	ctx := context.Background()

	// Get current ETag
	cfg, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	originalETag := cfg.ETagVersion

	// Create authenticated request with CSRF
	formData := strings.NewReader("csrf_token=valid-token")
	req := httptest.NewRequest("POST", "/config/increment-etag", formData)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	addAuthToRequest(t, h.SessionManager, req)

	h.ConfigIncrementETag(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	// Parse HTML response
	doc, err := html.Parse(strings.NewReader(w.Body.String()))
	if err != nil {
		t.Fatalf("Parse HTML: %v", err)
	}

	// Verify response contains new ETag value
	etagElem := testutil.FindElementByID(doc, "config-etag-version")
	if etagElem == nil {
		t.Fatal("Element #config-etag-version not found in response")
	}

	newETag := testutil.GetAttr(etagElem, "value")
	if newETag == "" {
		newETag = testutil.GetTextContent(etagElem)
	}

	// Verify format
	pattern := `^\d{8}-\d{2}$`
	matched, _ := regexp.MatchString(pattern, newETag)
	if !matched {
		t.Errorf("New ETag %q does not match pattern", newETag)
	}

	// Verify it differs from original
	if newETag == originalETag {
		t.Errorf("ETag not incremented, still %q", originalETag)
	}

	// Verify database was updated
	reloaded, err := app.configService.Load(ctx)
	if err != nil {
		t.Fatalf("Reload config: %v", err)
	}
	if reloaded.ETagVersion != newETag {
		t.Errorf("Database ETag = %q, want %q", reloaded.ETagVersion, newETag)
	}
}
