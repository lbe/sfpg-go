package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lbe/sfpg-go/internal/testutil"
)

// TestGalleryByID_IncludesInfoBoxStateScript verifies that the gallery page
// includes the JavaScript necessary to persist and restore the info box state
// across page navigations (e.g., when clicking breadcrumbs).
func TestGalleryByID_IncludesInfoBoxStateScript(t *testing.T) {
	gh := setupTestGalleryHandlers(t, &fakeHandlerQueries{})

	req := httptest.NewRequest(http.MethodGet, "/gallery/1", nil)
	req.SetPathValue("id", "1")
	w := httptest.NewRecorder()

	gh.GalleryByID(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("failed to parse HTML: %v", err)
	}

	// Verify the body has the _= attribute with sessionStorage check
	body := testutil.FindElementByTag(doc, "body")
	if body == nil {
		t.Fatal("missing body element")
	}

	bodyScript := testutil.GetAttr(body, "_")
	if bodyScript == "" {
		t.Error("body element missing _ (hyperscript) attribute for state restoration")
	}

	// The script should check sessionStorage for info box state
	if !containsAny(bodyScript, []string{"sessionStorage", "infobox-visible"}) {
		t.Error("body init script should check sessionStorage for 'infobox-visible' state")
	}

	// Find the info button and verify it has script to persist state
	infoBtn := testutil.FindElementByID(doc, "info-btn")
	if infoBtn == nil {
		t.Fatal("missing #info-btn element")
	}

	infoBtnScript := testutil.GetAttr(infoBtn, "_")
	if infoBtnScript == "" {
		t.Error("info-btn missing _ (hyperscript) attribute")
	}

	// The info button script should save state to sessionStorage
	if !containsAny(infoBtnScript, []string{"sessionStorage", "infobox-visible"}) {
		t.Error("info-btn script should save 'infobox-visible' state to sessionStorage")
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
