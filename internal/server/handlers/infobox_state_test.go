package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/testutil"
)

// TestGalleryByID_IncludesInfoBoxStateScript verifies that the gallery page
// includes the Hyperscript behaviors necessary to persist and restore the info
// box state across page navigations (e.g., when clicking breadcrumbs).
func TestGalleryByID_IncludesInfoBoxStateScript(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	rendered := w.Body.String()

	doc, err := testutil.ParseHTML(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	// Verify the body installs the behavior that restores info box state
	body := testutil.FindElementByTag(doc, "body")
	if body == nil {
		t.Fatal("missing body element")
	}

	bodyScript := testutil.GetAttr(body, "_")
	if bodyScript == "" {
		t.Error("body element missing _ (hyperscript) attribute for state restoration")
	}

	if !contains(bodyScript, "BodyKeyHandler") {
		t.Error("body should install BodyKeyHandler behavior")
	}

	// The BodyKeyHandler behavior restores state from sessionStorage
	if !contains(rendered, "behavior BodyKeyHandler") {
		t.Error("rendered page should define BodyKeyHandler behavior")
	}
	if !containsAny(rendered, []string{"sessionStorage.getItem('infobox-visible')", "sessionStorage.getItem(\"infobox-visible\")"}) {
		t.Error("BodyKeyHandler behavior should check sessionStorage for 'infobox-visible' state")
	}

	// Find the info button and verify it installs the behavior that persists state
	infoBtn := testutil.FindElementByID(doc, "info-btn")
	if infoBtn == nil {
		t.Fatal("missing #info-btn element")
	}

	infoBtnScript := testutil.GetAttr(infoBtn, "_")
	if infoBtnScript == "" {
		t.Error("info-btn missing _ (hyperscript) attribute")
	}

	if !contains(infoBtnScript, "DesktopInfoButton") {
		t.Error("info-btn should install DesktopInfoButton behavior")
	}

	// The DesktopInfoButton behavior persists state to sessionStorage
	if !contains(rendered, "behavior DesktopInfoButton") {
		t.Error("rendered page should define DesktopInfoButton behavior")
	}
	if !containsAny(rendered, []string{"sessionStorage.setItem('infobox-visible'", "sessionStorage.setItem(\"infobox-visible\""}) {
		t.Error("DesktopInfoButton behavior should save 'infobox-visible' state to sessionStorage")
	}
}

// containsAny checks if s contains any of the substrings
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if contains(s, substr) {
			return true
		}
	}
	return false
}

// contains is a simple substring check
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
