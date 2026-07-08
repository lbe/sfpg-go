package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/ui"
	"github.com/lbe/sfpg-go/internal/testutil"
	"github.com/lbe/sfpg-go/web"
)

func TestConfigHandlers_ConfigGet_Authenticated(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConfigHandlers_ConfigGet_LoadError(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}
}

func TestConfigHandlers_ConfigGet_UsernameFallback(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	mockSvc := &mockConfigServiceForConfig{
		getConfigValueFun: func(ctx context.Context, key string) (string, error) {
			return "", errors.New("username lookup failed")
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "admin") {
		t.Error("expected rendered output to contain default username 'admin'")
	}
}

func TestConfigHandlers_ConfigGet_WithCategory(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	ch := setupTestConfigHandlers(t, &mockConfigServiceForConfig{}, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config?category=security", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}
	// The config-modal template does not currently emit the category as a visible
	// marker; this test exercises the category branch in ConfigGet and confirms
	// the modal still renders successfully.
	if !strings.Contains(w.Body.String(), "Configuration") {
		t.Error("expected config modal to render")
	}
}

func TestConfigHandlers_ConfigGet_ThemesSorted(t *testing.T) {
	if err := ui.ParseTemplates(web.FS); err != nil {
		t.Fatalf("ParseTemplates failed: %v", err)
	}

	// Create config with unsorted themes
	cfg := config.DefaultConfig()
	cfg.Themes = []string{"dark", "light", "cupcake", "wireframe", "valentine"}

	mockSvc := &mockConfigServiceForConfig{
		loadFunc: func(ctx context.Context) (*config.Config, error) {
			return cfg, nil
		},
	}
	ch := setupTestConfigHandlers(t, mockSvc, &mockAuthServiceForConfig{})
	ch.SessionManager.(*mockSessionManagerAuth).authenticated = true

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	w := httptest.NewRecorder()

	ch.ConfigGet(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Parse response and verify themes are sorted
	doc, err := testutil.ParseHTML(w.Body)
	if err != nil {
		t.Fatalf("parse HTML: %v", err)
	}

	// Find the themes select element
	themesSelect := testutil.FindElementByID(doc, "")
	if themesSelect == nil {
		// The themes are rendered in a select element with name="themes"
		// We need to check the order of options in the rendered HTML
		bodyStr := w.Body.String()
		// Look for themes in order they should appear (light, dark first, then alphabetical)
		expectedOrder := []string{"light", "dark", "cupcake", "valentine", "wireframe"}

		// Find positions of each theme in the HTML
		positions := make(map[string]int)
		for _, theme := range expectedOrder {
			pos := strings.Index(bodyStr, fmt.Sprintf("value=\"%s\"", theme))
			if pos == -1 {
				t.Errorf("theme %q not found in response", theme)
				continue
			}
			positions[theme] = pos
		}

		// Verify order - each theme should appear before the next alphabetically
		for i := 0; i < len(expectedOrder)-1; i++ {
			curr := expectedOrder[i]
			next := expectedOrder[i+1]
			if positions[curr] > positions[next] {
				t.Errorf("themes not sorted alphabetically: %q (pos %d) should come before %q (pos %d)",
					curr, positions[curr], next, positions[next])
			}
		}
	}
}
